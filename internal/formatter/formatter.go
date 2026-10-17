// Package formatter holds `qoqi fmt`, which is a Qoqi program: fmt.qi,
// compiled into the qoqi binary. It finds the files and says what became
// of them; how a program is written is decided by std.formatter, a module
// of the standard library, which the language server runs as well.
package formatter

import _ "embed"

// File is the name the program's source goes by in messages.
const File = "<fmt>/fmt.qi"

// Source is the text of the program.
//
//go:embed fmt.qi
var Source string
