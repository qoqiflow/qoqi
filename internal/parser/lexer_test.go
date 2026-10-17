package parser

import (
	"strconv"
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/token"
)

func lexTypes(src string) []token.TokenType {
	var types []token.TokenType
	for _, tok := range NewLexer(src, "test.qi").Tokenize() {
		types = append(types, tok.Type)
	}
	return types
}

func TestLexerTokens(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []token.TokenType
	}{
		{"keywords and idents", "fn main let x_1", []token.TokenType{token.FN, token.IDENT, token.LET, token.IDENT}},
		{"builtin types", "Int String Self", []token.TokenType{token.INT_TYPE, token.STRING_TYPE, token.SELF_TYPE}},
		{"numbers", "42 3.14 10e3 1.5e-2", []token.TokenType{token.INT, token.FLOAT, token.FLOAT, token.FLOAT}},
		{"int followed by member", "1.x", []token.TokenType{token.INT, token.DOT, token.IDENT}},
		{"two char operators", "== != <= >= && || ?? ->", []token.TokenType{
			token.EQ, token.NEQ, token.LTE, token.GTE, token.AND, token.OR, token.QUESTION_QUESTION, token.ARROW,
		}},
		{"one char operators", "= < > ! ? | &", []token.TokenType{
			token.ASSIGN, token.LT, token.GT, token.NOT, token.QUESTION, token.PIPE, token.AMPERSAND,
		}},
		{"ellipsis and dot", "... .", []token.TokenType{token.ELLIPSIS, token.DOT}},
		{"a #! line that starts the file", "#!/usr/bin/env qoqi\nfn", []token.TokenType{token.FN}},
		{"compound assignment", "+= -= *= /= %=", []token.TokenType{
			token.PLUS_ASSIGN, token.MINUS_ASSIGN, token.STAR_ASSIGN, token.SLASH_ASSIGN, token.PERCENT_ASSIGN,
		}},
		{"increment and decrement", "i++ j--", []token.TokenType{token.IDENT, token.INC, token.IDENT, token.DEC}},
		{"spaced signs stay separate", "a - -b + +c", []token.TokenType{
			token.IDENT, token.MINUS, token.MINUS, token.IDENT, token.PLUS, token.PLUS, token.IDENT,
		}},
		{"arrow is not minus and greater", "a->b a - >b", []token.TokenType{
			token.IDENT, token.ARROW, token.IDENT, token.IDENT, token.MINUS, token.GT, token.IDENT,
		}},
		{"comments are skipped", "a // line\n/* block\n */ b", []token.TokenType{token.IDENT, token.IDENT}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := lexTypes(tt.src)
			want := append(tt.want, token.EOF)
			if len(got) != len(want) {
				t.Fatalf("expected %v, got %v", want, got)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Errorf("token %d: expected %s, got %s", i, want[i], got[i])
				}
			}
		})
	}
}

func TestLexerStringEscapes(t *testing.T) {
	toks := NewLexer(`"a\n\t\"b\\"`, "test.qi").Tokenize()
	if toks[0].Type != token.STRING {
		t.Fatalf("expected STRING, got %s", toks[0].Type)
	}
	if want := "a\n\t\"b\\"; toks[0].Literal != want {
		t.Errorf("expected %q, got %q", want, toks[0].Literal)
	}
}

func TestLexerPositions(t *testing.T) {
	toks := NewLexer("let x\n  = 10;", "test.qi").Tokenize()

	want := []struct{ line, col, endCol int }{
		{1, 1, 4}, // let
		{1, 5, 6}, // x
		{2, 3, 4}, // =
		{2, 5, 7}, // 10
		{2, 7, 8}, // ;
	}
	for i, w := range want {
		tok := toks[i]
		if tok.Pos.Line != w.line || tok.Pos.Col != w.col || tok.EndPos.Col != w.endCol {
			t.Errorf("token %d (%s): expected %d:%d-%d, got %d:%d-%d",
				i, tok.Literal, w.line, w.col, w.endCol, tok.Pos.Line, tok.Pos.Col, tok.EndPos.Col)
		}
		if tok.Pos.File != "test.qi" {
			t.Errorf("token %d: expected file test.qi, got %q", i, tok.Pos.File)
		}
	}
}

func TestLexerErrors(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"unterminated string", `"abc`, "unterminated string literal"},
		{"string across lines", "\"abc\ndef\"", "unterminated string literal"},
		{"bad escape", `"a\qb"`, `invalid escape sequence '\q'`},
		{"unterminated comment", "/* never closed", "unterminated block comment"},
		{"unknown character", "#", "unexpected character '#'"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tok := NewLexer(tt.src, "test.qi").Tokenize()[0]
			if tok.Type != token.ILLEGAL {
				t.Fatalf("expected ILLEGAL, got %s", tok.Type)
			}
			if tok.Literal != tt.want {
				t.Errorf("expected %q, got %q", tt.want, tok.Literal)
			}
		})
	}
}

// spell writes tokens back as text, to compare a string with `${...}` in it
// with the expression it stands for.
func spell(src string) string {
	var parts []string
	for _, tok := range NewLexer(src, "test.qi").Tokenize() {
		switch tok.Type {
		case token.EOF:
		case token.STRING:
			parts = append(parts, strconv.Quote(tok.Literal))
		case token.ILLEGAL:
			parts = append(parts, "<"+tok.Literal+">")
		default:
			parts = append(parts, tok.Literal)
		}
	}
	return strings.Join(parts, " ")
}

func TestLexerStringInterpolation(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		// A string without ${ is one token, as before
		{"plain", `"total"`, `"total"`},
		{"dollar alone", `"cost: $5"`, `"cost: $5"`},
		{"dollar at the end", `"cost $"`, `"cost $"`},
		{"braces alone", `"{a} {}"`, `"{a} {}"`},
		{"escaped", `"\${n}"`, `"${n}"`},
		{"escaped dollar alone", `"\$5"`, `"$5"`},

		// With one, it is the tokens of a concatenation
		{"in the middle", `"total: ${n} items"`, `( "total: " + __text ( n ) + " items" )`},
		{"at the start", `"${n} items"`, `( "" + __text ( n ) + " items" )`},
		{"at the end", `"total: ${n}"`, `( "total: " + __text ( n ) )`},
		{"alone", `"${n}"`, `( "" + __text ( n ) )`},
		{"two in a row", `"${a}${b}"`, `( "" + __text ( a ) + __text ( b ) )`},
		{"several", `"${a} and ${b}!"`, `( "" + __text ( a ) + " and " + __text ( b ) + "!" )`},
		{"an expression", `"${a + b * 2}"`, `( "" + __text ( a + b * 2 ) )`},
		{"a call", `"${f(x, y)}"`, `( "" + __text ( f ( x , y ) ) )`},
		{"a member and an index", `"${p.items[0]}"`, `( "" + __text ( p . items [ 0 ] ) )`},
		{"spaces inside", `"${  n  }"`, `( "" + __text ( n ) )`},
		{"escapes around it", `"a\t${n}\n"`, `( "a\t" + __text ( n ) + "\n" )`},
		{"an escaped one next to a real one", `"\${a} ${b}"`, `( "${a} " + __text ( b ) )`},

		// Braces and strings inside the expression
		{"a struct literal", `"${P { x: 1 }.x}"`, `( "" + __text ( P { x : 1 } . x ) )`},
		{"a lambda", `"${fn() -> Int { return 1; }()}"`, `( "" + __text ( fn ( ) -> Int { return 1 ; } ( ) ) )`},
		{"a string", `"${"in"}"`, `( "" + __text ( "in" ) )`},
		{"a string with a quote", `"${"a\"b"}"`, `( "" + __text ( "a\"b" ) )`},
		{"a string with one of its own", `"${"x ${n} y"}"`, `( "" + __text ( ( "x " + __text ( n ) + " y" ) ) )`},
		{"three deep", `"${"${"${n}"}"}"`, `( "" + __text ( ( "" + __text ( ( "" + __text ( n ) ) ) ) ) )`},
		{"a comment", `"${n /* which */}"`, `( "" + __text ( n ) )`},

		// What follows the string is untouched
		{"followed by code", `"${a}" + b;`, `( "" + __text ( a ) ) + b ;`},
		{"two strings", `"${a}" "b"`, `( "" + __text ( a ) ) "b"`},

		// Mistakes
		{"nothing inside", `"a ${} b"`, `<nothing between '${' and '}' in a string>`},
		{"only spaces inside", `"a ${  } b"`, `<nothing between '${' and '}' in a string>`},
		{"never closed", `"a ${n b"`, `<unterminated '${' in a string: expected '}' before the end of the line>`},
		{"closed on another line", "\"a ${n\n} b\"", `<unterminated '${' in a string: expected '}' before the end of the line>`},
		{"a bad character inside", `"a ${n # 1} b"`, `<unexpected character '#'>`},
		{"unclosed string after one", `"a ${n} b`, `<unterminated string literal>`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := spell(tt.src); got != tt.want {
				t.Errorf("%s\n  want %s\n   got %s", tt.src, tt.want, got)
			}
		})
	}
}

func TestLexerInterpolationPositions(t *testing.T) {
	//          1         2
	// 1234567890123456789012345
	src := `x = "ab ${count + 1} cd";`
	toks := NewLexer(src, "test.qi").Tokenize()

	// The tokens of the expression are where they are written, so an
	// error in it is shown under it
	for _, tok := range toks {
		switch tok.Literal {
		case "count":
			if tok.Pos.Col != 11 || tok.EndPos.Col != 16 {
				t.Errorf("expected count at columns 11-16, got %d-%d", tok.Pos.Col, tok.EndPos.Col)
			}
		case "1":
			if tok.Pos.Col != 19 {
				t.Errorf("expected 1 at column 19, got %d", tok.Pos.Col)
			}
		case InterpolationFunction:
			// What is made up is placed at the ${
			if tok.Pos.Col != 9 || tok.EndPos.Col != 11 {
				t.Errorf("expected the call at the ${, columns 9-11, got %d-%d", tok.Pos.Col, tok.EndPos.Col)
			}
		}
	}
	// The whole thing starts at the opening quote
	if open := toks[2]; open.Type != token.LPAREN || open.Pos.Col != 5 {
		t.Errorf("expected the expression to start at the quote, got %v at column %d", open.Type, open.Pos.Col)
	}
	// ...and what comes after the string is read from the right place
	if last := toks[len(toks)-2]; last.Type != token.SEMICOLON || last.Pos.Col != 25 {
		t.Errorf("expected the semicolon at column 25, got %v at %d", last.Type, last.Pos.Col)
	}
	for _, tok := range toks {
		if tok.Pos.Line != 1 || tok.Pos.File != "test.qi" {
			t.Errorf("token %q has position %v", tok.Literal, tok.Pos)
		}
	}
}

// \u{...} writes a character by its number, in hexadecimal; anything else
// after \u says how it is written.
func TestLexUnicodeEscapes(t *testing.T) {
	good := map[string]string{
		`"caf\u{e9}"`:      "café",
		`"\u{1F600}!"`:     "😀!",
		`"\u{41}\u{042}"`:  "AB",
		`"""a\u{20AC}b"""`: "a€b",
	}
	for src, want := range good {
		toks := NewLexer(src, "t.qi").Tokenize()
		if toks[0].Type != token.STRING || toks[0].Literal != want {
			t.Errorf("%s: got %v %q, want %q", src, toks[0].Type, toks[0].Literal, want)
		}
	}
	for _, src := range []string{`"\u00e9"`, `"\u{}"`, `"\u{1234567}"`, `"\u{D800}"`, `"\u{110000}"`, `"\u{zz}"`} {
		toks := NewLexer(src, "t.qi").Tokenize()
		if toks[0].Type != token.ILLEGAL || !strings.Contains(toks[0].Literal, `\u`) {
			t.Errorf("%s: expected an error about \\u, got %v %q", src, toks[0].Type, toks[0].Literal)
		}
	}
}
