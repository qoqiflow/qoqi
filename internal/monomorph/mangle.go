package monomorph

import (
	"fmt"
	"strings"

	"github.com/qoqiflow/qoqi/internal/types"
)

// mangleName generates a unique mangled name for a monomorphized instance.
// Format: {base_name}_{type1}_{type2}_..._typeN}
func (m *Monomorphizer) mangleName(key InstantiationKey) string {
	inst := m.instantiations[key]
	if inst == nil || len(inst.TypeArgs) == 0 {
		return string(key)
	}

	parts := []string{inst.BaseName}
	for _, t := range inst.TypeArgs {
		parts = append(parts, m.mangleType(t))
	}

	return strings.Join(parts, "_")
}

// mangleType converts a type to its mangled string representation.
func (m *Monomorphizer) mangleType(t types.Type) string {
	switch typ := t.(type) {
	case *types.PrimitiveType:
		// Capitalize primitive types: Int, Float, String, Bool, Void
		name := string(typ.Name)
		if len(name) > 0 {
			return strings.ToUpper(name[:1]) + name[1:]
		}
		return name

	case *types.StructType:
		// Use the struct name
		// If it has type arguments, flatten them
		if len(typ.TypeArgs) > 0 {
			parts := []string{typ.BaseName}
			for _, arg := range typ.TypeArgs {
				parts = append(parts, m.mangleType(arg))
			}
			return strings.Join(parts, "_")
		}
		return typ.Name

	case *types.ArrayType:
		// Arrays use Array_{element_type}
		return fmt.Sprintf("Array_%s", m.mangleType(typ.ElementType))

	case *types.FunctionType:
		// Functions encode params and return: Fn_Int_String (for fn(Int) -> String)
		parts := []string{"Fn"}
		for _, param := range typ.Params {
			parts = append(parts, m.mangleType(param))
		}
		parts = append(parts, m.mangleType(typ.ReturnType))
		return strings.Join(parts, "_")

	case *types.UnionType:
		// T? is Opt_T; other unions list their members
		parts := []string{"Opt"}
		for _, member := range typ.Types {
			if !member.Equals(types.Null) {
				parts = append(parts, m.mangleType(member))
			}
		}
		return strings.Join(parts, "_")

	case *types.TypeParam:
		// Type parameters should be substituted before mangling
		// If we reach here, it's an error, but return the name for debugging
		return typ.Name

	default:
		return "Unknown"
	}
}
