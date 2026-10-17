package parser

import (
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/errors"
)

// Keywords used as names parse as names wherever a name goes, and stay
// keywords wherever they were keywords.
func TestParseKeywordsAsNames(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string // A statement of the body, as printed
	}{
		{"a loop variable", `for case in cases { log(case); }`, "for case in cases"},
		{"a loop variable with its position", `for i, match in found { }`, "for i, match in found"},
		{"a loop over a variable called in", `for x in in { }`, "for x in in"},
		{"a loop whose variable is in", `for in in ins { }`, "for in in ins"},
		{"a loop over an array literal", `for x in [1, 2] { }`, "for x in [1, 2]"},
		{"a loop over parentheses", `for x in (a) { }`, "for x in a"},
		{"a let", `let match = 1;`, "let match = 1;"},
		{"an assignment", `match = match + 1;`, "(match = (match + 1));"},
		{"a compound assignment", `in += 2;`, "(in += 2);"},
		{"an increment", `case++;`, "(case += 1);"},
		{"a field read", `log(case.name);`, "log(case.name);"},
		{"a field write", `match.count = 0;`, "(match.count = 0);"},
		{"a method", `items.match(x);`, "items.match(x);"},
		{"an index", `let a = in[0];`, "let a = in[0];"},
		{"an index of struct", `let a = struct[0];`, "let a = struct[0];"},
		{"a call of a function called extern", `extern(1);`, "extern(1);"},
		{"an argument", `log(case, as);`, "log(case, as);"},
		{"a comparison", `let b = case == 1 && as != 2;`, "let b = ((case == 1) && (as != 2));"},
		{"a choice", `let b = match ? case : as;`, "let b = (if match case else as);"},
		{"a fallback", `let b = in ?? 0;`, "let b = (in ?? 0);"},
		{"a range", `let r = case..in;`, "let r = (case..in);"},
		{"a lambda", `let f = case -> case + 1;`, "let f = fn(case) { return (case + 1); };"},
		{"a condition", `if match { }`, "if match"},
		{"a while", `while in { }`, "while in"},
		{"a match on a variable called match", `match match { case 1: { } default: { } }`, "match match"},
		{"a match on a tuple", `match (a, b) { default: { } }`, "match"},
		{"a value returned", `return case;`, "return case;"},
		{"a defer of a call", `defer log(1);`, "defer"},
		{"a cast", `let n = x as Int;`, "let n = (x as Int);"},
		{"a cast to a tuple", `let n = x as (Int, Int);`, "let n = (x as __Tuple2<Int, Int>);"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stmts := parseBody(t, tt.src)
			if got := stmts[0].String(); !strings.HasPrefix(got, tt.want) {
				t.Errorf("expected %s, got %s", tt.want, got)
			}
		})
	}
}

// A keyword is a name where a parameter, a field, a function or a method
// is declared.
func TestParseKeywordsAsDeclaredNames(t *testing.T) {
	prog := parseOK(t, `
		struct Case { in: Int; match: Bool; }
		fn struct(case: Case, as: Int) -> Int { return case.in + as; }
		impl Case { fn match() -> Bool { return self.match; } }
		fn main() -> Void { let c = Case { in: 1, match: true }; log(struct(c, 2)); }
	`)
	st := prog.Decls[0].(*ast.StructDecl)
	if st.Fields[0].Name != "in" || st.Fields[1].Name != "match" {
		t.Errorf("fields: %s, %s", st.Fields[0].Name, st.Fields[1].Name)
	}
	fn := prog.Decls[1].(*ast.FnDecl)
	if fn.Name != "struct" || fn.Params[0].Name != "case" || fn.Params[1].Name != "as" {
		t.Errorf("function: %s(%s, %s)", fn.Name, fn.Params[0].Name, fn.Params[1].Name)
	}
	if method := prog.Decls[2].(*ast.ImplDecl).Methods[0]; method.Name != "match" {
		t.Errorf("method: %s", method.Name)
	}
}

// Declarations and statements that start with a keyword still do.
func TestParseKeywordsStayKeywords(t *testing.T) {
	prog := parseOK(t, `
		import std.strings as text;
		pub struct Point { x: Int; }
		enum Shape { Circle(Float), Empty }
		trait Named { fn name() -> String; }
		impl Named for Point { fn name() -> String { return "p"; } }
		impl [Int] { fn second() -> Int { return self[1]; } }
		extern fn clock() -> Int;
		fn main() -> Void {
			match Shape.Empty { case Shape.Circle(r): { } case Shape.Empty: { } }
			defer { log(1); }
			for x in [1, 2] { }
			for (k, v) in pairs { }
		}
	`)
	if len(prog.Imports) != 1 || len(prog.Decls) != 7 {
		t.Errorf("expected an import and 7 declarations, got %d and %d", len(prog.Imports), len(prog.Decls))
	}
}

// A keyword alone where a declaration goes is still the declaration with
// something missing.
func TestParseKeywordAloneIsNoName(t *testing.T) {
	errs := errors.NewHandler()
	Parse("import;", "test.qi", errs)
	if !errs.HasErrors() {
		t.Error("expected an error for `import;`")
	}
}
