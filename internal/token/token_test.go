package token

import "testing"

func TestTokenTypeString(t *testing.T) {
	tests := []struct {
		typ      TokenType
		expected string
	}{
		{ILLEGAL, "ILLEGAL"},
		{EOF, "EOF"},
		{IDENT, "IDENT"},
		{INT, "INT"},
		{FLOAT, "FLOAT"},
		{STRING, "STRING"},
		{IF, "IF"},
		{ELSE, "ELSE"},
		{FN, "FN"},
		{LET, "LET"},
		{CONST, "CONST"},
		{STRUCT, "STRUCT"},
		{ENUM, "ENUM"},
		{TRAIT, "TRAIT"},
		{IMPL, "IMPL"},
		{TRUE, "TRUE"},
		{FALSE, "FALSE"},
		{NULL, "NULL"},
		{PLUS, "PLUS"},
		{MINUS, "MINUS"},
		{STAR, "STAR"},
		{SLASH, "SLASH"},
		{EQ, "EQ"},
		{NEQ, "NEQ"},
		{LT, "LT"},
		{GTE, "GTE"},
		{AND, "AND"},
		{OR, "OR"},
		{NOT, "NOT"},
		{LPAREN, "LPAREN"},
		{RPAREN, "RPAREN"},
		{LBRACE, "LBRACE"},
		{RBRACE, "RBRACE"},
		{SEMICOLON, "SEMICOLON"},
		{ARROW, "ARROW"},
		{ELLIPSIS, "ELLIPSIS"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.typ.String(); got != tt.expected {
				t.Errorf("TokenType.String() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestLookupIdent(t *testing.T) {
	tests := []struct {
		ident    string
		expected TokenType
	}{
		{"if", IF},
		{"else", ELSE},
		{"fn", FN},
		{"let", LET},
		{"const", CONST},
		{"struct", STRUCT},
		{"enum", ENUM},
		{"trait", TRAIT},
		{"impl", IMPL},
		{"for", FOR},
		{"while", WHILE},
		{"in", IN},
		{"break", BREAK},
		{"continue", CONTINUE},
		{"return", RETURN},
		{"match", MATCH},
		{"case", CASE},
		{"import", IMPORT},
		{"as", AS},
		{"true", TRUE},
		{"false", FALSE},
		{"null", NULL},
		{"Int", INT_TYPE},
		{"Float", FLOAT_TYPE},
		{"Bool", BOOL_TYPE},
		{"String", STRING_TYPE},
		{"Any", ANY_TYPE},
		{"Void", VOID_TYPE},
		{"Self", SELF_TYPE},
		{"myVar", IDENT},
		{"foo", IDENT},
		{"_private", IDENT},
		{"x123", IDENT},
	}

	for _, tt := range tests {
		t.Run(tt.ident, func(t *testing.T) {
			if got := LookupIdent(tt.ident); got != tt.expected {
				t.Errorf("LookupIdent(%q) = %v, want %v", tt.ident, got, tt.expected)
			}
		})
	}
}

func TestTokenIsLiteral(t *testing.T) {
	tests := []struct {
		name     string
		tok      Token
		expected bool
	}{
		{"IDENT is literal", Token{Type: IDENT}, true},
		{"INT is literal", Token{Type: INT}, true},
		{"FLOAT is literal", Token{Type: FLOAT}, true},
		{"STRING is literal", Token{Type: STRING}, true},
		{"IF is not literal", Token{Type: IF}, false},
		{"PLUS is not literal", Token{Type: PLUS}, false},
		{"LPAREN is not literal", Token{Type: LPAREN}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.tok.IsLiteral(); got != tt.expected {
				t.Errorf("Token.IsLiteral() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestTokenIsKeyword(t *testing.T) {
	tests := []struct {
		name     string
		tok      Token
		expected bool
	}{
		{"IF is keyword", Token{Type: IF}, true},
		{"FN is keyword", Token{Type: FN}, true},
		{"LET is keyword", Token{Type: LET}, true},
		{"TRUE is keyword", Token{Type: TRUE}, true},
		{"INT_TYPE is keyword", Token{Type: INT_TYPE}, true},
		{"IDENT is not keyword", Token{Type: IDENT}, false},
		{"PLUS is not keyword", Token{Type: PLUS}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.tok.IsKeyword(); got != tt.expected {
				t.Errorf("Token.IsKeyword() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestTokenIsOperator(t *testing.T) {
	tests := []struct {
		name     string
		tok      Token
		expected bool
	}{
		{"PLUS is operator", Token{Type: PLUS}, true},
		{"MINUS is operator", Token{Type: MINUS}, true},
		{"EQ is operator", Token{Type: EQ}, true},
		{"AND is operator", Token{Type: AND}, true},
		{"QUESTION_QUESTION is operator", Token{Type: QUESTION_QUESTION}, true},
		{"IF is not operator", Token{Type: IF}, false},
		{"LPAREN is not operator", Token{Type: LPAREN}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.tok.IsOperator(); got != tt.expected {
				t.Errorf("Token.IsOperator() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestTokenIsDelimiter(t *testing.T) {
	tests := []struct {
		name     string
		tok      Token
		expected bool
	}{
		{"LPAREN is delimiter", Token{Type: LPAREN}, true},
		{"RBRACE is delimiter", Token{Type: RBRACE}, true},
		{"SEMICOLON is delimiter", Token{Type: SEMICOLON}, true},
		{"ARROW is delimiter", Token{Type: ARROW}, true},
		{"ELLIPSIS is delimiter", Token{Type: ELLIPSIS}, true},
		{"PLUS is not delimiter", Token{Type: PLUS}, false},
		{"IF is not delimiter", Token{Type: IF}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.tok.IsDelimiter(); got != tt.expected {
				t.Errorf("Token.IsDelimiter() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestTokenString(t *testing.T) {
	tests := []struct {
		name     string
		tok      Token
		expected string
	}{
		{
			"Token with literal",
			Token{Type: IDENT, Literal: "myVar"},
			"IDENT(myVar)",
		},
		{
			"Token without literal",
			Token{Type: PLUS},
			"PLUS",
		},
		{
			"INT token with value",
			Token{Type: INT, Literal: "123"},
			"INT(123)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.tok.String(); got != tt.expected {
				t.Errorf("Token.String() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestNewToken(t *testing.T) {
	pos := Position{File: "test.qi", Line: 1, Col: 5}
	endPos := Position{File: "test.qi", Line: 1, Col: 10}
	tok := NewToken(IDENT, "myVar", pos, endPos)

	if tok.Type != IDENT {
		t.Errorf("Token.Type = %v, want %v", tok.Type, IDENT)
	}
	if tok.Literal != "myVar" {
		t.Errorf("Token.Literal = %v, want %v", tok.Literal, "myVar")
	}
	if tok.Pos != pos {
		t.Errorf("Token.Pos = %v, want %v", tok.Pos, pos)
	}
	if tok.EndPos != endPos {
		t.Errorf("Token.EndPos = %v, want %v", tok.EndPos, endPos)
	}
}

func TestPrecedence(t *testing.T) {
	tests := []struct {
		typ      TokenType
		expected int
	}{
		{OR, 1},
		{AND, 2},
		{EQ, 3},
		{NEQ, 3},
		{LT, 4},
		{GTE, 4},
		{PIPE_INTO, 5},
		{QUESTION_QUESTION, 6},
		{PIPE, 7},
		{CARET, 8},
		{AMPERSAND, 9},
		{PLUS, 11},
		{MINUS, 11},
		{STAR, 12},
		{SLASH, 12},
		{PERCENT, 12},
		{IF, 0},     // Non-operator
		{LPAREN, 0}, // Non-operator
	}

	for _, tt := range tests {
		t.Run(tt.typ.String(), func(t *testing.T) {
			if got := tt.typ.Precedence(); got != tt.expected {
				t.Errorf("TokenType.Precedence() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestPrecedenceOrdering(t *testing.T) {
	// Verify precedence ordering
	if !(OR.Precedence() < AND.Precedence()) {
		t.Error("OR should have lower precedence than AND")
	}
	if !(AND.Precedence() < EQ.Precedence()) {
		t.Error("AND should have lower precedence than EQ")
	}
	if !(EQ.Precedence() < LT.Precedence()) {
		t.Error("EQ should have lower precedence than LT")
	}
	if !(LT.Precedence() < PLUS.Precedence()) {
		t.Error("LT should have lower precedence than PLUS")
	}
	if !(PLUS.Precedence() < STAR.Precedence()) {
		t.Error("PLUS should have lower precedence than STAR")
	}
}
