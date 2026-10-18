# Qoqi Flow and Restate

Restate is a durable execution server, one Rust binary, with SDKs for TypeScript, Java, Kotlin, Go, Python and Rust. Your services stay ordinary services, in your own language; Restate sits in front of them like a reverse proxy, keeps a journal of what each handler did, and sends it back to the handler when it runs again. Qoqi Flow is a language and a runtime made for flows: a flow is written in `.qi`, and the daemon keeps its state. The difference that matters most: a Restate handler recovers by being played again against its journal, so the code around the journaled actions must do the same thing each time; a Qoqi instance is frozen and thawed, so there is nothing to play again.

## The same flow in both

An expense: tell the manager, wait up to 7 days for an answer, pay once.

In Restate, with the TypeScript SDK, as a workflow. The workflow's ID (`ctx.key`) is the expense's, and a workflow runs once per ID:

```ts
import * as restate from "@restatedev/restate-sdk";

const expenseApproval = restate.workflow({
  name: "expenseApproval",
  handlers: {
    run: async (ctx: restate.WorkflowContext, expense: Expense) => {
      await ctx.run("notify", () => notifyManager(ctx.key, expense));

      let answer: Answer;
      try {
        answer = await ctx.promise<Answer>("answer").get().orTimeout({ days: 7 });
      } catch (e) {
        if (e instanceof restate.TimeoutError) return { status: "expired" };
        throw e;
      }

      if (!answer.approved) return { status: "refused", by: answer.by };

      // Run again after a crash: the provider takes the key and pays once
      const payout = await ctx.run("pay", () => pay(ctx.key, expense), { maxRetryAttempts: 5 });
      return { status: "paid", payout };
    },

    answer: async (ctx: restate.WorkflowSharedContext, answer: Answer) => {
      await ctx.promise<Answer>("answer").resolve(answer);
    },
  },
});

restate.serve({ services: [expenseApproval], port: 9080 });
```

`notifyManager` and `pay` are ordinary async functions; `pay` sends `ctx.key` as the idempotency key. When its retries run out, `ctx.run` throws a `TerminalError`, and the workflow fails unless it catches it.

In Qoqi Flow:

```qoqi
pub struct Expense {
    id: String;
    employee: String;
    cents: Int;
    answers: Inbox<Answer> = flow.inbox<Answer>(1);
}

impl Flow<Outcome> for Expense {
    fn run() -> Outcome {
        notifyManager(self.id, self.employee, self.cents);

        let answer = wait match {
            case let a = self.answers.receive(): a;
            case time.after(7.days()): null;
        };

        if answer == null {
            return Outcome.Expired;
        }

        if !answer.approved {
            return Outcome.Refused(answer.by);
        }

        match pay(self.employee, self.cents) {
            case Ok(payout): { return Outcome.Paid(payout); }
            case Err(why): { return Outcome.Unpaid(why); }
        }
    }
}

// Run again after a crash under the same key: the provider makes one payout
pub step fn pay(employee: String, cents: Int) -> Result<String, String> within 30.seconds() {
    let sent = http.to(Method.Post, "https://payouts.example.com/v1/payouts")
        .header("Idempotency-Key", flow.key())
        .body("{\"to\": \"${employee}\", \"cents\": ${cents}}")
        .send();

    match sent {
        case Ok(response): { return Ok(response.body); }
        case Err(error): {
            if flow.attempt() < 5 {
                flow.retry(error.describe(), 1.minutes());
            }

            return Err(error.describe());
        }
    }
}
```

`notifyManager` is a `step fn` too. A message reaches the `answers` inbox through the command line or the HTTP API.

## Recovery after a crash

Restate records each action of a handler in a journal: the result of a `ctx.run`, a call to another service, a timer, a promise. When a handler fails or its process stops, Restate calls it again with the journal; the handler runs from the top, and each action already in the journal gives back its recorded result instead of running. An action that was running when the process stopped is run again, so `pay` needs an idempotency key, as it does in Qoqi.

A Qoqi instance is not run from the top. At each wait and each step, the VM's state (the calls in progress and their variables) is written to the store; after a crash the instance is thawed from that state and goes on. A step that may have half run is run again under the same key `flow.key()`, or, if it returns `Settled<T>`, is not run again and the flow gets `Unknown` (see [steps](../concepts/steps.md#after-a-crash)).

## Determinism

Because a Restate handler is played again, the code between journaled actions must make the same actions in the same order each time. The SDK gives what replay needs: `ctx.run` for anything non-deterministic, `ctx.date.now()` for the time and `ctx.rand` for random numbers and UUIDs. A handler that takes another path on replay fails with a non-determinism error.

Qoqi flow code need not be deterministic. Nothing is played again, so a flow may read the clock, draw a random number or branch on anything; the clock and random numbers are given back to the flow when it is taken up.

## Reaching the world

In Restate, a call to the outside belongs in `ctx.run`. Nothing stops a handler from calling `fetch` directly: the call works, is not journaled, and runs again on every replay. Inside `ctx.run` the context cannot be used (no `ctx.sleep`, no nested `ctx.run`).

In Qoqi, the compiler follows every call from `run` and refuses one that reaches the network, a file or a database outside a `step fn`, with error `F0001` and the call path that leads there (see [the rule](../concepts/steps.md#the-rule-checked-by-the-compiler)).

## Waiting

Restate has `ctx.sleep` with no hard limit, `.orTimeout(...)` on any of its promises (it throws `TimeoutError`), and `RestatePromise.race`, `any`, `all` and `allSettled`. A person or another system answers through an awakeable (`ctx.awakeable`, completed by its ID) or, in a workflow, a durable promise by name (`ctx.promise`, resolved from a shared handler). On a platform such as AWS Lambda, a handler that waits is suspended and costs nothing.

In Qoqi, a flow waits for a timer, a message on an inbox, or the first of several with `wait match` (see [waiting](../concepts/waiting.md)). While it waits, the instance is only rows in the store.

## New versions of the code

A Restate deployment is immutable: each version of a service gets its own endpoint and is registered with the server. New requests go to the newest deployment; an invocation in progress stays on the one it started on, and that deployment must stay up until its invocations end. The docs advise against handlers that run for days or months, and suggest splitting them with delayed calls. A bug inside a `ctx.run` can be fixed in place; changing the order, number or inputs of Restate actions cannot. A stuck invocation can be paused and resumed on a newer deployment, if the fix still fits the journal it has. The state of a Virtual Object is kept across versions.

A Qoqi instance is pinned to its version too, until it reaches a `stage`: there it moves to the newest version, if the variables alive at that stage still have the shape recorded in `flow.lock`. When they do not, a `migrate fn` says how, and the daemon refuses a deploy that would leave an instance alive with no way to move. A step's body always runs in the newest version. See [versions](../concepts/versioning.md).

## Testing

Restate's TypeScript testing package, `@restatedev/restate-sdk-testcontainers`, starts a real Restate server in Docker (`RestateTestEnvironment.start`). Tests call handlers through it, read state with `stateOf`, and can set `alwaysReplay`, which replays the handler at every suspension point to find non-determinism, and `disableRetries`. The testing docs do not describe a clock a test can move on.

A Qoqi trial runs in the test process, on a store in memory and a clock of its own: `trial.advance(7.days())` takes no time. `trial.replace` stands in for a step, and `everyCrash` runs a scenario again with the process stopped after each write the instance made. See [testing flows](../guides/testing-flows.md).

## Running it

Restate is a server to run, plus your services. The server keeps its log in Bifrost, a replicated log, with RocksDB per partition and snapshots in S3 or GCS; it starts as one node and grows into a cluster. Your services run where you like: containers, Kubernetes, AWS Lambda, Vercel, Cloudflare Workers, Cloud Run, Deno Deploy. They are called over HTTP or from Kafka. Restate Cloud is the managed service, with a free tier; there is also a bring-your-own-cloud option, or you host it yourself. It has a web UI and a CLI.

Qoqi Flow is one Go binary, the daemon, on a store of SQLite, PostgreSQL or MySQL. Several daemons can share one PostgreSQL or MySQL store, each instance held by one daemon under a 30-second lease. There is a web console and an HTTP API, and no managed hosting. See [running in production](../guides/production.md).

## Where Restate is ahead

*   **Maturity.** Restate 1.0 shipped in June 2024; the server is at 1.8 and the company says hundreds of companies run it. Qoqi Flow is at v0.1.0, from October 2026, with no years in production.
*   **Languages.** Handlers are written in TypeScript, Java, Kotlin, Go, Python or Rust, in the services you already have. In Qoqi, flows are written in `.qi`; Go, PHP and TypeScript come in only as application steps behind a queue.
*   **More than workflows.** Plain durable services and Virtual Objects, with their own key-value state and one writer per key, are part of the same model. Qoqi has flows and steps.
*   **Where the code runs.** Services can be serverless functions, suspended while they wait. A Qoqi flow runs in the daemon.
*   **Managed hosting.** Restate Cloud exists. Qoqi has none.
*   **Scale and operations.** A replicated log, partitions, snapshots to object storage, clusters, Kafka as a source of calls.

## Where Qoqi Flow is different

*   **No replay, no determinism rules.** The state of an instance is frozen and thawed; flow code may read the clock or branch on anything.
*   **I/O outside a step is a compile error**, not a mistake found on replay or not at all.
*   **Long waits are the normal case.** An instance waits days or months in the store, and moves to new code at a `stage`, checked against `flow.lock`, with `migrate fn` for a change of shape. Old versions need no server kept up for them.
*   **Steps that must never run twice** return `Settled<T>`, and the flow must say what `Unknown` means.
*   **Crash tests and time in tests.** `everyCrash` and a trial's own clock, in the test process, with no container.
*   **Its license.** Qoqi Flow is open source. The Restate server is under the Business Source License 1.1, which its repository says is not an open-source license; each release becomes Apache 2.0 four years after it. Its SDKs are MIT.

## Sources

*   https://github.com/restatedev/restate
*   https://github.com/restatedev/restate/blob/main/LICENSE
*   https://docs.restate.dev/concepts/durable_execution
*   https://docs.restate.dev/foundations/key-concepts
*   https://docs.restate.dev/references/architecture
*   https://docs.restate.dev/develop/ts/journaling-results
*   https://docs.restate.dev/develop/ts/durable-steps
*   https://docs.restate.dev/develop/ts/durable-timers
*   https://docs.restate.dev/develop/ts/external-events
*   https://docs.restate.dev/develop/ts/testing
*   https://docs.restate.dev/operate/versioning
*   https://docs.restate.dev/server/clusters
*   https://docs.restate.dev/hosting/overview
*   https://restate.dev/blog/announcing-restate-1.0-restate-cloud-and-our-seed-funding-round/
