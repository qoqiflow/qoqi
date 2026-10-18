# Qoqi Flow and Inngest

Inngest is a platform for durable functions and workflows. You write functions with its SDK in TypeScript, Python or Go, serve them from your own application over HTTP, and an Inngest server (Inngest Cloud, or one you run) calls them, stores the result of each step and calls them again to go on. Qoqi Flow is a language and a runtime made for flows, in one Go binary that holds the instances itself. The difference that matters most: Inngest goes on by calling your handler again from the top and handing back the saved results of the steps already done, so the code outside steps runs again and must take the same path; Qoqi Flow freezes the state of execution and thaws it, so no history is played again and the code need not be deterministic.

## The same flow in both

An expense: tell the manager, wait up to 7 days for an answer, pay once.

In Inngest (TypeScript SDK v4), the answer is an event matched to the run by the expense id:

```ts
import { Inngest } from "inngest";

const inngest = new Inngest({ id: "expenses" });

export const expense = inngest.createFunction(
  { id: "expense-approval", triggers: { event: "expense/submitted" }, retries: 5 },
  async ({ event, step }) => {
    const { id, employee, cents } = event.data;

    await step.run("notify-manager", () => notifyManager(id, employee, cents));

    // No process waits: the run is held by the Inngest server
    const answer = await step.waitForEvent("wait-for-answer", {
      event: "expense/answered",
      timeout: "7d",
      if: "async.data.expenseId == event.data.id",
    });

    if (answer === null) return { status: "expired" };
    if (!answer.data.approved) return { status: "refused", by: answer.data.by };

    // A step may run again if it ran before its result was saved
    const payout = await step.run("pay", async () => {
      const res = await fetch("https://payouts.example.com/v1/payouts", {
        method: "POST",
        headers: { "Idempotency-Key": `expense-${id}` },
        body: JSON.stringify({ to: employee, cents }),
      });
      if (!res.ok) throw new Error(`payout failed: ${res.status}`);
      return res.text();
    });

    return { status: "paid", payout };
  },
);
```

A thrown error in `pay` retries that step (here up to 5 times); when the retries are spent, the error comes out of `step.run` and fails the run unless you catch it.

In Qoqi Flow, the answer arrives in an inbox of the instance:

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

        // Seven days without a process waiting: the instance is in the store
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
            case Ok(payout): {
                return Outcome.Paid(payout);
            }

            case Err(why): {
                return Outcome.Unpaid(why);
            }
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
        case Ok(response): {
            return Ok(response.body);
        }

        case Err(error): {
            if flow.attempt() < 5 {
                flow.retry(error.describe(), 1.minutes());
            }

            return Err(error.describe());
        }
    }
}
```

The declarations of `Answer`, `Outcome` and `notifyManager` are left out. In both, the payout provider is asked to dedupe: neither tool can know whether a request that was cut off reached the provider.

## Recovery after a crash

Inngest saves the result of each step when it finishes. To go on, it calls your handler again with the event and the saved results; each `step.run` that already has a result returns it without running its callback, and the handler stops at the first step that has none. In TypeScript v4, steps can also run back to back in one call (checkpointing), with a fall back to the call-per-step model when a step retries or steps run in parallel. A step that ran but whose result was not saved runs again, so the docs ask for idempotent side effects.

Qoqi Flow writes the state of execution, the calls in progress and their variables, to the store, and thaws it after a crash. A step's result is written before the flow goes on. A step that may have half run is run again under the same key, `flow.key()`. A step that returns `Settled<T>` is never run twice: the flow gets `Unknown` and decides. See [steps](../concepts/steps.md) and [failures](../concepts/failures.md).

## Determinism

In Inngest, the code outside steps runs again each time the handler is called. It must reach the same steps, with the same ids, in the same order: a `Math.random()` or a `Date.now()` that picks a branch outside a step can lead a replay down another path. Nothing enforces this; the docs ask you to put non-deterministic work inside `step.run`. Step results are matched by step id (hashed, with a counter for steps in loops), not by position alone.

Qoqi Flow does not replay, so flow code need not be deterministic. A flow may read the clock (`time.now()`), draw random numbers and decide on them; they are given back to the flow when it is taken up. See [flows](../concepts/flows.md).

## Reaching the world

In Inngest, a database call or a `fetch` outside `step.run` is ordinary TypeScript. It runs, and may run again on each replay. No tool reports it.

In Qoqi Flow, the compiler refuses I/O outside a `step fn`, with error F0001 and the call path that leads to it. Steps can also be carried out by your own application through a queue (`by payments`), with client code from `qoqi gen go|php|ts`; see [application steps](../guides/application-steps.md).

## Waiting

Inngest has `step.sleep` and `step.sleepUntil` (up to a year), `step.waitForEvent` with a timeout and a match on a field or a CEL expression (it returns `null` on timeout), `step.invoke` to call another function, and `cancelOn` to cancel a run when an event arrives. A waiting run uses no compute. It adds flow control around runs: concurrency limits, throttling, rate limits, debounce, priority, batching. How long a run may last depends on the plan: 30 days on the free plan, up to 366 days on higher ones.

Qoqi Flow waits with `wait match` over inboxes and timers, as above; the instance sits in the store meanwhile. A cancellation runs the undos noted with `flow.undo`, the last noted first. See [waiting](../concepts/waiting.md).

## New versions of the code

Inngest has no version pinning: a run in flight picks up the new code on its next call. Saved results are matched by step id. A new step runs when the code reaches it; a removed step's result is left unused; a step whose code changed but whose id did not keeps its saved result. Renaming a step id makes in-flight runs run it again, which can repeat a payment. For a change the saved state cannot carry, the docs say to deploy a new function with a new id and keep the old one until its runs finish.

Qoqi Flow pins an instance to the version that started it until it reaches a `stage`, where it moves to the new code. The shapes of the state are recorded in `flow.lock`, a `migrate fn` converts them, and the daemon refuses a deploy that is not compatible. See [versions](../concepts/versioning.md#pinned-until-a-stage).

## Testing

Inngest has `@inngest/test` (for SDK v4): an `InngestTestEngine` runs a function, or runs it up to one step, with mocked events and mocked steps, and turns each step tool into a spy. `step.sleep` and `step.waitForEvent` should always be mocked; a mocked wait returns an event or `null` for a timeout. The docs describe no separate clock. The local Dev Server (`npx inngest-cli@latest dev`) shows each run and its steps, and can replay runs.

Qoqi Flow tests are trials on their own clock: `trial.advance(7.days())` makes the week pass, `trial.replace` stands in for a step, and `everyCrash` runs a scenario again, stopping the process after each write, to check the outcome is the same. See [testing flows](../guides/testing-flows.md#every-crash).

## Running it

Inngest runs your functions where your application already runs: `serve()` adds an endpoint (adapters for many frameworks, including serverless platforms), or Connect opens an outbound WebSocket from long-running workers. The server is Inngest Cloud, or you self-host `inngest start`, one binary or Docker image: SQLite by default on one node, or external PostgreSQL and Redis to scale out. The server is under the SSPL 1.0, with each release also under Apache 2.0 three years after it is made available; the SDKs are under Apache 2.0.

Qoqi Flow is one Go binary: the compiler and the daemon that runs the instances. The store is SQLite, PostgreSQL or MySQL; several daemons can share a PostgreSQL or MySQL store with 30 second leases. It has a web console and an HTTP API. There is no managed hosting. See [running in production](../guides/production.md#several-daemons).

## Where Inngest is ahead

- Maturity: released in 2022, used in production by many companies, with a funded company behind it. Qoqi Flow is v0.1.0, from October 2026.
- Languages: flows are written in TypeScript, Python or Go, in the code base you already have. Qoqi Flow asks you to learn a new language for the flow itself.
- Managed hosting: Inngest Cloud, SOC 2 Type II. Qoqi Flow has none.
- Serverless: functions can run on serverless platforms, called over HTTP; no long-lived worker is needed.
- Flow control: concurrency keys, throttling, rate limits, debounce, priority and batching are built in.
- Observability and ecosystem: a dashboard with traces per run, replay of runs, a local Dev Server, middleware, and AI helpers.

## Where Qoqi Flow is different

- Recovery thaws a frozen state; it does not replay, so flow code need not be deterministic and there are no step ids to keep stable across deploys.
- I/O outside a step is a compile error, not a convention.
- Running instances stay on their version until a `stage`, and incompatible state changes are caught at deploy against `flow.lock`, instead of being matched by step id.
- `Settled` steps are never run twice; the flow is told `Unknown` instead.
- `everyCrash` tests crash recovery at every write, and trials have their own clock.
- One binary and your own SQL database, under an open-source license, with no separate queue or cache to run.

## Sources

- https://www.inngest.com/docs
- https://www.inngest.com/docs/learn/how-functions-are-executed
- https://www.inngest.com/docs/learn/versioning
- https://www.inngest.com/docs/learn/serving-inngest-functions
- https://www.inngest.com/docs/setup/connect
- https://www.inngest.com/docs/reference/typescript/functions/create
- https://www.inngest.com/docs/reference/typescript/functions/step-wait-for-event
- https://www.inngest.com/docs/features/inngest-functions/steps-workflows/wait-for-event
- https://www.inngest.com/docs/features/inngest-functions/cancellation/cancel-on-events
- https://www.inngest.com/docs/guides/flow-control
- https://www.inngest.com/docs/usage-limits/inngest
- https://www.inngest.com/docs/reference/testing
- https://www.inngest.com/docs/local-development
- https://www.inngest.com/docs/self-hosting
- https://www.inngest.com/about
- https://github.com/inngest/inngest/blob/main/LICENSE.md
