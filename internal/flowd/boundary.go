package flowd

// What crosses to the applications that carry out steps, in the form of
// the boundary (see docs/flow-app-steps.md, "Data at the boundary"): no
// "$type", the daemon knowing the type expected. A step's arguments go
// out in it; its answer comes back in it, and is read against the type
// the step returns.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/qoqiflow/qoqi/internal/vm"
)

// Plain writes data as the program holds it, "$type" and all, in the
// form of the boundary, as a value of the type given.
func (p *Schema) Plain(value any, t *Type) any {
	switch t.Kind {
	case "float":
		if special, ok := value.(map[string]any); ok {
			if text, ok := special["$float"].(string); ok {
				return text
			}
		}
		return value
	case "option":
		if value == nil {
			return nil
		}
		return p.Plain(value, t.Elem)
	case "array":
		items, ok := value.([]any)
		if !ok {
			return value
		}
		out := make([]any, len(items))
		for i, item := range items {
			out[i] = p.Plain(item, t.Elem)
		}
		return out
	case "dict":
		object, ok := value.(map[string]any)
		if !ok {
			return value
		}
		keys, _ := object["keys"].([]any)
		values, _ := object["values"].([]any)
		out := &fields{values: map[string]any{}}
		for i, key := range keys {
			if i < len(values) {
				out.set(fmt.Sprint(key), p.Plain(values[i], t.Elem))
			}
		}
		return out
	case "struct":
		d := p.Types[t.Name]
		object, ok := value.(map[string]any)
		if d == nil || !ok {
			return value
		}
		out := &fields{values: map[string]any{}}
		for _, f := range d.Fields {
			if item, ok := object[f.Name]; ok {
				out.set(f.Name, p.Plain(item, f.Type))
			}
		}
		return out
	case "enum":
		d := p.Types[t.Name]
		object, ok := value.(map[string]any)
		if d == nil || !ok {
			return value
		}
		typ, _ := object["$type"].(string)
		name := typ[strings.LastIndex(typ, ".")+1:]
		for _, v := range d.Variants {
			if v.Name != name {
				continue
			}
			switch {
			case len(v.Values) == 0:
				return name
			case v.Named:
				values := &fields{values: map[string]any{}}
				for i, f := range v.Values {
					values.set(f.Name, p.Plain(object[fmt.Sprint(i)], f.Type))
				}
				return map[string]any{name: values}
			case len(v.Values) == 1:
				return map[string]any{name: p.Plain(object["0"], v.Values[0].Type)}
			}
			values := make([]any, len(v.Values))
			for i, f := range v.Values {
				values[i] = p.Plain(object[fmt.Sprint(i)], f.Type)
			}
			return map[string]any{name: values}
		}
	}
	return value
}

// Step is a step of the program, by its name as the application knows
// it, the last part of its name in the program.
func (p *Schema) Step(name string) *StepInfo {
	name = name[strings.LastIndex(name, ".")+1:]
	for _, s := range p.Steps {
		if s.Name[strings.LastIndex(s.Name, ".")+1:] == name {
			return s
		}
	}
	return nil
}

// decode reads JSON with its numbers as they are written.
func decode(data []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var value any
	err := d.Decode(&value)
	return value, err
}

// Boundary is the form of the boundary for the runtime of a program:
// what a step of a queue is given, written in it, and what comes back,
// read from it.
type Boundary struct {
	Schema *Schema
}

// Args writes a step's arguments, by name, in the form of the boundary.
func (b Boundary) Args(step string, args json.RawMessage) (json.RawMessage, error) {
	s := b.Schema.Step(step)
	if s == nil {
		return args, nil
	}
	value, err := decode(args)
	if err != nil {
		return nil, err
	}
	given, _ := value.(map[string]any)
	out := &fields{values: map[string]any{}}
	for _, param := range s.Params {
		out.set(param.Name, b.Schema.Plain(given[param.Name], param.Type))
	}
	return json.Marshal(out)
}

// Answer reads what an application answered a step against the type it
// returns, and the undo it gave against the parameter of the step its
// declaration names: as the program's data. What does not fit is a
// panic of the step, saying where.
func (b Boundary) Answer(step, undo string, a vm.Answer) vm.Answer {
	if a.Outcome != vm.Answered {
		return a
	}
	s := b.Schema.Step(step)
	if s == nil {
		return a
	}
	name := s.Name[strings.LastIndex(s.Name, ".")+1:]
	read := func(data json.RawMessage, t *Type, what string) (json.RawMessage, error) {
		value, err := decode(data)
		if err != nil {
			return nil, fmt.Errorf("%s is not JSON: %v", what, err)
		}
		typed, err := b.Schema.conform(value, t, what)
		if err != nil {
			return nil, err
		}
		return json.Marshal(typed)
	}
	value, err := read(a.Value, s.Returns, name)
	if err != nil {
		return vm.Answer{Outcome: vm.Failed, Reason: "what the application answered does not fit: " + err.Error()}
	}
	a.Value = value
	if a.Undo != nil && undo != "" {
		if u := b.Schema.Step(undo); u != nil && len(u.Params) == 1 {
			undone, err := read(a.Undo, u.Params[0].Type, "the undo of "+name)
			if err != nil {
				return vm.Answer{Outcome: vm.Failed, Reason: "what the application answered does not fit: " + err.Error()}
			}
			a.Undo = undone
		}
	}
	return a
}
