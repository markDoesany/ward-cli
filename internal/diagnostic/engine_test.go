package diagnostic

import (
	"context"
	"testing"

	"github.com/ward/ward/internal/config"
)

type fakeExecutor struct{}

func (fakeExecutor) Health(context.Context, string) (any, error) {
	return map[string]any{"status": "HEALTHY"}, nil
}
func (fakeExecutor) APIGet(context.Context, string) (any, error) {
	return map[string]any{"status_code": 200}, nil
}
func (fakeExecutor) DatabaseQuery(context.Context, string, []string) (any, error) {
	return map[string]any{"row_count": 1}, nil
}
func (fakeExecutor) LogSearch(context.Context, string, map[string]string) (any, error) {
	return map[string]any{"count": 5}, nil
}

func TestRunProducesConfiguredFinding(t *testing.T) {
	definition := config.DiagnosticDefinition{
		Parameters: []config.QueryParameter{{Name: "user_id", Type: "int"}},
		Steps:      []config.DiagnosticStep{{ID: "errors", Type: "logs", Source: "app", Filters: map[string]string{"user": "${user_id}"}}},
		Assertions: []config.DiagnosticAssertion{{Step: "errors", Selector: "count", Operator: "gt", Value: 4, Severity: "warning", Issue: "Errors for ${user_id}"}},
	}
	result, err := Run(context.Background(), "user", definition, map[string]string{"user_id": "42"}, fakeExecutor{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "DEGRADED" || len(result.Findings) != 1 || result.Findings[0].Issue != "Errors for 42" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestRunRejectsUnknownParameter(t *testing.T) {
	definition := config.DiagnosticDefinition{Parameters: []config.QueryParameter{{Name: "id", Type: "int"}}, Steps: []config.DiagnosticStep{{ID: "health", Type: "health"}}}
	if _, err := Run(context.Background(), "x", definition, map[string]string{"id": "1", "extra": "2"}, fakeExecutor{}); err == nil {
		t.Fatal("expected unknown parameter error")
	}
}
