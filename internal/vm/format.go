package vm

import (
	"sort"
	"strconv"
	"strings"
	"unsafe"
)

// maxFormatDepth bounds nesting when formatting, so that a structure that
// contains itself cannot recurse forever.
const maxFormatDepth = 8

// FormatValue returns the user-facing text of a value, as printed by
// print/println and returned by toString. Unlike Value.String, which is a
// debug representation, it follows heap pointers.
func FormatValue(v Value) string {
	f := formatter{room: maxFormatLength}
	return f.formatValue(v, 0)
}

// maxFormatLength bounds what one value is written as. Depth alone does
// not: an array holding itself fifty times over is fifty to the power of
// the depth, more than will ever be read or waited for.
const maxFormatLength = 1 << 20

// formatter writes a value, keeping count of the room left.
type formatter struct {
	room int

	// show writes a value of a type that says how it is written
	// (std.ops.Show), if it is one; nil writes every value the same way
	show func(obj *ObjectObj, v Value) (string, bool)
}

// elided is written in place of what there is no room left for.
const elided = "..."

func (f *formatter) formatValue(v Value, depth int) string {
	switch {
	case v.IsInt():
		return strconv.FormatInt(v.AsInt(), 10)
	case v.IsBool():
		return strconv.FormatBool(v.AsBool())
	case v.IsNull():
		return "null"
	case v.IsNumber():
		return strconv.FormatFloat(v.AsNumber(), 'g', -1, 64)
	case !v.IsHeapPtr() || v.AsHeapPtr() == 0:
		return v.String()
	}

	ptr := unsafe.Pointer(v.AsHeapPtr())
	switch (*ObjHeader)(ptr).Type {
	case ObjTypeString:
		return (*StringObj)(ptr).Data()

	case ObjTypeArray:
		if depth >= maxFormatDepth {
			return "[...]"
		}
		arr := (*ArrayObj)(ptr)
		parts := make([]string, 0, arr.Len())
		for i := 0; i < arr.Len(); i++ {
			if f.room <= 0 {
				parts = append(parts, elided)
				break
			}
			parts = append(parts, f.formatNested(arr.Get(i), depth+1))
		}
		return "[" + strings.Join(parts, ", ") + "]"

	case ObjTypeObject:
		if depth >= maxFormatDepth {
			return "{...}"
		}
		obj := (*ObjectObj)(ptr)
		if f.show != nil {
			if text, ok := f.show(obj, v); ok {
				return text
			}
		}
		if obj.shape.variant {
			// Written as it is made, without the enum's name: Rect(3, 4)
			name := obj.shape.typeName[strings.LastIndex(obj.shape.typeName, ".")+1:]
			var values []string
			for i := 0; ; i++ {
				value, ok := obj.Get(strconv.Itoa(i))
				if !ok {
					break
				}
				if f.room <= 0 {
					values = append(values, elided)
					break
				}
				values = append(values, f.formatNested(value, depth+1))
			}
			if len(values) == 0 {
				return name
			}
			if strings.HasPrefix(obj.shape.typeName, tuplePrefix) {
				// A tuple, written as it is made: (1, "a")
				name = ""
			}
			return name + "(" + strings.Join(values, ", ") + ")"
		}
		keys := obj.Keys()
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			value, _ := obj.Get(key)
			if isFunction(value) {
				continue // Methods attached to a struct value are not data
			}
			if f.room <= 0 {
				parts = append(parts, elided)
				break
			}
			parts = append(parts, key+": "+f.formatNested(value, depth+1))
		}
		return "{" + strings.Join(parts, ", ") + "}"

	case ObjTypeFunction:
		return "<fn " + (*FunctionObj)(ptr).Proto().Name + ">"
	case ObjTypeExternFunction:
		return "<fn " + (*ExternFunctionObj)(ptr).Name() + ">"
	case ObjTypeIndex:
		return "<index>"
	}
	return v.String()
}

// formatNested formats a value inside an array or object, where strings are
// quoted to keep them apart from the surrounding syntax.
func (f *formatter) formatNested(v Value, depth int) string {
	var text string
	if s, ok := stringValue(v); ok {
		text = strconv.Quote(s)
	} else {
		text = f.formatValue(v, depth)
	}
	// Two more for what sets it apart from the next
	f.room -= len(text) + 2
	return text
}

// heapType returns the object type of a heap value.
func heapType(v Value) (ObjType, bool) {
	if !v.IsHeapPtr() || v.AsHeapPtr() == 0 {
		return 0, false
	}
	return (*ObjHeader)(unsafe.Pointer(v.AsHeapPtr())).Type, true
}

// stringValue returns the Go string of a heap string value.
func stringValue(v Value) (string, bool) {
	if t, ok := heapType(v); !ok || t != ObjTypeString {
		return "", false
	}
	return (*StringObj)(unsafe.Pointer(v.AsHeapPtr())).Data(), true
}

func isFunction(v Value) bool {
	t, ok := heapType(v)
	return ok && (t == ObjTypeFunction || t == ObjTypeExternFunction)
}

// typeName describes a value's runtime type for error messages.
func typeName(v Value) string {
	switch {
	case v.IsInt():
		return "Int"
	case v.IsBool():
		return "Bool"
	case v.IsNull():
		return "null"
	case v.IsNumber():
		return "Float"
	}
	switch t, _ := heapType(v); t {
	case ObjTypeString:
		return "String"
	case ObjTypeArray:
		return "array"
	case ObjTypeObject:
		if name := (*ObjectObj)(unsafe.Pointer(v.AsHeapPtr())).shape.typeName; name != "" {
			return name
		}
		return "struct"
	case ObjTypeFunction, ObjTypeExternFunction:
		return "function"
	case ObjTypeIndex:
		return "index"
	}
	return "unknown"
}

// tuplePrefix starts the name of the enums the compiler declares for
// tuples (ast.TuplePrefix); a value of one prints without a name.
const tuplePrefix = "__Tuple"
