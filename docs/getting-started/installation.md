# Installation

Qoqi Flow is one binary, `qoqi`: the compiler, the daemon and its console, the test runner, the formatter and the language server.

## From a release

The installer downloads the binary for your system (Linux or macOS, x86_64 or arm64), checks it against its checksum and puts it in `~/.local/bin`:

```console
$ curl -fsSL https://qoqi.dev/install.sh | sh
$ qoqi version
```

`QOQI_VERSION=v0.2.3` picks a release, `QOQI_INSTALL=/usr/local/bin` the folder. The download is checked against the checksums of its release, and, where [gh](https://cli.github.com) is installed, against its attestation, signed by GitHub for the release workflow of the repository (from v0.2.3): `QOQI_VERIFY=1` requires that check, `QOQI_VERIFY=0` skips it. By hand: `gh attestation verify qoqi-linux-x86_64.tar.gz --repo qoqiflow/qoqi`. The binaries, Windows' too, are also on [the releases](https://github.com/qoqiflow/qoqi/releases).

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

## The editor

The VS Code extension in [editors/vscode](../../editors/vscode) colours `.qi` files and talks to `qoqi lsp`: mistakes as you type, hover, go to definition, completion, formatting on save. See [the editor](../reference/language/editor.md).

## Next

[Quickstart](quickstart.md): a flow that waits for a person, served, started and answered in five minutes.
