# Flows

A **flow** is a process written as a program: it may wait for days, survive a crash or a restart, move to a new version of its code, and it never does a finished step twice.

```qoqi
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

## Flows and instances

A flow is a struct that implements the trait `Flow<T>` of `std.flow`:

| | |
|--|--|
| Its **fields** | The state of one instance: what it is started with, and its [inboxes](waiting.md#messages-from-outside) |
| Its **`run`** | What an instance does, from start to end |
| **`T`** | What `run` returns when the instance ends |

An **instance** is one run of a flow, under a **name** you choose: `reminder:ada`, `order:4411`. The name is the instance. Starting a name that is taken gives the instance that has it, whatever it was started with, so starting twice starts once.

```console
$ qoqi start Reminder reminder:ada '{"user": "ada"}'
```

The fields are given as JSON ([how values are written](../reference/http-api.md#values)). A field with a default may be left out; an `Inbox` field always has one.

A flow is otherwise ordinary code. `run` calls functions, which call others; they use `match`, `Result`, `defer`, tasks, the whole language. There is no `async` and no special kind of function: what makes the whole of it durable is that the daemon runs it.

## What is durable

While an instance waits, its **whole state of execution** is written to the store: the calls in progress, their variables, the place each is at. When the wait ends, minutes or months later, on this machine or another, it is taken up from what was written and goes on from the line it was at.

It is taken up from what was **written**, not by running its history again. The code of a flow does not have to be deterministic: it may read the clock, draw random numbers, and decide on them.

An instance is written at two kinds of moments:

*   **Before anything leaves the program.** A step's request is sent only after the instance that asks for it is written. So a crash never loses track of something done in the world.
*   **When it goes to sleep**, at a `wait` that has to wait.

After a crash, an instance goes on from its last write. What it computed since is computed again; it reached nothing outside, since anything that would have was preceded by a write. Each step it had asked for is found by its key: if it finished, its result is given back and it is not run again ([steps](steps.md#after-a-crash)).

## The life of an instance

| Status | |
|--------|--|
| `running` | Held by a daemon: its tasks run, or its steps are under way |
| `waiting` | Asleep in the store, until what it waits for comes: a time, a message, a child flow |
| `stopped` | Something needs a person: a mistake in the program, an undo that failed, a step that cannot say what it did. Nothing more happens on its own ([failures](failures.md#stopped-instances)) |
| `done` | `run` returned |
| `failed` | `run` returned an `Err`, or the instance was cancelled; its undos have run |

```console
$ qoqi ls
reminder:ada   Reminder   waiting      waits until 2026-10-21 09:14
order:4411     Order      running      charge
order:4412     Order      stopped      undo release failed: the warehouse is gone

$ qoqi result reminder:ada --wait 30s
```

## Flows that start flows

A flow can start other flows. A **child** is cancelled with its parent, and its undos run before the parent's:

```qoqi
let payment = flow.start("payment:${order.id}", Payment { invoice: invoice });

match wait payment {
    case Ok(outcome): { ... }
    case Err(cancelled): { ... }
}
```

`wait` on an `Instance<F>` gives `Ok` with what its `run` returned, its own `Err` included, or `Err(Cancelled { reason })`. `flow.detach(name, instance)` starts one that is no one's child and outlives the flow that started it.

Starting is by name here too: a flow taken up after a crash that starts `payment:4411` again gets the one it started before.

## The one rule

A flow reaches the world, a file, the network, a database, another program, **only through a [`step fn`](steps.md)**. The compiler checks it, for `run` and every function it can reach, and refuses the program otherwise. That rule is what lets the daemon keep everything else: between two steps, a flow only computes.

## Next

*   [Steps](steps.md): what is kept, keys, retries, time limits.
*   [Waiting](waiting.md): timers, messages from outside, the first of several, tasks.
*   [Failures](failures.md): errors, undos, cancellation, stopped instances.
*   [Versions](versioning.md): deploying new code under running instances.
