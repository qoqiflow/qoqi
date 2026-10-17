# Parsing Specification

This document describes the first stage of the [compilation pipeline](pipeline.md): turning source text into an Abstract Syntax Tree. It covers the lexer, the parser, the places where the grammar is ambiguous and how they are resolved, and error recovery.

The grammar itself is in [language-spec.md](../../reference/language/spec.md#12-grammar-ebnf-sketch). The implementation is in `internal/parser`.

## Objective

*   Produce an `*ast.Program` for every syntactically valid source file.
*   Give every node the token it came from, so later stages can point at the exact source span.
*   Report syntax errors with a precise location and, where possible, what to do about them.
*   Keep going after an error, so one run reports several.

## Usage

```go
errs := errors.NewHandler()
prog := parser.Parse(src, "main.qi", errs)

if errs.HasErrors() {
    // prog still holds every declaration that parsed cleanly
}
```

`parser.SourceLines(src)` returns the 1-based line map that the other stages pass to the error handler.

## Lexer (`lexer.go`)

The lexer turns characters into `token.Token` values. Each token records its type, its text, and its start and end position (file, line, column, both 1-based; the end is exclusive).

| Category | Rules |
|----------|-------|
| Whitespace | Spaces, tabs, carriage returns and newlines separate tokens and are otherwise ignored |
| Comments | `// to end of line` and `/* block */` (not nested). Dropped by `Tokenize` |
| Identifiers | A letter or `_`, then letters, digits and `_`. Keywords and builtin type names are recognized by `token.LookupIdent` |
| Integers | Digits: `42` |
| Floats | Digits with a fraction and/or exponent: `3.14`, `10e3`, `1.5e-2`. A `.` only starts a fraction when a digit follows, so `xs.size` and `1.x` are not floats |
| Strings | `"..."` on one line, with escapes `\n \t \r \0 \\ \" \$`. With `${...}` inside, several tokens: see [below](#string-interpolation) |
| Operators | Longest match first: `...`, `->`, `==`, `!=`, `<=`, `>=`, `&&`, `||`, `??`, `+=`, `-=`, `*=`, `/=`, `%=`, `++`, `--` before the single-character ones. As a consequence `a--b` is `a`, `--`, `b`; write `a - -b` |

There is no token for negative numbers: `-1` is the unary operator applied to `1`.

**Lexical errors do not stop the lexer.** An unknown character, an unterminated string or comment, or an invalid escape produces an `ILLEGAL` token whose text describes the problem. The parser reports it (as `E0002`) when it reaches the token. This keeps all reporting in one place and lets the parser recover the same way as for any other error.

### String interpolation

A string literal with `${expr}` in it is not one token. The lexer turns it into the tokens of the concatenation it stands for:

```
"total: ${n + 1} items"      ( "total: " + __text ( n + 1 ) + " items" )
"${a}${b}"                   ( "" + __text ( a ) + __text ( b ) )
```

*   The text before the first `${` always comes first, empty or not: it makes the expression a `String` whatever follows.
*   `__text` is a builtin that does what `toString` does. It has a name of its own so that a program's variable called `toString` cannot get in the way.
*   The parentheses keep the whole thing one operand: `"${a}" + b` and `f("${a}")` mean what they look like.
*   The tokens of the expression are read by the lexer itself, from the source, so they carry their real positions and an error in the expression is shown under it. A string inside the expression is read like any string, so it may have `${...}` of its own. Braces are counted, so a struct literal or a lambda inside is fine. The made-up tokens are positioned at the `${`.
*   The expression has to close on the line it starts on. `${}` with nothing inside and a `${` that is never closed are lexical errors (`E0002`), and what is left of the line is skipped so that one mistake gives one error.

The lexer keeps the tokens it has made and not yet handed out in a queue (`Lexer.pending`); `Next` empties it before reading on.

Doing this in the lexer means no later stage knows the feature exists. The price is that a string with interpolation prints back, in a diagnostic or in `qoqi dump --ir`, as the concatenation.

## Parser (`parser.go`)

A hand-written recursive descent parser over the full token list. Having all tokens in memory makes arbitrary lookahead and backtracking trivial, which two of the ambiguities below rely on.

### Declarations

`Program = { Import | Decl }`. A declaration is selected by its first token: `fn`, `struct`, `enum`, `trait`, `impl`. `pub` may come first; it sets `Pub` on the declaration (on a method inside an `impl`, too) and is an error before `impl`.

`const NAME = value;` at the top level is an `ast.ConstDecl`, with an optional type after the name. Inside a function `const` stays a statement.

An `enum` declaration lists variants, each with the types of the values it holds. After a dot, and as the name of a variant, the names of the builtin types are accepted as names (`Json.Bool`).

Type arguments are also recognized before a dot, for the name of a generic enum: `Option<Int>.None`.

**Patterns.** The pattern of a `case` is parsed as a type when it can only be one, and as an expression otherwise. An expression that has the shape of a variant is then turned into an `ast.VariantPattern`: names joined by dots (`Shape.Empty`), optionally called (`Shape.Rect(w, h)`). Each argument becomes a sub-pattern: a bare name is an `ast.BindingPattern` (`_` included), a literal stays a literal, and an argument with the shape of a variant is a pattern in turn. Anything else as an argument is a syntax error. Whether the names really are an enum and a variant is decided later; `module.Type` has the same shape and is sorted out by [module loading](module-loading.md).

`extern fn name(params) -> Type;` is a function without a body: an `ast.FnDecl` with `Extern` set and a nil `Body`. Type parameters and a body are syntax errors with their own messages.

An import is `import a.b.c;`, optionally followed by `as name` or by `.{X, y}`, and optionally preceded by `pub`. The `ImportDecl` carries the dotted path, and is positioned at the path so that diagnostics underline it.

Where a type is expected, `m.T` names a type of an imported module; it is parsed into a single type whose name is `"m.T"`. The same goes for the struct of a literal (`m.T { ... }`) and the names of an `impl`. In expressions `m.f` is an ordinary member access: only [module loading](module-loading.md) knows whether `m` is a module.

*   **Functions**: `fn name<T>(params) -> Type { body }`. The return type may be omitted (`Void`). Trait methods end with `;` instead of a body.
*   **Structs**: fields may be separated by `;` or `,`, and the last separator is optional.
*   **Impl blocks**: `impl Trait for Type { ... }` or `impl Type { ... }`. The second form leaves `TraitName` empty. For a generic struct the parameters follow `impl` and must be repeated, in the same order, on the struct: `impl<T> Box<T>`. The repetition is what ties a method's `T` to the struct's, so a mismatch is reported (with the correct header as suggestion) without abandoning the block.

### Statements

A statement is selected by its first token; anything else is an expression statement.

**Assignment** has no node of its own. After parsing an expression statement's expression, a following `=` turns it into a `BinaryExpr` with operator `"="`:

```
x = 1;        BinaryExpr{ Left: Ident(x),        Op: "=", Right: 1 }
xs[0] = 1;    BinaryExpr{ Left: IndexExpr(...),  Op: "=", Right: 1 }
p.x = 1;      BinaryExpr{ Left: MemberExpr(...), Op: "=", Right: 1 }
```

The compound forms use the same node with their operator: `x += 1` has Op `"+="`. `x++` and `x--` are parsed as `x += 1` and `x -= 1`; they exist only as statements, so there is no prefix form and no value to define. `ast.CompoundOp` and `ast.IsAssignment` classify the operators for later stages.

The left side must be an identifier, an index expression or a member expression (`E0004` otherwise). The desugarer builds the same shape for the assignments it generates, so every later stage handles one representation.

**Match**: each case is `case Pattern:` or `default:`, followed by a block or by an expression and `;`. `default` is not a keyword; it is recognized by position. A default case has a `nil` pattern.

A pattern is either a type or an expression, and the two overlap:

*   The parser first tries to read the pattern as a **type** (`tryTypePattern`, speculative). It keeps the result when the pattern can only be a type: `Int`, `[String]`, `Int?`, `fn(Int) -> Bool`, `Box<Int>`.
*   A **bare name** is ambiguous (`case Point:` or `case limit:`), so it is parsed as an expression. When the whole file has been read, `classifyPatterns` turns those that name a struct or trait declared in the file into type patterns.
*   Everything else is an expression: literals, `-1`.

### Expressions

Binary expressions use precedence climbing. All binary operators are left-associative. From lowest to highest:

| Level | Operators |
|-------|-----------|
| 1 | `||` |
| 2 | `&&` |
| 3 | `??` |
| 4 | `==` `!=` |
| 5 | `<` `<=` `>` `>=` |
| 6 | `+` `-` |
| 7 | `*` `/` `%` |
| unary | `-` `!` |
| cast | `value as Type` |
| postfix | call `f(x)`, index `a[i]`, member `a.b` |

`value is Type` sits at the same level and produces a `TypeTestExpr`. `is` is not a reserved word: after a complete expression an identifier cannot follow, so in that position it can only be this operator, and `is` remains usable as a name elsewhere.

`value is Pattern` is the same operator with the pattern of a variant after it, and produces a `PatternTestExpr` holding the `ast.VariantPattern` a `case` would (`parseIsTest` in `patterns.go`). What follows `is` is decided by its first token:

| After `is` | Read as |
|------------|---------|
| `Int`, `[T]`, `fn(...)`, a bare `Name`, `Name<T>`, `m.Name<T>`, `m.Name?` | a type: these cannot be a pattern |
| `Name.Name` and nothing more | a pattern; [module loading](module-loading.md) makes it the type test when the first name is a module |
| `Name.Name(...)`, `m.Name.Name`, `Name<T>.Name(...)` | a pattern |
| `(a, b)` | the pattern of a tuple when it parses as one, else the tuple type: `(Int, [String])`. A tuple of names that are all types is turned back into the type by module loading, which knows the types of every module |
| `Name(...)` | an error that says the variant goes with its enum |
| a literal | an error that says to compare with `==` |

The pattern is parsed as a postfix expression with struct literals switched off, whatever the context, and converted as the pattern of a case is. So it ends where its name or its parentheses end: in `if shape is Shape.Empty { }` the brace is the block, and the struct literal ambiguity below is no wider than before.

A cast produces a `CastExpr`. Its type is parsed without unions, so `v as Int | String` is not a cast to a union; `v as Int?` is fine. `as` is also the keyword of `import a.b as name`, where it is handled by the import rule.

Primary expressions: literals, identifiers (optionally with type arguments), parenthesized expressions, array literals `[a, b]`, struct literals `Name { field: value }` and function expressions `fn(params) -> Type { body }`.

## Ambiguities

### Struct literal or block?

```rust
if ready { run(); }        // `ready` is the condition, `{` opens the body
let p = Point { x: 1 };    // `Point {` opens a struct literal
```

Both are an identifier followed by `{`. Two rules decide:

1.  **Context.** While parsing the header of `if`, `while`, `for` and `match`, struct literals are switched off (`noStructLit`). They are switched back on inside any delimiters, because there the `{` cannot be the body: `if (Point { x: 1 }).x == 1 { ... }` and `f(Point { x: 1 })` are fine.
2.  **Lookahead.** Elsewhere, `Name {` is a struct literal only if what follows is `}` or `field:`. This keeps `x { ... }` from being misread in the rare places a block may follow an expression.

### Type arguments or comparison?

```rust
collect<Int>(1, 2)     // generic call
a < b                  // comparison
a < b > (c)            // two comparisons? or a<b>(c)?
```

After an identifier, `<` starts a **speculative** parse of a type argument list (`tryTypeArgs`). It is accepted only if it is well formed, closed by `>`, and immediately followed by `(` or `{` — the two things that can follow an instantiated name. Otherwise the parser rewinds to the `<` and treats it as an operator. Errors raised while speculating are not reported.

The same rule applies after a member name, for calls of generic methods: `stack.map<String>(f)` carries its type arguments on the `MemberExpr`, while `a.b < c` stays a comparison.

The price is that `a < b > (c)` is read as the generic call `a<b>(c)`. Chained comparisons like that do not type check anyway, and parentheses resolve it: `(a < b) > c`.

## Error Handling

All errors are reported through `internal/errors` with a code in the `E0xxx` range; see [diagnosis.md](diagnosis.md) for the table.

### Recovery

A syntax error unwinds to the nearest recovery point, which then skips ahead to a place where parsing can resume. There are two levels:

| Level | Recovery point | Skips to |
|-------|----------------|----------|
| Statement | `parseStmtRecovering`, called for each statement of a block | Past the next `;` at the same nesting depth, or to the `}` closing the block |
| Declaration | `parseTopLevel`, called for each import and declaration | The next `fn`, `struct`, `enum`, `trait`, `impl`, `import` or `pub` |

The broken statement is dropped from the AST; the statements around it are kept. So a file with three unrelated mistakes reports three errors, and each is reported once.

**Missing semicolons are not fatal at all.** `expectSemicolon` reports `E0003` pointing just past the previous token, where the `;` belongs, and carries on as if it were present:

```text
[ERROR] E0003: expected ';' after '1', found 'let'
  --> main.qi:3:14
   |
 3 |     let a = 1
   |              ^
   |
   = Suggestion: Add ';' to end the statement
```

Since the pipeline stops after a stage that reported errors, a program with syntax errors is never resolved or type checked; the partial AST is only used to find further syntax errors.

## Testing Strategy

Tests are in `internal/parser/lexer_test.go` and `parser_test.go`.

*   **Lexer**: token types for each category, escape handling, positions across lines, each lexical error.
*   **Precedence**: expressions are parsed and printed back with `String()`, which parenthesizes every binary expression, so the expected grouping is visible in the test: `1 + 2 * 3` → `(1 + (2 * 3))`.
*   **Ambiguities**: one test each for generic call vs comparison and struct literal vs block.
*   **Errors**: one case per error code, asserting on code, message and line.
*   **Recovery**: a file with several mistakes must report exactly those and keep the valid declarations.
