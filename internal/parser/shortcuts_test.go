package parser

import (
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/token"
)

// literalOf lexes a source that is one text and returns what it holds.
func literalOf(t *testing.T, src string) string {
	t.Helper()
	toks := NewLexer(src, "test.qi").Tokenize()
	if toks[0].Type != token.STRING {
		t.Fatalf("expected a STRING, got %s %q", toks[0].Type, toks[0].Literal)
	}
	if toks[1].Type != token.EOF {
		t.Fatalf("expected one token, then the end; got %s %q", toks[1].Type, toks[1].Literal)
	}
	return toks[0].Literal
}

func TestLexerTextOverSeveralLines(t *testing.T) {
	q := `"""`
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"on one line", q + `one line` + q, "one line"},
		{"empty", q + q, ""},
		{"quotes inside", q + `say "hi" and "" twice` + q, `say "hi" and "" twice`},
		{"the line ends at both ends are not part of it", q + "\nabc\n" + q, "abc"},
		{"the closing quotes say what to take off", q + "\n    usage\n      add\n    " + q, "usage\n  add"},
		{"a line indented less keeps what it has", q + "\n    a\n  b\n    " + q, "a\n  b"},
		{"an empty line stays empty", q + "\n    a\n\n    b\n    " + q, "a\n\nb"},
		{"tabs count as written", q + "\n\t\ta\n\t\t\tb\n\t\t" + q, "a\n\tb"},
		{"closing quotes after text take nothing off", q + "\n    a\n    b" + q, "    a\n    b"},
		{"a line end written out is not indentation", q + "\n    a\\n    b\n    " + q, "a\n    b"},
		{"escapes work", q + `a\tb\\c\"d\$e` + q, "a\tb\\c\"d$e"},
		{"line ends of Windows", q + "\r\n    a\r\n    b\r\n    " + q, "a\nb"},
		{"only a line end", q + "\n" + q, ""},
		{"an empty line and nothing else", q + "\n\n    " + q, ""},
		{"text on the line of the opening quotes", q + "a\n    b\n    " + q, "a\nb"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := literalOf(t, tt.src); got != tt.want {
				t.Errorf("expected %q, got %q", tt.want, got)
			}
		})
	}

	// With values in it, it is the tokens of a concatenation, the
	// indentation taken off every piece
	src := q + "\n    total: ${n}\n      of ${ m + 1 } items\n    " + q
	var parts []string
	for _, tok := range NewLexer(src, "test.qi").Tokenize() {
		if tok.Type == token.EOF {
			break
		}
		if tok.Type == token.STRING {
			parts = append(parts, strings.ReplaceAll(`"`+tok.Literal+`"`, "\n", `\n`))
		} else {
			parts = append(parts, tok.Literal)
		}
	}
	want := `( "total: " + __text ( n ) + "\n  of " + __text ( m + 1 ) + " items" )`
	if got := strings.Join(parts, " "); got != want {
		t.Errorf("expected the tokens\n  %s\ngot\n  %s", want, got)
	}

	// What follows is on the right line
	toks := NewLexer(q+"\n a\n b\n "+q+" x", "test.qi").Tokenize()
	if toks[1].Literal != "x" || toks[1].Pos.Line != 4 {
		t.Errorf("expected x on line 4, got %q on line %d", toks[1].Literal, toks[1].Pos.Line)
	}

	// One that is never closed is reported where it starts
	for _, src := range []string{q + "abc", q + "\nabc\n\"\"", `x = ` + q + `a"`} {
		toks := NewLexer(src, "test.qi").Tokenize()
		last := toks[len(toks)-2]
		if last.Type != token.ILLEGAL || !strings.Contains(last.Literal, `unterminated text: one opened with """ ends with """`) {
			t.Errorf("%q: expected an unterminated text, got %s %q", src, last.Type, last.Literal)
		}
	}
	// A text in ordinary quotes still ends with its line
	if tok := NewLexer("\"abc\ndef\"", "test.qi").Tokenize()[0]; tok.Type != token.ILLEGAL {
		t.Errorf("expected a text in one pair of quotes not to cross lines, got %s", tok.Type)
	}
}

// writtenBody parses the statements of a function body and writes them out.
func writtenBody(t *testing.T, body string) string {
	t.Helper()
	errs := errors.NewHandler()
	prog := Parse("fn f() {\n"+body+"\n}", "test.qi", errs)
	for _, d := range errs.Diagnostics {
		t.Fatalf("unexpected diagnostic: %s", d.Message)
	}
	var lines []string
	for _, stmt := range prog.Decls[0].(*ast.FnDecl).Body.Stmts {
		lines = append(lines, stmt.String())
	}
	return strings.Join(lines, " | ")
}

func TestParseTryAndSafeAccess(t *testing.T) {
	tests := []struct {
		src  string
		want string
	}{
		{`x?;`, `(x?);`},
		{`f(a)?;`, `(f(a)?);`},
		{`a.b?;`, `(a.b?);`},
		{`-x?;`, `(-(x?));`},
		{`x? + y?;`, `((x?) + (y?));`},
		{`(x?)?;`, `((x?)?);`},
		{`x ?? y;`, `(x ?? y);`},
		{`f(x?)?;`, `(f((x?))?);`},
		// The access is made on a name that stands for the value
		{`a?.b;`, `(a?.__safe1.b);`},
		{`a?.m(1, c);`, `(a?.__safe1.m(1, c));`},
		{`a?.b?.c;`, `((a?.__safe1.b)?.__safe2.c);`},
		{`a?.b.c;`, `(a?.__safe1.b.c);`},
		{`a?.b.c(1)[2].d;`, `(a?.__safe1.b.c(1)[2].d);`},
		{`a?.b.c?.d.e;`, `((a?.__safe1.b.c)?.__safe2.d.e);`},
		{`a?.b + c.d;`, `((a?.__safe1.b) + c.d);`},
		{`(a?.b).c;`, `(a?.__safe1.b).c;`},
		{`a?.b ?? c;`, `((a?.__safe1.b) ?? c);`},
		{`a.b?.c(d)?;`, `((a.b?.__safe1.c(d))?);`},
		{`a?.b?.c;`, `((a?.__safe1.b)?.__safe2.c);`},
		{`a?.0.1;`, `(a?.__safe1.0.1);`},
		{`a?.0;`, `(a?.__safe1.0);`},
		{`f()?.b;`, `(f()?.__safe1.b);`},
		{`(a?).b;`, `(a?).b;`},
		{`a?.m<Int>(1);`, `(a?.__safe1.m<Int>(1));`},
		// A type still ends with its own question mark
		{`let n = x as Int?;`, `let n = (x as Int?);`},
		{`let n = (x as Int?)?;`, `let n = ((x as Int?)?);`},
	}
	for _, tt := range tests {
		if got := writtenBody(t, tt.src); got != tt.want {
			t.Errorf("%s\n  want %s\n   got %s", tt.src, tt.want, got)
		}
	}

	// The holder is what SafeHolder finds, for a member and for a call
	errs := errors.NewHandler()
	prog := Parse("fn f() { a?.b; a?.m().c[0].d(); a + b; }", "test.qi", errs)
	stmts := prog.Decls[0].(*ast.FnDecl).Body.Stmts
	for i, want := range []string{"__safe1", "__safe2", ""} {
		holder := ast.SafeHolder(stmts[i].(*ast.ExprStmt).Expr.(*ast.BinaryExpr))
		if (holder == nil) != (want == "") || (holder != nil && holder.Name != want) {
			t.Errorf("statement %d: expected the holder %q, got %v", i, want, holder)
		}
	}
}

func TestParseTuples(t *testing.T) {
	tests := []struct {
		src  string
		want string
	}{
		{`let p = (1, "a");`, `let p = __Tuple2.Of(1, "a");`},
		{`let p = (1, 2, 3,);`, `let p = __Tuple3.Of(1, 2, 3);`},
		{`let p = ((1, 2), (3));`, `let p = __Tuple2.Of(__Tuple2.Of(1, 2), 3);`},
		{`let p = (1);`, `let p = 1;`},
		{`let p: (Int, String) = q;`, `let p: __Tuple2<Int, String> = q;`},
		{`let p: [(Int, (Bool, Float))]? = q;`, `let p: [__Tuple2<Int, __Tuple2<Bool, Float>>]? = q;`},
		{`let p: (Int) = q;`, `let p: Int = q;`},
		{`let f: fn((Int, Int)) -> (Int, Int) = g;`, `let f: fn(__Tuple2<Int, Int>) -> __Tuple2<Int, Int> = g;`},
		{`p.0;`, `p.0;`},
		{`p.0.1;`, `p.0.1;`},
		{`p.1.0.2;`, `p.1.0.2;`},
		{`p.0.name;`, `p.0.name;`},
		{`f().1 + 2.5;`, `(f().1 + 2.5);`},
		// Taking one apart is short for a variable per value
		{`let (a, b) = p;`, `let __tuple1 = p; | let a = __tuple1.0; | let b = __tuple1.1;`},
		{`let (a, _, c) = f(x);`, `let __tuple1 = f(x); | let a = __tuple1.0; | let c = __tuple1.2;`},
		{`const (a, b) = p;`, `let __tuple1 = p; | const a = __tuple1.0; | const b = __tuple1.1;`},
		{`for (k, v) in pairs { use(k); }`, `for __tuple1 in pairs { let k = __tuple1.0; let v = __tuple1.1; use(k); }`},
		{`for i, (k, _) in pairs { }`, `for i, __tuple1 in pairs { let k = __tuple1.0; }`},
	}
	for _, tt := range tests {
		got := strings.Join(strings.Fields(writtenBody(t, tt.src)), " ")
		if got != tt.want {
			t.Errorf("%s\n  want %s\n   got %s", tt.src, tt.want, got)
		}
	}

	// A pattern on a tuple is a pattern on the variant behind it
	errs := errors.NewHandler()
	prog := Parse("fn f() { match p { case (0, x): { } case (_, (a, true)): { } } }", "test.qi", errs)
	match := prog.Decls[0].(*ast.FnDecl).Body.Stmts[0].(*ast.MatchStmt)
	for i, want := range []string{"__Tuple2.Of(0, x)", "__Tuple2.Of(_, __Tuple2.Of(a, true))"} {
		pattern, ok := match.Cases[i].Pattern.(*ast.VariantPattern)
		if !ok || pattern.String() != want {
			t.Errorf("case %d: expected the pattern %s, got %v", i, want, match.Cases[i].Pattern)
		}
	}

	failures := []struct{ src, want string }{
		{`let p = (1, 2, 3, 4, 5, 6, 7);`, "a tuple holds 6 values at most, and this one has 7"},
		{`let p: (Int, Int, Int, Int, Int, Int, Int) = q;`, "a tuple holds 6 values at most"},
		{`let (a) = p;`, "a tuple has at least two values, and 1 is named here"},
		{`let () = p;`, "a tuple has at least two values, and 0 are named here"},
		{`let (a, 1) = p;`, "expected a name for a value of the tuple"},
		{`for (a, b), c in p { }`, "the position comes first"},
		{`p.x1.5e3;`, "expected"},
	}
	for _, tt := range failures {
		errs := errors.NewHandler()
		Parse("fn f() {\n"+tt.src+"\n}", "test.qi", errs)
		if !errs.HasErrors() || !strings.Contains(errs.Diagnostics[0].Message, tt.want) {
			t.Errorf("%s: expected an error containing %q, got %v", tt.src, tt.want, errs.Diagnostics)
		}
	}

	// The names behind tuples
	if ast.TupleName(3) != "__Tuple3" || !ast.IsTupleName("__Tuple2") || ast.IsTupleName("Tuple2") {
		t.Error("unexpected names for the enums behind tuples")
	}
	for name, want := range map[string]int{"0": 0, "12": 12, "x": -1, "": -1, "1a": -1, "-1": -1} {
		if index, ok := ast.TupleIndex(name); (ok && index != want) || (!ok && want != -1) {
			t.Errorf("TupleIndex(%q): got %d, %v", name, index, ok)
		}
	}
	errs = errors.NewHandler()
	declared := Parse(ast.TupleSource(), "<tuples>", errs)
	if errs.HasErrors() || len(declared.Decls) != ast.MaxTupleSize-ast.MinTupleSize+1 {
		t.Errorf("expected the enums behind tuples to parse, got %d declarations and %v", len(declared.Decls), errs.Diagnostics)
	}
}
