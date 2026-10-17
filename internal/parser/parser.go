// Package parser turns Qoqi source text into an AST.
package parser

import (
	"fmt"
	"strings"

	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/token"
)

// Error codes for parsing
const (
	ErrUnexpectedToken  = "E0001" // Unexpected token or malformed construct
	ErrLexical          = "E0002" // Invalid character, unterminated string or comment
	ErrMissingSemicolon = "E0003" // Statement not terminated by ';'
	ErrInvalidAssign    = "E0004" // Left side of '=' is not assignable
	ErrDuplicateField   = "E0005" // Field given twice in a struct literal
)

// bailout is used to unwind the parser after a syntax error.
type bailout struct{}

// Parser is a recursive descent parser for Qoqi.
type Parser struct {
	toks        []token.Token
	pos         int
	errs        *errors.Handler
	sourceLines map[int]string

	// Struct literals are ambiguous with blocks in `if x {`, so they are
	// disabled while parsing the header of if/while/for/match.
	noStructLit bool

	// While speculating, syntax errors unwind without being reported.
	speculating bool

	// The ranges read so far that no loop and no case took: values
	keptRanges int

	// The names written after a dot
	members map[string]bool

	// In the pattern of a case a | separates patterns, and is not the
	// operator on bits it is everywhere else.
	noBitOr bool

	// How many names were made up so far, for those to differ
	made int

	// The expression about to be read is what is between the brackets
	// of an index, where a range may leave out its end: items[1..]
	openRange bool

	// A literal of a dictionary was read
	entries bool
	waits   bool
	stages  bool

	// The statements that follow the one just parsed, when it is short for
	// several (`let (a, b) = pair;`)
	pendingStmts []ast.Stmt
}

// makeName returns a name no program can write.
func (p *Parser) makeName(prefix string) string {
	p.made++
	return fmt.Sprintf("__%s%d", prefix, p.made)
}

// SourceLines splits source text into a 1-based line map for diagnostics.
func SourceLines(src string) map[int]string {
	lines := make(map[int]string)
	for i, line := range strings.Split(src, "\n") {
		lines[i+1] = strings.TrimRight(line, "\r")
	}
	return lines
}

// Parse parses a whole source file. Syntax errors are reported to errs; the
// returned program contains every declaration that parsed cleanly.
func Parse(src, file string, errs *errors.Handler) *ast.Program {
	toks := NewLexer(src, file).Tokenize()
	softKeywordsAsNames(toks)
	p := &Parser{
		toks:        toks,
		errs:        errs,
		sourceLines: SourceLines(src),
	}
	return p.parseProgram()
}

// --- Token helpers ---

func (p *Parser) cur() token.Token {
	return p.toks[p.pos]
}

func (p *Parser) peekAt(offset int) token.Token {
	if p.pos+offset >= len(p.toks) {
		return p.toks[len(p.toks)-1]
	}
	return p.toks[p.pos+offset]
}

func (p *Parser) at(typ token.TokenType) bool {
	return p.cur().Type == typ
}

func (p *Parser) next() token.Token {
	tok := p.cur()
	if tok.Type != token.EOF {
		p.pos++
	}
	return tok
}

func (p *Parser) accept(typ token.TokenType) bool {
	if p.at(typ) {
		p.next()
		return true
	}
	return false
}

func (p *Parser) expect(typ token.TokenType, what string) token.Token {
	if !p.at(typ) {
		p.fail("expected %s, found %s", what, describe(p.cur()))
	}
	return p.next()
}

// fail reports a syntax error at the current token and unwinds to the
// nearest recovery point.
func (p *Parser) fail(format string, args ...interface{}) {
	p.failWith(ErrUnexpectedToken, "", format, args...)
}

func (p *Parser) failWith(code, suggestion, format string, args ...interface{}) {
	if !p.speculating {
		tok := p.cur()
		msg := fmt.Sprintf(format, args...)
		if tok.Type == token.ILLEGAL {
			// The lexer already described the problem in the token literal
			code, msg, suggestion = ErrLexical, tok.Literal, lexicalSuggestion(tok.Literal)
		}
		if suggestion != "" {
			p.errs.ReportErrorWithSuggestion(code, msg, suggestion, tok, p.sourceLines)
		} else {
			p.errs.ReportErrorFromToken(code, msg, tok, p.sourceLines)
		}
	}
	panic(bailout{})
}

func lexicalSuggestion(msg string) string {
	switch {
	case strings.HasPrefix(msg, "unterminated string"):
		return "Add a closing '\"' before the end of the line"
	case strings.HasPrefix(msg, "unterminated block comment"):
		return "Add a closing '*/'"
	case strings.HasPrefix(msg, "invalid escape"):
		return "Valid escapes are \\n, \\t, \\r, \\0, \\\\, \\\", \\$ and \\u{1F600}"
	case strings.HasPrefix(msg, "unterminated '${'"):
		return "Close the expression with '}'; to write a dollar sign followed by a brace, escape it: \\${"
	case strings.HasPrefix(msg, "nothing between"):
		return "Put an expression between the braces: \"${name}\"; for the characters themselves, write \\${}"
	}
	return ""
}

// expectSemicolon consumes the ';' ending a statement. A missing one is
// reported but does not stop parsing: the parser carries on as if it were
// there, so the rest of the block is still checked.
func (p *Parser) expectSemicolon() {
	if p.accept(token.SEMICOLON) {
		return
	}
	if p.at(token.ILLEGAL) {
		p.fail("")
	}
	if p.speculating {
		panic(bailout{})
	}

	// Point just past the previous token, where the ';' belongs
	prev := p.toks[p.pos-1]
	at := token.Token{Type: token.SEMICOLON, Pos: prev.EndPos, EndPos: prev.EndPos}
	at.EndPos.Col++
	p.errs.ReportErrorWithSuggestion(
		ErrMissingSemicolon,
		fmt.Sprintf("expected ';' after %s, found %s", describe(prev), describe(p.cur())),
		"Add ';' to end the statement",
		at,
		p.sourceLines,
	)
}

func describe(tok token.Token) string {
	switch tok.Type {
	case token.EOF:
		return "end of file"
	case token.ILLEGAL:
		return tok.Literal
	case token.STRING:
		return fmt.Sprintf("string %q", tok.Literal)
	default:
		return "'" + tok.Literal + "'"
	}
}

func base(tok token.Token) ast.BaseNode {
	return ast.BaseNode{Pos: tok.Pos, Tok: tok}
}

// --- Program and declarations ---

func (p *Parser) parseProgram() *ast.Program {
	prog := &ast.Program{BaseNode: base(p.cur())}

	for !p.at(token.EOF) {
		p.parseTopLevel(prog)
	}
	classifyPatterns(prog)
	prog.KeepsRanges = p.keptRanges > 0
	prog.HasEntries = p.entries
	prog.HasWaits = p.waits
	prog.HasStages = p.stages
	prog.Members = p.members
	return prog
}

// parseTopLevel parses one import, declaration or statement, recovering
// on error so that several errors can be reported: to the next top-level
// keyword after a declaration, past the ';' after a statement.
func (p *Parser) parseTopLevel(prog *ast.Program) {
	if p.at(token.RBRACE) {
		// Not the start of a statement: one brace too many
		p.errs.ReportErrorWithSuggestion(ErrUnexpectedToken, "expected declaration or statement, found '}'",
			"Nothing is open here for it to close", p.cur(), p.sourceLines)
		p.next()
		return
	}
	if p.atStmt() {
		prog.StmtStarts = append(prog.StmtStarts, p.cur().Pos)
		if stmt := p.parseStmtRecovering(); stmt != nil {
			prog.Stmts = append(prog.Stmts, stmt)
		}
		// What a statement that stands for several left behind
		prog.Stmts = append(prog.Stmts, p.pendingStmts...)
		p.pendingStmts = nil
		return
	}

	start := p.pos
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(bailout); !ok {
				panic(r)
			}
			if p.pos == start {
				p.next()
			}
			p.synchronize()
		}
	}()

	pubTok, pub := p.cur(), p.accept(token.PUB)
	if p.at(token.IMPORT) {
		imp := p.parseImport()
		imp.Pub = pub
		prog.Imports = append(prog.Imports, imp)
		return
	}
	decl := p.parseDecl()
	if pub {
		p.markPub(decl, pubTok)
	}
	prog.Decls = append(prog.Decls, decl)
}

// atStmt says whether what comes next at the top level is a statement,
// one a script runs, rather than an import or a declaration. A `const`
// there is a constant of the program; `fn name` declares a function.
func (p *Parser) atStmt() bool {
	if p.atStep() {
		return false
	}
	switch p.cur().Type {
	case token.PUB, token.IMPORT, token.CONST, token.EXTERN, token.STRUCT, token.ENUM, token.TRAIT, token.IMPL:
		return false
	case token.FN:
		return p.peekAt(1).Type != token.IDENT
	}
	return true
}

// markPub records that a declaration is exported from its module.
func (p *Parser) markPub(decl ast.Decl, pubTok token.Token) {
	switch d := decl.(type) {
	case *ast.FnDecl:
		d.Pub = true
	case *ast.StructDecl:
		d.Pub = true
	case *ast.EnumDecl:
		d.Pub = true
	case *ast.TraitDecl:
		d.Pub = true
	case *ast.ConstDecl:
		d.Pub = true
	case *ast.ImplDecl:
		p.errs.ReportErrorWithSuggestion(ErrUnexpectedToken,
			"'pub' cannot be applied to an impl block",
			"Mark the methods instead: pub fn name() { ... }", pubTok, p.sourceLines)
	}
}

func (p *Parser) synchronize() {
	for {
		switch p.cur().Type {
		case token.EOF, token.STRUCT, token.ENUM, token.TRAIT, token.IMPL, token.IMPORT, token.PUB, token.EXTERN:
			return
		case token.CONST:
			// A constant of a function body is indented; one that starts a
			// line is a declaration
			if p.cur().Pos.Col == 1 {
				return
			}
		case token.FN:
			// `fn name` starts a declaration; `fn(` is a function type or
			// a lambda inside the broken declaration
			if p.peekAt(1).Type == token.IDENT {
				return
			}
		case token.LET, token.IF, token.WHILE, token.FOR, token.MATCH, token.IDENT:
			// A statement of a script starts a line; one of a function
			// body is indented
			if p.cur().Pos.Col == 1 {
				return
			}
		}
		p.next()
	}
}

func (p *Parser) parseImport() *ast.ImportDecl {
	tok := p.expect(token.IMPORT, "'import'")
	decl := &ast.ImportDecl{BaseNode: base(tok)}
	if p.at(token.STRING) {
		p.failWith(ErrUnexpectedToken, "Name the module by its path from the program's folder: import physics.body;",
			"expected module path, found %s", describe(p.cur()))
	}

	// The token of the import is its path, so diagnostics underline it
	first := p.expect(token.IDENT, "module path")
	decl.BaseNode = base(first)
	segments := []string{first.Literal}
	for p.accept(token.DOT) {
		if p.accept(token.LBRACE) {
			for !p.at(token.RBRACE) {
				nameTok := p.expect(token.IDENT, "name to import")
				decl.Names = append(decl.Names, &ast.ImportName{BaseNode: base(nameTok), Name: nameTok.Literal})
				if !p.accept(token.COMMA) {
					break
				}
			}
			closing := p.expect(token.RBRACE, "'}'")
			if len(decl.Names) == 0 {
				p.pos--
				p.failWith(ErrUnexpectedToken, "List the names to import, or import the module itself: import "+strings.Join(segments, ".")+";",
					"expected a name to import, found %s", describe(closing))
			}
			break
		}
		last := p.expect(token.IDENT, "module name")
		segments = append(segments, last.Literal)
		decl.Tok.EndPos = last.EndPos
	}
	decl.Path = strings.Join(segments, ".")

	if p.at(token.AS) {
		if len(decl.Names) > 0 {
			p.failWith(ErrUnexpectedToken, "An alias names the module; with a list of names, use the names directly",
				"'as' cannot follow a list of imported names")
		}
		p.next()
		decl.Alias = p.expect(token.IDENT, "import alias").Literal
	}
	p.expectSemicolon()
	return decl
}

func (p *Parser) parseDecl() ast.Decl {
	if p.atStep() {
		// step fn charge(...): a step of a flow
		p.next()
		return p.parseStepDecl()
	}
	switch p.cur().Type {
	case token.CONST:
		return p.parseConstDecl()
	case token.EXTERN:
		return p.parseExternDecl()
	case token.FN:
		return p.parseFnDecl(false)
	case token.STRUCT:
		return p.parseStructDecl()
	case token.ENUM:
		return p.parseEnumDecl()
	case token.TRAIT:
		return p.parseTraitDecl()
	case token.IMPL:
		return p.parseImplDecl()
	}
	p.fail("expected declaration (fn, struct, enum, trait, impl, const, import), found %s", describe(p.cur()))
	return nil
}

// parseConstDecl parses a constant at the top level: `const NAME = value;`
// with an optional type after the name.
// atStep reports whether a step of a flow is declared here: the word step
// before `fn name`. Anywhere else step is a name.
func (p *Parser) atStep() bool {
	tok := p.cur()
	return tok.Type == token.IDENT && tok.Literal == "step" && p.peekAt(1).Type == token.FN && p.peekAt(2).Type == token.IDENT
}

func (p *Parser) parseConstDecl() *ast.ConstDecl {
	p.expect(token.CONST, "'const'")
	nameTok := p.expect(token.IDENT, "constant name")
	decl := &ast.ConstDecl{BaseNode: base(nameTok), Name: nameTok.Literal}
	if p.accept(token.COLON) {
		decl.Type = p.parseType()
	}
	if !p.at(token.ASSIGN) {
		p.failWith(ErrUnexpectedToken, "A constant needs a value: const "+decl.Name+" = 1;",
			"expected '=', found %s", describe(p.cur()))
	}
	p.next()
	decl.Value = p.parseExpr()
	p.expectSemicolon()
	return decl
}

// parseExternDecl parses a function the runtime provides, which a
// program declares without writing it:
//
//	extern fn name(params) -> Type;
func (p *Parser) parseExternDecl() ast.Decl {
	p.expect(token.EXTERN, "'extern'")
	if !p.at(token.FN) {
		p.failWith(ErrUnexpectedToken, "extern fn name(...);",
			"expected 'fn' after 'extern', found %s", describe(p.cur()))
	}
	tok := p.expect(token.FN, "'fn' after 'extern'")
	decl := &ast.FnDecl{BaseNode: base(tok), Extern: true}

	nameTok := p.expect(token.IDENT, "function name")
	decl.Name = nameTok.Literal
	decl.BaseNode = base(nameTok)
	if p.at(token.LT) {
		p.failWith(ErrUnexpectedToken, "Give the parameters concrete types, or 'Any'",
			"an extern function cannot have type parameters")
	}
	decl.Params = p.parseParams()
	if p.accept(token.ARROW) {
		decl.ReturnType = p.parseType()
	}
	if p.at(token.LBRACE) {
		p.failWith(ErrUnexpectedToken, "Remove 'extern' to write the function here, or end the declaration with ';'",
			"an extern function has no body: the runtime provides it")
	}
	p.expectSemicolon()
	return decl
}

func (p *Parser) parseTypeParams() []string {
	params, _ := p.parseBoundedTypeParams(false)
	return params
}

// parseBoundedTypeParams parses `<T, U: Ordered + Show>`: type parameters,
// each with the traits it is required to implement, if any. Where bounds
// make no sense (the parameters of a trait, the ones repeated after the
// struct name of an impl) they are refused.
func (p *Parser) parseBoundedTypeParams(bounded bool) ([]string, map[string][]*ast.Type) {
	var params []string
	var bounds map[string][]*ast.Type
	if !p.accept(token.LT) {
		return nil, nil
	}
	for {
		name := p.expect(token.IDENT, "type parameter name").Literal
		params = append(params, name)
		if p.at(token.COLON) {
			if !bounded {
				p.failWith(ErrUnexpectedToken, "Say what a type parameter has to implement where it is introduced: after 'fn name', 'struct Name', 'enum Name' or 'impl'",
					"a type parameter cannot be given a requirement here")
			}
			p.next()
			if bounds == nil {
				bounds = make(map[string][]*ast.Type)
			}
			for {
				bounds[name] = append(bounds[name], p.parseTraitName())
				if !p.accept(token.PLUS) {
					break
				}
			}
		}
		if !p.accept(token.COMMA) {
			break
		}
	}
	p.expect(token.GT, "'>'")
	return params, bounds
}

// parseTraitName parses the name of a trait, with its type arguments if it
// has any: `Ordered`, `shapes.Drawable`, `Source<Int>`.
func (p *Parser) parseTraitName() *ast.Type {
	tok := p.cur()
	name := p.expectTypeName("trait name")
	tok.Literal = name
	t := &ast.Type{BaseNode: base(tok), Kind: ast.TypeBasic, Name: name}
	if p.accept(token.LT) {
		t.Generics = p.parseTypeList(token.GT)
		p.expect(token.GT, "'>'")
	}
	return t
}

// parseFnDecl parses a function declaration. Trait methods have no body.
func (p *Parser) parseFnDecl(traitMethod bool) *ast.FnDecl {
	tok := p.expect(token.FN, "'fn'")
	decl := &ast.FnDecl{BaseNode: base(tok), IsTraitMethod: traitMethod}
	decl.Name = p.expect(token.IDENT, "function name").Literal
	decl.TypeParams, decl.Bounds = p.parseBoundedTypeParams(true)
	decl.Params = p.parseParams()
	if p.accept(token.ARROW) {
		decl.ReturnType = p.parseType()
	}

	if traitMethod && p.accept(token.SEMICOLON) {
		return decl
	}
	decl.Body = p.parseBlock()
	return decl
}

// parseStepDecl parses a step of a flow, after the word step. How long
// it may take comes after its signature, the one place a step says it;
// then its body, or, for a step the application carries out, its queue
// and what undoes it:
//
//	step fn capture(id: String) -> Settled<String> within 5.minutes() { ... }
//	step fn charge(invoice: Invoice) -> Result<Charge, Decline> by billing;
//	step fn reserve(cart: Cart) -> Result<Hold, Problem> by warehouse undo release;
//	step fn wire(to: Account) -> Settled<Wire> within 2.hours() by bank;
//
// A step in Qoqi begins with __std_flow.limit(d), which tells the
// runtime; the body a step of a queue is given sends it there, through
// std.flow as `stage` reaches it: __std_flow.deliver("billing",
// "invoice", 60000) as the type it returns.
func (p *Parser) parseStepDecl() *ast.FnDecl {
	tok := p.expect(token.FN, "'fn'")
	decl := &ast.FnDecl{BaseNode: base(tok), Step: true}
	decl.Name = p.expect(token.IDENT, "function name").Literal
	decl.TypeParams, decl.Bounds = p.parseBoundedTypeParams(true)
	decl.Params = p.parseParams()
	if p.accept(token.ARROW) {
		decl.ReturnType = p.parseType()
	}
	withinTok := p.cur()
	if p.atWord("within") {
		p.next()
		decl.Within = p.parseHeaderExpr()
	}
	if !p.atWord("by") {
		decl.Body = p.parseBlock()
		if decl.Within != nil {
			module := &ast.Ident{BaseNode: base(withinTok), Name: ast.FlowModule}
			limit := &ast.CallExpr{BaseNode: base(withinTok), Func: &ast.MemberExpr{BaseNode: base(withinTok), Expr: module, Name: "limit"},
				Args: []ast.Expr{lengthOf(withinTok, decl.Within)}}
			decl.Body.Stmts = append([]ast.Stmt{&ast.ExprStmt{BaseNode: base(withinTok), Expr: limit}}, decl.Body.Stmts...)
			p.stages = true
		}
		return decl
	}

	p.next()
	queue := p.expect(token.IDENT, "the name of a queue")
	decl.Queue, decl.QueueTok = queue.Literal, queue
	if p.atWord("undo") {
		p.next()
		decl.Undo = p.expect(token.IDENT, "the step that undoes this one").Literal
	}
	if p.atWord("within") {
		p.failWith(ErrUnexpectedToken, "Say how long it may take after its signature: step fn "+decl.Name+"(...) -> T within 2.hours() by "+decl.Queue+";",
			"within comes before by")
	}
	if p.at(token.LBRACE) {
		p.failWith(ErrUnexpectedToken, "A step its queue carries out has no body: the application writes it",
			"a step by %s has no body", decl.Queue)
	}
	p.expectSemicolon()
	if len(decl.TypeParams) > 0 {
		p.errs.ReportErrorWithSuggestion(ErrUnexpectedToken,
			"a step its queue carries out cannot have type parameters",
			"Give its parameters and what it returns the types the application reads and writes", tok, p.sourceLines)
	}

	var names []string
	for _, param := range decl.Params {
		names = append(names, param.Name)
	}
	// A minute, unless it says
	var within ast.Expr = &ast.Literal{BaseNode: base(queue), Value: "60000", Kind: "int"}
	if decl.Within != nil {
		within = lengthOf(queue, decl.Within)
	}
	args := []ast.Expr{
		&ast.Literal{BaseNode: base(queue), Value: decl.Queue, Kind: "string"},
		&ast.Literal{BaseNode: base(queue), Value: strings.Join(names, ","), Kind: "string"},
		within,
	}
	deliver := "deliver"
	if decl.Undo != "" {
		deliver = "deliverUndone"
		args = append(args, &ast.Ident{BaseNode: base(queue), Name: decl.Undo})
	}
	module := &ast.Ident{BaseNode: base(queue), Name: ast.FlowModule}
	var sent ast.Expr = &ast.CallExpr{BaseNode: base(queue), Func: &ast.MemberExpr{BaseNode: base(queue), Expr: module, Name: deliver}, Args: args}
	var stmt ast.Stmt = &ast.ExprStmt{BaseNode: base(queue), Expr: sent}
	if decl.ReturnType != nil {
		stmt = &ast.ReturnStmt{BaseNode: base(queue), Value: &ast.CastExpr{BaseNode: base(queue), Expr: sent, Type: (&ast.Cloner{}).Type(decl.ReturnType)}}
	}
	decl.Body = &ast.BlockStmt{BaseNode: base(queue), Stmts: []ast.Stmt{stmt}}
	p.stages = true
	return decl
}

// lengthOf is how long a step may take, `within 2.hours()`, as the
// milliseconds std.flow takes: __std_flow.lengthOf(2.hours()). A copy:
// the body is named and checked on its own.
func lengthOf(tok token.Token, within ast.Expr) ast.Expr {
	module := &ast.Ident{BaseNode: base(tok), Name: ast.FlowModule}
	return &ast.CallExpr{BaseNode: base(tok), Func: &ast.MemberExpr{BaseNode: base(tok), Expr: module, Name: "lengthOf"},
		Args: []ast.Expr{(&ast.Cloner{}).Expr(within)}}
}

// atWord says whether the next token is a name that is a word here only:
// by, undo, within after a step's signature.
func (p *Parser) atWord(word string) bool {
	return p.at(token.IDENT) && p.cur().Literal == word
}

func (p *Parser) parseParams() []*ast.Param {
	var params []*ast.Param
	p.expect(token.LPAREN, "'('")
	for !p.at(token.RPAREN) {
		if t := p.cur().Type; t == token.LBRACKET || t == token.FN || t == token.ELLIPSIS || isBuiltinTypeToken(t) {
			p.failWith(ErrUnexpectedToken, "Parameters are written as 'name: Type'",
				"expected parameter name, found %s", describe(p.cur()))
		}
		tok := p.expect(token.IDENT, "parameter name")
		param := &ast.Param{BaseNode: base(tok), Name: tok.Literal}
		p.expect(token.COLON, "':'")
		param.Type = p.parseType()
		if p.accept(token.ASSIGN) {
			// mode: String = "r": what a call that leaves it out gives
			param.Default = p.parseNestedExpr()
		} else if len(params) > 0 && params[len(params)-1].Default != nil {
			p.errs.ReportErrorWithSuggestion(ErrUnexpectedToken,
				fmt.Sprintf("parameter '%s' has no value of its own, and comes after one that has", param.Name),
				"Put the parameters with a value of their own last: a call leaves out the ones at the end",
				tok, p.sourceLines)
		}
		params = append(params, param)
		if !p.accept(token.COMMA) {
			break
		}
	}
	p.expect(token.RPAREN, "')'")
	return params
}

func (p *Parser) parseStructDecl() *ast.StructDecl {
	tok := p.expect(token.STRUCT, "'struct'")
	decl := &ast.StructDecl{BaseNode: base(tok)}
	decl.Name = p.expect(token.IDENT, "struct name").Literal
	decl.TypeParams, decl.Bounds = p.parseBoundedTypeParams(true)

	p.expect(token.LBRACE, "'{'")
	for !p.at(token.RBRACE) {
		// priv keys: [String]; the word is a name anywhere else
		private := false
		if p.at(token.IDENT) && p.cur().Literal == "priv" && p.peekAt(1).Type == token.IDENT {
			p.next()
			private = true
		}
		nameTok := p.expect(token.IDENT, "field name")
		field := &ast.Field{BaseNode: base(nameTok), Name: nameTok.Literal, Private: private}
		p.expect(token.COLON, "':'")
		field.Type = p.parseType()
		if p.accept(token.ASSIGN) {
			// port: Int = 8080; what the field holds when a literal does
			// not say
			field.Default = p.parseExpr()
		}
		decl.Fields = append(decl.Fields, field)
		if !p.accept(token.SEMICOLON) && !p.accept(token.COMMA) {
			break
		}
	}
	p.expect(token.RBRACE, "'}'")
	return decl
}

func (p *Parser) parseEnumDecl() *ast.EnumDecl {
	tok := p.expect(token.ENUM, "'enum'")
	decl := &ast.EnumDecl{BaseNode: base(tok)}
	decl.Name = p.expect(token.IDENT, "enum name").Literal
	decl.TypeParams, decl.Bounds = p.parseBoundedTypeParams(true)

	p.expect(token.LBRACE, "'{'")
	for !p.at(token.RBRACE) {
		nameTok := p.expectMemberName("variant name")
		variant := &ast.EnumVariant{BaseNode: base(nameTok), Name: nameTok.Literal}
		if p.accept(token.LPAREN) {
			for !p.at(token.RPAREN) {
				variant.Values = append(variant.Values, p.parseType())
				if !p.accept(token.COMMA) {
					break
				}
			}
			p.expect(token.RPAREN, "')'")
		} else if p.accept(token.LBRACE) {
			// Rect { width: Float, height: Float }: values with names
			for !p.at(token.RBRACE) {
				fieldTok := p.expect(token.IDENT, "the name of a value")
				for _, given := range variant.Names {
					if given == fieldTok.Literal {
						p.pos--
						p.failWith(ErrDuplicateField, "Give each value its own name",
							"'%s' names two values of %s", given, variant.Name)
					}
				}
				p.expect(token.COLON, "':'")
				variant.Names = append(variant.Names, fieldTok.Literal)
				variant.Values = append(variant.Values, p.parseType())
				if !p.accept(token.COMMA) && !p.accept(token.SEMICOLON) {
					break
				}
			}
			p.expect(token.RBRACE, "'}'")
		}
		decl.Variants = append(decl.Variants, variant)
		if !p.accept(token.COMMA) && !p.accept(token.SEMICOLON) {
			break
		}
	}
	p.expect(token.RBRACE, "'}'")
	return decl
}

func (p *Parser) parseTraitDecl() *ast.TraitDecl {
	tok := p.expect(token.TRAIT, "'trait'")
	decl := &ast.TraitDecl{BaseNode: base(tok)}
	decl.Name = p.expect(token.IDENT, "trait name").Literal
	decl.TypeParams = p.parseTypeParams()

	p.expect(token.LBRACE, "'{'")
	for !p.at(token.RBRACE) {
		decl.Methods = append(decl.Methods, p.parseFnDecl(true))
	}
	p.expect(token.RBRACE, "'}'")
	return decl
}

// parseImplDecl parses `impl Trait for Struct { ... }` or `impl Struct { ... }`.
// For a generic struct the type parameters are introduced after `impl` and
// repeated on the struct: `impl<T> Box<T> { ... }`.
func (p *Parser) parseImplDecl() *ast.ImplDecl {
	tok := p.expect(token.IMPL, "'impl'")
	decl := &ast.ImplDecl{BaseNode: base(tok)}
	decl.TypeParams, decl.Bounds = p.parseBoundedTypeParams(true)

	if p.at(token.LBRACKET) {
		// impl<T> [T] { ... }: methods for every array
		decl.StructName = p.arrayImplTarget(decl)
		return p.parseImplBody(decl)
	}
	if len(decl.TypeParams) == 1 && p.at(token.IDENT) && p.cur().Literal == decl.TypeParams[0] && p.peekAt(1).Type == token.QUESTION {
		// impl<T> T? { ... }: methods for every optional
		p.next()
		p.next()
		decl.StructName = ast.OptionalImpl
		return p.parseImplBody(decl)
	}

	first := p.expectTypeName("trait or struct name")
	// impl Source<Int> for Counter: the type arguments of a trait are
	// types, where those of the struct can only repeat the parameters of
	// the impl. Which of the two follows is known once they are read.
	if p.at(token.LT) {
		start := p.pos
		if args := p.tryTraitArgs(); args != nil {
			decl.TraitArgs = args
		} else {
			p.pos = start
		}
	}
	if p.accept(token.FOR) {
		decl.TraitName = first
		if p.at(token.LBRACKET) {
			// impl<T> Sized for [T] { ... }
			decl.StructName = p.arrayImplTarget(decl)
			return p.parseImplBody(decl)
		}
		decl.StructName = p.expectTypeName("struct name")
	} else {
		decl.StructName = first
	}
	p.expectImplTypeArgs(decl)
	return p.parseImplBody(decl)
}

// arrayImplTarget reads the `[T]` of an impl for arrays. What the arrays
// hold is the one type parameter of the impl: the methods are those of
// every array, whatever it holds.
func (p *Parser) arrayImplTarget(decl *ast.ImplDecl) string {
	open := p.expect(token.LBRACKET, "'['")
	wrong := func() {
		p.errs.ReportErrorWithSuggestion(ErrUnexpectedToken,
			"an impl for arrays is for every array, or for the arrays of one type",
			"Write: impl<T> [T] { ... }, where T is the type of the items; or impl [Int] { ... }", open, p.sourceLines)
	}
	if p.at(token.IDENT) && len(decl.TypeParams) == 1 && p.cur().Literal == decl.TypeParams[0] {
		p.next()
	} else if len(decl.TypeParams) == 0 && (p.at(token.IDENT) || isBuiltinTypeToken(p.cur().Type)) {
		// impl [Int] { ... }: methods for the arrays of one type
		item := p.expectTypeName("the type of the items")
		p.expect(token.RBRACKET, "']'")
		return ast.ArrayImplOf(item)
	} else {
		if !p.speculating {
			wrong()
		}
		for !p.at(token.RBRACKET) && !p.at(token.LBRACE) && !p.at(token.EOF) {
			p.next()
		}
	}
	p.expect(token.RBRACKET, "']'")
	return ast.ArrayImpl
}

func (p *Parser) parseImplBody(decl *ast.ImplDecl) *ast.ImplDecl {
	p.expect(token.LBRACE, "'{'")
	for !p.at(token.RBRACE) {
		pub := p.accept(token.PUB)
		// static fn origin(): a function of the type. The word is a name
		// anywhere else
		static, migrate := false, false
		if p.at(token.IDENT) && p.cur().Literal == "static" && p.peekAt(1).Type == token.FN {
			p.next()
			static = true
		}
		// migrate fn polling(...): a migration of a flow, named after a
		// stage of the version before
		if p.at(token.IDENT) && p.cur().Literal == "migrate" && p.peekAt(1).Type == token.FN {
			p.next()
			migrate = true
		}
		method := p.parseFnDecl(false)
		method.Pub = pub
		method.Static = static
		method.Migrate = migrate
		decl.Methods = append(decl.Methods, method)
	}
	p.expect(token.RBRACE, "'}'")
	return decl
}

// tryTraitArgs parses `<Int, [T]>` when `for` follows it, which makes it
// the type arguments of the trait of an impl. Otherwise it returns nil.
func (p *Parser) tryTraitArgs() (args []*ast.Type) {
	wasSpeculating := p.speculating
	p.speculating = true
	defer func() {
		p.speculating = wasSpeculating
		if r := recover(); r != nil {
			if _, ok := r.(bailout); !ok {
				panic(r)
			}
			args = nil
		}
	}()

	p.expect(token.LT, "'<'")
	list := p.parseTypeList(token.GT)
	p.expect(token.GT, "'>'")
	if !p.at(token.FOR) {
		return nil
	}
	return list
}

// expectImplTypeArgs parses the `<T, U>` after the struct name of an impl
// and checks that it repeats the impl's type parameters in order, since
// that is how a method's T is tied to the struct's. A mismatch is reported
// without abandoning the impl, so its methods are still parsed.
func (p *Parser) expectImplTypeArgs(decl *ast.ImplDecl) {
	params := strings.Join(decl.TypeParams, ", ")
	correct := fmt.Sprintf("Write: impl<%s> %s<%s>", params, decl.StructName, params)

	if !p.at(token.LT) {
		if len(decl.TypeParams) > 0 {
			p.errs.ReportErrorWithSuggestion(ErrUnexpectedToken,
				fmt.Sprintf("expected the type parameters of '%s' after its name", decl.StructName),
				correct, p.cur(), p.sourceLines)
		}
		return
	}

	start := p.cur()
	if args := p.parseTypeParams(); strings.Join(args, ", ") != params {
		if len(decl.TypeParams) == 0 {
			correct = fmt.Sprintf("Write: impl<%s> %s<%s>", strings.Join(args, ", "), decl.StructName, strings.Join(args, ", "))
		}
		p.errs.ReportErrorWithSuggestion(ErrUnexpectedToken,
			fmt.Sprintf("type arguments of '%s' must repeat the parameters declared after 'impl'", decl.StructName),
			correct, start, p.sourceLines)
	}
}

// expectMemberName reads a name that follows a dot, or names a variant of
// an enum. The names of the builtin types are allowed there, where they
// cannot be taken for a type: a variant may be called Bool or String, as
// in `Json.Bool(true)`.
func (p *Parser) expectMemberName(what string) token.Token {
	if isBuiltinTypeToken(p.cur().Type) {
		return p.next()
	}
	return p.expect(token.IDENT, what)
}

func (p *Parser) expectTypeName(what string) string {
	if isBuiltinTypeToken(p.cur().Type) {
		return p.next().Literal
	}
	name := p.expect(token.IDENT, what).Literal
	// A type of another module: module.Name
	if p.at(token.DOT) && p.peekAt(1).Type == token.IDENT {
		p.next()
		name += "." + p.next().Literal
	}
	return name
}

// --- Types ---

func isBuiltinTypeToken(typ token.TokenType) bool {
	switch typ {
	case token.INT_TYPE, token.FLOAT_TYPE, token.BOOL_TYPE, token.STRING_TYPE,
		token.ANY_TYPE, token.VOID_TYPE, token.SELF_TYPE:
		return true
	}
	return false
}

// parseType parses a type, including unions: T1 | T2.
func (p *Parser) parseType() *ast.Type {
	first := p.parsePostfixType()
	if !p.at(token.PIPE) {
		return first
	}

	union := &ast.Type{BaseNode: first.BaseNode, Kind: ast.TypeUnion, UnionTypes: []*ast.Type{first}}
	for p.accept(token.PIPE) {
		union.UnionTypes = append(union.UnionTypes, p.parsePostfixType())
	}
	return union
}

func (p *Parser) parsePostfixType() *ast.Type {
	t := p.parsePrimaryType()
	for p.accept(token.QUESTION) {
		t = &ast.Type{BaseNode: t.BaseNode, Kind: ast.TypeOption, ElemType: t}
	}
	return t
}

func (p *Parser) parsePrimaryType() *ast.Type {
	tok := p.cur()

	switch {
	case isBuiltinTypeToken(tok.Type):
		p.next()
		return &ast.Type{BaseNode: base(tok), Kind: ast.TypeBasic, Name: tok.Literal}

	case tok.Type == token.IDENT:
		p.next()
		t := &ast.Type{BaseNode: base(tok), Kind: ast.TypeBasic, Name: tok.Literal}
		// A type of another module: module.Name
		if p.at(token.DOT) && p.peekAt(1).Type == token.IDENT {
			p.next()
			member := p.next()
			t.Name += "." + member.Literal
			t.Tok.Literal = t.Name
			t.Tok.EndPos = member.EndPos
		}
		if p.accept(token.LT) {
			t.Generics = p.parseTypeList(token.GT)
			p.expect(token.GT, "'>'")
		}
		return t

	case tok.Type == token.LBRACKET:
		p.next()
		elem := p.parseType()
		p.expect(token.RBRACKET, "']'")
		return &ast.Type{BaseNode: base(tok), Kind: ast.TypeArray, ElemType: elem}

	case tok.Type == token.LPAREN:
		p.next()
		t := p.parseType()
		if !p.at(token.COMMA) {
			p.expect(token.RPAREN, "')'")
			return t
		}
		// (Int, String): a tuple
		elems := []*ast.Type{t}
		for p.accept(token.COMMA) && !p.at(token.RPAREN) {
			elems = append(elems, p.parseType())
		}
		p.expect(token.RPAREN, "')'")
		p.checkTupleSize(tok, len(elems))
		return &ast.Type{BaseNode: base(tok), Kind: ast.TypeBasic, Name: ast.TupleName(len(elems)), Generics: elems}

	case tok.Type == token.ELLIPSIS:
		p.next()
		return &ast.Type{BaseNode: base(tok), Kind: ast.TypeVariadic, ElemType: p.parseType()}

	case tok.Type == token.FN:
		p.next()
		t := &ast.Type{BaseNode: base(tok), Kind: ast.TypeFunction}
		p.expect(token.LPAREN, "'('")
		t.ParamTypes = p.parseTypeList(token.RPAREN)
		p.expect(token.RPAREN, "')'")
		if p.accept(token.ARROW) {
			t.ReturnType = p.parseType()
		}
		return t
	}

	p.fail("expected type, found %s", describe(tok))
	return nil
}

func (p *Parser) parseTypeList(end token.TokenType) []*ast.Type {
	var list []*ast.Type
	for !p.at(end) {
		list = append(list, p.parseType())
		if !p.accept(token.COMMA) {
			break
		}
	}
	return list
}

// --- Statements ---

func (p *Parser) parseBlock() *ast.BlockStmt {
	tok := p.expect(token.LBRACE, "'{'")
	block := &ast.BlockStmt{BaseNode: base(tok)}

	// Struct literals are fine again inside a block body.
	saved := p.noStructLit
	p.noStructLit = false
	for !p.at(token.RBRACE) && !p.at(token.EOF) {
		if stmt := p.parseStmtRecovering(); stmt != nil {
			block.Stmts = append(block.Stmts, stmt)
		}
		// What a statement that stands for several left behind
		block.Stmts = append(block.Stmts, p.pendingStmts...)
		p.pendingStmts = nil
	}
	p.noStructLit = saved

	p.expect(token.RBRACE, "'}'")
	return block
}

// parseStmtRecovering parses one statement. On a syntax error it skips to
// the end of the broken statement and returns nil, so the following
// statements are still parsed and checked.
func (p *Parser) parseStmtRecovering() (stmt ast.Stmt) {
	if p.speculating {
		return p.parseStmt()
	}

	start := p.pos
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(bailout); !ok {
				panic(r)
			}
			stmt = nil
			if p.pos == start {
				p.next()
			}
			p.skipToStmtEnd()
		}
	}()
	return p.parseStmt()
}

// skipToStmtEnd advances past the next ';' at the current nesting level, or
// stops before the '}' that closes the enclosing block.
func (p *Parser) skipToStmtEnd() {
	depth := 0
	for !p.at(token.EOF) {
		switch p.cur().Type {
		case token.LBRACE:
			depth++
		case token.RBRACE:
			if depth == 0 {
				return
			}
			depth--
			if depth == 0 {
				p.next()
				return
			}
		case token.SEMICOLON:
			if depth == 0 {
				p.next()
				return
			}
		}
		p.next()
	}
}

func (p *Parser) parseStmt() ast.Stmt {
	tok := p.cur()
	if tok.Type == token.IDENT && tok.Literal == "stage" && p.peekAt(1).Type == token.IDENT && p.peekAt(2).Type == token.SEMICOLON {
		// stage polling;: where an instance of a flow may move to a new
		// version. Anywhere else stage is a name
		p.next()
		name := p.next()
		p.next()
		p.stages = true
		module := &ast.Ident{BaseNode: base(tok), Name: ast.FlowModule}
		arg := &ast.Literal{BaseNode: base(name), Value: name.Literal, Kind: "string"}
		call := &ast.CallExpr{BaseNode: base(tok), Func: &ast.MemberExpr{BaseNode: base(tok), Expr: module, Name: "passStage"}, Args: []ast.Expr{arg}}
		return &ast.ExprStmt{BaseNode: base(tok), Expr: call, Stage: name.Literal}
	}
	if p.atWait() && p.peekAt(1).Type == token.MATCH {
		return p.parseWaitMatchStmt()
	}

	switch tok.Type {
	case token.LET, token.CONST:
		if p.peekAt(1).Type == token.LPAREN {
			return p.parseLetTuple()
		}
		return p.parseLetStmt()
	case token.IF:
		return p.parseIfStmt()
	case token.WHILE:
		p.next()
		cond := p.parseHeaderExpr()
		return &ast.WhileStmt{BaseNode: base(tok), Cond: cond, Body: p.parseBlock()}
	case token.FOR:
		p.next()
		// for item in items, or for index, item in items
		stmt := &ast.ForStmt{BaseNode: base(tok)}
		// for (key, value) in pairs: each item is a tuple, taken apart
		var apart []token.Token
		loopVar := func() string {
			if p.at(token.LPAREN) {
				apart = p.parseTupleNames()
				return p.makeName("tuple")
			}
			return p.expect(token.IDENT, "loop variable name").Literal
		}
		nameTok := p.cur()
		stmt.Var = loopVar()
		if p.accept(token.COMMA) {
			if apart != nil {
				p.fail("expected 'in': the position comes first, as in for i, (key, value) in pairs")
			}
			stmt.Index = &ast.ForIndex{BaseNode: base(nameTok), Name: nameTok.Literal}
			stmt.Var = loopVar()
		}
		p.expect(token.IN, "'in'")
		stmt.Iterable = p.counted(p.parseHeaderExpr())
		stmt.Body = p.parseBlock()
		if apart != nil {
			item := &ast.Ident{BaseNode: base(nameTok), Name: stmt.Var}
			stmt.Body.Stmts = append(p.tupleLets(apart, item, false), stmt.Body.Stmts...)
		}
		return stmt
	case token.MATCH:
		return p.parseMatchStmt()
	case token.DEFER:
		// defer call(); or defer { ... }: what to do when the block is left
		p.next()
		stmt := &ast.DeferStmt{BaseNode: base(tok)}
		if p.at(token.LBRACE) {
			stmt.Body = p.parseBlock()
		} else {
			stmt.Body = &ast.BlockStmt{BaseNode: base(tok), Stmts: []ast.Stmt{p.parseExprStmt()}}
		}
		return stmt
	case token.RETURN:
		p.next()
		stmt := &ast.ReturnStmt{BaseNode: base(tok)}
		if !p.at(token.SEMICOLON) {
			stmt.Value = p.parseExpr()
		}
		p.expectSemicolon()
		return stmt
	case token.BREAK:
		p.next()
		p.expectSemicolon()
		return &ast.BreakStmt{BaseNode: base(tok)}
	case token.CONTINUE:
		p.next()
		p.expectSemicolon()
		return &ast.ContinueStmt{BaseNode: base(tok)}
	case token.LBRACE:
		return p.parseBlock()
	}

	return p.parseExprStmt()
}

func (p *Parser) parseLetStmt() *ast.LetStmt {
	tok := p.next()
	stmt := &ast.LetStmt{BaseNode: base(tok), IsConst: tok.Type == token.CONST}
	stmt.NameTok = p.expect(token.IDENT, "variable name")
	stmt.Name = stmt.NameTok.Literal
	if p.accept(token.COLON) {
		stmt.Type = p.parseType()
	}
	p.expect(token.ASSIGN, "'='")
	stmt.Value = p.parseExpr()
	p.expectSemicolon()
	return stmt
}

// parseLetTuple parses `let (a, b) = pair;`, which names the values of a
// tuple. It is short for
//
//	let __tuple1 = pair;
//	let a = __tuple1.0;
//	let b = __tuple1.1;
//
// and is returned as the first of these, the others being kept for the
// block to take (see parseBlock). A `_` takes a value without naming it.
func (p *Parser) parseLetTuple() ast.Stmt {
	tok := p.next()
	names := p.parseTupleNames()
	p.expect(token.ASSIGN, "'='")
	whole := &ast.LetStmt{BaseNode: base(tok), Name: p.makeName("tuple"), Value: p.parseExpr()}
	p.expectSemicolon()

	item := &ast.Ident{BaseNode: base(tok), Name: whole.Name}
	p.pendingStmts = append(p.pendingStmts, p.tupleLets(names, item, tok.Type == token.CONST)...)
	return whole
}

// parseTupleNames parses the `(a, b)` of a tuple being taken apart.
func (p *Parser) parseTupleNames() []token.Token {
	open := p.expect(token.LPAREN, "'('")
	var names []token.Token
	for !p.at(token.RPAREN) {
		names = append(names, p.expect(token.IDENT, "a name for a value of the tuple"))
		if !p.accept(token.COMMA) {
			break
		}
	}
	p.expect(token.RPAREN, "')'")
	if len(names) < ast.MinTupleSize {
		p.failWith(ErrUnexpectedToken, "Name each value of the tuple: let (first, second) = pair;",
			"a tuple has at least two values, and %d %s named here", len(names), map[bool]string{true: "is", false: "are"}[len(names) == 1])
	}
	p.checkTupleSize(open, len(names))
	return names
}

// tupleLets declares a variable for each named value of a tuple.
func (p *Parser) tupleLets(names []token.Token, tuple *ast.Ident, isConst bool) []ast.Stmt {
	var lets []ast.Stmt
	for i, name := range names {
		if name.Literal == "_" {
			continue
		}
		copy := *tuple
		lets = append(lets, &ast.LetStmt{
			BaseNode: base(name),
			Name:     name.Literal,
			IsConst:  isConst,
			Value:    &ast.MemberExpr{BaseNode: base(name), Expr: &copy, Name: fmt.Sprint(i), TupleSize: len(names)},
		})
	}
	return lets
}

// tuple builds the value `(a, b)`: the variant of the enum behind tuples
// of that size.
func (p *Parser) tuple(open token.Token, elems []ast.Expr) ast.Expr {
	p.checkTupleSize(open, len(elems))
	enum := &ast.Ident{BaseNode: base(open), Name: ast.TupleName(len(elems))}
	variant := &ast.MemberExpr{BaseNode: base(open), Expr: enum, Name: ast.TupleVariant}
	return &ast.CallExpr{BaseNode: base(open), Func: variant, Args: elems}
}

func (p *Parser) checkTupleSize(open token.Token, size int) {
	if size <= ast.MaxTupleSize {
		return
	}
	if !p.speculating {
		p.errs.ReportErrorWithSuggestion(ErrUnexpectedToken,
			fmt.Sprintf("a tuple holds %d values at most, and this one has %d", ast.MaxTupleSize, size),
			"Use a struct: with that many values, each deserves a name", open, p.sourceLines)
	}
	panic(bailout{})
}

func (p *Parser) parseIfStmt() *ast.IfStmt {
	tok := p.expect(token.IF, "'if'")
	stmt := &ast.IfStmt{BaseNode: base(tok)}
	stmt.Cond = p.parseHeaderExpr()
	stmt.Then = p.parseBlock()
	if p.accept(token.ELSE) {
		if p.at(token.IF) {
			stmt.Else = p.parseIfStmt()
		} else {
			stmt.Else = p.parseBlock()
		}
	}
	return stmt
}

// parseIfExpr parses an if where a value is expected:
//
//	let size = if n > 100 { "large" } else if n > 10 { "medium" } else { "small" };
//
// It always has an else: there is a value either way. A branch is an
// expression in braces, after statements if it needs some; one that ends
// in return, break, continue or panic gives nothing, and need not.
func (p *Parser) parseIfExpr() ast.Expr {
	tok := p.expect(token.IF, "'if'")
	expr := &ast.CondExpr{BaseNode: base(tok)}
	expr.Cond = p.parseHeaderExpr()
	expr.Then = p.parseValueBlock()
	if !p.at(token.ELSE) {
		p.failWith(ErrUnexpectedToken, "Say what the value is otherwise: if condition { one } else { other }",
			"an 'if' that gives a value needs an 'else', found %s", describe(p.cur()))
	}
	p.next()
	if p.at(token.IF) {
		expr.Else = p.parseIfExpr()
	} else {
		expr.Else = p.parseValueBlock()
	}
	return expr
}

// parseValueBlock parses the branch of an if that gives a value: braces
// around an expression, which statements may come before.
func (p *Parser) parseValueBlock() ast.Expr {
	open := p.expect(token.LBRACE, "'{'")
	saved, savedOr := p.noStructLit, p.noBitOr
	p.noStructLit, p.noBitOr = false, false
	defer func() { p.noStructLit, p.noBitOr = saved, savedOr }()

	block := &ast.BlockExpr{BaseNode: base(open)}
	for !p.at(token.RBRACE) && !p.at(token.EOF) {
		switch p.cur().Type {
		case token.LET, token.RETURN, token.BREAK, token.CONTINUE, token.WHILE, token.FOR, token.DEFER:
			block.Stmts = append(block.Stmts, p.parseStmt())
			continue
		case token.IF, token.MATCH:
			// An if without an else, or a match, that something follows is
			// a statement; the last thing in the braces is the value
			if stmt := p.tryStatementBeforeValue(); stmt != nil {
				block.Stmts = append(block.Stmts, stmt)
				continue
			}
		}
		at := p.cur()
		expr, assigned := p.parseExprOrAssignment()
		if !assigned && p.at(token.RBRACE) {
			// The last thing in the braces, with no ';': the value
			block.Value = expr
			break
		}
		p.expectSemicolon()
		block.Stmts = append(block.Stmts, &ast.ExprStmt{BaseNode: base(at), Expr: expr})
	}
	p.expect(token.RBRACE, "'}'")
	if block.Value != nil && len(block.Stmts) == 0 {
		return block.Value
	}
	return block
}

// tryStatementBeforeValue parses the if or match at the current token as
// a statement, if it is one: something other than the closing brace of
// the block follows it. Otherwise it leaves the position where it was and
// returns nil, and the if or match is read again as the block's value.
func (p *Parser) tryStatementBeforeValue() (stmt ast.Stmt) {
	start := p.pos
	wasSpeculating := p.speculating
	p.speculating = true
	defer func() {
		p.speculating = wasSpeculating
		if r := recover(); r != nil {
			if _, ok := r.(bailout); !ok {
				panic(r)
			}
			p.pos = start
			stmt = nil
		}
	}()

	stmt = p.parseStmt()
	// What an operator or a dot follows was a value going on: if a { 1 } else { 2 } + 3
	_, continues, _ := p.binaryOperator()
	goesOn := continues > 0 || p.at(token.DOT) || p.at(token.QUESTION_DOT) || p.at(token.QUESTION) || p.at(token.AS) || p.at(token.DOTDOT)
	if goesOn || p.at(token.RBRACE) || p.at(token.EOF) {
		p.pos = start
		return nil
	}
	return stmt
}

// parseMatchStmt parses a match statement. A `default:` case is stored with a
// nil pattern.
func (p *Parser) parseMatchStmt() *ast.MatchStmt {
	return p.parseMatch(false)
}

// parseMatch parses a match: the statement, or the one that gives a value
// (asValue), which only differ in what ends a case. In a statement a case
// that is an expression ends with ';'. Where a value is expected it may
// end with ',' as well, and the last one with nothing.
//
//	case 1: ...                          a value
//	case 1..10: ...                      a range of numbers
//	case Int: ...                        a type
//	case Shape.Rect(w, h): ...           a variant, its values named
//	case Light.Red | Light.Amber: ...    any of several patterns
//	case Shape.Circle(r) if r > 10.0:    a pattern and a condition
func (p *Parser) parseMatch(asValue bool) *ast.MatchStmt {
	tok := p.expect(token.MATCH, "'match'")
	stmt := &ast.MatchStmt{BaseNode: base(tok)}
	stmt.Cond = p.parseHeaderExpr()

	// Struct literals are fine again between the braces of the match
	saved := p.noStructLit
	p.noStructLit = false
	defer func() { p.noStructLit = saved }()

	p.expect(token.LBRACE, "'{'")
	for !p.at(token.RBRACE) {
		caseTok := p.cur()
		matchCase := &ast.MatchCase{BaseNode: base(caseTok)}

		if caseTok.Type == token.IDENT && caseTok.Literal == "default" && p.peekAt(1).Type == token.COLON {
			p.next()
		} else {
			p.expect(token.CASE, "'case' or 'default'")
			matchCase.Pattern = p.parseCasePattern()
			for p.accept(token.PIPE) {
				matchCase.Alternatives = append(matchCase.Alternatives, p.parseCasePattern())
			}
			if p.accept(token.IF) {
				matchCase.Guard = p.parseHeaderExpr()
			}
		}
		p.expect(token.COLON, "':'")

		switch {
		case p.at(token.LBRACE) && asValue:
			// As the branch of an if: statements, then the value, or a
			// block that leaves
			body := p.parseValueBlock()
			if block, ok := body.(*ast.BlockExpr); ok && block.Value == nil {
				matchCase.Body = &ast.BlockStmt{BaseNode: block.BaseNode, Stmts: block.Stmts}
			} else {
				matchCase.Body = body
			}
			p.accept(token.COMMA)
		case p.at(token.LBRACE):
			matchCase.Body = p.parseBlock()
		case asValue:
			matchCase.Body = p.parseExpr()
			if !p.accept(token.COMMA) && !p.accept(token.SEMICOLON) && !p.at(token.RBRACE) {
				p.fail("expected ';' after the value of a case, found %s", describe(p.cur()))
			}
		default:
			matchCase.Body = p.parseExpr()
			p.expectSemicolon()
		}
		stmt.Cases = append(stmt.Cases, matchCase)
	}
	p.expect(token.RBRACE, "'}'")
	return stmt
}

// parseCasePattern parses one pattern of a case.
func (p *Parser) parseCasePattern() ast.Node {
	if typ := p.tryTypePattern(); typ != nil {
		return typ
	}
	// A | here starts the next pattern: case 1 | 2:. A brace is a
	// struct pattern, case Point { x: 0, y }:, never a block
	savedOr, savedLit := p.noBitOr, p.noStructLit
	p.noBitOr, p.noStructLit = true, false
	expr := p.parseExpr()
	p.noBitOr, p.noStructLit = savedOr, savedLit
	return p.variantPattern(p.counted(expr), true)
}

// counted marks a range written where numbers are gone through or
// compared with, the header of a loop or a case: it is not a value there,
// and nothing is made for it.
func (p *Parser) counted(expr ast.Expr) ast.Expr {
	if r, ok := expr.(*ast.RangeExpr); ok && !r.Counted {
		r.Counted = true
		p.keptRanges--
	}
	return expr
}

// tryTypePattern parses the pattern of a match case as a type when it can
// only be one: `Int`, `[Int]`, `String?`, `fn(Int) -> Int`, `Box<Int>`.
// A bare name is left alone, since `case Point:` and `case limit:` look the
// same here; the compiler decides once it knows what the name refers to.
func (p *Parser) tryTypePattern() (typ *ast.Type) {
	start := p.pos
	wasSpeculating := p.speculating
	p.speculating = true
	defer func() {
		p.speculating = wasSpeculating
		if r := recover(); r != nil {
			if _, ok := r.(bailout); !ok {
				panic(r)
			}
			p.pos = start
			typ = nil
		}
	}()

	// (a, b) is a tuple of patterns, whose names are new variables, and
	// never the tuple type of types called a and b
	if p.at(token.LPAREN) {
		p.fail("not a type pattern")
	}

	// One type: `Int | String` is two patterns, each read on its own
	parsed := p.parsePostfixType()
	bareName := parsed.Kind == ast.TypeBasic && len(parsed.Generics) == 0 && !isBuiltinTypeToken(parsed.Tok.Type)
	if bareName || !(p.at(token.COLON) || p.at(token.PIPE) || p.at(token.IF)) {
		p.fail("not a type pattern")
	}
	return parsed
}

// parseExprStmt parses an expression statement or an assignment. Assignments
// are represented as a BinaryExpr with Op "=", or "+=" and the like for the
// compound forms. `i++` and `i--` are `i += 1` and `i -= 1`; they are
// statements, so there is no value of `i++` to define.
func (p *Parser) parseExprStmt() *ast.ExprStmt {
	tok := p.cur()
	expr, _ := p.parseExprOrAssignment()
	p.expectSemicolon()
	return &ast.ExprStmt{BaseNode: base(tok), Expr: expr}
}

// parseExprOrAssignment parses what an expression statement is made of:
// an expression, or an assignment to one (`x = 1`, `x += 1`, `x++`). It
// reports whether it was an assignment, which has no value.
func (p *Parser) parseExprOrAssignment() (ast.Expr, bool) {
	expr := p.parseExpr()

	opTok := p.cur()
	switch opTok.Type {
	case token.ASSIGN, token.PLUS_ASSIGN, token.MINUS_ASSIGN, token.STAR_ASSIGN, token.SLASH_ASSIGN, token.PERCENT_ASSIGN,
		token.AMPERSAND_ASSIGN, token.PIPE_ASSIGN, token.CARET_ASSIGN:
		p.checkAssignable(expr)
		p.next()
		return &ast.BinaryExpr{BaseNode: base(opTok), Left: expr, Op: opTok.Literal, Right: p.parseExpr()}, true

	case token.LT, token.GT:
		// a <<= n and a >>= n: the lexer reads < then <=
		next := p.peekAt(1)
		shifts := (opTok.Type == token.LT && next.Type == token.LTE) || (opTok.Type == token.GT && next.Type == token.GTE)
		if shifts && adjacent(opTok, next) {
			p.checkAssignable(expr)
			p.next()
			p.next()
			return &ast.BinaryExpr{BaseNode: base(opTok), Left: expr, Op: opTok.Literal + next.Literal, Right: p.parseExpr()}, true
		}

	case token.INC, token.DEC:
		p.checkAssignable(expr)
		p.next()
		one := &ast.Literal{BaseNode: base(opTok), Value: "1", Kind: "int"}
		return &ast.BinaryExpr{BaseNode: base(opTok), Left: expr, Op: opTok.Literal[:1] + "=", Right: one}, true
	}
	return expr, false
}

// checkAssignable fails unless expr can stand on the left of an assignment.
func (p *Parser) checkAssignable(expr ast.Expr) {
	switch expr.(type) {
	case *ast.Ident, *ast.IndexExpr, *ast.MemberExpr:
	default:
		p.failWith(ErrInvalidAssign, "Only variables, fields and array elements can be assigned to",
			"invalid assignment target")
	}
}

// --- Expressions ---

// parseHeaderExpr parses the expression in an if/while/for/match header,
// where `name {` starts the body rather than a struct literal.
func (p *Parser) parseHeaderExpr() ast.Expr {
	saved := p.noStructLit
	p.noStructLit = true
	expr := p.parseExpr()
	p.noStructLit = saved
	return expr
}

// parseNestedExpr parses an expression inside delimiters, where struct
// literals are unambiguous again.
func (p *Parser) parseNestedExpr() ast.Expr {
	saved, savedOr := p.noStructLit, p.noBitOr
	p.noStructLit, p.noBitOr = false, false
	expr := p.parseExpr()
	p.noStructLit, p.noBitOr = saved, savedOr
	return expr
}

// pipeInto is `value |> target`: a call of target with the value first.
//
//	xs |> arrays.map(f)     arrays.map(xs, f)
//	xs |> arrays.sum        arrays.sum(xs)
//	n |> (x -> x + 1)       (x -> x + 1)(n)
//	text |> parse()?        parse(text)?
//
// It binds looser than arithmetic and tighter than comparisons:
//
//	a + b |> f              f(a + b)
//	xs |> arrays.len == 3   arrays.len(xs) == 3
//	n |> f(1) + 2           f(n, 1) + 2
//
// It is the call it stands for from here on: no stage after this one
// knows the program wrote it another way.
func (p *Parser) pipeInto(value, target ast.Expr, pipe token.Token) ast.Expr {
	switch t := target.(type) {
	case *ast.CallExpr:
		t.Args = append([]ast.Expr{value}, t.Args...)
		return t
	case *ast.UnaryExpr:
		if t.Op == ast.TryOp || t.Op == ast.UnwrapOp {
			// The ? is asked of what the call returns, not of the function
			t.Expr = p.pipeInto(value, t.Expr, pipe)
			return t
		}
	case *ast.Ident, *ast.MemberExpr, *ast.FnExpr:
		// A function by its name, or written there: called with the value
		return &ast.CallExpr{BaseNode: base(target.Token()), Func: target, Args: []ast.Expr{value}}
	case nil:
		return value
	}
	if !p.speculating {
		p.errs.ReportErrorWithSuggestion(ErrUnexpectedToken,
			"what follows '|>' has to be a call or a function: the value before it becomes its first argument",
			"Write value |> f(other, arguments), or value |> f for a function of one argument", pipe, p.sourceLines)
	}
	return value
}

func (p *Parser) parseExpr() ast.Expr {
	open := p.openRange
	p.openRange = false
	left := p.parseBinary(1)

	// low..high and low..=high: looser than every operator, so that
	// 0..count - 1 is the range up to count - 1
	if tok := p.cur(); tok.Type == token.DOTDOT || tok.Type == token.DOTDOT_EQ {
		p.next()
		p.keptRanges++
		if open && tok.Type == token.DOTDOT && p.at(token.RBRACKET) {
			// items[1..]: up to the end
			return &ast.RangeExpr{BaseNode: base(tok), Low: left, High: p.rangeEnd(p.cur()), Inclusive: false}
		}
		if p.at(token.RBRACKET) || p.at(token.RPAREN) || p.at(token.SEMICOLON) || p.at(token.COMMA) {
			p.failWith(ErrUnexpectedToken, "Only a range between the brackets of an index may leave out its end: items[1..]",
				"a range needs its end here, found %s", describe(p.cur()))
		}
		return &ast.RangeExpr{BaseNode: base(tok), Low: left, High: p.parseBinary(1), Inclusive: tok.Type == token.DOTDOT_EQ}
	}

	// condition ? one : other, the short if that gives a value: looser
	// than every operator, and read from the right, so that
	// a ? b : c ? d : e is a ? b : (c ? d : e)
	if tok := p.cur(); tok.Type == token.QUESTION {
		p.next()
		expr := &ast.CondExpr{BaseNode: base(tok), Cond: left, Then: p.parseExpr()}
		if !p.at(token.COLON) {
			p.failWith(ErrUnexpectedToken, "Say what the value is otherwise: condition ? one : other",
				"expected ':' after the first value of '?', found %s", describe(p.cur()))
		}
		p.next()
		expr.Else = p.parseExpr()
		return expr
	}
	return left
}

// choiceAhead reports whether the '?' at the current token starts the
// two values of `condition ? one : other`, rather than leaving with the
// failure of what comes before it (`value?`): it does when an expression
// and a ':' follow it. Nothing is consumed.
func (p *Parser) choiceAhead() (found bool) {
	start, ranges, made := p.pos, p.keptRanges, p.made
	wasSpeculating := p.speculating
	p.speculating = true
	defer func() {
		p.speculating = wasSpeculating
		p.pos, p.keptRanges, p.made = start, ranges, made
		if r := recover(); r != nil {
			if _, ok := r.(bailout); !ok {
				panic(r)
			}
			found = false
		}
	}()

	p.next()
	switch next := p.cur(); next.Type {
	case token.SEMICOLON, token.COMMA, token.RPAREN, token.RBRACKET, token.RBRACE, token.COLON, token.EOF:
		return false
	case token.IDENT:
		// Nothing but `is` follows `value?`; parsing the choice says
		// what is missing from it
		return next.Literal != "is"
	case token.INT, token.FLOAT, token.STRING, token.TRUE, token.FALSE, token.NULL, token.NOT, token.FN, token.IF, token.MATCH:
		return true
	}
	// value? - 1, value?[0]: a value may follow, or the rest of an operation
	p.parseExpr()
	return p.at(token.COLON)
}

func binaryPrecedence(typ token.TokenType) int {
	switch typ {
	case token.OR, token.AND, token.QUESTION_QUESTION,
		token.EQ, token.NEQ, token.LT, token.LTE, token.GT, token.GTE,
		token.PLUS, token.MINUS, token.STAR, token.SLASH, token.PERCENT, token.PIPE_INTO,
		token.AMPERSAND, token.PIPE, token.CARET:
		return typ.Precedence()
	}
	return 0
}

// adjacent reports whether a token starts right where another ends: the
// two halves of << and >>, which the lexer leaves as two tokens.
func adjacent(first, second token.Token) bool {
	return first.EndPos.Line == second.Pos.Line && first.EndPos.Col == second.Pos.Col && first.Pos.Line == second.Pos.Line
}

// binaryOperator reads the binary operator at the current token without
// taking it: its text, its precedence (0 when there is none) and how many
// tokens it is made of.
func (p *Parser) binaryOperator() (op string, prec, width int) {
	tok := p.cur()
	if tok.Type == token.LT || tok.Type == token.GT {
		next := p.peekAt(1)
		if adjacent(tok, next) {
			if next.Type == tok.Type {
				return tok.Literal + tok.Literal, token.PrecedenceOfShift, 2
			}
			// <<= and >>=: an assignment, for whoever parses those
			if (tok.Type == token.LT && next.Type == token.LTE) || (tok.Type == token.GT && next.Type == token.GTE) {
				return "", 0, 0
			}
		}
	}
	if tok.Type == token.PIPE && p.noBitOr {
		// Between the patterns of a case: case 1 | 2:
		return "", 0, 0
	}
	return tok.Literal, binaryPrecedence(tok.Type), 1
}

func (p *Parser) parseBinary(minPrec int) ast.Expr {
	left := p.parseUnary()
	for {
		tok := p.cur()
		op, prec, width := p.binaryOperator()
		if prec == 0 || prec < minPrec {
			return left
		}
		for ; width > 0; width-- {
			p.next()
		}
		if tok.Type == token.PIPE_INTO {
			// What follows is the call the value goes into, and no more:
			// in value |> f(a) + 1 the 1 is added to what f returns
			left = p.pipeInto(left, p.parseUnary(), tok)
			continue
		}
		right := p.parseBinary(prec + 1)
		left = &ast.BinaryExpr{BaseNode: base(tok), Left: left, Op: op, Right: right}
	}
}

func (p *Parser) parseUnary() ast.Expr {
	tok := p.cur()
	if tok.Type == token.MINUS || tok.Type == token.NOT || tok.Type == token.TILDE {
		p.next()
		return &ast.UnaryExpr{BaseNode: base(tok), Op: tok.Literal, Expr: p.parseUnary()}
	}

	// `value as Type`, `value is Type` and `value is Pattern` bind tighter
	// than any binary operator. `is` is not a reserved word: after a
	// complete expression an identifier cannot follow, so there it can only
	// be this operator. `wait` binds tighter still: wait x is P tests what
	// came.
	var expr ast.Expr
	if p.atWait() {
		expr = p.parseWait()
	} else {
		expr = p.parsePostfix()
	}
	for {
		tok := p.cur()
		switch {
		case tok.Type == token.AS:
			p.next()
			expr = &ast.CastExpr{BaseNode: base(tok), Expr: expr, Type: p.parsePostfixType()}
		case tok.Type == token.IDENT && tok.Literal == "is":
			p.next()
			expr = p.parseIsTest(tok, expr)
		default:
			return expr
		}
	}
}

func (p *Parser) parsePostfix() ast.Expr {
	expr := p.parsePrimary()
	for {
		if next, ok := p.parsePostfixOp(expr); ok {
			expr = next
			continue
		}

		tok := p.cur()
		switch tok.Type {
		case token.QUESTION_DOT:
			// value?.member: what follows is done with the value, by a name
			// that stands for it, and only if it is not null. That goes for
			// all that follows: in user?.address.city the city is read
			// when there is a user, and nothing is when there is none.
			p.next()
			var access ast.Expr = &ast.Ident{BaseNode: base(expr.Token()), Name: p.makeName("safe")}
			if p.at(token.INT) || p.at(token.FLOAT) {
				access = p.parseTuplePositions(access) // pair?.0
			} else {
				nameTok := p.expectMemberName("member name")
				access = &ast.MemberExpr{BaseNode: base(nameTok), Expr: access, Name: nameTok.Literal, TypeArgs: p.tryTypeArgs()}
			}
			for {
				next, ok := p.parsePostfixOp(access)
				if !ok {
					break
				}
				access = next
			}
			expr = &ast.BinaryExpr{BaseNode: base(tok), Left: expr, Op: ast.SafeAccessOp, Right: access}
		case token.QUESTION:
			// value?: the value, or leave the function with the failure.
			// A '?' that a value and a ':' follow is a choice instead
			if p.choiceAhead() {
				return expr
			}
			p.next()
			expr = &ast.UnaryExpr{BaseNode: base(tok), Op: ast.TryOp, Expr: expr}
		case token.NOT:
			// value!: the value, or the program stops here. Nothing else
			// has a '!' after a value
			p.next()
			expr = &ast.UnaryExpr{BaseNode: base(tok), Op: ast.UnwrapOp, Expr: expr}
		default:
			return expr
		}
	}
}

// parseTuplePositions parses the number after a dot, which reads a value
// of a tuple by its position. Two positions in a row, pair.0.1, come from
// the lexer as one token: the number 0.1.
func (p *Parser) parseTuplePositions(expr ast.Expr) ast.Expr {
	numTok := p.next()
	for _, part := range strings.Split(numTok.Literal, ".") {
		if _, ok := ast.TupleIndex(part); !ok {
			p.pos--
			p.fail("expected the position of a value in a tuple, as in pair.0, found %s", describe(numTok))
		}
		expr = &ast.MemberExpr{BaseNode: base(numTok), Expr: expr, Name: part}
	}
	return expr
}

// parsePostfixOp parses one thing done to an expression, if one follows:
// a call, an index or a member. It reports whether there was one.
func (p *Parser) parsePostfixOp(expr ast.Expr) (ast.Expr, bool) {
	tok := p.cur()
	switch tok.Type {
	case token.LPAREN:
		p.next()
		// Anchor the call at its callee so diagnostics underline the
		// name rather than the parenthesis
		call := &ast.CallExpr{BaseNode: base(expr.Token()), Func: expr}
		call.Args = p.parseExprList(token.RPAREN)
		p.expect(token.RPAREN, "')'")
		return call, true
	case token.LBRACKET:
		p.next()
		index := p.parseIndex()
		p.expect(token.RBRACKET, "']'")
		return &ast.IndexExpr{BaseNode: base(tok), Expr: expr, Index: index}, true
	case token.DOT:
		p.next()
		// pair.0: a value of a tuple, by its position. Two in a row,
		// pair.0.1, reach the lexer looking like the number 0.1
		if p.at(token.INT) || p.at(token.FLOAT) {
			return p.parseTuplePositions(expr), true
		}
		nameTok := p.expectMemberName("member name")
		name := nameTok.Literal
		// Like a generic function name, a method name may carry type
		// arguments when a call follows: stack.map<String>(f)
		typeArgs := p.tryTypeArgs()

		// A struct of another module: module.Name { ... }
		if owner, ok := expr.(*ast.Ident); ok && len(owner.TypeArgs) == 0 && !strings.HasPrefix(owner.Name, "__") && p.at(token.LBRACE) && p.structLitFollows() {
			qualified := owner.Tok
			qualified.Literal = owner.Name + "." + name
			qualified.EndPos = nameTok.EndPos
			return p.parseStructInit(qualified, typeArgs), true
		}
		if p.members == nil {
			p.members = make(map[string]bool)
		}
		p.members[name] = true
		return &ast.MemberExpr{BaseNode: base(tok), Expr: expr, Name: name, TypeArgs: typeArgs}, true
	}
	return expr, false
}

// parseEntries parses the literal of a dictionary, the keys and values of
// its entries: {"ada": 36, "alan": 41}, {(3, 4): "seat"}, {}. It is the
// call of ast.EntriesFunction with an array of pairs, which the type
// checker makes a Dict or a Map.
func (p *Parser) parseEntries() ast.Expr {
	open := p.expect(token.LBRACE, "'{'")
	p.entries = true
	saved, savedOr := p.noStructLit, p.noBitOr
	p.noStructLit, p.noBitOr = false, false
	defer func() { p.noStructLit, p.noBitOr = saved, savedOr }()

	var pairs []ast.Expr
	for !p.at(token.RBRACE) {
		key := p.parseExpr()
		if !p.at(token.COLON) {
			p.failWith(ErrUnexpectedToken, "Write each entry as key: value, as in {\"ada\": 36}",
				"expected ':' after the key of an entry, found %s", describe(p.cur()))
		}
		p.next()
		value := p.parseExpr()
		pairs = append(pairs, p.tuple(key.Token(), []ast.Expr{key, value}))
		if !p.accept(token.COMMA) {
			break
		}
	}
	p.expect(token.RBRACE, "'}'")
	array := &ast.ArrayLiteralExpr{BaseNode: base(open), Elements: pairs}
	return &ast.CallExpr{BaseNode: base(open), Func: &ast.Ident{BaseNode: base(open), Name: ast.EntriesFunction}, Args: []ast.Expr{array}}
}

// parseIndex parses what is between the brackets of an index: a value,
// or a range, which may leave out where it starts or where it ends.
//
//	items[2]        an item
//	items[1..3]     the items at 1 and 2
//	items[1..]      from 1 to the end
//	items[..2]      the first two
func (p *Parser) parseIndex() ast.Expr {
	if tok := p.cur(); tok.Type == token.DOTDOT || tok.Type == token.DOTDOT_EQ {
		p.next()
		p.keptRanges++
		low := &ast.Literal{BaseNode: base(tok), Kind: "int", Value: "0"}
		low.Tok.Type, low.Tok.Literal = token.INT, "0"
		saved, savedOr := p.noStructLit, p.noBitOr
		p.noStructLit, p.noBitOr = false, false
		high := p.parseBinary(1)
		p.noStructLit, p.noBitOr = saved, savedOr
		return &ast.RangeExpr{BaseNode: base(tok), Low: low, High: high, Inclusive: tok.Type == token.DOTDOT_EQ}
	}
	p.openRange = true
	return p.parseNestedExpr()
}

// rangeEnd is the end of a range that leaves it out, items[1..]: the
// largest Int, past the end of any array or text.
func (p *Parser) rangeEnd(at token.Token) ast.Expr {
	end := &ast.Literal{BaseNode: base(at), Kind: "int", Value: RangeEnd}
	end.Tok.Type, end.Tok.Literal = token.INT, RangeEnd
	return end
}

// RangeEnd is the end given to a range written without one, items[1..]:
// the largest Int there is.
const RangeEnd = "140737488355327"

func (p *Parser) parseExprList(end token.TokenType) []ast.Expr {
	var list []ast.Expr
	for !p.at(end) {
		list = append(list, p.parseNestedExpr())
		if !p.accept(token.COMMA) {
			break
		}
	}
	return list
}

func (p *Parser) parsePrimary() ast.Expr {
	tok := p.cur()

	switch tok.Type {
	case token.INT:
		p.next()
		return &ast.Literal{BaseNode: base(tok), Value: tok.Literal, Kind: "int"}
	case token.FLOAT:
		p.next()
		return &ast.Literal{BaseNode: base(tok), Value: tok.Literal, Kind: "float"}
	case token.STRING:
		p.next()
		return &ast.Literal{BaseNode: base(tok), Value: tok.Literal, Kind: "string"}
	case token.TRUE, token.FALSE:
		p.next()
		return &ast.Literal{BaseNode: base(tok), Value: tok.Literal, Kind: "bool"}
	case token.NULL:
		p.next()
		return &ast.Literal{BaseNode: base(tok), Value: tok.Literal, Kind: "null"}

	case token.MATCH:
		// A match where a value is expected gives the value of its case
		return &ast.MatchExpr{BaseNode: base(tok), Match: p.parseMatch(true)}

	case token.LBRACE:
		if !p.noStructLit {
			return p.parseEntries()
		}

	case token.INT_TYPE, token.FLOAT_TYPE, token.STRING_TYPE, token.BOOL_TYPE:
		// String.upper: a method of a builtin type, as a function
		if p.peekAt(1).Type == token.DOT {
			p.next()
			return &ast.Ident{BaseNode: base(tok), Name: tok.Literal}
		}

	case token.IF:
		// An if where a value is expected gives the value of its branch
		return p.parseIfExpr()

	case token.IDENT:
		if isShortFnArrow(p.peekAt(1)) {
			// item -> item.name
			p.next()
			return p.parseShortFn(tok, []ast.Expr{&ast.Ident{BaseNode: base(tok), Name: tok.Literal}})
		}
		return p.parseIdentExpr()

	case token.LPAREN:
		p.next()
		if p.at(token.RPAREN) && isShortFnArrow(p.peekAt(1)) {
			// () -> now()
			p.next()
			return p.parseShortFn(tok, nil)
		}
		expr := p.parseNestedExpr()
		elems := []ast.Expr{expr}
		for p.accept(token.COMMA) && !p.at(token.RPAREN) {
			elems = append(elems, p.parseNestedExpr())
		}
		p.expect(token.RPAREN, "')'")
		if isShortFnArrow(p.cur()) {
			// (a, b) -> a + b: what was read is its parameters
			return p.parseShortFn(tok, elems)
		}
		if len(elems) == 1 {
			return expr
		}
		// (1, "a"): a tuple
		return p.tuple(tok, elems)

	case token.LBRACKET:
		p.next()
		arr := &ast.ArrayLiteralExpr{BaseNode: base(tok)}
		arr.Elements = p.parseExprList(token.RBRACKET)
		p.expect(token.RBRACKET, "']'")
		return arr

	case token.FN:
		p.next()
		fn := &ast.FnExpr{BaseNode: base(tok)}
		fn.Params = p.parseParams()
		if p.accept(token.ARROW) {
			fn.ReturnType = p.parseType()
		}
		fn.Body = p.parseBlock()
		return fn
	}

	p.fail("expected expression, found %s", describe(tok))
	return nil
}

// isShortFnArrow reports whether a token is the arrow of a function
// written without types, or the `=>` other languages write there, which
// is reported with what to write instead.
func isShortFnArrow(tok token.Token) bool {
	return tok.Type == token.ARROW || tok.Type == token.FAT_ARROW
}

// parseShortFn parses a function written without types, from its arrow:
//
//	item -> item.name
//	(a, b) -> a + b
//	() -> now()
//	line -> { count++; return strings.trim(line); }
//
// The arrow is the one of `fn(item: Person) -> String`: what is on its
// left gives what is on its right. names are the parameters as they were
// read, as expressions. Their types and that of the result are those of
// the function type expected where the function stands, which the type
// checker fills in.
func (p *Parser) parseShortFn(start token.Token, names []ast.Expr) ast.Expr {
	fn := &ast.FnExpr{BaseNode: base(start), Short: true}
	for _, name := range names {
		ident, ok := name.(*ast.Ident)
		if !ok || len(ident.TypeArgs) > 0 {
			if !p.speculating {
				p.errs.ReportErrorWithSuggestion(ErrUnexpectedToken,
					"the parameters of a function written with '->' are names",
					"Write them as (first, second) -> ..., or use fn(first: Type) -> Type { ... } to give their types",
					name.Token(), p.sourceLines)
			}
			panic(bailout{})
		}
		fn.Params = append(fn.Params, &ast.Param{BaseNode: ident.BaseNode, Name: ident.Name})
	}

	if p.at(token.FAT_ARROW) {
		p.failWith(ErrUnexpectedToken, "Write '->', as in: item -> item.name", "a function is written with '->', not '=>'")
	}
	arrow := p.expect(token.ARROW, "'->'")
	if p.at(token.LBRACE) {
		fn.Body = p.parseBlock()
		return fn
	}
	// One expression: what the function gives. Or one assignment, which
	// gives nothing: () -> count++
	saved := p.noStructLit
	p.noStructLit = false
	value, isAssignment := p.parseExprOrAssignment()
	p.noStructLit = saved
	if isAssignment {
		fn.Body = &ast.BlockStmt{BaseNode: base(arrow), Stmts: []ast.Stmt{&ast.ExprStmt{BaseNode: base(arrow), Expr: value}}}
		return fn
	}
	fn.ExprBody = true
	fn.Body = &ast.BlockStmt{BaseNode: base(arrow), Stmts: []ast.Stmt{&ast.ReturnStmt{BaseNode: base(arrow), Value: value}}}
	return fn
}

// parseIdentExpr parses an identifier, optionally with type arguments, and
// the struct literal that may follow it.
func (p *Parser) parseIdentExpr() ast.Expr {
	tok := p.next()
	typeArgs := p.tryTypeArgs()

	if p.at(token.LBRACE) && p.structLitFollows() {
		return p.parseStructInit(tok, typeArgs)
	}
	return &ast.Ident{BaseNode: base(tok), Name: tok.Literal, TypeArgs: typeArgs}
}

// tryTypeArgs speculatively parses `<T, U>` after an identifier. It only
// commits when the closing '>' is followed by '(' or '{', which separates
// generic instantiation from the comparison `a < b`.
func (p *Parser) tryTypeArgs() (args []*ast.Type) {
	if !p.at(token.LT) {
		return nil
	}

	start := p.pos
	wasSpeculating := p.speculating
	p.speculating = true
	defer func() {
		p.speculating = wasSpeculating
		if r := recover(); r != nil {
			if _, ok := r.(bailout); !ok {
				panic(r)
			}
			p.pos = start
			args = nil
		}
	}()

	p.next()
	list := p.parseTypeList(token.GT)
	p.expect(token.GT, "'>'")
	// `(` and `{` follow a generic call and a generic struct literal, `.`
	// the name of a generic enum: Option<Int>.None
	if len(list) == 0 || (!p.at(token.LPAREN) && !p.at(token.LBRACE) && !p.at(token.DOT)) {
		p.fail("not type arguments")
	}
	return list
}

// structLitFollows reports whether the '{' at the cursor opens a struct
// literal: `{}` or `{ name: ...`.
func (p *Parser) structLitFollows() bool {
	if p.noStructLit {
		return false
	}
	if p.peekAt(1).Type == token.RBRACE {
		return true
	}
	// Point { x: 1 }, or Point { x, y } for Point { x: x, y: y }
	next := p.peekAt(2).Type
	return p.peekAt(1).Type == token.IDENT && (next == token.COLON || next == token.COMMA || next == token.RBRACE)
}

func (p *Parser) parseStructInit(nameTok token.Token, typeArgs []*ast.Type) *ast.StructInitExpr {
	init := &ast.StructInitExpr{
		BaseNode: base(nameTok),
		Name:     nameTok.Literal,
		TypeArgs: typeArgs,
		Fields:   make(map[string]ast.Expr),
	}

	p.expect(token.LBRACE, "'{'")
	for !p.at(token.RBRACE) {
		fieldTok := p.expect(token.IDENT, "field name")
		if _, dup := init.Fields[fieldTok.Literal]; dup {
			p.pos--
			p.failWith(ErrDuplicateField, "Remove one of the two initializers",
				"duplicate field '%s' in struct literal", fieldTok.Literal)
		}
		if p.accept(token.COLON) {
			init.Fields[fieldTok.Literal] = p.parseNestedExpr()
		} else {
			// Point { x }: the field gets the value of the name
			init.Fields[fieldTok.Literal] = &ast.Ident{BaseNode: base(fieldTok), Name: fieldTok.Literal}
			init.Shorthand = append(init.Shorthand, fieldTok.Literal)
		}
		if !p.accept(token.COMMA) {
			break
		}
	}
	p.expect(token.RBRACE, "'}'")
	return init
}
