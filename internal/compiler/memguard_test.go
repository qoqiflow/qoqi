package compiler

import (
	"os"
	"testing"

	"github.com/qoqiflow/qoqi/internal/memguard"
)

// TestMain guards the tests: they run programs, and a program that takes
// memory without end must end the tests, not the machine they run on.
func TestMain(m *testing.M) {
	memguard.EnforceInTests()
	os.Exit(m.Run())
}
