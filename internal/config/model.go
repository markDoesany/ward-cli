package config

import "time"

type ProjectConfig struct {
	SchemaVersion int                          `yaml:"schema_version" json:"schema_version"`
	Project       ProjectMetadata              `yaml:"project" json:"project"`
	Environments  map[string]EnvironmentConfig `yaml:"environments" json:"environments"`
}

type ProjectMetadata struct {
	ID          string `yaml:"id" json:"id"`
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description" json:"description,omitempty"`
}

type EnvironmentConfig struct {
	Kind        string                          `yaml:"kind" json:"kind"`
	API         APIConfig                       `yaml:"api" json:"api,omitempty"`
	Database    *DatabaseConfig                 `yaml:"database" json:"database,omitempty"`
	Services    []ServiceConfig                 `yaml:"services" json:"services,omitempty"`
	Queries     map[string]QueryDefinition      `yaml:"queries" json:"queries,omitempty"`
	Logs        LogConfig                       `yaml:"logs" json:"logs,omitempty"`
	Diagnostics map[string]DiagnosticDefinition `yaml:"diagnostics" json:"diagnostics,omitempty"`
	Redaction   RedactionConfig                 `yaml:"redaction" json:"redaction,omitempty"`
	Limits      LimitsConfig                    `yaml:"limits" json:"limits,omitempty"`
}

type APIConfig struct {
	BaseURL    string         `yaml:"base_url" json:"base_url,omitempty"`
	BaseURLEnv string         `yaml:"base_url_env" json:"base_url_env,omitempty"`
	Auth       *AuthConfig    `yaml:"auth" json:"auth,omitempty"`
	Headers    []HeaderConfig `yaml:"headers" json:"headers,omitempty"`
	CACertPath string         `yaml:"ca_cert_path" json:"ca_cert_path,omitempty"`
}

type AuthConfig struct {
	BearerTokenEnv string `yaml:"bearer_token_env" json:"bearer_token_env"`
}

type HeaderConfig struct {
	Name     string `yaml:"name" json:"name"`
	Value    string `yaml:"value" json:"value,omitempty"`
	ValueEnv string `yaml:"value_env" json:"value_env,omitempty"`
}

type ServiceConfig struct {
	Name           string `yaml:"name" json:"name"`
	Type           string `yaml:"type" json:"type"`
	URL            string `yaml:"url" json:"url,omitempty"`
	Host           string `yaml:"host" json:"host,omitempty"`
	HostEnv        string `yaml:"host_env" json:"host_env,omitempty"`
	Port           int    `yaml:"port" json:"port,omitempty"`
	PortEnv        string `yaml:"port_env" json:"port_env,omitempty"`
	UseAuth        bool   `yaml:"use_auth" json:"use_auth,omitempty"`
	ExpectedStatus int    `yaml:"expected_status" json:"expected_status,omitempty"`
}

type DatabaseConfig struct {
	Enabled     *bool  `yaml:"enabled" json:"enabled,omitempty"`
	Driver      string `yaml:"driver" json:"driver"`
	Host        string `yaml:"host" json:"host,omitempty"`
	HostEnv     string `yaml:"host_env" json:"host_env,omitempty"`
	Port        int    `yaml:"port" json:"port,omitempty"`
	PortEnv     string `yaml:"port_env" json:"port_env,omitempty"`
	Database    string `yaml:"database" json:"database,omitempty"`
	DatabaseEnv string `yaml:"database_env" json:"database_env,omitempty"`
	Username    string `yaml:"username" json:"username,omitempty"`
	UsernameEnv string `yaml:"username_env" json:"username_env,omitempty"`
	PasswordEnv string `yaml:"password_env" json:"password_env"`
	TLS         string `yaml:"tls" json:"tls,omitempty"`
	CACertPath  string `yaml:"ca_cert_path" json:"ca_cert_path,omitempty"`
}

// IsEnabled preserves schema v1 behavior: an omitted enabled field means enabled.
func (c DatabaseConfig) IsEnabled() bool { return c.Enabled == nil || *c.Enabled }

type QueryDefinition struct {
	Description string           `yaml:"description" json:"description,omitempty"`
	Parameters  []QueryParameter `yaml:"parameters" json:"parameters,omitempty"`
	SQL         string           `yaml:"sql" json:"sql"`
}

type QueryParameter struct {
	Name string `yaml:"name" json:"name"`
	Type string `yaml:"type" json:"type"`
}

type LimitsConfig struct {
	TimeoutSeconds   int `yaml:"timeout_seconds" json:"timeout_seconds,omitempty"`
	MaxRows          int `yaml:"max_rows" json:"max_rows,omitempty"`
	MaxResponseBytes int `yaml:"max_response_bytes" json:"max_response_bytes,omitempty"`
}

type RedactionConfig struct {
	Fields   []string `yaml:"fields" json:"fields,omitempty"`
	Patterns []string `yaml:"patterns" json:"patterns,omitempty"`
}

type LogConfig struct {
	Sources []LogSourceConfig `yaml:"sources" json:"sources,omitempty"`
}

type LogSourceConfig struct {
	ID     string               `yaml:"id" json:"id"`
	Name   string               `yaml:"name" json:"name"`
	Type   string               `yaml:"type" json:"type"`
	Format string               `yaml:"format" json:"format"`
	Paths  []string             `yaml:"paths" json:"paths,omitempty"`
	HTTP   *HTTPLogSourceConfig `yaml:"http" json:"http,omitempty"`
	Fields LogFieldMapping      `yaml:"fields" json:"fields,omitempty"`
}

type HTTPLogSourceConfig struct {
	BaseURL    string            `yaml:"base_url" json:"base_url"`
	Endpoint   string            `yaml:"endpoint" json:"endpoint"`
	Auth       *AuthConfig       `yaml:"auth" json:"auth,omitempty"`
	Headers    []HeaderConfig    `yaml:"headers" json:"headers,omitempty"`
	CACertPath string            `yaml:"ca_cert_path" json:"ca_cert_path,omitempty"`
	Query      map[string]string `yaml:"query" json:"query,omitempty"`
}

type LogFieldMapping struct {
	Timestamp string `yaml:"timestamp" json:"timestamp,omitempty"`
	Level     string `yaml:"level" json:"level,omitempty"`
	Message   string `yaml:"message" json:"message,omitempty"`
	User      string `yaml:"user" json:"user,omitempty"`
	Reference string `yaml:"reference" json:"reference,omitempty"`
}

type DiagnosticDefinition struct {
	Description string                `yaml:"description" json:"description,omitempty"`
	Parameters  []QueryParameter      `yaml:"parameters" json:"parameters,omitempty"`
	Steps       []DiagnosticStep      `yaml:"steps" json:"steps"`
	Assertions  []DiagnosticAssertion `yaml:"assertions" json:"assertions,omitempty"`
}

type DiagnosticStep struct {
	ID              string            `yaml:"id" json:"id"`
	Type            string            `yaml:"type" json:"type"`
	ContinueOnError bool              `yaml:"continue_on_error" json:"continue_on_error,omitempty"`
	Service         string            `yaml:"service" json:"service,omitempty"`
	Path            string            `yaml:"path" json:"path,omitempty"`
	Query           string            `yaml:"query" json:"query,omitempty"`
	Arguments       []string          `yaml:"arguments" json:"arguments,omitempty"`
	Source          string            `yaml:"source" json:"source,omitempty"`
	Filters         map[string]string `yaml:"filters" json:"filters,omitempty"`
}

type DiagnosticAssertion struct {
	Step           string `yaml:"step" json:"step"`
	Selector       string `yaml:"selector" json:"selector"`
	Operator       string `yaml:"operator" json:"operator"`
	Value          any    `yaml:"value" json:"value,omitempty"`
	Severity       string `yaml:"severity" json:"severity"`
	Issue          string `yaml:"issue" json:"issue"`
	Evidence       string `yaml:"evidence" json:"evidence,omitempty"`
	Recommendation string `yaml:"recommendation" json:"recommendation,omitempty"`
}

const (
	SupportedSchemaVersion = 1
	DefaultTimeout         = 10 * time.Second
	DefaultMaxRows         = 1000
	DefaultMaxResponse     = 5 * 1024 * 1024
)
