# The Project File: `qoqi.json`

A single `.qi` file needs nothing but itself. A program that grows past that can describe itself in a file called `qoqi.json`:

```json
{
    "name": "greeter",
    "entry": "src/main.qi",
    "root": "src",
    "packages": {
        "textkit": "packages/textkit"
    },
    "heap_mb": 512,
    "max_depth": 20000
}
```

A working example is in [`examples/project/`](../../examples/project); one with queues in [`examples/shop/`](../../examples/shop).

## What It Does

| Key | Meaning | Without it |
|-----|---------|------------|
| `name` | The program's name. Informative only | |
| `entry` | The file the program starts in | A file has to be named on the command line |
| `root` | The folder [module](language/modules.md) paths start at | The folder of `qoqi.json` |
| `packages` | Names for folders of modules that live outside the root | No packages |
| `heap_mb` | Memory the program may hold at once, in MB | 256 |
| `max_depth` | How deep calls may nest | 10000 |
| `queues` | Where the steps declared `by` a queue are carried out: `{"payments": {"url": "env:PAYMENTS_URL", "secret": "env:SHOP_SECRET", "concurrency": 2}}` ([below](#queues)) | Every step `by` a queue is an error (`F0012`) |
| `require`, `require-dev` | The packages the program needs, by name and version: `{"acme/textkit": "^1.4"}`. Installed by `qoqi pkg install` ([packages.md](language/packages.md)) | No packages |
| `repositories` | Where packages come from besides GitHub ([packages.md](language/packages.md#where-packages-come-from)) | |
| `version`, `description`, `license`, `import` | What a package says of itself to the projects that require it ([packages.md](language/packages.md#making-a-package)) | |

Every key is optional; `{}` is a valid project file. Paths are relative to the folder of `qoqi.json`, or absolute.

A key that is not in this table is an error, so a typo (`"entri"`) is reported instead of silently ignored.

## Where It Is Looked For

In the folder of the file being compiled, then in each folder above it. The nearest one is used. This means every file of a project is compiled with the project's settings, whichever one is run:

```
greeter/
  qoqi.json
  src/
    main.qi
    app/
      guests.qi
      demo.qi          qoqi run src/app/demo.qi   uses greeter/qoqi.json too
```

Without a `qoqi.json` anywhere above, nothing changes from how a single file is run: module paths start at the folder of the file given on the command line.

## Running a Project

With an `entry`, the commands that take a source file can be given the project's folder, or nothing:

```bash
qoqi run                    # the project of the current folder (or of a folder above)
qoqi run path/to/greeter    # the project in that folder
qoqi run .                  # the same as nothing
qoqi deploy .               # the flows of the project of the current folder, its entry and its modules, to the daemon
qoqi check path/to/greeter
qoqi dump --ir
```

Naming a file still works and is unaffected by `entry`: `qoqi run src/app/demo.qi`.

What follows the folder or the file is handed to the program, which reads it with [`os.args()`](language/stdlib.md#stdos): `qoqi run . add "Buy milk"`. With arguments the folder has to be named, a dot at least: `qoqi run add` would look for a file called `add`.

## `root`: Where Module Paths Start

Module paths start at one folder, in every file of the program (see [modules.md](language/modules.md)). With a project file that folder is `root`:

```json
{ "entry": "src/main.qi", "root": "src" }
```

```rust
import app.guests;       // src/app/guests.qi, from any file of the project
```

Without `root` it is the folder of `qoqi.json` itself, and the same import would be written `import src.app.guests;`.

This also lets the entry file sit below the root (`"entry": "src/cmd/tool/main.qi"`), which is not possible without a project file, where the root is always the entry file's own folder.

## `packages`: Modules From Somewhere Else

A package is a folder of modules with a name. The name is the first part of the path used to import from it:

```json
{ "packages": { "textkit": "packages/textkit", "shared": "../shared/qoqi" } }
```

| Import | File |
|--------|------|
| `import textkit.format.pad;` | `packages/textkit/format/pad.qi` |
| `import textkit;` | `packages/textkit.qi`, the file next to the folder |
| `import shared.geometry;` | `../shared/qoqi/geometry.qi` |

Everything else about modules applies unchanged: `pub`, aliases, importing names, `pub import`. Inside a package, modules name each other through the package, like any other file of the program:

```rust
// packages/textkit.qi
import textkit.format.pad;
```

So a package does not depend on where it is installed, only on the name it is given.

Names at runtime and in messages use the package's name, not its folder: `textkit.format.pad.left`.

A package name wins over a folder of the same name under the root: with a package `std`, `import std.list;` never looks at `<root>/std/list.qi`.

A package name must be usable in an import: letters, digits and `_`, not starting with a digit.

The [standard library](language/stdlib.md) is a package too, named `std`, whose folder is inside `qoqi` itself. It does not have to be listed. Listing a package named `std` replaces it, whole, with the folder given: the program then gets none of the built-in modules.

### Packages with versions

`"packages"` says where code is; it does not get it there. For packages that are downloaded, have versions, and require packages of their own, use `"require"` and `qoqi pkg install`: see [packages.md](language/packages.md). The packages it installs are found like those of `"packages"`, from the folders `vendor/installed.json` gives; a name in both is an error.

## Limits

`heap_mb` and `max_depth` set what `--heap-mb` and `--max-depth` set (see [guide.md](language/tour.md#commands)), for every run of the project's source. An option on the command line wins over the file.

They apply to every command that executes the project's source: `run`, `test`, `serve`.

## Queues

A step declared `by` a queue is carried out by an application ([steps in your application](../guides/application-steps.md)). The queue says where:

```json
{
    "entry": "orders.qi",
    "queues": {
        "warehouse": { "url": "env:WAREHOUSE_URL", "secret": "env:SHOP_SECRET", "concurrency": 4 },
        "payments": { "url": "env:PAYMENTS_URL", "secret": "env:SHOP_SECRET", "concurrency": 2 }
    }
}
```

| Key | |
|-----|--|
| `url` | Where the application serves the queue |
| `secret` | What the daemon signs its requests with, and the application checks them with |
| `concurrency` | How many steps of the queue may be under way at once; eight when not said |

`env:NAME` reads the value from the environment of the daemon, so that no secret is written in the file. A value without `env:` is taken as written.

## Errors

Mistakes in `qoqi.json` stop the command before anything is compiled, with the file named:

```
Error: qoqi.json: unknown key "main"; the keys are name, entry, root, packages, heap_mb, max_depth, queues, version, description, license, import, require, require-dev, repositories
Error: qoqi.json: line 3, column 3: not valid JSON: invalid character 'e' looking for beginning of object key string
Error: qoqi.json: line 2: "heap_mb" must be a whole number
Error: qoqi.json: package name "my-lib" is not a name that can be imported: use letters, digits and '_', not starting with a digit
Error: qoqi.json does not say where the program starts; add "entry": "main.qi"
Error: sub is a folder without a qoqi.json; name a .qi file, or add a project file with an "entry"
```

A broken project file is an error even when a file is named on the command line: the file is part of the project, and compiling it with the settings ignored would be worse than stopping.

The format is plain JSON: no comments, no trailing commas.

## Implementation

`internal/project` reads and checks the file (`Find`, `Load`, `Parse`) and resolves its paths. The command line (`cmd/qoqi`) turns it into `compiler.Options{Root, Packages}`, which reach the module loader as `modules.Options`; see [compilation/module_loading.md](../internals/compilation/module-loading.md). The compiler itself never reads `qoqi.json`, so it can be driven with any layout by code that embeds it.
