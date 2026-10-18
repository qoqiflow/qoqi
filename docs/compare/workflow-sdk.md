# Qoqi Flow and Workflow SDK

Workflow SDK (first called Workflow Development Kit) is Vercel's open-source library for durable functions in TypeScript, with a Python SDK in beta. You mark a function `"use workflow"` and the functions it calls `"use step"`; a compiler plugin turns them into routes of the application you already have, in Next.js, Nuxt, SvelteKit, Express, Hono and other frameworks. Where the state is kept and how work is queued is decided by a **World**: the Local World on disk for development, the Vercel World on Vercel, the Postgres World or a World of your own elsewhere. Qoqi Flow is a language and a runtime made for flows, in one Go binary that holds the instances itself. The difference that matters most: Workflow SDK goes on by running the workflow function again from the start against its event log, so that function must be deterministic and runs in a sandbox that keeps it so; Qoqi Flow freezes the state of execution and thaws it, so no history is played again and the code need not be deterministic.

## The same flow in both

An expense: tell the manager, wait up to 7 days for an answer, pay once.

In Workflow SDK (v5), the answer reaches the run through a hook whose token is the expense id:

```ts
import { createHook, sleep, getStepMetadata, RetryableError } from "workflow";

type Answer = { approved: boolean; by: string };

export async function expense(id: string, employee: string, cents: number) {
  "use workflow";

  using answers = createHook<Answer>({ token: `expense:${id}` });
  await notifyManager(id, employee, cents);

  // No process waits: the run is in the World's event log
  const answer = await Promise.race([
    answers,
    sleep("7d").then(() => "expired" as const),
  ]);

  if (answer === "expired") return { status: "expired" };
  if (!answer.approved) return { status: "refused", by: answer.by };

  try {
    return { status: "paid", payout: await pay(employee, cents) };
  } catch (error) {
    return { status: "unpaid", why: String(error) };
  }
}

async function notifyManager(id: string, employee: string, cents: number) {
  "use step";
  console.log(`[mail] expense ${id} of ${employee}, ${cents} cents, waits for your word`);
}

// Run again after a crash or a failure under the same stepId: the provider makes one payout
async function pay(employee: string, cents: number): Promise<string> {
  "use step";
  const res = await fetch("https://payouts.example.com/v1/payouts", {
    method: "POST",
    headers: { "Idempotency-Key": getStepMetadata().stepId },
    body: JSON.stringify({ to: employee, cents }),
  });
  if (!res.ok) throw new RetryableError(`payout failed: ${res.status}`, { retryAfter: "1m" });
  return res.text();
}
pay.maxRetries = 4; // 5 attempts in all
```

A route of the application starts it with `start(expense, [id, employee, cents])` from `workflow/api`, and the manager's answer is sent with `resumeHook("expense:" + id, { approved: true, by: "grace" })`. The token is not a password: the route that calls `resumeHook` must check who is calling.

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

The declarations of `Answer`, `Outcome` and `notifyManager` are left out. The answer is sent with `qoqi send expense:1 answers '{...}'` or the HTTP API. In both, the payout provider is asked to dedupe: neither tool can know whether a request that was cut off reached the provider.

## Recovery after a crash

Workflow SDK is event sourced. Every change, a run started, a step started or completed, a hook received, a sleep ended, is an event in the World's log, and the state of runs and steps is derived from it. To go on, the runtime runs the workflow function again from the top; each step call that already has a result in the log returns it without running, and the function suspends at the first one that has none. If a step's invocation dies before it reports (a timeout, running out of memory), the step is retried under its retry policy, with the same `stepId`. The default is 3 retries; `FatalError` stops them. A replay that cannot follow the log is retried, and the run fails with `CORRUPTED_EVENT_LOG` if it keeps diverging. Runs have a ceiling of events (25,000 by default on the Local and Postgres Worlds); an experimental QuickJS engine can resume a long encrypted run from a snapshot of the VM's memory instead.

Qoqi Flow writes the state of execution, the calls in progress and their variables, to the store, and thaws it after a crash. A step's result is written before the flow goes on. A step that may have half run is run again under the same key, `flow.key()`. A step that returns `Settled<T>` is never run twice: the flow gets `Unknown` and decides. See [steps](../concepts/steps.md#after-a-crash) and [failures](../concepts/failures.md).

## Determinism

In Workflow SDK, the workflow function must be deterministic, and the SDK makes much of that hold for you. Workflow code is bundled apart and run in a Node.js VM: `Math.random` and `crypto.randomUUID` are seeded per run, `Date` follows a clock kept in the log, `setTimeout` is not there (use `sleep`), `process.env` is frozen. What remains yours is the order of steps: code that takes another path on a replay makes it diverge.

Qoqi Flow does not replay, so flow code need not be deterministic. A flow may read the clock (`time.now()`), draw random numbers and decide on them; they are given back to the flow when it is taken up. See [flows](../concepts/flows.md).

## Reaching the world

In Workflow SDK, the sandbox is the guard. A workflow function that imports a Node.js module such as `fs` or `http`, or a package that does, fails the build or the loading of the bundle, with the import chain. The global `fetch` is not defined there: a call to it, or a library calling it, throws when it runs; `fetch` from `workflow` is a step. Step functions run as ordinary Node.js, with every npm package.

In Qoqi Flow, the compiler refuses I/O outside a `step fn`, with error F0001 and the call path that leads to it, before anything runs. Steps can also be carried out by your own application through a queue (`by payments`), with client code from `qoqi gen go|php|ts`; see [application steps](../guides/application-steps.md).

## Waiting

Workflow SDK has `sleep("7d")` or `sleep(date)`, hooks (`createHook`, `defineHook` with a schema, resumed by `resumeHook`) and webhooks (`createWebhook`, a URL that resumes the run when it is called). A hook can receive several payloads. A deadline is a `Promise.race` of a hook against a `sleep`, as above. A waiting run uses no compute. Runs can stream output to a client with `getWritable()`, and can be cancelled, with `AbortSignal` passed into steps.

Qoqi Flow waits with `wait match` over inboxes and timers, as above; the instance sits in the store meanwhile. A message sent twice with the same key is one message. A cancellation runs the undos noted with `flow.undo`, the last noted first. See [waiting](../concepts/waiting.md).

## New versions of the code

Workflow SDK pins each run to the deployment that started it: a run in flight keeps resuming on its old code, and new runs start on the new one. That deployment must stay available for the whole run, or the run cannot go on and must be run again. There is no patching API. To move work to new code you cancel runs and start them again on the latest deployment, or write the workflow as a chain of runs, each ending by starting the next with `start(..., { deploymentId: "latest" })` and passing its state on. `deploymentId: "latest"` is a Vercel World feature for now; on other Worlds, how a run reaches its own code depends on how you deploy.

Qoqi Flow also pins an instance to the version that started it, but only until it reaches a `stage`, where it moves to the new code from the same place. The shapes of the state are recorded in `flow.lock`, a `migrate fn` converts them, and the daemon refuses a deploy that is not compatible. The daemon keeps every version's bytecode in the store while an instance runs it. See [versions](../concepts/versioning.md#pinned-until-a-stage).

## Testing

Without the compiler, `"use step"` and `"use workflow"` do nothing, so steps are unit tested as plain functions. For the rest, the `@workflow/vitest` plugin builds the bundles and runs workflows in the test process on a fresh Local World. A test starts a run, waits for it to reach a hook (`waitForHook`) or a sleep (`waitForSleep`), resumes the hook, and wakes a sleep at once with `getRun(id).wakeUp()`. `vi.mock()` cannot reach workflow code, and reaches step code only in some setups. The docs describe no crash testing.

Qoqi Flow tests are trials on their own clock: `trial.advance(7.days())` makes the week pass, `trial.replace` stands in for a step, and `everyCrash` runs a scenario again, stopping the process after each write, to check the outcome is the same. See [testing flows](../guides/testing-flows.md#every-crash).

## Running it

Workflow SDK runs inside your application; the World says what is underneath. On Vercel, the Vercel World is chosen on its own: the platform provides storage, queues, encryption and a dashboard, under the product name Vercel Workflows, generally available since April 2026. Elsewhere, `WORKFLOW_TARGET_WORLD` picks a World: `@workflow/world-postgres` keeps the log in PostgreSQL and its queue in graphile-worker, run by your server process; its docs call it a reference implementation, with no authentication on the workflow routes, to adapt before production. Community Worlds exist for Redis, MongoDB, Turso and others. A CLI (`workflow inspect`, `cancel`, `web`) and a local web UI show runs, with OpenTelemetry tracing. The SDK is under Apache 2.0.

Qoqi Flow is one Go binary: the compiler and the daemon that runs the instances. The store is SQLite, PostgreSQL or MySQL; several daemons can share a PostgreSQL or MySQL store with 30 second leases. It has a web console and an HTTP API. There is no managed hosting. See [running in production](../guides/production.md#several-daemons).

## Where Workflow SDK is ahead

- Maturity and adoption: a public release in October 2025, now at 5.x, from a large company; Vercel reports over 100 million runs across more than 1,500 customers. Qoqi Flow is v0.1.0, from October 2026, with no years in production.
- Language: flows are TypeScript in the code base you already have, with types across the steps; a Python SDK is in beta. Qoqi Flow asks you to learn a new language for the flow itself.
- Managed hosting: on Vercel there is nothing to run, with scaling, queues and regions handled by the platform. Qoqi Flow has none.
- Frameworks and ecosystem: integrations for more than ten web frameworks, durable AI agents with the AI SDK, resumable streams to the browser.
- Observability: a trace viewer per run, the Vercel dashboard, OpenTelemetry, encryption of the event log at rest.
- Pluggable storage: a World can be written for the infrastructure you have.

## Where Qoqi Flow is different

- Recovery thaws a frozen state; it does not replay, so flow code need not be deterministic and has no event ceiling.
- I/O outside a step is a compile error with its call path, not a sandbox that fails at build, load or run.
- Running instances move to new code at a `stage`, checked against `flow.lock` with a `migrate fn`, instead of staying on their deployment to the end or being started again.
- `Settled` steps are never run twice; the flow is told `Unknown` instead.
- `everyCrash` tests crash recovery at every write, and trials have their own clock.
- Self-hosting is the main way to run it: one binary and your own SQL database, with several daemons on one store.

## Sources

- https://workflow-sdk.dev/docs
- https://workflow-sdk.dev/llms.txt
- https://workflow-sdk.dev/docs/foundations/workflows-and-steps
- https://workflow-sdk.dev/docs/how-it-works/code-transform
- https://workflow-sdk.dev/docs/how-it-works/event-sourcing
- https://workflow-sdk.dev/docs/api-reference/workflow-globals
- https://workflow-sdk.dev/docs/foundations/errors-and-retries
- https://workflow-sdk.dev/docs/foundations/idempotency
- https://workflow-sdk.dev/docs/foundations/hooks
- https://workflow-sdk.dev/docs/api-reference/workflow/sleep
- https://workflow-sdk.dev/docs/foundations/versioning
- https://workflow-sdk.dev/docs/errors/deployment-mismatch
- https://workflow-sdk.dev/docs/errors/replay-divergence
- https://workflow-sdk.dev/docs/errors/step-executed-multiple-times
- https://workflow-sdk.dev/docs/errors/node-js-module-in-workflow
- https://workflow-sdk.dev/docs/errors/fetch-in-workflow
- https://workflow-sdk.dev/docs/errors/max-events-exceeded
- https://workflow-sdk.dev/docs/testing
- https://workflow-sdk.dev/docs/deploying
- https://workflow-sdk.dev/docs/configuration/worlds
- https://workflow-sdk.dev/worlds/postgres
- https://workflow-sdk.dev/docs/whats-new
- https://workflow-sdk.dev/docs/getting-started/python
- https://github.com/vercel/workflow
- https://vercel.com/blog/introducing-workflow
- https://vercel.com/blog/a-new-programming-model-for-durable-execution
