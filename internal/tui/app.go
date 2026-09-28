package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	bubbletea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/ward/ward/internal/app"
	"github.com/ward/ward/internal/branding"
	"github.com/ward/ward/internal/config"
	"github.com/ward/ward/internal/service"
)

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	logoStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	mutedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	goodStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	badStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	prodStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("208"))
)

type screen int

const (
	dashboardScreen screen = iota
	apiScreen
	databaseScreen
	logsScreen
	logSetupScreen
	diagnosticScreen
	configScreen
)

type model struct {
	application  *app.App
	active       screen
	projectID    string
	project      string
	environment  string
	status       string
	message      string
	content      string
	loading      bool
	width        int
	height       int
	viewport     viewport.Model
	ready        bool
	cancel       context.CancelFunc
	requestID    uint64
	apiPath      textinput.Model
	queryID      textinput.Model
	queryParams  textinput.Model
	logSource    textinput.Model
	logLast      textinput.Model
	logSearch    textinput.Model
	logAddID     textinput.Model
	logAddName   textinput.Model
	logAddPath   textinput.Model
	logAddFormat textinput.Model
	logFollow    bool
	diagName     textinput.Model
	diagParams   textinput.Model
}

type healthMsg struct {
	id                              uint64
	projectID, project, environment string
	report                          any
	status                          string
	err                             error
	duration                        time.Duration
}

type operationMsg struct {
	id                                      uint64
	command, projectID, environment, status string
	data                                    any
	err                                     error
	duration                                time.Duration
}

type logTickMsg struct{}

func Run(application *app.App) error {
	program := bubbletea.NewProgram(newModel(application), bubbletea.WithAltScreen())
	_, err := program.Run()
	return err
}

func newModel(application *app.App) *model {
	input := func(prompt, placeholder string) textinput.Model {
		value := textinput.New()
		value.Prompt, value.Placeholder, value.Width = prompt, placeholder, 52
		return value
	}
	return &model{
		application:  application,
		apiPath:      input("Path: ", "/health"),
		queryID:      input("Query ID: ", "user_by_id"),
		queryParams:  input("Parameters: ", "1001"),
		logSource:    input("Source: ", "all configured sources"),
		logLast:      input("Window: ", "30m"),
		logSearch:    input("Search: ", "connection refused"),
		logAddID:     input("ID: ", "application"),
		logAddName:   input("Name: ", "Application log"),
		logAddPath:   input("Path: ", "logs/app.log"),
		logAddFormat: input("Format: ", "text"),
		diagName:     input("Workflow: ", "user"),
		diagParams:   input("Parameters: ", "user_id=10291"),
	}
}

func (m *model) Init() bubbletea.Cmd { return m.startHealth() }

func (m *model) Update(message bubbletea.Msg) (bubbletea.Model, bubbletea.Cmd) {
	switch message := message.(type) {
	case bubbletea.WindowSizeMsg:
		m.width, m.height = message.Width, message.Height
		contentHeight := message.Height - 9
		if contentHeight < 5 {
			contentHeight = 5
		}
		if !m.ready {
			m.viewport = viewport.New(message.Width-2, contentHeight)
			m.ready = true
			if m.content != "" {
				m.viewport.SetContent(m.content)
			}
		} else {
			m.viewport.Width, m.viewport.Height = message.Width-2, contentHeight
		}
		return m, nil
	case healthMsg:
		if message.id != m.requestID {
			return m, nil
		}
		m.loading, m.cancel = false, nil
		m.projectID, m.project, m.environment, m.status = message.projectID, message.project, message.environment, message.status
		m.application.RecordOperation("ui health", message.projectID, message.environment, message.status, message.err, message.duration)
		if message.err != nil {
			m.message = message.err.Error()
			m.setContent("HEALTH ERROR", message.err.Error())
		} else {
			m.message = ""
			m.setJSON("SYSTEM HEALTH", message.report)
		}
		return m, nil
	case operationMsg:
		if message.id != m.requestID {
			return m, nil
		}
		m.loading, m.cancel = false, nil
		m.application.RecordOperation(message.command, message.projectID, message.environment, message.status, message.err, message.duration)
		m.message = errorText(message.err)
		if message.data != nil {
			m.setJSON(strings.ToUpper(message.command), message.data)
		} else if message.err != nil {
			m.setContent(strings.ToUpper(message.command), message.err.Error())
		}
		if message.command == "ui logs add" && message.err == nil {
			m.active = logsScreen
			m.blurAll()
			m.logSource.Focus()
		}
		if message.command == "ui logs" && m.logFollow && message.err == nil {
			return m, bubbletea.Tick(500*time.Millisecond, func(time.Time) bubbletea.Msg { return logTickMsg{} })
		}
		return m, nil
	case logTickMsg:
		if m.active == logsScreen && m.logFollow && !m.loading {
			return m, m.startOperation(m.runLogs)
		}
		return m, nil
	case bubbletea.KeyMsg:
		key := message.String()
		if key == "ctrl+c" {
			m.stop()
			return m, bubbletea.Quit
		}
		if key == "esc" {
			m.stop()
			m.blurAll()
			if m.active == logSetupScreen {
				m.active = logsScreen
				m.logSource.Focus()
				return m, nil
			}
			m.active, m.message = dashboardScreen, ""
			return m, m.startHealth()
		}
		if m.loading {
			return m, nil
		}
		if m.active == dashboardScreen {
			switch key {
			case "q":
				m.stop()
				return m, bubbletea.Quit
			case "1":
				return m, nil
			case "2":
				m.active = apiScreen
				m.apiPath.Focus()
				return m, nil
			case "3":
				m.active = databaseScreen
				m.queryID.Focus()
				return m, nil
			case "4":
				m.active = logsScreen
				m.logSource.Focus()
				return m, nil
			case "5":
				m.active = diagnosticScreen
				m.diagName.Focus()
				return m, nil
			case "6":
				m.active = configScreen
				return m, m.startOperation(m.loadConfig)
			case "r":
				return m, m.startHealth()
			case "p":
				if err := m.cycleProject(); err != nil {
					m.message = err.Error()
					return m, nil
				}
				return m, m.startHealth()
			case "e":
				if err := m.cycleEnvironment(); err != nil {
					m.message = err.Error()
					return m, nil
				}
				return m, m.startHealth()
			}
		}
		if m.active == logsScreen {
			switch key {
			case "f":
				m.logFollow = !m.logFollow
				if m.logFollow {
					return m, m.startOperation(m.runLogs)
				}
				return m, nil
			case "a":
				m.logFollow = false
				m.blurAll()
				m.active = logSetupScreen
				m.logAddID.Focus()
				return m, nil
			}
		}
		if key == "enter" {
			switch m.active {
			case apiScreen:
				return m, m.startOperation(m.runAPI)
			case databaseScreen:
				return m, m.startOperation(m.runDatabase)
			case logsScreen:
				return m, m.startOperation(m.runLogs)
			case logSetupScreen:
				return m, m.startOperation(m.addLogSource)
			case diagnosticScreen:
				return m, m.startOperation(m.runDiagnostic)
			}
		}
		if key == "tab" {
			m.focusNext()
			return m, nil
		}
		if command := m.updateInput(message); command != nil {
			return m, command
		}
	}
	if m.ready {
		var command bubbletea.Cmd
		m.viewport, command = m.viewport.Update(message)
		return m, command
	}
	return m, nil
}

func (m *model) View() string {
	var builder strings.Builder
	if m.active == dashboardScreen {
		builder.WriteString(logoStyle.Render(branding.Logo))
		builder.WriteString("\n")
	}
	builder.WriteString(titleStyle.Render(branding.Name))
	builder.WriteString("  " + branding.Tagline + "\n")
	if m.project != "" {
		fmt.Fprintf(&builder, "Project: %s    Environment: %s\n", m.project, m.environmentLabel())
	} else {
		builder.WriteString("Project/environment not selected\n")
	}
	builder.WriteString(strings.Repeat("-", max(20, min(m.width-1, 100))))
	builder.WriteString("\n")
	if m.loading {
		builder.WriteString("Working... press Esc to cancel\n")
	} else {
		switch m.active {
		case dashboardScreen:
			if m.ready {
				builder.WriteString(m.viewport.View())
			}
		case apiScreen:
			builder.WriteString(titleStyle.Render("API GET"))
			builder.WriteString("\n\n" + m.apiPath.View() + "\n\nEnter runs; Esc cancels/returns.\n")
		case databaseScreen:
			builder.WriteString(titleStyle.Render("READ-ONLY DATABASE QUERY"))
			builder.WriteString("\n\n" + m.queryID.View() + "\n" + m.queryParams.View() + "\n\nTab changes field; Enter runs.\n")
		case logsScreen:
			builder.WriteString(titleStyle.Render("LOG SEARCH"))
			follow := "OFF"
			if m.logFollow {
				follow = "ON"
			}
			builder.WriteString("\n\n" + m.logSource.View() + "\n" + m.logLast.View() + "\n" + m.logSearch.View() + "\n\nTab changes field; Enter searches. F toggles live refresh (" + follow + "); A adds a file source.\n")
		case logSetupScreen:
			builder.WriteString(titleStyle.Render("ADD LOCAL LOG SOURCE"))
			builder.WriteString("\n\n" + m.logAddID.View() + "\n" + m.logAddName.View() + "\n" + m.logAddPath.View() + "\n" + m.logAddFormat.View() + "\n\nPath may be a file or glob, relative to the project YAML. Tab changes field; Enter saves.\n")
		case diagnosticScreen:
			builder.WriteString(titleStyle.Render("DIAGNOSTIC WORKFLOW"))
			builder.WriteString("\n\n" + m.diagName.View() + "\n" + m.diagParams.View() + "\n\nUse comma-separated name=value parameters; Enter runs.\n")
		case configScreen:
			if m.ready {
				builder.WriteString(m.viewport.View())
			}
		}
		if m.message != "" {
			builder.WriteString("\n" + badStyle.Render(m.message) + "\n")
		}
	}
	builder.WriteString("\n")
	builder.WriteString(mutedStyle.Render("1 Dashboard  2 API  3 DB  4 Logs  5 Diagnose  6 Config  P Project  E Environment  R Refresh  Q Quit"))
	builder.WriteString("\n")
	return builder.String()
}

func (m *model) startHealth() bubbletea.Cmd {
	return m.startOperation(func(ctx context.Context) operationMsg { return operationMsg{} }, func(ctx context.Context) bubbletea.Msg {
		started := time.Now()
		cfg, projectID, err := m.application.RequireProject()
		if err != nil {
			return healthMsg{err: err, duration: time.Since(started)}
		}
		_, _, envID, env, err := m.application.RequireEnvironment()
		if err != nil {
			return healthMsg{projectID: projectID, project: cfg.Project.Name, err: err, duration: time.Since(started)}
		}
		report, err := m.application.Operations(projectID, env).CheckHealth(ctx, "")
		return healthMsg{projectID: projectID, project: cfg.Project.Name, environment: envID, report: report, status: report.Status, err: err, duration: time.Since(started)}
	})
}

func (m *model) startOperation(operation any, custom ...func(context.Context) bubbletea.Msg) bubbletea.Cmd {
	m.stop()
	m.requestID++
	id := m.requestID
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel, m.loading = cancel, true
	if len(custom) > 0 {
		return func() bubbletea.Msg {
			message := custom[0](ctx)
			switch typed := message.(type) {
			case healthMsg:
				typed.id = id
				return typed
			case operationMsg:
				typed.id = id
				return typed
			default:
				return message
			}
		}
	}
	function := operation.(func(context.Context) operationMsg)
	return func() bubbletea.Msg {
		message := function(ctx)
		message.id = id
		return message
	}
}

func (m *model) runAPI(ctx context.Context) operationMsg {
	started := time.Now()
	_, projectID, envID, env, err := m.application.RequireEnvironment()
	if err != nil {
		return operation("ui api", projectID, envID, nil, err, started)
	}
	response, err := m.application.Operations(projectID, env).APIGetRequest(ctx, m.apiPath.Value(), url.Values{}, nil)
	if err == nil && (response.StatusCode < 200 || response.StatusCode >= 300) {
		err = fmt.Errorf("API returned %s", response.Status)
	}
	return operation("ui api", projectID, envID, response, err, started)
}

func (m *model) runDatabase(ctx context.Context) operationMsg {
	started := time.Now()
	_, projectID, envID, env, err := m.application.RequireEnvironment()
	if err != nil {
		return operation("ui db query", projectID, envID, nil, err, started)
	}
	result, err := m.application.Operations(projectID, env).RunDatabaseQuery(ctx, m.queryID.Value(), strings.Fields(m.queryParams.Value()))
	return operation("ui db query", projectID, envID, result, err, started)
}

func (m *model) runLogs(ctx context.Context) operationMsg {
	started := time.Now()
	_, projectID, envID, env, err := m.application.RequireEnvironment()
	if err != nil {
		return operation("ui logs", projectID, envID, nil, err, started)
	}
	values := map[string]string{"last": valueOr(m.logLast.Value(), "30m"), "search": m.logSearch.Value(), "limit": "500"}
	filter, err := service.ParseLogFilter(values)
	if err != nil {
		return operation("ui logs", projectID, envID, nil, err, started)
	}
	sources := []string{}
	if value := strings.TrimSpace(m.logSource.Value()); value != "" {
		for _, source := range strings.Split(value, ",") {
			sources = append(sources, strings.TrimSpace(source))
		}
	}
	result, err := m.application.Operations(projectID, env).SearchLogs(ctx, sources, filter)
	return operation("ui logs", projectID, envID, result, err, started)
}

func (m *model) addLogSource(ctx context.Context) operationMsg {
	started := time.Now()
	_, projectID, envID, env, err := m.application.RequireEnvironment()
	if err != nil {
		return operation("ui logs add", projectID, envID, nil, err, started)
	}
	if strings.EqualFold(env.Kind, "production") {
		return operation("ui logs add", projectID, envID, nil, fmt.Errorf("adding a production log source is disabled in the TUI; use the CLI with explicit --yes"), started)
	}
	source := config.LogSourceConfig{ID: strings.TrimSpace(m.logAddID.Value()), Name: valueOr(m.logAddName.Value(), m.logAddID.Value()), Type: "file", Format: valueOr(m.logAddFormat.Value(), "text"), Paths: []string{strings.TrimSpace(m.logAddPath.Value())}}
	if source.ID == "" || source.Paths[0] == "" {
		return operation("ui logs add", projectID, envID, nil, fmt.Errorf("source ID and path are required"), started)
	}
	err = m.application.Operations(projectID, env).AddFileLogSource(envID, source)
	return operation("ui logs add", projectID, envID, source, err, started)
}

func (m *model) runDiagnostic(ctx context.Context) operationMsg {
	started := time.Now()
	_, projectID, envID, env, err := m.application.RequireEnvironment()
	if err != nil {
		return operation("ui diagnose", projectID, envID, nil, err, started)
	}
	params := map[string]string{}
	for _, raw := range strings.Split(m.diagParams.Value(), ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		key, value, ok := strings.Cut(raw, "=")
		if !ok || key == "" {
			return operation("ui diagnose", projectID, envID, nil, fmt.Errorf("parameters must use name=value"), started)
		}
		params[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	result, err := m.application.Operations(projectID, env).Diagnose(ctx, m.diagName.Value(), params)
	return operationWithStatus("ui diagnose", projectID, envID, result.Status, result, err, started)
}

func (m *model) loadConfig(ctx context.Context) operationMsg {
	started := time.Now()
	cfg, projectID, envID, _, err := m.application.RequireEnvironment()
	if err != nil {
		return operation("ui config", projectID, envID, nil, err, started)
	}
	data, err := config.SafeJSON(cfg)
	if err != nil {
		return operation("ui config", projectID, envID, nil, err, started)
	}
	var value any
	err = json.Unmarshal(data, &value)
	return operation("ui config", projectID, envID, value, err, started)
}

func (m *model) updateInput(message bubbletea.KeyMsg) bubbletea.Cmd {
	var command bubbletea.Cmd
	switch m.active {
	case apiScreen:
		m.apiPath, command = m.apiPath.Update(message)
	case databaseScreen:
		if m.queryID.Focused() {
			m.queryID, command = m.queryID.Update(message)
		} else {
			m.queryParams, command = m.queryParams.Update(message)
		}
	case logsScreen:
		if m.logSource.Focused() {
			m.logSource, command = m.logSource.Update(message)
		} else if m.logLast.Focused() {
			m.logLast, command = m.logLast.Update(message)
		} else {
			m.logSearch, command = m.logSearch.Update(message)
		}
	case logSetupScreen:
		switch {
		case m.logAddID.Focused():
			m.logAddID, command = m.logAddID.Update(message)
		case m.logAddName.Focused():
			m.logAddName, command = m.logAddName.Update(message)
		case m.logAddPath.Focused():
			m.logAddPath, command = m.logAddPath.Update(message)
		default:
			m.logAddFormat, command = m.logAddFormat.Update(message)
		}
	case diagnosticScreen:
		if m.diagName.Focused() {
			m.diagName, command = m.diagName.Update(message)
		} else {
			m.diagParams, command = m.diagParams.Update(message)
		}
	}
	return command
}

func (m *model) focusNext() {
	switch m.active {
	case databaseScreen:
		if m.queryID.Focused() {
			m.queryID.Blur()
			m.queryParams.Focus()
		} else {
			m.queryParams.Blur()
			m.queryID.Focus()
		}
	case logsScreen:
		if m.logSource.Focused() {
			m.logSource.Blur()
			m.logLast.Focus()
		} else if m.logLast.Focused() {
			m.logLast.Blur()
			m.logSearch.Focus()
		} else {
			m.logSearch.Blur()
			m.logSource.Focus()
		}
	case diagnosticScreen:
		if m.diagName.Focused() {
			m.diagName.Blur()
			m.diagParams.Focus()
		} else {
			m.diagParams.Blur()
			m.diagName.Focus()
		}
	case logSetupScreen:
		switch {
		case m.logAddID.Focused():
			m.logAddID.Blur()
			m.logAddName.Focus()
		case m.logAddName.Focused():
			m.logAddName.Blur()
			m.logAddPath.Focus()
		case m.logAddPath.Focused():
			m.logAddPath.Blur()
			m.logAddFormat.Focus()
		default:
			m.logAddFormat.Blur()
			m.logAddID.Focus()
		}
	}
}

func (m *model) blurAll() {
	m.apiPath.Blur()
	m.queryID.Blur()
	m.queryParams.Blur()
	m.logSource.Blur()
	m.logLast.Blur()
	m.logSearch.Blur()
	m.logAddID.Blur()
	m.logAddName.Blur()
	m.logAddPath.Blur()
	m.logAddFormat.Blur()
	m.diagName.Blur()
	m.diagParams.Blur()
}
func (m *model) stop() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.loading = false
}
func (m *model) setJSON(title string, value any) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		m.setContent(title, err.Error())
		return
	}
	m.setContent(title, string(data))
}
func (m *model) setContent(title, value string) {
	m.content = titleStyle.Render(title) + "\n\n" + value
	if m.ready {
		m.viewport.SetContent(m.content)
		m.viewport.GotoTop()
	}
}

func (m *model) cycleProject() error {
	if m.application.Manager == nil || len(m.application.Manager.Projects) == 0 {
		return fmt.Errorf("no projects are configured")
	}
	ids := make([]string, 0, len(m.application.Manager.Projects))
	for id := range m.application.Manager.Projects {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	index := -1
	for i, id := range ids {
		if id == m.application.Loaded.Project {
			index = i
			break
		}
	}
	next := ids[(index+1)%len(ids)]
	cfg := m.application.Manager.Projects[next]
	m.application.Options.Project = ""
	m.application.Options.Env = ""
	// Switching projects always clears the environment. A same-named
	// production environment must never be inherited implicitly.
	environment := ""
	m.projectID, m.project, m.environment = next, cfg.Project.Name, environment
	return m.application.SaveSelection(next, environment)
}

func (m *model) cycleEnvironment() error {
	cfg, projectID, err := m.application.RequireProject()
	if err != nil {
		return err
	}
	ids := app.SortedEnvironmentIDs(cfg)
	if len(ids) == 0 {
		return fmt.Errorf("project %q has no environments", projectID)
	}
	index := -1
	for i, id := range ids {
		if id == m.application.Loaded.Environment {
			index = i
			break
		}
	}
	environment := ids[(index+1)%len(ids)]
	m.application.Options.Project = ""
	m.application.Options.Env = ""
	m.environment = environment
	return m.application.SaveSelection(projectID, environment)
}

func (m *model) environmentLabel() string {
	if m.environment == "" {
		return badStyle.Render("NOT SELECTED")
	}
	if m.application.Manager != nil {
		if cfg, ok := m.application.Manager.Projects[m.projectID]; ok {
			if environment, exists := cfg.Environments[m.environment]; exists && strings.EqualFold(environment.Kind, "production") {
				return prodStyle.Render(strings.ToUpper(m.environment) + " (PRODUCTION)")
			}
		}
	}
	return strings.ToUpper(m.environment)
}

func operation(command, projectID, environment string, data any, err error, started time.Time) operationMsg {
	return operationWithStatus(command, projectID, environment, "", data, err, started)
}
func operationWithStatus(command, projectID, environment, status string, data any, err error, started time.Time) operationMsg {
	if err != nil && status == "" {
		status = "ERROR"
	} else if status == "" {
		status = "OK"
	}
	return operationMsg{command: command, projectID: projectID, environment: environment, status: status, data: data, err: err, duration: time.Since(started)}
}
func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
