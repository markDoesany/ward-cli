# WARD User Guide

WARD is the Ward Observer Support System: a project-agnostic command-line and terminal dashboard for support engineering. It centralizes service health checks, API inspection, read-only database checks, log investigation, diagnostic workflows, configuration inspection, and safe project selection.

WARD is designed for investigation. It does not deploy applications, restart services, write to application databases, or supervise application processes.

## 1. Installation and first run

### Run from the repository

Requirements:

- Go 1.26 or later.
- PowerShell and Windows Terminal on Windows.
- GNU Make for the optional `make` shortcuts.

Run the application directly:

```powershell
go run . --help
go run . version
```

Build a native executable:

```powershell
go build -o ward.exe .
.\ward.exe version
```

Using Make:

```powershell
make run
make run ARGS="version"
make build
make test
make check
```

On Windows, `make run` uses the repository `projects` directory and selects:

```text
Project: tunover
Environment: development
```

Override those defaults when needed:

```powershell
make run PROJECT=sample ENVIRONMENT=development ARGS="health"
make run CONFIG_DIR="C:\path\to\ward-config" ARGS="project list"
```

### Run the installed executable

After building or installing `ward`, the normal entry point is:

```powershell
ward
ward ui
```

In an interactive terminal, either command opens the dashboard. In a redirected shell, the root command prints help instead of attempting to start the interactive interface.

The command examples below assume the project files are available in WARD's default configuration directory. When working directly from this repository, add `--config-dir .` to the command or use `go run .`/`make run`.

## 2. Configuration and data locations

WARD discovers project definitions in:

```text
<config-directory>\projects\*.yaml
```

The default Windows configuration and state directory is:

```text
%APPDATA%\ward
```

Override the locations with environment variables:

```powershell
$env:WARD_CONFIG_DIR = "C:\path\to\ward-config"
$env:WARD_STATE_DIR = "C:\path\to\ward-state"
```

The equivalent command-line flags take precedence for that invocation:

```powershell
ward --config-dir C:\path\to\ward-config --state-dir C:\path\to\ward-state project list
```

WARD stores the selected project and environment in `state.json` and its operational audit log in `ward.log`. These files belong to WARD, not to the application being investigated.

### Project configuration basics

Every project file requires:

```yaml
schema_version: 1

project:
  id: example
  name: Example Application

environments:
  development:
    kind: development
    api:
      base_url: http://localhost:8080
```

Supported environment kinds are `development`, `staging`, and `production`.

Secrets must not be placed directly in YAML. Use environment-variable references instead:

```yaml
api:
  base_url: https://api.example.test
  auth:
    bearer_token_env: EXAMPLE_API_TOKEN
```

```powershell
$env:EXAMPLE_API_TOKEN = "token-value"
```

An API URL can also be selected through an environment variable:

```yaml
api:
  base_url_env: WARD_EXAMPLE_API_URL
```

Only one of `base_url` and `base_url_env` may be configured.

### Validate before investigating

Validate every project definition:

```powershell
ward --config-dir . config validate
```

Validate WARD's local readiness and the selected project/environment:

```powershell
ward --config-dir . --project tunover --env development doctor
```

`doctor` checks the configuration directory, state directory, project definitions, active selection, required environment variables, and configured CA certificate files.

## 3. Selecting a project and environment

List all available projects:

```powershell
ward project list
```

Select a project:

```powershell
ward project use tunover
```

List environments for the active project:

```powershell
ward env list
```

Select an environment:

```powershell
ward env use development
```

Inspect the current selection:

```powershell
ward project info
```

For scripts and one-off checks, avoid changing persisted state and pass the selection explicitly:

```powershell
ward --project tunover --env development health
```

Environment variables can also supply the selection:

```powershell
$env:WARD_PROJECT = "tunover"
$env:WARD_ENV = "development"
ward health
```

When switching projects, WARD clears the previous environment selection. This prevents a `production` selection from being silently carried into another project.

## 4. Health checks

Run all configured service checks:

```powershell
ward --project tunover --env development health
```

Health checks can be configured for:

- HTTP and HTTPS endpoints.
- TCP services.
- MySQL database connectivity.

Independent service checks run concurrently. Each result includes a status, response or connection message, and duration.

The aggregate statuses are:

| Status | Meaning |
|---|---|
| `HEALTHY` | All checks passed. |
| `DEGRADED` | Some checks passed while others failed or are unknown. |
| `UNHEALTHY` | All checks failed. |
| `UNKNOWN` | Checks could not be evaluated, usually because configuration or required variables are missing. |

### Expected HTTP statuses

A service normally expects HTTP 200. Protected endpoints can declare another expected status:

```yaml
services:
  - name: Protected session endpoint
    type: http
    url: /auth/session
    expected_status: 401
```

In this example, HTTP 401 means the endpoint is reachable and correctly requires authentication, so WARD reports it as healthy.

### TUNOVER health

The included TUNOVER development profile checks:

- `/` and expects HTTP 200.
- `/api/profile` and expects HTTP 401 without a session.

Start TUNOVER separately, then run:

```powershell
ward --project tunover --env development health
```

WARD never starts or restarts TUNOVER.

## 5. API inspection

WARD currently provides read-only API `GET` inspection:

```powershell
ward --project tunover --env development api GET /
ward --project sample --env development api GET /health
```

Add query parameters with repeated `--query` flags:

```powershell
ward api GET /users --query status=active --query limit=20
```

Add request headers with repeated `--header` flags:

```powershell
ward api GET /health --header X-Request-ID=ward-check-001
```

Headers and query values use `name=value` syntax. API paths must be relative and begin with `/`.

WARD automatically loads configured bearer authentication where the project defines `bearer_token_env`. Sensitive response fields are redacted before display.

Use JSON output for scripts:

```powershell
ward --json api GET /health
```

Do not put bearer tokens directly in command-line arguments. Command-line arguments may be visible in shell history or process inspection tools.

## 6. Read-only database inspection

Database operations are configured per environment. A database profile can be explicitly disabled:

```yaml
database:
  enabled: false
  driver: mysql
```

An omitted `enabled` field preserves the original enabled-by-default behavior.

Check connectivity:

```powershell
ward --project sample --env development db status
```

Run a named query:

```powershell
ward --project sample --env development db query user_by_id 1001
```

Named queries are defined in YAML:

```yaml
queries:
  user_by_id:
    description: Find a user by ID
    parameters:
      - name: user_id
        type: int
    sql: |
      SELECT id, email, status, created_at
      FROM users
      WHERE id = ?
```

WARD does not provide unrestricted raw SQL. Queries must be configured, read-only `SELECT` or `WITH` statements, and single-statement queries. Destructive operations such as `INSERT`, `UPDATE`, `DELETE`, `DROP`, `ALTER`, and `TRUNCATE` are rejected.

Production MySQL connections require TLS. Credentials are loaded from environment variables and never stored in project YAML.

## 7. Log investigation

WARD reads logs produced by other applications. It does not create application log files, redirect application output, launch processes, or supervise processes.

Supported sources are:

- Local files.
- Local file globs.
- HTTP log endpoints configured in YAML.

### List configured sources

```powershell
ward --project tunover --env development logs sources
```

### Register a local file

```powershell
ward --project sample --env development logs add application `
  --name "Application log" `
  --path logs/application.log `
  --format text
```

Register a glob:

```powershell
ward logs add application --path "logs/*.jsonl" --format jsonl
```

Multiple paths can be supplied:

```powershell
ward logs add application --path logs/app-1.log --path logs/app-2.log
```

Relative paths are resolved relative to the project YAML file, not the current shell directory. Absolute paths are also supported.

### Remove a source

```powershell
ward --project sample --env development logs remove application
```

When the selected environment is production, adding or removing a source requires explicit confirmation:

```powershell
ward --project sample --env production logs add application --path logs/app.log --yes
```

The add/remove commands edit the project YAML through an atomic update and reject duplicate source IDs.

### Search logs

Search the last 30 minutes by default:

```powershell
ward logs
```

Common filters:

```powershell
ward logs --last 30m
ward logs --level ERROR
ward logs --user 10291
ward logs --ref TXN12345
ward logs --search "connection refused"
ward logs --source application
```

Use absolute time boundaries with RFC3339 timestamps:

```powershell
ward logs --since 2026-09-28T10:00:00Z --until 2026-09-28T10:30:00Z
```

Limit the number of returned entries:

```powershell
ward logs --limit 100
```

If a configured local glob matches no files, WARD reports that source as unavailable instead of silently returning an empty result.

### Follow new entries

Follow local file sources and print new complete lines as they arrive:

```powershell
ward --project tunover --env development logs --source tunover-web --follow
```

Stop following with `Ctrl+C`. Follow mode polls every 500 milliseconds, handles truncation and same-path replacement, and supports local files only. `--follow` cannot be combined with `--json`.

### Export sanitized logs

Export the current search result as JSON Lines:

```powershell
ward logs export --output errors.jsonl --level ERROR --last 1h
```

WARD refuses to overwrite an existing export unless `--force` is supplied:

```powershell
ward logs export --output errors.jsonl --force
```

Exported values are redacted according to the environment's redaction configuration.

### TUNOVER log setup

From the TUNOVER repository, redirect development output to a file:

```powershell
cd C:\Users\emman\OneDrive\Documents\Project\TUNOVER
New-Item -ItemType Directory -Force logs | Out-Null
npm run dev *> logs\tunover-web.log
```

The included WARD TUNOVER profile looks for `../../TUNOVER/logs/*.log` relative to `projects/tunover.yaml`. From the repository layout, the source is already configured:

```powershell
ward --project tunover --env development logs sources
ward --project tunover --env development logs --source tunover-web --last 30m
```

If the profile is copied to another configuration directory, register the actual log path manually with `ward logs add`.

## 8. Diagnostic workflows

Diagnostics combine multiple checks into one named workflow. A workflow may contain health, API, database, and log steps, followed by assertions.

Run the TUNOVER observer workflow:

```powershell
ward --project tunover --env development diagnose observer
```

The workflow checks:

1. TUNOVER web availability.
2. Protected profile reachability and expected unauthenticated behavior.
3. Recent TUNOVER log errors.

The sample project includes a parameterized user workflow:

```powershell
ward --project sample --env development diagnose user --param user_id=10291
```

Pass multiple parameters by repeating `--param`:

```powershell
ward diagnose workflow-name --param user_id=10291 --param region=manila
```

Each parameter must match the configured type. Diagnostics return `HEALTHY`, `DEGRADED`, `UNHEALTHY`, or `UNKNOWN`. A non-healthy diagnostic returns exit code 1, which makes it suitable for automation gates.

## 9. Editing configuration from the terminal

Edit the active project's YAML like `sudo nano`:

```powershell
ward --project tunover --env development config edit --editor nano
```

Editor selection order:

1. `--editor`.
2. `WARD_EDITOR`.
3. `VISUAL`.
4. `EDITOR`.
5. `nano`.
6. `vim`.
7. `vi`.
8. `notepad.exe` on Windows.

Example:

```powershell
$env:WARD_EDITOR = "nano"
ward --project tunover --env development config edit
```

WARD edits a temporary draft first. After the editor exits, it parses and validates the draft. The original file is replaced atomically only when validation succeeds. If validation fails, the draft is retained and its path is printed so it can be corrected or recovered.

Production configuration editing requires explicit confirmation:

```powershell
ward --project sample --env production config edit --editor nano --yes
```

Other configuration commands:

```powershell
ward config show
ward config validate
```

`config show` masks sensitive values and is safe to use when sharing configuration diagnostics.

## 10. Interactive dashboard

Launch the dashboard:

```powershell
ward
ward ui
```

The dashboard opens with the WARD observer-ward system mark, the active project/environment, and a health view.

Main screens:

| Key | Screen |
|---|---|
| `1` | Dashboard |
| `2` | API GET inspector |
| `3` | Read-only database query |
| `4` | Log search |
| `5` | Diagnostic workflow |
| `6` | Safe configuration view |
| `P` | Cycle project |
| `E` | Cycle environment |
| `R` | Refresh health |
| `Q` | Quit |

Common controls:

- `Tab` changes form fields.
- `Enter` runs the selected operation.
- `Esc` cancels an operation or returns to the dashboard.
- `Ctrl+C` exits.

On the log screen:

- `F` toggles live refresh.
- `A` opens the local log-source form.

The TUI uses the same project configuration and operation services as the CLI. A change made through the TUI or CLI affects the same YAML project file.

## 11. Output modes and exit codes

Human-readable output is the default:

```powershell
ward health
```

Use JSON for automation:

```powershell
ward --json health
ward --json logs --source application --last 1h
```

Suppress successful human output:

```powershell
ward --quiet health
```

Enable more technical diagnostics:

```powershell
ward --verbose health
```

Exit codes:

| Code | Meaning |
|---:|---|
| `0` | Command completed successfully. |
| `1` | Operational failure or non-healthy result. |
| `2` | Usage or configuration error. |

Use `--timeout`, `--max-rows`, and `--max-bytes` to control operation limits:

```powershell
ward --timeout 20 --max-rows 5000 db query user_by_id 1001
```

## 12. Security and safe operating rules

Follow these rules when using WARD against sensitive environments:

1. Never put passwords, bearer tokens, cookies, or API keys in YAML.
2. Use environment variables for credentials and sensitive headers.
3. Confirm the project and environment before every production investigation.
4. Treat `production` output as sensitive even when values are masked.
5. Do not paste secrets into `--header` or other command-line arguments.
6. Use configured named database queries instead of raw SQL.
7. Keep database access read-only.
8. Review exported JSONL files before sharing them.
9. Do not commit generated state, audit logs, or support bundles.
10. Use `--yes` only when you intentionally want to change production configuration.

WARD automatically redacts common fields such as passwords, tokens, authorization values, cookies, and configured project-specific patterns. Redaction is a safety layer, not a substitute for access control.

## 13. Troubleshooting

### No project configurations found

Check that the selected directory contains a `projects` folder with YAML files:

```powershell
Get-ChildItem .\projects\*.yaml
ward --config-dir . project list
```

### No project or environment selected

Select them explicitly:

```powershell
ward project use tunover
ward env use development
```

Or pass them for one command:

```powershell
ward --project tunover --env development health
```

### Configuration validation fails

Run:

```powershell
ward --config-dir . config validate
```

Typical causes include an unsupported environment kind, a missing API URL, duplicate log-source IDs, an invalid database query, or an unknown diagnostic step reference.

### LokalStore health is unknown

The LokalStore profile requires an API URL environment variable:

```powershell
$env:WARD_LOKALSTORE_API_URL = "http://localhost:8080"
ward --project lokalstore-api --env development health
```

The profile expects `/api/auth/session` to return HTTP 401 when no session is present.

### TUNOVER health is unhealthy

Confirm the application is running at `http://localhost:3000`:

```powershell
Test-NetConnection localhost -Port 3000
ward --project tunover --env development health
```

WARD does not start the application for you.

### A log source is unavailable

List the configured source and verify its resolved file exists:

```powershell
ward --project tunover --env development logs sources
Get-ChildItem ..\..\TUNOVER\logs\*.log
```

If the project configuration was copied elsewhere, add a source with an absolute path or a path relative to that copied YAML file.

### The editor cannot be found

Install a terminal editor or set its full executable path:

```powershell
$env:WARD_EDITOR = "C:\tools\nano\nano.exe"
ward config edit
```

The configured editor must block until the editing session is complete.

### Production edit or log-source operation is refused

This is intentional. Repeat the command with `--yes` only after verifying the selected project and environment:

```powershell
ward project info
ward --project sample --env production config edit --yes
```

## 14. Recommended support workflow

When a user reports that an application is unavailable or cannot authenticate:

```powershell
ward project use tunover
ward env use development
ward project info
ward health
ward diagnose observer
```

For an individual API issue:

```powershell
ward api GET /api/profile
ward logs --source tunover-web --level ERROR --last 30m
```

For a database-backed sample workflow:

```powershell
ward --project sample --env development doctor
ward --project sample --env development db status
ward --project sample --env development db query user_by_id 10291
ward --project sample --env development diagnose user --param user_id=10291
```

Capture the following in an escalation:

- WARD version.
- Project and environment.
- Health result.
- Diagnostic result.
- Relevant timestamps and reference IDs.
- Sanitized log export, when approved.

Do not include passwords, bearer tokens, cookies, raw authentication headers, or unreviewed production data.

## 15. Extending WARD with another project

To add a project without changing WARD's core code:

1. Create a new YAML file under `<config-directory>\projects`.
2. Define one or more environments.
3. Configure API base URLs and service checks.
4. Reference secrets through environment variables.
5. Add named read-only queries if database inspection is needed.
6. Add local or HTTP log sources.
7. Define diagnostic workflows and assertions.
8. Run `ward config validate`.
9. Run `ward project list` and select the new project.

The application logic remains in WARD; project-specific behavior belongs in configuration and, when necessary, future adapters.

## 16. Command reference

```text
ward
ward ui

ward project list
ward project use <project-id>
ward project info

ward env list
ward env use <environment-id>

ward health
ward api GET <path> [--header name=value] [--query name=value]

ward db status
ward db query <query-id> [parameters...]

ward logs [filters]
ward logs sources
ward logs add <id> --path <file-or-glob> [--format text|jsonl]
ward logs remove <id>
ward logs export --output <path> [--force]

ward diagnose <workflow> [--param name=value]

ward config show
ward config validate
ward config edit [--editor <executable>] [--yes]

ward doctor
ward version
```

Run command-specific help at any time:

```powershell
ward --help
ward logs --help
ward config edit --help
```
