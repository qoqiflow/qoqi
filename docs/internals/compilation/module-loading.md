# Module Loading

**Package**: `internal/modules`
**Input**: the source text of the entry file, and a function that reads other files
**Output**: one `*ast.Program` holding every declaration of the program

What a module is and how it is used is described in [../modules.md](../../reference/language/modules.md). This document is about the implementation.

## The Idea

A program of several files is turned into one program, at the very start of the pipeline, by giving every top-level declaration a name that is unique in the whole program and rewriting every reference to it.

```rust
// physics/body.qi                      // after loading
pub fn new(x: Int) -> Body { ... }       fn physics.body.new(x: Int) -> physics.body.Body { ... }

// main.qi
import physics.body;
let b = body.new(1);                     let b = physics.body.new(1);
```

`physics.body.new` is not something a programmer can write as one name, since an identifier has no dots, so it cannot clash with anything in the source. Declarations of the entry file keep their names, which is why a single-file program goes through this stage unchanged.

After this the resolver, the type checker, monomorphization, desugaring, IR generation and the backend work exactly as they do for one file. They need no notion of a module, with one exception noted below.

## Entry Point

```go
prog := modules.Load(src, "game/main.qi", opts, errs)
```

```go
type Options struct {
    Read     ReadFile            // func(path string) (string, error)
    Root     string              // where module paths start; default: the entry file's folder
    Packages map[string]string   // first part of a module path -> folder
}
```

The compiler passes a `Read` that reads from disk (`compiler.Compile`); tests pass one backed by a map (`compiler.CompileWith`), so multi-file programs can be tested without touching the file system. `Root` and `Packages` come from the project file, [`qoqi.json`](../../reference/configuration.md), which the command line reads; the loader itself knows nothing about that file.

## Steps

### 1. Load

The entry file is parsed, then every file its imports name, then theirs, until nothing new turns up. A module path maps to a file by replacing dots with path separators under the root and adding `.qi`. If the first part of the path names a package, the rest is looked up under the package's folder instead, and the package name alone is the file next to that folder (`vendor/std.qi` for a package `std` in `vendor/std`). Either way the module keeps the name it was imported by, so declarations of a package are named after the package, not after where it is on disk.

Each file is read and parsed once, however many modules import it, which is also what makes modules that import each other terminate. The source lines of every file are registered with the error handler (see [Diagnostics](#diagnostics)).

All files are parsed before the stage gives up, so syntax errors in several files are reported together. If any file failed to load or parse, loading stops here: binding imports against half a program would only add noise.

### 2. Collect

For each module, its top-level declarations are recorded in two tables, values (functions) and types (structs, traits, enums), each with its program-wide name and whether it is `pub`. Re-exports (`pub import m.{name}`) are recorded for every module *before* any import is bound, so a chain of re-exports can be followed whatever order the files were loaded in. Following a chain keeps a set of visited modules, so re-exports going in a circle end as "no such name" instead of looping.

### 3. Bind

Each import adds to the scope of its module:

| Import | Adds |
|--------|------|
| `import a.b;` | the alias `b` for the module |
| `import a.b as c;` | the alias `c` |
| `import a.b.{X, f};` | `X` in the types, `f` in the values |

This is where unknown names (`E8002`), private names (`E8003`) and clashes (`E8004`) in import lines are reported. A name that fails is left unbound.

### 4. Rewrite

The `rewriter` walks every declaration of every module and replaces names:

| Found | When | Becomes |
|-------|------|---------|
| identifier `f` | a function of this module, or imported by name | its program-wide name |
| `m.f` | `m` is an imported module | an identifier with the program-wide name |
| type `T` | a type of this module, or imported by name | its program-wide name |
| type `m.T`, struct literal `m.T { ... }` | `m` is an imported module | the same, with the program-wide name |
| match pattern `T` or `m.T` | it names a type | a type pattern |
| `value is m.T`, `value is (T, U)` | every name is a type | a type test; the parser had read a pattern, which is what `value is Shape.Empty` and `value is (a, b)` are |
| a name in a pattern after `is` (`r` in `value is Shape.Circle(r)`) | always | a variable of the rest of the `&&` chain and of the block the test guards |
| identifier `C`, `m.C` | a constant | its program-wide name |
| identifier `E` in `E.Variant`, `m.E` | an enum | the enum's program-wide name; the variant is left as written |
| a name in a variant pattern (`w` in `Shape.Rect(w, h)`) | always | a variable of the case, which hides functions, constants and modules of that name |
| `impl T for S` | | both names resolved as types |

To tell these names from local ones the rewriter tracks two kinds of scope as it walks: **variables** (parameters, `let`, loop variables, lambda parameters), which hide functions and modules of the same name, and **type parameters**, which hide types. A `let` is in scope after its initializer, as in the resolver.

A name the rewriter does not know (a builtin, a mistake) is left alone: the resolver or the type checker reports it as it would in a single file.

Rewriting runs even if binding reported errors, so that misuses of the modules that did load are reported in the same run.

The rewriter also fills in two fields of each function and method declaration: `Module`, the module it was written in, and `Pub` for methods a trait declares or implements.

### 5. Merge

The declarations of all modules are appended to one `ast.Program`, entry file first. Imports are dropped.

Functions declared with `extern fn` are renamed like any other and then moved out of `Decls` into `Program.Externs`. They have nothing to compile, so the stages that walk declarations never meet a function without a body; the resolver puts them in scope by name, the type checker reads their signature when one is called, and the backend is told their names so that a reference becomes a host function (`NEWEXFUNC`) and not a compiled one.

### The standard library

The package `std` is added by the driver (`withStandardLibrary` in `internal/compiler`) with the folder `<std>`, which does not exist on disk: the `Read` function it installs serves paths under it from the sources embedded in the binary (`internal/stdlib`). To the loader it is a package like any other. A program that brings its own package named `std` keeps it.

## The One Thing Later Stages Know

Whether a method may be called depends on the type of the receiver, which only the type checker knows. So method visibility is checked there (`checkMethodVisible`, `E2027`): a method that is not `pub` may only be called from a function whose `Module` is the method's. A lambda takes the module of the function it is written in.

## Diagnostics

A diagnostic quotes the source line it is about. With one file, each stage was handed that file's lines. With several, the error handler keeps the lines of every file (`Handler.AddSource`), and looks the line up by the file of the token being reported. Stages still pass their own lines; they are used only for a file the handler does not know, so stage tests that build a handler by hand behave as before.

The bytecode generator reads the same table for the source line stored with each instruction, which is what runtime errors and the console display.

## What the Parser Contributes

*   `import a.b.c;`, `import a.b as c;`, `import a.b.{X, y};`, each optionally after `pub`: an `ast.ImportDecl` with the dotted `Path`, the `Alias` and the `Names`.
*   `pub` before `fn`, `struct`, `trait`, `enum`, and before a method in an `impl`.
*   Qualified names where a type is expected: `m.T` is parsed into a type named `"m.T"`; likewise the struct of a struct literal and the names of an `impl`.

The parser cannot know that `body` in `body.new(1)` is a module rather than a variable, so in expressions it produces the same member access as for a field. Deciding is the rewriter's job.

## A Clash the Loader Rules Out

A method `m` of struct `B` in module `a` is named `a.B.m` by IR generation. A function `m` in a module `a.B` (the file `a/B.qi`) would get the same name. The loader rejects a struct whose program-wide name is also the path of a loaded module (`E8004`).

## Testing

`internal/modules/modules_test.go` and `errors_test.go` drive the loader from programs held in a map and assert on the merged program printed back as source, which shows exactly which names were rewritten: names through each form of import, scoping (what hides what), patterns, re-export chains, modules importing each other, each file read once, every error with its code, message and suggestion, and diagnostics quoting the right file.

`internal/compiler/modules_test.go` runs multi-file programs through the whole pipeline. See [../testing.md](../../../CONTRIBUTING.md).
