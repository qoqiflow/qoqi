# AST Desugaring Specification

This document details the design and implementation of the AST Desugaring phase in the Qoqi compiler.

## Objective

The Desugaring phase transforms complex syntactic constructs into simpler, more primitive forms that are easier to compile to IR. This simplification reduces the complexity of later compilation stages while preserving the semantics of the original program.

## Motivation

Desugaring provides several benefits:

1. **Simplified IR Generation**: By transforming complex constructs into simpler ones, IR generation becomes more straightforward and less error-prone
2. **Reduced Backend Complexity**: The IR generator only needs to handle a small set of primitive operations
3. **Easier Optimization**: Simpler constructs are easier to analyze and optimize
4. **Consistent Semantics**: Complex features are defined in terms of their simpler equivalents, ensuring consistent behavior

### Example

Given this high-level code:
```rust
fn sum_evens(nums: [Int]) -> Int {
    let total = 0;
    for num in nums {
        if num % 2 == 0 {
            total = total + num;
        }
    }
    return total;
}
```

After desugaring:
```rust
fn sum_evens(nums: [Int]) -> Int {
    let total = 0;
    let __iter_0 = 0;
    let __len_0 = __len(nums);
    while __iter_0 < __len_0 {
        let num = nums[__iter_0];
        if num % 2 == 0 {
            total = total + num;
        }
        __iter_0 = __iter_0 + 1;
    }
    return total;
}
```

## Input and Output

**Input**:
- `*ast.Program` (from monomorphization)
- `*types.Info` (type information for all expressions)
- `*resolve.Table` (name resolution information)

**Output**:
- `*ast.Program` (transformed with all sugar removed)
- `*DesugarInfo` (mapping of original nodes to desugared nodes for debugging)

## Desugaring Transformations

### 1. For-In Loops → While Loops

**Goal**: Transform `for-in` loops over arrays into `while` loops with explicit index management.

**Transformation**:

```rust
// Before
for item in collection {
    body
}

// After (for arrays)
{
    let __iter_N = 0;
    let __len_N = __len(collection);
    while __iter_N < __len_N {
        let item = collection[__iter_N];
        __iter_N = __iter_N + 1;
        body
    }
}
```

With an index, `for i, item in collection`, the position is copied into `i`
after the element is read and before the increment:

```rust
let item = collection[__iter_N];
let i = __iter_N;
__iter_N = __iter_N + 1;
```

The type checker has already rejected any assignment to `i` or `item`, so
these declarations are only ever read.

The increment comes **before** the body. A `continue` in the body jumps back to
the loop condition; with the increment at the end it would be skipped and the
loop would never advance.

When the iterable is anything but a plain variable, it is stored in a temporary
first so that it is evaluated exactly once:

```rust
// Before
for item in loadItems() {
    body
}

// After
{
    let __tmp_N = loadItems();
    let __iter_M = 0;
    let __len_M = __len(__tmp_N);
    while __iter_M < __len_M {
        let item = __tmp_N[__iter_M];
        __iter_M = __iter_M + 1;
        body
    }
}
```

**For strings**:
```rust
// Before
for char in str {
    body
}

// After
{
    let __iter_N = 0;
    let __len_N = __len(str);
    while __iter_N < __len_N {
        let char = str[__iter_N];  // char extraction
        __iter_N = __iter_N + 1;
        body
    }
}
```

An iterable whose static type is `Any` (for example a value read from a generic
container) is accepted as well; whether it really is an array or a string is
checked when the loop runs.

**Over numbers**, `for i in low..high`, nothing is read from a collection.
When the body cannot tell `i` from the number the loop counts with (no
function made in it reads `i`, no deferred statement does, nothing assigns
to it), the loop counts with `i` itself, and adds one after each iteration:

```rust
// Before
for i in low..high {
    body
}

// After
{
    let i = low;
    let __len_N = high;
    while i < __len_N { body } update { i = i + 1; }
}
```

The update is part of the `while` the desugarer makes (`WhileStmt.Update`;
source has no form of it): it runs after the body and after a `continue`,
before the condition is asked again. A turn of `for i in 0..n { total += i; }`
is four instructions: the test, the sum, the increment, the jump back.

A range that includes its end, `low..=high`, tests for the last number
before counting past it, so that a loop can end at the largest Int; the
first test is made once, before the loop, which has no condition of its own
(`WhileStmt.Cond` is nil) and is left by the `break` of its update:

```rust
{
    let i = low;
    let __len_N = high;
    if i <= __len_N {
        while { body } update { if i == __len_N { break; } i = i + 1; }
    }
}
```

Otherwise, and with a position (`for n, i in low..high`), the loop counts
with a number of its own, `__iter_N`, the same way, and the body starts with
`let i = __iter_N;`: each iteration has an `i` of its own for the functions
made in it to keep. The check looks at names only, so a variable of the same
name inside the body counts too: a loop is never wrongly made to count with
`i`, and may keep a counter it did not need.

**Implementation Details**:
- Generate unique variable names for iterator and length (`__iter_N`, `__len_N`)
- Wrap the entire transformation in a block to ensure proper scoping
- Preserve the loop variable name from the original `for` statement
- `break` and `continue` statements remain unchanged, which is why the increment precedes the body
- Update type information for generated variables

### 2. Match Statements → If-Else Chains

**Goal**: Transform `match` expressions into nested `if-else` statements.

**Simple Value Matching**:
```rust
// Before
match x {
    case 1: { action1(); }
    case 2: { action2(); }
    case 3: { action3(); }
    default: { defaultAction(); }
}

// After
if x == 1 {
    action1();
} else if x == 2 {
    action2();
} else if x == 3 {
    action3();
} else {
    defaultAction();
}
```

**Type Matching**:
```rust
// Before
match value {
    case Int: { handleInt(value + 1); }
    case String: { handleString(value.len()); }
    default: { handleOther(); }
}

// After
if value is Int {
    handleInt(value + 1);
} else if value is String {
    handleString(value.len());
} else {
    handleOther();
}
```

A type pattern becomes a type test (`ast.TypeTestExpr`), the same node the
`is` operator produces. When the matched expression is a plain variable it is
tested directly, without a temporary: `value is Int` then tells the type
checker that `value` is an `Int` inside the case, which is what lets the case
body use it as one. Any other expression is first stored in a temporary, so
that it is evaluated once.

Patterns that are types: the builtin types, `[T]`, `T?`, function types,
generic instantiations (`Box<Int>`), and the bare name of a struct or trait.
The last one is decided by the parser once the whole file is read
(see [parsing.md](parsing.md)).

**Enum Variant Matching**:
```rust
// Before
match result {
    case Ok(val): { return val; }
    case Err(err): { print(err); return 0; }
}

// After
{
    let __match_tmp_N = result;
    if __is_variant(__match_tmp_N, "Ok") {
        let val = __extract_variant(__match_tmp_N, 0);
        return val;
    } else if __is_variant(__match_tmp_N, "Err") {
        let err = __extract_variant(__match_tmp_N, 0);
        print(err);
        return 0;
    }
}
```

**Implementation Details**:
- Generate a temporary variable to store the matched expression
- Convert each case into an equality or type check
- Chain cases using `if-else`
- For exhaustive matches (all cases covered), omit the final `else`
- For enum variants, introduce runtime helper functions (`__is_variant`, `__extract_variant`)
- Preserve source location information for error reporting

**Patterns accepted today**: literals (including negative numbers such as
`case -1:`), constants, types (above) and `default:`. A `default:` case has
no pattern and becomes the final `else`. A constant, `case HAND_OVER:`, is
an identifier the resolver found to be a top-level constant; it is compared
like a literal, `value == HAND_OVER`, and the constant is then replaced by
its value as everywhere else. A name that is not a type, a variant or a
constant (`case limit:`, a variable) is rejected by the resolver with
`E1005`; other expressions by the compiler driver with `E7001` (see
[pipeline.md](pipeline.md)).

#### Patterns on enum variants

A case on a variant of an enum ([enums.md](../../reference/language/enums.md)) becomes a test and, at the start of the case, a `let` for each name the pattern binds:

```rust
match shape {
    case Shape.Rect(w, 0.0): { flat(w); }
    case Shape.Circle(r): { round(r); }
    case Shape.Empty: { }
}
```
```rust
if (shape is Shape.Rect) && (shape.Rect#1 == 0.0) {
    let w = shape.Rect#0;
    flat(w);
} else if (shape is Shape.Circle) {
    let r = shape.Circle#0;
    round(r);
} else {
}
```

`shape is Shape.Rect` and `shape.Rect#1` are the AST nodes `VariantTestExpr` and `VariantFieldExpr` (they have no syntax of their own; this is how they print). The rules:

*   A value of a variant is only read after the test that the value is that variant. The tests are joined with `&&`, in the order of the pattern, which IR generation evaluates left to right and stops at the first that fails.
*   A pattern inside a pattern is lowered the same way, with the outer value's field as what it tests: `Expr.Neg(Expr.Num(n))` gives `e is Expr.Neg`, `e.Neg#0 is Expr.Num`, then `let n = e.Neg#0.Num#0;`.
*   `_` produces nothing.
*   When the type checker marked the match as handling every value (`MatchStmt.Exhaustive`), the last case needs no test and becomes the final `else`. That is one test fewer, and it makes plain to the stages below that one of the cases always runs.
*   As for the other kinds of pattern, a matched value that is not a plain variable is put in a temporary first, so it is worked out once.

#### Values of an enum

`Shape.Circle(2.0)` is an ordinary call to the parser, which cannot know that `Shape` is an enum. Here it becomes a `VariantExpr` holding the enum, the variant, the values and, for a generic enum, the type arguments the checker worked out, written down so that the type information computed again after this stage does not have to infer them a second time. `Shape.Circle` that is not called becomes a lambda that makes the value.

#### Constants

A use of a top-level constant is replaced by its value: the expression it is declared with, in which the constants it uses are replaced in turn, so that only literals and operators remain. The declarations are dropped. The new nodes are positioned at the use, so a failure while working the value out (a division by zero) is reported where the constant is used. There is no folding: `WIDTH * 3` stays a multiplication, done when the program runs.

#### Conditions, several patterns, ranges

A case is one test in the chain, whatever its pattern is made of:

| Case | Test |
|------|------|
| `case 1 \| 2:` | `x == 1 \|\| x == 2` |
| `case 1..10:`, `case 1..=10:` | `x >= 1 && x < 10`, `x >= 1 && x <= 10` |
| `case Shape.Circle(r) if r > 1.0:` | `x is Shape.Circle && { let r = x.Circle#0; r > 1.0 }` |

The condition of a case uses the names of its pattern before the case has started, so they are declared a first time for it, in a `BlockExpr` (below), and again at the start of the body. A case with a condition is never the unconditional last `else`.

#### A match that gives a value

`let size = match shape { ... };` cannot become an `if` statement: it stands where a value does. It becomes a chain of two expressions that only desugaring makes:

*   `CondExpr{Cond, Then, Else}`: one of two values, only the chosen one evaluated;
*   `BlockExpr{Stmts, Value}`: statements, then the expression that gives their value. Without a value the statements always leave (a case written as a block ends in `return`, `break`, `continue` or `panic`).

```rust
let size = match shape {
    case Shape.Circle(r) if r > 10.0: "big";
    case Shape.Circle(_): "round";
    default: { return null; }
};
```

```
let size = cond(shape is Shape.Circle && { let r = shape.Circle#0; r > 10.0 },
                { let r = shape.Circle#0; "big" },
                cond(shape is Shape.Circle, "round", { return null; }));
```

The type checker gives a `CondExpr` the type its two sides share when the program is checked again after this stage, so nothing has to be declared for the result. IR generation compiles it like `&&`: one register written on both paths.

### 2b. Loops over a range

`for i in low..high` does not go through an array: it counts.

```rust
{
    let __iter_N: Int = low;
    let __len_N: Int = high;
    while __iter_N < __len_N {        // <= for low..=high
        let i: Int = __iter_N;
        __iter_N = __iter_N + 1;
        body
    }
}
```

`defer` is not desugared: its body is, and the statement goes on to IR generation, which knows the ways out of a block ([ir_generation.md](ir-generation.md)).

### 2c. Pattern tests: `value is Pattern`

A pattern test is a case of a match written as a condition, and is lowered with the same pieces: the tests of `lowerPattern` joined by `&&`, and a `let` for each name (`is.go`).

```rust
if shape is Shape.Rect(w, 0.0) { body } else { other }
```
```
if shape is Shape.Rect && shape.Rect#1 == 0.0 { let w = shape.Rect#0; { body } } else { other }
```

The block stays a block of its own under the names, so it may declare a variable called as one of them.

**Names in the condition.** In a chain of `&&`, what follows a test sees its names. The chain is regrouped to the right (`a && (b && c)` evaluates exactly as `(a && b) && c` does) so that everything after a test stands in one `BlockExpr` that declares them; they are declared again in front of the block:

```rust
if shape is Shape.Circle(r) && r > 1.0 && ready { body }
```
```
{
    let __is_0 = shape;
    if __is_0 is Shape.Circle && { let r = __is_0.Circle#0; r > 1.0 && ready } {
        let r = __is_0.Circle#0;
        { body }
    }
}
```

The second declaration is a different variable to the type checker that runs after this stage, so what the condition established about the name (`next != null`) would be lost on it. The first check records it (`types.Info.NarrowedNames`) and the second `let` is declared with that type. Facts about other variables pass through a `BlockExpr`: the checker looks at its value.

**The value** is read where it stands when it is a plain variable and nothing runs between the test and the reading of its names. Otherwise it is kept in a temporary `__is_N`: when it is computed, when more of the condition follows a test that names something (that code could assign the variable), and when the pattern reuses the name of the variable. The temporary is declared before the statement, which is also where a value that comes first in the condition is evaluated. Behind an `&&` a value must only be evaluated when its turn comes, so its temporary starts as `null` and is filled in the condition:

```rust
if ready && next() is Option.Some(item) { body } else { other }
```
```
{
    let __is_0: Option<Int>? = null;
    if ready && { __is_0 = next(); __is_0 is Option.Some } { let item = __is_0.Some#0; { body } } else { other }
}
```

An `else if` is an `if` in the `else`, so its temporaries are declared, and its values evaluated, only when the conditions before it failed.

**`while`.** With no temporary the loop keeps its shape: `while x is P(n) { body }` tests `x` and declares `n` in front of the body. With one, the value has to be evaluated on every turn:

```rust
while queue.take() is Option.Some(job) { body }
```
```
while true {
    let __is_0 = queue.take();
    if __is_0 is Option.Some { let job = __is_0.Some#0; { body } } else { break; }
}
```

`continue` in the body goes to the top of this loop, which evaluates the value again, as the condition of a `while` is.

**As a value** (`let round = shape is Shape.Circle(_);`, an operand of `||`, under `!`) a test is its tests, in a `BlockExpr` that declares the temporary if one is needed. Name resolution has made sure it names nothing there, or only for the rest of its own `&&` chain.

Nothing below this stage knows the form exists: IR generation sees `if`, `while`, `&&`, `VariantTestExpr`, `VariantFieldExpr` and `BlockExpr`, all of which a `match` already produced.

### 3. Optional Chaining (Future)

**Goal**: Transform optional chaining into explicit null checks.

```rust
// Before
let result = user?.address?.street;

// After
let result;
if user != null {
    let __tmp1 = user.address;
    if __tmp1 != null {
        result = __tmp1.street;
    } else {
        result = null;
    }
} else {
    result = null;
}
```

### 4. Short-Circuit Operators (`&&`, `||`, `??`)

Short-circuit operators (`&&`, `||`, `??`) are **not rewritten** by the
desugarer. Their operands are desugared; the operator stays in the tree, and
IR generation lowers it to branches (see
[ir_generation.md](ir-generation.md#short-circuit-operators)), wherever it
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

### 5. String Interpolation

`"Hello, ${name}! You are ${age}."` is concatenation, but it is not lowered here: it is the **lexer** that does it. A string with `${...}` in it is handed to the parser as the tokens of

```rust
("Hello, " + __text(name) + "! You are " + __text(age) + ".")
```

so the parser, the type checker and this stage see an ordinary expression and need no node for it. See [parsing.md](parsing.md#string-interpolation).

## Implementation Structure

### Package: `internal/desugar`

```go
package desugar

import (
    "qoqi/internal/ast"
    "qoqi/internal/types"
    "qoqi/internal/resolve"
    "qoqi/internal/errors"
)

type Desugarer struct {
    prog        *ast.Program
    typeInfo    *types.Info
    resolvTable *resolve.Table
    errs        *errors.Handler

    // Counter for generating unique temporary variable names
    tmpCounter  int

    // Mapping from original nodes to desugared nodes (for debugging)
    mapping     map[ast.Node]ast.Node
}

func NewDesugarer(
    prog *ast.Program,
    typeInfo *types.Info,
    resolvTable *resolve.Table,
    errs *errors.Handler,
) *Desugarer

func (d *Desugarer) Desugar() (*ast.Program, *DesugarInfo)
```

### Key Functions

```go
// Main entry point
func (d *Desugarer) Desugar() (*ast.Program, *DesugarInfo)

// Statement transformations
func (d *Desugarer) desugarStmt(stmt ast.Stmt) ast.Stmt
func (d *Desugarer) desugarForStmt(stmt *ast.ForStmt) ast.Stmt
func (d *Desugarer) desugarMatchStmt(stmt *ast.MatchStmt) ast.Stmt

// Expression transformations
func (d *Desugarer) desugarExpr(expr ast.Expr) ast.Expr
func (d *Desugarer) desugarBinaryExpr(expr *ast.BinaryExpr) ast.Expr
func (d *Desugarer) desugarNullCoalesce(expr *ast.BinaryExpr) ast.Expr
func (d *Desugarer) desugarShortCircuit(expr *ast.BinaryExpr) ast.Expr

// Utilities
func (d *Desugarer) generateTmpVar() string
func (d *Desugarer) wrapInBlock(stmts ...ast.Stmt) *ast.BlockStmt
func (d *Desugarer) createComparison(left, right ast.Expr, op string) *ast.BinaryExpr
func (d *Desugarer) cloneType(t *ast.Type) *ast.Type
```

### DesugarInfo Structure

```go
type DesugarInfo struct {
    // Maps original AST nodes to their desugared equivalents
    Mapping map[ast.Node]ast.Node

    // Maps generated temporary variables to their purposes (for debugging)
    TmpVars map[string]string
}
```

## Algorithm Overview

The desugaring process is a single-pass tree transformation:

### Pass: Tree Transformation

1. **Walk the AST**: Traverse the program tree recursively
2. **Identify Sugar**: Detect constructs that need desugaring
3. **Transform**: Replace sugared constructs with their desugared equivalents
4. **Recurse**: Continue desugaring nested expressions and statements
5. **Update Metadata**: Maintain type and resolution information for generated nodes

**Traversal Order**: Post-order (children first, then parent)
- This ensures that nested sugar is desugared from the inside out
- Example: `for x in arr { match x { ... } }` desugars the match first, then the for loop

## Type and Resolution Table Updates

When generating new AST nodes during desugaring:

1. **Type Information**: Generated variables and expressions need type information
   - Iterator variables: `Int` type
   - Length variables: `Int` type
   - Temporary variables: inherit type from their source expression

2. **Resolution Table**: Generated variables need entries in the resolution table
   - Create new symbols for generated variables
   - Add them to the appropriate scope
   - Link identifier references to their symbols

3. **Source Locations**: Preserve original source locations
   - Generated nodes should reference the original source location
   - This ensures error messages point to the right place
   - Use the `BaseNode` position from the original construct

### 6. Anonymous Functions

The body of a function expression is desugared like the body of a declaration,
so loops and matches inside lambdas are lowered too.

## Error Handling

Desugaring should generally not introduce new errors, as it operates on a well-typed AST. However, some validation may be necessary:

The codes below are defined in `internal/desugar/errors.go`.

| Code | Name | Raised when |
|------|------|-------------|
| `E4001` | Unsupported iterable | A `for-in` loop iterates over something that is neither an array, a string nor `Any` |
| `E4002` | Invalid match pattern | A match case pattern is malformed or cannot be desugared |
| `E4003` | Missing type info | The desugarer needs the type of an expression that the checker did not record |
| `E4004` | Nested short-circuit | Warning: `&&`/`||` nested five or more levels deep |
| `E4005` | Unsupported match type | A type pattern that cannot be lowered, e.g. a union type |
| `E4006` | Enum variant extraction | Destructuring an enum variant in a pattern |

### E4001: Unsupported Iterable

**Example**:
```rust
for x in 42 {  // Cannot iterate over Int
    print(x);
}
```

**Error Message**: "Cannot iterate over type 'int'" with the suggestion "Only arrays and strings are iterable."

**Note**: The type checker reports the same mistake first as `E2011`, so this
code is only reached for programs built without it (as in the unit tests).

### E4002: Invalid Match Pattern

**Example**:
```rust
match x {
    case 1..10: { action(); }  // Range patterns not supported yet
}
```

## Interaction with Other Phases

### Input from Monomorphization

Desugaring expects:
1. All generic types and functions have been instantiated
2. No `TypeParam` nodes remain in the tree
3. All type information is concrete and complete

### Output to Closure Analysis

After desugaring:
1. The AST contains simpler, more primitive constructs
2. Control flow is more explicit (no `for` loops, only `while`)
3. All expressions are simple (no complex operators like `&&`, `||`, `??`)
4. The closure analyzer can work with a reduced set of node types

### Impact on IR Generation

Desugaring significantly simplifies IR generation:
1. Fewer statement types to handle (`while` instead of `for`)
2. Fewer expression types to handle (no short-circuit operators)
3. More explicit control flow makes basic block construction easier
4. Temporary variables make SSA form generation straightforward

## Testing Strategy

### Unit Tests

1. **For Loop Desugaring**:
   - Simple `for-in` loop over array
   - `for-in` loop with `break`
   - `for-in` loop with `continue`
   - Nested `for` loops
   - `for-in` loop over string

2. **Match Statement Desugaring**:
   - Simple value matching
   - Type matching (union types)
   - Enum variant matching with destructuring
   - Exhaustive vs non-exhaustive matches

3. **Null Coalescing**:
   - Simple `a ?? b`
   - Nested coalescing `a ?? b ?? c`
   - Coalescing with function calls

4. **Short-Circuit Operators**:
   - `&&` in condition
   - `||` in condition
   - Nested combinations `a && b || c`
   - Short-circuit in assignments

### Integration Tests

1. **End-to-End**:
   - Source code with sugar → Parse → Resolve → Type Check → Monomorphize → Desugar → Verify AST
   - Ensure desugared code has equivalent semantics

2. **Complex Programs**:
   - Programs with multiple layers of sugar
   - Verify type information is preserved
   - Verify resolution table is updated correctly

3. **Round-Trip**:
   - Desugar code, then pretty-print it
   - Verify the output is valid Qoqi code (even if more verbose)

### Golden File Tests

For each transformation, maintain "golden files" showing:
- Input: original sugared code
- Output: expected desugared AST (as string representation)

Example:
```
Input: for x in [1, 2, 3] { print(x); }

Expected Output:
{
  let __iter_0 = 0;
  let __len_0 = 3;
  while __iter_0 < __len_0 {
    let x = array[__iter_0];
    print(x);
    __iter_0 = __iter_0 + 1;
  }
}
```

## Performance Considerations

### Compilation Impact

Desugaring adds minimal overhead:
1. **Single-pass transformation**: Linear time complexity O(n) where n = AST size
2. **Node cloning**: Some memory allocation for new nodes
3. **Symbol table updates**: O(1) insertions per generated variable

### Code Size Impact

Desugaring can increase AST size:
1. **For loops**: 1 `for` → 1 `while` + 3-4 statements (initialization, condition, increment)
2. **Match statements**: 1 `match` → n `if-else` chains
3. **Short-circuit operators**: 1 expression → 3-4 statements

**Mitigation**: IR generation and optimization can often reduce this overhead.

### Runtime Impact

Desugaring should have **no runtime impact**:
1. Desugared code has identical semantics to original
2. IR generator produces the same IR whether code is sugared or not
3. Optimizations can eliminate temporary variables

## Future Extensions

### 1. Loop Desugaring → Loop + Break

Alternative desugaring that uses an infinite loop:
```rust
// Before
for item in collection {
    body
}

// After (alternative)
{
    let __iter_N = 0;
    let __len_N = __len(collection);
    loop {
        if __iter_N >= __len_N { break; }
        let item = collection[__iter_N];
        body
        __iter_N = __iter_N + 1;
    }
}
```

### 2. Pattern Matching Optimization

For dense integer ranges, use jump tables instead of `if-else` chains:
```rust
// Before
match x {
    case 0: { a(); }
    case 1: { b(); }
    case 2: { c(); }
    case 3: { d(); }
}

// After (with jump table hint)
switch_table x {
    0 => { a(); }
    1 => { b(); }
    2 => { c(); }
    3 => { d(); }
}
```

### 3. List Comprehensions

```rust
// Before
let evens = [x * 2 for x in nums if x % 2 == 0];

// After
{
    let evens: [Int] = [];
    for x in nums {
        if x % 2 == 0 {
            evens.push(x * 2);
        }
    }
}
```

## References

- [Scala Desugaring](https://docs.scala-lang.org/scala3/reference/other-new-features/explicit-nulls.html)
- [Rust HIR (High-level IR)](https://rustc-dev-guide.rust-lang.org/hir.html) - Rust's approach to desugaring
- [Swift SIL](https://github.com/apple/swift/blob/main/docs/SIL.rst) - Swift's desugaring and lowering strategy
- [GHC Core](https://gitlab.haskell.org/ghc/ghc/-/wikis/commentary/compiler/core-syn-type) - Haskell's core language after desugaring

## Loops over a range that was kept

`for i in 0..10` counts, as shown above. A range anywhere else is a value, a `Range` of `std.ops` (module loading writes the struct literal in its place), and a loop over one is desugared to a count as well, read from the struct:

```rust
for i in numbers { body }

{
    let __tmp_N = numbers;
    let __iter_N: Int = __tmp_N.from;
    let __len_N: Int = __tmp_N.stop();     // the first number past the end
    let __tmp_M: Int = __tmp_N.by;
    while __iter_N != __len_N {
        let i: Int = __iter_N;
        __iter_N = __iter_N + __tmp_M;
        body
    }
}
```

`stop` is a whole number of steps from `from`, so the loop meets it exactly, going up or down. No array is made; `Range` implements `Iterable<Int>` only so that it is one to the type checker and to generic code.
