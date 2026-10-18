package main

import (
	"strconv"
	"strings"
	"unicode"

	"github.com/yuin/goldmark/ast"
)

// The ids of headings as GitHub makes them, for the links the
// documentation was written with: lower case, spaces as dashes, and
// nothing else but letters, digits, - and _.
type githubIDs struct{ seen map[string]int }

func newIDs() *githubIDs { return &githubIDs{seen: map[string]int{}} }

func (g *githubIDs) Generate(value []byte, kind ast.NodeKind) []byte {
	var b strings.Builder
	for _, r := range strings.ToLower(string(value)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('-')
		}
	}
	id := b.String()
	if id == "" {
		id = "section"
	}
	n := g.seen[id]
	g.seen[id] = n + 1
	if n > 0 {
		id += "-" + strconv.Itoa(n)
	}
	return []byte(id)
}

func (g *githubIDs) Put(value []byte) { g.seen[string(value)]++ }
