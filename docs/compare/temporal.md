# Qoqi Flow and Temporal

Temporal is a durable execution platform: a server, the Temporal Service, and SDKs to write workflows in Go, Java, Python, TypeScript, .NET, Ruby, PHP and Rust. You write workflows and activities as ordinary code in your language and run them in Workers, your own processes, which poll the service for work. The service keeps an Event History of each workflow execution. Qoqi Flow is a language and a runtime made for flows: a flow is written in `.qi`, and one daemon runs it and keeps its state. The difference that matters most: a Temporal workflow recovers by running its code again against its Event History, so the workflow code must be deterministic; a Qoqi instance is frozen and thawed, so there is nothing to play again.

## The same flow in both

An expense: tell the manager, wait up to 7 days for an answer, pay once.

In Temporal, with the Go SDK. The answer comes as a Signal named `answers`; a timer and the signal channel race in a `Selector`:

```go
func ExpenseWorkflow(ctx workflow.Context, exp Expense) (string, error) {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{InitialInterval: time.Minute, MaximumAttempts: 5},
	})

	if err := workflow.ExecuteActivity(ctx, NotifyManager, exp).Get(ctx, nil); err != nil {
		return "", err
	}

	var answer Answer
	answered := false
	timerCtx, cancelTimer := workflow.WithCancel(ctx)
	sel := workflow.NewSelector(ctx)
	sel.AddReceive(workflow.GetSignalChannel(ctx, "answers"), func(c workflow.ReceiveChannel, more bool) {
		c.Receive(ctx, &answer)
		answered = true
		cancelTimer()
	})
	sel.AddFuture(workflow.NewTimer(timerCtx, 7*24*time.Hour), func(f workflow.Future) {})
	sel.Select(ctx)

	if !answered {
		return "expired", nil
	}

	if !answer.Approved {
		return "refused by " + answer.By, nil
	}

	var payout string
	if err := workflow.ExecuteActivity(ctx, Pay, exp).Get(ctx, &payout); err != nil {
		return "unpaid: " + err.Error(), nil
	}

	return "paid " + payout, nil
}

// May run more than once: the provider makes one payout per key
func Pay(ctx context.Context, exp Expense) (string, error) {
	info := activity.GetInfo(ctx)
	key := info.WorkflowExecution.RunID + "-" + info.ActivityID

	return payouts.Send(ctx, exp.Employee, exp.Cents, key)
}
```

`NotifyManager` is an activity too. Both are registered on a Worker (`worker.New`, `RegisterWorkflow`, `RegisterActivity`), and the manager's answer is sent with `client.SignalWorkflow`. The Temporal docs suggest the Workflow Run ID and the Activity ID as an idempotency key: the same across retries, unique among executions.

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

The Temporal Service writes every event of a workflow execution to its Event History: an activity scheduled and completed, a timer started and fired, a signal received. When a Worker takes the workflow up again, on the same machine or another, the SDK runs the workflow function from the start and checks each command it makes against the history; a command already in the history gets its recorded result instead of running. An activity is retried by its retry policy (by default: from 1 second, doubling up to 100 seconds, with no limit on attempts) and may run more than once, so `Pay` needs an idempotency key, as it does in Qoqi.

A Qoqi instance is not run from the start. At each wait and before each step, the VM's state (the calls in progress and their variables) is written to the store; after a crash the instance is thawed from that state and goes on. A step that may have half run is run again under the same key `flow.key()`, or, if it returns `Settled<T>`, is not run again and the flow gets `Unknown` (see [steps](../concepts/steps.md#after-a-crash)).

The history of a Temporal execution has a limit: 51,200 events or 50 MB. A long-lived workflow uses Continue-As-New to start over with a fresh history. A Qoqi instance keeps its state, not its history.

## Determinism

Because a Temporal workflow is played again, it must make the same Workflow API calls in the same order for the same input; a mismatch with the history is a non-deterministic error. In Go this means `workflow.Now` in place of `time.Now`, `workflow.Sleep` in place of `time.Sleep`, `workflow.Go`, `workflow.Channel` and `workflow.Selector` in place of `go`, `chan` and `select`, no `range` over a map without sorting its keys, and `workflow.SideEffect` for a random value. The Go SDK has a static checker, `workflowcheck`, that finds some of this; other SDKs have their own help, such as the Python SDK's sandbox.

Qoqi flow code need not be deterministic. Nothing is played again, so a flow may read the clock, draw a random number or branch on anything; the clock and random numbers are given back to the flow when it is taken up.

## Reaching the world

In Temporal, a call to the outside belongs in an activity. Nothing in the Go compiler stops a workflow function from calling `http.Get` or a database directly: `workflowcheck` may report it, and otherwise the call runs again on every replay, unrecorded.

In Qoqi, the compiler follows every call from `run` and refuses one that reaches the network, a file or a database outside a `step fn`, with error `F0001` and the call path that leads there (see [the rule](../concepts/steps.md#the-rule-checked-by-the-compiler)).

## Waiting

Temporal has durable timers (`workflow.Sleep`, `workflow.NewTimer`) of any length, and three ways for the world to talk to a running workflow: Signals (a message, buffered on a channel), Queries (read its state) and Updates (a message with an answer, and a validator that can refuse it). A `Selector` waits for the first of several. While a workflow waits, no Worker is busy with it.

In Qoqi, a flow waits for a timer, a message on an inbox, a child flow, or the first of several with `wait match` (see [waiting](../concepts/waiting.md)). While it waits, the instance is only rows in the store. Qoqi has no counterpart to Queries or Updates in a flow; the console and `qoqi show` show an instance's variables.

## New versions of the code

Temporal gives two ways to change code under running workflows. Patching marks a change in the code: in Go, `workflow.GetVersion(ctx, changeID, workflow.DefaultVersion, 1)` returns the old version for executions that started before the change and the new one for the others, and the old branch stays until those executions are gone. Worker Versioning, generally available since March 2026, groups Workers into deployment versions by Build ID: a Pinned workflow ends on the version it started on, with no patching, and old Workers are kept until it drains; an Auto-Upgrade workflow moves to the current version and must stay replay-safe, with patches. Replay tests in CI check new code against recorded histories.

A Qoqi instance is pinned to its version too, until it reaches a `stage`: there it moves to the newest version, if the variables alive at that stage still have the shape recorded in `flow.lock`. When they do not, a `migrate fn` says how, and the daemon refuses a deploy that would leave an instance alive with no way to move. Old versions need no Workers kept up: their bytecode is in the store. A step's body always runs in the newest version. See [versions](../concepts/versioning.md).

## Testing

The Go SDK's test suite (`testsuite.WorkflowTestSuite`, `NewTestWorkflowEnvironment`) runs a workflow in the test process. Timers are skipped when nothing else runs, `RegisterDelayedCallback` sends a signal at a given moment of the test's clock, and `env.OnActivity(...)` mocks an activity. `worker.NewWorkflowReplayer` replays recorded histories against new code, to catch non-determinism before a deploy. The other SDKs have the same pieces, such as `TestWorkflowEnvironment.createTimeSkipping()` in TypeScript. A Worker that crashes mid-workflow is not one of them: crash scenarios are left to you.

A Qoqi trial runs in the test process, on a store in memory and a clock of its own: `trial.advance(7.days())` takes no time. `trial.replace` stands in for a step, and `everyCrash` runs a scenario again with the process stopped after each write the instance made (see [testing flows](../guides/testing-flows.md#every-crash)).

## Running it

Temporal is a service to run, plus your Workers. Self-hosted, the service keeps its data in Cassandra, PostgreSQL or MySQL (SQLite for development only), and advanced visibility (searching executions) uses Elasticsearch, OpenSearch or SQL. `temporal server start-dev` starts one for development, with its web UI. Temporal Cloud is the managed service, on AWS and GCP, billed per Action; your Workers stay in your own environment. There is a web UI, a CLI, metrics, and Nexus for calls between namespaces.

Qoqi Flow is one Go binary, the daemon, on a store of SQLite, PostgreSQL or MySQL; flows run inside it, and only [application steps](../guides/application-steps.md) run in your processes. Several daemons can share one PostgreSQL or MySQL store, each instance held by one daemon under a 30-second lease. There is a web console and an HTTP API, and no managed hosting. See [running in production](../guides/production.md).

## Where Temporal is ahead

*   **Maturity.** Temporal began as a fork of Uber's Cadence, and its site names many companies that run it, among them Snap, Cloudflare, DoorDash, Retool and Duolingo. The server is at v1.32 and the Go SDK at v1.50. Qoqi Flow is at v0.1.0, from October 2026, with no years in production.
*   **Languages.** Workflows are written in Go, Java, Python, TypeScript, .NET, Ruby, PHP or Rust, beside the code you already have. In Qoqi, flows are written in `.qi`; Go, PHP and TypeScript come in only as application steps behind a queue.
*   **Managed hosting.** Temporal Cloud exists, across many AWS and GCP regions, with multi-region replication. Qoqi has none.
*   **Scale.** The service is a cluster that grows, on Cassandra if need be, with task queues spread over many Workers.
*   **Observability and ecosystem.** Search over executions, metrics, a mature web UI, Queries and Updates, Schedules, Nexus, samples and a large community.

## Where Qoqi Flow is different

*   **No replay, no determinism rules.** The state of an instance is frozen and thawed; flow code may read the clock or branch on anything, and there is no history limit to manage with Continue-As-New.
*   **I/O outside a step is a compile error**, not a mistake found by a linter, on replay, or not at all.
*   **New code at a `stage`.** An instance moves to new code at a named place, checked against `flow.lock`, with `migrate fn` for a change of shape, instead of patch branches or Workers kept up for old versions.
*   **Steps that must never run twice** return `Settled<T>`, and the flow must say what `Unknown` means.
*   **Crash tests.** `everyCrash` stops the process after each write and checks that every run ends the same.
*   **One binary.** No separate service, Workers and visibility store to run: the daemon and a SQL database.

Both are open source: the Temporal server and most SDKs are MIT, its Java SDK is Apache 2.0.

## Sources

*   https://github.com/temporalio/temporal
*   https://github.com/temporalio/sdk-go
*   https://github.com/temporalio/sdk-java
*   https://docs.temporal.io/encyclopedia/temporal-sdks
*   https://docs.temporal.io/workflow-definition
*   https://docs.temporal.io/activity-definition
*   https://docs.temporal.io/encyclopedia/retry-policies
*   https://docs.temporal.io/workflow-execution/event
*   https://docs.temporal.io/workflow-execution/limits
*   https://docs.temporal.io/develop/go/workflows/basics
*   https://docs.temporal.io/develop/go/message-passing
*   https://docs.temporal.io/develop/go/versioning
*   https://docs.temporal.io/worker-versioning
*   https://temporal.io/blog/ga-worker-versioning-public-preview-upgrade-on-continue-as-new
*   https://docs.temporal.io/develop/go/testing-suite
*   https://docs.temporal.io/develop/typescript/testing-suite
*   https://docs.temporal.io/temporal-service/persistence
*   https://docs.temporal.io/self-hosted-guide/visibility
*   https://docs.temporal.io/cli/server
*   https://docs.temporal.io/cloud/overview
*   https://docs.temporal.io/cloud/pricing
*   https://temporal.io/in-use
