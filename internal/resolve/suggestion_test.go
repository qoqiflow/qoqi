package resolve

import (
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/parser"
	"github.com/qoqiflow/qoqi/internal/token"
)

func TestResolveSuggestion(t *testing.T) {
	// let count = 1;
	// let x = cont; // Typo: cont -> count

	countDecl := &ast.LetStmt{
		BaseNode: ast.BaseNode{Tok: token.Token{Pos: token.Position{Line: 1, Col: 1}, EndPos: token.Position{Line: 1, Col: 14}}},
		Name:     "count",
		Value:    &ast.Literal{Value: "1", Kind: "int"},
	}

	contRef := &ast.Ident{
		BaseNode: ast.BaseNode{Tok: token.Token{Pos: token.Position{Line: 2, Col: 9}, EndPos: token.Position{Line: 2, Col: 13}}},
		Name:     "cont",
	}
	xDecl := &ast.LetStmt{
		BaseNode: ast.BaseNode{Tok: token.Token{Pos: token.Position{Line: 2, Col: 1}, EndPos: token.Position{Line: 2, Col: 14}}},
		Name:     "x",
		Value:    contRef,
	}

	prog := &ast.Program{
		Decls: []ast.Decl{
			&ast.FnDecl{
				Name: "main",
				Body: &ast.BlockStmt{
					Stmts: []ast.Stmt{countDecl, xDecl},
				},
			},
		},
	}

	errs := errors.NewHandler()
	resolver := NewResolver(errs)
	resolver.Resolve(prog)

	if !errs.HasErrors() {
		t.Fatal("Expected resolution errors, but got none")
	}

	foundSuggestion := false
	for _, diag := range errs.Diagnostics {
		if strings.Contains(diag.Message, "undefined identifier 'cont'") {
			if strings.Contains(diag.Suggestion, "Did you mean 'count'?") {
				foundSuggestion = true
			}
		}
	}

	if !foundSuggestion {
		t.Errorf("Expected suggestion 'Did you mean 'count'?', but got errors: %v", errs.Diagnostics)
	}
}

func TestResolveTypeSuggestion(t *testing.T) {
	// let x: Intger = 1; // Typo: Intger -> Int

	xDecl := &ast.LetStmt{
		BaseNode: ast.BaseNode{Tok: token.Token{Pos: token.Position{Line: 1, Col: 1}, EndPos: token.Position{Line: 1, Col: 18}}},
		Name:     "x",
		Type:     &ast.Type{Kind: ast.TypeBasic, Name: "Intger", BaseNode: ast.BaseNode{Tok: token.Token{Pos: token.Position{Line: 1, Col: 8}, EndPos: token.Position{Line: 1, Col: 14}}}},
		Value:    &ast.Literal{Value: "1", Kind: "int"},
	}

	prog := &ast.Program{
		Decls: []ast.Decl{
			&ast.FnDecl{
				Name: "main",
				Body: &ast.BlockStmt{
					Stmts: []ast.Stmt{xDecl},
				},
			},
		},
	}

	errs := errors.NewHandler()
	resolver := NewResolver(errs)
	resolver.Resolve(prog)

	if !errs.HasErrors() {
		t.Fatal("Expected resolution errors, but got none")
	}

	foundSuggestion := false
	for _, diag := range errs.Diagnostics {
		if strings.Contains(diag.Message, "undefined type 'Intger'") {
			if strings.Contains(diag.Suggestion, "Did you mean 'Int'?") {
				foundSuggestion = true
			}
		}
	}

	if !foundSuggestion {
		t.Errorf("Expected suggestion 'Did you mean 'Int'?', but got errors: %v", errs.Diagnostics)
	}
}

func TestPatternMatchingVariableBinding(t *testing.T) {
	// match x { case y: y + 1 }
	// y should be bound as a new variable in the match case scope

	xIdent := &ast.Ident{
		BaseNode: ast.BaseNode{Tok: token.Token{Pos: token.Position{Line: 1, Col: 7}, EndPos: token.Position{Line: 1, Col: 8}}},
		Name:     "x",
	}

	// Pattern: identifier y (should bind y)
	patternY := &ast.Ident{
		BaseNode: ast.BaseNode{Tok: token.Token{Pos: token.Position{Line: 1, Col: 16}, EndPos: token.Position{Line: 1, Col: 17}}},
		Name:     "y",
	}

	// Body: y + 1 (should resolve y to the bound variable)
	bodyYRef := &ast.Ident{
		BaseNode: ast.BaseNode{Tok: token.Token{Pos: token.Position{Line: 1, Col: 19}, EndPos: token.Position{Line: 1, Col: 20}}},
		Name:     "y",
	}

	matchStmt := &ast.MatchStmt{
		Cond: xIdent,
		Cases: []*ast.MatchCase{
			{
				Pattern: patternY,
				Body: &ast.BinaryExpr{
					Left:  bodyYRef,
					Op:    "+",
					Right: &ast.Literal{Value: "1", Kind: "int"},
				},
			},
		},
	}

	// Declare x in outer scope
	xDecl := &ast.LetStmt{
		BaseNode: ast.BaseNode{Tok: token.Token{Pos: token.Position{Line: 1, Col: 1}, EndPos: token.Position{Line: 1, Col: 11}}},
		Name:     "x",
		Value:    &ast.Literal{Value: "5", Kind: "int"},
	}

	prog := &ast.Program{
		Decls: []ast.Decl{
			&ast.FnDecl{
				Name: "main",
				Body: &ast.BlockStmt{
					Stmts: []ast.Stmt{xDecl, matchStmt},
				},
			},
		},
	}

	errs := errors.NewHandler()
	resolver := NewResolver(errs)
	table := resolver.Resolve(prog)

	if errs.HasErrors() {
		t.Errorf("Expected no errors, but got: %v", errs.Diagnostics)
	}

	// Verify that y in the body resolves to the pattern-bound variable
	if sym := table.Defs[bodyYRef]; sym == nil {
		t.Error("Expected y in body to be resolved")
	} else if sym.Decl != patternY {
		t.Error("Expected y in body to resolve to the pattern binding")
	}
}

func TestPatternMatchingWildcard(t *testing.T) {
	// match x { case _: 1 }
	// _ is a wildcard and should not bind a variable

	xIdent := &ast.Ident{
		BaseNode: ast.BaseNode{Tok: token.Token{Pos: token.Position{Line: 1, Col: 7}, EndPos: token.Position{Line: 1, Col: 8}}},
		Name:     "x",
	}

	// Pattern: wildcard _
	wildcardPattern := &ast.Ident{
		BaseNode: ast.BaseNode{Tok: token.Token{Pos: token.Position{Line: 1, Col: 16}, EndPos: token.Position{Line: 1, Col: 17}}},
		Name:     "_",
	}

	matchStmt := &ast.MatchStmt{
		Cond: xIdent,
		Cases: []*ast.MatchCase{
			{
				Pattern: wildcardPattern,
				Body:    &ast.Literal{Value: "1", Kind: "int"},
			},
		},
	}

	xDecl := &ast.LetStmt{
		BaseNode: ast.BaseNode{Tok: token.Token{Pos: token.Position{Line: 1, Col: 1}, EndPos: token.Position{Line: 1, Col: 11}}},
		Name:     "x",
		Value:    &ast.Literal{Value: "5", Kind: "int"},
	}

	prog := &ast.Program{
		Decls: []ast.Decl{
			&ast.FnDecl{
				Name: "main",
				Body: &ast.BlockStmt{
					Stmts: []ast.Stmt{xDecl, matchStmt},
				},
			},
		},
	}

	errs := errors.NewHandler()
	resolver := NewResolver(errs)
	resolver.Resolve(prog)

	// Should have no errors - wildcard doesn't try to redeclare _
	if errs.HasErrors() {
		t.Errorf("Expected no errors for wildcard pattern, but got: %v", errs.Diagnostics)
	}
}

func TestPatternMatchingLiteral(t *testing.T) {
	// match x { case 42: true }
	// Literal patterns don't bind variables

	xIdent := &ast.Ident{
		BaseNode: ast.BaseNode{Tok: token.Token{Pos: token.Position{Line: 1, Col: 7}, EndPos: token.Position{Line: 1, Col: 8}}},
		Name:     "x",
	}

	matchStmt := &ast.MatchStmt{
		Cond: xIdent,
		Cases: []*ast.MatchCase{
			{
				Pattern: &ast.Literal{Value: "42", Kind: "int"},
				Body:    &ast.Literal{Value: "true", Kind: "bool"},
			},
		},
	}

	xDecl := &ast.LetStmt{
		BaseNode: ast.BaseNode{Tok: token.Token{Pos: token.Position{Line: 1, Col: 1}, EndPos: token.Position{Line: 1, Col: 11}}},
		Name:     "x",
		Value:    &ast.Literal{Value: "5", Kind: "int"},
	}

	prog := &ast.Program{
		Decls: []ast.Decl{
			&ast.FnDecl{
				Name: "main",
				Body: &ast.BlockStmt{
					Stmts: []ast.Stmt{xDecl, matchStmt},
				},
			},
		},
	}

	errs := errors.NewHandler()
	resolver := NewResolver(errs)
	resolver.Resolve(prog)

	if errs.HasErrors() {
		t.Errorf("Expected no errors for literal pattern, but got: %v", errs.Diagnostics)
	}
}

// A name alone in the `${}` of a text may have been meant as those
// characters: what is undefined there says how to write them.
func TestResolveSuggestionInsertion(t *testing.T) {
	cases := []struct {
		name       string
		line       string
		suggestion string
		literally  bool
	}{
		{"a name alone", `println("cost: ${price}");`, `write \${price}`, true},
		{"a name alone, with a guess", `let prise = 1; println("${price}");`, `Did you mean 'prise'? To write ${price} itself`, true},
		{"a name in a longer expression", `let total = 1; println("${total + price}");`, "", false},
		{"a name with a call", `println("${price()}");`, "", false},
		{"a name outside a text", `println(price);`, "", false},
		{"two insertions, the second wrong", `let a = 1; println("${a} and ${price}");`, `write \${price}`, true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			src := "fn main() -> Void {\n    " + tt.line + "\n}\n"
			errs := errors.NewHandler()
			prog := parser.Parse(src, "test.qi", errs)
			if errs.HasErrors() {
				t.Fatalf("test source does not parse: %s", errs.Diagnostics[0].Message)
			}
			resolver := NewResolver(errs)
			resolver.SetSourceLines(parser.SourceLines(src))
			resolver.Resolve(prog)

			var found *errors.Diagnostic
			for i, d := range errs.Diagnostics {
				if strings.Contains(d.Message, "'price'") {
					found = &errs.Diagnostics[i]
				}
			}
			if found == nil {
				t.Fatalf("expected an error about 'price', got %v", errs.Diagnostics)
			}
			if got := strings.Contains(found.Suggestion, `\${`); got != tt.literally {
				t.Errorf("suggests writing \\${: %v, want %v (%q)", got, tt.literally, found.Suggestion)
			}
			if tt.suggestion != "" && !strings.Contains(found.Suggestion, tt.suggestion) {
				t.Errorf("suggestion %q does not contain %q", found.Suggestion, tt.suggestion)
			}
		})
	}
}

// A variant written alone and misspelt is guessed like any name.
func TestResolveSuggestionVariant(t *testing.T) {
	src := "enum Shape { Circle(Float), Empty }\nfn main() -> Void {\n    let s = Circel(1.0);\n}\n"
	errs := errors.NewHandler()
	prog := parser.Parse(src, "test.qi", errs)
	NewResolver(errs).Resolve(prog)
	if len(errs.Diagnostics) != 1 || errs.Diagnostics[0].Suggestion != "Did you mean 'Circle'?" {
		t.Errorf("expected a guess at Circle, got %v", errs.Diagnostics)
	}
}
