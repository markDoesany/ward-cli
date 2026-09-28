package database

import (
	"testing"

	"github.com/ward/ward/internal/config"
)

func TestParseArguments(t *testing.T) {
	definition := config.QueryDefinition{Parameters: []config.QueryParameter{{Name: "id", Type: "int"}, {Name: "active", Type: "bool"}}}
	values, err := ParseArguments(definition, []string{"42", "true"})
	if err != nil {
		t.Fatal(err)
	}
	if values[0] != int64(42) || values[1] != true {
		t.Fatalf("unexpected values: %#v", values)
	}
}

func TestQueryRejectsMutatingSQL(t *testing.T) {
	for _, statement := range []string{"DELETE FROM users", "SELECT * FROM users INTO OUTFILE '/tmp/users'", "SELECT GET_LOCK('x', 1)", "SELECT 1; SELECT 2"} {
		if err := VerifyReadOnlySQL(statement); err == nil {
			t.Fatalf("expected %q to be rejected", statement)
		}
	}
	if err := VerifyReadOnlySQL("WITH active AS (SELECT id FROM users) SELECT * FROM active"); err != nil {
		t.Fatalf("expected read-only CTE: %v", err)
	}
}
