# Contributing

Issues and discussions are on [GitHub](https://github.com/qoqiflow/qoqi). A change comes with its tests, in the same pull request, at every level it touches.

## Building

```console
$ make build          # ./qoqi; needs Go 1.25
$ make test           # unit and end to end
$ make all            # the console too, then qoqi with it; needs Node
```

The console (`console/`, React) is built into `internal/flowd/console`, which is committed: building `qoqi` needs no Node. Rebuild it with `make console` after changing it.

## Where things are

| | |
|--|--|
| `cmd/qoqi` | The command |
| `internal/parser`, `resolve`, `types`, `desugar`, `monomorph`, `analysis`, `irgen`, `codegen`, `compiler` | The compiler, one package per stage ([pipeline](docs/internals/compilation/pipeline.md)) |
| `internal/vm` | The virtual machine, its natives, freezing and thawing a program |
| `internal/stdlib` | The standard library, in Qoqi (`lib/*.qi`) |
| `internal/flows` | What the compiler checks of flows: the one rule, steps, stages, `flow.lock` |
| `internal/flowrt` | The runtime of flows: instances, the store, steps and their keys |
| `internal/flowd` | The daemon: deploys, the HTTP API, queues, the console |
| `internal/flowgen` | `qoqi gen`: the code an application in Go, PHP or TypeScript needs |
| `internal/formatter`, `lsp`, `testrun`, `pkgmgr` | `qoqi fmt`, `lsp`, `test` and `pkg`, each a Qoqi program kept in the binary |
| `pkg/flow`, `sdk/ts`, `sdk/php` | The SDKs for applications |
| `console/` | The console's source |
| `editors/vscode` | The VS Code extension |

## Style

*   Go: `gofmt`. Qoqi: `qoqi fmt`, which CI checks.
*   Messages, comments and documentation say what a thing is and what to do, in plain sentences.

## Tests

Few tests, each worth having: a test for what a user depends on, at the level where a failure says the most. Every change to the language, the runtime or the daemon comes with its tests in the same change, at every level it touches.

| Level | What it exercises | Where | Run with |
|-------|-------------------|-------|----------|
| Unit | One package at a time, most of them driven from Qoqi source text | `internal/*/..._test.go`, `pkg/flow` | `make test-unit` |
| End to end | The `qoqi` binary as a person runs it: programs, the daemon and its commands, the shop served by applications in Go, PHP and TypeScript, the packages | `test/e2e/` | `make test-e2e` |
| Searching | Inputs made by a machine, in a sandbox; whole programs, measured | `test/fuzz`, `test/bench` | `make test-e2e`, `make fuzz`, `make bench` |

`make test` runs the first two. Every target runs with a time limit, and the packages that run programs end themselves if the test binary comes to hold more than 4 GB (`internal/memguard`, from a `TestMain` in each): a test that goes wrong fails, it does not take the machine down. When running `go test` by hand, pass `-timeout`.

### Unit tests

#### Flows

| Package | What is tested |
|---------|----------------|
| `internal/flows` | The one rule: the walk from `run`, the world reached directly and from deep inside, steps and what only steps reach, what is not the world, calls through values and traits, `F0001` with its path of calls; what a step may be given and return; the shapes of stages against `flow.lock` |
| `internal/flowrt` | The runtime: an instance frozen in a native, kept in the store, taken up on this daemon or another; steps kept once and never run twice; `Settled` and `Unknown`; inboxes and their keys; undo; cancel; stages and migrations; leases between daemons; the stores on SQLite, PostgreSQL and MySQL (the servers when `QOQI_TEST_POSTGRES` and `QOQI_TEST_MYSQL` name one) |
| `internal/flowd` | The daemon: deploy, versions, the diff of two deploys, the HTTP API every command goes through, the queues an application serves, the token, several daemons on one store, the console's pages |
| `internal/flowtest` | The trials of `std.flow`: `go`, `advance`, `send`, `replace`, `result`, and `everyCrash`, which stops the process after each write of a scenario and requires the same ending every time |
| `internal/flowgen` | What `qoqi gen` writes for Go, PHP and TypeScript, against what the SDKs compile |
| `pkg/flow` | The Go SDK: signatures, keepers, answers, retries, the handler that serves a queue |

#### The language

| Package | What is tested |
|---------|----------------|
| `internal/parser` | Tokens and positions; every construct; operator precedence; `a < b` against `f<T>(x)`; struct literal against block; string interpolation as the tokens it becomes; every error code; recovery after a broken statement |
| `internal/modules` | Programs of several files held in a map: names reached through each form of import; what a local variable or type parameter hides; chains of re-exports; each file read once; every error code with its message and suggestion |
| `internal/resolve`, `internal/types` | Builtins in scope; the types inferred; programs that must be accepted and programs that must be rejected, with the message: methods, traits, `Self`, optionals and narrowing, type tests, generics and the inference of type arguments, casts, `const`, return paths, enums and the completeness of a `match`, the warnings |
| `internal/desugar`, `internal/analysis`, `internal/irgen` | What each lowering keeps: loops evaluate their iterable once, short-circuit operators stay, patterns become tests in the right order; which variables each lambda captures; `let` copies, scopes, calls direct and through traits |
| `internal/codegen` | Small IR functions generated and **executed**, asserting on the value: every constant kind, arguments surviving calls, closures sharing variables, temporaries sharing registers, each limit of the bytecode format with its error |
| `internal/compiler` | One test per feature, a short program whose output is compared: control flow, functions, arrays, structs and traits, closures, generics, `match`, enums and constants, failures (`panic`, `attempt`, `Result`), programs of several files, the collector keeping live values across many collections, one compile error per stage by its code. **When a program misbehaves, the smallest reproduction goes here first**, then down to the package that owns the bug |
| `internal/vm` | Instructions executed one at a time, runtime errors with their kind, code, suggestion and stack, how a value prints and when two are equal, type tests and casts, the natives of the library called directly (files in a temporary folder, input, HTTP against a Go server), the collector under pressure, wide operands, the limits on memory and depth, freezing a program and thawing it |
| `internal/stdlib` | Every module of the library used from Qoqi programs, with ordinary and edge inputs; the runtime errors a misuse gives; that each `extern fn` has a native and each native a declaration; the library under a heap of 3 MB |
| `internal/formatter` | Each rule of the style; every `.qi` file of the repository formatted and compared, token by token, with what it was, then formatted again to change nothing; the dense programs of `testdata` against what they have to become |
| `internal/testrun`, `internal/project`, `internal/pkgmgr`, `internal/inspect`, `internal/lsp` | `qoqi test`, `qoqi.json`, the package manager, `std.compiler` and the language server, each as the Qoqi program it is |

The tests of the stages are driven from source text: a helper parses (and resolves, type checks, ...) a snippet and the test asserts on the result. This keeps them short and independent of how AST nodes are built by hand.

### End to end

`test/e2e` builds the `qoqi` binary once and runs it on real files, checking stdout, stderr and the exit code.

*   `flow_test.go`, `flow_deploy_test.go`, `flow_queues_test.go`: the daemon started on the examples, instances started, listed, shown, sent to, cancelled, resumed, answered and ended from the command line; a second daemon on the same store refused; deploys, versions and their diff; what happens to instances when the daemon is killed and started again; the queues an application serves.
*   `flow_go_test.go`: the shop of `examples/shop`, its steps carried out by the application in Go, then in PHP, then in TypeScript, each generated by `qoqi gen` and compiled; the orders that succeed and the ones that are declined, undeliverable or out of stock, with the undo each one runs.
*   `cases/NAME.qi` is run with `qoqi run`; optional files next to it state what to expect: `NAME.out` (exact stdout), `NAME.err` (lines that must appear in stderr), `NAME.exit` (the status). `go test ./test/e2e -update` rewrites the `.out` files; `.err` and `.exit` are written by hand, because they state intent.
*   Every example is run and compared with `examples/expected/NAME.out`, so the examples in the repository are guaranteed to work; `qoqi test examples/flows` runs the trials of the example flows.
*   `packages_test.go` publishes packages to git repositories of the test's own and drives `qoqi pkg require`, `install`, `update`, `remove`, `show`, `why` and `outdated` on a project.
*   `tools_test.go`, `scripts_test.go`, `lsp_test.go`, `cli_test.go`: `qoqi fmt` and `qoqi test` as a person would run them, scripts with `#!/usr/bin/env qoqi`, `qoqi check` as text and as JSON, the language server formatting as an editor does on save, signals, the limits on memory and depth and their options, `--cpuprofile`, and the usage errors.
*   A run that takes longer than 20 seconds fails the test: performance that regresses that far is caught here.

### Searching

*   `test/fuzz`: `FuzzSource` gives the compiler any text and runs what compiles; `FuzzBytecode` gives the bytecode loader any bytes and runs what loads; `TestRandomProgramsMeanWhatTheySay` makes programs at random and compares what each prints with what a few lines of Go say it means; `TestRandomProgramsMeanTheSameFormatted` formats them and runs them again. Everything runs in `internal/sandbox`: bounded steps, memory, depth and output, and no native that reaches outside the program. `make fuzz` lets the fuzzers search for new inputs, a minute each; an input that fails is saved under `test/fuzz/testdata` and runs with the tests from then on. Commit it with the fix.
*   `test/bench`: one program per part of the language, each with the output it must print, timed by `make bench`, and the compiler on them. Before a change meant to make Qoqi faster or smaller, save the output; after, compare with `benchstat`.

### Running

```bash
make test                                   # unit and end to end
make test-unit                              # one level
make test-e2e
make test-race                              # the packages that run programs, under the race detector
go test ./internal/compiler -run TestClosures -v
go test ./test/e2e -run 'TestCases/features' -v
go test ./test/e2e -run 'TestTheShopInGo' -v
```

### Writing a test

*   Name it by what it checks, as a sentence: `TestAStepIsNeverRunTwice`, `TestNoAnswerGoesUp`.
*   Test the contract, not the implementation; prefer a program that prints over an assertion on a structure.
*   Keep assertions plain, with the standard library: `if got != want { t.Errorf("got %v, want %v", got, want) }`.
*   A bug fix comes with the test that would have found it, at the level where it showed.
*   Nothing is written into the repository by a test: files go in `t.TempDir()`, stores next to them, caches in `QOQI_CACHE`.
