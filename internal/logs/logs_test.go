package logs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ward/ward/internal/config"
)

func TestSearchFilesFiltersAndRedacts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	data := "INFO user=7 started\nERROR user=42 token=secret failed\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	env := config.EnvironmentConfig{Logs: config.LogConfig{Sources: []config.LogSourceConfig{{ID: "app", Name: "App", Type: "file", Format: "text", Paths: []string{"*.log"}}}}}
	result, err := Search(context.Background(), env, dir, []string{"app"}, Filter{Level: "ERROR", User: "42", Limit: 10}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if result.Count != 1 || strings.Contains(result.Entries[0].Message, "secret") {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestExportRefusesOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs.jsonl")
	result := SearchResult{Entries: []Entry{{Source: "app", Message: "safe"}}, Count: 1}
	if err := ExportJSONL(path, result, false); err != nil {
		t.Fatal(err)
	}
	if err := ExportJSONL(path, result, false); err == nil {
		t.Fatal("expected overwrite refusal")
	}
	if err := ExportJSONL(path, result, true); err != nil {
		t.Fatal(err)
	}
}

func TestFollowEmitsNewLinesAndHandlesTruncation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	if err := os.WriteFile(path, []byte("2026-09-28T10:00:00Z INFO before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := config.EnvironmentConfig{Logs: config.LogConfig{Sources: []config.LogSourceConfig{{ID: "app", Name: "App", Type: "file", Format: "text", Paths: []string{path}}}}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	entries := make(chan Entry, 1)
	followErr := make(chan error, 1)
	go func() {
		followErr <- Follow(ctx, env, dir, []string{"app"}, Filter{Limit: 10}, func(entry Entry) error {
			entries <- entry
			return context.Canceled
		})
	}()
	time.Sleep(650 * time.Millisecond)
	if err := os.WriteFile(path, []byte("2026-09-28T10:00:01Z ERROR after\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case entry := <-entries:
		if entry.Level != "ERROR" || entry.Message == "" || !strings.Contains(entry.Message, "after") {
			t.Fatalf("unexpected followed entry: %#v", entry)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for followed entry")
	}
	select {
	case err := <-followErr:
		if err != context.Canceled {
			t.Fatalf("unexpected follow error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("follow did not stop after callback cancellation")
	}
}

func TestSearchReportsMissingConfiguredFiles(t *testing.T) {
	env := config.EnvironmentConfig{Logs: config.LogConfig{Sources: []config.LogSourceConfig{{ID: "app", Name: "App", Type: "file", Format: "text", Paths: []string{"missing/*.log"}}}}}
	result, err := Search(context.Background(), env, t.TempDir(), []string{"app"}, Filter{Limit: 10}, time.Second)
	if err == nil || result.Errors["app"] == "" {
		t.Fatalf("expected missing log source error, result=%#v err=%v", result, err)
	}
}
