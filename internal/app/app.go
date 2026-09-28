package app

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ward/ward/internal/audit"
	"github.com/ward/ward/internal/config"
	"github.com/ward/ward/internal/output"
	"github.com/ward/ward/internal/project"
	"github.com/ward/ward/internal/security"
	"github.com/ward/ward/internal/service"
	"github.com/ward/ward/internal/state"
)

type Options struct {
	ConfigDir string
	StateDir  string
	Project   string
	Env       string
	JSON      bool
	Quiet     bool
	Verbose   bool
	Timeout   int
	MaxRows   int
	MaxBytes  int
}

type App struct {
	Options        Options
	Stdout         io.Writer
	Stderr         io.Writer
	Manager        *project.Manager
	State          *state.Store
	Loaded         state.State
	EmittedError   bool
	Logger         *audit.Logger
	CurrentCommand string
	Started        time.Time
}

func New(options Options, stdout, stderr io.Writer) *App {
	return &App{Options: options, Stdout: stdout, Stderr: stderr}
}

func (a *App) Prepare() error {
	if a.Options.ConfigDir == "" {
		dir, err := config.DefaultConfigDir()
		if err != nil {
			return output.ConfigError(err.Error())
		}
		a.Options.ConfigDir = dir
	}
	if a.Options.StateDir == "" {
		dir, err := config.DefaultStateDir()
		if err != nil {
			return output.ConfigError(err.Error())
		}
		a.Options.StateDir = dir
	}
	a.State = state.New(a.Options.StateDir)
	a.Logger = audit.New(a.Options.StateDir)
	loaded, err := a.State.Load()
	if err != nil {
		return output.ConfigError(err.Error())
	}
	a.Loaded = loaded
	return nil
}

func (a *App) BeginCommand(command string) { a.CurrentCommand, a.Started = command, time.Now() }

func (a *App) RecordOperation(command, projectID, envID, status string, err error, duration time.Duration) {
	if a.Logger == nil {
		return
	}
	entry := audit.Entry{Command: command, Project: projectID, Environment: envID, Status: statusOrOK(status), DurationMS: duration.Milliseconds()}
	if err != nil {
		entry.Status, entry.Error = "ERROR", err.Error()
	}
	a.Logger.Record(entry)
}

func (a *App) durationMS() int64 {
	if a.Started.IsZero() {
		return 0
	}
	return time.Since(a.Started).Milliseconds()
}

func (a *App) LoadProjects() error {
	manager, err := project.Load(a.Options.ConfigDir)
	if err != nil {
		if err == config.ErrNoProjects {
			return output.ConfigError(fmt.Sprintf("no project configurations found in %s; add YAML files under %s", a.Options.ConfigDir, filepath.Join(a.Options.ConfigDir, "projects")))
		}
		return output.ConfigError(err.Error())
	}
	a.Manager = manager
	return nil
}

func (a *App) RequireProject() (config.ProjectConfig, string, error) {
	if a.Manager == nil {
		if err := a.LoadProjects(); err != nil {
			return config.ProjectConfig{}, "", err
		}
	}
	id := a.Options.Project
	if id == "" {
		id = os.Getenv("WARD_PROJECT")
	}
	if id == "" {
		id = a.Loaded.Project
	}
	if id == "" {
		return config.ProjectConfig{}, "", output.ConfigError("no project selected; use --project, WARD_PROJECT, or `ward project use <id>`")
	}
	cfg, err := a.Manager.Get(id)
	if err != nil {
		return config.ProjectConfig{}, "", output.ConfigError(err.Error())
	}
	return cfg, id, nil
}

func (a *App) RequireEnvironment() (config.ProjectConfig, string, string, config.EnvironmentConfig, error) {
	cfg, projectID, err := a.RequireProject()
	if err != nil {
		return config.ProjectConfig{}, "", "", config.EnvironmentConfig{}, err
	}
	envID := a.Options.Env
	if envID == "" {
		envID = os.Getenv("WARD_ENV")
	}
	if envID == "" {
		// Persisted environments belong to the persisted project. Never carry one
		// across an explicit project override, even when both projects use the same
		// environment ID (especially "production").
		if projectID == a.Loaded.Project {
			envID = a.Loaded.Environment
		}
	}
	if envID == "" {
		return config.ProjectConfig{}, "", "", config.EnvironmentConfig{}, output.ConfigError("no environment selected; use --env, WARD_ENV, or `ward env use <id>`")
	}
	env, ok := cfg.Environments[envID]
	if !ok {
		return config.ProjectConfig{}, "", "", config.EnvironmentConfig{}, output.ConfigError(fmt.Sprintf("environment %q not found in project %q", envID, projectID))
	}
	return cfg, projectID, envID, env, nil
}

func (a *App) SaveSelection(projectID, envID string) error {
	if err := a.State.Save(state.State{Project: projectID, Environment: envID}); err != nil {
		return output.ConfigError(err.Error())
	}
	a.Loaded = state.State{Project: projectID, Environment: envID}
	return nil
}

func (a *App) Operations(projectID string, env config.EnvironmentConfig) *service.Operations {
	path := ""
	if a.Manager != nil {
		path = a.Manager.Paths[projectID]
	}
	return &service.Operations{Environment: env, ConfigPath: path, Timeout: a.Timeout(env), MaxRows: a.MaxRows(env), MaxBytes: a.MaxBytes(env)}
}

func (a *App) Timeout(env config.EnvironmentConfig) time.Duration {
	seconds := a.Options.Timeout
	if seconds <= 0 {
		seconds = env.Limits.TimeoutSeconds
	}
	if seconds <= 0 {
		seconds = 10
	}
	return time.Duration(seconds) * time.Second
}

func (a *App) MaxRows(env config.EnvironmentConfig) int {
	if a.Options.MaxRows > 0 {
		return a.Options.MaxRows
	}
	if env.Limits.MaxRows > 0 {
		return env.Limits.MaxRows
	}
	return config.DefaultMaxRows
}

func (a *App) MaxBytes(env config.EnvironmentConfig) int {
	if a.Options.MaxBytes > 0 {
		return a.Options.MaxBytes
	}
	if env.Limits.MaxResponseBytes > 0 {
		return env.Limits.MaxResponseBytes
	}
	return config.DefaultMaxResponse
}

func (a *App) Emit(command, projectID, envID, status string, data any, human func(io.Writer)) {
	if a.Logger != nil {
		a.Logger.Record(audit.Entry{Command: command, Project: projectID, Environment: envID, Status: statusOrOK(status), DurationMS: a.durationMS()})
	}
	if a.Options.JSON {
		_ = output.WriteJSON(a.Stdout, output.Envelope{OK: true, Command: command, Project: projectID, Environment: envID, Status: status, Data: data})
		return
	}
	if !a.Options.Quiet && human != nil {
		human(a.Stdout)
	}
}

func (a *App) EmitOutcome(command, projectID, envID, status string, data any, err error, human func(io.Writer)) {
	logStatus := statusOrOK(status)
	if err != nil {
		logStatus = "ERROR"
	}
	if a.Logger != nil {
		entry := audit.Entry{Command: command, Project: projectID, Environment: envID, Status: logStatus, DurationMS: a.durationMS()}
		if err != nil {
			entry.Error = err.Error()
		}
		a.Logger.Record(entry)
	}
	if a.Options.JSON {
		envelope := output.Envelope{OK: err == nil, Command: command, Project: projectID, Environment: envID, Status: status, Data: data}
		if err != nil {
			envelope.Error = output.ErrorInfoFrom(err)
			a.EmittedError = true
		}
		_ = output.WriteJSON(a.Stdout, envelope)
		return
	}
	if !a.Options.Quiet && human != nil {
		human(a.Stdout)
	}
}

func (a *App) EmitError(command string, err error) {
	if a.Logger != nil {
		a.Logger.Record(audit.Entry{Command: command, Status: "ERROR", Error: err.Error(), DurationMS: a.durationMS()})
	}
	if a.Options.JSON {
		_ = output.WriteJSON(a.Stdout, output.Envelope{OK: false, Command: command, Error: output.ErrorInfoFrom(err)})
		a.EmittedError = true
		return
	}
	if a.Stderr != nil {
		fmt.Fprintln(a.Stderr, err)
		if a.Options.Verbose {
			var typed *output.Error
			if errors.As(err, &typed) && typed.Detail != "" {
				fmt.Fprintln(a.Stderr, security.DefaultRedactor().RedactText(typed.Detail))
			}
		}
	}
}

func statusOrOK(status string) string {
	if status == "" {
		return "OK"
	}
	return status
}

func SortedEnvironmentIDs(cfg config.ProjectConfig) []string {
	ids := make([]string, 0, len(cfg.Environments))
	for id := range cfg.Environments {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func ProjectKind(cfg config.ProjectConfig, envID string) string {
	return strings.ToUpper(cfg.Environments[envID].Kind)
}
