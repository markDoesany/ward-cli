package security

import (
	"strings"
	"testing"
)

func TestRedactJSON(t *testing.T) {
	value := RedactJSON([]byte(`{"token":"secret","profile":{"email":"user@example.com"}}`))
	if !strings.Contains(string(value), `"token": "************"`) || !strings.Contains(string(value), `"email": "user@example.com"`) {
		t.Fatalf("unexpected redacted JSON: %s", value)
	}
}

func TestRedactHeaders(t *testing.T) {
	value := RedactHeaders(map[string]string{"Authorization": "Bearer secret", "Content-Type": "application/json"})
	if value["Authorization"] == "Bearer secret" || value["Content-Type"] != "application/json" {
		t.Fatalf("unexpected headers: %#v", value)
	}
}

func TestRedactPlainTextAndConfiguredPatterns(t *testing.T) {
	redactor, err := NewRedactor([]string{"session_id"}, []string{`account=[0-9]+`})
	if err != nil {
		t.Fatal(err)
	}
	value := redactor.RedactText("Authorization: Bearer abc.def password=hunter2 account=10291")
	if strings.Contains(value, "abc.def") || strings.Contains(value, "hunter2") || strings.Contains(value, "10291") {
		t.Fatalf("secret remained in %q", value)
	}
}
