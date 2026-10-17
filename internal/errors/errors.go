package errors

import (
	"fmt"
	"strings"

	"github.com/qoqiflow/qoqi/internal/token"
)

// Severity indicates the seriousness of the diagnostic.
type Severity int

const (
	SeverityInfo Severity = iota
	SeverityWarning
	SeverityError
)

func (s Severity) String() string {
	switch s {
	case SeverityInfo:
		return "INFO"
	case SeverityWarning:
		return "WARNING"
	case SeverityError:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

// Highlight represents a range of text to underline in the context.
type Highlight struct {
	StartCol int
	EndCol   int // Exclusive
}

// Diagnostic represents a single compiler message.
type Diagnostic struct {
	Severity   Severity
	Code       string
	Message    string
	Pos        token.Position
	Context    string      // The source line content
	Highlights []Highlight // Specific ranges to underline
	Suggestion string      // Optional fix suggestion

	// Function names the function the position is in, when that helps:
	// a failure of a running program is found by its function as much as
	// by its line.
	Function string

	// Notes are lines printed after everything else, such as the calls
	// that led to a failure.
	Notes []string
}

func (d Diagnostic) String() string {
	var sb strings.Builder

	// Header: [ERROR] E1001: undefined variable 'count'
	codeStr := ""
	if d.Code != "" {
		codeStr = " " + d.Code
	}
	sb.WriteString(fmt.Sprintf("[%s]%s: %s\n", d.Severity, codeStr, d.Message))

	// Location: --> src/main.qi:10:5
	sb.WriteString(fmt.Sprintf("  --> %s", d.Pos))
	if d.Function != "" {
		sb.WriteString(fmt.Sprintf(" in function '%s'", d.Function))
	}
	sb.WriteString("\n")

	if d.Context != "" {
		lineNumStr := fmt.Sprintf("%d", d.Pos.Line)
		padding := strings.Repeat(" ", len(lineNumStr))

		sb.WriteString(fmt.Sprintf(" %s |\n", padding))
		sb.WriteString(fmt.Sprintf(" %s | %s\n", lineNumStr, d.Context))

		// Highlights
		sb.WriteString(fmt.Sprintf(" %s | ", padding))
		if len(d.Highlights) > 0 {
			// Find max end col to size the buffer
			maxCol := 0
			for _, h := range d.Highlights {
				if h.EndCol > maxCol {
					maxCol = h.EndCol
				}
			}

			// Create underline buffer
			underline := make([]byte, maxCol)
			for i := range underline {
				underline[i] = ' '
			}

			for _, h := range d.Highlights {
				start := h.StartCol
				end := h.EndCol
				// Assume 1-based columns
				if start < 1 {
					start = 1
				}

				for i := start; i < end; i++ {
					if i-1 < len(underline) {
						underline[i-1] = '^'
					}
				}
			}
			sb.Write(underline)
		} else if d.Pos.Col > 0 {
			// Fallback: point to the start column
			if d.Pos.Col > 1 {
				sb.WriteString(strings.Repeat(" ", d.Pos.Col-1))
			}
			sb.WriteString("^")
		}
		sb.WriteString("\n")
	}

	if d.Suggestion != "" {
		lineNumStr := fmt.Sprintf("%d", d.Pos.Line)
		padding := strings.Repeat(" ", len(lineNumStr))
		sb.WriteString(fmt.Sprintf(" %s |\n", padding))
		sb.WriteString(fmt.Sprintf(" %s = Suggestion: %s\n", padding, d.Suggestion))
	}
	for _, note := range d.Notes {
		sb.WriteString("  " + note + "\n")
	}
	return sb.String()
}

// Handler collects diagnostics during compilation.
type Handler struct {
	Diagnostics []Diagnostic

	// Source text of the files being compiled, by file name. A program
	// made of several modules has one entry per file, so a diagnostic
	// shows the line of the file it is about.
	sources map[string]map[int]string
}

func NewHandler() *Handler {
	return &Handler{
		Diagnostics: make([]Diagnostic, 0),
	}
}

// AddSource registers the lines of a file (1-based). Diagnostics positioned
// in that file take their source line from here, whatever lines the
// reporting stage passes.
func (h *Handler) AddSource(file string, lines map[int]string) {
	if h.sources == nil {
		h.sources = make(map[string]map[int]string)
	}
	h.sources[file] = lines
}

// SourceLine returns a line of a registered file.
func (h *Handler) SourceLine(file string, line int) (string, bool) {
	text, ok := h.sources[file][line]
	return text, ok
}

// linesFor picks the lines a diagnostic at tok should quote.
func (h *Handler) linesFor(tok token.Token, fallback map[int]string) map[int]string {
	if lines, ok := h.sources[tok.Pos.File]; ok {
		return lines
	}
	return fallback
}

func (h *Handler) Add(d Diagnostic) {
	h.Diagnostics = append(h.Diagnostics, d)
}

func (h *Handler) HasErrors() bool {
	for _, d := range h.Diagnostics {
		if d.Severity == SeverityError {
			return true
		}
	}
	return false
}

// ReportErrorFromToken adds an error diagnostic with automatic context from a token.
func (h *Handler) ReportErrorFromToken(code, msg string, tok token.Token, sourceLines map[int]string) {
	h.Add(NewDiagnosticFromToken(SeverityError, code, msg, tok, h.linesFor(tok, sourceLines)))
}

// ReportWarningFromToken adds a warning diagnostic with automatic context from a token.
func (h *Handler) ReportWarningFromToken(code, msg string, tok token.Token, sourceLines map[int]string) {
	h.Add(NewDiagnosticFromToken(SeverityWarning, code, msg, tok, h.linesFor(tok, sourceLines)))
}

// ReportWarningWithSuggestion adds a warning diagnostic with a suggestion.
func (h *Handler) ReportWarningWithSuggestion(code, msg, suggestion string, tok token.Token, sourceLines map[int]string) {
	d := NewDiagnosticFromToken(SeverityWarning, code, msg, tok, h.linesFor(tok, sourceLines))
	d.Suggestion = suggestion
	h.Add(d)
}

// ReportErrorWithSuggestion adds an error diagnostic with a suggestion.
func (h *Handler) ReportErrorWithSuggestion(code, msg, suggestion string, tok token.Token, sourceLines map[int]string) {
	d := NewDiagnosticFromToken(SeverityError, code, msg, tok, h.linesFor(tok, sourceLines))
	d.Suggestion = suggestion
	h.Add(d)
}

// NewDiagnosticFromToken creates a diagnostic from a token with automatic context extraction.
// sourceLines should contain all source lines indexed by line number (1-based).
func NewDiagnosticFromToken(severity Severity, code, msg string, tok token.Token, sourceLines map[int]string) Diagnostic {
	d := Diagnostic{
		Severity: severity,
		Code:     code,
		Message:  msg,
		Pos:      tok.Pos,
	}

	// Extract context line
	if line, ok := sourceLines[tok.Pos.Line]; ok {
		d.Context = line

		// Calculate highlights from token span
		if tok.EndPos.Line == tok.Pos.Line {
			d.Highlights = []Highlight{
				{StartCol: tok.Pos.Col, EndCol: tok.EndPos.Col},
			}
		}
	}

	return d
}
