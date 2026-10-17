# Monomorphization Package

This package implements **monomorphization** for the Qoqi compiler - the process of generating concrete, specialized versions of generic functions and types for each unique set of type arguments used in the program.

## Overview

Monomorphization transforms generic code like this:

```rust
fn identity<T>(x: T) -> T {
    return x;
}

fn main() -> Int {
    let a = identity<Int>(42);
    let b = identity<String>("hello");
    return a;
}
```

Into concrete, specialized code like this:

```rust
fn identity_Int(x: Int) -> Int {
    return x;
}

fn identity_String(x: String) -> String {
    return x;
}

fn main() -> Int {
    let a = identity_Int(42);
    let b = identity_String("hello");
    return a;
}
```

## Architecture

The monomorphization process consists of **four main phases**:

### Phase 1: Instantiation Discovery
- Walks the entire AST to find all uses of generic functions and types
- Records each unique combination of (function/type name, type arguments)
- Tracks all use sites for error reporting

### Phase 2: Dependency Resolution
- Builds a dependency graph (e.g., if `foo<T>` calls `bar<T>`, then `foo<Int>` depends on `bar<Int>`)
- Follows what each instantiation needs in turn until nothing new turns up; the order of generation is the order of discovery
- Recursion between instances is fine; a chain of ever larger instances is cut off and reported (infinite instantiation)

### Phase 3: Code Generation
- For each instantiation, clones the generic AST declaration
- Substitutes all type parameters with concrete types throughout the cloned AST
- Generates a unique mangled name for the instance
- Adds the new concrete declaration to the program

### Phase 4: Call Site Rewriting
- Rewrites all generic function calls to use mangled names
- Rewrites all generic struct instantiations to use mangled names
- Removes type arguments from calls

## Usage

```go
import (
    "github.com/qoqiflow/qoqi/internal/monomorph"
    "github.com/qoqiflow/qoqi/internal/types"
    "github.com/qoqiflow/qoqi/internal/resolve"
    "github.com/qoqiflow/qoqi/internal/errors"
)

// After parsing, name resolution, and type checking:
errs := errors.NewHandler()
m := monomorph.NewMonomorphizer(prog, typeInfo, resolveTable, errs)
m.SetSourceLines(sourceLines) // Optional: for better error reporting

transformedProg, monoInfo := m.Monomorphize()

if errs.HasErrors() {
    // Handle monomorphization errors
}

// transformedProg now contains only concrete types
// monoInfo contains mapping of instantiation keys to generated declarations
```

## Name Mangling Scheme

The package uses a consistent name mangling scheme to generate unique names:

**Format**: `{base_name}_{type1}_{type2}_..._{typeN}`

**Examples**:
- `identity<Int>` → `identity_Int`
- `map<Int, String>` → `map_Int_String`
- `Vec<Int>` → `Vec_Int`
- `Vec<Vec<Int>>` → `Vec_Vec_Int`
- `Option<Array<Int>>` → `Option_Array_Int`

**Type Encoding**:
- Primitives: `Int`, `Float`, `String`, `Bool`, `Void`
- Arrays: `Array_{element_type}`
- Structs: Use the struct name
- Functions: `Fn_{param1}_{param2}_..._{return}`

## Error Handling

The package defines three error codes:

### E3001: Unbounded Generics
Generic function/type used but type arguments cannot be inferred.

**Example**:
```rust
fn identity<T>(x: T) -> T { return x; }

fn main() -> Void {
    let x = identity(42);  // Error: Cannot infer type T
}
```

### E3002: Infinite Instantiation
A generic function whose instances each need a larger one, without end. Calling itself with the same type arguments is ordinary recursion, not this.

**Example**:
```rust
fn recurse<T>(x: T) -> T {
    return recurse<[T]>([x]);  // Infinite: T -> [T] -> [[T]] -> ...
}
```

### E3003: Type Argument Mismatch
Number of type arguments doesn't match number of type parameters.

**Example**:
```rust
fn pair<T, U>(x: T, y: U) -> Void { }

fn main() -> Void {
    pair<Int>(1, 2);  // Error: Expected 2 type arguments, got 1
}
```

## Type Registry Integration

The monomorphizer integrates with the type checker's type registry for proper type lookup:

```go
// During type checking, register type definitions:
typeInfo.RegisterStructDecl(structDecl, structType)
typeInfo.RegisterType("MyType", myType)

// The monomorphizer will look up types from typeInfo.Defs
// to get complete type information including fields
```

This ensures that when converting AST types to `types.Type`, the monomorphizer:
1. First checks the type registry for the complete type definition
2. Preserves field information from the original type
3. Falls back to creating a basic type reference if not found

## Implementation Files

- `monomorph.go` - Core monomorphizer struct and main entry point
- `discovery.go` - Phase 1: AST walking and instantiation discovery
- `dependencies.go` - Phase 2: Dependency resolution and topological sort
- `codegen.go` - Phase 3: Code generation (cloning and substitution)
- `mangle.go` - Name mangling utilities
- `rewrite.go` - Phase 4: Call site rewriting

## Testing

The package includes comprehensive tests:

```bash
go test ./internal/monomorph/... -v
```

**Test Coverage**:
- Instantiation key generation
- Name mangling for various type combinations
- Generic function collection
- Basic monomorphization workflow
- Duplicate instantiation handling (caching)

## Limitations & Future Work

**Current Limitations**:
- Type inference is not yet fully implemented - requires explicit type arguments
- Enum types are not yet supported (only structs and functions)
- No trait bounds or constraints checking
- No specialization support

**Future Enhancements**:
- Full type inference integration with the type checker
- Support for generic enums and traits
- Trait bound validation during monomorphization
- Optimization: detecting and deduplicating structurally identical functions
- Better error recovery and multiple error reporting

## Performance Considerations

**Compile-Time Impact**:
- Compilation time is proportional to the number of instantiations
- Memory usage increases due to AST cloning
- Binary size grows with each instantiation

**Mitigations**:
- **Instantiation Caching**: Reuses previously generated instances
- **Lazy Instantiation**: Only instantiates what's actually used
- Future: **Deduplication**: Detect structurally identical functions

**Runtime Benefits**:
- No virtual dispatch overhead (direct function calls)
- Each instance can be optimized independently
- Easier inlining for the compiler

## References

- [Qoqi Monomorphization Specification](../../docs/internals/compilation/monomorphization.md)
- [Rust RFC 1210: Specialization](https://rust-lang.github.io/rfcs/1210-impl-specialization.html)
- [Swift Generics Manifesto](https://github.com/apple/swift/blob/main/docs/GenericsManifesto.md)
- [C++ Template Instantiation](https://en.cppreference.com/w/cpp/language/templates)
