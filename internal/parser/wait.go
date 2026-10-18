package parser

import (
	"strconv"

	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/token"
)

// `wait` is not a reserved word. It is the operator where a name could not
// be: before what starts a value, `wait payment`, `wait self.jobs.receive()`,
// `wait match {`. Before a parenthesis or a bracket it stays a name, so a
// function or an array called wait is used as before: wait(x), wait[0].
//
// What it waits with is std.task. The parser writes `wait value` as the
// call __std_task.waitFor(value), and a `wait match` as the calls that
// wait for the first of its cases (see waitMatch), the way a literal of a
// dictionary is written as the call that makes it.

// atWait reports whether the cursor is at the operator wait.
func (p *Parser) atWait() bool {
	tok := p.cur()
	if tok.Type != token.IDENT || tok.Literal != "wait" {
		return false
	}
	switch p.peekAt(1).Type {
	case token.IDENT, token.MATCH, token.INT, token.FLOAT, token.STRING, token.TRUE, token.FALSE, token.NULL:
		return true
	}
	return false
}

// parseWait parses `wait value`, where value is what a postfix expression
// is: a name, a call, a member, an index; or a `wait match` that gives a
// value.
func (p *Parser) parseWait() ast.Expr {
	tok := p.next()
	p.waits = true
	if p.at(token.MATCH) {
		return p.parseWaitMatch(tok, true).(ast.Expr)
	}
	return p.taskCall(tok, "waitFor", p.parsePostfix())
}

// parseWaitMatchStmt parses a `wait match` where a statement is.
func (p *Parser) parseWaitMatchStmt() ast.Stmt {
	tok := p.next()
	p.waits = true
	return p.parseWaitMatch(tok, false).(ast.Stmt)
}

// waitCase is one case of a wait match, as written.
type waitCase struct {
	tok      token.Token
	bind     *ast.Ident // case let d = w:
	waitable ast.Expr
	pattern  ast.Node // case w is P:, nil for the other two forms
	alts     []ast.Node
	body     ast.Node
}

// parseWaitMatch parses the cases of a wait match, after `wait`:
//
//	case let d = waitable:      takes what it gives, and names it
//	case waitable is Pattern:   takes what fits the pattern
//	case waitable:              takes what it gives
//
// and writes what does it. asValue says that the match gives a value,
// which its cases then end with, as those of a match do.
func (p *Parser) parseWaitMatch(wait token.Token, asValue bool) ast.Node {
	p.expect(token.MATCH, "'match'")
	saved := p.noStructLit
	p.noStructLit = false
	defer func() { p.noStructLit = saved }()

	p.expect(token.LBRACE, "'{'")
	var cases []*waitCase
	for !p.at(token.RBRACE) && !p.at(token.EOF) {
		c := &waitCase{tok: p.expect(token.CASE, "'case'")}
		if p.accept(token.LET) {
			name := p.expect(token.IDENT, "a name")
			c.bind = &ast.Ident{BaseNode: base(name), Name: name.Literal}
			p.expect(token.ASSIGN, "'='")
			c.waitable = p.parseExpr()
		} else {
			c.waitable = p.parsePostfix()
			if p.cur().Type == token.IDENT && p.cur().Literal == "is" {
				p.next()
				c.pattern = p.parseCasePattern()
				for p.accept(token.PIPE) {
					c.alts = append(c.alts, p.parseCasePattern())
				}
			}
		}
		p.expect(token.COLON, "':'")
		c.body = p.parseCaseBody(asValue)
		cases = append(cases, c)
	}
	p.expect(token.RBRACE, "'}'")
	if len(cases) == 0 {
		p.failWith(ErrUnexpectedToken, "Give it a case: case let value = channel.receive(): { ... }",
			"a wait match with no case would wait forever")
	}
	return p.waitMatch(wait, cases, asValue)
}

// parseCaseBody parses what a case does, as parseMatch does.
func (p *Parser) parseCaseBody(asValue bool) ast.Node {
	switch {
	case p.atLeaving():
		return p.parseLeaving()
	case p.at(token.LBRACE) && asValue:
		body := p.parseValueBlock()
		p.accept(token.COMMA)
		if block, ok := body.(*ast.BlockExpr); ok && block.Value == nil {
			return &ast.BlockStmt{BaseNode: block.BaseNode, Stmts: block.Stmts}
		}
		return body
	case p.at(token.LBRACE):
		return p.parseBlock()
	case asValue:
		body := p.parseExpr()
		if !p.accept(token.COMMA) && !p.accept(token.SEMICOLON) && !p.at(token.RBRACE) {
			p.fail("expected ';' after the value of a case, found %s", describe(p.cur()))
		}
		return body
	default:
		tok := p.cur()
		return &ast.BlockStmt{BaseNode: base(tok), Stmts: []ast.Stmt{p.parseStmt()}}
	}
}

// waitMatch writes a wait match as what it does. For
//
//	wait match {
//	    case let d = decisions.receive(): { return d; }
//	    case jobs.receive() is Stop: { stop(); }
//	    case time.after(1000.milliseconds()): { tick(); }
//	}
//
// it writes, the names being ones no program writes:
//
//	{
//	    let w0 = decisions.receive();
//	    let w1 = jobs.receive();
//	    let w2 = time.after(1000.milliseconds());
//	    if false { match __std_task.sample(w1) { case Stop: {} } }    the cases of a channel, checked
//	    match __std_task.first([
//	        __std_task.arm(w0),
//	        __std_task.armIf(w1, v -> match v { case Stop: true, default: false }),
//	        __std_task.arm(w2),
//	    ]) {
//	        case 0: { let d = w0.take(); return d; }
//	        case 1: { let v = w1.take(); match v { case Stop: { stop(); } default: {} } }
//	        case 2: { w2.take(); tick(); }
//	        default: {}
//	    }
//	}
//
// Where a value is expected the same is an if whose condition always
// holds, so that the statements have somewhere to be.
func (p *Parser) waitMatch(wait token.Token, cases []*waitCase, asValue bool) ast.Node {
	at := base(wait)
	var stmts []ast.Stmt
	names := make([]string, len(cases))
	for i, c := range cases {
		names[i] = p.makeName("wait")
		stmts = append(stmts, &ast.LetStmt{BaseNode: base(c.tok), Name: names[i], Value: c.waitable})
	}
	stmts = append(stmts, p.waitCasesChecked(cases, names)...)

	var arms []ast.Expr
	for i, c := range cases {
		w := &ast.Ident{BaseNode: base(c.tok), Name: names[i]}
		if c.pattern == nil {
			arms = append(arms, p.taskCall(c.tok, "arm", w))
			continue
		}
		param := p.makeName("waited")
		test := p.matches(&waitCase{tok: c.tok, pattern: unnamed(c.pattern), alts: unnamedAll(c.alts)}, &ast.Ident{BaseNode: base(c.tok), Name: param},
			&ast.Literal{BaseNode: base(c.tok), Value: "true", Kind: "bool"},
			&ast.Literal{BaseNode: base(c.tok), Value: "false", Kind: "bool"}, true)
		accepts := &ast.FnExpr{
			BaseNode: base(c.tok), Short: true, ExprBody: true,
			Params: []*ast.Param{{BaseNode: base(c.tok), Name: param}},
			Body:   &ast.BlockStmt{BaseNode: base(c.tok), Stmts: []ast.Stmt{&ast.ReturnStmt{BaseNode: base(c.tok), Value: test.(ast.Expr)}}},
		}
		arms = append(arms, p.taskCall(c.tok, "armIf", w, accepts))
	}
	which := &ast.MatchStmt{BaseNode: at, Cond: p.taskCall(wait, "first", &ast.ArrayLiteralExpr{BaseNode: at, Elements: arms})}

	for i, c := range cases {
		w := &ast.Ident{BaseNode: base(c.tok), Name: names[i]}
		take := &ast.CallExpr{BaseNode: base(c.tok), Func: &ast.MemberExpr{BaseNode: base(c.tok), Expr: w, Name: "take"}}
		var body ast.Node
		switch {
		case c.bind != nil:
			body = prepend(&ast.LetStmt{BaseNode: base(c.tok), Name: c.bind.Name, Value: take, NameTok: c.bind.Tok}, c.body)
		case c.pattern != nil:
			got := p.makeName("got")
			taken := &ast.LetStmt{BaseNode: base(c.tok), Name: got, Value: take}
			var otherwise ast.Node = &ast.BlockStmt{BaseNode: base(c.tok)}
			if asValue {
				otherwise = unreachable(c.tok)
			}
			inner := p.matches(c, &ast.Ident{BaseNode: base(c.tok), Name: got}, c.body, otherwise, asValue)
			if asValue {
				body = &ast.BlockExpr{BaseNode: base(c.tok), Stmts: []ast.Stmt{taken}, Value: inner.(ast.Expr)}
			} else {
				body = &ast.BlockStmt{BaseNode: base(c.tok), Stmts: []ast.Stmt{taken, inner.(ast.Stmt)}}
			}
		default:
			body = prepend(&ast.ExprStmt{BaseNode: base(c.tok), Expr: take}, c.body)
		}
		which.Cases = append(which.Cases, &ast.MatchCase{
			BaseNode: base(c.tok),
			Pattern:  &ast.Literal{BaseNode: base(c.tok), Value: strconv.Itoa(i), Kind: "int"},
			Body:     body,
		})
	}

	if !asValue {
		which.Cases = append(which.Cases, &ast.MatchCase{BaseNode: at, Body: &ast.BlockStmt{BaseNode: at}})
		return &ast.BlockStmt{BaseNode: at, Stmts: append(stmts, which)}
	}
	which.Cases = append(which.Cases, &ast.MatchCase{BaseNode: at, Body: unreachable(wait)})
	return &ast.CondExpr{
		BaseNode: at,
		Cond:     &ast.Literal{BaseNode: at, Value: "true", Kind: "bool"},
		Then:     &ast.BlockExpr{BaseNode: at, Stmts: stmts, Value: &ast.MatchExpr{BaseNode: at, Match: which}},
		Else:     &ast.BlockExpr{BaseNode: at, Stmts: unreachable(wait).Stmts},
	}
}

// matches writes `match value { case <the case's patterns>: yes, default:
// no }`, as a value or as a statement.
func (p *Parser) matches(c *waitCase, value ast.Expr, yes, no ast.Node, asValue bool) ast.Node {
	m := &ast.MatchStmt{BaseNode: base(c.tok), Cond: value}
	m.Cases = append(m.Cases,
		&ast.MatchCase{BaseNode: base(c.tok), Pattern: c.pattern, Alternatives: c.alts, Body: yes},
		&ast.MatchCase{BaseNode: base(c.tok), Body: no})
	if asValue {
		return &ast.MatchExpr{BaseNode: base(c.tok), Match: m}
	}
	return m
}

// waitCasesChecked writes, for each waitable whose cases take values by
// their patterns, a match of those patterns that never runs: the compiler
// then says, as of any match, when they leave a value out. One value no
// case takes would stay where it is, and the match would wait for good.
// Cases on one waitable are those that write it alike.
func (p *Parser) waitCasesChecked(cases []*waitCase, names []string) []ast.Stmt {
	var stmts []ast.Stmt
	seen := map[string]bool{}
	for i, c := range cases {
		key := c.waitable.String()
		if c.pattern == nil || seen[key] {
			continue
		}
		seen[key] = true
		check := &ast.MatchStmt{BaseNode: base(c.tok), WaitsOn: true, Cond: p.taskCall(c.tok, "sample", &ast.Ident{BaseNode: base(c.tok), Name: names[i]})}
		takesAll := false
		for _, other := range cases {
			if other.waitable.String() != key {
				continue
			}
			if other.pattern == nil {
				takesAll = true
				break
			}
			check.Cases = append(check.Cases, &ast.MatchCase{BaseNode: base(other.tok), Pattern: unnamed(other.pattern), Alternatives: unnamedAll(other.alts), Body: &ast.BlockStmt{BaseNode: base(other.tok)}})
		}
		if takesAll {
			continue
		}
		stmts = append(stmts, &ast.IfStmt{
			BaseNode: base(c.tok),
			Cond:     &ast.Literal{BaseNode: base(c.tok), Value: "false", Kind: "bool"},
			Then:     &ast.BlockStmt{BaseNode: base(c.tok), Stmts: []ast.Stmt{check}},
		})
	}
	return stmts
}

// prepend puts a statement before the body of a case: a block, a block
// that gives a value, or a value alone.
func prepend(stmt ast.Stmt, body ast.Node) ast.Node {
	switch b := body.(type) {
	case *ast.BlockStmt:
		return &ast.BlockStmt{BaseNode: b.BaseNode, Stmts: append([]ast.Stmt{stmt}, b.Stmts...)}
	case *ast.BlockExpr:
		return &ast.BlockExpr{BaseNode: b.BaseNode, Stmts: append([]ast.Stmt{stmt}, b.Stmts...), Value: b.Value}
	case ast.Expr:
		return &ast.BlockExpr{BaseNode: base(b.Token()), Stmts: []ast.Stmt{stmt}, Value: b}
	}
	return body
}

// unreachable is a branch that cannot be taken, for where a value is
// expected and a branch has to be written.
func unreachable(tok token.Token) *ast.BlockStmt {
	call := &ast.CallExpr{BaseNode: base(tok), Func: &ast.Ident{BaseNode: base(tok), Name: "panic"},
		Args: []ast.Expr{&ast.Literal{BaseNode: base(tok), Value: "a wait match went on with no case", Kind: "string"}}}
	return &ast.BlockStmt{BaseNode: base(tok), Stmts: []ast.Stmt{&ast.ExprStmt{BaseNode: base(tok), Expr: call}}}
}

// taskCall is the call of a function of std.task, by the name the file
// has it under.
func (p *Parser) taskCall(tok token.Token, name string, args ...ast.Expr) *ast.CallExpr {
	module := &ast.Ident{BaseNode: base(tok), Name: ast.TaskModule}
	return &ast.CallExpr{BaseNode: base(tok), Func: &ast.MemberExpr{BaseNode: base(tok), Expr: module, Name: name}, Args: args}
}

// unnamed is a pattern as written, its names `_`: the test of a case,
// and the check of its channel, take what the case does and name nothing.
func unnamed(pattern ast.Node) ast.Node {
	switch p := pattern.(type) {
	case *ast.BindingPattern:
		return &ast.BindingPattern{BaseNode: p.BaseNode, Name: "_"}
	case *ast.VariantPattern:
		copied := &ast.VariantPattern{BaseNode: p.BaseNode, Target: p.Target, HasArgs: p.HasArgs}
		for _, arg := range p.Args {
			copied.Args = append(copied.Args, unnamed(arg))
		}
		return copied
	case *ast.StructPattern:
		copied := &ast.StructPattern{BaseNode: p.BaseNode, Type: p.Type}
		for _, field := range p.Fields {
			inner := field.Pattern
			if inner == nil {
				inner = &ast.BindingPattern{BaseNode: p.BaseNode, Name: "_"}
			} else {
				inner = unnamed(inner)
			}
			copied.Fields = append(copied.Fields, &ast.FieldPattern{Name: field.Name, Pattern: inner})
		}
		return copied
	}
	return pattern
}

func unnamedAll(patterns []ast.Node) []ast.Node {
	var out []ast.Node
	for _, pattern := range patterns {
		out = append(out, unnamed(pattern))
	}
	return out
}
