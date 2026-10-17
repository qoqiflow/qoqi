package ast

// Copies of parts of a program. A stage that writes the same code in two
// places (the default method of a trait in each impl that uses it, the
// default value of a field in each struct literal that leaves it out)
// needs a copy for each: the stages after it record what they learn about
// a node under that node.
//
// A Cloner may also replace type names on the way: the T of
// `trait Source<T>` by the Int of `impl Source<Int> for Counter`.

// Cloner copies nodes. Types maps the names of types to replace to what
// replaces them; nil replaces nothing.
type Cloner struct {
	Types map[string]*Type
}

// Type copies a type, replacing the names the cloner was given.
func (c *Cloner) Type(t *Type) *Type {
	if t == nil {
		return nil
	}
	if t.Kind == TypeBasic && len(t.Generics) == 0 {
		if with, ok := c.Types[t.Name]; ok {
			return (&Cloner{}).Type(with)
		}
	}
	copy := *t
	copy.Generics = c.types(t.Generics)
	copy.ElemType = c.Type(t.ElemType)
	copy.UnionTypes = c.types(t.UnionTypes)
	copy.ParamTypes = c.types(t.ParamTypes)
	copy.ReturnType = c.Type(t.ReturnType)
	return &copy
}

func (c *Cloner) types(list []*Type) []*Type {
	if list == nil {
		return nil
	}
	out := make([]*Type, len(list))
	for i, t := range list {
		out[i] = c.Type(t)
	}
	return out
}

func (c *Cloner) params(list []*Param) []*Param {
	if list == nil {
		return nil
	}
	out := make([]*Param, len(list))
	for i, p := range list {
		copy := *p
		copy.Type = c.Type(p.Type)
		out[i] = &copy
	}
	return out
}

// Fn copies a function declaration.
func (c *Cloner) Fn(fn *FnDecl) *FnDecl {
	copy := *fn
	copy.TypeParams = append([]string(nil), fn.TypeParams...)
	copy.Bounds = c.bounds(fn.Bounds)
	copy.Params = c.params(fn.Params)
	copy.ReturnType = c.Type(fn.ReturnType)
	copy.Body = c.Block(fn.Body)
	return &copy
}

func (c *Cloner) bounds(bounds map[string][]*Type) map[string][]*Type {
	if bounds == nil {
		return nil
	}
	out := make(map[string][]*Type, len(bounds))
	for name, list := range bounds {
		out[name] = c.types(list)
	}
	return out
}

// Block copies a block and everything in it.
func (c *Cloner) Block(b *BlockStmt) *BlockStmt {
	if b == nil {
		return nil
	}
	copy := *b
	copy.Stmts = make([]Stmt, len(b.Stmts))
	for i, stmt := range b.Stmts {
		copy.Stmts[i] = c.Stmt(stmt)
	}
	return &copy
}

// Stmt copies a statement.
func (c *Cloner) Stmt(stmt Stmt) Stmt {
	switch s := stmt.(type) {
	case nil:
		return nil
	case *BlockStmt:
		return c.Block(s)
	case *LetStmt:
		copy := *s
		copy.Type = c.Type(s.Type)
		copy.Value = c.Expr(s.Value)
		return &copy
	case *ReturnStmt:
		copy := *s
		copy.Value = c.Expr(s.Value)
		return &copy
	case *IfStmt:
		copy := *s
		copy.Cond = c.Expr(s.Cond)
		copy.Then = c.Block(s.Then)
		copy.Else = c.Stmt(s.Else)
		return &copy
	case *WhileStmt:
		copy := *s
		copy.Cond = c.Expr(s.Cond)
		copy.Body = c.Block(s.Body)
		return &copy
	case *ForStmt:
		copy := *s
		if s.Index != nil {
			index := *s.Index
			copy.Index = &index
		}
		copy.Iterable = c.Expr(s.Iterable)
		copy.Body = c.Block(s.Body)
		return &copy
	case *MatchStmt:
		copy := *s
		copy.Cond = c.Expr(s.Cond)
		copy.Cases = make([]*MatchCase, len(s.Cases))
		for i, mc := range s.Cases {
			one := *mc
			one.Pattern = c.node(mc.Pattern)
			one.Body = c.node(mc.Body)
			if mc.Alternatives != nil {
				one.Alternatives = make([]Node, len(mc.Alternatives))
				for j, alt := range mc.Alternatives {
					one.Alternatives[j] = c.node(alt)
				}
			}
			one.Guard = c.Expr(mc.Guard)
			copy.Cases[i] = &one
		}
		return &copy
	case *DeferStmt:
		copy := *s
		copy.Body = c.Block(s.Body)
		return &copy
	case *ExprStmt:
		copy := *s
		copy.Expr = c.Expr(s.Expr)
		return &copy
	case *BreakStmt:
		copy := *s
		return &copy
	case *ContinueStmt:
		copy := *s
		return &copy
	}
	return stmt
}

// node copies what a match case holds: a pattern or a body.
func (c *Cloner) node(n Node) Node {
	switch v := n.(type) {
	case nil:
		return nil
	case *Type:
		return c.Type(v)
	case *VariantPattern:
		copy := *v
		copy.Target = c.Expr(v.Target)
		copy.Args = make([]Node, len(v.Args))
		for i, arg := range v.Args {
			copy.Args[i] = c.node(arg)
		}
		return &copy
	case *BindingPattern:
		copy := *v
		return &copy
	case *StructPattern:
		copy := *v
		copy.Type = c.Type(v.Type)
		copy.Fields = make([]*FieldPattern, len(v.Fields))
		for i, field := range v.Fields {
			copy.Fields[i] = &FieldPattern{Name: field.Name, Pattern: c.node(field.Pattern)}
		}
		return &copy
	case Stmt:
		return c.Stmt(v)
	case Expr:
		return c.Expr(v)
	}
	return n
}

func (c *Cloner) exprs(list []Expr) []Expr {
	if list == nil {
		return nil
	}
	out := make([]Expr, len(list))
	for i, e := range list {
		out[i] = c.Expr(e)
	}
	return out
}

// Expr copies an expression.
func (c *Cloner) Expr(expr Expr) Expr {
	switch e := expr.(type) {
	case nil:
		return nil
	case *Literal:
		copy := *e
		return &copy
	case *Ident:
		copy := *e
		copy.TypeArgs = c.types(e.TypeArgs)
		return &copy
	case *BinaryExpr:
		copy := *e
		copy.Left = c.Expr(e.Left)
		copy.Right = c.Expr(e.Right)
		return &copy
	case *UnaryExpr:
		copy := *e
		copy.Expr = c.Expr(e.Expr)
		return &copy
	case *CastExpr:
		copy := *e
		copy.Expr = c.Expr(e.Expr)
		copy.Type = c.Type(e.Type)
		return &copy
	case *TypeTestExpr:
		copy := *e
		copy.Expr = c.Expr(e.Expr)
		copy.Type = c.Type(e.Type)
		return &copy
	case *PatternTestExpr:
		copy := *e
		copy.Expr = c.Expr(e.Expr)
		copy.Pattern = c.node(e.Pattern).(*VariantPattern)
		return &copy
	case *CallExpr:
		copy := *e
		copy.Func = c.Expr(e.Func)
		copy.Args = c.exprs(e.Args)
		return &copy
	case *MemberExpr:
		copy := *e
		copy.Expr = c.Expr(e.Expr)
		copy.TypeArgs = c.types(e.TypeArgs)
		return &copy
	case *IndexExpr:
		copy := *e
		copy.Expr = c.Expr(e.Expr)
		copy.Index = c.Expr(e.Index)
		return &copy
	case *StructInitExpr:
		copy := *e
		copy.TypeArgs = c.types(e.TypeArgs)
		copy.Fields = make(map[string]Expr, len(e.Fields))
		for name, value := range e.Fields {
			copy.Fields[name] = c.Expr(value)
		}
		return &copy
	case *ArrayLiteralExpr:
		copy := *e
		copy.Elements = c.exprs(e.Elements)
		return &copy
	case *FnExpr:
		copy := *e
		copy.Params = c.params(e.Params)
		copy.ReturnType = c.Type(e.ReturnType)
		copy.Body = c.Block(e.Body)
		return &copy
	case *VariantExpr:
		copy := *e
		copy.TypeArgs = c.types(e.TypeArgs)
		copy.Args = c.exprs(e.Args)
		return &copy
	case *VariantTestExpr:
		copy := *e
		copy.Expr = c.Expr(e.Expr)
		return &copy
	case *VariantFieldExpr:
		copy := *e
		copy.Expr = c.Expr(e.Expr)
		return &copy
	case *MatchExpr:
		copy := *e
		copy.Match = c.Stmt(e.Match).(*MatchStmt)
		return &copy
	case *RangeExpr:
		copy := *e
		copy.Low = c.Expr(e.Low)
		copy.High = c.Expr(e.High)
		return &copy
	case *BlockExpr:
		copy := *e
		copy.Stmts = make([]Stmt, len(e.Stmts))
		for i, stmt := range e.Stmts {
			copy.Stmts[i] = c.Stmt(stmt)
		}
		copy.Value = c.Expr(e.Value)
		return &copy
	case *CondExpr:
		copy := *e
		copy.Cond = c.Expr(e.Cond)
		copy.Then = c.Expr(e.Then)
		copy.Else = c.Expr(e.Else)
		return &copy
	}
	return expr
}
