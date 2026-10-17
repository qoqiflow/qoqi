package vm

import (
	"fmt"
	"strings"
	"unsafe"
)

// Casts (`value as Type`) are verified by the __cast host function against
// a descriptor of the type, produced by the type checker:
//
//	int float bool string null   that kind of value
//	any                          anything
//	function                     any function
//	[D]                          an array whose elements all match D
//	?D                           null, or what D says
//	{A|B}                        a value of struct A or B
//	{A:f=D,g=D}                  a value of struct A whose fields f and g match
//	fn(D,D)->D                   a function usable with that signature
//	fn(D,...D)->void             ...variadic, returning nothing
//
// The fn form is also how a compiled function records its own type (in its
// prototype), which is what a cast to a function type is compared with.

// castType is a parsed cast descriptor.
type castType struct {
	kind   string      // "int", ..., "any", "function", "array", "optional", "struct", "fn", "void"
	text   string      // the descriptor this was parsed from
	elem   *castType   // array elements, the value of an optional, or what a fn returns
	names  []string    // struct: the accepted struct names
	fields []castField // struct: fields to verify

	params   []*castType // fn: parameter types
	variadic bool        // fn: the last parameter takes any number of arguments
}

type castField struct {
	name string
	typ  *castType
}

// parseCastType parses a descriptor. Descriptors come from the compiler, so
// a malformed one is reported as an error rather than recovered from.
func parseCastType(descriptor string) (*castType, error) {
	typ, rest, err := parseCastPrefix(descriptor)
	if err != nil {
		return nil, err
	}
	if rest != "" {
		return nil, fmt.Errorf("invalid cast descriptor %q", descriptor)
	}
	return typ, nil
}

// parseCastPrefix parses one descriptor from the start of s and returns
// what follows it.
func parseCastPrefix(s string) (*castType, string, error) {
	typ, rest, err := parseCastNode(s)
	if err == nil {
		typ.text = s[:len(s)-len(rest)]
	}
	return typ, rest, err
}

func parseCastNode(s string) (*castType, string, error) {
	invalid := func() (*castType, string, error) {
		return nil, "", fmt.Errorf("invalid cast descriptor near %q", s)
	}

	switch {
	case strings.HasPrefix(s, "?"):
		elem, rest, err := parseCastPrefix(s[1:])
		if err != nil {
			return nil, "", err
		}
		return &castType{kind: "optional", elem: elem}, rest, nil

	case strings.HasPrefix(s, "["):
		elem, rest, err := parseCastPrefix(s[1:])
		if err != nil {
			return nil, "", err
		}
		if !strings.HasPrefix(rest, "]") {
			return invalid()
		}
		return &castType{kind: "array", elem: elem}, rest[1:], nil

	case strings.HasPrefix(s, "{"):
		return parseCastStruct(s[1:])

	case strings.HasPrefix(s, "fn("):
		return parseCastFn(s[3:])
	}

	// A plain word
	end := 0
	for end < len(s) && s[end] >= 'a' && s[end] <= 'z' {
		end++
	}
	switch word := s[:end]; word {
	case "int", "float", "bool", "string", "null", "any", "function", "void":
		return &castType{kind: word}, s[end:], nil
	}
	return invalid()
}

// parseCastFn parses what follows "fn(": the parameters, ")->" and the
// return type.
func parseCastFn(s string) (*castType, string, error) {
	typ := &castType{kind: "fn"}
	invalid := fmt.Errorf("invalid cast descriptor near %q", s)

	for !strings.HasPrefix(s, ")") {
		if strings.HasPrefix(s, "...") {
			typ.variadic = true
			s = s[3:]
		}
		param, rest, err := parseCastPrefix(s)
		if err != nil {
			return nil, "", err
		}
		typ.params = append(typ.params, param)

		switch {
		case strings.HasPrefix(rest, ",") && !typ.variadic && !strings.HasPrefix(rest, ",)"):
			s = rest[1:]
		case strings.HasPrefix(rest, ")"):
			s = rest
		default:
			return nil, "", invalid
		}
	}

	if !strings.HasPrefix(s, ")->") {
		return nil, "", invalid
	}
	ret, rest, err := parseCastPrefix(s[3:])
	if err != nil {
		return nil, "", err
	}
	typ.elem = ret
	return typ, rest, nil
}

// parseCastStruct parses the inside of {A|B} or {A:f=D,g=D}.
func parseCastStruct(s string) (*castType, string, error) {
	typ := &castType{kind: "struct"}

	end := strings.IndexAny(s, ":}")
	if end < 0 {
		return nil, "", fmt.Errorf("invalid cast descriptor near %q", s)
	}
	if end > 0 {
		typ.names = strings.Split(s[:end], "|")
	}
	if s[end] == '}' {
		return typ, s[end+1:], nil
	}

	// Field list
	rest := s[end+1:]
	for {
		eq := strings.Index(rest, "=")
		if eq <= 0 {
			return nil, "", fmt.Errorf("invalid cast descriptor near %q", rest)
		}
		fieldType, after, err := parseCastPrefix(rest[eq+1:])
		if err != nil {
			return nil, "", err
		}
		typ.fields = append(typ.fields, castField{name: rest[:eq], typ: fieldType})

		switch {
		case strings.HasPrefix(after, ","):
			rest = after[1:]
		case strings.HasPrefix(after, "}"):
			return typ, after[1:], nil
		default:
			return nil, "", fmt.Errorf("invalid cast descriptor near %q", after)
		}
	}
}

// String renders the type the way it is written in source, for messages.
func (t *castType) String() string {
	switch t.kind {
	case "int":
		return "Int"
	case "float":
		return "Float"
	case "bool":
		return "Bool"
	case "string":
		return "String"
	case "any":
		return "Any"
	case "void":
		return "Void"
	case "fn":
		params := make([]string, len(t.params))
		for i, param := range t.params {
			params[i] = param.String()
		}
		if t.variadic {
			params[len(params)-1] = "..." + params[len(params)-1]
		}
		return "fn(" + strings.Join(params, ", ") + ") -> " + t.elem.String()
	case "array":
		return "[" + t.elem.String() + "]"
	case "optional":
		return t.elem.String() + "?"
	case "struct":
		if len(t.names) == 0 {
			return "a type nothing implements"
		}
		return strings.Join(t.names, " or ")
	}
	return t.kind
}

// mismatch explains why a value does not have the type, or returns "" if it
// does. A problem inside an array or struct is prefixed with where it is:
// "element 2: String is not an Int".
//
// The check follows the descriptor, which is a finite tree, so it ends even
// on a value that contains itself.
func (t *castType) mismatch(v Value) string {
	heapKind, isHeap := heapType(v)

	ok := false
	switch t.kind {
	case "any":
		ok = true
	case "int":
		ok = v.IsInt()
	case "float":
		ok = v.IsNumber()
	case "bool":
		ok = v.IsBool()
	case "null":
		ok = v.IsNull()
	case "string":
		ok = isHeap && heapKind == ObjTypeString
	case "function":
		ok = isHeap && (heapKind == ObjTypeFunction || heapKind == ObjTypeExternFunction)

	case "fn":
		if !isHeap || (heapKind != ObjTypeFunction && heapKind != ObjTypeExternFunction) {
			break
		}
		return t.signatureMismatch(functionSignature(v))

	case "optional":
		if v.IsNull() {
			return ""
		}
		return t.elem.mismatch(v)

	case "array":
		if !isHeap || heapKind != ObjTypeArray {
			break
		}
		arr := (*ArrayObj)(unsafe.Pointer(v.AsHeapPtr()))
		for i := 0; i < arr.Len(); i++ {
			if why := t.elem.mismatch(arr.Get(i)); why != "" {
				return fmt.Sprintf("element %d: %s", i, why)
			}
		}
		return ""

	case "struct":
		if !isHeap || heapKind != ObjTypeObject {
			break
		}
		obj := (*ObjectObj)(unsafe.Pointer(v.AsHeapPtr()))
		named := false
		for _, name := range t.names {
			if name == obj.shape.typeName {
				named = true
			}
		}
		if !named {
			break
		}
		for _, field := range t.fields {
			value, present := obj.Get(field.name)
			if !present {
				value = NewNull()
			}
			if why := field.typ.mismatch(value); why != "" {
				return fmt.Sprintf("field '%s': %s", field.name, why)
			}
		}
		return ""
	}

	if ok {
		return ""
	}
	return fmt.Sprintf("%s is not %s", typeName(v), withArticle(t.String()))
}

// functionSignature returns the recorded type of a function value, or "".
func functionSignature(v Value) string {
	ptr := unsafe.Pointer(v.AsHeapPtr())
	if (*ObjHeader)(ptr).Type == ObjTypeExternFunction {
		return (*ExternFunctionObj)(ptr).signature
	}
	return (*FunctionObj)(ptr).Proto().Signature
}

// signatureMismatch explains why a function with the given signature cannot
// be used as a function of type t, or returns "".
func (t *castType) signatureMismatch(signature string) string {
	if signature == "" {
		return "the function has no recorded type"
	}
	actual, err := parseCastType(signature)
	if err != nil || actual.kind != "fn" {
		return "the function has no usable type"
	}
	if !actual.usableAs(t) {
		return fmt.Sprintf("it is a %s", actual)
	}
	return ""
}

// usableAs reports whether a function of type f can stand in for one of
// type want: it must accept what want's callers pass and return what they
// expect.
func (f *castType) usableAs(want *castType) bool {
	if len(f.params) != len(want.params) || f.variadic != want.variadic {
		return false
	}
	for i := range want.params {
		if !want.params[i].subtypeOf(f.params[i]) {
			return false
		}
	}
	return f.elem.subtypeOf(want.elem)
}

// subtypeOf reports whether every value of type t is also one of type u.
func (t *castType) subtypeOf(u *castType) bool {
	if u.kind == "any" {
		return true
	}
	if u.kind == "optional" {
		switch t.kind {
		case "null":
			return true
		case "optional":
			return t.elem.subtypeOf(u.elem)
		}
		return t.subtypeOf(u.elem)
	}
	if t.kind != u.kind {
		return false
	}

	switch t.kind {
	case "array":
		return t.elem.subtypeOf(u.elem)
	case "fn":
		return t.usableAs(u)
	case "struct":
		if len(t.names) == 0 {
			return false
		}
		for _, name := range t.names {
			accepted := false
			for _, other := range u.names {
				accepted = accepted || name == other
			}
			if !accepted {
				return false
			}
		}
		// Field requirements only compare as a whole
		return len(u.fields) == 0 || t.text == u.text
	}
	return true // Same scalar kind
}

// withArticle prefixes a type name for a sentence: "an Int", "a String".
func withArticle(name string) string {
	if strings.HasPrefix(name, "a ") {
		return name
	}
	if strings.ContainsRune("AEIOU", rune(name[0])) {
		return "an " + name
	}
	return "a " + name
}

// cast implements __cast: it returns the value if it has the described
// type and an error saying why not otherwise.
func (vm *VM) cast(v Value, descriptor string) (Value, error) {
	typ, err := vm.castTypeFor(descriptor)
	if err != nil {
		return NewNull(), err
	}

	why := typ.mismatch(v)
	if why == "" {
		return v, nil
	}

	// When the reason says more than "X is not a T" (which part of the
	// value did not fit, or what the function's type is), add it
	if why != fmt.Sprintf("%s is not %s", typeName(v), withArticle(typ.String())) {
		return NewNull(), fmt.Errorf("cannot cast %s to %s: %s", typeName(v), typ, why)
	}
	return NewNull(), fmt.Errorf("cannot cast %s to %s", typeName(v), typ)
}

// castTypeFor returns the parsed form of a descriptor, parsing each
// distinct descriptor once.
func (vm *VM) castTypeFor(descriptor string) (*castType, error) {
	if typ, cached := vm.castTypes[descriptor]; cached {
		return typ, nil
	}
	typ, err := parseCastType(descriptor)
	if err != nil {
		return nil, err
	}
	vm.castTypes[descriptor] = typ
	return typ, nil
}
