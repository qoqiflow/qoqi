# Questions

The questions people ask first, answered plainly.

## Why a new language, and not a library?

Because the thing that makes Qoqi Flow different needs it. A library cannot write down where a Go, Python or TypeScript function is and what its locals hold, then resume it tomorrow on another machine; so every library brings a workflow back by calling it again and skipping what was done, and the code has to replay identically. Qoqi owns its virtual machine, so it writes down the state of execution itself and thaws it at the line it was at. No replay, no determinism rules, and a compiler that knows which call reaches the world ([steps](concepts/steps.md#the-rule-checked-by-the-compiler)).

The language is small and ordinary on purpose: structs, enums, `match`, `Result`, closures, tasks. [A tour](reference/language/tour.md) takes half an hour.

## Do I have to rewrite my application in Qoqi?

No. Only the flow is Qoqi: the process, its waits and its decisions. A step that needs your code, your database or your models stays in your service, in any language that answers Qoqi's protocol, a signed HTTP POST. `qoqi gen` writes the typed interfaces for Go, PHP and TypeScript; another language implements [the protocol](guides/application-steps.md#the-protocol) in a page of code.

## What exactly is frozen?

Every task of the instance, every call in progress with its function and instruction pointer, the registers, and the objects of the heap they point to, written as JSON in the format `qoqi.vm/2`, beside the name of the bytecode it runs on. An expense waiting for a manager is about 3 KB. See [the runtime](internals/runtime.md).

## Is the code of a flow really free of determinism rules?

Yes. It is never run twice: it may read the clock, draw a random number, iterate a map in any order. What it read is frozen with the rest. The one rule is that the world is reached only through a step, and the compiler checks it (`F0001`).

## Can a step run twice?

A step's result is written before the flow goes on, and a step that finished is never run again. A step stopped half way by a crash is run again under the same key, `flow.key()`, so that a provider which takes idempotency keys answers the second request like the first. A step declared `Settled<T>` is never run again by the daemon: the flow is told `Unknown` and decides. See [after a crash](concepts/steps.md#after-a-crash).

## How do I ship a new version under running instances?

`qoqi deploy`, with no restart. An instance stays on the code it started with, and moves to the new version at its next `stage`, if the variables alive there have the same shape; `flow.lock` records each shape, the compiler warns when one changes, and the daemon refuses such a deploy while an instance is at that stage until a `migrate fn` says how. See [versions](concepts/versioning.md).

## What happens when I upgrade qoqi itself?

The bytecode of every version deployed is kept in the store, so an instance is always thawed on the code it was frozen with, whatever the compiler became. The format of a frozen instance is named, `qoqi.vm/2`, and a daemon refuses a state of another format rather than guess. Until 1.0 that format may change: read the release notes before upgrading a daemon that has instances in flight.

## How fast is it? What does a waiting instance cost?

A waiting instance is a row in the store and nothing else: no process, no memory, no timer in a worker. While steps are under way, the whole state is written at most once per ten milliseconds, and the steps of those ten milliseconds leave together. `--workers` instances run side by side, one per processor by default. There are no published throughput numbers yet.

## Where is the state, and who can read it?

In the store: SQLite by default, PostgreSQL or MySQL to share it. What a flow was given and what its steps returned are written there as JSON, so do not give a flow what must not be stored. The daemon listens on `127.0.0.1` by default, every request carries its token or a person's session, and requests to your services are signed with each queue's secret. See [security](guides/production.md#security).

## Can it run on several machines?

Yes, on PostgreSQL or MySQL. Daemons share one store; an instance is held by one daemon at a time under a 30-second lease, and taken up by another when its daemon dies. See [several daemons](guides/production.md#several-daemons).

## Is it ready for production? Who uses it?

It is at v0.2.3. The language, the runtime and the daemon work end to end, and they are tested by crashing them on purpose after every write (`everyCrash`). It does not have years of production behind it yet. Start with a flow that matters but does not hold the company up, and tell us how it goes.

## How is it licensed? Is there hosting?

Apache 2.0, the whole of it: the compiler, the daemon, the console. There is no managed hosting: it runs wherever one binary runs.

## How does it compare with Temporal, DBOS, Restate, Inngest?

Side by side, with code: [Qoqi Flow vs the rest](/vs/).
