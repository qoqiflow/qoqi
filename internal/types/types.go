package types

import (
	"fmt"
	"github.com/qoqiflow/qoqi/internal/ast"
	"strings"
)

type TypeKind int

const (
	KindPrimitive TypeKind = iota
	KindStruct
	KindFunction
	KindArray
	KindUnion
	KindBehavior
	KindIntersection
	KindTypeParam
)

// Type represents any type in the Qoqi language.
type Type interface {
	Kind() TypeKind
	String() string
	Equals(other Type) bool
}

// --- Type Parameters ---

type TypeParam struct {
	Name string
}

func NewTypeParam(name string) *TypeParam {
	return &TypeParam{Name: name}
}

func (t *TypeParam) Kind() TypeKind { return KindTypeParam }
func (t *TypeParam) String() string { return t.Name }
func (t *TypeParam) Equals(other Type) bool {
	o, ok := other.(*TypeParam)
	return ok && t.Name == o.Name
}

// --- Primitives ---

type PrimitiveKind string

const (
	PrimitiveInt    PrimitiveKind = "int"
	PrimitiveFloat  PrimitiveKind = "float"
	PrimitiveBool   PrimitiveKind = "bool"
	PrimitiveString PrimitiveKind = "string"
	PrimitiveVoid   PrimitiveKind = "void"
	PrimitiveAny    PrimitiveKind = "any"
	PrimitiveSelf   PrimitiveKind = "Self"
	PrimitiveNull   PrimitiveKind = "null"

	// PrimitiveUnknown is the type of an expression the checker could not
	// type, either because an error was already reported for it or because
	// nothing constrains it yet (the elements of `[]`). It is compatible
	// with every type so that one mistake is reported once.
	PrimitiveUnknown PrimitiveKind = "unknown"
)

type PrimitiveType struct {
	Name PrimitiveKind
}

func (t *PrimitiveType) Kind() TypeKind { return KindPrimitive }
func (t *PrimitiveType) String() string { return string(t.Name) }
func (t *PrimitiveType) Equals(other Type) bool {
	o, ok := other.(*PrimitiveType)
	return ok && t.Name == o.Name
}

var (
	Int    = &PrimitiveType{Name: PrimitiveInt}
	Float  = &PrimitiveType{Name: PrimitiveFloat}
	Bool   = &PrimitiveType{Name: PrimitiveBool}
	String = &PrimitiveType{Name: PrimitiveString}
	Void   = &PrimitiveType{Name: PrimitiveVoid}
	Any    = &PrimitiveType{Name: PrimitiveAny}
	Self   = &PrimitiveType{Name: PrimitiveSelf}

	// Null is the type of the `null` literal. `T?` is the union T | null.
	Null    = &PrimitiveType{Name: PrimitiveNull}
	Unknown = &PrimitiveType{Name: PrimitiveUnknown}
)

// --- Structs ---

type Field struct {
	Name   string
	Type   Type
	Public bool
}

type StructType struct {
	Name       string
	BaseName   string
	Fields     []Field
	TypeParams []string
	TypeArgs   []Type
}

func NewStruct(name string, fields []Field) *StructType {
	return &StructType{Name: name, BaseName: name, Fields: fields}
}

func NewGenericStruct(name string, typeParams []string, fields []Field) *StructType {
	return &StructType{Name: name, BaseName: name, Fields: fields, TypeParams: typeParams}
}

func (t *StructType) Kind() TypeKind { return KindStruct }
func (t *StructType) String() string {
	if len(t.TypeArgs) > 0 && len(t.TypeParams) == 0 {
		return t.Name
	}

	name := t.Name
	if len(t.TypeParams) > 0 {
		name = fmt.Sprintf("%s<%s>", t.Name, strings.Join(t.TypeParams, ", "))
	}

	var fields []string
	for _, f := range t.Fields {
		prefix := ""
		if f.Public {
			prefix = "pub "
		}
		fields = append(fields, fmt.Sprintf("%s%s: %s", prefix, f.Name, f.Type.String()))
	}
	return fmt.Sprintf("struct %s { %s }", name, strings.Join(fields, ", "))
}
func (t *StructType) Equals(other Type) bool {
	o, ok := other.(*StructType)
	// Nominal typing for structs: if names match, they are the same type.
	// We assume unique names for now.
	return ok && t.Name == o.Name
}

// --- Functions ---

type FunctionType struct {
	Params     []Type
	ReturnType Type
	TypeParams []string
	IsVariadic bool

	// Defaults are what the last parameters are when a call leaves them
	// out, for a function declared with them; nil for a function value,
	// which is called with all its arguments
	Defaults []ast.Expr
}

func NewFunction(params []Type, returnType Type) *FunctionType {
	return &FunctionType{Params: params, ReturnType: returnType}
}

func NewVariadicFunction(params []Type, returnType Type) *FunctionType {
	return &FunctionType{Params: params, ReturnType: returnType, IsVariadic: true}
}

func NewGenericFunction(typeParams []string, params []Type, returnType Type) *FunctionType {
	return &FunctionType{Params: params, ReturnType: returnType, TypeParams: typeParams}
}

func NewGenericVariadicFunction(typeParams []string, params []Type, returnType Type) *FunctionType {
	return &FunctionType{Params: params, ReturnType: returnType, TypeParams: typeParams, IsVariadic: true}
}

func (t *FunctionType) Kind() TypeKind { return KindFunction }
func (t *FunctionType) String() string {
	prefix := "fn"
	if len(t.TypeParams) > 0 {
		prefix += fmt.Sprintf("<%s>", strings.Join(t.TypeParams, ", "))
	}
	var params []string
	for i, p := range t.Params {
		s := p.String()
		if t.IsVariadic && i == len(t.Params)-1 {
			s = "..." + s
		}
		params = append(params, s)
	}
	return fmt.Sprintf("%s(%s) -> %s", prefix, strings.Join(params, ", "), t.ReturnType.String())
}
func (t *FunctionType) Equals(other Type) bool {
	o, ok := other.(*FunctionType)
	if !ok {
		return false
	}
	if t.IsVariadic != o.IsVariadic {
		return false
	}
	if len(t.Params) != len(o.Params) {
		return false
	}
	for i, p := range t.Params {
		if !p.Equals(o.Params[i]) {
			return false
		}
	}
	return t.ReturnType.Equals(o.ReturnType)
}

// --- Arrays ---

type ArrayType struct {
	ElementType Type
}

func NewArray(elemType Type) *ArrayType {
	return &ArrayType{ElementType: elemType}
}

func (t *ArrayType) Kind() TypeKind { return KindArray }
func (t *ArrayType) String() string { return fmt.Sprintf("[%s]", t.ElementType.String()) }
func (t *ArrayType) Equals(other Type) bool {
	o, ok := other.(*ArrayType)
	return ok && t.ElementType.Equals(o.ElementType)
}

// --- Unions ---

type UnionType struct {
	Types []Type
}

func NewUnion(types ...Type) *UnionType {
	// Flatten nested unions and deduplicate could happen here,
	// but for now we just store them.
	return &UnionType{Types: types}
}

func (t *UnionType) Kind() TypeKind { return KindUnion }
func (t *UnionType) String() string {
	var types []string
	for _, sub := range t.Types {
		types = append(types, sub.String())
	}
	return strings.Join(types, " | ")
}
func (t *UnionType) Equals(other Type) bool {
	o, ok := other.(*UnionType)
	if !ok || len(t.Types) != len(o.Types) {
		return false
	}
	// Order independent equality check is expensive but correct for unions.
	// For simplicity, we assume order matters or they are normalized.
	// Let's do a simple check for now.
	for i, sub := range t.Types {
		if !sub.Equals(o.Types[i]) {
			return false
		}
	}
	return true
}

// --- Behaviors (Interfaces) ---

type Method struct {
	Name   string
	Type   *FunctionType
	Public bool
}

type BehaviorType struct {
	Name    string
	Methods []Method

	// TypeArgs are the type arguments of a trait that has type
	// parameters: the Int of Source<Int>
	TypeArgs []Type
}

func NewBehavior(name string, methods []Method) *BehaviorType {
	return &BehaviorType{Name: name, Methods: methods}
}

func (t *BehaviorType) Kind() TypeKind { return KindBehavior }
func (t *BehaviorType) String() string {
	return fmt.Sprintf("behavior %s", t.Name)
}
func (t *BehaviorType) Equals(other Type) bool {
	o, ok := other.(*BehaviorType)
	if !ok || t.Name != o.Name || len(t.TypeArgs) != len(o.TypeArgs) {
		return false
	}
	for i := range t.TypeArgs {
		if !t.TypeArgs[i].Equals(o.TypeArgs[i]) {
			return false
		}
	}
	return true
}

// --- Intersections ---

type IntersectionType struct {
	Behaviors []*BehaviorType
}

func NewIntersection(behaviors ...*BehaviorType) *IntersectionType {
	return &IntersectionType{Behaviors: behaviors}
}

func (t *IntersectionType) Kind() TypeKind { return KindIntersection }
func (t *IntersectionType) String() string {
	var names []string
	for _, b := range t.Behaviors {
		names = append(names, b.Name)
	}
	return strings.Join(names, " & ")
}
func (t *IntersectionType) Equals(other Type) bool {
	o, ok := other.(*IntersectionType)
	if !ok || len(t.Behaviors) != len(o.Behaviors) {
		return false
	}
	for i, b := range t.Behaviors {
		if !b.Equals(o.Behaviors[i]) {
			return false
		}
	}
	return true
}

// Instantiate replaces type parameters in a generic type with concrete types.
func Instantiate(t Type, bindings map[string]Type) Type {
	switch t := t.(type) {
	case *TypeParam:
		if concrete, ok := bindings[t.Name]; ok {
			return concrete
		}
		return t

	case *StructType:
		// Instantiate fields
		newFields := make([]Field, len(t.Fields))
		changed := false
		for i, f := range t.Fields {
			newType := Instantiate(f.Type, bindings)
			newFields[i] = Field{
				Name:   f.Name,
				Type:   newType,
				Public: f.Public,
			}
			if newType != f.Type {
				changed = true
			}
		}

		// Resolve type parameters
		newTypeParams := []string{}
		newTypeArgs := []Type{}

		if len(t.TypeParams) > 0 {
			// Definition -> Instantiation
			for _, tp := range t.TypeParams {
				if val, ok := bindings[tp]; ok {
					newTypeArgs = append(newTypeArgs, val)
					changed = true
				} else {
					newTypeParams = append(newTypeParams, tp)
					// Keep as TypeParam in args
					newTypeArgs = append(newTypeArgs, NewTypeParam(tp))
				}
			}
		} else if len(t.TypeArgs) > 0 {
			// Instantiation -> Instantiation
			for _, arg := range t.TypeArgs {
				newArg := Instantiate(arg, bindings)
				newTypeArgs = append(newTypeArgs, newArg)
				if newArg != arg {
					changed = true
				}
			}
		}

		if !changed {
			return t
		}

		base := t.BaseName
		if base == "" {
			base = t.Name
		}

		newName := base
		if len(newTypeArgs) > 0 {
			args := []string{}
			for _, a := range newTypeArgs {
				args = append(args, a.String())
			}
			newName = fmt.Sprintf("%s[%s]", base, strings.Join(args, ", "))
		}

		return &StructType{
			Name:       newName,
			BaseName:   base,
			Fields:     newFields,
			TypeParams: newTypeParams,
			TypeArgs:   newTypeArgs,
		}

	case *FunctionType:
		newParams := make([]Type, len(t.Params))
		changed := false
		for i, p := range t.Params {
			newParams[i] = Instantiate(p, bindings)
			if newParams[i] != p {
				changed = true
			}
		}
		newRet := Instantiate(t.ReturnType, bindings)
		if newRet != t.ReturnType {
			changed = true
		}

		// Handle function type params?
		// Usually we instantiate a generic function when we call it.
		// But here we are instantiating a TYPE.
		// If we have `fn<T>(T) -> T`, and we instantiate T=Int, we get `fn(Int) -> Int`.
		// The resulting function type is NOT generic anymore.

		newTypeParams := []string{}
		if len(t.TypeParams) > 0 {
			for _, tp := range t.TypeParams {
				if _, ok := bindings[tp]; !ok {
					newTypeParams = append(newTypeParams, tp)
				} else {
					changed = true
				}
			}
		}

		if !changed {
			return t
		}

		return &FunctionType{
			Params:     newParams,
			ReturnType: newRet,
			TypeParams: newTypeParams,
		}

	case *ArrayType:
		newElem := Instantiate(t.ElementType, bindings)
		if newElem != t.ElementType {
			return NewArray(newElem)
		}
		return t

	case *UnionType:
		newTypes := make([]Type, len(t.Types))
		changed := false
		for i, sub := range t.Types {
			newTypes[i] = Instantiate(sub, bindings)
			if newTypes[i] != sub {
				changed = true
			}
		}
		if changed {
			return NewUnion(newTypes...)
		}
		return t

	case *IntersectionType:
		// Intersections of behaviors. Behaviors might be generic too?
		// For now assuming behaviors are not generic or handled similarly.
		// If behaviors are generic, we need to update BehaviorType too.
		return t
	}
	return t
}
