# Installation

Qoqi Flow is one binary, `qoqi`: the compiler, the daemon and its console, the test runner, the formatter and the language server.

## From the source

You need Go 1.25 or later. Node is not needed: the console is prebuilt in the repository.

```console
$ git clone https://github.com/qoqiflow/qoqi
$ cd qoqi
$ make build
$ ./qoqi version
qoqi dev darwin/arm64
```

Put `./qoqi` somewhere on your `PATH`, or run `go install ./cmd/qoqi`.

## From a release

Once releases are published, the installer downloads the binary for your system, checks it against its checksum and puts it in `~/.local/bin`:

```console
$ curl -fsSL https://raw.githubusercontent.com/qoqiflow/qoqi/main/install.sh | sh
```

`QOQI_VERSION=v0.1.0` picks a release, `QOQI_INSTALL=/usr/local/bin` the folder.

## The editor

The VS Code extension in [editors/vscode](../../editors/vscode) colours `.qi` files and talks to `qoqi lsp`: mistakes as you type, hover, go to definition, completion, formatting on save. See [the editor](../reference/language/editor.md).

## Next

[Quickstart](quickstart.md): a flow that waits for a person, served, started and answered in five minutes.
