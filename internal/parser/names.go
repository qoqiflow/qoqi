package parser

import "github.com/qoqiflow/qoqi/internal/token"

// Most keywords may also be names: of a variable, a parameter, a field, a
// function or a method.
//
//	for case in cases { println(case.name); }
//	let match = pattern.find(text);
//	fn total(in: [Int]) -> Int { ... }
//
// Whether one is a name is told from the tokens around it, before the
// program is parsed: one after `let`, `fn`, `for` or a dot is the name
// being declared or the member being read, and one followed by what only
// ever follows a value (`.`, `=`, `,`, `)`, an operator) is a name being
// used. Everywhere else it is the keyword it always was, so no program
// changes meaning.
//
// The keywords a statement or an expression can only start with (`if`,
// `while`, `return`, `fn`, `let`, `true`...) stay keywords everywhere.

// softKeywords are the keywords that may be names.
var softKeywords = map[token.TokenType]bool{
	token.CASE:   true,
	token.MATCH:  true,
	token.IN:     true,
	token.AS:     true,
	token.DEFER:  true,
	token.IMPORT: true,
	token.PUB:    true,
	token.EXTERN: true,
	token.STRUCT: true,
	token.ENUM:   true,
	token.TRAIT:  true,
	token.IMPL:   true,
}

// declaresName are the tokens after which comes a name being declared or
// a member: let case, fn match(), for in in..., item.case
var declaresName = map[token.TokenType]bool{
	token.LET:          true,
	token.CONST:        true,
	token.FN:           true,
	token.FOR:          true,
	token.DOT:          true,
	token.QUESTION_DOT: true,
}

// followsValue are the tokens that only ever follow a value: what comes
// before one of them is a name, never the keyword.
var followsValue = map[token.TokenType]bool{
	token.DOT: true, token.QUESTION_DOT: true,
	token.ASSIGN: true, token.PLUS_ASSIGN: true, token.MINUS_ASSIGN: true, token.STAR_ASSIGN: true,
	token.SLASH_ASSIGN: true, token.PERCENT_ASSIGN: true, token.AMPERSAND_ASSIGN: true,
	token.PIPE_ASSIGN: true, token.CARET_ASSIGN: true, token.INC: true, token.DEC: true,
	token.SEMICOLON: true, token.COMMA: true, token.COLON: true,
	token.RPAREN: true, token.RBRACKET: true, token.RBRACE: true,
	token.ARROW: true, token.EQ: true, token.NEQ: true, token.LTE: true, token.GTE: true,
	token.PLUS: true, token.STAR: true, token.SLASH: true, token.PERCENT: true,
	token.AND: true, token.OR: true, token.QUESTION: true, token.QUESTION_QUESTION: true,
	token.PIPE_INTO: true, token.DOTDOT: true, token.DOTDOT_EQ: true,
	token.AMPERSAND: true, token.CARET: true,
}

// startsCondition are the keywords whose condition or value is followed by
// a block: `if case {`, `match match {`, `for x in in {`.
var startsCondition = map[token.TokenType]bool{
	token.IF:    true,
	token.WHILE: true,
	token.MATCH: true,
	token.IN:    true,
	token.ELSE:  true,
}

// softKeywordsAsNames makes the keywords used as names in a list of tokens
// names, in place.
func softKeywordsAsNames(toks []token.Token) {
	for i := range toks {
		if !softKeywords[toks[i].Type] {
			continue
		}
		var prev, next token.TokenType = token.EOF, token.EOF
		if i > 0 {
			prev = toks[i-1].Type
		}
		if i+1 < len(toks) {
			next = toks[i+1].Type
		}
		if softKeywordIsName(toks[i].Type, prev, next) {
			toks[i].Type = token.IDENT
		}
	}
}

func softKeywordIsName(keyword, prev, next token.TokenType) bool {
	if keyword == token.IN {
		// The keyword comes after the variable of a loop and nowhere
		// else: for x in items, for (k, v) in pairs
		return prev != token.IDENT && prev != token.RPAREN
	}
	switch {
	case declaresName[prev]:
		return true
	case next == token.SEMICOLON && (prev == token.EOF || prev == token.SEMICOLON || prev == token.LBRACE || prev == token.RBRACE):
		// import; alone is a declaration with something missing: a
		// name alone would do nothing
		return false
	case followsValue[next]:
		return true
	case next == token.IN:
		// for case in cases
		return true
	case next == token.LBRACE:
		// if case { ... }; not `match {`, which has nothing to match
		return startsCondition[prev] && prev != token.ELSE
	case next == token.LPAREN || next == token.LBRACKET:
		// A call or an index: a function or an array called match is
		// told from `match (a, b) {` by nothing, nor `case (a, b):` or
		// `x as (Int, Int)`
		switch keyword {
		case token.MATCH, token.CASE, token.AS, token.IMPL:
			// impl [Int] { ... }, impl (Int, Int) { ... }
			return false
		}
		return true
	}
	return false
}
