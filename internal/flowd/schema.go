package flowd

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/flows"
)

// What the daemon knows of the program it runs, for the pages: each flow,
// the types of its fields, what it returns, its steps, and where each is
// written. It is what lets a page build the form that starts an instance,
// and lets a start be written as plain JSON: the daemon gives every
// struct its "$type" and every variant its form, from the types.

// Type is a type of the program as the pages read it.
type Type struct {
	// int, float, bool, string, void, any, array, option, dict, struct,
	// enum, union, function, duration (a std.time.Duration, written
	// "90s"), inbox (Elem: what it takes), or other: a
	// type of the library that is not written as data (a channel, an
	// instance)
	Kind string  `json:"kind"`
	Name string  `json:"name,omitempty"` // struct, enum: its name, with its type arguments
	Text string  `json:"text"`           // as a person writes it: [Cart], Result<String, Problem>
	Elem *Type   `json:"elem,omitempty"` // array, option, dict: what it holds
	Of   []*Type `json:"of,omitempty"`   // union: its types
	// Base and Args are a struct's or an enum's name, without its type
	// arguments, and those arguments, as the parser read them
	Base string  `json:"base,omitempty"`
	Args []*Type `json:"args,omitempty"`
}

// Field is a field of a struct, or a named value of a variant.
type Field struct {
	Name     string `json:"name"`
	Type     *Type  `json:"type"`
	Default  bool   `json:"default,omitempty"` // it has one, written in the program
	Doc      string `json:"doc,omitempty"`
	Optional bool   `json:"optional,omitempty"` // may be left out: null
}

// Variant is a variant of an enum.
type Variant struct {
	Name   string   `json:"name"`
	Values []*Field `json:"values,omitempty"` // named after their place, "0", when the variant does not name them
	Named  bool     `json:"named,omitempty"`
}

// Decl is a struct or an enum of the program.
type Decl struct {
	Kind     string     `json:"kind"` // struct or enum
	Name     string     `json:"name"`
	Text     string     `json:"text"`
	Fields   []*Field   `json:"fields,omitempty"`
	Variants []*Variant `json:"variants,omitempty"`
	Doc      string     `json:"doc,omitempty"`
	At       *Position  `json:"at,omitempty"`
}

// Position is where something is written.
type Position struct {
	File string `json:"file"`
	Line int    `json:"line"`
}

// FlowInfo is a flow of the program.
type FlowInfo struct {
	Name    string    `json:"name"`
	Returns *Type     `json:"returns"`
	Fields  []*Field  `json:"fields"`
	Inboxes []*Field  `json:"inboxes,omitempty"` // the fields messages come in by: no start gives them
	Doc     string    `json:"doc,omitempty"`
	At      *Position `json:"at,omitempty"`
	Run     *Position `json:"run,omitempty"` // where its run is written
}

// StepInfo is a step of the program.
type StepInfo struct {
	Name    string   `json:"name"`
	Params  []*Field `json:"params"`
	Returns *Type    `json:"returns"`
	Once    bool     `json:"once,omitempty"` // it returns a Settled: never run twice
	// Queue is who carries it out, for a step of the application, and
	// Undo the step that undoes it
	Queue string    `json:"queue,omitempty"`
	Undo  string    `json:"undo,omitempty"`
	Doc   string    `json:"doc,omitempty"`
	At    *Position `json:"at,omitempty"`
}

// Schema is what the daemon knows of its program.
type Schema struct {
	Entry string           `json:"entry"`
	Flows []*FlowInfo      `json:"flows"`
	Steps []*StepInfo      `json:"steps"`
	Types map[string]*Decl `json:"types"` // by Type.Name: the structs and enums its flows and steps use
	Files []string         `json:"files"` // the source files the pages may show
}

// SchemaOf reads a program's flows from its syntax tree: those it serves,
// of its entry file and of its modules (flows.FlowsOf), and its steps.
func SchemaOf(entry string, prog *ast.Program, files []string) *Schema {
	s := &schema{
		structs: map[string]*ast.StructDecl{},
		enums:   map[string]*ast.EnumDecl{},
		docs:    map[string][]string{},
		out:     &Schema{Entry: entry, Types: map[string]*Decl{}, Files: files},
	}
	for _, decl := range prog.Decls {
		switch d := decl.(type) {
		case *ast.StructDecl:
			s.structs[d.Name] = d
		case *ast.EnumDecl:
			s.enums[d.Name] = d
		}
	}
	served := map[string]bool{}
	for _, flow := range flows.FlowsOf(prog) {
		served[flow] = true
	}
	for _, decl := range prog.Decls {
		switch d := decl.(type) {
		case *ast.ImplDecl:
			if d.TraitName != flows.FlowTrait || !served[d.StructName] {
				continue
			}
			flow := &FlowInfo{Name: d.StructName, Returns: &Type{Kind: "void", Text: "Void"}}
			if len(d.TraitArgs) == 1 {
				flow.Returns = s.typeOf(d.TraitArgs[0], nil)
			}
			if st := s.structs[d.StructName]; st != nil {
				// An inbox is made by the daemon at the start, and written to
				// afterwards: it is not given
				for _, f := range s.fields(st.Fields, nil) {
					if f.Type.Kind == "inbox" {
						flow.Inboxes = append(flow.Inboxes, f)
					} else {
						flow.Fields = append(flow.Fields, f)
					}
				}
				flow.Doc = s.doc(st.Pos.File, st.Pos.Line)
				flow.At = position(st.BaseNode)
			}
			for _, m := range d.Methods {
				if m.Name == "run" {
					flow.Run = position(m.BaseNode)
				}
			}
			s.out.Flows = append(s.out.Flows, flow)
		case *ast.FnDecl:
			if !d.Step {
				continue
			}
			step := &StepInfo{Name: d.Name, Returns: &Type{Kind: "void", Text: "Void"}, Doc: s.doc(d.Pos.File, d.Pos.Line), At: position(d.BaseNode)}
			for _, p := range d.Params {
				step.Params = append(step.Params, &Field{Name: p.Name, Type: s.typeOf(p.Type, nil), Default: p.Default != nil})
			}
			if d.ReturnType != nil {
				step.Returns = s.typeOf(d.ReturnType, nil)
				step.Once = strings.HasPrefix(d.ReturnType.Name, "std.flow.Settled")
			}
			step.Queue, step.Undo = d.Queue, d.Undo
			s.out.Steps = append(s.out.Steps, step)
		}
	}
	sort.Slice(s.out.Steps, func(i, j int) bool { return s.out.Steps[i].Name < s.out.Steps[j].Name })
	return s.out
}

type schema struct {
	structs map[string]*ast.StructDecl
	enums   map[string]*ast.EnumDecl
	docs    map[string][]string // the lines of each file read
	out     *Schema
}

func position(n ast.BaseNode) *Position {
	if n.Pos.File == "" {
		return nil
	}
	return &Position{File: n.Pos.File, Line: n.Pos.Line}
}

// short is a name as a person reads it: std.result.Result is Result.
func short(name string) string {
	if strings.HasPrefix(name, "std.") {
		return name[strings.LastIndex(name, ".")+1:]
	}
	return name
}

// typeOf reads a type, its type parameters given by bound.
func (s *schema) typeOf(t *ast.Type, bound map[string]*Type) *Type {
	if t == nil {
		return &Type{Kind: "void", Text: "Void"}
	}
	switch t.Kind {
	case ast.TypeArray:
		elem := s.typeOf(t.ElemType, bound)
		return &Type{Kind: "array", Elem: elem, Text: "[" + elem.Text + "]"}
	case ast.TypeOption:
		elem := s.typeOf(t.ElemType, bound)
		return &Type{Kind: "option", Elem: elem, Text: elem.Text + "?"}
	case ast.TypeUnion:
		u := &Type{Kind: "union"}
		var texts []string
		for _, part := range t.UnionTypes {
			pt := s.typeOf(part, bound)
			u.Of = append(u.Of, pt)
			texts = append(texts, pt.Text)
		}
		u.Text = strings.Join(texts, " | ")
		return u
	case ast.TypeFunction:
		return &Type{Kind: "function", Text: t.String()}
	}
	if b, ok := bound[t.Name]; ok {
		return b
	}
	switch t.Name {
	case "Int":
		return &Type{Kind: "int", Text: "Int"}
	case "Float":
		return &Type{Kind: "float", Text: "Float"}
	case "Bool":
		return &Type{Kind: "bool", Text: "Bool"}
	case "String":
		return &Type{Kind: "string", Text: "String"}
	case "Void":
		return &Type{Kind: "void", Text: "Void"}
	case "Any":
		return &Type{Kind: "any", Text: "Any"}
	}
	var args []*Type
	var texts []string
	for _, g := range t.Generics {
		a := s.typeOf(g, bound)
		args = append(args, a)
		texts = append(texts, a.Text)
	}
	name, text := t.Name, short(t.Name)
	if len(args) > 0 {
		name += "<" + strings.Join(texts, ", ") + ">"
		text += "<" + strings.Join(texts, ", ") + ">"
	}
	if t.Name == "std.dict.Dict" && len(args) == 1 {
		return &Type{Kind: "dict", Elem: args[0], Text: text}
	}
	if t.Name == "std.time.Duration" {
		return &Type{Kind: "duration", Name: t.Name, Text: "Duration"}
	}
	if t.Name == "std.flow.Inbox" && len(args) == 1 {
		return &Type{Kind: "inbox", Elem: args[0], Text: text}
	}
	if st := s.structs[t.Name]; st != nil && !strings.HasPrefix(t.Name, "std.") {
		s.declare(name, func() *Decl {
			d := &Decl{Kind: "struct", Name: t.Name, Text: text, Doc: s.doc(st.Pos.File, st.Pos.Line), At: position(st.BaseNode)}
			d.Fields = s.fields(st.Fields, params(st.TypeParams, args))
			return d
		})
		return &Type{Kind: "struct", Name: name, Text: text, Base: t.Name, Args: args}
	}
	if en := s.enums[t.Name]; en != nil && (!strings.HasPrefix(t.Name, "std.") || t.Name == "std.result.Result" || t.Name == "std.flow.Settled") {
		s.declare(name, func() *Decl {
			d := &Decl{Kind: "enum", Name: t.Name, Text: text}
			if !strings.HasPrefix(t.Name, "std.") {
				d.Doc, d.At = s.doc(en.Pos.File, en.Pos.Line), position(en.BaseNode)
			}
			bound := params(en.TypeParams, args)
			for _, v := range en.Variants {
				variant := &Variant{Name: v.Name, Named: len(v.Names) > 0}
				for i, vt := range v.Values {
					name := fmt.Sprint(i)
					if variant.Named {
						name = v.Names[i]
					}
					variant.Values = append(variant.Values, &Field{Name: name, Type: s.typeOf(vt, bound)})
				}
				d.Variants = append(d.Variants, variant)
			}
			return d
		})
		return &Type{Kind: "enum", Name: name, Text: text, Base: t.Name, Args: args}
	}
	return &Type{Kind: "other", Name: name, Text: text}
}

// declare keeps a declaration once: a type that holds itself is
// declared before its fields are read.
func (s *schema) declare(name string, make func() *Decl) {
	if _, ok := s.out.Types[name]; ok {
		return
	}
	s.out.Types[name] = &Decl{Name: name}
	*s.out.Types[name] = *make()
}

func params(names []string, args []*Type) map[string]*Type {
	bound := map[string]*Type{}
	for i, n := range names {
		if i < len(args) {
			bound[n] = args[i]
		} else {
			bound[n] = &Type{Kind: "any", Text: n}
		}
	}
	return bound
}

func (s *schema) fields(fields []*ast.Field, bound map[string]*Type) []*Field {
	var out []*Field
	for _, f := range fields {
		if f.Private {
			continue
		}
		ft := s.typeOf(f.Type, bound)
		out = append(out, &Field{Name: f.Name, Type: ft, Default: f.Default != nil, Optional: ft.Kind == "option", Doc: s.doc(f.Pos.File, f.Pos.Line)})
	}
	return out
}

// doc is the comment written on the lines just above a line of a file,
// or at its end: "// in cents".
func (s *schema) doc(file string, line int) string {
	if file == "" || line <= 0 {
		return ""
	}
	lines, ok := s.docs[file]
	if !ok {
		data, err := os.ReadFile(file)
		if err == nil {
			lines = strings.Split(string(data), "\n")
		}
		s.docs[file] = lines
	}
	if line > len(lines) {
		return ""
	}
	var above []string
	for i := line - 2; i >= 0; i-- {
		text := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(text, "//") {
			break
		}
		above = append([]string{strings.TrimSpace(strings.TrimPrefix(text, "//"))}, above...)
	}
	if len(above) > 0 {
		return strings.Join(above, " ")
	}
	if at := strings.Index(lines[line-1], "//"); at >= 0 && !strings.HasPrefix(strings.TrimSpace(lines[line-1]), "//") {
		return strings.TrimSpace(lines[line-1][at+2:])
	}
	return ""
}

// MergeSchemas is what a daemon knows of the programs it serves, as one:
// their flows, their steps, their types and their files. A flow belongs
// to one program; a type two programs have of one name is the first's.
func MergeSchemas(entry string, schemas []*Schema) *Schema {
	if len(schemas) == 1 {
		return schemas[0]
	}
	out := &Schema{Entry: entry, Types: map[string]*Decl{}}
	for _, s := range schemas {
		out.Flows = append(out.Flows, s.Flows...)
		out.Steps = append(out.Steps, s.Steps...)
		out.Files = append(out.Files, s.Files...)
		for name, decl := range s.Types {
			if _, taken := out.Types[name]; !taken {
				out.Types[name] = decl
			}
		}
	}
	sort.Slice(out.Steps, func(i, j int) bool { return out.Steps[i].Name < out.Steps[j].Name })
	sort.Strings(out.Files)
	return out
}
