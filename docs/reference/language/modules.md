# Modules

A Qoqi program can be split over several files. This document describes how: what a module is, how one module uses another, what is visible from outside, and what the compiler does with it all.

A complete example of a program in several files is [`examples/project`](../../../examples/project).

## Files and Folders

**A module is one file.** Its name is its path from the folder of the program's entry file, with dots instead of slashes and without `.qi`.

```
game/
  main.qi               the entry file: qoqi run game/main.qi
  player.qi             module  player
  physics.qi            module  physics
  physics/
    body.qi             module  physics.body
    collide.qi          module  physics.collide
```

**A folder is not a module.** It only groups modules. `import physics;` reads `physics.qi`; if there is no such file it is an error, whether or not a folder `physics/` exists.

**Paths always start at the entry file's folder**, in every file. `physics/body.qi` imports its neighbour as `import physics.collide;`, exactly as `main.qi` would. There are no relative imports, so a module has one name everywhere, and moving a file breaks exactly the imports that name it.

The folder is that of the file given on the command line, not the current directory: `qoqi run /some/where/game/main.qi` works from anywhere.

A test file run by `qoqi test` has no `main` to start from: without a project file, its paths start at the folder `qoqi test` runs in.

A project file, [`qoqi.json`](../configuration.md), can put the starting point elsewhere (`"root": "src"`) and give names to folders of modules outside it (`"packages"`), so that `import textkit.format.pad;` reads a file of a library kept somewhere else. Everything below applies to those modules too.

The entry file itself is not a module and cannot be imported (`E8006`). What other files need from it belongs in a module of its own.

## Importing

There are three forms.

```rust
import physics.body;                 // 1. the module
import physics.body as pb;           // 2. the module, under another name
import physics.collide.{Shape, overlaps};   // 3. names of the module
```

**1. The module.** Its declarations are used through the last part of its path:

```rust
import physics.body;

let b = body.new(3, 4);              // a function
let c: body.Body = body.Body { x: 1, size: 2 };   // a type, in an annotation and in a struct literal
let same = body.count<Int>([1, 2]);  // a generic function
if value is body.Body { ... }        // type tests, casts and match patterns
```

**2. Under another name**, when two modules end in the same word or the name is in the way:

```rust
import a.util;
import b.util as butil;
```

**3. Names of the module**, used as if they were declared in this file:

```rust
import physics.collide.{Shape, overlaps};

fn touching(a: Shape, b: Shape) -> Bool { return overlaps(a, b); }
```

The forms can be combined; importing the same module twice is harmless.

A local variable or parameter hides a module of the same name, as it hides a function:

```rust
import body;
fn f(body: Thing) -> Int { return body.size; }   // the parameter's field, not the module
```

Imports are written at the top level of a file, anywhere among the declarations; by convention, first.

## Visibility

**Everything is private to its module unless declared with `pub`.**

```rust
pub struct Body { x: Int; size: Int; }      // usable from other modules
pub trait Shape { fn left() -> Int; }
pub fn new(x: Int, size: Int) -> Body { ... }

fn atLeastOne(n: Int) -> Int { ... }        // only in this file
```

| Declaration | Rule |
|-------------|------|
| `fn`, `struct`, `trait`, `enum`, `const` | Private unless `pub` |
| Variants of an enum | As visible as the enum; reached through it: `geo.Shape.Circle(1.0)` |
| Fields of a struct | As visible as the struct. There is no per-field privacy |
| Methods in `impl Struct { ... }` | Private unless `pub fn`: callable only in the module that declares them |
| Methods in `impl Trait for Struct { ... }` | Always visible: they are what the trait promises |
| Methods declared by a `trait` | As visible as the trait |

`pub` cannot be put on an `impl` block; it goes on each method.

A private struct cannot be *named* from outside, but its values can leave the module, typically behind a trait:

```rust
struct Percent { off: Int; }                       // private
impl Rule for Percent { ... }
pub fn percent(off: Int) -> Rule { return Percent { off: off }; }
```

In the entry file `pub` changes nothing, since nothing can import it. A single-file program never needs it.

## Passing Names On

A module can export a name it imported, with `pub import`. This lets one file be the front door of a folder:

```rust
// physics.qi, next to the folder physics/
pub import physics.collide.{Shape, overlaps};
pub import physics.body.{Body};
```

```rust
import physics.{Shape, overlaps};      // without knowing which file declares what
import physics;                        // or physics.Shape, physics.overlaps
```

Only names can be passed on, not a module as a whole (`pub import physics.body;` is `E8006`). A passed-on name is the same declaration as the original: `physics.Body` and `physics.body.Body` are one type.

## Modules That Import Each Other

This is allowed, to any depth:

```rust
// even.qi
import odd;
pub fn isEven(n: Int) -> Bool { if n == 0 { return true; } return odd.isOdd(n - 1); }

// odd.qi
import even;
pub fn isOdd(n: Int) -> Bool { if n == 0 { return false; } return even.isEven(n - 1); }
```

It is harmless because a module has nothing that runs when it is loaded: no top-level statements, no initializers. The whole program is compiled together, so the order of files does not matter.

## Traits and Methods Across Modules

Any module can implement any trait for any struct it can name:

```rust
import physics.{Shape};

struct Wall { at: Int; }
impl Shape for Wall { ... }            // the program's struct, the module's trait
```

A module can also add methods to a struct of another module with a plain `impl`. Those methods belong to the module that wrote them: without `pub` only that module can call them.

Implementations are global. Two modules implementing the same trait for the same struct, or declaring the same method name on it, is an error (`E2019`).

## Functions the Runtime Provides

A function can be declared without a body, for the runtime to provide:

```rust
pub extern fn sqrt(x: Float) -> Float;
extern fn readLine() -> String?;
```

It is called, passed around and exported like any function, and calls to it are type checked against the declaration. It cannot have type parameters.

Which function it is depends on its full name: `extern fn sqrt` in the module `std.math` is the native function `std.math.sqrt` of the virtual machine. This is how the [standard library](stdlib.md) reaches the machine. The runtime has no functions other than the library's, so an `extern fn` anywhere else is rejected when the program is compiled (`E7002`), not when it runs. Declaring `extern fn sqrt` in a module of your own does not give you the library's: the name would be `yourmodule.sqrt`.

## The Entry Point

The program starts in the `main` of the entry file. A module may declare a function called `main`; it is an ordinary function there (`tool.main()`), and it does not make up for a missing `main` in the entry file.

## Names at Runtime

Every declaration outside the entry file is known to the rest of the compiler, and to the running program, by its module path and its name:

| Written | In `physics/body.qi` becomes |
|---------|-------------------------------|
| `fn new` | `physics.body.new` |
| `struct Body` | `physics.body.Body` |
| method `move` of `Body` | `physics.body.Body.move` |

Names of the entry file are kept as written. This is what appears in:

*   **type errors**: `physics.body.Body has no field 'y'`;
*   **runtime errors**: `at game/physics/body.qi:12:9 in function 'physics.body.new'`, with the source line of that file;
*   **type tests**: two modules may each have a `struct Node`. They are different types; `value is a.Node` and `value is b.Node` tell them apart, and a failed cast names both;
*   **`qoqi dump --ir` and listings**.

## Errors

| Code | Meaning | Example |
|------|---------|---------|
| `E8001` | No file for the module, or a `module.Name` whose module is not imported | `import physics.gravity;` |
| `E8002` | The module does not declare that name | `body.nwe(1, 2)` |
| `E8003` | The name is not `pub` | `body.atLeastOne(0)` |
| `E8004` | An import clashes with another name in scope | `import a.util; import b.util;` |
| `E8005` | A module or a type used as a value, or a module or function used as a type | `let m = body;` |
| `E8006` | An import that cannot work: the entry file, or a whole module after `pub` | `pub import physics.body;` |
| `E8007` | Statements outside a function in a module, or beside a `main` | `println("loaded");` in `physics/body.qi` |
| `E2027` | A method that is not `pub`, called from another module | `world.keepInside(thing)` |
| `E7002` | An `extern fn` the runtime does not provide | `extern fn launch();` |

Suggestions name the fix where there is an obvious one: the file that was expected, `Did you mean 'new'?`, `Declare it with 'pub' in physics/body.qi`, `import b.util as other;`. Writing `import physics.body.new;` for a name of a module is a common slip; the message gives the import that was meant: `import physics.body.{new};`.

A diagnostic shows the file it is about and quotes that file's line, wherever in the program it is. All files are parsed before anything is reported, so syntax errors in several modules come out in one run.

## What Is Not There

*   **A registry of packages.** [`qoqi pkg install`](packages.md) gets packages from git repositories, with versions; there is no index of them like Composer's Packagist. The one package that needs no installing is the [standard library](stdlib.md), `std`, which is inside `qoqi`.
*   **Variables shared between modules.** A module's variables outside functions are its own; others reach them through its functions.
*   **Separate compilation.** Every imported module is parsed and compiled on each run, and all of it ends up in the bytecode, used or not.
*   **Redeclaring a builtin is treated differently in a module**: the entry file may not declare `fn print`, a module may (it becomes `module.print`, and hides the builtin inside that module).

## How It Is Compiled

Modules exist only at the front of the compiler. The package `internal/modules` turns the files into one program in which every declaration has a unique name; no later stage knows the program had several files. See [compilation/module_loading.md](../../internals/compilation/module-loading.md).
