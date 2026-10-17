// Package flowgen writes, for an application in another language, what
// the compiler knows of the steps a queue carries out: the types they are
// given and return, an interface with a method for each, and what reads
// a step the daemon sends and writes its answer, in the form of the
// boundary (see docs/flow-app-steps.md, "The types, written for the
// other language").
package flowgen

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/qoqiflow/qoqi/internal/flowd"
)

// Options says what is written.
type Options struct {
	Source    string            // the program the types are written from, for the header
	Queue     string            // the queue whose steps are written; "" for every queue
	Namespace string            // PHP: the namespace of what is written
	From      string            // TypeScript: where the library is imported from
	Shapes    map[string]string // the shape of each step, by its name as the application knows it
}

// queues are the steps of the application, by queue, the queues sorted.
func queues(s *flowd.Schema, only string) ([]string, map[string][]*flowd.StepInfo, error) {
	by := map[string][]*flowd.StepInfo{}
	for _, step := range s.Steps {
		if step.Queue == "" || (only != "" && step.Queue != only) {
			continue
		}
		by[step.Queue] = append(by[step.Queue], step)
	}
	if len(by) == 0 {
		if only != "" {
			return nil, nil, fmt.Errorf("the program has no step by the queue %s", only)
		}
		return nil, nil, fmt.Errorf("the program has no step its application carries out: a step by a queue is declared `step fn charge(...) -> Charge by billing;`")
	}
	var names []string
	for name := range by {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, by, nil
}

// stepName is a step's name as the application knows it.
func stepName(step *flowd.StepInfo) string {
	return step.Name[strings.LastIndex(step.Name, ".")+1:]
}

// undoParam is the type of what undoes a step: the parameter of the
// step its declaration names.
func undoParam(s *flowd.Schema, step *flowd.StepInfo) *flowd.Type {
	if step.Undo == "" {
		return nil
	}
	if undo := s.Step(step.Undo); undo != nil && len(undo.Params) == 1 {
		return undo.Params[0].Type
	}
	return nil
}

// types are the structs and enums the steps given use, theirs and those
// they hold, the library's own (Result, Settled) left out, in the order
// they are first met.
func types(s *flowd.Schema, steps []*flowd.StepInfo) []*flowd.Type {
	seen := map[string]bool{}
	var out []*flowd.Type
	var visit func(t *flowd.Type)
	visit = func(t *flowd.Type) {
		if t == nil {
			return
		}
		switch t.Kind {
		case "option", "array", "dict":
			visit(t.Elem)
		case "struct", "enum":
			if seen[t.Name] {
				return
			}
			seen[t.Name] = true
			d := s.Types[t.Name]
			if d == nil {
				return
			}
			if !library(d) {
				out = append(out, t)
			}
			for _, f := range d.Fields {
				visit(f.Type)
			}
			for _, v := range d.Variants {
				for _, f := range v.Values {
					visit(f.Type)
				}
			}
		}
	}
	for _, step := range steps {
		for _, p := range step.Params {
			visit(p.Type)
		}
		visit(step.Returns)
		visit(undoParam(s, step))
	}
	return out
}

// library says whether a type is the library's own: Result, Settled.
func library(d *flowd.Decl) bool {
	return d.Name == "std.result.Result" || d.Name == "std.flow.Settled"
}

// isResult and isSettled say whether a type is the library's Result<T,
// E> or Settled<T>, whose type arguments the parser read.
func isResult(t *flowd.Type) bool {
	return t.Kind == "enum" && t.Base == "std.result.Result" && len(t.Args) == 2
}

func isSettled(t *flowd.Type) bool {
	return t.Kind == "enum" && t.Base == "std.flow.Settled" && len(t.Args) == 1
}

// className is the name a type is written under: its own, its type
// arguments joined in, Page<Cart> as PageOfCart, [Line] as ListOfLine.
func className(t *flowd.Type) string {
	switch t.Kind {
	case "int":
		return "Int"
	case "float":
		return "Float"
	case "bool":
		return "Bool"
	case "string":
		return "String"
	case "option":
		return className(t.Elem) + "OrNull"
	case "array":
		return "ListOf" + className(t.Elem)
	case "dict":
		return "DictOf" + className(t.Elem)
	}
	name := t.Base[strings.LastIndex(t.Base, ".")+1:]
	for i, arg := range t.Args {
		if i == 0 {
			name += "Of"
		} else {
			name += "And"
		}
		name += className(arg)
	}
	return name
}

// pascal is a queue's name as a type is named: billing as Billing,
// card_payments as CardPayments.
func pascal(name string) string {
	var out strings.Builder
	upper := true
	for _, r := range name {
		if r == '_' || r == '-' {
			upper = true
			continue
		}
		if upper {
			out.WriteRune(unicode.ToUpper(r))
			upper = false
		} else {
			out.WriteRune(r)
		}
	}
	return out.String()
}

// done is what the application returns for a step: what is Done, for
// one that returns a Settled, the daemon giving Unknown; the type itself
// otherwise.
func done(t *flowd.Type) *flowd.Type {
	if isSettled(t) {
		return t.Args[0]
	}
	return t
}
