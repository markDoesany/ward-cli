# WARD

WARD (the Ward Observer Support System) is a project-agnostic Go CLI and terminal dashboard for safe, repeatable support engineering checks.

For the complete operator workflow, see the [WARD User Guide](docs/USER_GUIDE.md).

The v0.2 implementation provides:

- Multi-project and multi-environment YAML configuration.
- Concurrent HTTP, TCP, and MySQL health checks.
- Read-only API GET inspection with response limits and redaction.
- Named, typed, read-only MySQL queries.
- Local-file and HTTP log search with filters and sanitized JSONL export.
- YAML-backed local log-source registration, source listing/removal, and live file follow mode.
- Development profiles for TUNOVER, Stayio, and LokalStore API with expected-status health semantics.
- Ordered diagnostic workflows with typed parameters and findings.
- Safe configuration inspection, readiness checks, audit logging, and stable JSON envelopes.
- A Bubble Tea dashboard for health, API, database, logs, diagnostics, and configuration.

Write operations, raw SQL, incident reports, bundles, transaction tracing, and release verification remain deferred until their safety and project-adapter contracts are defined.

## Run locally

    go run . version
    go run . project list --config-dir .

Project files are discovered under projects/*.yaml in the selected config directory. The included sample is safe documentation and points to example.invalid.

    $cfg = Join-Path $env:APPDATA 'ward'
    New-Item -ItemType Directory -Force (Join-Path $cfg 'projects') | Out-Null
    Copy-Item projects\sample.yaml (Join-Path $cfg 'projects\sample.yaml')
    go run . project use sample
    go run . env use development
    go run . config validate
    go run . logs --config-dir $cfg
    go run . diagnose user --param user_id=10291 --config-dir $cfg

Running `ward` or `ward ui` in an interactive terminal opens the dashboard. In a redirected or non-TTY shell, the root command prints help.

## Commands

    ward
    ward ui
    ward project list|use|info
    ward env list|use
    ward health
    ward api GET /health --query check=full
    ward db status
    ward db query user_by_id 1001
    ward logs --last 30m --level ERROR --search timeout
    ward logs sources
    ward logs add application --path logs/application.log --format text
    ward logs --source application --follow
    ward logs remove application
    ward logs export --output errors.jsonl --force
    ward diagnose user --param user_id=10291
    ward diagnose observer --project tunover --env development
    ward config show|validate
    ward config edit --editor nano
    ward doctor
    ward version

Global flags include --project, --env, --json, --quiet, --verbose, --timeout, --max-rows, --max-bytes, --config-dir, and --state-dir.

Use `ward config edit` to edit the selected project's YAML from a terminal editor, similar to `sudo nano`. WARD copies the file to a temporary draft, launches `WARD_EDITOR`, `VISUAL`, `EDITOR`, `nano`, `vim`, or `vi`, validates the saved YAML, and atomically replaces the original only when valid. Use `--editor nano` to select an editor explicitly. Production edits require `--yes`.

Operational failures return exit code 1; usage and configuration failures return 2. A non-healthy health or diagnostic result also returns 1.

## Configuration

Credentials must be supplied through environment variables and are never stored in YAML. A minimal environment looks like this:

    schema_version: 1
    project:
      id: example
      name: Example Application
    environments:
      staging:
        kind: staging
        api:
          base_url: https://api.example.test
          auth:
            bearer_token_env: EXAMPLE_API_TOKEN
        services:
          - name: Core API
            type: http
            url: /health
        logs:
          sources:
            - id: application
              name: Application log
              type: file
              format: jsonl
              paths:
                - logs/*.jsonl
        diagnostics:
          user:
            parameters:
              - name: user_id
                type: int
            steps:
              - id: errors
                type: logs
                source: application
                filters:
                  user: ${user_id}
                  level: ERROR
            assertions:
              - step: errors
                selector: count
                operator: gt
                value: 0
                severity: warning
                issue: Recent errors were found.
                recommendation: Review the matching application events.

Supported log sources are configured local files and HTTP endpoints. HTTP sources have their own base URL, endpoint, optional auth/header environment references, query mappings, and field mappings. Searches default to the last 30 minutes and 500 entries.

Local log sources are registered in the selected project YAML. A path can be absolute or a glob; relative paths resolve relative to the project YAML file. WARD only reads existing files and never starts or supervises the application that writes them. `logs --follow` polls local files every 500 ms, handles truncation/replacement, and can be stopped with Ctrl+C. It is intentionally incompatible with `--json` for now.

For the included TUNOVER profile, the repository sample log can be registered for a local simulation:

    go run . --config-dir . --project tunover --env development logs add application --path sample-logs/application.log
    go run . --config-dir . --project tunover --env development logs --source application --last 1h
    go run . --config-dir . --project tunover --env development logs remove application

The `stayio` profile expects `http://localhost:8080/auth/session` to return 401 when unauthenticated; that is a healthy reachability result. The `lokalstore-api` profile reads its endpoint from `WARD_LOKALSTORE_API_URL` and checks `/api/auth/session` with the same expected 401 status. Its database profile is explicitly disabled until credentials and named queries are supplied.

The TUNOVER profile includes an `observer` diagnostic workflow. It checks the public web page, verifies that `/api/profile` is protected by expecting HTTP 401 without a session, and searches recent errors in `../../TUNOVER/logs/*.log`. Start the TUNOVER development server with output redirected to that log directory before running the workflow.

Diagnostics run steps in YAML order. A step can set continue_on_error: true; otherwise the workflow stops at the first failed step. Parameters are passed by name with repeated --param name=value flags. Only configured selectors and comparison operators are accepted.

## Safety model

- API inspection is GET only.
- Database access uses configured named queries only.
- Queries must begin with SELECT or WITH, contain one statement, and pass forbidden-operation checks.
- MySQL sessions use read-only transactions and production requires TLS.
- HTTP redirects are restricted to the configured origin.
- Response, log, audit, database, and JSON output are redacted for common secret fields and configured patterns.
- Log exports refuse to overwrite existing files unless --force is supplied and use restrictive permissions.
- Switching projects clears the environment selection; production is never selected implicitly.
- Default limits are 1,000 database rows, 5 MiB response bodies, and a 10-second operation timeout.
- Database profiles with `enabled: false` are accepted without credentials; omitted `enabled` keeps the original enabled-by-default behavior.
- WARD doctor checks configuration, state-directory writability, project definitions, required environment variables, and CA files.

## Development

With GNU Make installed, common workflows are shortened to:

    make run
    make run ARGS="version"
    make ui
    make build
    make install
    make test
    make check
    make cover
    make build-windows

On Windows, `make run` uses the repository-local configuration, selects the safe `tunover` development sample, and starts the dashboard. Override the defaults with `make run PROJECT=sample ENVIRONMENT=development` or `make run CONFIG_DIR=C:\path\to\config`.

`make build` creates `ward.exe` on Windows and `ward` on Linux and macOS.

    gofmt -w cmd internal
    go test ./...
    go vet ./...
    go test -cover ./...
    go build -o bin\ward.exe .

The MySQL integration test is skipped unless WARD_TEST_MYSQL_HOST and its companion variables are set. GitHub Actions runs unit/vet tests on Windows and Linux, the MySQL integration test on Linux, and cross-builds Windows, Linux, and macOS amd64 binaries.

Build metadata can be injected with:

    go build -ldflags "-X github.com/ward/ward/cmd.version=0.2.0 -X github.com/ward/ward/cmd.commit=local -X github.com/ward/ward/cmd.buildDate=2026-09-28T00:00:00Z" -o bin\ward.exe .
