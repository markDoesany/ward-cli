package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveAPIBaseURLFromEnvironment(t *testing.T) {
	t.Setenv("WARD_TEST_API_URL", "http://localhost:9999/")
	value, err := ResolveAPIBaseURL(APIConfig{BaseURLEnv: "WARD_TEST_API_URL"})
	if err != nil || value != "http://localhost:9999" {
		t.Fatalf("unexpected resolved URL %q: %v", value, err)
	}
}

func TestValidateAllowsDisabledDatabaseWithoutCredentials(t *testing.T) {
	enabled := false
	cfg := ProjectConfig{SchemaVersion: 1, Project: ProjectMetadata{ID: "p", Name: "P"}, Environments: map[string]EnvironmentConfig{
		"dev": {Kind: "development", Database: &DatabaseConfig{Enabled: &enabled, Driver: "mysql"}},
	}}
	if err := Validate(cfg); err != nil {
		t.Fatalf("disabled database should not require credentials: %v", err)
	}
}

func TestAddAndRemoveFileLogSourcePreservesConfiguration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "project.yaml")
	contents := "# keep this comment\nschema_version: 1\nproject:\n  id: sample\n  name: Sample\nenvironments:\n  dev:\n    kind: development\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	source := LogSourceConfig{ID: "app", Name: "App", Type: "file", Format: "text", Paths: []string{"logs/*.log"}}
	if err := AddFileLogSource(path, "dev", source); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadFile(path)
	if err != nil || len(loaded.Environments["dev"].Logs.Sources) != 1 {
		t.Fatalf("source was not added: %#v %v", loaded, err)
	}
	if err := AddFileLogSource(path, "dev", source); err == nil {
		t.Fatal("expected duplicate source error")
	}
	if err := RemoveLogSource(path, "dev", "app"); err != nil {
		t.Fatal(err)
	}
	loaded, err = LoadFile(path)
	if err != nil || len(loaded.Environments["dev"].Logs.Sources) != 0 {
		t.Fatalf("source was not removed: %#v %v", loaded, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "# keep this comment") {
		t.Fatalf("unrelated YAML content was not preserved: %s (%v)", data, err)
	}
}

func TestLoadFileRejectsUnknownFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "project.yaml")
	contents := "schema_version: 1\nproject:\n  id: sample\n  name: Sample\nenvironments:\n  dev:\n    kind: development\n    unexpected: true\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(path); err == nil {
		t.Fatal("expected unknown field validation error")
	}
}

func TestDiscoverRejectsDuplicateIDs(t *testing.T) {
	dir := t.TempDir()
	projects := filepath.Join(dir, "projects")
	if err := os.MkdirAll(projects, 0o700); err != nil {
		t.Fatal(err)
	}
	contents := "schema_version: 1\nproject:\n  id: same\n  name: Same\nenvironments:\n  dev:\n    kind: development\n"
	for _, name := range []string{"a.yaml", "b.yaml"} {
		if err := os.WriteFile(filepath.Join(projects, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := Discover(dir); err == nil {
		t.Fatal("expected duplicate project error")
	}
}

func TestValidateRejectsPlaintextSensitiveHeader(t *testing.T) {
	cfg := ProjectConfig{SchemaVersion: 1, Project: ProjectMetadata{ID: "p", Name: "P"}, Environments: map[string]EnvironmentConfig{"dev": {Kind: "development", API: APIConfig{BaseURL: "https://example.com", Headers: []HeaderConfig{{Name: "Authorization", Value: "secret"}}}}}}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected sensitive header validation error")
	}
}

func TestValidateRejectsCredentialsInBaseURL(t *testing.T) {
	cfg := ProjectConfig{SchemaVersion: 1, Project: ProjectMetadata{ID: "p", Name: "P"}, Environments: map[string]EnvironmentConfig{"dev": {Kind: "development", API: APIConfig{BaseURL: "https://user:password@example.com"}}}}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected URL credentials validation error")
	}
}

func TestValidateAppliesConfiguredRedactionToHeaders(t *testing.T) {
	cfg := ProjectConfig{
		SchemaVersion: 1,
		Project:       ProjectMetadata{ID: "p", Name: "P"},
		Environments: map[string]EnvironmentConfig{"dev": {
			Kind:      "development",
			API:       APIConfig{BaseURL: "https://example.com", Headers: []HeaderConfig{{Name: "X-Session", Value: "secret"}}},
			Redaction: RedactionConfig{Fields: []string{"X-Session"}},
		}},
	}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected configured sensitive header validation error")
	}
}

func TestValidateRejectsUnknownDiagnosticTemplate(t *testing.T) {
	cfg := ProjectConfig{
		SchemaVersion: 1,
		Project:       ProjectMetadata{ID: "p", Name: "P"},
		Environments: map[string]EnvironmentConfig{"dev": {
			Kind: "development",
			Logs: LogConfig{Sources: []LogSourceConfig{{ID: "app", Name: "App", Type: "file", Format: "text", Paths: []string{"app.log"}}}},
			Diagnostics: map[string]DiagnosticDefinition{"check": {
				Parameters: []QueryParameter{{Name: "id", Type: "int"}},
				Steps:      []DiagnosticStep{{ID: "logs", Type: "logs", Source: "app", Filters: map[string]string{"user": "${missing}"}}},
			}},
		}},
	}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected unknown diagnostic template error")
	}
}
