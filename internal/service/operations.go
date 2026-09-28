package service

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ward/ward/internal/api"
	"github.com/ward/ward/internal/config"
	"github.com/ward/ward/internal/database"
	"github.com/ward/ward/internal/diagnostic"
	"github.com/ward/ward/internal/health"
	"github.com/ward/ward/internal/logs"
	"github.com/ward/ward/internal/security"
)

type Operations struct {
	Environment config.EnvironmentConfig
	ConfigPath  string
	Timeout     time.Duration
	MaxRows     int
	MaxBytes    int
}

func (o *Operations) Health(ctx context.Context, serviceName string) (any, error) {
	report, err := o.CheckHealth(ctx, serviceName)
	return report, err
}

func (o *Operations) CheckHealth(ctx context.Context, serviceName string) (health.Report, error) {
	env := o.Environment
	if serviceName != "" {
		services := []config.ServiceConfig{}
		for _, service := range env.Services {
			if strings.EqualFold(service.Name, serviceName) {
				services = append(services, service)
			}
		}
		if len(services) == 0 {
			return health.Report{}, fmt.Errorf("health service %q is not configured", serviceName)
		}
		env.Services = services
	}
	return health.Run(ctx, env, o.Timeout, o.maxBytes()), nil
}

func (o *Operations) APIGet(ctx context.Context, path string) (any, error) {
	return o.APIGetRequest(ctx, path, nil, nil)
}

func (o *Operations) APIGetRequest(ctx context.Context, path string, query url.Values, headers map[string]string) (api.Response, error) {
	client, err := api.NewClient(ctx, o.Environment, o.Timeout)
	if err != nil {
		return api.Response{}, err
	}
	for key, value := range headers {
		client.Headers[key] = value
	}
	return client.Get(ctx, path, query, o.maxBytes())
}

func (o *Operations) DatabaseStatus(ctx context.Context) error {
	if o.Environment.Database == nil {
		return fmt.Errorf("database configuration is not present")
	}
	if !o.Environment.Database.IsEnabled() {
		return fmt.Errorf("database profile is disabled")
	}
	runner, err := database.NewRunner(ctx, *o.Environment.Database, o.Environment.Kind, o.Timeout)
	if err != nil {
		return err
	}
	return runner.Close()
}

func (o *Operations) DatabaseQuery(ctx context.Context, queryID string, values []string) (any, error) {
	return o.RunDatabaseQuery(ctx, queryID, values)
}

func (o *Operations) RunDatabaseQuery(ctx context.Context, queryID string, values []string) (database.QueryResult, error) {
	if o.Environment.Database == nil {
		return database.QueryResult{}, fmt.Errorf("database configuration is not present")
	}
	if !o.Environment.Database.IsEnabled() {
		return database.QueryResult{}, fmt.Errorf("database profile is disabled")
	}
	definition, ok := o.Environment.Queries[queryID]
	if !ok {
		return database.QueryResult{}, fmt.Errorf("query %q is not configured", queryID)
	}
	args, err := database.ParseArguments(definition, values)
	if err != nil {
		return database.QueryResult{}, err
	}
	runner, err := database.NewRunner(ctx, *o.Environment.Database, o.Environment.Kind, o.Timeout)
	if err != nil {
		return database.QueryResult{}, err
	}
	defer runner.Close()
	result, err := runner.Query(ctx, definition, args, o.maxRows())
	if err != nil {
		return database.QueryResult{}, err
	}
	redactor, _ := security.NewRedactor(o.Environment.Redaction.Fields, o.Environment.Redaction.Patterns)
	return database.RedactResult(result, redactor), nil
}

func (o *Operations) SearchLogs(ctx context.Context, sourceIDs []string, filter logs.Filter) (logs.SearchResult, error) {
	baseDir := filepath.Dir(o.ConfigPath)
	return logs.Search(ctx, o.Environment, baseDir, sourceIDs, filter, o.Timeout)
}

func (o *Operations) LogSearch(ctx context.Context, source string, values map[string]string) (any, error) {
	filter, err := ParseLogFilter(values)
	if err != nil {
		return nil, err
	}
	sources := []string{}
	if source != "" {
		sources = append(sources, source)
	}
	return o.SearchLogs(ctx, sources, filter)
}

func (o *Operations) AddFileLogSource(environmentID string, source config.LogSourceConfig) error {
	if o.ConfigPath == "" {
		return fmt.Errorf("project configuration path is not available")
	}
	return config.AddFileLogSource(o.ConfigPath, environmentID, source)
}

func (o *Operations) RemoveLogSource(environmentID, sourceID string) error {
	if o.ConfigPath == "" {
		return fmt.Errorf("project configuration path is not available")
	}
	return config.RemoveLogSource(o.ConfigPath, environmentID, sourceID)
}

func (o *Operations) Diagnose(ctx context.Context, name string, parameters map[string]string) (diagnostic.Result, error) {
	definition, ok := o.Environment.Diagnostics[name]
	if !ok {
		return diagnostic.Result{}, fmt.Errorf("diagnostic workflow %q is not configured", name)
	}
	return diagnostic.Run(ctx, name, definition, parameters, o)
}

func ParseLogFilter(values map[string]string) (logs.Filter, error) {
	filter := logs.Filter{Limit: 500, Since: time.Now().UTC().Add(-30 * time.Minute)}
	if value := values["last"]; value != "" {
		duration, err := time.ParseDuration(value)
		if err != nil || duration <= 0 {
			return filter, fmt.Errorf("last must be a positive duration")
		}
		filter.Since = time.Now().UTC().Add(-duration)
	}
	if value := values["since"]; value != "" {
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			return filter, fmt.Errorf("since must be RFC3339")
		}
		filter.Since = parsed
	}
	if value := values["until"]; value != "" {
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			return filter, fmt.Errorf("until must be RFC3339")
		}
		filter.Until = parsed
	}
	filter.Level, filter.User, filter.Reference, filter.Search = values["level"], values["user"], values["reference"], values["search"]
	if value := values["limit"]; value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 10000 {
			return filter, fmt.Errorf("limit must be between 1 and 10000")
		}
		filter.Limit = parsed
	}
	return filter, nil
}

func (o *Operations) maxRows() int {
	if o.MaxRows > 0 {
		return o.MaxRows
	}
	return config.DefaultMaxRows
}
func (o *Operations) maxBytes() int {
	if o.MaxBytes > 0 {
		return o.MaxBytes
	}
	return config.DefaultMaxResponse
}
