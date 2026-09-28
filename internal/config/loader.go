package config

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/ward/ward/internal/security"
	"gopkg.in/yaml.v3"
)

var (
	ErrNoProjects = errors.New("no project configuration files found")
	forbiddenSQL  = regexp.MustCompile(`(?i)\b(insert|update|delete|drop|alter|truncate|create|grant|revoke|replace|load\s+data|call|into\s+(out|dump)file|for\s+update|lock\s+in\s+share\s+mode|get_lock|release_lock|sleep|benchmark|load_file)\b`)
	headerNameSQL = regexp.MustCompile("^[!#$%&'*+\\-.^_`|~0-9A-Za-z]+$")
	templateName  = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)
)

func DefaultConfigDir() (string, error) {
	if value := os.Getenv("WARD_CONFIG_DIR"); value != "" {
		return value, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find user config directory: %w", err)
	}
	return filepath.Join(base, "ward"), nil
}

func DefaultStateDir() (string, error) {
	if value := os.Getenv("WARD_STATE_DIR"); value != "" {
		return value, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find user state directory: %w", err)
	}
	return filepath.Join(base, "ward"), nil
}

func SamplePath() string { return filepath.Join("projects", "sample.yaml") }

func LoadFile(path string) (ProjectConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ProjectConfig{}, fmt.Errorf("read %s: %w", path, err)
	}
	var cfg ProjectConfig
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return ProjectConfig{}, fmt.Errorf("parse %s: %w", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return ProjectConfig{}, fmt.Errorf("parse %s: multiple YAML documents are not supported", path)
		}
		return ProjectConfig{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := Validate(cfg); err != nil {
		return ProjectConfig{}, fmt.Errorf("validate %s: %w", path, err)
	}
	return cfg, nil
}

func Validate(cfg ProjectConfig) error {
	if cfg.SchemaVersion != SupportedSchemaVersion {
		return fmt.Errorf("unsupported schema_version %d (expected %d)", cfg.SchemaVersion, SupportedSchemaVersion)
	}
	if cfg.Project.ID == "" || cfg.Project.Name == "" {
		return errors.New("project.id and project.name are required")
	}
	if len(cfg.Environments) == 0 {
		return errors.New("at least one environment is required")
	}
	for id, env := range cfg.Environments {
		if id == "" || !validKind(env.Kind) {
			return fmt.Errorf("environment %q must declare kind development, staging, or production", id)
		}
		redactor, err := security.NewRedactor(env.Redaction.Fields, env.Redaction.Patterns)
		if err != nil {
			return fmt.Errorf("environment %q redaction: %w", id, err)
		}
		if env.API.BaseURL != "" && env.API.BaseURLEnv != "" {
			return fmt.Errorf("environment %q API must set only one of base_url or base_url_env", id)
		}
		if strings.ContainsAny(env.API.BaseURLEnv, "\r\n") {
			return fmt.Errorf("environment %q api.base_url_env is invalid", id)
		}
		if env.API.BaseURL != "" {
			parsedURL, parseErr := url.Parse(env.API.BaseURL)
			if parseErr != nil || parsedURL.Host == "" || parsedURL.User != nil || parsedURL.RawQuery != "" || parsedURL.Fragment != "" || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
				return fmt.Errorf("environment %q api.base_url must use http or https", id)
			}
		}
		if env.API.Auth != nil && env.API.Auth.BearerTokenEnv == "" {
			return fmt.Errorf("environment %q API bearer_token_env is required", id)
		}
		for _, header := range env.API.Headers {
			if !validHeader(header) {
				return fmt.Errorf("environment %q has an invalid API header", id)
			}
			if redactor.IsSensitive(header.Name) && header.Value != "" {
				return fmt.Errorf("environment %q sensitive API header %q must use value_env", id, header.Name)
			}
		}
		if env.Database != nil {
			if strings.ToLower(env.Database.Driver) != "mysql" {
				return fmt.Errorf("environment %q uses unsupported database driver %q", id, env.Database.Driver)
			}
			if env.Database.IsEnabled() && ((env.Database.Host == "" && env.Database.HostEnv == "") || (env.Database.Database == "" && env.Database.DatabaseEnv == "") || (env.Database.Username == "" && env.Database.UsernameEnv == "") || env.Database.PasswordEnv == "") {
				return fmt.Errorf("environment %q database requires host, database, username, and password_env", id)
			}
			if env.Database.Port < 0 || env.Database.Port > 65535 {
				return fmt.Errorf("environment %q database port is invalid", id)
			}
			if strings.EqualFold(env.Kind, "production") && strings.EqualFold(env.Database.TLS, "false") {
				return fmt.Errorf("environment %q cannot disable MySQL TLS", id)
			}
		}
		for _, service := range env.Services {
			if service.Name == "" || service.Type == "" {
				return fmt.Errorf("environment %q has a service without name or type", id)
			}
			switch strings.ToLower(service.Type) {
			case "http":
				if env.API.BaseURL == "" && env.API.BaseURLEnv == "" || service.URL == "" || !strings.HasPrefix(service.URL, "/") {
					return fmt.Errorf("environment %q HTTP service %q requires url", id, service.Name)
				}
			case "tcp":
				if service.Host == "" && service.HostEnv == "" || service.Port == 0 && service.PortEnv == "" {
					return fmt.Errorf("environment %q TCP service %q requires host and port", id, service.Name)
				}
			case "mysql":
				if env.Database == nil {
					return fmt.Errorf("environment %q MySQL service %q requires database configuration", id, service.Name)
				}
			default:
				return fmt.Errorf("environment %q service %q uses unsupported type %q", id, service.Name, service.Type)
			}
			if service.Port < 0 || service.Port > 65535 {
				return fmt.Errorf("environment %q service %q has invalid port", id, service.Name)
			}
			if service.ExpectedStatus != 0 && (service.ExpectedStatus < 100 || service.ExpectedStatus > 599) {
				return fmt.Errorf("environment %q service %q has invalid expected_status", id, service.Name)
			}
		}
		for queryID, query := range env.Queries {
			sqlText := strings.TrimSpace(query.SQL)
			if queryID == "" || sqlText == "" {
				return fmt.Errorf("environment %q query %q requires sql", id, queryID)
			}
			lowerSQL := strings.ToLower(sqlText)
			trimmedSQL := strings.TrimSpace(strings.TrimSuffix(sqlText, ";"))
			if (!strings.HasPrefix(lowerSQL, "select") && !strings.HasPrefix(lowerSQL, "with")) || forbiddenSQL.MatchString(sqlText) || strings.Contains(trimmedSQL, ";") {
				return fmt.Errorf("environment %q query %q must be read-only SELECT or WITH SQL", id, queryID)
			}
			parameterNames := map[string]bool{}
			for _, param := range query.Parameters {
				if param.Name == "" || !validParameterType(param.Type) {
					return fmt.Errorf("environment %q query %q has invalid parameter", id, queryID)
				}
				if parameterNames[param.Name] {
					return fmt.Errorf("environment %q query %q has duplicate parameter %q", id, queryID, param.Name)
				}
				parameterNames[param.Name] = true
			}
		}
		if env.Limits.TimeoutSeconds < 0 || env.Limits.MaxRows < 0 || env.Limits.MaxResponseBytes < 0 {
			return fmt.Errorf("environment %q limits cannot be negative", id)
		}
		if err := validateLogs(id, env.Logs, redactor); err != nil {
			return err
		}
		if err := validateDiagnostics(id, env); err != nil {
			return err
		}
	}
	return nil
}

func validateLogs(environmentID string, logs LogConfig, redactor *security.Redactor) error {
	seen := map[string]bool{}
	for _, source := range logs.Sources {
		if source.ID == "" || source.Name == "" {
			return fmt.Errorf("environment %q log source requires id and name", environmentID)
		}
		if seen[source.ID] {
			return fmt.Errorf("environment %q has duplicate log source %q", environmentID, source.ID)
		}
		seen[source.ID] = true
		if source.Format != "jsonl" && source.Format != "text" {
			return fmt.Errorf("environment %q log source %q format must be jsonl or text", environmentID, source.ID)
		}
		switch source.Type {
		case "file":
			if len(source.Paths) == 0 || source.HTTP != nil {
				return fmt.Errorf("environment %q file log source %q requires paths only", environmentID, source.ID)
			}
		case "http":
			if source.HTTP == nil || len(source.Paths) != 0 {
				return fmt.Errorf("environment %q HTTP log source %q requires http configuration only", environmentID, source.ID)
			}
			parsed, err := url.Parse(source.HTTP.BaseURL)
			if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || !strings.HasPrefix(source.HTTP.Endpoint, "/") {
				return fmt.Errorf("environment %q HTTP log source %q has invalid base_url or endpoint", environmentID, source.ID)
			}
			if source.HTTP.Auth != nil && source.HTTP.Auth.BearerTokenEnv == "" {
				return fmt.Errorf("environment %q HTTP log source %q auth requires bearer_token_env", environmentID, source.ID)
			}
			for _, header := range source.HTTP.Headers {
				if !validHeader(header) {
					return fmt.Errorf("environment %q HTTP log source %q has invalid header", environmentID, source.ID)
				}
				if redactor.IsSensitive(header.Name) && header.Value != "" {
					return fmt.Errorf("environment %q sensitive log header %q must use value_env", environmentID, header.Name)
				}
			}
			for logical, remote := range source.HTTP.Query {
				if !oneOf(logical, "since", "until", "level", "user", "reference", "search", "limit") || strings.TrimSpace(remote) == "" {
					return fmt.Errorf("environment %q HTTP log source %q has invalid query mapping %q", environmentID, source.ID, logical)
				}
			}
		default:
			return fmt.Errorf("environment %q log source %q has unsupported type %q", environmentID, source.ID, source.Type)
		}
	}
	return nil
}

func validateDiagnostics(environmentID string, env EnvironmentConfig) error {
	sources := map[string]bool{}
	for _, source := range env.Logs.Sources {
		sources[source.ID] = true
	}
	for name, workflow := range env.Diagnostics {
		if name == "" || len(workflow.Steps) == 0 {
			return fmt.Errorf("environment %q diagnostic %q requires steps", environmentID, name)
		}
		parameterNames := map[string]bool{}
		for _, parameter := range workflow.Parameters {
			if parameter.Name == "" || !validParameterType(parameter.Type) || parameterNames[parameter.Name] {
				return fmt.Errorf("environment %q diagnostic %q has an invalid or duplicate parameter", environmentID, name)
			}
			parameterNames[parameter.Name] = true
		}
		validateTemplate := func(value, label string) error {
			matches := templateName.FindAllStringSubmatch(value, -1)
			if strings.Count(value, "${") != len(matches) {
				return fmt.Errorf("environment %q diagnostic %q has malformed template in %s", environmentID, name, label)
			}
			for _, match := range matches {
				if !parameterNames[match[1]] {
					return fmt.Errorf("environment %q diagnostic %q references unknown parameter %q in %s", environmentID, name, match[1], label)
				}
			}
			return nil
		}
		stepIDs := map[string]bool{}
		for _, step := range workflow.Steps {
			if step.ID == "" || stepIDs[step.ID] {
				return fmt.Errorf("environment %q diagnostic %q has missing or duplicate step id", environmentID, name)
			}
			stepIDs[step.ID] = true
			for label, value := range map[string]string{"service": step.Service, "path": step.Path} {
				if err := validateTemplate(value, "step "+step.ID+" "+label); err != nil {
					return err
				}
			}
			for index, value := range step.Arguments {
				if err := validateTemplate(value, fmt.Sprintf("step %s argument %d", step.ID, index)); err != nil {
					return err
				}
			}
			for key, value := range step.Filters {
				if err := validateTemplate(value, "step "+step.ID+" filter "+key); err != nil {
					return err
				}
			}
			switch step.Type {
			case "health":
			case "api":
				if step.Path == "" || !strings.HasPrefix(step.Path, "/") {
					return fmt.Errorf("environment %q diagnostic %q API step %q requires relative path", environmentID, name, step.ID)
				}
			case "database":
				query, ok := env.Queries[step.Query]
				if !ok {
					return fmt.Errorf("environment %q diagnostic %q step %q references unknown query %q", environmentID, name, step.ID, step.Query)
				}
				if len(step.Arguments) != len(query.Parameters) {
					return fmt.Errorf("environment %q diagnostic %q step %q requires %d database arguments", environmentID, name, step.ID, len(query.Parameters))
				}
			case "logs":
				if !sources[step.Source] {
					return fmt.Errorf("environment %q diagnostic %q step %q references unknown log source %q", environmentID, name, step.ID, step.Source)
				}
			default:
				return fmt.Errorf("environment %q diagnostic %q step %q has unsupported type %q", environmentID, name, step.ID, step.Type)
			}
		}
		for _, assertion := range workflow.Assertions {
			if !stepIDs[assertion.Step] {
				return fmt.Errorf("environment %q diagnostic %q assertion references unknown step %q", environmentID, name, assertion.Step)
			}
			if assertion.Selector != "" && !validSelector(assertion.Selector) {
				return fmt.Errorf("environment %q diagnostic %q has invalid assertion selector %q", environmentID, name, assertion.Selector)
			}
			if !oneOf(assertion.Operator, "exists", "equals", "contains", "gt", "gte", "lt", "lte") {
				return fmt.Errorf("environment %q diagnostic %q has invalid assertion operator %q", environmentID, name, assertion.Operator)
			}
			if !oneOf(assertion.Severity, "info", "warning", "critical") || assertion.Issue == "" {
				return fmt.Errorf("environment %q diagnostic %q assertion requires severity and issue", environmentID, name)
			}
			for label, value := range map[string]string{"issue": assertion.Issue, "evidence": assertion.Evidence, "recommendation": assertion.Recommendation} {
				if err := validateTemplate(value, "assertion "+assertion.Step+" "+label); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func validHeader(header HeaderConfig) bool {
	if header.Name == "" || !headerNameSQL.MatchString(header.Name) || (header.Value == "" && header.ValueEnv == "") || (header.Value != "" && header.ValueEnv != "") {
		return false
	}
	return !strings.ContainsAny(header.Value, "\r\n") && !strings.ContainsAny(header.ValueEnv, "\r\n")
}

func validSelector(value string) bool {
	for _, part := range strings.Split(value, ".") {
		if part == "" {
			return false
		}
		for _, character := range part {
			if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') && (character < '0' || character > '9') && character != '_' && character != '-' {
				return false
			}
		}
	}
	return true
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func Discover(dir string) (map[string]ProjectConfig, map[string]string, error) {
	projectsDir := filepath.Join(dir, "projects")
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil, ErrNoProjects
		}
		return nil, nil, fmt.Errorf("read projects directory: %w", err)
	}
	files := make([]string, 0)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext == ".yaml" || ext == ".yml" {
			files = append(files, filepath.Join(projectsDir, entry.Name()))
		}
	}
	sort.Strings(files)
	if len(files) == 0 {
		return nil, nil, ErrNoProjects
	}
	projects := make(map[string]ProjectConfig, len(files))
	paths := make(map[string]string, len(files))
	for _, path := range files {
		cfg, err := LoadFile(path)
		if err != nil {
			return nil, nil, err
		}
		if _, exists := projects[cfg.Project.ID]; exists {
			return nil, nil, fmt.Errorf("duplicate project id %q", cfg.Project.ID)
		}
		projects[cfg.Project.ID] = cfg
		paths[cfg.Project.ID] = path
	}
	return projects, paths, nil
}

func validKind(value string) bool {
	switch strings.ToLower(value) {
	case "development", "staging", "production":
		return true
	default:
		return false
	}
}

func validParameterType(value string) bool {
	switch strings.ToLower(value) {
	case "string", "int", "int64", "bool", "time":
		return true
	default:
		return false
	}
}
