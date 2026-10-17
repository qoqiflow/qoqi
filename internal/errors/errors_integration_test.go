package errors

import (
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/token"
)

func TestNewDiagnosticFromToken(t *testing.T) {
	// Setup source lines
	sourceLines := map[int]string{
		1: "let count = 10;",
		2: "let result = count + unknown;",
		3: "return result;",
	}

	// Create a token for "unknown" identifier
	tok := token.Token{
		Type:    token.IDENT,
		Literal: "unknown",
		Pos:     token.Position{File: "test.qi", Line: 2, Col: 22},
		EndPos:  token.Position{File: "test.qi", Line: 2, Col: 29},
	}

	// Create diagnostic from token
	diag := NewDiagnosticFromToken(
		SeverityError,
		"E1001",
		"undefined variable 'unknown'",
		tok,
		sourceLines,
	)

	// Verify diagnostic properties
	if diag.Severity != SeverityError {
		t.Errorf("Expected SeverityError, got %v", diag.Severity)
	}
	if diag.Code != "E1001" {
		t.Errorf("Expected code E1001, got %s", diag.Code)
	}
	if diag.Message != "undefined variable 'unknown'" {
		t.Errorf("Expected message about undefined variable, got %s", diag.Message)
	}
	if diag.Pos.Line != 2 {
		t.Errorf("Expected line 2, got %d", diag.Pos.Line)
	}
	if diag.Context != "let result = count + unknown;" {
		t.Errorf("Expected context line, got %s", diag.Context)
	}

	// Verify highlights
	if len(diag.Highlights) != 1 {
		t.Fatalf("Expected 1 highlight, got %d", len(diag.Highlights))
	}
	if diag.Highlights[0].StartCol != 22 {
		t.Errorf("Expected highlight start at 22, got %d", diag.Highlights[0].StartCol)
	}
	if diag.Highlights[0].EndCol != 29 {
		t.Errorf("Expected highlight end at 29, got %d", diag.Highlights[0].EndCol)
	}

	// Verify formatted output contains the highlight
	output := diag.String()
	if !strings.Contains(output, "unknown") {
		t.Error("Output should contain the token literal")
	}
	if !strings.Contains(output, "^^^^^^^") {
		t.Error("Output should contain highlight markers for the token span")
	}
}

func TestReportErrorFromToken(t *testing.T) {
	handler := NewHandler()

	sourceLines := map[int]string{
		1: "fn main() -> Int {",
		2: "    let x = y + 1;",
		3: "}",
	}

	tok := token.Token{
		Type:    token.IDENT,
		Literal: "y",
		Pos:     token.Position{File: "main.qi", Line: 2, Col: 13},
		EndPos:  token.Position{File: "main.qi", Line: 2, Col: 14},
	}

	handler.ReportErrorFromToken("E1001", "undefined variable 'y'", tok, sourceLines)

	if len(handler.Diagnostics) != 1 {
		t.Fatalf("Expected 1 diagnostic, got %d", len(handler.Diagnostics))
	}

	diag := handler.Diagnostics[0]
	if diag.Severity != SeverityError {
		t.Errorf("Expected SeverityError, got %v", diag.Severity)
	}
	if diag.Context != "    let x = y + 1;" {
		t.Errorf("Expected context line, got %s", diag.Context)
	}
}

func TestReportWarningFromToken(t *testing.T) {
	handler := NewHandler()

	sourceLines := map[int]string{
		1: "let unused = 42;",
	}

	tok := token.Token{
		Type:    token.IDENT,
		Literal: "unused",
		Pos:     token.Position{File: "test.qi", Line: 1, Col: 5},
		EndPos:  token.Position{File: "test.qi", Line: 1, Col: 11},
	}

	handler.ReportWarningFromToken("W2001", "variable 'unused' is never used", tok, sourceLines)

	if len(handler.Diagnostics) != 1 {
		t.Fatalf("Expected 1 diagnostic, got %d", len(handler.Diagnostics))
	}

	diag := handler.Diagnostics[0]
	if diag.Severity != SeverityWarning {
		t.Errorf("Expected SeverityWarning, got %v", diag.Severity)
	}
	if diag.Code != "W2001" {
		t.Errorf("Expected code W2001, got %s", diag.Code)
	}
}

func TestDiagnosticFromTokenMultilineSpan(t *testing.T) {
	// Test token spanning multiple lines (EndPos on different line)
	sourceLines := map[int]string{
		1: "let result = compute(",
		2: "    x, y",
		3: ");",
	}

	// Token for "compute" function call
	tok := token.Token{
		Type:    token.IDENT,
		Literal: "compute",
		Pos:     token.Position{File: "test.qi", Line: 1, Col: 14},
		EndPos:  token.Position{File: "test.qi", Line: 3, Col: 2}, // Different line
	}

	diag := NewDiagnosticFromToken(
		SeverityError,
		"E3001",
		"function 'compute' is not defined",
		tok,
		sourceLines,
	)

	// When token spans multiple lines, highlights should be empty
	if len(diag.Highlights) != 0 {
		t.Errorf("Expected no highlights for multiline token, got %d", len(diag.Highlights))
	}

	// Context should still be the starting line
	if diag.Context != "let result = compute(" {
		t.Errorf("Expected context from starting line, got %s", diag.Context)
	}
}

func TestDiagnosticFromTokenNoSourceLines(t *testing.T) {
	// Test diagnostic creation when source lines are not available
	tok := token.Token{
		Type:    token.IDENT,
		Literal: "missing",
		Pos:     token.Position{File: "test.qi", Line: 5, Col: 10},
		EndPos:  token.Position{File: "test.qi", Line: 5, Col: 17},
	}

	// Empty source lines map
	diag := NewDiagnosticFromToken(
		SeverityError,
		"E1001",
		"undefined variable 'missing'",
		tok,
		map[int]string{},
	)

	// Context should be empty
	if diag.Context != "" {
		t.Errorf("Expected empty context, got %s", diag.Context)
	}

	// No highlights
	if len(diag.Highlights) != 0 {
		t.Errorf("Expected no highlights, got %d", len(diag.Highlights))
	}

	// But position should still be set
	if diag.Pos.Line != 5 {
		t.Errorf("Expected line 5, got %d", diag.Pos.Line)
	}
}

func TestMultipleDiagnosticsFromTokens(t *testing.T) {
	handler := NewHandler()

	sourceLines := map[int]string{
		1: "let x = a + b;",
		2: "let y = c * d;",
	}

	// Error for 'a'
	tokA := token.Token{
		Type:    token.IDENT,
		Literal: "a",
		Pos:     token.Position{File: "test.qi", Line: 1, Col: 9},
		EndPos:  token.Position{File: "test.qi", Line: 1, Col: 10},
	}
	handler.ReportErrorFromToken("E1001", "undefined variable 'a'", tokA, sourceLines)

	// Error for 'b'
	tokB := token.Token{
		Type:    token.IDENT,
		Literal: "b",
		Pos:     token.Position{File: "test.qi", Line: 1, Col: 13},
		EndPos:  token.Position{File: "test.qi", Line: 1, Col: 14},
	}
	handler.ReportErrorFromToken("E1001", "undefined variable 'b'", tokB, sourceLines)

	// Warning for 'y'
	tokY := token.Token{
		Type:    token.IDENT,
		Literal: "y",
		Pos:     token.Position{File: "test.qi", Line: 2, Col: 5},
		EndPos:  token.Position{File: "test.qi", Line: 2, Col: 6},
	}
	handler.ReportWarningFromToken("W2001", "variable 'y' is never used", tokY, sourceLines)

	if len(handler.Diagnostics) != 3 {
		t.Fatalf("Expected 3 diagnostics, got %d", len(handler.Diagnostics))
	}

	// Check that HasErrors still works
	if !handler.HasErrors() {
		t.Error("Handler should have errors")
	}

	// Verify order and types
	if handler.Diagnostics[0].Severity != SeverityError {
		t.Error("First diagnostic should be error")
	}
	if handler.Diagnostics[1].Severity != SeverityError {
		t.Error("Second diagnostic should be error")
	}
	if handler.Diagnostics[2].Severity != SeverityWarning {
		t.Error("Third diagnostic should be warning")
	}
}
