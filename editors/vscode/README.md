# Qoqi for VS Code

Colours for `.qi` files, and the language server: mistakes underlined as you type, what a name is when you point at it, jumping to where it is declared, the outline of a file, suggestions after a dot, and formatting.

**Format Document** writes the file the way `qoqi fmt` does. To have it done on save:

```json
"[qoqi]": { "editor.formatOnSave": true }
```

**Qoqi: Run the tests** and **Qoqi: Run the tests of this file** run `qoqi test` in a terminal.

The extension starts `qoqi lsp`, the language server kept inside the `qoqi` program. It looks for `qoqi` in this order: the `qoqi.path` setting, a `qoqi` program at the root of the workspace or in a folder above it, then the `PATH`.

## Installing it

From this folder:

```sh
npx --yes @vscode/vsce package --allow-missing-repository --skip-license
code --install-extension qoqi-0.1.0.vsix
```

or, without packaging, link the folder where VS Code looks for extensions and restart it:

```sh
ln -s "$(pwd)" ~/.vscode/extensions/qoqi.qoqi-0.1.0
```

## Files

| File | For |
|------|-----|
| `syntaxes/qoqi.tmLanguage.json` | The colours: keywords, types, texts with `${...}`, comments |
| `language-configuration.json` | Brackets, comments, indentation |
| `extension.js` | Starts the server and passes on what it says; runs the tests in a terminal. No dependencies |

`Qoqi: Restart the language server` in the command palette starts it again, after rebuilding `qoqi` for one.
