# Qoqi Flow and the others

Temporal, DBOS, Restate, Inngest and the Workflow SDK make code durable in a language you already use. Qoqi Flow is a language made for it. Most of the differences below come from that one choice.

## The one idea

A durable workflow has to survive the process that runs it. There are two ways to bring it back after a crash, or after a wait of three days.

**Run it again, and skip what was done.** The others record what each step returned. To take a workflow up, they call its function again from the top; each step already recorded returns its saved result instead of running. The code runs again until it reaches the point where it stopped. For that to land in the same place, the code between the steps must take the same path every time: it must be **deterministic**. Temporal checks the calls against an event history, Restate against a journal, DBOS and Inngest look a step up by its order or its id, the Workflow SDK runs workflows in a sandbox with a seeded `Math.random` and a logical clock.

**Freeze it, and thaw it.** Qoqi Flow writes the state of execution itself: the calls in progress and their variables, at the line the flow is at. To take it up, it reads that back and goes on from the line. Nothing runs again, so nothing has to be deterministic, and the length of a history is no limit. A step is still the only place where the world is reached, and the compiler checks that.

```rust
impl Flow<Void> for Trial {
    fn run() {
        welcome(self.user);
        wait time.after(3.days());       // the instance is a row in the store
        if !hasCreatedAProject(self.user) {
            sendTips(self.user);
        }
    }
}
```

## Side by side

| | Qoqi Flow | Temporal | DBOS | Restate | Inngest | Workflow SDK |
|--|--|--|--|--|--|--|
| What it is | A language and its runtime | A server, and SDKs | A library on Postgres | A server, and SDKs | A platform, and SDKs | A TypeScript SDK and its compiler |
| Languages | Qoqi; steps in Go, PHP, TypeScript through queues | Go, Java, Python, TypeScript, .NET, Ruby, PHP, Rust | Python, TypeScript, Go, Java | TypeScript, Java, Kotlin, Python, Go, Rust | TypeScript, Python, Go | TypeScript (Python in beta) |
| Taken up by | Thawing the frozen state | Replaying the event history | Running the workflow again over its checkpoints | Replaying the journal | Calling the function again over its saved steps | Replaying the event log |
| Code between steps | Anything | Deterministic | Deterministic | Deterministic | Should be: it may run again | Deterministic, in a sandbox |
| The world reached outside a step | Refused by the compiler (`F0001`) | Not stopped (a static checker in Go, a sandbox in Python) | Not stopped | Not stopped | Not stopped | Fetch and timers removed from workflows |
| Running instances on new code | Move at a `stage`, checked against `flow.lock` | Pinned to a worker version, or patches (`GetVersion`, `patched`) | Pinned to an application version; patches; fork from a step | Pinned to a deployment | Matched by step id | Pinned to the deployment that started them |
| Testing | Trials on their own clock; `everyCrash` | Test environment that skips time; replay tests | Pytest fixtures; no tool for crashes | Real server in Docker; `alwaysReplay` | Test engine; waits mocked | Vitest plugin; waits skipped |
| Infrastructure | One binary, SQLite, PostgreSQL or MySQL | Server, and Cassandra, PostgreSQL or MySQL, Elasticsearch advised | Postgres (SQLite for development) | One binary, its own replicated log | One binary, Postgres and Redis to scale | Local, Postgres, or Vercel |
| Managed hosting | None | Temporal Cloud | DBOS Conductor (proprietary) | Restate Cloud | Inngest Cloud | Vercel Workflows |
| License | Apache 2.0 | MIT (Java SDK Apache 2.0) | MIT; Conductor proprietary | BSL 1.1 server, MIT SDKs | SSPL server, Apache 2.0 later; Apache 2.0 SDKs | Apache 2.0 |
| Age | v0.1.0, October 2026 | A fork of Uber's Cadence; server 1.32 | Library since 2023 | 1.0 in June 2024 | Released in 2022 | October 2025; GA in April 2026 |

Facts about the others were taken from their documentation in October 2026; each page lists its sources.

## What follows from it

**No determinism rules.** A Qoqi flow can read the clock, draw a random number, or loop over a map in whatever order it comes: it never runs again. In the others, a `time.Now()` or a `Math.random()` between two steps is a bug that shows up only on replay, or the SDK replaces them for you.

**A change of code is a choice, not a hazard.** In a replay engine, code changed under a running workflow can make the replay take another path; each tool has its answer, from patches to keeping the old deployment alive until the last run ends. A Qoqi instance stays on the code it started with, and moves to the new code only at a `stage` where the program says it may, its state checked against `flow.lock`, migrated by a `migrate fn` when the shape changed. See [versions](../concepts/versioning.md).

**The rule is checked before it runs.** All of them ask that the world be reached only from a step. Qoqi's compiler follows every call a flow can make and refuses the one that reaches the network, a file or a database outside a step, with the path that leads there. See [the rule](../concepts/steps.md#the-rule-checked-by-the-compiler).

**Crashes are tested.** `everyCrash` runs a scenario again, stopping the process after each write, and requires the same ending every time. See [testing flows](../guides/testing-flows.md).

**What it costs.** Your flows are written in Qoqi, not in the language of your application. Your application still carries out the steps it owns, in Go, PHP or TypeScript, through [queues](../guides/application-steps.md), with interfaces generated from the flow.

## When to choose another tool

*   **You need it proven.** Temporal has run at very large scale for years; DBOS, Restate and Inngest have production users and companies behind them. Qoqi Flow is at its first release.
*   **You want someone else to run it.** Each of the others has managed hosting. Qoqi Flow has none.
*   **The workflows must be written in your application's language,** by a team that will not learn another one. Qoqi lets your application carry out steps, but the flow itself is Qoqi.
*   **You already have Postgres and want a library, not a service:** DBOS adds durability inside your process.
*   **You deploy on Vercel, in TypeScript:** the Workflow SDK is built in.
*   **You need many languages, a large ecosystem of examples, observability tools and support contracts:** Temporal.

## Each of them

*   [Temporal](temporal.md)
*   [DBOS](dbos.md)
*   [Restate](restate.md)
*   [Inngest](inngest.md)
*   [Workflow SDK](workflow-sdk.md)
