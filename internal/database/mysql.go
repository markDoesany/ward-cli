package database

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"fmt"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/ward/ward/internal/config"
	"github.com/ward/ward/internal/security"
)

type QueryResult struct {
	Columns   []string         `json:"columns"`
	Rows      []map[string]any `json:"rows"`
	RowCount  int              `json:"row_count"`
	Truncated bool             `json:"truncated,omitempty"`
}

type Runner struct {
	DB      *sql.DB
	Conn    *sql.Conn
	Timeout time.Duration
}

var tlsConfigID atomic.Uint64

var forbiddenSQL = regexp.MustCompile(`(?i)\b(insert|update|delete|drop|alter|truncate|create|grant|revoke|replace|load\s+data|call|into\s+(out|dump)file|for\s+update|lock\s+in\s+share\s+mode|get_lock|release_lock|sleep|benchmark|load_file)\b`)

func NewRunner(ctx context.Context, cfg config.DatabaseConfig, environmentKind string, timeout time.Duration) (*Runner, error) {
	if strings.ToLower(cfg.Driver) != "mysql" {
		return nil, fmt.Errorf("unsupported database driver %q", cfg.Driver)
	}
	host, err := resolve(cfg.Host, cfg.HostEnv, "database host")
	if err != nil {
		return nil, err
	}
	databaseName, err := resolve(cfg.Database, cfg.DatabaseEnv, "database name")
	if err != nil {
		return nil, err
	}
	username, err := resolve(cfg.Username, cfg.UsernameEnv, "database username")
	if err != nil {
		return nil, err
	}
	password := os.Getenv(cfg.PasswordEnv)
	if cfg.PasswordEnv == "" || password == "" {
		return nil, fmt.Errorf("required database password environment variable %s is missing", cfg.PasswordEnv)
	}
	port := cfg.Port
	if cfg.PortEnv != "" {
		raw := os.Getenv(cfg.PortEnv)
		if raw == "" {
			return nil, fmt.Errorf("required database port environment variable %s is missing", cfg.PortEnv)
		}
		port, err = strconv.Atoi(raw)
		if err != nil || port < 1 || port > 65535 {
			return nil, fmt.Errorf("database port environment variable %s is invalid", cfg.PortEnv)
		}
	}
	if port == 0 {
		port = 3306
	}
	if cfg.TLS == "" {
		cfg.TLS = "true"
	}
	if strings.EqualFold(cfg.TLS, "false") && strings.EqualFold(environmentKind, "production") {
		return nil, fmt.Errorf("insecure MySQL connections are not permitted for production")
	}
	if cfg.CACertPath != "" {
		data, readErr := os.ReadFile(cfg.CACertPath)
		if readErr != nil {
			return nil, fmt.Errorf("read MySQL CA bundle: %w", readErr)
		}
		pool, poolErr := x509.SystemCertPool()
		if poolErr != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(data) {
			return nil, fmt.Errorf("MySQL CA bundle contains no certificates")
		}
		name := fmt.Sprintf("ward-mysql-%d", tlsConfigID.Add(1))
		if err := mysql.RegisterTLSConfig(name, &tls.Config{RootCAs: pool, ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return nil, fmt.Errorf("register MySQL TLS: %w", err)
		}
		cfg.TLS = name
	}
	driverConfig := mysql.NewConfig()
	driverConfig.User = username
	driverConfig.Passwd = password
	driverConfig.Net = "tcp"
	driverConfig.Addr = net.JoinHostPort(host, strconv.Itoa(port))
	driverConfig.DBName = databaseName
	driverConfig.ParseTime = true
	driverConfig.Timeout = timeout
	driverConfig.ReadTimeout = timeout
	driverConfig.WriteTimeout = timeout
	driverConfig.TLSConfig = cfg.TLS
	connector, err := mysql.NewConnector(driverConfig)
	if err != nil {
		return nil, fmt.Errorf("configure MySQL connection: %w", err)
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	checkCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := db.Conn(checkCtx)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("MySQL connection failed: %w", err)
	}
	if err := conn.PingContext(checkCtx); err != nil {
		conn.Close()
		db.Close()
		return nil, fmt.Errorf("MySQL connection failed: %w", err)
	}
	if _, err := conn.ExecContext(checkCtx, "SET SESSION TRANSACTION READ ONLY"); err != nil {
		conn.Close()
		db.Close()
		return nil, fmt.Errorf("configure read-only MySQL session: %w", err)
	}
	return &Runner{DB: db, Conn: conn, Timeout: timeout}, nil
}

func (r *Runner) Close() error {
	if r == nil {
		return nil
	}
	var first error
	if r.Conn != nil {
		first = r.Conn.Close()
	}
	if r.DB != nil {
		if err := r.DB.Close(); first == nil {
			first = err
		}
	}
	return first
}

func (r *Runner) Query(ctx context.Context, definition config.QueryDefinition, args []any, maxRows int) (QueryResult, error) {
	if err := VerifyReadOnlySQL(definition.SQL); err != nil {
		return QueryResult{}, err
	}
	if len(args) != len(definition.Parameters) {
		return QueryResult{}, fmt.Errorf("expected %d parameters, received %d", len(definition.Parameters), len(args))
	}
	if maxRows <= 0 {
		maxRows = config.DefaultMaxRows
	}
	queryCtx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()
	tx, err := r.Conn.BeginTx(queryCtx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return QueryResult{}, fmt.Errorf("begin read-only transaction: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(queryCtx, strings.TrimSpace(definition.SQL), args...)
	if err != nil {
		return QueryResult{}, err
	}
	columns, err := rows.Columns()
	if err != nil {
		rows.Close()
		return QueryResult{}, err
	}
	result := QueryResult{Columns: columns, Rows: make([]map[string]any, 0)}
	for len(result.Rows) < maxRows && rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			rows.Close()
			return QueryResult{}, err
		}
		row := make(map[string]any, len(columns))
		for i, value := range values {
			if bytes, ok := value.([]byte); ok {
				row[columns[i]] = string(bytes)
			} else {
				row[columns[i]] = value
			}
		}
		result.Rows = append(result.Rows, row)
	}
	if rows.Next() {
		result.Truncated = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return QueryResult{}, err
	}
	if err := rows.Close(); err != nil {
		return QueryResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return QueryResult{}, err
	}
	result.RowCount = len(result.Rows)
	return result, nil
}

func VerifyReadOnlySQL(statement string) error {
	text := strings.TrimSpace(statement)
	trimmed := strings.TrimSpace(strings.TrimSuffix(text, ";"))
	lower := strings.ToLower(trimmed)
	if !strings.HasPrefix(lower, "select") && !strings.HasPrefix(lower, "with") {
		return fmt.Errorf("query must begin with SELECT or WITH")
	}
	if strings.Contains(trimmed, ";") {
		return fmt.Errorf("multiple SQL statements are not allowed")
	}
	if forbiddenSQL.MatchString(trimmed) {
		return fmt.Errorf("query contains a forbidden SQL operation")
	}
	return nil
}

func RedactResult(result QueryResult, redactor *security.Redactor) QueryResult {
	if redactor == nil {
		redactor = security.DefaultRedactor()
	}
	for _, row := range result.Rows {
		for column, value := range row {
			if redactor.IsSensitive(column) {
				row[column] = security.Mask(fmt.Sprint(value))
			} else {
				row[column] = redactor.RedactValue(value)
			}
		}
	}
	return result
}

func ParseArguments(definition config.QueryDefinition, values []string) ([]any, error) {
	if len(values) != len(definition.Parameters) {
		return nil, fmt.Errorf("expected %d parameters, received %d", len(definition.Parameters), len(values))
	}
	args := make([]any, len(values))
	for i, value := range values {
		switch strings.ToLower(definition.Parameters[i].Type) {
		case "string":
			args[i] = value
		case "int", "int64":
			parsed, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("parameter %s must be an integer", definition.Parameters[i].Name)
			}
			args[i] = parsed
		case "bool":
			parsed, err := strconv.ParseBool(value)
			if err != nil {
				return nil, fmt.Errorf("parameter %s must be boolean", definition.Parameters[i].Name)
			}
			args[i] = parsed
		case "time":
			parsed, err := time.Parse(time.RFC3339, value)
			if err != nil {
				return nil, fmt.Errorf("parameter %s must be RFC3339 time", definition.Parameters[i].Name)
			}
			args[i] = parsed
		default:
			return nil, fmt.Errorf("unsupported parameter type %q", definition.Parameters[i].Type)
		}
	}
	return args, nil
}

func resolve(value, envName, label string) (string, error) {
	if envName != "" {
		value = os.Getenv(envName)
		if value == "" {
			return "", fmt.Errorf("required %s environment variable %s is missing", label, envName)
		}
	}
	if value == "" {
		return "", fmt.Errorf("%s is not configured", label)
	}
	return value, nil
}
