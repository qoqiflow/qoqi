<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/qoqi-wordmark-white.svg">
    <img src="assets/qoqi-wordmark.svg" height="80" alt="Qoqi">
  </picture>
</p>

<p align="center"><b>Durable workflows as plain programs.</b></p>

# Qoqi Flow

Write the process from top to bottom: wait three days, ask a manager, charge a card, retry, give up. Qoqi Flow keeps it alive through crashes, restarts and new versions of its own code, and never runs a finished step twice. Qoqi Flow is the product; `qoqi` is the language it is written in, and the command.

```rust
import std.flow;
import std.flow.{Flow, Inbox};
import std.time;

struct Expense {
    id: String;
    author: String;
    answers: Inbox<Answer> = flow.inbox<Answer>(1);
}

struct Answer {
    approved: Bool;
    why: String;
}

enum Decision {
    Approved(String),
    Refused(String),
    Unanswered,
}

impl Flow<Decision> for Expense {
    fn run() -> Decision {
        let approver = managerOf(self.author);

        for _ in 0..3 {
            ask(approver, self.id);

            // The process may stop here for days, and pick up where it was
            let answer = wait match {
                case let a = self.answers.receive(): a;
                case time.after(2.days()): null;
            };

            if answer != null {
                if answer.approved {
                    return Decision.Approved(approver);
                }

                return Decision.Refused(answer.why);
            }

            approver = managerOf(approver);    // nobody answered: one level up
        }

        return Decision.Unanswered;
    }
}

// The only way a flow reaches the world. What a step returned is kept,
// and it is never done again.
step fn ask(approver: String, id: String) { ... }
step fn managerOf(name: String) -> String { ... }
```

```console
$ qoqi serve &                      # the daemon: its files in ~/.qoqi (QOQI_PATH)
$ qoqi deploy approvals.qi
$ qoqi start Expense expense:1 '{"id": "1", "author": "lin"}'
$ qoqi send expense:1 answers '{"approved": true, "why": ""}'
$ qoqi result expense:1
Approved("grace")
```

> **Status: early.** The language, the runtime and the daemon work end to end, with tests. The interfaces may still change. The documentation is also at [qoqi.dev](https://qoqi.dev).

## Why

A process that waits (for a payment, a person, a webhook, a date) is usually taken apart by hand. The line it is at becomes a `status` column, its variables a JSON field, its `sleep` a delayed job, its retries a counter and a cron, its crash recovery a sweeper. Nobody can read the process from top to bottom any more, and the bugs live in the gaps.

Qoqi Flow keeps it a program. While a flow waits, its whole state of execution is frozen in a store: the calls in progress and their variables. When the wait ends, minutes or months later, on this machine or another, it is thawed and goes on from the line it was at.

## Features

- **Checked when it compiles.** A flow reaches the outside world only through a `step fn`. The compiler follows every call a flow can make, and points at the one that reaches the network, a file or a database outside a step (`F0001`), with the path of calls that leads there.
- **Frozen, not replayed.** An instance is taken up from what was saved, not by running its history again. The code of a flow need not be deterministic, and a new version never has to re-run an old history.
- **Steps run once.** What a step returned is written before the flow goes on. After a crash, a step that completed is not called again; one that may have half run is run again under the same key, which `flow.key()` gives it for the world. A step declared `Settled<T>` is never run twice: the flow gets `Unknown` instead, and says what that means.
- **New versions, checked.** `stage` marks where a running instance may move to new code, and `flow.lock` records the shape of each stage. A version that changes a shape without a `migrate fn` is flagged by the compiler, and refused by the daemon while an instance is at that stage. Instances are not pinned to old code for months.
- **Tests that crash on purpose.** A trial runs a flow on a clock of its own, so three days pass in a moment. `everyCrash` runs a scenario again, stopping the process after each write, and requires the same ending every time.
- **Steps in your application.** A step can be carried out by your own service, in Go, PHP or TypeScript, through a queue. `qoqi gen` writes the typed interfaces from the flow.
- **One binary.** The compiler, the daemon, an HTTP API, a web console, a test runner, a formatter and a language server. The store is SQLite, PostgreSQL or MySQL.

## Getting started

Install it (Linux or macOS), or [build it from the source](docs/getting-started/installation.md#from-the-source):

```console
$ curl -fsSL https://qoqi.dev/install.sh | sh
```

Write a flow, `reminder.qi`:

```rust
import std.flow;
import std.flow.{Flow};
import std.time;

struct Reminder {
    user: String;
}

impl Flow<Void> for Reminder {
    fn run() {
        wait time.after(3.days());

        if !hasLoggedIn(self.user) {
            sendReminder(self.user);
        }
    }
}

step fn hasLoggedIn(user: String) -> Bool { ... }
step fn sendReminder(user: String) { ... }
```

Serve it and start an instance:

```console
$ qoqi serve &
$ qoqi deploy reminder.qi
$ qoqi start Reminder reminder:ada '{"user": "ada"}'
$ qoqi ls
$ qoqi ui                   # the console, in a browser: admin, admin the first time
```

## Writing flows

A flow is a struct that implements `Flow<T>`. Its fields are the state of each instance, `run` is what an instance does, and `T` is what it returns at the end. Everything else is ordinary code: functions, `match`, `Result`, `defer`, tasks.

| A flow | Written with |
|--------|--------------|
| reaches the world | `step fn` |
| waits for a time | `wait time.after(3.days())` |
| waits for a message | an `Inbox<T>` field: `wait self.answers.receive()` |
| waits for the first of several | `wait match { case ... }` |
| does several things at once | `task.start`, `wait task.all(...)` |
| undoes what it did, if it fails | `flow.undo(step, argument)` |
| hears that it is cancelled | `case let reason = flow.cancelled():` |
| may move to new code here | `stage name;` |
| moves its state to new code | `migrate fn name(...)` |

There is no `async`, and no special kind of function: `run` calls ordinary functions, they call others, and the whole of it is durable.

## Testing

```rust
fn testNoAnswerGoesUp() {
    let trial = flow.test(Expense { id: "2", author: "lin" });

    trial.go();
    trial.advance(2.days());       // two days, in a moment
    trial.send("answers", Answer { approved: true, why: "" });

    test.equal("${trial.result()}", "Approved(\"ada\")");
}

// The same scenario again, with the process stopped after each of its
// writes: it has to end the same every time
fn testAnAnswerSurvivesCrashes() {
    flow.test(Expense { id: "3", author: "lin" }).everyCrash(fn(trial: flow.Trial<Decision>) {
        trial.go();
        trial.advance(2.days());
        trial.send("answers", Answer { approved: true, why: "" });
    });
}
```

```console
$ qoqi test
```

`trial.replace(step, fn)` stands a function in for a step, so a test reaches no real service.

## Steps in your application

A step declared `by` a queue has no body in the flow. The daemon hands it to your application, and keeps what the application answers. The declaration can also say how long the step may take, and which step undoes it.

```rust
step fn charge(customer: String, cents: Int) -> Result<String, Decline>
    within 30.seconds() by payments undo refund;
```

Queues are declared in `qoqi.json`, with their address and secret: `"payments": { "url": "env:PAYMENTS_URL", "secret": "env:SHOP_SECRET" }`. The typed interfaces of the steps are written from the flow:

```console
$ qoqi gen go  shop.qi --package steps > steps/steps_gen.go
$ qoqi gen php shop.qi --namespace 'App\Flow' --out src/Flow
$ qoqi gen ts  shop.qi > steps.ts
```

The same shop, with its application written three times, is in [examples/shop](examples/shop).

## Running in production

```console
$ qoqi serve --store postgres://user:pass@db/flows --workers 8
$ qoqi deploy shop.qi              # a new version, served at once; from anywhere with QOQI_URL, QOQI_TOKEN
$ qoqi versions                    # what runs on each version
$ qoqi diff 12                     # what a deploy changed
```

On PostgreSQL or MySQL, daemons on several machines share one store. Instances move to a new version at their next `stage`. A stopped instance is shown with what it was doing, and can be resumed, answered or ended (`qoqi resume`, `qoqi answer`, `qoqi end`).

## How it compares

| | Temporal, Restate, Inngest, DBOS, Vercel Workflow SDK and others | Qoqi Flow |
|--|-----------------------------------|-----------|
| What it is | A library or service added to an existing language | A language and runtime made for flows |
| Recovery after a crash | A history played again; the code must be deterministic | The state frozen and thawed |
| Reaching the world outside a step | Found when it runs, or not at all | Refused when it compiles |
| A new version of the code | A run is pinned to the version that started it, or patched by hand | Pinned too, until the instance reaches a `stage`, where it moves, checked against `flow.lock` |
| Testing crashes | Left to you | `everyCrash` |

What they have and Qoqi Flow does not yet: years in production, managed hosting, and SDKs for writing the flows themselves in many languages.

## Documentation

- [Quickstart](docs/getting-started/quickstart.md): a flow served, started, answered and tested in five minutes
- [Concepts](docs/concepts/flows.md): flows, steps, waiting, failures, versions
- [Guides](docs/README.md#guides): testing flows, steps in your application, running in production
- [Reference](docs/README.md#reference): the command line, the HTTP API, `qoqi.json`, error codes, the language

## Contributing

Issues and discussions are on GitHub. See [CONTRIBUTING.md](CONTRIBUTING.md) for how to build, where things are, and how the project is tested.

## License

Apache License 2.0. See [LICENSE](LICENSE).
