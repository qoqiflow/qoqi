# Command line

```console
$ qoqi <command> [arguments]
```

`qoqi` with no command prints the help; `qoqi serve --help` prints the usage of every command of flows.

## Flows

`qoqi serve` is the daemon: it starts, and is given its programs by `qoqi deploy`. Every other command but `gen` talks to it over its [HTTP API](http-api.md), from its machine or from elsewhere:

*   on its machine, by the files it keeps in its home, `QOQI_PATH`;
*   from anywhere, with `--url` and `--token`, or `QOQI_URL` and `QOQI_TOKEN`.

```console
$ export QOQI_URL=https://flows.example.com QOQI_TOKEN=$(ssh flows cat .qoqi/token)
$ qoqi deploy shop.qi
$ qoqi ls --status stopped
```

### The home: `QOQI_PATH`

The folder where the daemon keeps all of its files, `~/.qoqi` when `QOQI_PATH` is not set:

| File | |
|------|--|
| `flows.db` | The store, SQLite: the instances, the versions deployed, the users of the console. `--store` or `QOQI_STORE` names another, a server |
| `daemon.json` | Where the daemon listens, its token and its process, while it runs, readable by its user alone: the commands read it |
| `token` | The daemon's token, readable by its user alone; the same each time it starts |
| `logs/daemon.log` | What the daemon says, each line with its time |
| `logs/flows.log` | What the flows print |

One daemon runs from a home at a time: a second is refused while the first lives.

### `qoqi serve`

```console
$ qoqi serve [--listen 127.0.0.1:7070] [--hosts name,...] [--store address] [--workers N]
```

Runs the daemon, with the flows deployed to its store, until it is stopped (`Ctrl-C`, `SIGTERM`). It takes no program: `qoqi deploy` gives it them, and it serves them again each time it starts.

| Option | |
|--------|--|
| `--listen` | Where the HTTP API and the console are served |
| `--hosts` | The names it is reached by from elsewhere, besides its own address and `localhost`: `--hosts flows.example.com` |
| `--store` | A SQLite file, or a server: `postgres://user:pass@host/db`, `mysql://user:pass@host/db`. Also `QOQI_STORE`; `$QOQI_PATH/flows.db` by default |
| `--workers` | How many instances run side by side; the number of processors by default |

The first time, the console has one user: `admin`, password `admin`, which is changed at the first sign in.

### `qoqi deploy`

```console
$ qoqi deploy <program or folder> [--dry] [--watch]
```

A new version to the daemon, served at once: what runs ends its turn, and each instance moves at its next [stage](../concepts/versioning.md). The program is compiled here, and sent. `--dry` says what it would do; `--watch` deploys again each time a file of the program is saved, the modules it imports with it, and says what does not compile or is refused while the version before goes on. Writes `flow.lock` next to the program.

A program is a file with a `main`, and the modules it imports. Its flows are those of its file and of those modules, each started by its name: `qoqi start Easy` for a flow `Easy` of the module `easy`, which `qoqi ls` and the console show as `easy.Easy`. Two modules of one program with a flow of one name are refused. A module that has a `main` of its own is a program, and its flows are served when it is deployed, not by a program that imports it to try them.

A folder is every program in it that has flows, and the `entry` its `qoqi.json` names: a file of the folder with no `main` is a module of the programs that import it, not a program.

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

Starts an instance. A name taken gives the instance that has it. Prints the instance as JSON. A flow of a module is named as it is written, `Easy`, or as the program names it, `easy.Easy`.

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

Opens the console of the daemon in a browser. People sign in to it with a name and a password; see `qoqi users`.

### `qoqi users`

```console
$ qoqi users add ada                  # the password is asked for, twice
$ qoqi users add lin --admin          # deploys code and changes people too
$ echo "$PASSWORD" | qoqi users add ci --password-stdin
$ qoqi users ls
$ qoqi users passwd ada               # their sessions end; they choose another at their next sign in
$ qoqi users rm ada
```

The people who sign in to the console, through the daemon. The console starts with `admin`, password `admin`; a password given to someone, here or in the console, is theirs to change at their next sign in, before anything else.

A person added is an operator: they start, send to, cancel, resume, answer and end instances, and read everything. An admin, added with `--admin`, also edits, tries, tests and deploys programs, and adds and removes people. Code deployed runs on the daemon's host with the daemon's rights, through its steps: whoever deploys can do there whatever the daemon can, so make admins only of those you would give a shell on that host. The daemon's token is an admin's. People added before roles existed are admins.

### `qoqi gen`

```console
$ qoqi gen go <program> [--package steps] [--queue billing]
$ qoqi gen php <program> --namespace 'App\Flow' --out src/Flow [--queue billing]
$ qoqi gen ts <program> [--from @qoqi/flow] [--queue billing]
```

Writes what an application needs to carry out the steps of its queues: their types, an interface for each queue, and the handler that serves it. Every step of a queue is written with its shape, called by the program or not. Go and TypeScript are written to the standard output; PHP to a file per class. See [steps in your application](../guides/application-steps.md).

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
