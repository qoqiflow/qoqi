package parser

import (
	"math/big"
	"strings"

	"github.com/qoqiflow/qoqi/internal/token"
)

// InterpolationFunction is the builtin an expression in a string is passed
// to. It does what toString does, under a name a program cannot hide with
// a variable of its own.
const InterpolationFunction = "__text"

// Lexer converts Qoqi source text into tokens.
type Lexer struct {
	src  []rune
	file string
	pos  int
	line int
	col  int

	// Tokens already made and not yet handed out. A string with `${...}`
	// in it becomes several tokens at once (see lexString).
	pending []token.Token
}

// NewLexer creates a lexer for the given source.
func NewLexer(src, file string) *Lexer {
	return &Lexer{src: []rune(src), file: file, line: 1, col: 1}
}

// Tokenize lexes the whole input. Comments are dropped. The result always
// ends with an EOF token. Malformed input yields ILLEGAL tokens whose
// literal describes the problem.
func (l *Lexer) Tokenize() []token.Token {
	var toks []token.Token
	for {
		tok := l.Next()
		if tok.Type == token.COMMENT {
			continue
		}
		toks = append(toks, tok)
		if tok.Type == token.EOF {
			return toks
		}
	}
}

func (l *Lexer) position() token.Position {
	return token.Position{File: l.file, Line: l.line, Col: l.col}
}

func (l *Lexer) peek(offset int) rune {
	if l.pos+offset >= len(l.src) {
		return 0
	}
	return l.src[l.pos+offset]
}

func (l *Lexer) advance() rune {
	ch := l.src[l.pos]
	l.pos++
	if ch == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col++
	}
	return ch
}

func (l *Lexer) skipWhitespace() {
	for l.pos < len(l.src) {
		switch l.src[l.pos] {
		case ' ', '\t', '\r', '\n':
			l.advance()
		default:
			return
		}
	}
}

// Next returns the next token.
func (l *Lexer) Next() token.Token {
	if len(l.pending) > 0 {
		tok := l.pending[0]
		l.pending = l.pending[1:]
		return tok
	}
	return l.scan()
}

// scan reads one token from the source.
func (l *Lexer) scan() token.Token {
	l.skipWhitespace()
	start := l.position()

	if l.pos >= len(l.src) {
		return token.NewToken(token.EOF, "", start, start)
	}

	ch := l.peek(0)

	switch {
	case isLetter(ch):
		begin := l.pos
		for l.pos < len(l.src) && (isLetter(l.peek(0)) || isDigit(l.peek(0))) {
			l.advance()
		}
		lit := string(l.src[begin:l.pos])
		return token.NewToken(token.LookupIdent(lit), lit, start, l.position())
	case isDigit(ch):
		return l.lexNumber(start)
	case ch == '"':
		return l.lexString(start)
	}

	// Comments. A first line that starts with #! names the program that
	// runs the file (#!/usr/bin/env qoqi); it is a comment too
	shebang := l.pos == 0 && ch == '#' && l.peek(1) == '!'
	if (ch == '/' && l.peek(1) == '/') || shebang {
		begin := l.pos
		for l.pos < len(l.src) && l.peek(0) != '\n' {
			l.advance()
		}
		return token.NewToken(token.COMMENT, string(l.src[begin:l.pos]), start, l.position())
	}
	if ch == '/' && l.peek(1) == '*' {
		begin := l.pos
		l.advance()
		l.advance()
		for l.pos < len(l.src) && !(l.peek(0) == '*' && l.peek(1) == '/') {
			l.advance()
		}
		if l.pos >= len(l.src) {
			return token.NewToken(token.ILLEGAL, "unterminated block comment", start, l.position())
		}
		l.advance()
		l.advance()
		return token.NewToken(token.COMMENT, string(l.src[begin:l.pos]), start, l.position())
	}

	// Multi-character operators, longest first
	for _, op := range multiCharOps {
		if l.hasPrefix(op.text) {
			for range op.text {
				l.advance()
			}
			return token.NewToken(op.typ, op.text, start, l.position())
		}
	}

	l.advance()
	if typ, ok := singleCharOps[ch]; ok {
		return token.NewToken(typ, string(ch), start, l.position())
	}
	return token.NewToken(token.ILLEGAL, "unexpected character '"+string(ch)+"'", start, l.position())
}

func (l *Lexer) hasPrefix(s string) bool {
	for i, r := range []rune(s) {
		if l.peek(i) != r {
			return false
		}
	}
	return true
}

func (l *Lexer) lexNumber(start token.Position) token.Token {
	begin := l.pos
	typ := token.INT

	// 0xFF and 0b1010: a whole number by its bits. From here on it is
	// the number, written in tens like any other.
	if l.peek(0) == '0' {
		base, isDigitOf := 0, isDigit
		switch l.peek(1) {
		case 'x', 'X':
			base, isDigitOf = 16, isHexDigit
		case 'b', 'B':
			base, isDigitOf = 2, func(ch rune) bool { return ch == '0' || ch == '1' }
		}
		if base != 0 {
			l.advance()
			l.advance()
			digits := l.pos
			for isDigitOf(l.peek(0)) {
				l.advance()
			}
			written := string(l.src[begin:l.pos])
			value, ok := new(big.Int).SetString(string(l.src[digits:l.pos]), base)
			if !ok || isLetter(l.peek(0)) || isDigit(l.peek(0)) {
				for isLetter(l.peek(0)) || isDigit(l.peek(0)) {
					l.advance()
				}
				return token.NewToken(token.ILLEGAL, "'"+string(l.src[begin:l.pos])+"' is not a number: after "+written[:2]+" come the digits of the number", start, l.position())
			}
			return token.NewToken(token.INT, value.String(), start, l.position())
		}
	}
	for isDigit(l.peek(0)) {
		l.advance()
	}
	if l.peek(0) == '.' && isDigit(l.peek(1)) {
		typ = token.FLOAT
		l.advance()
		for isDigit(l.peek(0)) {
			l.advance()
		}
	}
	if l.peek(0) == 'e' || l.peek(0) == 'E' {
		signed := l.peek(1) == '+' || l.peek(1) == '-'
		if isDigit(l.peek(1)) || (signed && isDigit(l.peek(2))) {
			typ = token.FLOAT
			l.advance()
			if signed {
				l.advance()
			}
			for isDigit(l.peek(0)) {
				l.advance()
			}
		}
	}
	return token.NewToken(typ, string(l.src[begin:l.pos]), start, l.position())
}

// lexString reads a string literal.
//
// A string may hold expressions, written `${expr}`: "total: ${n} items".
// Such a string is not one token but the tokens of the concatenation it
// stands for,
//
//	( "total: " + __text ( n ) + " items" )
//
// so the parser and everything after it see an ordinary expression. The
// first token is returned and the rest are kept for the next calls. The
// tokens of each expression are read by the lexer itself, with their real
// positions; the ones that are made up are positioned at the `${`.
//
// A text over several lines is written between """ and """:
//
//	let help = """
//	    usage: todo <command>
//	      add <title>
//	    """;
//
// The line end after the opening quotes and the one before the closing
// quotes are not part of the text, and what the closing quotes are
// indented by is taken off the front of every line. Inside, a " is an
// ordinary character; escapes and ${...} work as elsewhere.
func (l *Lexer) lexString(start token.Position) token.Token {
	multi := l.hasPrefix(`"""`)
	l.advance() // opening quote
	if multi {
		l.advance()
		l.advance()
	}

	var sb strings.Builder
	var parts []token.Token // Non-nil once a `${` was met
	text := func(at token.Position) token.Token {
		tok := token.NewToken(token.STRING, sb.String(), at, l.position())
		sb.Reset()
		return tok
	}

	for {
		if l.pos >= len(l.src) && multi {
			return token.NewToken(token.ILLEGAL, `unterminated text: one opened with """ ends with """`, start, start)
		}
		if l.pos >= len(l.src) || (l.peek(0) == '\n' && !multi) {
			return token.NewToken(token.ILLEGAL, "unterminated string literal", start, l.position())
		}

		if l.peek(0) == '$' && l.peek(1) == '{' {
			at := l.position()
			made := func(typ token.TokenType, literal string) token.Token {
				end := at
				end.Col += 2
				return token.NewToken(typ, literal, at, end)
			}
			if parts == nil {
				// The text before comes first, even when empty: it is what
				// makes the whole thing a text whatever follows
				parts = []token.Token{token.NewToken(token.LPAREN, "(", start, start), text(start)}
			} else if sb.Len() > 0 {
				parts = append(parts, made(token.PLUS, "+"), text(at))
			}
			l.advance()
			l.advance()

			inner, problem := l.lexInterpolation(at)
			if problem != "" {
				// What is left of the line is part of the broken string:
				// reading it as code would only add errors about nothing
				end := at
				end.Col += 2
				for l.pos < len(l.src) && l.peek(0) != '\n' {
					l.advance()
				}
				l.pending = nil
				return token.NewToken(token.ILLEGAL, problem, at, end)
			}
			parts = append(parts, made(token.PLUS, "+"), made(token.IDENT, InterpolationFunction), made(token.LPAREN, "("))
			parts = append(parts, inner...)
			parts = append(parts, made(token.RPAREN, ")"))
			continue
		}

		if multi && l.hasPrefix(`"""`) {
			l.advance()
			l.advance()
			l.advance()
			break
		}
		ch := l.advance()
		if ch == '"' && !multi {
			break
		}
		if multi && ch == '\r' && l.peek(0) == '\n' {
			continue
		}
		if multi && ch == '\n' {
			// Marked, to tell it from a line end written \n: only these
			// have indentation after them to take off
			sb.WriteRune(lineBreak)
			continue
		}
		if ch != '\\' {
			sb.WriteRune(ch)
			continue
		}
		if l.pos >= len(l.src) {
			return token.NewToken(token.ILLEGAL, "unterminated string literal", start, l.position())
		}
		switch esc := l.advance(); esc {
		case 'n':
			sb.WriteRune('\n')
		case 't':
			sb.WriteRune('\t')
		case 'r':
			sb.WriteRune('\r')
		case '0':
			sb.WriteRune(0)
		case '\\', '"', '$':
			sb.WriteRune(esc)
		case 'u':
			// \u{1F600}: a character by its number, in hexadecimal
			r, problem := l.unicodeEscape()
			if problem != "" {
				return token.NewToken(token.ILLEGAL, problem, start, l.position())
			}
			sb.WriteRune(r)
		default:
			return token.NewToken(token.ILLEGAL, "invalid escape sequence '\\"+string(esc)+"'", start, l.position())
		}
	}

	if parts == nil {
		last := sb.String()
		if multi {
			last = strings.TrimPrefix(unindented(last, closingIndent(&last)), "\n")
		}
		return token.NewToken(token.STRING, last, start, l.position())
	}

	end := l.position()
	if multi {
		last := sb.String()
		indent := closingIndent(&last)
		sb.Reset()
		sb.WriteString(unindented(last, indent))
		for i := range parts {
			if parts[i].Type == token.STRING && strings.ContainsRune(parts[i].Literal, lineBreak) {
				parts[i].Literal = unindented(parts[i].Literal, indent)
			}
		}
		// The line end after the opening quotes; parts[1] is the text
		// before the first ${
		parts[1].Literal = strings.TrimPrefix(parts[1].Literal, "\n")
	}
	if sb.Len() > 0 {
		parts = append(parts, token.NewToken(token.PLUS, "+", end, end), text(end))
	}
	parts = append(parts, token.NewToken(token.RPAREN, ")", end, end))
	l.pending = append(parts[1:], l.pending...)
	return parts[0]
}

// lineBreak stands for a line end of the source inside a text written
// between """, until the text is complete and its indentation known. It
// is not a character: no text holds it.
const lineBreak = '\U0010FFFF'

// closingIndent takes the last line of a text between """ off it, if that
// line is only the indentation of the closing quotes, together with the
// line end before it; it returns that indentation.
func closingIndent(last *string) string {
	at := strings.LastIndex(*last, string(lineBreak))
	if at < 0 {
		return ""
	}
	indent := (*last)[at+len(string(lineBreak)):]
	if strings.Trim(indent, " \t") != "" {
		return ""
	}
	*last = (*last)[:at]
	return indent
}

// unicodeEscape reads what follows \u in a text: {, one to six
// hexadecimal digits, }. It gives the character, or what is wrong.
func (l *Lexer) unicodeEscape() (rune, string) {
	const form = "write a character by its number as \\u{e9} or \\u{1F600}"
	if l.pos >= len(l.src) || l.peek(0) != '{' {
		return 0, "invalid escape sequence '\\u': " + form
	}
	l.advance()
	digits := 0
	var value rune
	for l.pos < len(l.src) && l.peek(0) != '}' {
		c := l.advance()
		var d rune
		switch {
		case c >= '0' && c <= '9':
			d = c - '0'
		case c >= 'a' && c <= 'f':
			d = c - 'a' + 10
		case c >= 'A' && c <= 'F':
			d = c - 'A' + 10
		default:
			return 0, "invalid escape sequence '\\u': " + form
		}
		value = value*16 + d
		digits++
		if digits > 6 {
			return 0, "invalid escape sequence '\\u': at most six digits, " + form
		}
	}
	if l.pos >= len(l.src) || digits == 0 {
		return 0, "invalid escape sequence '\\u': " + form
	}
	l.advance()
	if value > 0x10FFFF || (value >= 0xD800 && value <= 0xDFFF) {
		return 0, "invalid escape sequence '\\u': no character has that number"
	}
	return value, ""
}

// unindented turns the marked line ends of a text into real ones, taking
// the indentation off the line that follows each.
func unindented(text, indent string) string {
	text = strings.ReplaceAll(text, string(lineBreak)+indent, "\n")
	return strings.ReplaceAll(text, string(lineBreak), "\n")
}

// lexInterpolation reads the tokens of the expression of a `${...}`, up to
// the brace that closes it, and returns them without that brace. Braces
// inside the expression (a struct literal, a lambda) are counted, and a
// string inside it is read as any string. On a problem it returns the
// message.
func (l *Lexer) lexInterpolation(at token.Position) ([]token.Token, string) {
	var tokens []token.Token
	depth := 1
	for {
		tok := l.scan()
		switch {
		case tok.Type == token.EOF || tok.Pos.Line != at.Line:
			return nil, "unterminated '${' in a string: expected '}' before the end of the line"
		case tok.Type == token.ILLEGAL:
			if strings.HasPrefix(tok.Literal, "unterminated string") {
				// The quote that was taken for the start of a string is
				// the end of this one: the brace is what is missing
				return nil, "unterminated '${' in a string: expected '}' before the end of the line"
			}
			return nil, tok.Literal
		case tok.Type == token.COMMENT:
			continue
		case tok.Type == token.LBRACE:
			depth++
		case tok.Type == token.RBRACE:
			depth--
			if depth == 0 {
				if len(tokens) == 0 {
					return nil, "nothing between '${' and '}' in a string"
				}
				return tokens, ""
			}
		}
		tokens = append(tokens, tok)

		// A string with `${` of its own left its tokens to be handed out
		tokens = append(tokens, l.pending...)
		l.pending = nil
	}
}

var multiCharOps = []struct {
	text string
	typ  token.TokenType
}{
	{"...", token.ELLIPSIS},
	{"..=", token.DOTDOT_EQ},
	{"..", token.DOTDOT},
	{"->", token.ARROW},
	{"=>", token.FAT_ARROW},
	{"==", token.EQ},
	{"!=", token.NEQ},
	{"<=", token.LTE},
	{">=", token.GTE},
	{"&&", token.AND},
	{"||", token.OR},
	{"|>", token.PIPE_INTO},
	{"??", token.QUESTION_QUESTION},
	{"?.", token.QUESTION_DOT},
	{"+=", token.PLUS_ASSIGN},
	{"-=", token.MINUS_ASSIGN},
	{"*=", token.STAR_ASSIGN},
	{"/=", token.SLASH_ASSIGN},
	{"%=", token.PERCENT_ASSIGN},
	{"&=", token.AMPERSAND_ASSIGN},
	{"|=", token.PIPE_ASSIGN},
	{"^=", token.CARET_ASSIGN},
	{"++", token.INC},
	{"--", token.DEC},
}

var singleCharOps = map[rune]token.TokenType{
	'+': token.PLUS,
	'-': token.MINUS,
	'*': token.STAR,
	'/': token.SLASH,
	'%': token.PERCENT,
	'<': token.LT,
	'>': token.GT,
	'!': token.NOT,
	'?': token.QUESTION,
	'=': token.ASSIGN,
	'&': token.AMPERSAND,
	'|': token.PIPE,
	'^': token.CARET,
	'~': token.TILDE,
	'(': token.LPAREN,
	')': token.RPAREN,
	'{': token.LBRACE,
	'}': token.RBRACE,
	'[': token.LBRACKET,
	']': token.RBRACKET,
	',': token.COMMA,
	';': token.SEMICOLON,
	':': token.COLON,
	'.': token.DOT,
}

func isLetter(ch rune) bool {
	return ch == '_' || (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')
}

func isHexDigit(ch rune) bool {
	return isDigit(ch) || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')
}

func isDigit(ch rune) bool {
	return ch >= '0' && ch <= '9'
}
