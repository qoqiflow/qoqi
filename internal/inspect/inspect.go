// Package inspect answers questions about a Qoqi program from its source:
// what is wrong with it, what it declares, what the name under the cursor
// is, what could be written next. It is the front of the compiler made
// available to programs written in Qoqi (the module std.compiler), an
// editor's language server first of all.
package inspect

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/compiler"
	"github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/project"
	"github.com/qoqiflow/qoqi/internal/resolve"
	"github.com/qoqiflow/qoqi/internal/stdlib"
	"github.com/qoqiflow/qoqi/internal/types"
)

// Diagnostic is something the compiler has to say about a program.
type Diagnostic struct {
	Severity   string // "error", "warning" or "info"
	Code       string
	Message    string
	File       string
	Line       int
	Column     int
	EndColumn  int // Exclusive; Column + 1 when the extent is not known
	Suggestion string
}

// Symbol is something a file declares.
type Symbol struct {
	Kind      string // "function", "struct", "enum", "trait", "constant", "field", "method", "variant"
	Name      string
	Detail    string // Its signature or type
	Container string // What it is part of, for a field, a method or a variant
	Line      int
	Column    int
}

// Description is what is known of the name at a place of a file.
type Description struct {
	Signature string // How it is declared: "fn double(n: Int) -> Int", "let total: Int"
	Doc       string // The comment above its declaration
	// Where it is declared. File is "" for what has no place: a builtin.
	File   string
	Line   int
	Column int
	// The extent of the name asked about, on its line
	StartColumn int
	EndColumn   int
}

// Completion is something that could be written at a place of a file.
type Completion struct {
	Label  string
	Kind   string // As Symbol.Kind, plus "variable", "module" and "keyword"
	Detail string
}

// --- Files being edited ---

var (
	lock     sync.Mutex
	overlays = map[string]string{} // Text of files open in an editor, by absolute path
	lastKey  string
	last     *compiler.Analysis
)

// Overlay says a file is being edited, and what it holds for now: that
// text is what importing the file gives, not what is on disk.
func Overlay(path, text string) {
	lock.Lock()
	defer lock.Unlock()
	overlays[absolute(path)] = text
	last, lastKey = nil, ""
}

// Forget ends what Overlay started for a file.
func Forget(path string) {
	lock.Lock()
	defer lock.Unlock()
	delete(overlays, absolute(path))
	last, lastKey = nil, ""
}

func absolute(path string) string {
	if strings.HasPrefix(path, "<") {
		return path
	}
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}

// options says where the modules of the program at path are found: by its
// project file if it has one, with the files being edited in front.
func options(path string) compiler.Options {
	opts := compiler.Options{Unused: true}
	if cfg, err := project.Find(filepath.Dir(path)); err == nil && cfg != nil {
		opts.Root, opts.Packages, opts.Queues = cfg.RootDir(), cfg.PackageDirs(), cfg.QueueNames()
	}
	opts.Read = func(file string) (string, error) {
		if text, ok := overlays[absolute(file)]; ok {
			return text, nil
		}
		data, err := os.ReadFile(file)
		return string(data), err
	}
	return opts
}

// analyze understands the program at path, whose text is src. The last
// answer is kept: an editor asks several things of the same text.
func analyze(path, src string) *compiler.Analysis {
	key := path + "\x00" + src
	if last != nil && key == lastKey {
		return last
	}
	analysis := compiler.Analyze(src, path, options(path), errors.NewHandler())
	last, lastKey = analysis, key
	return analysis
}

// --- Diagnostics ---

// missingMain is the code of "program has no 'main' function".
const missingMain = "E6006"

// Check compiles the program at path, whose text is src, and gives
// everything the compiler has to say, in this file and in the modules it
// imports.
func Check(path, src string) []Diagnostic {
	lock.Lock()
	defer lock.Unlock()

	errs := errors.NewHandler()
	library := libraryFile(path)
	if library != "" {
		// A file of the standard library is a module of std, not a
		// program: checked as one, by a program that imports it. Its
		// extern functions are the runtime's under that name only.
		opts := options(path)
		read := opts.Read
		opts.Root = ""
		opts.Packages = map[string]string{stdlib.Package: stdlib.Root}
		opts.Read = func(file string) (string, error) {
			switch {
			case file == library:
				return src, nil
			case stdlib.IsPath(file):
				return stdlib.Read(file)
			}
			return read(file)
		}
		module := stdlib.Package + "." + strings.TrimSuffix(strings.TrimPrefix(library, stdlib.Root+"/"), ".qi")
		// The file is the library: all of it is looked at, though the
		// program made around it uses none of it
		opts.WholeLibrary = true
		compiler.CompileOptions("import "+module+";\nfn main() -> Int { return 0; }\n", libraryProbe, opts, errs)
	} else {
		compiler.CompileOptions(src, path, options(path), errs)
	}

	out := make([]Diagnostic, 0, len(errs.Diagnostics))
	for _, d := range errs.Diagnostics {
		if library != "" {
			if d.Pos.File != library {
				// Of the program that imports it, or of another module
				continue
			}
			d.Pos.File = path
		}
		if d.Code == missingMain {
			// A file without a main is a module: checked as a program
			// because that is the only way to check it, not because it
			// is meant to be run
			continue
		}
		one := Diagnostic{
			Severity:   strings.ToLower(d.Severity.String()),
			Code:       d.Code,
			Message:    d.Message,
			File:       d.Pos.File,
			Line:       d.Pos.Line,
			Column:     d.Pos.Col,
			EndColumn:  d.Pos.Col + 1,
			Suggestion: d.Suggestion,
		}
		if one.File == "" {
			one.File = path
		}
		if len(d.Highlights) > 0 && d.Highlights[0].EndCol > d.Highlights[0].StartCol {
			one.Column, one.EndColumn = d.Highlights[0].StartCol, d.Highlights[0].EndCol
		}
		if one.Line < 1 {
			one.Line, one.Column, one.EndColumn = 1, 1, 2
		}
		out = append(out, one)
	}
	return out
}

// libraryProbe is the name of the program a file of the library is
// checked through.
const libraryProbe = "<check>.qi"

// libraryFile gives the name the library knows a file on disk by,
// "<std>/math.qi", or "" for a file that is not one of its own: the
// copies OnDisk writes out for an editor to open, and the sources of the
// library in a checkout of qoqi.
func libraryFile(path string) string {
	name := stdlib.Root + "/" + filepath.Base(path)
	if _, err := stdlib.Read(name); err != nil {
		return ""
	}
	dir := filepath.ToSlash(filepath.Dir(absolute(path)))
	if strings.HasSuffix(dir, "/internal/stdlib/lib") {
		return name
	}
	if base, err := os.UserCacheDir(); err == nil && dir == filepath.ToSlash(filepath.Join(base, "qoqi", "std")) {
		return name
	}
	return ""
}

// --- What a file declares ---

// Outline lists what the file at path declares, in the order it does.
func Outline(path, src string) []Symbol {
	lock.Lock()
	defer lock.Unlock()

	analysis := analyze(path, src)
	if analysis.Program == nil {
		return nil
	}
	lines := strings.Split(src, "\n")
	var out []Symbol
	add := func(kind, name, detail, container string, node ast.Node) {
		pos := node.Position()
		if pos.File != path {
			return
		}
		out = append(out, Symbol{Kind: kind, Name: name, Detail: detail, Container: container, Line: pos.Line, Column: nameColumn(lines, pos.Line, pos.Col, name)})
	}
	for _, decl := range analysis.Program.Decls {
		switch d := decl.(type) {
		case *ast.FnDecl:
			add("function", d.Name, signature(d, d.Name), "", d)
		case *ast.ConstDecl:
			add("constant", d.Name, typeText(d.Type), "", d)
		case *ast.StructDecl:
			add("struct", d.Name, "", "", d)
			for _, field := range d.Fields {
				add("field", field.Name, typeText(field.Type), d.Name, field)
			}
		case *ast.EnumDecl:
			add("enum", d.Name, "", "", d)
			for _, variant := range d.Variants {
				add("variant", variant.Name, variantDetail(variant), d.Name, variant)
			}
		case *ast.TraitDecl:
			add("trait", d.Name, "", "", d)
			for _, method := range d.Methods {
				add("method", method.Name, signature(method, method.Name), d.Name, method)
			}
		case *ast.ImplDecl:
			for _, method := range d.Methods {
				if method.FromTrait != "" {
					continue // A default the impl did not write
				}
				add("method", method.Name, signature(method, method.Name), d.StructName, method)
			}
		}
	}
	// The functions the program only declares are kept apart from the rest
	for _, extern := range analysis.Program.Externs {
		add("function", extern.Name, signature(extern, extern.Name), "", extern)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Line < out[j].Line })
	return out
}

// --- What a name is ---

// Describe says what the name at a line and column of the file at path
// is, or nil if there is no name there or the program cannot be read.
func Describe(path, src string, line, column int) *Description {
	lock.Lock()
	defer lock.Unlock()

	analysis := analyze(path, src)
	if analysis.Info == nil {
		return nil
	}
	d := &Description{}
	place := func(node ast.Node, name string) {
		pos := node.Position()
		if pos.Line < 1 {
			return
		}
		source := sourceOf(pos.File, path, src)
		d.File, d.Line = absolute(pos.File), pos.Line
		d.Column = nameColumn(source, pos.Line, pos.Col, name)
		d.Doc = docAbove(source, pos.Line)
	}

	lines := strings.Split(src, "\n")
	if imported := importAt(analysis.Program, path, lines, line, column, d); imported {
		return d
	}
	found := exprAt(analysis.Program, path, line, column)
	if found == nil {
		// Not a name used in an expression: the name of a type where one
		// is written, or a name where it is declared
		if typ := typeAt(analysis.Program, path, lines, line, column); typ != nil {
			d.StartColumn, d.EndColumn = typ.start, typ.end
			node, text, doc := typeDeclaration(analysis.Program, typ.name)
			d.Signature = text
			if node != nil {
				place(node, lastPart(typ.name))
			} else {
				d.Doc = doc
			}
			return d
		}
		if owner, name, start := fieldKeyAt(analysis.Program, path, lines, line, column); name != "" {
			d.StartColumn, d.EndColumn = start, start+len([]rune(name))
			if node, text := member(analysis.Program, owner, name); node != nil {
				d.Signature = text
				place(node, name)
				return d
			}
		}
		declared := declaredAt(analysis, path, lines, line, column)
		if declared == nil {
			return nil
		}
		d.StartColumn, d.EndColumn, d.Signature = declared.start, declared.end, declared.signature
		place(declared.node, declared.name)
		return d
	}
	d.StartColumn, d.EndColumn = found.start, found.end

	switch e := found.expr.(type) {
	case *ast.Ident:
		typ := typeName(analysis.Info.Types[e])
		d.Signature = e.Name + ": " + typ
		sym := analysis.Table.Defs[e]
		if sym == nil {
			// The enum of a pattern, `case Shape.Circle(r)`: named, not looked up
			if node, text, _ := typeDeclaration(analysis.Program, e.Name); node != nil {
				d.Signature = text
				place(node, lastPart(e.Name))
			} else if analysis.Info.Types[e] == nil {
				return nil
			}
			break
		}
		short := lastPart(sym.Name)
		switch decl := sym.Decl.(type) {
		case *ast.FnDecl:
			d.Signature = signature(decl, displayName(decl.Name))
			if fn, ok := analysis.Info.Types[e].(*types.FunctionType); ok && e.Name == types.WaitFunction {
				// What wait gives, for this value
				d.Signature = "(operator) wait -> " + typeName(fn.ReturnType)
			}
		case *ast.Param:
			d.Signature = "(parameter) " + decl.Name + ": " + typ
		case *ast.LetStmt:
			keyword := "let"
			if decl.IsConst {
				keyword = "const"
			}
			d.Signature = keyword + " " + decl.Name + ": " + typ
		case *ast.ConstDecl:
			d.Signature = "const " + displayName(decl.Name) + ": " + typ
		case *ast.StructDecl:
			d.Signature = structText(decl)
		case *ast.EnumDecl:
			d.Signature = enumText(decl)
		case *ast.TraitDecl:
			d.Signature = "trait " + displayName(decl.Name)
		}
		if sym.Decl != nil {
			place(sym.Decl, short)
		}

	case *ast.StructInitExpr:
		name := found.name
		for _, decl := range analysis.Program.Decls {
			if s, ok := decl.(*ast.StructDecl); ok && (s.Name == name || lastPart(s.Name) == lastPart(name)) {
				d.Signature = structText(s)
				place(s, lastPart(s.Name))
				break
			}
		}
		if d.Signature == "" {
			return nil
		}

	case *ast.MemberExpr:
		owner := ownerName(analysis.Info.Types[e.Expr])
		if holder, ok := e.Expr.(*ast.Ident); ok {
			// Shape.Circle: the holder is the enum itself, not a value
			if sym := analysis.Table.Defs[holder]; sym != nil && (sym.Kind == resolve.SymbolEnum || sym.Kind == resolve.SymbolStruct) {
				owner = sym.Name
			} else if node, _, _ := typeDeclaration(analysis.Program, holder.Name); sym == nil && node != nil {
				owner = holder.Name
			}
		}
		d.Signature = e.Name + ": " + typeName(analysis.Info.Types[e])
		if owner == "" {
			break
		}
		d.Signature = displayName(owner) + "." + d.Signature
		if node, text := member(analysis.Program, owner, e.Name); node != nil {
			d.Signature = text
			place(node, e.Name)
		}
	}
	return d
}

// located is an expression that stands at a place, with the extent of
// the name that is there.
type located struct {
	expr       ast.Expr
	name       string
	start, end int
}

// exprAt finds the name at a line and column of a file: an identifier, a
// member after a dot, or the struct a literal makes.
func exprAt(prog *ast.Program, file string, line, column int) *located {
	var best *located
	consider := func(expr ast.Expr, name string, start int) {
		end := start + len([]rune(name))
		if column < start || column >= end {
			return
		}
		if best == nil || end-start < best.end-best.start {
			best = &located{expr: expr, name: name, start: start, end: end}
		}
	}
	visit := func(expr ast.Expr) {
		tok := expr.Token()
		if tok.Pos.File != file || tok.Pos.Line != line {
			return
		}
		switch e := expr.(type) {
		case *ast.Ident:
			// A name reached through its module is one identifier whose
			// text is as written: math.abs. `wait` is the call of
			// std.task.waitFor, written as the word alone
			if e.Name == types.WaitFunction {
				consider(e, "wait", tok.Pos.Col)
				return
			}
			consider(e, tok.Literal, tok.Pos.Col)
		case *ast.StructInitExpr:
			consider(e, tok.Literal, tok.Pos.Col)
		case *ast.MemberExpr:
			start := tok.Pos.Col
			if tok.Literal != e.Name {
				start = tok.EndPos.Col // The token is the dot before the name
			}
			consider(e, e.Name, start)
		}
	}
	eachBody(prog, func(node ast.Node) {
		types.WalkExprs(node, visit)
		eachPattern(node, func(pattern *ast.VariantPattern) { types.WalkExprs(pattern.Target, visit) })
	})
	return best
}

// fieldKeyAt finds the name of a field where a struct value gives it,
// the `cents` of `Price { cents: 100 }`: the struct it is of, the name
// and its column. A value is found by the place of what it is given,
// which starts on the same line as its name, after it.
func fieldKeyAt(prog *ast.Program, file string, lines []string, line, column int) (string, string, int) {
	owner, found, at := "", "", 0
	eachBody(prog, func(node ast.Node) {
		types.WalkExprs(node, func(expr ast.Expr) {
			literal, ok := expr.(*ast.StructInitExpr)
			if !ok || literal.Position().File != file || literal.Position().Line > line {
				return
			}
			for name, value := range literal.Fields {
				pos := value.Position()
				if pos.File != file || pos.Line != line {
					continue
				}
				start := nameColumn(lines, line, 1, name)
				end := start + len([]rune(name))
				if column < start || column >= end || start >= pos.Col {
					continue
				}
				// A name, then its colon
				rest := strings.TrimLeft(string([]rune(lines[line-1])[end-1:]), " ")
				if strings.HasPrefix(rest, ":") {
					owner, found, at = literal.Name, name, start
				}
			}
		})
	})
	return owner, found, at
}

// eachPattern calls visit with every pattern that takes a variant apart
// under a node, `case Shape.Rect(w, h)` and `value is Shape.Rect(w, h)`,
// those inside others included.
func eachPattern(node ast.Node, visit func(*ast.VariantPattern)) {
	var walk func(pattern ast.Node)
	walk = func(pattern ast.Node) {
		variant, ok := pattern.(*ast.VariantPattern)
		if !ok || variant == nil {
			return
		}
		visit(variant)
		for _, arg := range variant.Args {
			walk(arg)
		}
	}
	types.WalkStmts(node, func(stmt ast.Stmt) {
		if match, ok := stmt.(*ast.MatchStmt); ok {
			for _, matchCase := range match.Cases {
				for _, pattern := range matchCase.Patterns() {
					walk(pattern)
				}
			}
		}
	})
	// And the patterns of tests: value is Shape.Rect(w, h)
	types.WalkExprs(node, func(expr ast.Expr) {
		if test, ok := expr.(*ast.PatternTestExpr); ok {
			walk(test.Pattern)
		}
	})
}

// eachBody calls visit with every piece of a program that holds
// expressions: the bodies of functions and methods, the values of
// constants and of fields.
func eachBody(prog *ast.Program, visit func(ast.Node)) {
	for _, decl := range prog.Decls {
		switch d := decl.(type) {
		case *ast.FnDecl:
			if d.Body != nil {
				visit(d.Body)
			}
		case *ast.ImplDecl:
			for _, method := range d.Methods {
				if method.Body != nil {
					visit(method.Body)
				}
			}
		case *ast.TraitDecl:
			for _, method := range d.Methods {
				if method.Body != nil {
					visit(method.Body)
				}
			}
		case *ast.ConstDecl:
			if d.Value != nil {
				visit(d.Value)
			}
		case *ast.StructDecl:
			for _, field := range d.Fields {
				if field.Default != nil {
					visit(field.Default)
				}
			}
		}
	}
}

// --- Import lines ---

// importAt describes what is under the cursor on an import line: the
// module, with the comment its file starts with, or one of the names
// taken from it. It reports whether there was something there.
func importAt(prog *ast.Program, path string, lines []string, line, column int, d *Description) bool {
	if line < 1 || line > len(lines) {
		return false
	}
	text := lines[line-1]
	imports := importsIn(text)
	if len(imports) != 1 {
		return false
	}
	imp := imports[0]
	runes := []rune(text)
	// The word under the cursor, and where the module's path lies
	start := column - 1
	if start < 0 || start >= len(runes) || !isNameRune(runes[start]) {
		return false
	}
	for start > 0 && isNameRune(runes[start-1]) {
		start--
	}
	end := start
	for end < len(runes) && isNameRune(runes[end]) {
		end++
	}
	word := string(runes[start:end])
	pathStart := strings.Index(text, imp.Path)
	if pathStart < 0 {
		return false
	}
	pathStart = len([]rune(text[:pathStart]))
	pathEnd := pathStart + len([]rune(imp.Path))

	file := moduleFile(path, imp.Path)
	switch {
	case start >= pathStart && end <= pathEnd:
		d.StartColumn, d.EndColumn = pathStart+1, pathEnd+1
		d.Signature = "module " + imp.Path
		if file == "" {
			return true
		}
		source := sourceOf(file, path, strings.Join(lines, "\n"))
		d.File, d.Line, d.Column = absolute(file), 1, 1
		d.Doc = docAtTop(source)
		return true
	case start >= pathEnd:
		for _, name := range imp.Names {
			if name != word {
				continue
			}
			node, signature := declarationNamed(prog, imp.Path+"."+name)
			if node == nil {
				return false
			}
			pos := node.Position()
			source := sourceOf(pos.File, path, strings.Join(lines, "\n"))
			d.StartColumn, d.EndColumn, d.Signature = start+1, end+1, signature
			d.File, d.Line = absolute(pos.File), pos.Line
			d.Column = nameColumn(source, pos.Line, pos.Col, name)
			d.Doc = docAbove(source, pos.Line)
			return true
		}
	}
	return false
}

// moduleFile gives the file a module path names, as the loader finds it:
// in the library, in a package of the project, or under the program's
// root. "" if there is none.
func moduleFile(path, module string) string {
	parts := strings.Split(module, ".")
	if parts[0] == stdlib.Package {
		file := stdlib.Root + "/" + strings.Join(parts[1:], "/") + ".qi"
		if _, err := stdlib.Read(file); err != nil {
			return ""
		}
		return file
	}
	opts := options(path)
	dir := opts.Root
	if dir == "" {
		dir = filepath.Dir(path)
	}
	if packaged, ok := opts.Packages[parts[0]]; ok {
		dir, parts = packaged, parts[1:]
	}
	file := filepath.Join(append([]string{dir}, parts...)...) + ".qi"
	if _, known := overlays[absolute(file)]; known {
		return file
	}
	if _, err := os.Stat(file); err != nil {
		return ""
	}
	return file
}

// declarationNamed finds what a module declares under a name, given in
// full: std.task.Task. It gives the declaration and how it reads.
func declarationNamed(prog *ast.Program, name string) (ast.Node, string) {
	if node, text, _ := typeDeclaration(prog, name); node != nil {
		return node, text
	}
	for _, decl := range prog.Decls {
		switch d := decl.(type) {
		case *ast.FnDecl:
			if d.Name == name {
				return d, signature(d, displayName(name))
			}
		case *ast.ConstDecl:
			if d.Name == name {
				text := "const " + displayName(name)
				if typ := typeText(d.Type); typ != "" {
					text += ": " + typ
				}
				return d, text
			}
		}
	}
	for _, extern := range prog.Externs {
		if extern.Name == name {
			return extern, signature(extern, displayName(name))
		}
	}
	return nil, ""
}

// docAtTop gives the comment a file starts with: what a module says of
// itself.
func docAtTop(lines []string) string {
	var doc []string
	for _, line := range lines {
		text := strings.TrimSpace(line)
		if !strings.HasPrefix(text, "//") {
			break
		}
		doc = append(doc, strings.TrimPrefix(strings.TrimPrefix(text, "//"), " "))
	}
	return strings.Join(doc, "\n")
}

// --- Types where they are written ---

// namedType is the name of a type written at a place: the Shape of
// `shape: Shape`, the Int of `[Int]`.
type namedType struct {
	name       string // As the program's declarations are named: std.http.Request
	start, end int
}

// typeAt finds the name of a type at a line and column of a file, among
// the types written in declarations and in the bodies of functions.
func typeAt(prog *ast.Program, file string, lines []string, line, column int) *namedType {
	var best *namedType
	// header looks for a name written on the line of a declaration that
	// keeps it as a text: the struct and the trait of an impl
	header := func(node ast.Node, name string) {
		pos := node.Position()
		if pos.File != file || pos.Line != line || name == "" {
			return
		}
		short := lastPart(name)
		start := nameColumn(lines, line, pos.Col, short)
		if end := start + len([]rune(short)); column >= start && column < end && start > pos.Col {
			best = &namedType{name: name, start: start, end: end}
		}
	}
	var visit func(t *ast.Type)
	visit = func(t *ast.Type) {
		if t == nil {
			return
		}
		if t.Kind == ast.TypeBasic && t.Name != "" {
			tok := t.Token()
			start, end := tok.Pos.Col, tok.Pos.Col+len([]rune(tok.Literal))
			if tok.Pos.File == file && tok.Pos.Line == line && column >= start && column < end {
				best = &namedType{name: t.Name, start: start, end: end}
			}
		}
		for _, sub := range t.Generics {
			visit(sub)
		}
		visit(t.ElemType)
		for _, sub := range t.UnionTypes {
			visit(sub)
		}
		for _, sub := range t.ParamTypes {
			visit(sub)
		}
		visit(t.ReturnType)
	}
	function := func(fn *ast.FnDecl) {
		for _, param := range fn.Params {
			visit(param.Type)
		}
		visit(fn.ReturnType)
		for _, bounds := range fn.Bounds {
			for _, bound := range bounds {
				visit(bound)
			}
		}
	}
	for _, decl := range prog.Decls {
		switch d := decl.(type) {
		case *ast.FnDecl:
			function(d)
		case *ast.ConstDecl:
			visit(d.Type)
		case *ast.StructDecl:
			for _, field := range d.Fields {
				visit(field.Type)
			}
		case *ast.EnumDecl:
			for _, variant := range d.Variants {
				for _, value := range variant.Values {
					visit(value)
				}
			}
		case *ast.TraitDecl:
			for _, method := range d.Methods {
				function(method)
			}
		case *ast.ImplDecl:
			header(d, d.StructName)
			header(d, d.TraitName)
			for _, arg := range d.TraitArgs {
				visit(arg)
			}
			for _, method := range d.Methods {
				if method.FromTrait == "" {
					function(method)
				}
			}
		}
	}
	for _, extern := range prog.Externs {
		function(extern)
	}
	eachBody(prog, func(node ast.Node) {
		types.WalkStmts(node, func(stmt ast.Stmt) {
			if let, ok := stmt.(*ast.LetStmt); ok {
				visit(let.Type)
			}
		})
		types.WalkExprs(node, func(expr ast.Expr) {
			switch e := expr.(type) {
			case *ast.FnExpr:
				for _, param := range e.Params {
					visit(param.Type)
				}
				visit(e.ReturnType)
			case *ast.CastExpr:
				visit(e.Type)
			case *ast.TypeTestExpr:
				visit(e.Type)
			case *ast.StructInitExpr:
				for _, arg := range e.TypeArgs {
					visit(arg)
				}
			case *ast.Ident:
				for _, arg := range e.TypeArgs {
					visit(arg)
				}
			case *ast.MemberExpr:
				for _, arg := range e.TypeArgs {
					visit(arg)
				}
			}
		})
	})
	return best
}

// What the types that come with the language are.
var builtinTypes = map[string]string{
	"Int":    "A whole number, from -140737488355328 to 140737488355327.",
	"Float":  "A number with decimals.",
	"String": "A text.",
	"Bool":   "true or false.",
	"Void":   "Nothing: what a function returns when it returns no value.",
	"Any":    "A value of any type. Test it with `is`, or cast it with `as`, to use it.",
	"Self":   "The type the trait is implemented for.",
}

// typeDeclaration finds what a type's name stands for: the struct, enum
// or trait declared under it, with how it reads. For a type that comes
// with the language there is no declaration, only a word about it; any
// other name is a type parameter.
func typeDeclaration(prog *ast.Program, name string) (ast.Node, string, string) {
	for _, decl := range prog.Decls {
		switch d := decl.(type) {
		case *ast.StructDecl:
			if d.Name == name {
				return d, structText(d), ""
			}
		case *ast.EnumDecl:
			if d.Name == name {
				return d, enumText(d), ""
			}
		case *ast.TraitDecl:
			if d.Name == name {
				return d, traitText(d), ""
			}
		}
	}
	if doc, ok := builtinTypes[name]; ok {
		return nil, name, doc
	}
	return nil, "(type parameter) " + name, ""
}

func traitText(d *ast.TraitDecl) string {
	methods := make([]string, 0, len(d.Methods))
	for _, method := range d.Methods {
		methods = append(methods, "    "+signature(method, method.Name)+";")
	}
	head := "trait " + displayName(d.Name) + typeParams(d.TypeParams)
	if len(methods) == 0 {
		return head + " { }"
	}
	return head + " {\n" + strings.Join(methods, "\n") + "\n}"
}

// --- Names where they are declared ---

// declaration is a name at the place that declares it.
type declaration struct {
	node       ast.Node
	name       string
	signature  string
	start, end int
}

// declaredAt finds the name declared at a line and column of a file: of
// a function, a type, a constant, a field, a variant, a method, a
// parameter or a variable.
func declaredAt(analysis *compiler.Analysis, file string, lines []string, line, column int) *declaration {
	var best *declaration
	consider := func(node ast.Node, name, text string) {
		pos := node.Position()
		if pos.File != file || pos.Line != line || name == "" {
			return
		}
		start := nameColumn(lines, line, pos.Col, name)
		end := start + len([]rune(name))
		if column >= start && column < end {
			best = &declaration{node: node, name: name, signature: text, start: start, end: end}
		}
	}
	typeOf := func(expr ast.Expr) string {
		if analysis.Info == nil || expr == nil || analysis.Info.Types[expr] == nil {
			return "?"
		}
		return typeName(analysis.Info.Types[expr])
	}
	function := func(fn *ast.FnDecl, shown string) {
		consider(fn, fn.Name, signature(fn, shown))
		for _, param := range fn.Params {
			consider(param, param.Name, "(parameter) "+paramText(param))
		}
	}
	for _, decl := range analysis.Program.Decls {
		switch d := decl.(type) {
		case *ast.FnDecl:
			function(d, d.Name)
		case *ast.ConstDecl:
			text := "const " + d.Name
			if typ := typeText(d.Type); typ != "" {
				text += ": " + typ
			} else if typ := typeOf(d.Value); typ != "?" {
				text += ": " + typ
			}
			consider(d, d.Name, text)
		case *ast.StructDecl:
			consider(d, d.Name, structText(d))
			for _, field := range d.Fields {
				consider(field, field.Name, "(field) "+fieldPrefix(field)+d.Name+"."+field.Name+": "+typeText(field.Type))
			}
		case *ast.EnumDecl:
			consider(d, d.Name, enumText(d))
			for _, variant := range d.Variants {
				consider(variant, variant.Name, "(variant) "+d.Name+"."+variantDetail(variant))
			}
		case *ast.TraitDecl:
			consider(d, d.Name, traitText(d))
			for _, method := range d.Methods {
				function(method, d.Name+"."+method.Name)
			}
		case *ast.ImplDecl:
			for _, method := range d.Methods {
				if method.FromTrait == "" {
					function(method, d.StructName+"."+method.Name)
				}
			}
		}
	}
	for _, extern := range analysis.Program.Externs {
		function(extern, extern.Name)
	}
	// The type of a variable that is not declared with one is the type
	// the checker gave its uses
	typeOfUses := func(decl ast.Node) string {
		if analysis.Info == nil || analysis.Table == nil {
			return "?"
		}
		for ident, sym := range analysis.Table.Defs {
			if sym != nil && sym.Decl == decl && analysis.Info.Types[ident] != nil {
				return typeName(analysis.Info.Types[ident])
			}
		}
		return "?"
	}
	variable := func(node ast.Node, name, what string) {
		if pos := node.Position(); pos.File != file || pos.Line != line {
			return
		}
		consider(node, name, "("+what+") "+name+": "+typeOfUses(node))
	}
	eachBody(analysis.Program, func(node ast.Node) {
		types.WalkExprs(node, func(expr ast.Expr) {
			if fn, ok := expr.(*ast.FnExpr); ok {
				for _, param := range fn.Params {
					if param.Type != nil {
						consider(param, param.Name, "(parameter) "+paramText(param))
					} else {
						variable(param, param.Name, "parameter")
					}
				}
			}
		})
		eachPattern(node, func(pattern *ast.VariantPattern) {
			for _, arg := range pattern.Args {
				if binding, ok := arg.(*ast.BindingPattern); ok && !binding.Ignored() {
					variable(binding, binding.Name, "variable")
				}
			}
		})
		types.WalkStmts(node, func(stmt ast.Stmt) {
			if loop, ok := stmt.(*ast.ForStmt); ok {
				variable(loop, loop.Var, "variable")
				if loop.Index != nil {
					variable(loop.Index, loop.Index.Name, "variable")
				}
			}
			let, ok := stmt.(*ast.LetStmt)
			if !ok {
				return
			}
			keyword := "let"
			if let.IsConst {
				keyword = "const"
			}
			typ := typeText(let.Type)
			if typ == "" {
				typ = typeOf(let.Value)
			}
			consider(let, let.Name, keyword+" "+let.Name+": "+typ)
		})
	})
	return best
}

// member finds a field, a method or a variant of a struct or an enum by
// name: its declaration and how it reads.
func member(prog *ast.Program, owner, name string) (ast.Node, string) {
	shown := displayName(owner)
	for _, decl := range prog.Decls {
		switch d := decl.(type) {
		case *ast.StructDecl:
			if d.Name != owner {
				continue
			}
			for _, field := range d.Fields {
				if field.Name == name {
					return field, "(field) " + fieldPrefix(field) + shown + "." + name + ": " + typeText(field.Type)
				}
			}
		case *ast.EnumDecl:
			if d.Name != owner {
				continue
			}
			for _, variant := range d.Variants {
				if variant.Name == name {
					return variant, "(variant) " + shown + "." + variantDetail(variant)
				}
			}
		case *ast.ImplDecl:
			if !owns(d.StructName, owner) {
				continue
			}
			for _, method := range d.Methods {
				if method.Name == name {
					if _, arrays := ast.IsArrayImpl(d.StructName); arrays {
						return method, signature(method, name)
					}
					return method, signature(method, shown+"."+name)
				}
			}
		}
	}
	return nil, ""
}

// --- What could be written ---

var keywords = []string{
	"fn", "let", "const", "return", "if", "else", "while", "for", "in", "break", "continue", "defer",
	"match", "case", "default", "struct", "enum", "trait", "impl", "import", "pub", "extern",
	"true", "false", "null", "self", "as", "is",
}

// Complete lists what could be written at a line and column of the file
// at path. After `name.` it is what that name has: the public names of a
// module, the fields and methods of a value, the variants of an enum.
// Anywhere else it is everything in reach.
func Complete(path, src string, line, column int) []Completion {
	lock.Lock()
	defer lock.Unlock()

	lines := strings.Split(src, "\n")
	if line < 1 || line > len(lines) {
		return nil
	}
	before := []rune(lines[line-1])
	if column-1 < len(before) {
		before = before[:max(column-1, 0)]
	}
	if found, importing := importCompletions(path, string(before)); importing {
		return found
	}
	holder, afterDot := holderBefore(string(before))
	// After a dot that follows more than a name, `json.object().text("a", "b").`,
	// or a name that is not one of the cases below: what the expression
	// before the dot is, asked of the checker
	chain, chained := chainBefore(src, line, column)

	// A line being typed seldom reads as a program. What it needs is
	// around it: the same text without that line.
	analysis := analyze(path, src)
	if caseHead := caseHeadOf(string(before)); analysis.Info == nil && caseHead != "" {
		// The line of a case keeps its pattern, whose names are what is
		// being typed with: `case Shape.Circle(r) if r.`
		kept := append([]string{}, lines...)
		kept[line-1] = strings.TrimSuffix(caseHead, ":") + `: { panic(""); }`
		if with := analyze(path, strings.Join(kept, "\n")); with.Info != nil {
			analysis = with
		}
	}
	if analysis.Info == nil {
		blank := append([]string{}, lines...)
		blank[line-1] = ""
		analysis = analyze(path, strings.Join(blank, "\n"))
	}
	if analysis.Program == nil {
		return nil
	}

	var out []Completion
	seen := map[string]bool{}
	add := func(label, kind, detail string) {
		if label == "" || seen[label] || strings.HasPrefix(label, "__") {
			return
		}
		seen[label] = true
		out = append(out, Completion{Label: label, Kind: kind, Detail: detail})
	}
	prog := analysis.Program
	locals := localsAt(analysis, path, line)

	if afterDot {
		if module := moduleOf(src, holder); module != "" {
			declarations(prog, module, add)
			return out
		}
		owner := holder
		if typ, ok := locals[holder]; ok {
			owner = ownerName(typ)
		}
		members(prog, owner, add)
		if len(out) > 0 {
			return out
		}
	}
	if chained {
		if typ, read := probe(path, src, chain); typ != nil {
			// The members belong to the program the expression was read in
			members(read, ownerName(typ), add)
		}
		return out
	}

	for name, typ := range locals {
		detail := ""
		if typ != nil {
			detail = typeName(typ)
		}
		add(name, "variable", detail)
	}
	declarations(prog, "", add)
	for _, imp := range importsIn(src) {
		if len(imp.Names) == 0 {
			add(imp.Alias, "module", imp.Path)
		}
		for _, name := range imp.Names {
			add(name, "function", imp.Path)
		}
	}
	for _, builtin := range []string{"print", "println", "toString", "panic"} {
		add(builtin, "function", "builtin")
	}
	for _, keyword := range keywords {
		add(keyword, "keyword", "")
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

// importCompletions lists what could come next on an import line, and
// says whether the cursor is on one. After `import ` and after each dot
// it is the modules there: those of the library under std, the files and
// folders of the program, the packages its project names. Between the
// braces of `import module.{` it is what that module makes public.
func importCompletions(path, before string) ([]Completion, bool) {
	text := strings.TrimLeft(before, " \t")
	text = strings.TrimPrefix(text, "pub ")
	if !strings.HasPrefix(text, "import ") {
		return nil, false
	}
	text = strings.TrimLeft(strings.TrimPrefix(text, "import "), " ")
	if strings.ContainsAny(text, ";") || strings.Contains(text, " as ") {
		return nil, true // The path is written: nothing more to suggest
	}
	out := []Completion{}

	if module, _, inBraces := strings.Cut(text, ".{"); inBraces {
		// The module is read on its own: the file being typed may not load
		probe := "import " + module + ";\nfn main() -> Int { return 0; }\n"
		analysis := compiler.Analyze(probe, path, options(path), errors.NewHandler())
		if analysis.Program != nil {
			seen := map[string]bool{}
			declarations(analysis.Program, module, func(label, kind, detail string) {
				if !seen[label] {
					seen[label] = true
					out = append(out, Completion{Label: label, Kind: kind, Detail: detail})
				}
			})
		}
		return out, true
	}

	// What is written so far, up to the last dot, is the folder to look in
	parent := ""
	if dot := strings.LastIndex(text, "."); dot >= 0 {
		parent = text[:dot]
	}
	seen := map[string]bool{}
	add := func(name, detail string) {
		if name != "" && !seen[name] && isName(name) {
			seen[name] = true
			out = append(out, Completion{Label: name, Kind: "module", Detail: detail})
		}
	}
	opts := options(path)
	root := opts.Root
	if root == "" {
		root = filepath.Dir(path)
	}
	parts := []string{}
	if parent != "" {
		parts = strings.Split(parent, ".")
	}

	if len(parts) == 0 {
		add(stdlib.Package, "the standard library")
		for name := range opts.Packages {
			add(name, "package")
		}
	}
	if len(parts) > 0 && parts[0] == stdlib.Package {
		if len(parts) == 1 {
			for _, module := range stdlib.Modules() {
				add(strings.TrimPrefix(module, stdlib.Package+"."), module)
			}
		}
		sort.SliceStable(out, func(i, j int) bool { return out[i].Label < out[j].Label })
		return out, true
	}
	// A package's modules are in its own folder; the program's, under its root
	dir := filepath.Join(append([]string{root}, parts...)...)
	if len(parts) > 0 {
		if packaged, ok := opts.Packages[parts[0]]; ok {
			dir = filepath.Join(append([]string{packaged}, parts[1:]...)...)
		}
	}
	entries, _ := os.ReadDir(dir)
	self := strings.TrimSuffix(filepath.Base(path), ".qi")
	for _, entry := range entries {
		name := entry.Name()
		switch {
		case entry.IsDir():
			if hasModules(filepath.Join(dir, name)) {
				add(name, "folder")
			}
		case strings.HasSuffix(name, ".qi"):
			module := strings.TrimSuffix(name, ".qi")
			if filepath.Join(dir, name) != path && !(len(parts) == 0 && module == self) {
				add(module, strings.Join(append(parts, module), "."))
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out, true
}

// hasModules reports whether a folder holds a .qi file, itself or in a
// folder under it, two levels down at most: enough to tell a folder of
// modules from one of anything else.
func hasModules(dir string) bool {
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".qi") {
			return true
		}
	}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		inner, _ := os.ReadDir(filepath.Join(dir, entry.Name()))
		for _, file := range inner {
			if !file.IsDir() && strings.HasSuffix(file.Name(), ".qi") {
				return true
			}
		}
	}
	return false
}

// isName reports whether a text can be a part of a module path.
func isName(text string) bool {
	for i, r := range text {
		if !isNameRune(r) || (i == 0 && r >= '0' && r <= '9') {
			return false
		}
	}
	return text != ""
}

// --- The value before a dot ---

// expression is a piece of a source text that reads as a value: what
// stands before the dot the cursor is after.
type expression struct {
	text  string
	start int // Where it starts in the text, in characters
	line  int // The line the cursor is on, from 1
}

// chainBefore finds the expression before the dot the cursor follows:
// names, calls, indexes and dots as far back as they go, over several
// lines when a chain of calls is written one call per line. It reports
// whether the cursor follows a dot at all.
func chainBefore(src string, line, column int) (expression, bool) {
	runes := []rune(src)
	// Where the cursor is in the text
	at, current := 0, 1
	for at < len(runes) && current < line {
		if runes[at] == '\n' {
			current++
		}
		at++
	}
	at = min(at+max(column-1, 0), len(runes))

	// Back over the name being typed, to the dot
	end := at
	for end > 0 && isNameRune(runes[end-1]) {
		end--
	}
	if end == 0 || runes[end-1] != '.' {
		return expression{}, false
	}
	end--
	if end > 0 && runes[end-1] == '?' {
		end-- // value?.name
	}

	i := end
	afterDot := true // Whether what was read last, to the right, is a dot
	for i > 0 {
		r := runes[i-1]
		switch {
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			// A chain goes on over a line end only before a dot
			if !afterDot {
				goto done
			}
			j := i - 1
			for j > 0 && (runes[j-1] == ' ' || runes[j-1] == '\t' || runes[j-1] == '\n' || runes[j-1] == '\r') {
				j--
			}
			if j == 0 || !(runes[j-1] == ')' || runes[j-1] == ']' || isNameRune(runes[j-1])) {
				goto done
			}
			i = j
		case r == ')' || r == ']':
			open := matching(runes, i-1)
			if open < 0 {
				goto done
			}
			i = open
			afterDot = false
			// The type arguments of a call: list.of<String>(...)
			if r == ')' && i > 0 && runes[i-1] == '>' {
				if less := typeArguments(runes, i-1); less >= 0 {
					i = less
				}
			}
		case isNameRune(r):
			if !afterDot && i < end && isNameRune(runes[i]) {
				goto done
			}
			for i > 0 && isNameRune(runes[i-1]) {
				i--
			}
			afterDot = false
		case r == '.':
			if afterDot {
				goto done
			}
			i--
			if i > 0 && runes[i-1] == '?' {
				i--
			}
			afterDot = true
		default:
			goto done
		}
	}
done:
	for i < end && (runes[i] == ' ' || runes[i] == '\t' || runes[i] == '\n' || runes[i] == '\r' || runes[i] == '.') {
		i++
	}
	text := strings.TrimSpace(string(runes[i:end]))
	if text == "" {
		return expression{}, false
	}
	return expression{text: text, start: i, line: line}, true
}

// matching finds the bracket that opens the one closed at a position,
// reading backwards past texts and nested brackets. -1 if there is none.
func matching(runes []rune, closeAt int) int {
	depth := 0
	for i := closeAt; i >= 0; i-- {
		switch runes[i] {
		case '"':
			// Back to the quote that opens the text
			i--
			for i >= 0 && !(runes[i] == '"' && (i == 0 || runes[i-1] != '\\')) {
				i--
			}
		case ')', ']', '}':
			depth++
		case '(', '[', '{':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// typeArguments finds the `<` that opens the type arguments closed at a
// position, if what lies between reads as types. -1 if it does not.
func typeArguments(runes []rune, closeAt int) int {
	depth := 0
	for i := closeAt; i >= 0; i-- {
		r := runes[i]
		switch {
		case r == '>':
			depth++
		case r == '<':
			depth--
			if depth == 0 {
				return i
			}
		case isNameRune(r) || r == ',' || r == ' ' || r == '.' || r == '[' || r == ']' || r == '?' || r == '(' || r == ')' || r == '-':
		default:
			return -1
		}
	}
	return -1
}

// probe asks the checker what type an expression of a file has. The
// statement being typed seldom reads as a program, and what follows it in
// its function may hang on it. So the question is put to another text:
// the file up to the statement the expression is in, the expression given
// a name, the brackets open at that point closed, then the declarations
// that follow the function. It gives the type and the program it was
// read in; nil if that does not read either.
func probe(path, src string, e expression) (types.Type, *ast.Program) {
	runes := []rune(src)

	// Where the statement the expression is part of starts: after the
	// `;`, `{` or `}` before it that is not inside brackets of its own
	start, depth := e.start, 0
	for start > 0 {
		r := runes[start-1]
		if r == '"' {
			start--
			for start > 0 && !(runes[start-1] == '"' && (start < 2 || runes[start-2] != '\\')) {
				start--
			}
			start = max(start-1, 0)
			continue
		}
		if r == ')' || r == ']' {
			depth++
		} else if (r == '(' || r == '[') && depth > 0 {
			depth--
		} else if depth == 0 && (r == ';' || r == '{' || r == '}') {
			break
		}
		start--
	}
	head := string(runes[:start])

	// What follows the function: the next lines that start a declaration
	tail := ""
	lines := strings.Split(src, "\n")
	for i := e.line; i < len(lines); i++ {
		if first := []rune(lines[i]); len(first) > 0 && ((first[0] >= 'a' && first[0] <= 'z') || first[0] == '/') {
			tail = strings.Join(lines[i:], "\n")
			break
		}
	}

	var text strings.Builder
	text.WriteString(head)
	open, at := openBracketsAt(head)
	inMatch := len(open) > 0 && open[len(open)-1] == '{' && matchBefore(runes, at[len(open)-1]) >= 0

	// In a case written as an expression, `case Shape.At(place): place.`,
	// the statement is the case: the question is asked in a block of that
	// case, where the names of its pattern exist
	question := "\nlet " + probeName + " = " + e.text + ";\n"
	if caseHead := caseHeadOf(string(runes[start:e.start])); inMatch && caseHead != "" {
		caseHead = strings.TrimSuffix(caseHead, ":") + ":"
		question = "\n" + caseHead + " {" + question + "}\n"
	}
	text.WriteString(question)

	for i := len(open) - 1; i >= 0; i-- {
		switch open[i] {
		case '{':
			text.WriteString("}")
			// A match that gives a value is part of a statement, which
			// ends after it: let word = match n { ... };
			if m := matchBefore(runes, at[i]); m >= 0 && !startsStatement(runes, m) && (i == 0 || open[i-1] == '{') {
				text.WriteString(";")
			}
		case '(':
			text.WriteString(")")
		case '[':
			text.WriteString("]")
		}
		// A call or an index that held the statement ends its own
		if open[i] != '{' && (i == 0 || open[i-1] == '{') {
			text.WriteString(";")
		}
	}
	text.WriteString("\n" + tail)

	analysis := analyze(path, text.String())
	if analysis.Info == nil || analysis.Program == nil {
		return nil, nil
	}
	var found types.Type
	eachBody(analysis.Program, func(node ast.Node) {
		types.WalkStmts(node, func(stmt ast.Stmt) {
			if let, ok := stmt.(*ast.LetStmt); ok && let.Name == probeName && let.Position().File == path {
				found = analysis.Info.Types[let.Value]
			}
		})
	})
	return found, analysis.Program
}

// probeName is the variable the expression asked about is given to.
const probeName = "__asked"

// matchBefore finds the `match` a brace opens the cases of: the position
// of the word, or -1 when the brace at the given position opens something
// else.
func matchBefore(runes []rune, brace int) int {
	for i := brace - 1; i >= 4; i-- {
		switch runes[i] {
		case '{', '}', ';':
			return -1
		}
		if string(runes[i-4:i+1]) == "match" && (i-5 < 0 || !isNameRune(runes[i-5])) && !isNameRune(runes[i+1]) {
			return i - 4
		}
	}
	return -1
}

// startsStatement reports whether what is at a position is the first
// thing of its statement: only space separates it from the `;` or the
// brace before it.
func startsStatement(runes []rune, at int) bool {
	for i := at - 1; i >= 0; i-- {
		switch runes[i] {
		case ' ', '\t', '\n', '\r':
			continue
		case ';', '{', '}':
			return true
		}
		return false
	}
	return true
}

// caseHeadOf gives the `case pattern:` or `default:` a text starts with,
// colon included, or "" when it starts with something else.
func caseHeadOf(text string) string {
	trimmed := strings.TrimLeft(text, " \t\r\n")
	if !strings.HasPrefix(trimmed, "case ") && !strings.HasPrefix(trimmed, "default:") {
		return ""
	}
	depth, quoted := 0, false
	for i, r := range trimmed {
		switch {
		case r == '"' && (i == 0 || trimmed[i-1] != '\\'):
			quoted = !quoted
		case quoted:
		case r == '(' || r == '[':
			depth++
		case r == ')' || r == ']':
			depth--
		case r == ':' && depth == 0:
			return trimmed[:i+1]
		}
	}
	// The colon is still to come: a condition is being typed
	if at := strings.Index(trimmed, " if "); at > 0 {
		return trimmed[:at]
	}
	return ""
}

// openBrackets lists the brackets a text opens and does not close, in
// order, leaving aside what texts and comments hold.
func openBrackets(text string) []rune {
	open, _ := openBracketsAt(text)
	return open
}

// openBracketsAt is openBrackets with the position of each bracket.
func openBracketsAt(text string) ([]rune, []int) {
	var open []rune
	var at []int
	runes := []rune(text)
	for i := 0; i < len(runes); i++ {
		switch r := runes[i]; r {
		case '/':
			if i+1 < len(runes) && runes[i+1] == '/' {
				for i < len(runes) && runes[i] != '\n' {
					i++
				}
			}
		case '"':
			long := i+2 < len(runes) && runes[i+1] == '"' && runes[i+2] == '"'
			if long {
				i += 3
				for i+2 < len(runes) && !(runes[i] == '"' && runes[i+1] == '"' && runes[i+2] == '"') {
					i++
				}
				i += 2
				break
			}
			i++
			for i < len(runes) && runes[i] != '"' && runes[i] != '\n' {
				if runes[i] == '\\' {
					i++
				}
				i++
			}
		case '(', '[', '{':
			open = append(open, r)
			at = append(at, i)
		case ')', ']', '}':
			if len(open) > 0 {
				open = open[:len(open)-1]
				at = at[:len(at)-1]
			}
		}
	}
	return open, at
}

// holderBefore looks at the text before the cursor: for `shape.ar` it
// gives "shape" and true, the name whose members are wanted.
func holderBefore(text string) (string, bool) {
	runes := []rune(text)
	i := len(runes)
	for i > 0 && isNameRune(runes[i-1]) {
		i--
	}
	if i == 0 || runes[i-1] != '.' {
		return "", false
	}
	end := i - 1
	start := end
	for start > 0 && isNameRune(runes[start-1]) {
		start--
	}
	return string(runes[start:end]), start < end
}

func isNameRune(r rune) bool {
	return r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

// imported is one import line of a file.
type imported struct {
	Path  string   // The module, dotted: "std.math"
	Alias string   // The name the file calls it by
	Names []string // Names taken from it directly, if any
}

// importsIn reads the import lines of a source text. The loader folds
// the modules into one program and keeps no trace of who imported what,
// and a file being edited may not load at all: the lines are read as
// written.
func importsIn(src string) []imported {
	var out []imported
	for _, line := range strings.Split(src, "\n") {
		text := strings.TrimSpace(line)
		text = strings.TrimSpace(strings.TrimPrefix(text, "pub "))
		if !strings.HasPrefix(text, "import ") {
			continue
		}
		text = strings.TrimSpace(strings.TrimPrefix(text, "import "))
		if end := strings.Index(text, ";"); end >= 0 {
			text = text[:end]
		}
		one := imported{}
		if open := strings.Index(text, ".{"); open >= 0 {
			for _, name := range strings.Split(strings.TrimSuffix(text[open+2:], "}"), ",") {
				if name = strings.TrimSpace(name); name != "" {
					one.Names = append(one.Names, name)
				}
			}
			text = text[:open]
		}
		path, alias, renamed := strings.Cut(text, " as ")
		one.Path = strings.TrimSpace(path)
		one.Alias = lastPart(one.Path)
		if renamed {
			one.Alias = strings.TrimSpace(alias)
		}
		if one.Path != "" {
			out = append(out, one)
		}
	}
	return out
}

// moduleOf gives the path of the module a file calls by a name, or "".
func moduleOf(src, name string) string {
	for _, imp := range importsIn(src) {
		if len(imp.Names) == 0 && imp.Alias == name {
			return imp.Path
		}
	}
	return ""
}

// declarations lists what a module declares for others to use; with ""
// for the module, what the entry file declares for itself.
func declarations(prog *ast.Program, module string, add func(label, kind, detail string)) {
	prefix := ""
	if module != "" {
		prefix = module + "."
	}
	local := func(name string, pub bool) (string, bool) {
		if module == "" {
			return name, !strings.Contains(name, ".")
		}
		rest := strings.TrimPrefix(name, prefix)
		return rest, pub && rest != name && !strings.Contains(rest, ".")
	}
	for _, decl := range prog.Decls {
		switch d := decl.(type) {
		case *ast.FnDecl:
			if name, ok := local(d.Name, d.Pub); ok {
				add(name, "function", signature(d, name))
			}
		case *ast.ConstDecl:
			if name, ok := local(d.Name, d.Pub); ok {
				add(name, "constant", typeText(d.Type))
			}
		case *ast.StructDecl:
			if name, ok := local(d.Name, d.Pub); ok {
				add(name, "struct", "")
			}
		case *ast.EnumDecl:
			if name, ok := local(d.Name, d.Pub); ok {
				add(name, "enum", "")
			}
		case *ast.TraitDecl:
			if name, ok := local(d.Name, d.Pub); ok {
				add(name, "trait", "")
			}
		}
	}
	for _, extern := range prog.Externs {
		if name, ok := local(extern.Name, extern.Pub); ok {
			add(name, "function", signature(extern, name))
		}
	}
}

// members lists the fields and methods of a struct, or the variants and
// methods of an enum, named as the program names it or in full.
func members(prog *ast.Program, owner string, add func(label, kind, detail string)) {
	if owner == "" {
		return
	}
	is := func(name string) bool { return name == owner || lastPart(name) == owner }
	for _, decl := range prog.Decls {
		switch d := decl.(type) {
		case *ast.FnDecl:
			// static fn origin() of an impl: Point::origin, written
			// Point.origin()
			if typeName, name, static := strings.Cut(d.Name, "::"); static && is(typeName) {
				if d.Module == "" || d.Pub {
					add(name, "function", signature(d, name))
				}
			}
		case *ast.StructDecl:
			if is(d.Name) {
				for _, field := range d.Fields {
					// A priv field of another module's struct is not there
					// to be written
					if field.Private && d.Module != "" {
						continue
					}
					add(field.Name, "field", typeText(field.Type))
				}
			}
		case *ast.EnumDecl:
			if is(d.Name) {
				for _, variant := range d.Variants {
					add(variant.Name, "variant", variantDetail(variant))
				}
			}
		case *ast.ImplDecl:
			if is(d.StructName) || owns(d.StructName, owner) {
				for _, method := range d.Methods {
					if (strings.Contains(d.StructName, ".") || types.IsBuiltinTypeName(d.StructName)) && method.Module != "" && !method.Pub && d.TraitName == "" {
						continue // Private to its module
					}
					if d.TraitName == types.TraitWaitable {
						continue // What `wait` is made of, not to be called
					}
					add(method.Name, "method", signature(method, method.Name))
				}
			}
		}
	}
}

// localsAt lists the variables of the function a line is in that are
// declared before it, with their types: its parameters and its `let`s.
// A type is nil when the program could not be checked: the names are
// still worth suggesting.
func localsAt(analysis *compiler.Analysis, file string, line int) map[string]types.Type {
	out := map[string]types.Type{}
	var fn *ast.FnDecl
	consider := func(candidate *ast.FnDecl) {
		pos := candidate.Position()
		if candidate.Body == nil || pos.File != file || pos.Line > line {
			return
		}
		if fn == nil || pos.Line >= fn.Position().Line {
			fn = candidate
		}
	}
	for _, decl := range analysis.Program.Decls {
		switch d := decl.(type) {
		case *ast.FnDecl:
			consider(d)
		case *ast.ImplDecl:
			for _, method := range d.Methods {
				consider(method)
			}
		}
	}
	if fn == nil {
		return out
	}
	typeOf := func(expr ast.Expr) types.Type {
		if analysis.Info == nil || expr == nil {
			return nil
		}
		return analysis.Info.Types[expr]
	}

	for _, param := range fn.Params {
		out[param.Name] = nil
	}
	types.WalkStmts(fn.Body, func(stmt ast.Stmt) {
		if let, ok := stmt.(*ast.LetStmt); ok && let.Position().Line < line {
			out[let.Name] = typeOf(let.Value)
		}
	})
	// The variables of loops and the names of patterns, used yet or not
	types.WalkStmts(fn.Body, func(stmt ast.Stmt) {
		if loop, ok := stmt.(*ast.ForStmt); ok && loop.Position().Line < line {
			if _, listed := out[loop.Var]; !listed && !strings.HasPrefix(loop.Var, "__") {
				out[loop.Var] = nil
			}
			if loop.Index != nil {
				if _, listed := out[loop.Index.Name]; !listed {
					out[loop.Index.Name] = nil
				}
			}
		}
	})
	eachPattern(fn.Body, func(pattern *ast.VariantPattern) {
		for _, arg := range pattern.Args {
			if binding, ok := arg.(*ast.BindingPattern); ok && !binding.Ignored() && binding.Position().Line <= line {
				if _, listed := out[binding.Name]; !listed {
					out[binding.Name] = nil
				}
			}
		}
	})
	if analysis.Info == nil || analysis.Table == nil {
		return out
	}
	// Parameters, and the variables of loops and of short functions, have
	// the type the checker gave their uses
	types.WalkExprs(fn.Body, func(expr ast.Expr) {
		ident, ok := expr.(*ast.Ident)
		if !ok || ident.Position().Line >= line {
			return
		}
		sym := analysis.Table.Defs[ident]
		if sym == nil || sym.Kind != resolve.SymbolVar {
			return
		}
		if _, constant := sym.Decl.(*ast.ConstDecl); constant {
			return
		}
		if known, listed := out[ident.Name]; !listed || known == nil {
			out[ident.Name] = analysis.Info.Types[ident]
		}
	})
	return out
}

// --- Writing things out ---

// signature writes a function as it is declared, without its body.
func signature(fn *ast.FnDecl, name string) string {
	var sb strings.Builder
	sb.WriteString("fn " + name)
	if len(fn.TypeParams) > 0 {
		sb.WriteString("<" + strings.Join(fn.TypeParams, ", ") + ">")
	}
	params := make([]string, 0, len(fn.Params))
	for _, p := range fn.Params {
		params = append(params, paramText(p))
	}
	sb.WriteString("(" + strings.Join(params, ", ") + ")")
	if fn.ReturnType != nil {
		if ret := typeText(fn.ReturnType); ret != "Void" && ret != "" {
			sb.WriteString(" -> " + ret)
		}
	}
	return sb.String()
}

// fieldPrefix is "priv " for a field hidden from other modules.
func fieldPrefix(field *ast.Field) string {
	if field.Private {
		return "priv "
	}
	return ""
}

func structText(d *ast.StructDecl) string {
	fields := make([]string, 0, len(d.Fields))
	for _, field := range d.Fields {
		fields = append(fields, "    "+fieldPrefix(field)+field.Name+": "+typeText(field.Type)+";")
	}
	head := "struct " + displayName(d.Name) + typeParams(d.TypeParams)
	if len(fields) == 0 {
		return head + " { }"
	}
	return head + " {\n" + strings.Join(fields, "\n") + "\n}"
}

func enumText(d *ast.EnumDecl) string {
	variants := make([]string, 0, len(d.Variants))
	for _, variant := range d.Variants {
		variants = append(variants, "    "+variantDetail(variant)+",")
	}
	return "enum " + displayName(d.Name) + typeParams(d.TypeParams) + " {\n" + strings.Join(variants, "\n") + "\n}"
}

func typeParams(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return "<" + strings.Join(names, ", ") + ">"
}

func variantDetail(v *ast.EnumVariant) string {
	if len(v.Values) == 0 {
		return v.Name
	}
	values := make([]string, 0, len(v.Values))
	for _, value := range v.Values {
		values = append(values, typeText(value))
	}
	return v.Name + "(" + strings.Join(values, ", ") + ")"
}

// typeText writes a type as the program wrote it.
func typeText(t *ast.Type) string {
	if t == nil {
		return ""
	}
	return shortNames(t.String())
}

// paramText writes a parameter as the program wrote it.
func paramText(p *ast.Param) string {
	if p.Type == nil {
		return p.Name
	}
	return p.Name + ": " + typeText(p.Type)
}

// typeName writes a type the checker worked out, the way a program
// would write it: Int, [String], Point, fn(Int) -> Bool.
func typeName(t types.Type) string {
	switch t := t.(type) {
	case nil:
		return "?"
	case *types.PrimitiveType:
		name := string(t.Name)
		if name == "" || name == "null" || name == "Self" {
			return name
		}
		return strings.ToUpper(name[:1]) + name[1:]
	case *types.StructType:
		name := displayName(t.BaseName)
		if name == "" {
			name = displayName(t.Name)
		}
		if len(t.TypeArgs) > 0 {
			args := make([]string, 0, len(t.TypeArgs))
			for _, arg := range t.TypeArgs {
				args = append(args, typeName(arg))
			}
			return name + "<" + strings.Join(args, ", ") + ">"
		}
		return name + typeParams(t.TypeParams)
	case *types.ArrayType:
		return "[" + typeName(t.ElementType) + "]"
	case *types.FunctionType:
		params := make([]string, 0, len(t.Params))
		for i, p := range t.Params {
			text := typeName(p)
			if t.IsVariadic && i == len(t.Params)-1 {
				text = "..." + text
			}
			params = append(params, text)
		}
		head := "fn" + typeParams(t.TypeParams) + "(" + strings.Join(params, ", ") + ")"
		if ret := typeName(t.ReturnType); ret != "Void" {
			return head + " -> " + ret
		}
		return head
	case *types.UnionType:
		var parts []string
		optional := false
		for _, sub := range t.Types {
			if p, ok := sub.(*types.PrimitiveType); ok && p.Name == types.PrimitiveNull {
				optional = true
				continue
			}
			parts = append(parts, typeName(sub))
		}
		if optional && len(parts) == 1 {
			return parts[0] + "?"
		}
		if optional {
			parts = append(parts, "null")
		}
		return strings.Join(parts, " | ")
	}
	return shortNames(t.String())
}

// ownerName gives the name of the struct or enum a type is, as the
// program's declarations are named, or "".
func ownerName(t types.Type) string {
	switch t := t.(type) {
	case *types.StructType:
		if t.BaseName != "" {
			return t.BaseName
		}
		return t.Name
	case *types.UnionType:
		// A value that may be null has the members of what it may be
		for _, sub := range t.Types {
			if name := ownerName(sub); name != "" {
				return name
			}
		}
	case *types.ArrayType:
		// The methods of the arrays of its items, and of every array
		return ast.ArrayImplOf(types.ImplKey(t.ElementType))
	case *types.PrimitiveType:
		// A number or a text has the methods impls give it
		return types.ImplKey(t)
	}
	return ""
}

// owns reports whether what an impl is for gives its methods to an owner:
// the same type, or every array when the owner is an array.
func owns(implFor, owner string) bool {
	if implFor == owner {
		return true
	}
	_, arrays := ast.IsArrayImpl(owner)
	return arrays && implFor == ast.ArrayImpl
}

// displayName shortens a name the loader made unique in the program to
// what the module itself calls it: std.task.Task is shown as task.Task.
func displayName(name string) string {
	parts := strings.Split(name, ".")
	if len(parts) <= 2 {
		return name
	}
	return strings.Join(parts[len(parts)-2:], ".")
}

// shortNames shortens every qualified name in a piece of source.
func shortNames(text string) string {
	var sb strings.Builder
	runes := []rune(text)
	for i := 0; i < len(runes); {
		if !isNameRune(runes[i]) {
			sb.WriteRune(runes[i])
			i++
			continue
		}
		j := i
		for j < len(runes) && (isNameRune(runes[j]) || (runes[j] == '.' && j+1 < len(runes) && isNameRune(runes[j+1]))) {
			j++
		}
		sb.WriteString(lastPart(string(runes[i:j])))
		i = j
	}
	return sb.String()
}

func lastPart(name string) string {
	return name[strings.LastIndex(name, ".")+1:]
}

// --- Source text ---

// sourceOf gives the lines of a file of the program: the one being
// looked at, one being edited, one of the library, or one on disk.
func sourceOf(file, path, src string) []string {
	text := ""
	switch {
	case file == path:
		text = src
	case stdlib.IsPath(file):
		text, _ = stdlib.Read(file)
	default:
		if overlay, ok := overlays[absolute(file)]; ok {
			text = overlay
		} else if data, err := os.ReadFile(file); err == nil {
			text = string(data)
		}
	}
	return strings.Split(text, "\n")
}

// OnDisk gives a file of the program a path an editor can open. The
// files of the standard library are inside qoqi: each is written out,
// the first time it is asked for, to a folder kept for that, and marked
// as not to be changed. Any other path is given back as it is; "" when
// the file cannot be written.
func OnDisk(file string) string {
	if !stdlib.IsPath(file) {
		return file
	}
	text, err := stdlib.Read(file)
	if err != nil {
		return ""
	}
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "qoqi", "std")
	path := filepath.Join(dir, strings.TrimPrefix(file, stdlib.Root+"/"))
	if had, err := os.ReadFile(path); err == nil && string(had) == text {
		return path
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	os.Chmod(path, 0o644) // Written before, by another version of qoqi
	if err := os.WriteFile(path, []byte(text), 0o444); err != nil {
		return ""
	}
	return path
}

// nameColumn finds where a name starts on a line, from a column on: a
// declaration is placed at its keyword, and its name is what is wanted.
func nameColumn(lines []string, line, from int, name string) int {
	if line < 1 || line > len(lines) || name == "" {
		return max(from, 1)
	}
	runes := []rune(lines[line-1])
	want := []rune(name)
	for i := max(from-1, 0); i+len(want) <= len(runes); i++ {
		if string(runes[i:i+len(want)]) != name {
			continue
		}
		before := i == 0 || !isNameRune(runes[i-1])
		after := i+len(want) == len(runes) || !isNameRune(runes[i+len(want)])
		if before && after {
			return i + 1
		}
	}
	return max(from, 1)
}

// docAbove gives the comment written just above a line: the lines
// starting with // that end right before it.
func docAbove(lines []string, line int) string {
	var doc []string
	for i := line - 2; i >= 0 && i < len(lines); i-- {
		text := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(text, "//") {
			break
		}
		doc = append([]string{strings.TrimPrefix(strings.TrimPrefix(text, "//"), " ")}, doc...)
	}
	return strings.Join(doc, "\n")
}
