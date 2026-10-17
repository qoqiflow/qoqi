package errors

import (
	"testing"

	"github.com/qoqiflow/qoqi/internal/token"
)

func TestDiagnosticString(t *testing.T) {
	tests := []struct {
		name     string
		diag     Diagnostic
		expected string
	}{
		{
			name: "Simple Error",
			diag: Diagnostic{
				Severity: SeverityError,
				Code:     "E1001",
				Message:  "undefined variable 'count'",
				Pos: token.Position{
					File: "src/main.qi",
					Line: 10,
					Col:  5,
				},
			},
			expected: `[ERROR] E1001: undefined variable 'count'
  --> src/main.qi:10:5
`,
		},
		{
			name: "Error with Context and Highlight",
			diag: Diagnostic{
				Severity: SeverityError,
				Code:     "E1001",
				Message:  "undefined variable 'count'",
				Pos: token.Position{
					File: "src/main.qi",
					Line: 10,
					Col:  5,
				},
				Context: "    print(cont)",
				Highlights: []Highlight{
					{StartCol: 11, EndCol: 15},
				},
				Suggestion: "Did you mean 'count'?",
			},
			expected: `[ERROR] E1001: undefined variable 'count'
  --> src/main.qi:10:5
    |
 10 |     print(cont)
    |           ^^^^ 
    |
    = Suggestion: Did you mean 'count'?
`,
		},
		{
			name: "Error with Context and Point Highlight",
			diag: Diagnostic{
				Severity: SeverityError,
				Code:     "P0001",
				Message:  "unexpected token",
				Pos: token.Position{
					File: "src/main.qi",
					Line: 5,
					Col:  10,
				},
				Context: "if x > 10",
			},
			expected: `[ERROR] P0001: unexpected token
  --> src/main.qi:5:10
   |
 5 | if x > 10
   |          ^
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.diag.String()
			// Normalize line endings for comparison if needed, but simple string compare should work
			// if we are careful with the expected string format.
			if got != tt.expected {
				t.Errorf("Diagnostic.String() mismatch:\nGot:\n%q\nExpected:\n%q\n\nVisual Got:\n%s\nVisual Expected:\n%s", got, tt.expected, got, tt.expected)
			}
		})
	}
}

func TestHandler(t *testing.T) {
	h := NewHandler()

	if h.HasErrors() {
		t.Error("New handler should not have errors")
	}

	h.ReportErrorFromToken("E001", "Test Error", token.Token{
		Pos: token.Position{File: "test.qi", Line: 1, Col: 1},
	}, nil)

	if !h.HasErrors() {
		t.Error("Handler should have errors after ReportError")
	}

	if len(h.Diagnostics) != 1 {
		t.Errorf("Expected 1 diagnostic, got %d", len(h.Diagnostics))
	}

	d := h.Diagnostics[0]
	if d.Severity != SeverityError {
		t.Errorf("Expected SeverityError, got %v", d.Severity)
	}
	if d.Code != "E001" {
		t.Errorf("Expected code E001, got %s", d.Code)
	}
}
