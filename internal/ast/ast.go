package ast

import (
	"fmt"
	"strings"

	"github.com/qoqiflow/qoqi/internal/token"
)

// Node is the base interface for all AST nodes.
type Node interface {
	String() string
	Position() token.Position
	Token() token.Token
}

// BaseNode provides common functionality for all AST nodes.
type BaseNode struct {
	Pos token.Position
	Tok token.Token // Original token (for rich diagnostics)

	// widened marks a whole number written where a Float is expected:
	// let half: Float = 1; The checker sets it, and the desugarer makes
	// the number a Float there. It is kept in the node, and not beside
	// it, so that a copy of the node is still marked.
	widened bool
}

// MarkWidened says that the value of this expression, an Int, is used as
// a Float.
func (n *BaseNode) MarkWidened(yes bool) { n.widened = yes }

// IsWidened reports whether the value of this expression, an Int, is
// used as a Float.
func (n *BaseNode) IsWidened() bool { return n.widened }

// Widenable is an expression that can be marked as an Int used as a Float.
type Widenable interface {
	MarkWidened(yes bool)
	IsWidened() bool
}

func (n *BaseNode) Position() token.Position {
	return n.Pos
}

func (n *BaseNode) Token() token.Token {
	return n.Tok
}

// Expr is the interface for all expression nodes.
type Expr interface {
	Node
	exprNode()
}

// Stmt is the interface for all statement nodes.
type Stmt interface {
	Node
	stmtNode()
}

// Decl is the interface for all declaration nodes.
type Decl interface {
	Node
	declNode()
}

// Program represents a full Qoqi source file.
type Program struct {
	BaseNode
	Imports []*ImportDecl
	Decls   []Decl

	// Stmts are the statements written outside any function, in the order
	// they are written: a script. Module loading makes them the body of
	// the program's main (see modules.Load); only the entry file may have
	// them.
	Stmts []Stmt

	// StmtStarts are where the statements of the script start, as written:
	// at their first token, which the position of a statement need not be
	StmtStarts []token.Position

	// Globals are the variables declared outside any function in a file
	// that is not run as a script: in a module, or next to a main. Each
	// is made the first time it is used, and its functions all see it.
	Globals []*LetStmt

	// Externs are the functions declared with `extern fn`: they have a
	// signature and no body, the runtime provides them. The parser leaves
	// them among Decls; module loading moves them here, so that later
	// stages only see declarations with something to compile.
	Externs []*FnDecl

	// KeepsRanges says that a range is used as a value somewhere in the
	// file: it then needs the Range of std.ops, imported or not.
	KeepsRanges bool

	// HasEntries says that the file writes a literal of a dictionary,
	// {"ada": 36}: it then needs std.dict and std.map, imported or not.
	HasEntries bool

	// HasWaits says that the file writes `wait`: what it waits with is
	// std.task, imported or not, under the name TaskModule.
	HasWaits bool

	// HasStages says that the file writes `stage`, or a step by a queue:
	// what passes it, or sends the step, is
	// std.flow, imported or not, under the name FlowModule.
	HasStages bool

	// Members are the names written after a dot in the file: value.name.
	// A method of the library among them brings its module along (see
	// modules.MethodModules).
	Members map[string]bool
}

func (p *Program) String() string {
	var sb strings.Builder
	for _, imp := range p.Imports {
		sb.WriteString(imp.String() + "\n")
	}
	for _, decl := range p.Decls {
		sb.WriteString(decl.String() + "\n\n")
	}
	for _, stmt := range p.Stmts {
		sb.WriteString(stmt.String() + "\n")
	}
	return sb.String()
}

// ImportDecl represents an import statement:
//
//	import physics.body;              the module, used as body.name
//	import physics.body as pb;        the module under another name
//	import physics.body.{Body, new};  names of the module, used directly
//	pub import physics.body.{Body};   the same, and exported again
type ImportDecl struct {
	BaseNode
	Path  string // Module path, dotted: "physics.body"
	Alias string
	Names []*ImportName // Names imported directly, if any
	Pub   bool

	// Implicit marks an import the program did not write: the one that
	// gives every file the name Result (see modules.ResultModule).
	Implicit bool
}

// ImportName is one name of an `import module.{...}` list.
type ImportName struct {
	BaseNode
	Name string
}

func (n *ImportName) String() string { return n.Name }

func (d *ImportDecl) String() string {
	s := "import " + d.Path
	if d.Pub {
		s = "pub " + s
	}
	if len(d.Names) > 0 {
		var names []string
		for _, n := range d.Names {
			names = append(names, n.Name)
		}
		s += ".{" + strings.Join(names, ", ") + "}"
	}
	if d.Alias != "" {
		s += " as " + d.Alias
	}
	return s + ";"
}

// TypeKind distinguishes different kinds of types.
type TypeKind int

const (
	TypeBasic TypeKind = iota
	TypeArray
	TypeOption
	TypeUnion
	TypeFunction
	TypeVariadic
)

// Type represents a type annotation.
type Type struct {
	BaseNode
	Kind TypeKind

	// For Basic (e.g. Int, MyStruct<T>)
	Name     string
	Generics []*Type

	// For Array, Option, Variadic
	ElemType *Type

	// For Union
	UnionTypes []*Type

	// For Function
	ParamTypes []*Type
	ReturnType *Type
}

func (t *Type) String() string {
	switch t.Kind {
	case TypeArray:
		return "[" + t.ElemType.String() + "]"
	case TypeOption:
		return t.ElemType.String() + "?"
	case TypeVariadic:
		return "..." + t.ElemType.String()
	case TypeUnion:
		var parts []string
		for _, u := range t.UnionTypes {
			parts = append(parts, u.String())
		}
		return strings.Join(parts, " | ")
	case TypeFunction:
		var params []string
		for _, p := range t.ParamTypes {
			params = append(params, p.String())
		}
		ret := "Void"
		if t.ReturnType != nil {
			ret = t.ReturnType.String()
		}
		return fmt.Sprintf("fn(%s) -> %s", strings.Join(params, ", "), ret)
	case TypeBasic:
		s := t.Name
		if len(t.Generics) > 0 {
			var args []string
			for _, g := range t.Generics {
				args = append(args, g.String())
			}
			s += "<" + strings.Join(args, ", ") + ">"
		}
		return s
	default:
		return "unknown_type"
	}
}

// --- Declarations ---
type FnDecl struct {
	BaseNode
	Name       string
	TypeParams []string
	// What each type parameter is required to implement: for
	// `fn largest<T: Ordered + Show>`, Bounds["T"] is [Ordered, Show]
	Bounds        map[string][]*Type
	Params        []*Param
	ReturnType    *Type
	Body          *BlockStmt
	IsTraitMethod bool

	// Static marks a function of an impl written `static fn`: a function
	// of the type, Point.origin(), which has no self
	Static bool

	// FromTrait names the trait whose default this method is a copy of,
	// for a method an impl did not write itself
	FromTrait string

	// Pub marks a declaration other modules may use. Module is the module
	// the declaration was written in, "" for the program's entry file.
	Pub    bool
	Module string

	// Extern marks a function the runtime provides: `extern fn name(...);`
	Extern bool

	// Step marks a step of a flow: `step fn charge(...)`. It is the only
	// way a flow reaches outside the program (see docs/flow.md)
	Step bool

	// Queue names, for a step the application carries out, who carries
	// it out: `step fn charge(invoice: Invoice) -> Charge by billing;`.
	// Such a step has no body of its own: the parser gives it one that
	// sends it to the queue (see docs/flow-app-steps.md). Undo names the
	// step that undoes it, `undo release`. Within is how long any step
	// may take, `-> Wire within 2.hours()`, nil for a minute.
	Queue    string
	QueueTok token.Token
	Undo     string
	Within   Expr

	// Migrate marks a migration of a flow, `migrate fn polling(...)`:
	// written in an impl of the flow, named after a stage of the version
	// before, it takes the variables that stage had and gives the struct
	// of the stage an instance goes on from in this one
	Migrate bool

	// CopyOf names the generic function this one is the copy of, for
	// some types: first_String is a copy of first
	CopyOf string

	// Script marks the main made of the statements a file has outside any
	// function (see Program.Stmts)
	Script bool
}

func (d *FnDecl) declNode() {}
func (d *FnDecl) String() string {
	var sb strings.Builder
	if d.Extern {
		sb.WriteString("extern ")
	}
	if d.Step {
		sb.WriteString("step ")
	}
	sb.WriteString("fn " + d.Name)
	if len(d.TypeParams) > 0 {
		sb.WriteString("<" + strings.Join(d.TypeParams, ", ") + ">")
	}
	sb.WriteString("(")
	var params []string
	for _, p := range d.Params {
		params = append(params, p.String())
	}
	sb.WriteString(strings.Join(params, ", "))
	sb.WriteString(")")
	if d.ReturnType != nil {
		sb.WriteString(" -> " + d.ReturnType.String())
	}
	if d.Within != nil {
		sb.WriteString(" within " + d.Within.String())
	}
	if d.Queue != "" {
		sb.WriteString(" by " + d.Queue)
		if d.Undo != "" {
			sb.WriteString(" undo " + d.Undo)
		}
		sb.WriteString(";")
	} else if d.Body != nil {
		sb.WriteString(" " + d.Body.String())
	} else {
		sb.WriteString(";")
	}
	return sb.String()
}

type Param struct {
	BaseNode
	Name string
	Type *Type

	// Default is what the parameter is when a call leaves it out:
	// fn open(path: String, mode: String = "r"). Made where the call is.
	Default Expr
}

func (p *Param) String() string {
	if p.Type == nil {
		// A parameter of a function written without types
		return p.Name
	}
	return fmt.Sprintf("%s: %s", p.Name, p.Type.String())
}

type StructDecl struct {
	BaseNode
	Name       string
	TypeParams []string
	Bounds     map[string][]*Type // See FnDecl.Bounds
	Fields     []*Field
	Pub        bool
	Module     string // The module that declares it, whose fields are its
}

func (d *StructDecl) declNode() {}
func (d *StructDecl) String() string {
	var sb strings.Builder
	sb.WriteString("struct " + d.Name)
	if len(d.TypeParams) > 0 {
		sb.WriteString("<" + strings.Join(d.TypeParams, ", ") + ">")
	}
	sb.WriteString(" { ")
	var fields []string
	for _, f := range d.Fields {
		fields = append(fields, f.String())
	}
	sb.WriteString(strings.Join(fields, ", "))
	sb.WriteString(" }")
	return sb.String()
}

type EnumDecl struct {
	BaseNode
	Name       string
	TypeParams []string
	Bounds     map[string][]*Type // See FnDecl.Bounds
	Variants   []*EnumVariant
	Pub        bool
}

func (d *EnumDecl) declNode() {}
func (d *EnumDecl) String() string {
	var sb strings.Builder
	sb.WriteString("enum " + d.Name)
	if len(d.TypeParams) > 0 {
		sb.WriteString("<" + strings.Join(d.TypeParams, ", ") + ">")
	}
	sb.WriteString(" { ")
	var variants []string
	for _, v := range d.Variants {
		variants = append(variants, v.String())
	}
	sb.WriteString(strings.Join(variants, ", "))
	sb.WriteString(" }")
	return sb.String()
}

type EnumVariant struct {
	BaseNode
	Name   string
	Values []*Type

	// Names are the names of the values, for a variant declared with
	// them: Rect { width: Float, height: Float }. Its values are still
	// in that order; the names say which is which where it is built or
	// matched, Rect { width: 2.0, height: 1.0 }.
	Names []string
}

func (v *EnumVariant) String() string {
	if len(v.Names) > 0 {
		var fields []string
		for i, t := range v.Values {
			fields = append(fields, v.Names[i]+": "+t.String())
		}
		return fmt.Sprintf("%s { %s }", v.Name, strings.Join(fields, ", "))
	}
	if len(v.Values) == 0 {
		return v.Name
	}
	var types []string
	for _, t := range v.Values {
		types = append(types, t.String())
	}
	return fmt.Sprintf("%s(%s)", v.Name, strings.Join(types, ", "))
}

type Field struct {
	BaseNode
	Name string
	Type *Type

	// Private marks a field written `priv`: only its module reads it,
	// writes it and gives it in a literal. Any other field is as visible
	// as its struct.
	Private bool

	// Default is the value a struct literal gives the field when it does
	// not mention it: `port: Int = 8080;`. Nil for a field that has to be
	// given.
	Default Expr
}

func (f *Field) String() string {
	return fmt.Sprintf("%s: %s", f.Name, f.Type.String())
}

type TraitDecl struct {
	BaseNode
	Name       string
	TypeParams []string // trait Source<T>
	Methods    []*FnDecl
	Pub        bool
}

func (d *TraitDecl) declNode() {}
func (d *TraitDecl) String() string {
	var sb strings.Builder
	sb.WriteString("trait " + d.Name + " { ")
	for _, m := range d.Methods {
		sb.WriteString(m.String() + " ")
	}
	sb.WriteString("}")
	return sb.String()
}

// ArrayImpl is what an impl for arrays has as the name of its struct:
// impl<T> [T] { ... } gives every array the methods it declares, T being
// what the array holds. Not a name a program can write for a struct.
const ArrayImpl = "[]"

// OptionalImpl is what an impl for optionals has as the name of its
// struct: impl<T> T? { ... } gives every optional the methods it
// declares, T being what it holds when it is not null.
const OptionalImpl = "?"

// ArrayImplOf is the name of the struct of an impl for the arrays of one
// type, impl [Int] { ... }: methods only those arrays have.
func ArrayImplOf(item string) string { return "[" + item + "]" }

// IsArrayImpl reports whether the struct of an impl names arrays, and
// which: "" for every array, or the type of the items.
func IsArrayImpl(structName string) (item string, ok bool) {
	if len(structName) < 2 || structName[0] != '[' || structName[len(structName)-1] != ']' {
		return "", false
	}
	return structName[1 : len(structName)-1], true
}

type ImplDecl struct {
	BaseNode
	TypeParams []string           // impl<T> Box<T>: the struct's type parameters, by position
	Bounds     map[string][]*Type // impl<T: Ordered> Box<T>; see FnDecl.Bounds
	StructName string
	TraitName  string
	TraitArgs  []*Type // impl Source<Int> for Counter: the trait's type arguments

	header  *BlockStmt // See TraitArgsScope
	Methods []*FnDecl
}

func (d *ImplDecl) declNode() {}
func (d *ImplDecl) String() string {
	var sb strings.Builder
	sb.WriteString("impl")
	generics := ""
	if len(d.TypeParams) > 0 {
		generics = "<" + strings.Join(d.TypeParams, ", ") + ">"
		sb.WriteString(generics)
	}
	if d.TraitName != "" {
		sb.WriteString(" " + d.TraitName + " for")
	}
	sb.WriteString(" " + d.StructName + generics + " { ")
	for _, m := range d.Methods {
		sb.WriteString(m.String() + " ")
	}
	sb.WriteString("}")
	return sb.String()
}

// --- Statements ---

type BlockStmt struct {
	BaseNode
	Stmts []Stmt
}

func (s *BlockStmt) stmtNode() {}
func (s *BlockStmt) String() string {
	var sb strings.Builder
	sb.WriteString("{ ")
	for _, stmt := range s.Stmts {
		sb.WriteString(stmt.String() + " ")
	}
	sb.WriteString("}")
	return sb.String()
}

type LetStmt struct {
	BaseNode
	Name    string
	Type    *Type
	Value   Expr
	IsConst bool
	NameTok token.Token // Where the name is written, when it is
}

func (s *LetStmt) stmtNode() {}
func (s *LetStmt) String() string {
	kind := "let"
	if s.IsConst {
		kind = "const"
	}
	t := ""
	if s.Type != nil {
		t = ": " + s.Type.String()
	}
	return fmt.Sprintf("%s %s%s = %s;", kind, s.Name, t, s.Value.String())
}

type ReturnStmt struct {
	BaseNode
	Value Expr
}

func (s *ReturnStmt) stmtNode() {}
func (s *ReturnStmt) String() string {
	if s.Value != nil {
		return fmt.Sprintf("return %s;", s.Value.String())
	}
	return "return;"
}

type IfStmt struct {
	BaseNode
	Cond Expr
	Then *BlockStmt
	Else Stmt // Can be BlockStmt or IfStmt
}

func (s *IfStmt) stmtNode() {}
func (s *IfStmt) String() string {
	str := fmt.Sprintf("if %s %s", s.Cond.String(), s.Then.String())
	if s.Else != nil {
		str += " else " + s.Else.String()
	}
	return str
}

type WhileStmt struct {
	BaseNode
	Cond Expr
	Body *BlockStmt

	// Update, when the desugarer gives one, is run after each iteration,
	// one ended by `continue` included, before the condition is asked
	// again: what a loop over numbers counts with. It may `break`. Source
	// has no form of it, nor of a loop without Cond, which runs until it
	// is left.
	Update Stmt
}

func (s *WhileStmt) stmtNode() {}
func (s *WhileStmt) String() string {
	text := "while " + s.Body.String()
	if s.Cond != nil {
		text = fmt.Sprintf("while %s %s", s.Cond.String(), s.Body.String())
	}
	if s.Update != nil {
		text += " update " + s.Update.String()
	}
	return text
}

type ForStmt struct {
	BaseNode
	Var      string
	Index    *ForIndex // `for i, item in items`: the position variable, or nil
	Iterable Expr
	Body     *BlockStmt
}

func (s *ForStmt) stmtNode() {}
func (s *ForStmt) String() string {
	vars := s.Var
	if s.Index != nil {
		vars = s.Index.Name + ", " + s.Var
	}
	return fmt.Sprintf("for %s in %s %s", vars, s.Iterable.String(), s.Body.String())
}

// ForIndex is the position variable of a for loop. It is a node of its own
// so that a use of the variable can be told from a use of the element.
type ForIndex struct {
	BaseNode
	Name string
}

func (i *ForIndex) String() string { return i.Name }

type BreakStmt struct{ BaseNode }

func (s *BreakStmt) stmtNode()      {}
func (s *BreakStmt) String() string { return "break;" }

type ContinueStmt struct{ BaseNode }

func (s *ContinueStmt) stmtNode()      {}
func (s *ContinueStmt) String() string { return "continue;" }

type MatchStmt struct {
	BaseNode
	Cond  Expr
	Cases []*MatchCase

	// Exhaustive is set by the type checker when the cases cover every
	// variant of the enum being matched, so that one of them always runs
	// although there is no `default`.
	Exhaustive bool

	// WaitsOn marks the match a `wait match` writes to check the cases of
	// one channel: they have to take every value it can give, null once
	// it is closed included, or the one no case takes would stay there.
	WaitsOn bool
}

func (s *MatchStmt) stmtNode() {}
func (s *MatchStmt) String() string {
	var sb strings.Builder
	sb.WriteString("match " + s.Cond.String() + " { ")
	for _, c := range s.Cases {
		sb.WriteString(c.String() + " ")
	}
	sb.WriteString("}")
	return sb.String()
}

type MatchCase struct {
	BaseNode
	Pattern Node // Can be Expr (value) or Type (type check)
	Body    Node // Can be *BlockStmt or Expr

	// Alternatives are the patterns after the first of a case that takes
	// several, `case Light.Red | Light.Amber:`. The case runs when any of
	// them matches.
	Alternatives []Node

	// Guard is the condition of `case Shape.Circle(r) if r > 10.0:`. The
	// case only runs when it holds, and it may use the names of the pattern.
	Guard Expr
}

// Patterns lists every pattern of the case: the first and its
// alternatives. A `default` has none.
func (c *MatchCase) Patterns() []Node {
	if c.Pattern == nil {
		return nil
	}
	return append([]Node{c.Pattern}, c.Alternatives...)
}

// IsDefault reports whether the case is `default:`.
func (c *MatchCase) IsDefault() bool { return c.Pattern == nil }

func (c *MatchCase) String() string {
	if c.Pattern == nil {
		return fmt.Sprintf("default: %s", c.Body.String())
	}
	var patterns []string
	for _, pattern := range c.Patterns() {
		patterns = append(patterns, pattern.String())
	}
	head := strings.Join(patterns, " | ")
	if c.Guard != nil {
		head += " if " + c.Guard.String()
	}
	return fmt.Sprintf("case %s: %s", head, c.Body.String())
}

// MatchExpr is a match that gives a value: `let size = match shape { ... };`.
// It is the statement, in a place where a value is expected: each case
// gives the value of the whole, as an expression, or leaves (return, break,
// continue, panic) in a block.
type MatchExpr struct {
	BaseNode
	Match *MatchStmt
}

func (e *MatchExpr) exprNode()      {}
func (e *MatchExpr) String() string { return "(" + e.Match.String() + ")" }

// RangeExpr is the whole numbers from Low up to High: `0..10` stops before
// High, `0..=10` includes it. It is what a `for` loops over, or what a
// case matches against; it is not a value to keep.
type RangeExpr struct {
	BaseNode
	Low       Expr
	High      Expr
	Inclusive bool

	// Counted marks the range of a loop (for i in 0..10) or of a case
	// (case 0..10:): numbers gone through or compared with, and never a
	// value. Any other range is one: a Range of std.ops, which the
	// loading of modules writes in its place.
	Counted bool
}

func (e *RangeExpr) exprNode() {}
func (e *RangeExpr) String() string {
	op := ".."
	if e.Inclusive {
		op = "..="
	}
	return fmt.Sprintf("(%s%s%s)", e.Low.String(), op, e.High.String())
}

// DeferStmt is cleanup that runs when the block it is written in is left,
// however that happens: by reaching its end, by return, break or continue,
// or by a failure passing through. `defer file.close();` is a body of one
// statement.
type DeferStmt struct {
	BaseNode
	Body *BlockStmt
}

func (s *DeferStmt) stmtNode()      {}
func (s *DeferStmt) String() string { return "defer " + s.Body.String() }

// --- Expressions made by desugaring ---
//
// Nobody writes these two. Desugaring makes them for what needs statements
// or a choice where a value stands: a match that gives a value, the
// condition of a case.

// BlockExpr is statements followed by the expression that gives their
// value. Without one (Value is nil) the statements always leave: they end
// in return, break, continue or panic, and no value is needed.
type BlockExpr struct {
	BaseNode
	Stmts []Stmt
	Value Expr
}

func (e *BlockExpr) exprNode() {}
func (e *BlockExpr) String() string {
	var sb strings.Builder
	sb.WriteString("{ ")
	for _, stmt := range e.Stmts {
		sb.WriteString(stmt.String() + " ")
	}
	if e.Value != nil {
		sb.WriteString(e.Value.String() + " ")
	}
	sb.WriteString("}")
	return sb.String()
}

// CondExpr is one of two values, by a condition; only the one chosen is
// evaluated.
type CondExpr struct {
	BaseNode
	Cond Expr
	Then Expr
	Else Expr
}

func (e *CondExpr) exprNode() {}
func (e *CondExpr) String() string {
	return fmt.Sprintf("(if %s %s else %s)", e.Cond.String(), e.Then.String(), e.Else.String())
}

type ExprStmt struct {
	BaseNode
	Expr Expr

	// Stage is the name of a stage of a flow, `stage polling;`, which is
	// this call of FlowModule.passStage (see docs/flow.md)
	Stage string
}

func (s *ExprStmt) stmtNode() {}
func (s *ExprStmt) String() string {
	if s.Stage != "" {
		return "stage " + s.Stage + ";"
	}
	return s.Expr.String() + ";"
}

// --- Expressions ---

// BinaryExpr is a binary operation. Assignments are binary expressions too:
// Op is "=" for `target = value`, and "+=", "-=", "*=", "/=", "%=" for the
// compound forms (`i++` is `i += 1`).
type BinaryExpr struct {
	BaseNode
	Left  Expr
	Op    string
	Right Expr
}

// CompoundOp returns the arithmetic operator of a compound assignment
// ("+" for "+="), and whether op is one.
func CompoundOp(op string) (string, bool) {
	switch op {
	case "+=", "-=", "*=", "/=", "%=", "&=", "|=", "^=", "<<=", ">>=":
		return op[:len(op)-1], true
	}
	return "", false
}

// IsAssignment reports whether op assigns to its left operand.
func IsAssignment(op string) bool {
	_, compound := CompoundOp(op)
	return op == "=" || compound
}

func (e *BinaryExpr) exprNode() {}
func (e *BinaryExpr) String() string {
	if e.Op == SafeAccessOp {
		return fmt.Sprintf("(%s?.%s)", e.Left.String(), e.Right.String())
	}
	return fmt.Sprintf("(%s %s %s)", e.Left.String(), e.Op, e.Right.String())
}

// SafeAccessOp is the operator of `value?.member`: a BinaryExpr whose left
// side is the value and whose right side is everything done with it,
// members, calls and indexes, starting from an identifier that stands for
// the value once it is known not to be null. `user?.address.city` is
//
//	BinaryExpr{Left: user, Op: "?.", Right: __safe1.address.city}
//
// It gives null when the value is null, without evaluating the right side.
const SafeAccessOp = "?."

// SafeHolder returns the identifier that stands for the value in the
// right side of a `?.`, or nil if e is not one.
//
// It is what the chain of the right side starts from. Later stages may
// rewrite the chain: `__safe1.items.len()` becomes a call of the library
// with `__safe1.items` as its first argument. So the holder is looked for
// by its name, in the receiver first and then in the arguments, and not
// inside a function written in the chain, which has names of its own.
func SafeHolder(e *BinaryExpr) *Ident {
	if e.Op != SafeAccessOp {
		return nil
	}
	return findHolder(e.Right)
}

// SafeHolderPrefix starts the name the parser gives the value of a `?.`.
const SafeHolderPrefix = "__safe"

func findHolder(expr Expr) *Ident {
	switch inner := expr.(type) {
	case *Ident:
		if strings.HasPrefix(inner.Name, SafeHolderPrefix) {
			return inner
		}
	case *MemberExpr:
		return findHolder(inner.Expr)
	case *IndexExpr:
		return findHolder(inner.Expr)
	case *BinaryExpr:
		// A `?.` further on: the holder is on its left
		return findHolder(inner.Left)
	case *CallExpr:
		if found := findHolder(inner.Func); found != nil {
			return found
		}
		for _, arg := range inner.Args {
			if found := findHolder(arg); found != nil {
				return found
			}
		}
	}
	return nil
}

// Tuples: `(1, "a")`, of type `(Int, String)`.
//
// A tuple is a value of an enum the compiler declares for each size,
//
//	enum __Tuple2<A, B> { Of(A, B) }
//
// and the parser writes everything about tuples in terms of it: the type
// `(Int, String)` is `__Tuple2<Int, String>`, the value `(1, "a")` is
// `__Tuple2.Of(1, "a")`, and `case (n, text):` is a pattern on that
// variant. What enums have, tuples have with no code of their own: type
// arguments worked out from the values, equality by content, patterns
// inside patterns, a check that a match is complete. The one thing added
// is reading a value by its position, `pair.0`: a MemberExpr whose name is
// a number.
const (
	TuplePrefix  = "__Tuple"
	TupleVariant = "Of"
	MinTupleSize = 2
	MaxTupleSize = 6
)

// TupleName is the name of the enum behind tuples of a size.
func TupleName(size int) string {
	return fmt.Sprintf("%s%d", TuplePrefix, size)
}

// IsTupleName reports whether a type name is that of a tuple.
func IsTupleName(name string) bool {
	return strings.HasPrefix(name, TuplePrefix)
}

// TupleIndex returns the position a member name stands for, as in
// `pair.0`, or false if it is an ordinary name.
func TupleIndex(member string) (int, bool) {
	if member == "" {
		return 0, false
	}
	index := 0
	for _, ch := range member {
		if ch < '0' || ch > '9' {
			return 0, false
		}
		index = index*10 + int(ch-'0')
	}
	return index, true
}

// TupleSource declares the enums behind tuples, as Qoqi source.
func TupleSource() string {
	var sb strings.Builder
	params := []string{"A", "B", "C", "D", "E", "F"}
	for size := MinTupleSize; size <= MaxTupleSize; size++ {
		list := strings.Join(params[:size], ", ")
		fmt.Fprintf(&sb, "pub enum %s<%s> { %s(%s) }\n", TupleName(size), list, TupleVariant, list)
	}
	return sb.String()
}

// TryOp is the operator of `value?`, a UnaryExpr written after its
// operand: the value of a Result or of an optional, or a return from the
// enclosing function with the failure, or with null.
const TryOp = "?"

// UnwrapOp is the operator of `value!`, written after its operand: the
// value of an optional or of a Result, the program stopping with the
// line when there is none.
const UnwrapOp = "!!"

type UnaryExpr struct {
	BaseNode
	Op   string
	Expr Expr
}

func (e *UnaryExpr) exprNode() {}
func (e *UnaryExpr) String() string {
	if e.Op == TryOp {
		return fmt.Sprintf("(%s?)", e.Expr.String())
	}
	if e.Op == UnwrapOp {
		return fmt.Sprintf("(%s!)", e.Expr.String())
	}
	return fmt.Sprintf("(%s%s)", e.Op, e.Expr.String())
}

// CastExpr converts a value to a type, checked when the program runs:
// `value as Int`.
type CastExpr struct {
	BaseNode
	Expr Expr
	Type *Type
}

func (e *CastExpr) exprNode() {}
func (e *CastExpr) String() string {
	return fmt.Sprintf("(%s as %s)", e.Expr.String(), e.Type.String())
}

// TypeTestExpr asks whether a value is of a type when the program runs:
// `value is Int`.
type TypeTestExpr struct {
	BaseNode
	Expr Expr
	Type *Type
}

func (e *TypeTestExpr) exprNode() {}
func (e *TypeTestExpr) String() string {
	return fmt.Sprintf("(%s is %s)", e.Expr.String(), e.Type.String())
}

type CallExpr struct {
	BaseNode
	Func Expr
	Args []Expr
}

func (e *CallExpr) exprNode() {}
func (e *CallExpr) String() string {
	var args []string
	for _, a := range e.Args {
		args = append(args, a.String())
	}
	return fmt.Sprintf("%s(%s)", e.Func.String(), strings.Join(args, ", "))
}

type MemberExpr struct {
	BaseNode
	Expr Expr
	Name string

	// Type arguments of a call to a generic method: stack.map<String>(f)
	TypeArgs []*Type

	// For a value read from a tuple being taken apart, `let (a, b) = pair;`:
	// how many names were written, which has to be what the tuple holds
	TupleSize int
}

func (e *MemberExpr) exprNode() {}
func (e *MemberExpr) String() string {
	s := fmt.Sprintf("%s.%s", e.Expr.String(), e.Name)
	if len(e.TypeArgs) > 0 {
		var args []string
		for _, t := range e.TypeArgs {
			args = append(args, t.String())
		}
		s += "<" + strings.Join(args, ", ") + ">"
	}
	return s
}

type IndexExpr struct {
	BaseNode
	Expr  Expr
	Index Expr
}

func (e *IndexExpr) exprNode() {}
func (e *IndexExpr) String() string {
	return fmt.Sprintf("%s[%s]", e.Expr.String(), e.Index.String())
}

type Literal struct {
	BaseNode
	Value string
	Kind  string // "int", "float", "string", "bool", "null"
}

func (e *Literal) exprNode() {}
func (e *Literal) String() string {
	if e.Kind == "string" {
		return fmt.Sprintf("%q", e.Value)
	}
	return e.Value
}

type Ident struct {
	BaseNode
	Name     string
	TypeArgs []*Type

	// Candidates are, for the Ident named EnumToFind, the enums a variant
	// written alone may be of: which one is told by the type the value
	// has to be.
	Candidates []string
}

// TaskModule is the name std.task goes by in a file that writes `wait`,
// which `wait value` and `wait match` are made of: wait value is
// __std_task.waitFor(value). No program writes it.
const TaskModule = "__std_task"

// FlowModule is the name std.flow goes by in a file that writes `stage`:
// stage polling is __std_flow.passStage("polling"). No program writes it.
const FlowModule = "__std_flow"

// EntriesFunction is what a literal of a dictionary calls, with its
// entries as pairs: {"ada": 36} is __entries([("ada", 36)]). The type
// checker makes it the dict.of or the map.of it is, by its keys.
const EntriesFunction = "__entries"

// EnumToFind stands for the enum of a variant written alone that more than
// one enum has, `Empty` with Shape.Empty and Box.Empty in scope: module
// loading writes it Enum.Variant with this for the enum, and the type
// checker puts the enum the value is expected to be of in its place.
const EnumToFind = "__enum"

func (e *Ident) exprNode() {}
func (e *Ident) String() string {
	s := e.Name
	if len(e.TypeArgs) > 0 {
		var args []string
		for _, t := range e.TypeArgs {
			args = append(args, t.String())
		}
		s += "<" + strings.Join(args, ", ") + ">"
	}
	return s
}

type StructInitExpr struct {
	BaseNode
	Name     string
	TypeArgs []*Type
	Fields   map[string]Expr

	// Range is the range this was written as, for the Range of the
	// standard library a range kept as a value is: let r = 0..10;
	// What is wrong with its ends is then said of a range.
	Range *RangeExpr

	// Shorthand are the fields written alone, Point { x }, whose value is
	// the name: in a pattern they are names to give
	Shorthand []string
}

func (e *StructInitExpr) exprNode() {}
func (e *StructInitExpr) String() string {
	name := e.Name
	if len(e.TypeArgs) > 0 {
		var args []string
		for _, t := range e.TypeArgs {
			args = append(args, t.String())
		}
		name += "<" + strings.Join(args, ", ") + ">"
	}

	var fields []string
	for k, v := range e.Fields {
		fields = append(fields, fmt.Sprintf("%s: %s", k, v.String()))
	}
	return fmt.Sprintf("%s { %s }", name, strings.Join(fields, ", "))
}

type ArrayLiteralExpr struct {
	BaseNode
	Elements []Expr
}

func (e *ArrayLiteralExpr) exprNode() {}
func (e *ArrayLiteralExpr) String() string {
	var elems []string
	for _, elem := range e.Elements {
		elems = append(elems, elem.String())
	}
	return fmt.Sprintf("[%s]", strings.Join(elems, ", "))
}

// FnExpr represents an anonymous function expression (lambda/closure)
type FnExpr struct {
	BaseNode
	Params     []*Param
	ReturnType *Type
	Body       *BlockStmt

	// Short marks a function written without types, `item -> item.name`:
	// the type checker writes them in, from the function type expected
	// where it stands. ExprBody says its body was one expression, which
	// is then the only statement of Body: returned, or just evaluated
	// when the function returns nothing.
	Short    bool
	ExprBody bool
	// Typed says the types of a short function have been written in
	Typed bool
}

func (e *FnExpr) exprNode() {}
func (e *FnExpr) String() string {
	var sb strings.Builder
	sb.WriteString("fn(")
	var params []string
	for _, p := range e.Params {
		params = append(params, p.String())
	}
	sb.WriteString(strings.Join(params, ", "))
	sb.WriteString(")")
	if e.ReturnType != nil {
		sb.WriteString(" -> " + e.ReturnType.String())
	}
	if e.Body != nil {
		sb.WriteString(" " + e.Body.String())
	}
	return sb.String()
}

// --- Constants ---

// ConstDecl is a constant declared at the top level of a file:
// `const LIMIT = 100;`. Its value is made of literals and other constants,
// and is worked out when the program is compiled.
type ConstDecl struct {
	BaseNode
	Name   string
	Type   *Type // Declared type, or nil
	Value  Expr
	Pub    bool
	Module string
}

func (d *ConstDecl) declNode() {}
func (d *ConstDecl) String() string {
	t := ""
	if d.Type != nil {
		t = ": " + d.Type.String()
	}
	return fmt.Sprintf("const %s%s = %s;", d.Name, t, d.Value.String())
}

// --- Patterns of a match on an enum ---

// VariantPattern matches one variant of an enum and takes its values
// apart: `case Shape.Rect(w, h):`. Target names the variant, as the
// expression `Shape.Rect`. Each argument is a pattern for one value of the
// variant: a *BindingPattern, a literal, or another *VariantPattern.
//
// Without arguments (`case Shape.Empty:`) it matches the variant whatever
// values it holds.
type VariantPattern struct {
	BaseNode
	Target  Expr
	Args    []Node
	HasArgs bool // Written with parentheses
}

func (p *VariantPattern) String() string {
	s := p.Target.String()
	if !p.HasArgs {
		return s
	}
	var args []string
	for _, arg := range p.Args {
		args = append(args, arg.String())
	}
	return s + "(" + strings.Join(args, ", ") + ")"
}

// StructPattern matches a struct by the values of its fields, and names
// them: `case Point { x: 0, y }:`. Type is the struct; each field has a
// pattern, a *BindingPattern for a field written alone (`y` is `y: y`), a
// literal, a *VariantPattern or another *StructPattern. The fields not
// written are not looked at.
type StructPattern struct {
	BaseNode
	Type   *Type
	Fields []*FieldPattern
}

// FieldPattern is one field of a StructPattern.
type FieldPattern struct {
	Name    string
	Pattern Node
}

func (p *StructPattern) String() string {
	var fields []string
	for _, field := range p.Fields {
		fields = append(fields, field.Name+": "+field.Pattern.String())
	}
	return p.Type.String() + " { " + strings.Join(fields, ", ") + " }"
}

// Bindings lists the names a struct pattern gives to values, those of the
// patterns inside it included.
func (p *StructPattern) Bindings() []*BindingPattern {
	var names []*BindingPattern
	for _, field := range p.Fields {
		names = append(names, bindingsOf(field.Pattern)...)
	}
	return names
}

// bindingsOf lists the names a pattern gives, whatever pattern it is.
func bindingsOf(pattern Node) []*BindingPattern {
	switch a := pattern.(type) {
	case *BindingPattern:
		if !a.Ignored() {
			return []*BindingPattern{a}
		}
	case *VariantPattern:
		return a.Bindings()
	case *StructPattern:
		return a.Bindings()
	}
	return nil
}

// BindingPattern names a value of a variant, making it a variable of the
// case: the `w` of `case Shape.Rect(w, h):`. The name `_` matches the
// value without naming it.
type BindingPattern struct {
	BaseNode
	Name string
}

func (p *BindingPattern) String() string { return p.Name }

// Ignored reports whether the pattern is `_`.
func (p *BindingPattern) Ignored() bool { return p.Name == "_" }

// PatternTestExpr asks whether a value fits a pattern, and names what the
// pattern names: `shape is Shape.Circle(r)`. It is a Bool like any other,
// and its names are variables of the code that only runs when it held:
// the rest of an `&&` it stands in, and the block of the `if` or `while`
// whose condition it is. Desugaring writes it as the tests and variables
// of a match case.
type PatternTestExpr struct {
	BaseNode
	Expr    Expr
	Pattern *VariantPattern
}

func (e *PatternTestExpr) exprNode() {}
func (e *PatternTestExpr) String() string {
	return fmt.Sprintf("(%s is %s)", e.Expr.String(), e.Pattern.String())
}

// Conjuncts lists what a condition is made of when it is a chain of `&&`,
// in the order it is evaluated: a, b and c for `a && b && c`, however the
// chain is grouped. Anything else is the one thing it is.
func Conjuncts(cond Expr) []Expr {
	if binary, ok := cond.(*BinaryExpr); ok && binary.Op == "&&" {
		return append(Conjuncts(binary.Left), Conjuncts(binary.Right)...)
	}
	return []Expr{cond}
}

// GuardTest gives, for an `if` that leaves when a pattern test does not
// hold, the test: the `value is Ok(n)` of
//
//	if !(value is Ok(n)) { return; }
//
// whose names are variables of what follows the `if` in its block. The
// `if` has no `else`, and its block always leaves: it ends with return,
// break, continue or a call of panic. Anything else gives nil.
func GuardTest(s *IfStmt) Expr {
	not, ok := s.Cond.(*UnaryExpr)
	if !ok || not.Op != "!" || s.Else != nil || len(PatternTests(not.Expr)) == 0 || !Leaves(s.Then) {
		return nil
	}
	return not.Expr
}

// Leaves reports whether a block ends with a statement that leaves it,
// whatever happens: return, break, continue, or a call of panic.
func Leaves(block *BlockStmt) bool {
	if block == nil || len(block.Stmts) == 0 {
		return false
	}
	switch last := block.Stmts[len(block.Stmts)-1].(type) {
	case *ReturnStmt, *BreakStmt, *ContinueStmt:
		return true
	case *ExprStmt:
		call, ok := last.Expr.(*CallExpr)
		if !ok {
			return false
		}
		ident, ok := call.Func.(*Ident)
		return ok && ident.Name == "panic"
	case *BlockStmt:
		return Leaves(last)
	}
	return false
}

// PatternTests lists the pattern tests whose names reach what a condition
// guards: the ones that have to hold for the whole condition to hold.
// One under `||` or `!` is not among them, since the condition can hold
// without it.
func PatternTests(cond Expr) []*PatternTestExpr {
	var tests []*PatternTestExpr
	for _, part := range Conjuncts(cond) {
		if test, ok := part.(*PatternTestExpr); ok {
			tests = append(tests, test)
		}
	}
	return tests
}

// Bindings lists the names a pattern gives to values, those of the
// patterns inside it included, in the order they are written.
func (p *VariantPattern) Bindings() []*BindingPattern {
	var names []*BindingPattern
	for _, arg := range p.Args {
		names = append(names, bindingsOf(arg)...)
	}
	return names
}

// --- Enum values after desugaring ---
//
// The parser cannot tell `Shape.Circle(2.0)` from a method call, so it is
// an ordinary call until desugaring, which knows what `Shape` is and
// replaces it by the nodes below.

// VariantExpr creates a value of an enum: the variant and its values.
type VariantExpr struct {
	BaseNode
	Enum     string
	Variant  string
	TypeArgs []*Type // Of a generic enum, when known
	Args     []Expr
}

func (e *VariantExpr) exprNode() {}
func (e *VariantExpr) String() string {
	s := e.Enum
	if len(e.TypeArgs) > 0 {
		var args []string
		for _, t := range e.TypeArgs {
			args = append(args, t.String())
		}
		s += "<" + strings.Join(args, ", ") + ">"
	}
	s += "." + e.Variant
	if len(e.Args) == 0 {
		return s
	}
	var args []string
	for _, a := range e.Args {
		args = append(args, a.String())
	}
	return s + "(" + strings.Join(args, ", ") + ")"
}

// VariantTestExpr asks whether an enum value is a given variant.
type VariantTestExpr struct {
	BaseNode
	Expr    Expr
	Enum    string
	Variant string
}

func (e *VariantTestExpr) exprNode() {}
func (e *VariantTestExpr) String() string {
	return fmt.Sprintf("(%s is %s.%s)", e.Expr.String(), e.Enum, e.Variant)
}

// VariantFieldExpr reads one of the values of an enum value known to be a
// given variant.
type VariantFieldExpr struct {
	BaseNode
	Expr    Expr
	Enum    string
	Variant string
	Index   int
}

func (e *VariantFieldExpr) exprNode() {}
func (e *VariantFieldExpr) String() string {
	return fmt.Sprintf("%s.%s#%d", e.Expr.String(), e.Variant, e.Index)
}

// TraitArgsScope is the node the scope of an impl's header is recorded
// under: its type parameters, seen by its requirements and by the type
// arguments of its trait. The impl itself stands for the scope of each of
// its methods.
func (d *ImplDecl) TraitArgsScope() Node {
	if d.header == nil {
		d.header = &BlockStmt{BaseNode: d.BaseNode}
	}
	return d.header
}
