# Name Resolution Specification

This document details the design and implementation of the Name Resolution phase in the Qoqi compiler.

## Objective

The Name Resolution phase is responsible for linking every identifier usage (variable, function call, type name) to its corresponding declaration. It ensures that the program follows the scoping rules of the language and detects errors such as undefined variables or invalid redeclarations.

## Data Structures

The core of the resolution process relies on a Symbol Table that manages scopes and symbols.

### 1. Symbol

A `Symbol` represents a named entity in the program.

```go
type SymbolKind int

const (
    SymbolVar SymbolKind = iota
    SymbolFunc
    SymbolStruct
    SymbolEnum
    SymbolTrait
    SymbolTypeParam
)

type Symbol struct {
    Name string
    Kind SymbolKind
    Decl ast.Node // Pointer to the AST node where it is declared (e.g., *ast.LetStmt, *ast.FnDecl)
    // Optional: Type information if available early, or location info
}
```

### 2. Scope

A `Scope` represents a lexical region where names are valid. Scopes are nested.

```go
type Scope struct {
    Parent  *Scope
    Symbols map[string]*Symbol
}

func NewScope(parent *Scope) *Scope
func (s *Scope) Insert(name string, sym *Symbol) error
func (s *Scope) Lookup(name string) *Symbol
```

### 3. Resolution Table (Output)

The result of this phase is a mapping that links AST nodes (specifically `*ast.Ident`) to their resolved `Symbol`.

```go
type Table struct {
    // Maps a usage identifier to its definition symbol
    Defs map[*ast.Ident]*Symbol
    
    // Maps a declaration node to the scope it introduces (optional, useful for type checking)
    Scopes map[ast.Node]*Scope
}
```

## Scoping Rules

1.  **Lexical Scoping**: Inner scopes can access variables from outer scopes.
2.  **Shadowing**:
    *   **Local Shadowing**: Allowed. A variable declared in an inner block can shadow a variable with the same name in an outer block.
    *   **Global Shadowing**: Disallowed? (TBD: Usually top-level names must be unique).
3.  **Forward References**:
    *   **Top-Level**: Allowed. Functions and types can be used before they are defined in the file.
    *   **Local Variables**: Disallowed. A variable must be declared before use.

## Algorithm

The resolution process is performed in two passes to support forward references for top-level declarations.

### Builtins

Before either pass runs, the global scope is seeded with the host-provided
functions (`print`, `println`, `toString`, `push`). They are declared in
`internal/resolve/builtins.go` as ordinary `ast.FnDecl` nodes without a body,
so the rest of the pipeline treats them like user functions: the type checker
reads their signatures, and a user declaration with the same name is reported
as a redeclaration (`E1002`). `resolve.IsBuiltin(name)` tells later stages
which calls go to the host.

### Pass 1: Top-Level Declaration Collection

Traverse the `ast.Program` to collect all top-level declarations into the Global Scope.

*   Register `struct`, `enum`, `trait` names.
*   Register `fn` names.
*   Imports are gone by this point: [module loading](module-loading.md) has already merged all files into one program with unique names.
*   **Error Check**: Detect duplicate top-level names.

### Pass 2: Body Resolution

Traverse the AST again to resolve function bodies and variable initializers.

*   **Scopes**:
    *   Enter a new scope when entering a `FnDecl` (add parameters to this scope).
    *   Enter a new scope when entering a `BlockStmt`.
    *   Enter a new scope for `ForStmt` (loop variable) and `MatchCase` (pattern variables). A name alone as the pattern of a case is not a variable: it is looked up, and must be a top-level constant (`E1005`).
    *   Enter a new scope for an `IfStmt` and a `WhileStmt`, around the condition and the block it guards, not the `else`. A pattern test (`value is Shape.Circle(r)`) that is the condition, or an operand of the `&&` chain that is, declares its names there: the operands to its right and the block see them. A chain of `&&` with pattern tests anywhere else gets a scope of its own. A pattern test in any other place (under `!`, `||`, as a value) declares nothing, and each name it writes is `E1003`.
    *   Enter a new scope when entering a `FnExpr` (anonymous function); its parameters are added to it. The enclosing scopes stay visible, which is what lets a lambda refer to the variables it captures.
    *   Each method of an `ImplDecl` is resolved inside a scope that declares `self`. Outside of methods `self` is an undefined identifier.
*   **Declarations**:
    *   When encountering `LetStmt`, add the variable to the current scope.
    *   Ensure the initializer expression is resolved *before* the variable is added (prevents `let x = x;`).
*   **Usages**:
    *   When encountering an `Ident` (in an expression context), look it up in the current scope chain.
    *   If found, record the link in the `Table`.
    *   If not found, report an "Undefined identifier" error.
*   **Array literals**: every element expression is resolved.
*   **Types**: names inside `[T]`, `T?`, `...T`, unions and function types are checked, not just the outermost name. `Self` is a builtin type name.
*   **Member Access**:
    *   `MemberExpr` (e.g., `obj.prop`) is **NOT** fully resolved here. We resolve `obj`. The property `prop` depends on the type of `obj`, which is determined during Type Checking.

## Implementation Details

The implementation will reside in `internal/resolve`.

### `Resolver` Struct

The resolver should utilize the centralized error handling system.

```go
import "qoqi/internal/errors"

type Resolver struct {
    table   *Table
    current *Scope
    errs    *errors.Handler
}
```

### Visitor Pattern

Use a recursive walk or a visitor pattern to traverse the AST.

*   `VisitProgram`
*   `VisitFnDecl`
*   `VisitBlockStmt`
*   `VisitLetStmt`
*   `VisitExpr`...

## Error Handling

Errors should be reported using the `internal/errors` package, ensuring rich diagnostics with error codes and source context.

*   **E1001 Undefined Identifier**: "Variable 'x' is not defined."
    *   *Suggestion*: "Did you mean 'y'?" (if close match found).
*   **E1002 Redeclaration**: "Variable 'x' is already declared in this scope."
    *   *Context*: Point to the previous declaration.
*   **E1003 Unusable name**: a pattern after `is` names a value where no code could use the name. *Suggestion*: write `_`.
*   **E1005 Not a constant**: a name alone as the pattern of a case, `case limit:`, is looked up rather than declared, and must be a top-level constant, which the case compares with. A variable, a parameter, a local `const`, a function or an unknown name is this error. *Suggestion*: a condition, `case Int if value == limit:`.

The `Resolver` should continue processing after an error to report as many issues as possible (error recovery).

## Integration

*   **Input**: The `Resolver` takes the `*ast.Program` produced by the Parser.
*   **Output**: Returns a `*resolve.Table` which is passed to the Type Checker.
