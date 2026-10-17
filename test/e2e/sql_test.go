package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

// The depot of cases/sql_depot.qi on the servers QOQI_TEST_POSTGRES
// and QOQI_TEST_MYSQL give, when they give one: it prints what it
// prints on SQLite, the same rows, numbers and refusals.
func TestSQLOnEveryDatabase(t *testing.T) {
	want, err := os.ReadFile(filepath.Join(repoRoot, "test", "e2e", "cases", "sql_depot.out"))
	if err != nil {
		t.Fatal(err)
	}
	ran := 0
	for _, env := range []string{"QOQI_TEST_POSTGRES", "QOQI_TEST_MYSQL"} {
		address := os.Getenv(env)
		if address == "" {
			continue
		}
		ran++
		t.Run(env, func(t *testing.T) {
			res := runQoqi(t, "run", "test/e2e/cases/sql_depot.qi", address)
			if res.exit != 0 || res.stdout != string(want) {
				t.Errorf("exit %d\n%s\nexpected what SQLite prints:\n%s%s", res.exit, res.stdout, want, res.stderr)
			}
		})
	}
	if ran == 0 {
		t.Skip("no server: QOQI_TEST_POSTGRES and QOQI_TEST_MYSQL give none")
	}
}
