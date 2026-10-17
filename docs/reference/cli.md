# Command line

```console
$ qoqi <command> [arguments]
```

`qoqi` with no command prints the help; `qoqi serve --help` prints the usage of every command of flows.

## Flows

Every command but `serve` and `gen` talks to the daemon that serves the store, found from the store's address file (`flows.db.daemon`). `--store` names the store, `flows.db` by default.

### `qoqi serve`

```console
$ qoqi serve [program or folder] [--store flows.db] [--listen 127.0.0.1:7070] [--workers N] [--watch]
```

Runs the daemon: the flows deployed to the store, and those of the program, folder or project given. In a project folder with no argument, the project's flows.

| Option | |
|--------|--|
| `--store` | A SQLite file, or a server: `postgres://user:pass@host/db`, `mysql://user:pass@host/db` |
| `--listen` | Where the HTTP API and the console are served |
| `--workers` | How many instances run side by side; the number of processors by default |
| `--watch` | Deploys again what changes, as it is saved |

### `qoqi deploy`

```console
$ qoqi deploy <program or folder> [--dry] [--store flows.db]
```

A new version to the daemon, served at once: what runs ends its turn, and each instance moves at its next [stage](../concepts/versioning.md). `--dry` says what it would do. Writes `flow.lock` next to the program.

### `qoqi undeploy`

```console
$ qoqi undeploy <program>
```

Takes a program away, once none of its instances is alive. Its versions and instances are kept.

### `qoqi versions`, `qoqi diff`

```console
$ qoqi versions [program]
$ qoqi diff <seq or program> [--from seq]
```

The deploys kept, the last first, and what runs on each; what a deploy changed of the source, from the one before (or `--from`).

### `qoqi start`

```console
$ qoqi start <Flow> <name> [fields as JSON]
```

Starts an instance. A name taken gives the instance that has it. Prints the instance as JSON.

### `qoqi ls`, `qoqi show`, `qoqi result`

```console
$ qoqi ls [--flow Flow] [--status waiting|running|stopped|done|failed] [--json]
$ qoqi show <name> [--json]
$ qoqi result <name> [--wait 30s] [--json]
```

The instances and what each is doing; one instance, where it is, what it was given, what its steps returned; what its `run` returned, waiting for it that long. `result` ends with status 2 if the instance has not ended.

### `qoqi send`

```console
$ qoqi send <name> <inbox> <message as JSON> [--key evt-1]
```

A message for an inbox of an instance. A key sent before is one message.

### `qoqi cancel`, `qoqi resume`, `qoqi answer`, `qoqi end`

```console
$ qoqi cancel <name> [reason]
$ qoqi resume <name> [--go]
$ qoqi answer <name> <key> (<what it did, as JSON> | --nothing)
$ qoqi end <name> [reason]
```

Cancels an instance, its undos run; takes a stopped instance up, showing what it would do, `--go` to do it; answers for a step the application could not tell of; ends a stopped instance as it is. See [failures](../concepts/failures.md#stopped-instances).

### `qoqi ui`

Opens the console of the daemon in a browser, with its token.

### `qoqi gen`

```console
$ qoqi gen go <program> [--package steps] [--queue billing]
$ qoqi gen php <program> --namespace 'App\Flow' --out src/Flow [--queue billing]
$ qoqi gen ts <program> [--from @qoqi/flow] [--queue billing]
```

Writes what an application needs to carry out the steps of its queues: their types, an interface for each queue, and the handler that serves it. Go and TypeScript are written to the standard output; PHP to a file per class. See [steps in your application](../guides/application-steps.md).

## Programs

| Command | |
|---------|--|
| `qoqi run [file.qi \| folder] [arguments]` | Compiles and runs a program. `qoqi file.qi` is the same, and so is a file starting with `#!/usr/bin/env qoqi` |
| `qoqi test [--filter=words] [files or folders]` | Runs the tests: the functions `test...()` of the files `*_test.qi` |
| `qoqi check [--json] [files or folders]` | Compiles without running and says what is wrong. `--json` for a CI step, an editor or an agent |
| `qoqi fmt [--check] [files or folders]` | Formats `.qi` files in place; `--check` only says which would change |
| `qoqi dump [file.qi] [--ir]` | What the compiler made of a program: its bytecode, or its intermediate form |
| `qoqi pkg <command>` | The packages of the project: `require`, `install`, `update`, `remove`, `show`, `why`, `outdated` ([packages](language/packages.md)) |
| `qoqi lsp` | The language server, for an editor |
| `qoqi version` | Which `qoqi` this is |

Options for the commands that run a program, anywhere after the command:

| Option | Default | |
|--------|---------|--|
| `--heap-mb=N` | 256 | Memory the program may use, in MB |
| `--max-depth=N` | 10000 | How deep calls may nest |
| `--cpuprofile=FILE` | | Where the time went, for `go tool pprof` |

After a lone `--`, everything is the program's.

## Exit status

`0` on success, or the `Int` `main` returned; `1` for a compile error, a runtime error, a usage error or a refusal of the daemon; `2` for `qoqi result` on an instance that has not ended.
