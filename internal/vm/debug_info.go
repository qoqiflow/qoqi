package vm

import "fmt"

// DebugInfo contains comprehensive debug information for an instruction.
// This allows for detailed error messages and debugging capabilities.
type DebugInfo struct {
	// Source location
	FilePath string // Path to the source file
	Line     int    // Line number (1-indexed)
	Column   int    // Column number (1-indexed)

	// Context
	SourceLine string // The actual source line text
	FuncName   string // Name of the function this instruction belongs to

	// Instruction metadata
	Description string // Human-readable description of what this instruction does
	Notes       string // Additional notes or context
}

// NewDebugInfo creates a new DebugInfo with basic source location.
func NewDebugInfo(filePath string, line, column int) *DebugInfo {
	return &DebugInfo{
		FilePath: filePath,
		Line:     line,
		Column:   column,
	}
}

// WithSourceLine adds the source line text to the debug info.
func (d *DebugInfo) WithSourceLine(sourceLine string) *DebugInfo {
	d.SourceLine = sourceLine
	return d
}

// WithFuncName adds the function name to the debug info.
func (d *DebugInfo) WithFuncName(funcName string) *DebugInfo {
	d.FuncName = funcName
	return d
}

// WithDescription adds a human-readable description to the debug info.
func (d *DebugInfo) WithDescription(description string) *DebugInfo {
	d.Description = description
	return d
}

// WithNotes adds additional notes to the debug info.
func (d *DebugInfo) WithNotes(notes string) *DebugInfo {
	d.Notes = notes
	return d
}

// Format returns a formatted string representation of the debug info.
func (d *DebugInfo) Format() string {
	if d == nil {
		return ""
	}

	var result string

	// Add location
	if d.FilePath != "" {
		result += fmt.Sprintf("  at %s:%d:%d", d.FilePath, d.Line, d.Column)
		if d.FuncName != "" {
			result += fmt.Sprintf(" in function '%s'", d.FuncName)
		}
		result += "\n"
	}

	// Add source line with pointer
	if d.SourceLine != "" {
		result += fmt.Sprintf("    %s\n", d.SourceLine)
		if d.Column > 0 {
			// Add a pointer to the column
			result += "    "
			for i := 1; i < d.Column; i++ {
				result += " "
			}
			result += "^\n"
		}
	}

	// Add description
	if d.Description != "" {
		result += fmt.Sprintf("  %s\n", d.Description)
	}

	// Add notes
	if d.Notes != "" {
		result += fmt.Sprintf("  Note: %s\n", d.Notes)
	}

	return result
}

// FormatError creates a comprehensive error message using debug info.
func (d *DebugInfo) FormatError(errMsg string) string {
	result := fmt.Sprintf("Runtime error: %s\n", errMsg)
	if d != nil {
		result += d.Format()
	}
	return result
}
