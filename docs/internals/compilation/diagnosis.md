# Diagnosis System Specification

The diagnosis system is responsible for collecting, formatting, and reporting errors and warnings encountered during the compilation process. It is designed to be user-friendly, providing context and actionable suggestions.

## Architecture

The system is centralized around the `internal/errors` package. A `Handler` instance is passed through the compilation pipeline, allowing each stage to report diagnostics without aborting immediately (where possible).

**Every stage reports through the handler.** A stage does not print, does not
panic on bad input and does not invent its own error type for problems in the
user's program. This is what lets the CLI, the tests and any future tool treat
all compile errors uniformly.

## Data Structures

### Diagnostic
A single unit of feedback.

*   **Severity**: `INFO`, `WARNING`, `ERROR`.
*   **Code**: A unique identifier (e.g., `E1001`) for documentation lookup.
*   **Message**: A concise description of the issue.
*   **Position**: File, Line, Column.
*   **Context**: The source code line where the error occurred.
*   **Highlights**: Specific ranges within the context to underline.
*   **Suggestion**: Optional actionable fix (e.g., "Did you mean `myVar`?").

### Handler
Collects diagnostics and manages the compilation state (e.g., determining if compilation should halt).

```go
errs := errors.NewHandler()

// Report at a token: the handler fills in the source line and the underline
errs.ReportErrorFromToken(code, message, node.Token(), sourceLines)
errs.ReportErrorWithSuggestion(code, message, suggestion, node.Token(), sourceLines)
errs.ReportWarningFromToken(code, message, node.Token(), sourceLines)

errs.HasErrors()     // true if any diagnostic has severity ERROR
errs.Diagnostics     // everything reported so far, in order
```

`sourceLines` maps 1-based line numbers to the text of each line;
`parser.SourceLines(src)` builds it. Every AST node keeps the token it was
parsed from (`node.Token()`), so a stage can point at the exact span without
tracking positions itself.

### Reporting a new error

1.  Add a constant for the code next to the others of that stage, with a
    comment saying when it is raised.
2.  Report it with `ReportErrorFromToken`, or `ReportErrorWithSuggestion` when
    there is something concrete the user can do. A suggestion should name the
    fix ("Add ';' to end the statement"), not restate the problem.
3.  Add a case to that stage's error test, asserting on the **code** and a
    fragment of the message.
4.  Add the code to the table below.

Stages that work on data without AST nodes (bytecode generation works on IR)
build a token from the recorded position; see `Generator.report` in
`internal/codegen/codegen.go`.

## Error Categories & Codes

Codes are `E` followed by four digits. The first digit is the pipeline stage
that detects the problem.

### E0xxx — Parsing (`internal/parser`)

| Code | Meaning | Recovery |
|------|---------|----------|
| `E0001` | Unexpected token or malformed construct | Skips to the end of the statement, or to the next declaration |
| `E0002` | Lexical error: unknown character, unterminated string or comment, bad escape | Same |
| `E0003` | Missing `;` after a statement | Continues as if it were there |
| `E0004` | Left side of `=` is not a variable, field or element | Skips the statement |
| `E0005` | Field given twice in a struct literal | Skips the statement |

### E1xxx — Name resolution (`internal/resolve`)

| Code | Meaning |
|------|---------|
| `E1001` | Undefined identifier, type or trait. Suggests the closest known name |
| `E1002` | Name already declared in the same scope (includes redeclaring a builtin) |
| `E1003` | A pattern after `is` names a value where nothing runs only when the test held: under `!`, on a side of `\|\|`, in a test that is just a value. The suggestion is `_` |
| `E1005` | A name alone as the pattern of a case (`case limit:`) resolves to something other than a top-level constant: a variable, a parameter, a local `const`, a function, or nothing. The suggestion is a condition on a pattern that takes every value, `case Int if value == limit:` |

### E2xxx — Type checking (`internal/types`)

| Code | Meaning |
|------|---------|
| `E2001` | Type mismatch in a declaration, assignment, array literal or struct field |
| `E2002` | `return` outside of a function |
| `E2003` | Returned value does not match the function's return type |
| `E2004` | `return;` in a function that must return a value, or a function that can reach its end without returning one |
| `E2005` | Condition of `if`/`while` is not a `Bool` |
| `E2006` | Binary operator not defined for the operand types |
| `E2007` | Unary operator not defined for the operand type |
| `E2008` | Call of something that is not a function |
| `E2009` | Wrong number of arguments |
| `E2010` | Argument type does not match the parameter type |
| `E2011` | `for-in` over a type that cannot be iterated |
| `E2012` | `impl` is missing a method required by the trait |
| `E2013` | Method signature differs from the trait's (parameters, return type, or number of type parameters) |
| `E2014` | Field that does not exist: in a struct literal, or read from a value. Also a method used without calling it |
| `E2015` | Method that does not exist on the receiver's struct or trait. Suggests a close name |
| `E2016` | Struct literal of an undefined struct, or of a trait |
| `E2017` | Optional (`T?`) used where a value is required; the suggestion is to add `??` |
| `E2018` | Struct literal leaves fields without a value |
| `E2019` | Method defined twice for the same struct |
| `E2020` | `let x = [];` with nothing to tell the element type |
| `E2021` | Cast or type test that can never succeed (`1 as String`, `1 is String`, `case String:` on an Int), or for a type that cannot be verified |
| `E2022` | Assignment to a name declared with `const` |
| `E2023` | **Warning**: unreachable code after a statement that always leaves the block (reported once per block); a case that is never reached; a test `value is Pattern` that always holds |
| `E2024` | Type arguments of a method call that cannot be inferred, are superfluous or of the wrong number |
| `E2025` | Integer literal outside the 48-bit range of an `Int` |
| `E2026` | Assignment to the index or the element variable of a `for` loop. The suggestion names what works instead |
| `E2027` | Method that is not `pub`, called from another module |
| `E2033` | `.length` on an array or a text; the suggestion names the function of the standard library to call |
| `E2034` | `value?` where it cannot work: on something that is neither a `Result` nor an optional, or in a function that cannot return the failure or `null` |
| `E2035` | `value?.member` on a value that cannot be null, or on a `Result` |
| `E2036` | A type argument that does not implement what its type parameter requires (`<T: Ordered>`) |
| `E2037` | A function written without types (`x -> x + 1`) where nothing says what its parameters are, or with the wrong number of them |
| `E2028` | `match` on an enum that leaves values out; the message names one as a pattern |
| `E2029` | Pattern that cannot match: unknown variant, wrong number of values, variant of another enum, literal of the wrong type |
| `E2030` | Enum used wrongly: unknown variant, wrong number of values, type argument nothing determines, the enum as a value |
| `E2031` | Constant defined in terms of itself |
| `E2032` | Value of a constant that is not made of literals, operators and constants |

### E3xxx — Monomorphization (`internal/monomorph`)

| Code | Meaning |
|------|---------|
| `E3001` | Generic function called without type arguments where the arguments do not determine them |
| `E3002` | A generic function whose instances each need a larger one, without end (`f<T>` calling `f<[T]>`) |
| `E3003` | Number of type arguments differs from the number of type parameters |

### E4xxx — Desugaring and closure analysis

| Code | Package | Meaning |
|------|---------|---------|
| `E4001` | `desugar` | `for-in` over something that is not iterable |
| `E4002` | `desugar` | Invalid match pattern |
| `E4003` | `desugar` | Type information missing for an expression |
| `E4004` | `desugar` | Warning: deeply nested short-circuit operators |
| `E4005` | `desugar` | Unsupported type pattern in a match |
| `E4006` | `desugar` | Enum variant destructuring |
| `E4101` | `analysis` | Circular closure dependency (reserved, not raised yet) |

### E5xxx — IR generation (`internal/irgen`)

| Code | Meaning |
|------|---------|
| `E5001` | Construct that cannot be lowered to IR yet |
| `E5002` | Name used as a value that is neither a variable nor a function (e.g. a struct name) |
| `E5003` | Assignment to something that is not a variable (e.g. a function) |
| `E5005` | `break` or `continue` outside of a loop |

### E6xxx — Bytecode generation (`internal/codegen`)

| Code | Meaning |
|------|---------|
| `E6001` | Function needs more than 65536 registers |
| `E6002` | Program uses more than 65536 distinct constants |
| `E6003` | Program has more than 65536 functions |
| `E6004` | IR operation with no bytecode equivalent |
| `E6005` | Reference to a function that does not exist |
| `E6006` | No `main` function, or `main` takes parameters |
| `E6007` | Call with more arguments than fit in the register window |

`E6001`-`E6003` and `E6007` are limits of the bytecode format (16-bit
operands), not mistakes in the program; their suggestions say how to stay
within them.

### E7xxx — Compiler driver (`internal/compiler`)

| Code | Meaning |
|------|---------|
| `E7001` | Language feature that parses but cannot be compiled yet (match patterns other than literals, constants, ranges, types, structs and enum variants) |

| `E7002` | `extern fn` naming a function the runtime does not provide |

### R0xxx — Runtime errors (`internal/vm`)

A failure of a running program is a `vm.RuntimeError`; its `Diagnostic()` is shown like the errors above, with the calls that led to it as notes. See [errors.md](../../reference/language/error-handling.md#what-a-runtime-error-looks-like).

| Code | Meaning |
|------|---------|
| `R0001` | The program called `panic` |
| `R0002` | An index outside an array or a text |
| `R0003` | A cast of a value of another type |
| `R0004` | A division by zero, a result too large for an `Int` |
| `R0005` | Calls nested deeper than the limit |
| `R0006` | More memory than the limit |
| `R0007` | A function of the standard library given arguments it cannot work with |
| `R0008` | Anything else |
| `R0009` | Every task waits for another |

A `Diagnostic` has two fields for these: `Function`, shown after the position, and `Notes`, lines printed after the suggestion.

### E8xxx — Modules (`internal/modules`)

| Code | Meaning |
|------|---------|
| `E8001` | No file for an imported module, or `module.Name` whose module is not imported |
| `E8002` | The module does not declare the name |
| `E8003` | The name is not exported with `pub` |
| `E8004` | An import clashes with another name in scope |
| `E8005` | A module or type used as a value, or a module or function used as a type |
| `E8006` | An import that cannot work: the entry file, or a whole module after `pub` |

A method that is not `pub`, called from another module, is `E2027`: it is found by the type checker, which knows the receiver's type.

### Diagnostics in programs of several files

`Handler.AddSource(file, lines)` registers the text of a file. A diagnostic whose token is in a registered file quotes that file's line, whatever lines the reporting stage passes. The module loader registers every file it reads, so no stage needs to know which file it is reporting about. `Handler.SourceLine(file, line)` gives the same lookup to the bytecode generator, for the source lines kept in debug information.

### Severity

Most diagnostics are errors. A **warning** (currently only `E2023`) is printed like an error but does not set `Handler.HasErrors()`: the pipeline continues, bytecode is produced, and the CLI's exit code is that of the program. Code that reads `Diagnostics[0]` to find "the error" must skip warnings.

## Reporting Strategy

1.  **Within a stage**: The compiler recovers and continues to report as many errors as possible in a single run.
    *   *Example*: If a statement is missing a semicolon, the parser assumes it is there and continues with the next statement.
2.  **Between stages**: The pipeline stops after the first stage that reported an error, so a later stage never analyses a program that is already known to be broken (which would produce confusing follow-up errors).
3.  **Fatal Errors**: Issues that prevent any processing (e.g., file not found) are returned as a Go `error` by `compiler.CompileFile` and are not diagnostics.

Runtime errors (index out of bounds, division by zero, ...) are not part of
this system: the VM returns them as a Go `error` from `Exec`. They use the
debug information attached to each instruction to name the source location.

## Output Format

Diagnostics are printed to `stderr` in a human-readable format; the process
exits with code 1 if any of them is an error.

**Human-Readable Example:**
```text
[ERROR] E1001: undefined identifier 'cuont'
  --> src/main.qi:3:12
   |
 3 |     return cuont + 1;
   |            ^^^^^
   |
   = Suggestion: Did you mean 'count'?
```

**Runtime error example:**
```text
[ERROR] R0002: index out of bounds: 7 (length 3)
  --> src/main.qi:3:17 in function 'pick'
     |
 3 |     return items[index];
     |                 ^
     |
     = Suggestion: Compare the index with the length (items.len()) before using it, or use items.at(index), which gives null instead of failing
  called from src/main.qi:8:17 in function 'main'
```

## Not Implemented Yet

*   Structured output (`--format=json`) for tools.
*   Verbosity flags.
*   A per-code documentation page that the code could link to.
