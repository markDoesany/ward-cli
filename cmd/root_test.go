package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/ward/ward/internal/app"
)

func TestProjectUseNeverAutoSelectsProduction(t *testing.T) {
	configDir := t.TempDir()
	stateDir := t.TempDir()
	projectsDir := filepath.Join(configDir, "projects")
	if err := os.MkdirAll(projectsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	yaml := "schema_version: 1\nproject:\n  id: sample\n  name: Sample\nenvironments:\n  production:\n    kind: production\n    api:\n      base_url: https://example.com\n"
	if err := os.WriteFile(filepath.Join(projectsDir, "sample.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	a := app.New(app.Options{}, &stdout, &stderr)
	root := newRoot(a)
	root.SetArgs([]string{"--config-dir", configDir, "--state-dir", stateDir, "project", "use", "sample", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if a.Loaded.Project != "sample" || a.Loaded.Environment != "" {
		t.Fatalf("unexpected selection: %#v", a.Loaded)
	}
}

func TestExplicitProjectCannotReuseAnotherProjectsEnvironment(t *testing.T) {
	configDir := t.TempDir()
	stateDir := t.TempDir()
	projectsDir := filepath.Join(configDir, "projects")
	if err := os.MkdirAll(projectsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, project := range []string{"one", "two"} {
		yaml := "schema_version: 1\nproject:\n  id: " + project + "\n  name: " + project + "\nenvironments:\n  production:\n    kind: production\n    api:\n      base_url: https://example.com\n"
		if err := os.WriteFile(filepath.Join(projectsDir, project+".yaml"), []byte(yaml), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var stdout, stderr bytes.Buffer
	a := app.New(app.Options{ConfigDir: configDir, StateDir: stateDir}, &stdout, &stderr)
	if err := a.Prepare(); err != nil {
		t.Fatal(err)
	}
	if err := a.SaveSelection("one", "production"); err != nil {
		t.Fatal(err)
	}
	a.Options.Project = "two"
	if _, _, _, _, err := a.RequireEnvironment(); err == nil {
		t.Fatal("expected explicit project selection to require a new environment")
	}
}
