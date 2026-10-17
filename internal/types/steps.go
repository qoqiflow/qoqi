package types

import (
	"fmt"
	"strings"

	"github.com/qoqiflow/qoqi/internal/ast"
)

// A step of a flow, `step fn charge(...)`, is where a flow reaches the
// world. What it is given and what it returns are kept with the flow, in a
// store, and given back when the flow is taken up: they have to be data
// (see docs/flow.md).

// ErrStepNotData is a step given or giving what is not data.
const ErrStepNotData = "F0004"

// handles are the types of the library that stand for something alive in
// the running program, a task, a channel, a connection: kept in a store,
// they would mean nothing once taken up.
var handles = []string{
	"std.task.", "std.time.After", "std.http.Server",
}

// collections are the types of the library that are data when what they
// hold is.
var collections = []string{"std.dict.Dict", "std.map.Map", "std.list.List", "std.set.Set"}

// checkStep checks what a step is given and what it returns.
func (c *Checker) checkStep(d *ast.FnDecl) {
	if !d.Step {
		return
	}
	if len(d.TypeParams) > 0 {
		c.errorfWithSuggestion(d.Token(), ErrStepNotData,
			"Write it for the types it is used with",
			"a step has no type parameters: what it is given and returns is kept, and is of a type known where it is written")
		return
	}
	for _, param := range d.Params {
		if param.Type == nil {
			continue
		}
		if why := c.notData(c.resolveType(param.Type), map[string]bool{}); why != "" {
			c.errorfWithSuggestion(param.Token(), ErrStepNotData,
				"Give the step the data it needs instead: numbers, texts, structs, enums, arrays and dictionaries of those",
				"what a step is given is kept with the flow, and has to be data: '%s' is %s", param.Name, why)
		}
	}
	if d.ReturnType != nil {
		if why := c.notData(c.resolveType(d.ReturnType), map[string]bool{}); why != "" {
			c.errorfWithSuggestion(d.ReturnType.Token(), ErrStepNotData,
				"Return the data the flow needs: numbers, texts, structs, enums, arrays and dictionaries of those",
				"what a step returns is kept with the flow, and has to be data: this is %s", why)
		}
	}
}

// notData says what in a type is not data, or "" when all of it is.
func (c *Checker) notData(t Type, seen map[string]bool) string {
	switch t := t.(type) {
	case nil:
		return ""
	case *PrimitiveType:
		if t.Name == PrimitiveAny {
			return "Any, which may hold anything"
		}
		return ""
	case *ArrayType:
		return c.notData(t.ElementType, seen)
	case *UnionType:
		for _, member := range t.Types {
			if why := c.notData(member, seen); why != "" {
				return why
			}
		}
		return ""
	case *FunctionType:
		return "a function"
	case *BehaviorType:
		return fmt.Sprintf("a %s, a trait: what it holds is only known when the program runs", describe(t))
	case *TypeParam:
		return "a type parameter"
	case *StructType:
		for _, handle := range handles {
			if strings.HasPrefix(t.Name, handle) || strings.HasPrefix(t.BaseName, handle) {
				return fmt.Sprintf("a %s, which stands for something alive in the running program", describe(t))
			}
		}
		for _, collection := range collections {
			if t.BaseName == collection || t.Name == collection {
				// What it holds, not how it is made
				for _, arg := range t.TypeArgs {
					if why := c.notData(arg, seen); why != "" {
						return why
					}
				}
				return ""
			}
		}
		key := t.Name
		if seen[key] {
			return ""
		}
		seen[key] = true
		if decl, _ := c.enumOfType(t); decl != nil {
			for _, variant := range decl.Variants {
				for _, field := range c.variantFields(decl, variant, t.TypeArgs) {
					if why := c.notData(field, seen); why != "" {
						return fmt.Sprintf("%s, in %s.%s", why, describe(t), variant.Name)
					}
				}
			}
			return ""
		}
		names := []string{}
		for _, field := range t.Fields {
			names = append(names, field.Name)
		}
		if decl := c.structs[t.BaseName]; decl != nil && len(names) == 0 {
			for _, field := range decl.Fields {
				names = append(names, field.Name)
			}
		}
		for _, name := range names {
			field, ok := c.fieldType(t, name)
			if !ok {
				continue
			}
			if why := c.notData(field, seen); why != "" {
				return fmt.Sprintf("%s, in the field '%s' of %s", why, name, describe(t))
			}
		}
		return ""
	}
	return ""
}
