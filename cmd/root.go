package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/ward/ward/internal/app"
	"github.com/ward/ward/internal/branding"
	"github.com/ward/ward/internal/config"
	"github.com/ward/ward/internal/logs"
	"github.com/ward/ward/internal/output"
	"github.com/ward/ward/internal/service"
	"github.com/ward/ward/internal/state"
	"github.com/ward/ward/internal/tui"
	"golang.org/x/term"
)

var (
	version   = "0.2.0"
	commit    = "dev"
	buildDate = "unknown"
)

func Execute(args []string) error {
	a := app.New(app.Options{}, os.Stdout, os.Stderr)
	root := newRoot(a)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	root.SetContext(ctx)
	root.SetArgs(args)
	err := root.Execute()
	if err != nil && !a.EmittedError {
		command := a.CurrentCommand
		if command == "" {
			command = root.CommandPath()
		}
		a.EmitError(command, err)
	}
	return err
}

func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var typed *output.Error
	if errors.As(err, &typed) {
		return typed.Exit
	}
	return 1
}

func newRoot(a *app.App) *cobra.Command {
	root := &cobra.Command{
		Use:           "ward",
		Short:         "WARD observer support system",
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
				return cmd.Help()
			}
			if err := a.LoadProjects(); err != nil {
				return err
			}
			return tui.Run(a)
		},
	}
	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if err := a.Prepare(); err != nil {
			return err
		}
		a.BeginCommand(cmd.CommandPath())
		return nil
	}
	flags := root.PersistentFlags()
	flags.StringVar(&a.Options.ConfigDir, "config-dir", "", "WARD config directory")
	flags.StringVar(&a.Options.StateDir, "state-dir", "", "WARD state directory")
	flags.StringVarP(&a.Options.Project, "project", "p", "", "project ID")
	flags.StringVarP(&a.Options.Env, "env", "e", "", "environment ID")
	flags.BoolVar(&a.Options.JSON, "json", false, "emit JSON")
	flags.BoolVarP(&a.Options.Quiet, "quiet", "q", false, "suppress successful output")
	flags.BoolVarP(&a.Options.Verbose, "verbose", "v", false, "show verbose diagnostics")
	flags.IntVar(&a.Options.Timeout, "timeout", 0, "operation timeout in seconds")
	flags.IntVar(&a.Options.MaxRows, "max-rows", 0, "maximum database rows")
	flags.IntVar(&a.Options.MaxBytes, "max-bytes", 0, "maximum API response bytes")
	root.AddCommand(projectCommand(a), environmentCommand(a), healthCommand(a), apiCommand(a), databaseCommand(a), logsCommand(a), diagnoseCommand(a), configCommand(a), doctorCommand(a), versionCommand(a), uiCommand(a))
	return root
}

func projectCommand(a *app.App) *cobra.Command {
	root := &cobra.Command{Use: "project", Short: "Manage projects"}
	root.AddCommand(
		&cobra.Command{Use: "list", Short: "List available projects", RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.LoadProjects(); err != nil {
				return err
			}
			ids := make([]string, 0, len(a.Manager.Projects))
			for id := range a.Manager.Projects {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			data := make([]map[string]string, 0, len(ids))
			for _, id := range ids {
				cfg := a.Manager.Projects[id]
				data = append(data, map[string]string{"id": id, "name": cfg.Project.Name, "description": cfg.Project.Description})
			}
			a.Emit("project list", "", "", "", data, func(w io.Writer) {
				fmt.Fprintln(w, "AVAILABLE PROJECTS")
				for _, item := range data {
					marker := " "
					if item["id"] == a.Loaded.Project {
						marker = "●"
					}
					fmt.Fprintf(w, "%s %s\n", marker, item["name"])
					fmt.Fprintf(w, "  ID: %s\n", item["id"])
					if item["description"] != "" {
						fmt.Fprintf(w, "  %s\n", item["description"])
					}
				}
			})
			return nil
		}},
		&cobra.Command{Use: "use <id>", Short: "Select a project", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.LoadProjects(); err != nil {
				return err
			}
			cfg, err := a.Manager.Get(args[0])
			if err != nil {
				return output.ConfigError(err.Error())
			}
			envID := ""
			if args[0] == a.Loaded.Project {
				envID = a.Loaded.Environment
				if _, ok := cfg.Environments[envID]; !ok {
					envID = ""
				}
			}
			if err := a.SaveSelection(args[0], envID); err != nil {
				return err
			}
			a.Emit("project use", args[0], envID, "", map[string]string{"project": args[0], "environment": envID}, func(w io.Writer) {
				envLabel := strings.ToUpper(envID)
				if envLabel == "" {
					envLabel = "NOT SELECTED (run `ward env use <id>`)"
				}
				fmt.Fprintf(w, "Active project changed.\n\nProject: %s\nEnvironment: %s\n", cfg.Project.Name, envLabel)
			})
			return nil
		}},
		&cobra.Command{Use: "info", Short: "Show the active project", RunE: func(cmd *cobra.Command, args []string) error {
			cfg, id, err := a.RequireProject()
			if err != nil {
				return err
			}
			envID := ""
			if a.Options.Env != "" {
				envID = a.Options.Env
			} else if value := os.Getenv("WARD_ENV"); value != "" {
				envID = value
			} else if id == a.Loaded.Project {
				envID = a.Loaded.Environment
			}
			kind := ""
			if env, ok := cfg.Environments[envID]; ok {
				kind = env.Kind
			} else if envID != "" {
				return output.ConfigError(fmt.Sprintf("environment %q not found in project %q", envID, id))
			}
			data := map[string]any{"id": id, "name": cfg.Project.Name, "description": cfg.Project.Description, "environment": envID, "kind": kind}
			a.Emit("project info", id, envID, "", data, func(w io.Writer) {
				environmentLabel := "NOT SELECTED"
				if envID != "" {
					environmentLabel = strings.ToUpper(envID) + " [" + strings.ToUpper(kind) + "]"
				}
				fmt.Fprintf(w, "Project: %s (%s)\nEnvironment: %s\n", cfg.Project.Name, id, environmentLabel)
			})
			return nil
		}},
	)
	return root
}

func environmentCommand(a *app.App) *cobra.Command {
	root := &cobra.Command{Use: "env", Short: "Manage environments"}
	root.AddCommand(
		&cobra.Command{Use: "list", Short: "List project environments", RunE: func(cmd *cobra.Command, args []string) error {
			cfg, id, err := a.RequireProject()
			if err != nil {
				return err
			}
			ids := app.SortedEnvironmentIDs(cfg)
			data := make([]map[string]string, 0, len(ids))
			for _, envID := range ids {
				data = append(data, map[string]string{"id": envID, "kind": cfg.Environments[envID].Kind})
			}
			a.Emit("env list", id, "", "", data, func(w io.Writer) {
				fmt.Fprintf(w, "ENVIRONMENTS FOR %s\n", cfg.Project.Name)
				for _, item := range data {
					marker := " "
					if item["id"] == a.Loaded.Environment {
						marker = "●"
					}
					fmt.Fprintf(w, "%s %-16s %s\n", marker, item["id"], strings.ToUpper(item["kind"]))
				}
			})
			return nil
		}},
		&cobra.Command{Use: "use <id>", Short: "Select an environment", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			cfg, projectID, err := a.RequireProject()
			if err != nil {
				return err
			}
			if _, ok := cfg.Environments[args[0]]; !ok {
				return output.ConfigError(fmt.Sprintf("environment %q not found in project %q", args[0], projectID))
			}
			if err := a.SaveSelection(projectID, args[0]); err != nil {
				return err
			}
			a.Emit("env use", projectID, args[0], "", map[string]string{"project": projectID, "environment": args[0]}, func(w io.Writer) {
				fmt.Fprintf(w, "Active environment changed.\n\nProject: %s\nEnvironment: %s\n", cfg.Project.Name, strings.ToUpper(args[0]))
			})
			return nil
		}},
	)
	return root
}

func healthCommand(a *app.App) *cobra.Command {
	return &cobra.Command{Use: "health", Short: "Check configured service health", RunE: func(cmd *cobra.Command, args []string) error {
		cfg, projectID, envID, env, err := a.RequireEnvironment()
		if err != nil {
			return err
		}
		report, err := a.Operations(projectID, env).CheckHealth(cmd.Context(), "")
		if err != nil {
			return output.OperationalError("health check failed", err.Error())
		}
		var failure error
		if report.Status != "HEALTHY" {
			failure = output.OperationalError("overall health status is " + report.Status)
		}
		a.EmitOutcome("health", projectID, envID, report.Status, report, failure, func(w io.Writer) {
			fmt.Fprintf(w, "%s  %s  %s\n\nSYSTEM HEALTH\n\n", branding.Name, cfg.Project.Name, strings.ToUpper(envID))
			for _, item := range report.Results {
				symbol := "✓"
				if item.Status == "DEGRADED" || item.Status == "UNKNOWN" {
					symbol = "?"
				}
				if item.Status == "UNHEALTHY" {
					symbol = "✗"
				}
				fmt.Fprintf(w, "%-24s %s %-12s %d ms  %s\n", item.Name, symbol, item.Status, item.DurationMS, item.Message)
			}
			fmt.Fprintf(w, "\nOverall Status: %s\n", report.Status)
		})
		return failure
	}}
}

func apiCommand(a *app.App) *cobra.Command {
	root := &cobra.Command{Use: "api", Short: "Inspect an API"}
	var headerValues []string
	var queryValues []string
	get := &cobra.Command{Use: "GET <path>", Short: "Perform a read-only API GET", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		_, projectID, envID, env, err := a.RequireEnvironment()
		if err != nil {
			return err
		}
		headers := map[string]string{}
		for _, raw := range headerValues {
			key, value, ok := strings.Cut(raw, "=")
			if !ok || key == "" {
				return output.UsageError("--header values must use name=value")
			}
			headers[key] = value
		}
		query, err := parseKeyValues(queryValues)
		if err != nil {
			return output.UsageError(err.Error())
		}
		response, err := a.Operations(projectID, env).APIGetRequest(cmd.Context(), args[0], query, headers)
		if err != nil {
			return output.OperationalError("API request failed", err.Error())
		}
		var failure error
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			failure = output.OperationalError("API returned " + response.Status)
		}
		a.EmitOutcome("api GET", projectID, envID, "", response, failure, func(w io.Writer) {
			fmt.Fprintf(w, "REQUEST\n\nGET %s\n\nRESPONSE\n\nHTTP Status: %s\nDuration: %d ms\nContent-Type: %s\n\n%s\n", args[0], response.Status, response.DurationMS, response.ContentType, response.Body)
		})
		return failure
	}}
	get.Flags().StringArrayVar(&headerValues, "header", nil, "request header name=value")
	get.Flags().StringArrayVar(&queryValues, "query", nil, "query parameter name=value")
	root.AddCommand(get)
	return root
}

func parseKeyValues(values []string) (url.Values, error) {
	result := url.Values{}
	for _, raw := range values {
		key, value, ok := strings.Cut(raw, "=")
		if !ok || key == "" {
			return nil, fmt.Errorf("values must use name=value")
		}
		result.Add(key, value)
	}
	return result, nil
}

func databaseCommand(a *app.App) *cobra.Command {
	root := &cobra.Command{Use: "db", Short: "Inspect the configured database"}
	root.AddCommand(
		&cobra.Command{Use: "status", Short: "Check database connectivity", RunE: func(cmd *cobra.Command, args []string) error {
			cfg, projectID, envID, env, err := a.RequireEnvironment()
			if err != nil {
				return err
			}
			if env.Database == nil {
				return output.ConfigError("database configuration is not present")
			}
			if err := a.Operations(projectID, env).DatabaseStatus(cmd.Context()); err != nil {
				return output.OperationalError("DATABASE CONNECTION FAILED", err.Error())
			}
			data := map[string]string{"status": "connected", "driver": env.Database.Driver}
			a.Emit("db status", projectID, envID, "HEALTHY", data, func(w io.Writer) {
				fmt.Fprintf(w, "DATABASE\n\nDriver: %s\nStatus: Connected\nProject: %s\nEnvironment: %s\n", env.Database.Driver, cfg.Project.Name, strings.ToUpper(envID))
			})
			return nil
		}},
		&cobra.Command{Use: "query <query-id> [parameters...]", Short: "Run a named read-only query", Args: cobra.MinimumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			_, projectID, envID, env, err := a.RequireEnvironment()
			if err != nil {
				return err
			}
			if env.Database == nil {
				return output.ConfigError("database configuration is not present")
			}
			result, err := a.Operations(projectID, env).RunDatabaseQuery(cmd.Context(), args[0], args[1:])
			if err != nil {
				return output.OperationalError("database query failed", err.Error())
			}
			a.Emit("db query", projectID, envID, "", result, func(w io.Writer) {
				fmt.Fprintf(w, "QUERY: %s\n\n", args[0])
				rows := [][]string{append([]string(nil), result.Columns...)}
				for _, row := range result.Rows {
					values := make([]string, len(result.Columns))
					for i, column := range result.Columns {
						values[i] = fmt.Sprint(row[column])
					}
					rows = append(rows, values)
				}
				output.WriteTable(w, rows)
				if result.Truncated {
					fmt.Fprintf(w, "\nResult truncated at %d rows.\n", a.MaxRows(env))
				}
			})
			return nil
		}},
	)
	return root
}

type logOptions struct {
	sources                                            []string
	last, since, until, level, user, reference, search string
	limit                                              int
	follow                                             bool
}

func logsCommand(a *app.App) *cobra.Command {
	options := &logOptions{last: "30m", limit: 500}
	root := &cobra.Command{Use: "logs", Short: "Search configured log sources"}
	root.RunE = func(cmd *cobra.Command, args []string) error {
		if options.follow && a.Options.JSON {
			return output.UsageError("--follow cannot be combined with --json")
		}
		result, projectID, envID, err := executeLogSearch(cmd, a, options)
		if err != nil {
			return err
		}
		a.Emit("logs", projectID, envID, "", result, func(w io.Writer) { writeLogResult(w, result) })
		if options.follow {
			_, _, _, env, err := a.RequireEnvironment()
			if err != nil {
				return err
			}
			filter, err := logFilter(options)
			if err != nil {
				return output.UsageError(err.Error())
			}
			if err := logs.Follow(cmd.Context(), env, filepath.Dir(a.Manager.Paths[projectID]), options.sources, filter, func(entry logs.Entry) error {
				if !a.Options.Quiet {
					writeLogEntry(a.Stdout, entry)
				}
				return nil
			}); err != nil && !errors.Is(err, context.Canceled) {
				return output.OperationalError("log follow failed", err.Error())
			}
		}
		return nil
	}
	flags := root.PersistentFlags()
	flags.StringSliceVar(&options.sources, "source", nil, "configured source ID (repeat or comma-separate)")
	flags.StringVar(&options.last, "last", "30m", "relative search window")
	flags.StringVar(&options.since, "since", "", "start time in RFC3339")
	flags.StringVar(&options.until, "until", "", "end time in RFC3339")
	flags.StringVar(&options.level, "level", "", "log level")
	flags.StringVar(&options.user, "user", "", "user identifier")
	flags.StringVar(&options.reference, "ref", "", "reference identifier")
	flags.StringVar(&options.search, "search", "", "case-insensitive message search")
	flags.IntVar(&options.limit, "limit", 500, "maximum entries (1-10000)")
	flags.BoolVar(&options.follow, "follow", false, "follow new entries from local file sources")
	root.AddCommand(logSourcesCommand(a), logAddCommand(a), logRemoveCommand(a))
	var outputPath string
	var force bool
	export := &cobra.Command{Use: "export", Short: "Export sanitized results as JSONL", RunE: func(cmd *cobra.Command, args []string) error {
		if options.follow {
			return output.UsageError("--follow cannot be combined with logs export")
		}
		if outputPath == "" {
			return output.UsageError("--output is required")
		}
		result, projectID, envID, err := executeLogSearch(cmd, a, options)
		if err != nil {
			return err
		}
		if err := logs.ExportJSONL(outputPath, result, force); err != nil {
			return output.OperationalError("log export failed", err.Error())
		}
		a.Emit("logs export", projectID, envID, "", map[string]any{"path": outputPath, "entries": result.Count}, func(w io.Writer) {
			fmt.Fprintf(w, "Exported %d sanitized log entries to %s\n", result.Count, outputPath)
		})
		return nil
	}}
	export.Flags().StringVarP(&outputPath, "output", "o", "", "output JSONL path")
	export.Flags().BoolVar(&force, "force", false, "overwrite an existing output file")
	root.AddCommand(export)
	return root
}

func writeLogEntry(w io.Writer, entry logs.Entry) {
	timestamp := "-"
	if entry.Timestamp != nil {
		timestamp = entry.Timestamp.Local().Format("2006-01-02 15:04:05")
	}
	fmt.Fprintf(w, "%s %-7s %-14s %s\n", timestamp, strings.ToUpper(entry.Level), entry.Source, entry.Message)
}

func writeLogResult(w io.Writer, result logs.SearchResult) {
	fmt.Fprintf(w, "LOG RESULTS (%d)\n\n", result.Count)
	for _, entry := range result.Entries {
		writeLogEntry(w, entry)
	}
	for source, message := range result.Errors {
		fmt.Fprintf(w, "\nSource %s: %s\n", source, message)
	}
}

func logFilter(options *logOptions) (logs.Filter, error) {
	values := map[string]string{"last": options.last, "since": options.since, "until": options.until, "level": options.level, "user": options.user, "reference": options.reference, "search": options.search, "limit": strconv.Itoa(options.limit)}
	if options.since != "" {
		delete(values, "last")
	}
	return service.ParseLogFilter(values)
}

func logSourceEnvironment(a *app.App) (config.ProjectConfig, string, string, config.EnvironmentConfig, error) {
	return a.RequireEnvironment()
}

func logSourcesCommand(a *app.App) *cobra.Command {
	return &cobra.Command{Use: "sources", Short: "List configured log sources", RunE: func(cmd *cobra.Command, args []string) error {
		cfg, projectID, envID, env, err := logSourceEnvironment(a)
		if err != nil {
			return err
		}
		data := make([]map[string]any, 0, len(env.Logs.Sources))
		for _, source := range env.Logs.Sources {
			data = append(data, map[string]any{"id": source.ID, "name": source.Name, "type": source.Type, "format": source.Format, "paths": source.Paths})
		}
		a.Emit("logs sources", projectID, envID, "", data, func(w io.Writer) {
			fmt.Fprintf(w, "LOG SOURCES FOR %s / %s\n\n", cfg.Project.Name, envID)
			if len(data) == 0 {
				fmt.Fprintln(w, "No log sources configured.")
				return
			}
			for _, source := range data {
				fmt.Fprintf(w, "%-18s %-24s %-6s %s\n", source["id"], source["name"], source["format"], strings.Join(source["paths"].([]string), ", "))
			}
		})
		return nil
	}}
}

func logAddCommand(a *app.App) *cobra.Command {
	var name, format string
	var paths []string
	var yes bool
	command := &cobra.Command{Use: "add <id>", Short: "Register a local file or glob as a log source", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		_, projectID, envID, env, err := logSourceEnvironment(a)
		if err != nil {
			return err
		}
		if strings.EqualFold(env.Kind, "production") && !yes {
			return output.UsageError("adding a production log source requires --yes")
		}
		if len(paths) == 0 {
			return output.UsageError("at least one --path is required")
		}
		source := config.LogSourceConfig{ID: args[0], Name: logValueOr(name, args[0]), Type: "file", Format: logValueOr(format, "text"), Paths: paths}
		if err := a.Operations(projectID, env).AddFileLogSource(envID, source); err != nil {
			return output.ConfigError(err.Error())
		}
		a.Emit("logs add", projectID, envID, "", source, func(w io.Writer) { fmt.Fprintf(w, "Added log source %q to %s.\n", source.ID, envID) })
		return nil
	}}
	command.Flags().StringVar(&name, "name", "", "display name")
	command.Flags().StringArrayVarP(&paths, "path", "f", nil, "local file path or glob (repeatable)")
	command.Flags().StringVar(&format, "format", "text", "log format: text or jsonl")
	command.Flags().BoolVar(&yes, "yes", false, "confirm a production configuration edit")
	return command
}

func logValueOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func logRemoveCommand(a *app.App) *cobra.Command {
	var yes bool
	command := &cobra.Command{Use: "remove <id>", Short: "Remove a configured log source", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		_, projectID, envID, env, err := logSourceEnvironment(a)
		if err != nil {
			return err
		}
		if strings.EqualFold(env.Kind, "production") && !yes {
			return output.UsageError("removing a production log source requires --yes")
		}
		if err := a.Operations(projectID, env).RemoveLogSource(envID, args[0]); err != nil {
			return output.ConfigError(err.Error())
		}
		a.Emit("logs remove", projectID, envID, "", map[string]string{"id": args[0]}, func(w io.Writer) { fmt.Fprintf(w, "Removed log source %q from %s.\n", args[0], envID) })
		return nil
	}}
	command.Flags().BoolVar(&yes, "yes", false, "confirm a production configuration edit")
	return command
}

func executeLogSearch(cmd *cobra.Command, a *app.App, options *logOptions) (logs.SearchResult, string, string, error) {
	_, projectID, envID, env, err := a.RequireEnvironment()
	if err != nil {
		return logs.SearchResult{}, "", "", err
	}
	filter, err := logFilter(options)
	if err != nil {
		return logs.SearchResult{}, "", "", output.UsageError(err.Error())
	}
	result, err := a.Operations(projectID, env).SearchLogs(cmd.Context(), options.sources, filter)
	if err != nil {
		return result, projectID, envID, output.OperationalError("log search failed", err.Error())
	}
	return result, projectID, envID, nil
}

func diagnoseCommand(a *app.App) *cobra.Command {
	var rawParams []string
	command := &cobra.Command{Use: "diagnose <workflow>", Short: "Run a configured diagnostic workflow", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		_, projectID, envID, env, err := a.RequireEnvironment()
		if err != nil {
			return err
		}
		params := map[string]string{}
		for _, raw := range rawParams {
			key, value, ok := strings.Cut(raw, "=")
			if !ok || key == "" {
				return output.UsageError("--param values must use name=value")
			}
			params[key] = value
		}
		result, err := a.Operations(projectID, env).Diagnose(cmd.Context(), args[0], params)
		if err != nil {
			return output.OperationalError("diagnostic workflow failed", err.Error())
		}
		var findingErr error
		if result.Status == "DEGRADED" || result.Status == "UNHEALTHY" || result.Status == "UNKNOWN" {
			findingErr = output.OperationalError("diagnostic status is " + result.Status)
		}
		a.EmitOutcome("diagnose", projectID, envID, result.Status, result, findingErr, func(w io.Writer) {
			fmt.Fprintf(w, "DIAGNOSTIC: %s\nSTATUS: %s\n\n", strings.ToUpper(args[0]), result.Status)
			for _, step := range result.Steps {
				fmt.Fprintf(w, "%-20s %-10s %d ms\n", step.ID, step.Status, step.Duration)
			}
			for _, finding := range result.Findings {
				fmt.Fprintf(w, "\n%s: %s\nEvidence: %s\nRecommendation: %s\n", strings.ToUpper(finding.Severity), finding.Issue, finding.Evidence, finding.Recommendation)
			}
		})
		return findingErr
	}}
	command.Flags().StringArrayVar(&rawParams, "param", nil, "diagnostic input name=value")
	return command
}

func configCommand(a *app.App) *cobra.Command {
	root := &cobra.Command{Use: "config", Short: "Inspect and validate configuration"}
	root.AddCommand(
		configEditCommand(a),
		&cobra.Command{Use: "show", Short: "Show safe effective configuration", RunE: func(cmd *cobra.Command, args []string) error {
			cfg, projectID, envID, _, err := a.RequireEnvironment()
			if err != nil {
				return err
			}
			safe, err := config.SafeJSON(cfg)
			if err != nil {
				return output.ConfigError(err.Error())
			}
			if a.Options.JSON {
				a.Emit("config show", projectID, envID, "", json.RawMessage(safe), nil)
				return nil
			}
			a.Emit("config show", projectID, envID, "", nil, func(w io.Writer) { _, _ = w.Write(safe); fmt.Fprintln(w) })
			return nil
		}},
		&cobra.Command{Use: "validate", Short: "Validate all project files", RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.LoadProjects(); err != nil {
				return err
			}
			data := map[string]any{"valid": true, "projects": len(a.Manager.Projects), "config_dir": a.Options.ConfigDir}
			a.Emit("config validate", "", "", "HEALTHY", data, func(w io.Writer) {
				fmt.Fprintf(w, "Configuration valid.\nProjects: %d\nDirectory: %s\n", len(a.Manager.Projects), a.Options.ConfigDir)
			})
			return nil
		}},
	)
	return root
}

func configEditCommand(a *app.App) *cobra.Command {
	var editor string
	var yes bool
	command := &cobra.Command{
		Use:   "edit",
		Short: "Edit the active project YAML in a terminal editor",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, projectID, err := a.RequireProject()
			if err != nil {
				return err
			}
			if environmentID := selectedEnvironment(a, projectID); environmentID != "" {
				if environment, ok := cfg.Environments[environmentID]; ok && strings.EqualFold(environment.Kind, "production") && !yes {
					return output.UsageError("editing a production project configuration requires --yes")
				}
			}
			if a.Manager == nil {
				return output.ConfigError("project manager is not loaded")
			}
			path := a.Manager.Paths[projectID]
			if path == "" {
				return output.ConfigError(fmt.Sprintf("configuration path for project %q is not available", projectID))
			}
			selectedEditor, err := findEditor(editor)
			if err != nil {
				return output.ConfigError(err.Error())
			}
			if err := editConfigFile(cmd, path, selectedEditor); err != nil {
				return err
			}
			a.Emit("config edit", projectID, selectedEnvironment(a, projectID), "", map[string]string{"project": projectID, "path": path, "editor": selectedEditor}, func(w io.Writer) {
				fmt.Fprintf(w, "WARD configuration saved: %s\n", path)
			})
			return nil
		},
	}
	command.Flags().StringVar(&editor, "editor", "", "editor executable; defaults to WARD_EDITOR, VISUAL, EDITOR, nano, vim, or vi")
	command.Flags().BoolVar(&yes, "yes", false, "confirm editing a production project configuration")
	return command
}

func selectedEnvironment(a *app.App, projectID string) string {
	if a.Options.Env != "" {
		return a.Options.Env
	}
	if value := os.Getenv("WARD_ENV"); value != "" {
		return value
	}
	if projectID == a.Loaded.Project {
		return a.Loaded.Environment
	}
	return ""
}

func findEditor(explicit string) (string, error) {
	candidates := []string{}
	if strings.TrimSpace(explicit) != "" {
		candidates = append(candidates, explicit)
	} else {
		for _, name := range []string{"WARD_EDITOR", "VISUAL", "EDITOR"} {
			if value := strings.TrimSpace(os.Getenv(name)); value != "" {
				candidates = append(candidates, value)
				break
			}
		}
		candidates = append(candidates, "nano", "vim", "vi")
		if runtime.GOOS == "windows" {
			candidates = append(candidates, "notepad.exe")
		}
	}
	for _, candidate := range candidates {
		if path, err := exec.LookPath(candidate); err == nil {
			return path, nil
		}
	}
	return "", errors.New("no editor found; install nano or set WARD_EDITOR to a blocking editor executable")
}

func editConfigFile(parent *cobra.Command, path, editor string) error {
	original, err := os.ReadFile(path)
	if err != nil {
		return output.ConfigError(fmt.Sprintf("read %s: %v", path, err))
	}
	info, err := os.Stat(path)
	if err != nil {
		return output.ConfigError(fmt.Sprintf("stat %s: %v", path, err))
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".ward-edit-*.yaml")
	if err != nil {
		return output.ConfigError(fmt.Sprintf("create editable configuration copy: %v", err))
	}
	temporaryPath := temporary.Name()
	keepTemporary := true
	defer func() {
		if !keepTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(info.Mode().Perm()); err != nil {
		_ = temporary.Close()
		return output.ConfigError(fmt.Sprintf("secure editable configuration copy: %v", err))
	}
	if _, err := temporary.Write(original); err != nil {
		_ = temporary.Close()
		return output.ConfigError(fmt.Sprintf("write editable configuration copy: %v", err))
	}
	if err := temporary.Close(); err != nil {
		return output.ConfigError(fmt.Sprintf("close editable configuration copy: %v", err))
	}

	editorCommand := exec.CommandContext(parent.Context(), editor, temporaryPath)
	editorCommand.Stdin = os.Stdin
	editorCommand.Stdout = os.Stdout
	editorCommand.Stderr = os.Stderr
	if err := editorCommand.Run(); err != nil {
		return output.OperationalError("editor failed", err.Error())
	}
	if _, err := config.LoadFile(temporaryPath); err != nil {
		return output.ConfigError(fmt.Sprintf("edited configuration is invalid: %v; draft kept at %s", err, temporaryPath))
	}
	edited, err := os.ReadFile(temporaryPath)
	if err != nil {
		return output.ConfigError(fmt.Sprintf("read edited configuration: %v", err))
	}
	if err := state.AtomicWrite(path, edited, info.Mode().Perm()); err != nil {
		return output.ConfigError(fmt.Sprintf("save edited configuration: %v", err))
	}
	keepTemporary = false
	return nil
}

func doctorCommand(a *app.App) *cobra.Command {
	return &cobra.Command{Use: "doctor", Short: "Check WARD local readiness", RunE: func(cmd *cobra.Command, args []string) error {
		checks := []map[string]string{}
		add := func(name, status, message string) {
			checks = append(checks, map[string]string{"name": name, "status": status, "message": message})
		}
		if info, err := os.Stat(a.Options.ConfigDir); err != nil || !info.IsDir() {
			add("Configuration directory", "UNHEALTHY", "directory is missing or inaccessible: "+a.Options.ConfigDir)
		} else {
			add("Configuration directory", "HEALTHY", a.Options.ConfigDir)
		}
		if err := os.MkdirAll(a.Options.StateDir, 0o700); err != nil {
			add("State directory", "UNHEALTHY", err.Error())
		} else if testFile, err := os.CreateTemp(a.Options.StateDir, ".doctor-*"); err != nil {
			add("State directory", "UNHEALTHY", err.Error())
		} else {
			path := testFile.Name()
			testFile.Close()
			_ = os.Remove(path)
			add("State directory", "HEALTHY", a.Options.StateDir)
		}
		if err := a.LoadProjects(); err != nil {
			add("Project definitions", "UNHEALTHY", err.Error())
		} else {
			add("Project definitions", "HEALTHY", strconv.Itoa(len(a.Manager.Projects))+" project(s)")
		}
		var selectedEnv *config.EnvironmentConfig
		selectedProject := a.Options.Project
		if selectedProject == "" {
			selectedProject = os.Getenv("WARD_PROJECT")
		}
		if selectedProject == "" {
			selectedProject = a.Loaded.Project
		}
		selectedEnvironment := a.Options.Env
		if selectedEnvironment == "" {
			selectedEnvironment = os.Getenv("WARD_ENV")
		}
		if selectedEnvironment == "" && selectedProject == a.Loaded.Project {
			selectedEnvironment = a.Loaded.Environment
		}
		if selectedProject != "" && a.Manager != nil {
			if cfg, ok := a.Manager.Projects[selectedProject]; ok {
				add("Active project", "HEALTHY", cfg.Project.Name)
				if env, exists := cfg.Environments[selectedEnvironment]; exists && selectedEnvironment != "" {
					selectedEnv = &env
					add("Active environment", "HEALTHY", selectedEnvironment+" ("+env.Kind+")")
				} else {
					add("Active environment", "UNKNOWN", "none selected or not present in active project")
				}
			} else {
				add("Active project", "UNHEALTHY", selectedProject+" not found")
			}
		} else {
			add("Active project", "UNKNOWN", "none selected")
		}
		if selectedEnv != nil {
			missing := requiredEnvironmentVariables(*selectedEnv)
			if len(missing) == 0 {
				add("Environment variables", "HEALTHY", "all required variables are present")
			} else {
				add("Environment variables", "UNHEALTHY", "missing: "+strings.Join(missing, ", "))
			}
			caErrors := validateCAFiles(*selectedEnv)
			if len(caErrors) == 0 {
				add("CA certificate files", "HEALTHY", "configured files are readable")
			} else {
				add("CA certificate files", "UNHEALTHY", strings.Join(caErrors, "; "))
			}
		}
		status := "HEALTHY"
		for _, check := range checks {
			if check["status"] == "UNHEALTHY" {
				status = "UNHEALTHY"
				break
			}
			if check["status"] == "UNKNOWN" && status == "HEALTHY" {
				status = "DEGRADED"
			}
		}
		var failure error
		if status != "HEALTHY" {
			failure = output.OperationalError("WARD doctor found a problem")
		}
		a.EmitOutcome("doctor", "", "", status, checks, failure, func(w io.Writer) {
			fmt.Fprintln(w, branding.Name+" DOCTOR")
			fmt.Fprintln(w)
			for _, check := range checks {
				symbol := "✓"
				if check["status"] == "UNHEALTHY" {
					symbol = "✗"
				}
				fmt.Fprintf(w, "%-28s %s %s\n", check["name"], symbol, check["message"])
			}
		})
		return failure
	}}
}

func requiredEnvironmentVariables(env config.EnvironmentConfig) []string {
	set := map[string]bool{}
	add := func(name string) {
		if name != "" && os.Getenv(name) == "" {
			set[name] = true
		}
	}
	add(env.API.BaseURLEnv)
	if env.API.Auth != nil {
		add(env.API.Auth.BearerTokenEnv)
	}
	for _, header := range env.API.Headers {
		add(header.ValueEnv)
	}
	if env.Database != nil && env.Database.IsEnabled() {
		add(env.Database.HostEnv)
		add(env.Database.PortEnv)
		add(env.Database.DatabaseEnv)
		add(env.Database.UsernameEnv)
		add(env.Database.PasswordEnv)
	}
	for _, service := range env.Services {
		add(service.HostEnv)
		add(service.PortEnv)
	}
	for _, source := range env.Logs.Sources {
		if source.HTTP != nil {
			if source.HTTP.Auth != nil {
				add(source.HTTP.Auth.BearerTokenEnv)
			}
			for _, header := range source.HTTP.Headers {
				add(header.ValueEnv)
			}
		}
	}
	result := make([]string, 0, len(set))
	for name := range set {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func validateCAFiles(env config.EnvironmentConfig) []string {
	paths := []string{env.API.CACertPath}
	if env.Database != nil && env.Database.IsEnabled() {
		paths = append(paths, env.Database.CACertPath)
	}
	for _, source := range env.Logs.Sources {
		if source.HTTP != nil {
			paths = append(paths, source.HTTP.CACertPath)
		}
	}
	errorsFound := []string{}
	for _, path := range paths {
		if path == "" {
			continue
		}
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			errorsFound = append(errorsFound, path+" is not readable")
		}
	}
	return errorsFound
}

func versionCommand(a *app.App) *cobra.Command {
	return &cobra.Command{Use: "version", Short: "Show version", RunE: func(cmd *cobra.Command, args []string) error {
		a.Emit("version", "", "", "", map[string]string{"name": branding.Name, "version": version, "commit": commit, "build_date": buildDate}, func(w io.Writer) { fmt.Fprintf(w, "%s %s (%s, %s)\n", branding.Name, version, commit, buildDate) })
		return nil
	}}
}

func uiCommand(a *app.App) *cobra.Command {
	return &cobra.Command{Use: "ui", Short: "Launch the interactive dashboard", RunE: func(cmd *cobra.Command, args []string) error {
		if err := a.LoadProjects(); err != nil {
			return err
		}
		return tui.Run(a)
	}}
}
