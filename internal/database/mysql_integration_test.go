package database

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/ward/ward/internal/config"
)

// Set WARD_TEST_MYSQL_HOST to enable this test. It is intentionally
// skipped on developer machines and runs against the MySQL service in CI.
func TestMySQLReadOnlyIntegration(t *testing.T) {
	host := os.Getenv("WARD_TEST_MYSQL_HOST")
	if host == "" {
		t.Skip("WARD_TEST_MYSQL_HOST is not set")
	}
	port := 3306
	if value := os.Getenv("WARD_TEST_MYSQL_PORT"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			t.Fatal(err)
		}
		port = parsed
	}
	value := func(name string) string {
		result := os.Getenv(name)
		if result == "" {
			t.Fatalf("%s is required when MySQL integration is enabled", name)
		}
		return result
	}
	cfg := config.DatabaseConfig{
		Driver:      "mysql",
		Host:        host,
		Port:        port,
		Database:    value("WARD_TEST_MYSQL_DATABASE"),
		Username:    value("WARD_TEST_MYSQL_USER"),
		PasswordEnv: "WARD_TEST_MYSQL_PASSWORD",
		TLS:         "false",
	}
	if os.Getenv(cfg.PasswordEnv) == "" {
		t.Fatal("WARD_TEST_MYSQL_PASSWORD is required when MySQL integration is enabled")
	}
	runner, err := NewRunner(context.Background(), cfg, "development", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	result, err := runner.Query(context.Background(), config.QueryDefinition{
		SQL:        "SELECT ? AS supplied_value",
		Parameters: []config.QueryParameter{{Name: "value", Type: "string"}},
	}, []any{"ward"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if result.RowCount != 1 || result.Rows[0]["supplied_value"] != "ward" {
		t.Fatalf("unexpected query result: %#v", result)
	}
	readOnly, err := runner.Query(context.Background(), config.QueryDefinition{SQL: "SELECT @@session.transaction_read_only AS read_only"}, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(readOnly.Rows) != 1 {
		t.Fatalf("unexpected read-only query result: %#v", readOnly)
	}
	readOnlyValue := ""
	switch value := readOnly.Rows[0]["read_only"].(type) {
	case int64:
		readOnlyValue = strconv.FormatInt(value, 10)
	case string:
		readOnlyValue = value
	case []byte:
		readOnlyValue = string(value)
	}
	if readOnlyValue != "1" {
		t.Fatalf("MySQL session is not read-only: %#v", readOnly)
	}
}
