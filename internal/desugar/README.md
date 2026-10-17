# AST Desugaring Implementation

This package implements AST desugaring for the Qoqi programming language, transforming complex syntactic constructs into simpler, more primitive forms.

## Overview

The desugaring phase transforms:
- **For loops** → While loops with explicit iteration
- **Match statements** → If-else chains
- **Short-circuit operators** (`&&`, `||`, `??`) → kept as they are; IR generation lowers them
- **Null coalescing** (`??`) → Conditional assignments

This simplification reduces complexity for later compilation stages (closure analysis, IR generation) while preserving program semantics.

## Key Design Decisions

### Short-Circuit Operators Are Left to IR Generation

Short-circuit operators (`&&`, `||`, `??`) are **not rewritten** by the
desugarer. Their operands are desugared; the operator stays in the tree, and
IR generation lowers it to branches (see `docs/compilation/ir_generation.md`), wherever it
appears: conditions, `let` initializers, return values, arguments.

An earlier version rewrote them into statements with temporaries:

```rust
// Source                         // What the old rewrite produced
if a != null && a.ready {         let __tmp_0 = a != null;
    use(a);                       let __tmp_1 = false;
}                                 if __tmp_0 { __tmp_1 = a.ready; }
                                  if __tmp_1 { use(a); }
```

That form is equivalent at runtime, but it destroys information. The program
is type checked again after desugaring, and those types drive code generation
(which function a method call targets). In the rewritten form nothing says
that `a` is non-null inside the body any more: the condition is a temporary
Bool. The checker then cannot narrow `a`, and code generation has to guess.
Keeping the operators keeps the condition readable for the checker.

The desugarer still warns (`E4004`) about operators nested five or more levels
deep in a condition or a `let` initializer.

## Architecture

### Main Components

- **`desugar.go`**: Core desugarer, orchestrates transformations
- **`for_loop.go`**: For-loop → while-loop transformation
- **`match.go`**: Match statement → if-else transformation
- **`binary_expr.go`**: Desugars the operands of binary expressions
- **`errors.go`**: Error reporting with rich diagnostics and suggestions

### Data Structures

```go
type Desugarer struct {
    prog         *ast.Program
    typeInfo     *types.Info
    resolveTable *resolve.Table
    errs         *errors.Handler
    tmpCounter   int
    info         *DesugarInfo
}

type DesugarInfo struct {
    Mapping map[ast.Node]ast.Node  // Original → Desugared
    TmpVars map[string]string       // Variable → Purpose
}
```

## Transformations

### 1. For-Loop Desugaring

```rust
// Before
for item in collection {
    body
}

// After
{
    let __iter_0 = 0;
    let __len_0 = collection.length;
    while __iter_0 < __len_0 {
        let item = collection[__iter_0];
        __iter_0 = __iter_0 + 1;
        body
    }
}
```

The increment precedes the body so that a `continue` in the body cannot skip it.
An iterable that is not a plain variable is first stored in a temporary, so it is
evaluated once.

**Implementation**: [for_loop.go:20](./for_loop.go#L20)

**Features**:
- Generates unique iterator (`__iter_N`) and length (`__len_N`) variables
- Preserves original loop variable name
- Updates type information for generated variables
- Handles both arrays and strings (and `Any`, checked when the loop runs)

### 2. Match Statement Desugaring

```rust
// Before
match x {
    case 1: { action1(); }
    case 2: { action2(); }
    default: { defaultAction(); }
}

// After
if x == 1 {
    action1();
} else if x == 2 {
    action2();
} else {
    defaultAction();
}
```

**Implementation**: [match.go:51](./match.go#L51)

**Features**:
- Handles value matching (literals, expressions)
- Type patterns (`case Int:`) become type tests (`value is Int`), which let the checker narrow the matched variable inside the case
- Generates temporary variables for complex matched expressions
- Preserves pattern order and semantics

### 3. Short-Circuit Operators

Not transformed: the operands are desugared and the operator is kept. See
"Short-Circuit Operators Are Left to IR Generation" above.

**Implementation**: [binary_expr.go](./binary_expr.go)

## Type Information Management

The desugarer maintains type information for generated nodes:

```go
// Iterator variables get Int type
iterIdent := d.createIdent(iterVar, pos)
d.typeInfo.Types[iterIdent] = types.Int

// Result variables inherit expression types
if exprType, ok := d.typeInfo.Types[expr]; ok {
    d.typeInfo.Types[tmpVarExpr] = exprType
}
```

This ensures type-checking passes can still work with desugared code.

## Usage

```go
import (
    "github.com/qoqiflow/qoqi/internal/desugar"
    "github.com/qoqiflow/qoqi/internal/ast"
    "github.com/qoqiflow/qoqi/internal/types"
    "github.com/qoqiflow/qoqi/internal/resolve"
    "github.com/qoqiflow/qoqi/internal/errors"
)

// After monomorphization, type checking, and name resolution:
desugarer := desugar.NewDesugarer(prog, typeInfo, resolveTable, errHandler)
desugaredProg, desugarInfo := desugarer.Desugar()

// Use desugaredProg for further compilation stages
// Use desugarInfo for debugging (mapping original → desugared nodes)
```

## Error Reporting

The desugarer provides comprehensive error diagnostics with helpful context and suggestions:

### Error Codes

| Code | Description | Example |
|------|-------------|---------|
| **E4001** | Unsupported Iterable | `for x in 42 { }` - can't iterate over Int |
| **E4002** | Invalid Match Pattern | Unsupported pattern in match statement |
| **E4003** | Missing Type Info | Type information missing (internal error) |
| **E4004** | Nested Short-Circuit | Warning for deeply nested `&&`/`||` |
| **E4005** | Unsupported Match Type | Complex type patterns not supported |
| **E4006** | Enum Variant Extraction | Invalid enum variant pattern |

### Example Error Output

```
[ERROR] E4001: Cannot iterate over type 'int'
  --> src/main.qi:10:5
   10 | for x in count {
      |          ^^^^^
   = Suggestion: Only arrays and strings are iterable. Consider using a while loop or converting to an array.
```

### Validation Checks

The desugarer validates:
- **For loops**: Iterable type must be array or string
- **Match patterns**: Must be literals, identifiers, or type names
- **Type information**: All expressions must have type info from previous phases
- **Complexity**: Warns about deeply nested short-circuit operators (depth ≥ 5)

### Error Recovery

The desugarer continues processing after errors to report multiple issues:
- Invalid patterns in match statements don't stop validation of other patterns
- Type errors are collected and reported together
- Warnings don't prevent desugaring from completing

## Testing

Run tests:
```bash
go test ./internal/desugar/... -v
```

Test coverage includes:
- For loop desugaring (simple, nested, with break/continue)
- Match statement desugaring (value, type, enum matching)
- Short-circuit operators are kept, their operands desugared
- Type patterns become type tests
- Edge cases (empty programs, complex nesting)
- **Error reporting** (invalid iterables, missing type info, invalid patterns)
- **Warnings** (deeply nested expressions)

## Integration with Compilation Pipeline

Position in pipeline:
```
Source → Parse → Resolve → TypeCheck → Monomorphize → **Desugar** → ClosureAnalysis → IRGen → Bytecode
```

**Inputs**:
- Monomorphized AST (no generic type parameters)
- Complete type information
- Name resolution table

**Outputs**:
- Simplified AST (while loops only, no match)
- DesugarInfo for debugging

## Performance

- **Time complexity**: O(n) where n = AST size (single-pass traversal)
- **Space complexity**: O(n) for temporary node storage
- **Code size impact**: For loops expand 3-4x, match expands linearly with cases

The IR optimizer can eliminate many temporary variables introduced during desugaring.

## Future Extensions

Potential additions:
- String interpolation → concatenation
- List comprehensions → for loops
- Optional chaining (`?.`) → explicit null checks
- Pattern matching with destructuring
- Range-based for loops

## References

See [docs/compilation/desugaring.md](../../docs/internals/compilation/desugaring.md) for the full specification.
