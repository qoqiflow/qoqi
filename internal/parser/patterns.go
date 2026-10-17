package parser

import (
	"sort"
	"strings"

	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/token"
)

// classifyPatterns turns a match pattern that is the bare name of a struct,
// an enum or a trait into a type pattern. While parsing, `case Point:` cannot be told
// from a name used as a value; once the whole file is parsed the
// declarations are known.
func classifyPatterns(prog *ast.Program) {
	typeNames := make(map[string]bool)
	for _, decl := range prog.Decls {
		switch d := decl.(type) {
		case *ast.StructDecl:
			typeNames[d.Name] = true
		case *ast.EnumDecl:
			typeNames[d.Name] = true
		case *ast.TraitDecl:
			typeNames[d.Name] = true
		}
	}

	classify := func(pattern ast.Node) ast.Node {
		if ident, ok := pattern.(*ast.Ident); ok && typeNames[ident.Name] {
			return &ast.Type{
				BaseNode: ident.BaseNode,
				Kind:     ast.TypeBasic,
				Name:     ident.Name,
				Generics: ident.TypeArgs,
			}
		}
		return pattern
	}
	visitBlock := func(block *ast.BlockStmt) {
		if block == nil {
			return
		}
		ast.Walk(block, func(ast.Expr) {}, func(stmt ast.Stmt) {
			match, ok := stmt.(*ast.MatchStmt)
			if !ok {
				return
			}
			for _, c := range match.Cases {
				c.Pattern = classify(c.Pattern)
				for i, alt := range c.Alternatives {
					c.Alternatives[i] = classify(alt)
				}
			}
		})
	}

	for _, decl := range prog.Decls {
		switch d := decl.(type) {
		case *ast.FnDecl:
			visitBlock(d.Body)
		case *ast.ImplDecl:
			for _, m := range d.Methods {
				visitBlock(m.Body)
			}
		}
	}
}

// variantPattern turns the expression parsed for a match case into a
// pattern on an enum variant, when it has the shape of one:
//
//	Shape.Empty            a name with a dot
//	Shape.Rect(w, h)       the same, with a pattern for each value
//
// As values these expressions cannot be match patterns anyway (only
// literals can), so nothing is lost by reading them this way. Whether the
// name really is a variant is for the later stages to say. Anything else
// is returned unchanged.
//
// top is false for a pattern inside another, where a bare name is a
// variable to bind rather than something to compare with.
func (p *Parser) variantPattern(expr ast.Expr, top bool) ast.Node {
	switch e := expr.(type) {
	case *ast.MemberExpr:
		if isPath(e) {
			return &ast.VariantPattern{BaseNode: pathBase(e), Target: e}
		}
	case *ast.CallExpr:
		var pattern *ast.VariantPattern
		switch target := e.Func.(type) {
		case *ast.MemberExpr:
			if isPath(target) {
				pattern = &ast.VariantPattern{BaseNode: pathBase(target), Target: target, HasArgs: true}
			}
		case *ast.Ident:
			// Circle(r): the variant alone, its enum found by its name
			if len(target.TypeArgs) == 0 {
				pattern = &ast.VariantPattern{BaseNode: target.BaseNode, Target: target, HasArgs: true}
			}
		}
		if pattern == nil {
			break
		}
		for _, arg := range e.Args {
			pattern.Args = append(pattern.Args, p.subPattern(arg))
		}
		return pattern
	case *ast.Ident:
		if !top && len(e.TypeArgs) == 0 {
			return &ast.BindingPattern{BaseNode: e.BaseNode, Name: e.Name}
		}
	case *ast.StructInitExpr:
		// Point { x: 0, y }: the fields compared or named
		if e.Range != nil {
			break
		}
		typ := &ast.Type{BaseNode: e.BaseNode, Kind: ast.TypeBasic, Name: e.Name, Generics: e.TypeArgs}
		pattern := &ast.StructPattern{BaseNode: e.BaseNode, Type: typ}
		names := make([]string, 0, len(e.Fields))
		for name := range e.Fields {
			names = append(names, name)
		}
		// In the order written: by where each value starts
		sort.Slice(names, func(i, j int) bool {
			a, b := e.Fields[names[i]].Position(), e.Fields[names[j]].Position()
			return a.Line < b.Line || (a.Line == b.Line && a.Col < b.Col)
		})
		for _, name := range names {
			pattern.Fields = append(pattern.Fields, &ast.FieldPattern{Name: name, Pattern: p.subPattern(e.Fields[name])})
		}
		return pattern
	}
	return expr
}

// subPattern reads an argument of a variant pattern: a name to bind, a
// literal to compare with, or a pattern on another enum value.
func (p *Parser) subPattern(expr ast.Expr) ast.Node {
	switch e := expr.(type) {
	case *ast.Literal:
		return e
	case *ast.UnaryExpr:
		if lit, ok := e.Expr.(*ast.Literal); ok && e.Op == "-" && (lit.Kind == "int" || lit.Kind == "float") {
			return e
		}
	case *ast.Ident, *ast.MemberExpr, *ast.CallExpr, *ast.StructInitExpr:
		if pattern := p.variantPattern(expr, false); pattern != ast.Node(expr) {
			return pattern
		}
	}

	if p.speculating {
		panic(bailout{})
	}
	p.errs.ReportErrorWithSuggestion(ErrUnexpectedToken,
		"this cannot be matched against a value of a variant",
		"Use a name to give the value a name, '_' to ignore it, a literal, or the pattern of another variant",
		expr.Token(), p.sourceLines)
	return &ast.BindingPattern{BaseNode: ast.BaseNode{Pos: expr.Position(), Tok: expr.Token()}, Name: "_"}
}

// isPath reports whether an expression is names joined by dots.
func isPath(e *ast.MemberExpr) bool {
	if len(e.TypeArgs) > 0 {
		return false
	}
	switch inner := e.Expr.(type) {
	case *ast.Ident:
		return true
	case *ast.MemberExpr:
		return isPath(inner)
	}
	return false
}

// pathBase positions a pattern at the start of its path, with a token that
// spans all of it.
func pathBase(e *ast.MemberExpr) ast.BaseNode {
	first := ast.Expr(e)
	text := ""
	for {
		member, ok := first.(*ast.MemberExpr)
		if !ok {
			break
		}
		text = "." + member.Name + text
		first = member.Expr
	}
	tok := first.Token()
	tok.Literal += text
	tok.EndPos = e.Tok.EndPos
	tok.EndPos.Col += len(e.Name)
	return ast.BaseNode{Pos: tok.Pos, Tok: tok}
}

// parseIsTest parses what follows `value is`: a type, or the pattern of a
// variant of an enum, which is whatever a `case` takes for one.
//
//	value is Int                    a type
//	value is geo.Shape              a type of another module
//	shape is Shape.Empty            a variant, whatever it holds
//	shape is Shape.Rect(w, 0.0)     its values named or compared
//	pair is (true, count)           a tuple
//
// A name with a dot reads the same for a type of a module and for a
// variant; it is kept as a pattern, and the module loader, which knows the
// modules, makes it the type test when it is one.
//
// The pattern ends where its name or its parentheses end, so in
// `if shape is Shape.Empty { ... }` the brace opens the block: it is never
// read as a struct literal.
func (p *Parser) parseIsTest(is token.Token, value ast.Expr) ast.Expr {
	tok := p.cur()
	switch {
	case tok.Type == token.LPAREN:
		// (a, b) names the values of a tuple; (Int, String) can only be
		// the type of one
		if pattern := p.tryTuplePattern(); pattern != nil {
			return &ast.PatternTestExpr{BaseNode: base(is), Expr: value, Pattern: pattern}
		}

	case tok.Type == token.IDENT:
		if p.typeFollows() {
			break
		}
		start := p.pos
		saved := p.noStructLit
		p.noStructLit = true
		written := p.parsePostfix()
		p.noStructLit = saved

		pattern, ok := p.variantPattern(written, true).(*ast.VariantPattern)
		if !ok {
			p.pos = start
			p.failWith(ErrUnexpectedToken,
				"Write a type (value is Int), or a variant with a pattern for each of its values: value is Shape.Rect(w, h)",
				"'is' takes a type or the pattern of a variant, and this is neither")
		}
		return &ast.PatternTestExpr{BaseNode: base(is), Expr: value, Pattern: pattern}

	case tok.Type == token.INT, tok.Type == token.FLOAT, tok.Type == token.STRING, tok.Type == token.MINUS,
		tok.Type == token.TRUE, tok.Type == token.FALSE, tok.Type == token.NULL:
		p.failWith(ErrUnexpectedToken, "Compare values with ==",
			"'is' takes a type or the pattern of a variant, found %s", describe(tok))
	}

	return &ast.TypeTestExpr{BaseNode: base(is), Expr: value, Type: p.parsePostfixType()}
}

// typeFollows reports whether the name at the cursor starts what can only
// be a type, and not the pattern of a variant: `Point`, `Box<Int>`,
// `geo.Shape?`. A name with one dot and nothing more may be either, and
// what goes on with a dot or with parentheses is a pattern:
// `Option<Int>.Some(n)`. Nothing is consumed.
//
// A name alone with parentheses, `value is Circle(r)`, is neither; that
// is said here, where the mistake is plain.
func (p *Parser) typeFollows() bool {
	start := p.pos
	parsed, ok := p.speculateType()
	end := p.pos
	p.pos = start
	if !ok {
		// Not a type after all; reading it as one says what is wrong
		return true
	}

	plain := parsed.Kind == ast.TypeBasic && len(parsed.Generics) == 0
	dotted := strings.Contains(parsed.Name, ".")
	switch p.toks[end].Type {
	case token.LPAREN:
		// value is Circle(r): a variant, its enum found by its name
		return false
	case token.DOT:
		return false
	}
	return !(plain && dotted)
}

// speculateType parses a type at the cursor without reporting anything,
// and says whether there was one.
func (p *Parser) speculateType() (parsed *ast.Type, ok bool) {
	wasSpeculating := p.speculating
	p.speculating = true
	defer func() {
		p.speculating = wasSpeculating
		if r := recover(); r != nil {
			if _, bailed := r.(bailout); !bailed {
				panic(r)
			}
			parsed, ok = nil, false
		}
	}()
	return p.parsePostfixType(), true
}

// tryTuplePattern parses the `(a, b)` after `is` as the pattern of a
// tuple, when it reads as one. It gives nil, with nothing consumed, for
// what is not: a tuple type written with types no value is written with
// (`(Int, [String])`), or a type in parentheses.
func (p *Parser) tryTuplePattern() (pattern *ast.VariantPattern) {
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
			pattern = nil
		}
	}()

	saved := p.noStructLit
	p.noStructLit = true
	defer func() { p.noStructLit = saved }()

	written := p.parsePrimary()
	call, ok := written.(*ast.CallExpr)
	if !ok {
		p.fail("not a tuple")
	}
	target, ok := call.Func.(*ast.MemberExpr)
	if !ok || target.Name != ast.TupleVariant {
		p.fail("not a tuple")
	}
	if enum, ok := target.Expr.(*ast.Ident); !ok || !ast.IsTupleName(enum.Name) {
		p.fail("not a tuple")
	}
	found, ok := p.variantPattern(written, true).(*ast.VariantPattern)
	if !ok {
		p.fail("not a tuple")
	}
	return found
}
