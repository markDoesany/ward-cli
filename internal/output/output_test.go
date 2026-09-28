package output

import (
	"bytes"
	"strings"
	"testing"
)

func TestWriteJSONAddsSchemaVersion(t *testing.T) {
	var buffer bytes.Buffer
	if err := WriteJSON(&buffer, Envelope{OK: true, Command: "test"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buffer.String(), `"schema_version": 1`) {
		t.Fatalf("missing schema version: %s", buffer.String())
	}
}

func TestErrorInfoRedactsSecrets(t *testing.T) {
	var buffer bytes.Buffer
	if err := WriteJSON(&buffer, Envelope{OK: false, Error: ErrorInfoFrom(OperationalError("request failed", "Authorization: Bearer secret-token"))}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buffer.String(), "secret-token") {
		t.Fatalf("JSON error leaked secret: %s", buffer.String())
	}
}
