# Qoqi in an Editor

`qoqi lsp` is a language server: a program an editor starts and talks to, to show what the compiler knows while the program is being written. It speaks the [Language Server Protocol](https://microsoft.github.io/language-server-protocol/), so any editor that does can use it.

| In the editor | What it does |
|---------------|--------------|
| Mistakes underlined | Errors and warnings of the compiler, as you type, with what usually fixes them; in the file and in the modules it imports |
| Pointing at a name | How it is declared and the comment above its declaration: variables, functions, methods, fields, variants, types, modules |
| Go to definition | Jumps to where a name is declared: in the file, in another file of the program, or in the standard library |
| Outline | What the file declares: functions, structs with their fields, enums with their variants, traits, methods |
| Format Document | The file written the way [`qoqi fmt`](formatting.md) writes it, from the text in the editor, saved or not. A file that does not read as a program is left alone |
| Suggestions | After a dot: the fields and methods of the value before it, whether a name or a chain of calls (`json.object().text("a", "b").`); the variants of an enum; the public names of a module. On an `import` line: the modules there are. Elsewhere: everything in reach |

## VS Code

The extension is in [`editors/vscode`](../../../editors/vscode): colours for `.qi` files, and the wire to `qoqi lsp`. It has no dependencies.

```sh
cd editors/vscode
npx --yes @vscode/vsce package --allow-missing-repository --skip-license
code --install-extension qoqi-0.1.0.vsix
```

To format on save, turn on `editor.formatOnSave` for Qoqi files in the settings; the extension already names itself as their formatter:

```json
"[qoqi]": { "editor.formatOnSave": true }
```

Two commands run the [tests](stdlib.md#test-files-qoqi-test) in a terminal: **Qoqi: Run the tests** for the workspace, **Qoqi: Run the tests of this file** for the file being edited.

It looks for the `qoqi` program in this order: the `qoqi.path` setting, a `qoqi` at the root of the workspace or in a folder above it, then the `PATH`. After rebuilding `qoqi`, run **Qoqi: Restart the language server** from the command palette: the server is the program that was there when it started.

## On GitHub

GitHub does not know Qoqi, so nothing colours it there by name. Two things borrow the colours of Rust, whose syntax is close enough (`fn`, `let`, `struct`, `enum`, `impl`, `match`): the `.gitattributes` file marks `.qi` files as Rust for display, and the examples in the documentation are fenced as `rust`. The cost is that GitHub counts the Qoqi sources of the repository as Rust in its language bar.

## Any other editor

Tell it to run `qoqi lsp` for files ending in `.qi`, talking over standard input and output. For Neovim:

```lua
vim.filetype.add({ extension = { lum = "qoqi" } })
vim.api.nvim_create_autocmd("FileType", {
  pattern = "qoqi",
  callback = function()
    vim.lsp.start({ name = "qoqi", cmd = { "qoqi", "lsp" }, root_dir = vim.fn.getcwd() })
  end,
})
```

## How it is built

The server is a Qoqi program, [`internal/lsp/server.qi`](../../../internal/lsp/server.qi), kept inside the `qoqi` binary: some 400 lines that read messages, keep the text of the open files, and answer. It is written with four parts of the standard library:

*   [`std.compiler`](stdlib.md#stdcompiler) answers the questions: it is the front of the compiler, parsing, names and types, reachable from a program.
*   [`std.task`](tasks.md) waits before checking: a file is compiled 150 ms after the last keystroke, in a task that gives up if another keystroke came meanwhile.
*   [`std.formatter`](stdlib.md#stdformatter) formats: the same module `qoqi fmt` runs, so the editor and the command line write a file the same way.
*   [`std.json`](stdlib.md#stdjson) and `io.readBytes` read and write the protocol.

A file being edited is known to the compiler as it is in the editor, saved or not: a program that imports it is checked against that text.

The files of the standard library live inside `qoqi`. Going to a definition there needs a file to open, so the server writes the one asked for into the user's cache folder (`qoqi/std/`), marked as not to be changed.

A file without a `main` is a module: it is checked like a program, since that is the only way to check it, and the missing `main` is not held against it.

## What is not there

| Missing | Today |
|---------|-------|
| Renaming, finding every use of a name | Search |
| The parameters of a function while its arguments are typed | Point at its name |
| Formatting a selection | The whole file is formatted |
| Colours from the compiler (semantic tokens) | The grammar of the extension, which goes by the shape of words |
| Understanding a file with a mistake in it | A program is understood whole or not at all: while a line does not parse, hover and definition answer nothing; suggestions still work, from the file without that line |
| Sending only what changed | The whole text is sent with every change |
| Columns in texts with characters outside the usual planes (some emoji) | The protocol counts in UTF-16, Qoqi in characters: positions after one on the same line are off by one |
| The names of struct fields suggested inside `Point { ... }` | Fields are suggested after a dot |
