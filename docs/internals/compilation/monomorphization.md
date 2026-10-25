# Monomorphization Specification

This document details the design and implementation of the Monomorphization phase in the Qoqi compiler.

## Objective

Monomorphization is the process of generating concrete, specialized versions of generic functions and types for each unique set of type arguments used in the program. This transformation eliminates all generic type parameters, producing a fully concrete AST that can be lowered to IR without any polymorphism.

## Motivation

Qoqi uses **monomorphization** (similar to Rust, C++, and other statically-typed languages with generics) rather than runtime polymorphism for several reasons:

1. **Performance**: Each instantiation is optimized for specific types with no runtime type dispatch overhead
2. **Simplicity**: The VM doesn't need to handle polymorphic types or generic dispatch
3. **Type Safety**: All type relationships are resolved at compile time
4. **Code Size Trade-off**: Acceptable duplication of code in exchange for performance and simplicity

### Example

Given this generic function:
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

Monomorphization produces:
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

## Input and Output

**Input**:
- `*ast.Program` (from type checking)
- `*types.Info` (type information for all expressions)
- `*resolve.Table` (name resolution information)

**Output**:
- `*ast.Program` (transformed with all generics instantiated)
- `*MonomorphizationInfo` (mapping of original generic declarations to their monomorphized instances)

## Algorithm Overview

The monomorphization process consists of four main phases:

### Phase 1: Instantiation Discovery

**Goal**: Find all uses of generic functions and types with concrete type arguments.

**Process**:
1. Walk the entire AST
2. For each call to a generic function:
   - Record the call site
   - Extract the type arguments (explicit or inferred)
   - Create an instantiation key: `(FnName, [TypeArg1, TypeArg2, ...])`
3. For each use of a generic struct/enum:
   - Record the usage site
   - Extract the type arguments
   - Create an instantiation key: `(TypeName, [TypeArg1, TypeArg2, ...])`

**Data Structure**:
```go
type InstantiationKey struct {
    BaseName  string
    TypeArgs  []types.Type
}

type Instantiation struct {
    Key       InstantiationKey
    Decl      ast.Node  // Original generic declaration
    UseSites  []ast.Node
}
```

### Phase 2: Dependency Resolution

**Goal**: Handle transitive dependencies where one generic function calls another.

**Process**:
1. For each known instantiation, find the instantiations its body needs: if `foo<T>` calls `bar<T>`, then `foo<Int>` requires `bar<Int>`. Type parameters are substituted wherever they occur in the type arguments (`bar<[T]>` becomes `bar<[Int]>`).
2. Repeat for the ones just found, until nothing new turns up.
3. Instances are generated in the order they were found, which is the same on every run.

Instances that need each other, or themselves, are not a problem: `sorted<T>` calling `sorted<T>` is recursion, and `sorted<Int>` is one instance however often it is needed. What cannot be compiled is a function whose every instance needs a *new*, larger one. That shows as a chain of new instantiations that does not end; it is cut off after 100 links and reported as `E3002`.

**Example**:
```rust
fn map<T, U>(xs: [T], f: fn(T) -> U) -> [U] { ... }
fn double(x: Int) -> Int { return x * 2; }

fn main() -> [Int] {
    let nums = [1, 2, 3];
    return map<Int, Int>(nums, double);  // Instantiates map_Int_Int
}
```

### Phase 3: Code Generation

**Goal**: Generate specialized versions of generic declarations.

**Process**:

For each instantiation:

1. **Clone the AST** of the generic declaration
2. **Create a unique name**: `original_name` + `_` + mangled type args
   - Example: `identity<Int>` → `identity_Int`
   - Example: `Vec<Int>` → `Vec_Int`
   - Example: `map<Int, String>` → `map_Int_String`
3. **Substitute type parameters** throughout the cloned AST:
   - Replace all `TypeParam` nodes with concrete types
   - Update type annotations in parameters, return types, and local variables
4. **Update the symbol table** to register the new concrete function/type
5. **Add the new declaration** to the program

**Type Substitution Details**:
```go
type TypeSubstitution map[string]types.Type

func (s TypeSubstitution) Apply(t types.Type) types.Type {
    switch typ := t.(type) {
    case *types.TypeParam:
        if concrete, ok := s[typ.Name]; ok {
            return concrete
        }
        return t
    case *types.ArrayType:
        return types.NewArray(s.Apply(typ.ElementType))
    case *types.FunctionType:
        params := make([]types.Type, len(typ.Params))
        for i, p := range typ.Params {
            params[i] = s.Apply(p)
        }
        return types.NewFunction(params, s.Apply(typ.ReturnType))
    case *types.StructType:
        // Handle struct fields recursively
        // ...
    default:
        return t
    }
}
```

### Phase 4: Call Site Rewriting

**Goal**: Update all call sites to use the monomorphized versions.

**Process**:

1. For each generic function call:
   - Replace the function name with the mangled monomorphized name
   - Remove type arguments from the call expression
2. For each generic type instantiation:
   - Replace the type reference with the concrete type name
3. Update the resolution table to point to new declarations

**Example Transformation**:
```rust
// Before
let result = identity<Int>(42);

// After
let result = identity_Int(42);
```

**Generic structs are not rewritten.** A literal `Box<Int> { value: 1 }` keeps
its name and type arguments, and so does every type annotation mentioning
`Box<Int>`. The reasons:

*   The rest of the program refers to the struct as `Box<...>` (parameter and
    variable types, `impl<T> Box<T>`). Renaming only the literal to `Box_Int`
    would make the value a different type from every place it is used, and
    its methods, which belong to `Box`, would not be found.
*   Nothing is gained at runtime: a struct value does not carry its type
    arguments, so one `Box` serves all instantiations. Likewise the methods
    of `impl<T> Box<T>` are compiled once, not per instantiation, except
    one that casts to or tests a type parameter (below).

The concrete struct declarations (`Box_Int`) are still generated in phase 3
and appended to the program; they are currently unused. Generic *functions*
are rewritten as described above.

**Type arguments come from the checker.** A call written without type
arguments, `first([1, 2])`, has them inferred during type checking and written
into the AST (`first<Int>([1, 2])`), so by the time monomorphization runs every
call it can instantiate is explicit. `E3001` remains for calls where neither
the arguments nor the expected result type determine a type parameter.

**Function expressions and casts** are traversed: a generic call inside a
lambda is discovered and rewritten, and the type in `value as T` is
substituted when a generic function is instantiated.

**Methods that cast to their type parameters** (`internal/monomorph/methods.go`)
are made again for each type they are called with, since `value as T` in
`impl<T> Box<T> { fn get() -> T }` cannot be checked once for every `T`. The
checker marks such a call with the receiver's type (`MemberExpr.Receiver`),
and the pass clones the method with the impl's parameters and the method's own
replaced (`get$Int`), then points the call at the copy. A copy may itself call
generic functions or other such methods, so the passes run again until nothing
new is made (at most 16 rounds). Methods of a trait impl are not made again:
a call through the trait does not know the type, so a cast to their type
parameters is refused (`E2021`).

## Name Mangling Scheme

To generate unique names for monomorphized instances:

**Format**: `{base_name}_{type1}_{type2}_..._typeN}`

**Rules**:
1. Built-in types use their name: `Int`, `Float`, `String`, `Bool`
2. Arrays use `Array_{element_type}`: `[Int]` → `Array_Int`
3. Structs use their name: `Point` → `Point`
4. Generic structs flatten: `Vec<Int>` → `Vec_Int`
5. Functions encode params and return: `fn(Int) -> String` → `Fn_Int_String`
6. Nested generics flatten recursively: `Vec<Vec<Int>>` → `Vec_Vec_Int`

**Collision Handling**: If two different generic functions have the same base name and type arguments, they will collide. This is a name resolution error that should be caught earlier.

## Implementation Structure

### Package: `internal/monomorph`

```go
package monomorph

type Monomorphizer struct {
    prog        *ast.Program
    typeInfo    *types.Info
    resolvTable *resolve.Table
    errs        *errors.Handler

    // Tracks all discovered instantiations
    instantiations map[InstantiationKey]*Instantiation

    // Tracks generated monomorphic functions/types
    generated map[InstantiationKey]ast.Node

    // Original generic declarations
    generics map[string]ast.Node
}

func NewMonomorphizer(
    prog *ast.Program,
    typeInfo *types.Info,
    resolvTable *resolve.Table,
    errs *errors.Handler,
) *Monomorphizer

func (m *Monomorphizer) Monomorphize() (*ast.Program, *MonomorphizationInfo)
```

### Key Functions

```go
// Phase 1: Discovery
func (m *Monomorphizer) discoverInstantiations() error

// Phase 2: Dependencies
func (m *Monomorphizer) resolveDependencies() ([]InstantiationKey, error)

// Phase 3: Generation
func (m *Monomorphizer) generateInstance(key InstantiationKey) (ast.Node, error)
func (m *Monomorphizer) cloneAndSubstitute(decl ast.Node, subst TypeSubstitution) ast.Node

// Phase 4: Rewriting
func (m *Monomorphizer) rewriteCallSites() error

// Utilities
func (m *Monomorphizer) mangleName(baseName string, typeArgs []types.Type) string
func (m *Monomorphizer) extractTypeArgs(node ast.Node) []types.Type
```

## Error Handling

### E3001: Unbounded Generics

**Description**: Generic function/type is used but type arguments cannot be inferred.

**Example**:
```rust
fn identity<T>(x: T) -> T { return x; }

fn main() -> Void {
    // Error: Cannot infer type T
    let x = identity(42);  // Missing explicit type argument
}
```

**Error Message**: "Cannot infer type arguments for generic function 'identity'. Provide explicit type arguments: identity<T>(...)"

### E3002: Infinite Instantiation

**Description**: A generic function whose instances each require another instance with a larger type, without end.

**Example**:
```rust
fn recurse<T>(x: T) -> T {
    return recurse<[T]>([x]);  // Infinite: T -> [T] -> [[T]] -> ...
}
```

A generic function that calls itself with the *same* type arguments is ordinary recursion and compiles:
```rust
fn count<T>(items: [T], from: Int) -> Int {
    if from >= items.len() { return 0; }
    return 1 + count<T>(items, from + 1);
}
```

**Error Message**: "Generic function 'recurse' causes infinite instantiation. Detected cycle in type argument expansion."

### E3003: Type Argument Mismatch

**Description**: Number of type arguments doesn't match number of type parameters.

**Example**:
```rust
fn pair<T, U>(x: T, y: U) -> (T, U) { ... }

fn main() -> Void {
    // Error: Expected 2 type arguments, got 1
    let p = pair<Int>(1, 2);
}
```

**Error Message**: "Generic function 'pair' expects 2 type arguments, but got 1"

**Note on Duplicate Instantiations**: Multiple calls to the same generic with the same type arguments is normal and expected behavior. The monomorphizer will generate only **one** concrete instance and reuse it for all call sites:

```rust
fn identity<T>(x: T) -> T { return x; }

fn main() -> Int {
    let a = identity<Int>(1);
    let b = identity<Int>(2);
    let c = identity<Int>(3);
    // All three calls use the same identity_Int function
    return a + b + c;
}
```

This is an implementation detail handled by the instantiation cache - the monomorphizer tracks which instances have already been generated and reuses them.

## Type Inference for Generics

In many cases, type arguments can be inferred from usage context:

```rust
fn identity<T>(x: T) -> T { return x; }

fn main() -> Int {
    // Explicit: identity<Int>(42)
    // Inferred: identity(42) - T inferred as Int from argument type
    let x = identity(42);
    return x;
}
```

**Inference Algorithm**:
1. Collect constraints from argument types
2. Unify type parameters with concrete types
3. Propagate constraints through the function signature
4. If all type parameters can be resolved, use inferred types
5. Otherwise, require explicit type arguments

**Implementation**: Type inference is performed during type checking, before monomorphization. The type checker should annotate generic calls with their inferred type arguments.

## Interaction with Other Phases

### Input from Type Checking

The type checker must:
1. Validate that generic declarations are well-formed
2. Infer type arguments for generic calls where possible
3. Record the inferred/explicit type arguments in `types.Info`
4. Ensure trait bounds are satisfied (future feature)

### Output to IR Generation

After monomorphization:
1. The AST contains only concrete types
2. All function calls are to concrete functions
3. No `TypeParam` nodes remain in the tree
4. IR generation can assume all types are known

## Testing Strategy

### Unit Tests

1. **Basic Instantiation**:
   - Generic function with single type parameter
   - Generic struct with single type parameter
   - Multiple instantiations of the same generic

2. **Multiple Type Parameters**:
   - Function with 2+ type parameters
   - Different orderings and combinations

3. **Nested Generics**:
   - `Vec<Vec<Int>>`
   - Generic function returning generic type

4. **Transitive Dependencies**:
   - Generic function A calls generic function B
   - Verify both are instantiated correctly

5. **Type Inference**:
   - Inferred type arguments from context
   - Partial inference with some explicit args

### Integration Tests

1. **End-to-End**:
   - Source code with generics → Parse → Resolve → Type Check → Monomorphize → Verify AST
   - Compare generated function names and signatures

2. **Complex Programs**:
   - Generic standard library functions (map, filter, fold)
   - Generic data structures (Vec, Option, Result)

### Error Tests

1. Verify all error codes are triggered appropriately
2. Test error recovery (continue after errors to report multiple issues)

## Performance Considerations

### Compile-Time Impact

Monomorphization can significantly increase:
1. **Compilation time**: Proportional to number of instantiations
2. **Memory usage**: Cloning AST nodes for each instance
3. **Binary size**: Each instantiation adds code

**Mitigations**:
1. **Instantiation Caching**: Reuse previously generated instances
2. **Lazy Instantiation**: Only instantiate what's actually used
3. **Deduplication**: Detect structurally identical functions

### Runtime Impact

Monomorphization improves runtime performance:
1. **No virtual dispatch**: Direct function calls
2. **Optimization**: Each instance can be optimized independently
3. **Inlining**: Compiler can inline monomorphized functions more easily

## References

- [Rust RFC 1210: Specialization](https://rust-lang.github.io/rfcs/1210-impl-specialization.html)
- [Swift Generics Manifesto](https://github.com/apple/swift/blob/main/docs/GenericsManifesto.md)
- [C++ Template Instantiation](https://en.cppreference.com/w/cpp/language/templates)
