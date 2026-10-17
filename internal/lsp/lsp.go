// Package lsp holds the language server of Qoqi, which is a Qoqi
// program: server.qi, compiled into the qoqi binary and run by
// `qoqi lsp`.
package lsp

import _ "embed"

// File is the name the server's source goes by in messages.
const File = "<lsp>/server.qi"

// Source is the text of the server.
//
//go:embed server.qi
var Source string
