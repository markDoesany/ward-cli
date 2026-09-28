package audit

import (
	"os"
	"strings"
	"testing"
)

func TestRecordRedactsErrors(t *testing.T) {
	logger := New(t.TempDir())
	logger.Record(Entry{Command: "api", Status: "ERROR", Error: "Authorization: Bearer secret-token"})
	data, err := os.ReadFile(logger.Path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "secret-token") {
		t.Fatalf("audit log leaked secret: %s", data)
	}
}
