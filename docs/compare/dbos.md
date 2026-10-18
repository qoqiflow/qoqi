# Qoqi Flow and DBOS

DBOS Transact is an open-source library for durable workflows, in Python, TypeScript, Go and Java. You add it to your application, decorate functions as workflows and steps, and it checkpoints each step's result in a database, Postgres in production. There is no server of its own to run. Qoqi Flow is a language and a daemon: flows are written in `.qi`, and the daemon keeps them. The difference that matters most is recovery. DBOS calls an interrupted workflow again from the top with its saved inputs, and each step already done returns its saved result, so the workflow code must be deterministic. Qoqi Flow freezes the instance's state of execution and thaws it, so flow code need not be.

## The same flow in both

An expense: tell the manager, wait up to seven days for an answer, pay once.

With DBOS, in Python:

```python
import os
import requests
from dbos import DBOS, DBOSConfig, SetWorkflowID

config: DBOSConfig = {
    "name": "expenses",
    "system_database_url": os.environ.get("DBOS_SYSTEM_DATABASE_URL"),
}
DBOS(config=config)

@DBOS.step()
def notify_manager(expense_id: str, employee: str, cents: int) -> None:
    print(f"[mail] expense {expense_id} of {employee}, {cents} cents, waits for your word")

# Run at least once: after a crash it runs again, so the provider gets a key
@DBOS.step(retries_allowed=True, max_attempts=5, interval_seconds=60, backoff_rate=1.0)
def pay(employee: str, cents: int) -> str:
    response = requests.post(
        "https://payouts.example.com/v1/payouts",
        headers={"Idempotency-Key": f"{DBOS.workflow_id}-pay"},
        json={"to": employee, "cents": cents},
        timeout=30,
    )
    response.raise_for_status()
    return response.text

@DBOS.workflow()
def expense(expense_id: str, employee: str, cents: int) -> str:
    notify_manager(expense_id, employee, cents)

    # The deadline is kept in the database; the process may stop meanwhile
    answer = DBOS.recv("answers", timeout_seconds=7 * 24 * 3600)
    if answer is None:
        return "expired"
    if not answer["approved"]:
        return f"refused by {answer['by']}"

    try:
        return f"paid {pay(employee, cents)}"
    except Exception as why:
        return f"unpaid: {why}"

if __name__ == "__main__":
    DBOS.launch()
    with SetWorkflowID("expense-1"):
        DBOS.start_workflow(expense, "1", "lin", 4200)
```

The manager's answer, from any process connected to the same database: `DBOS.send("expense-1", {"approved": True, "by": "grace"}, topic="answers")`.

In Qoqi Flow:

```qoqi
import std.flow;
import std.flow.{Flow, Inbox};
import std.http;
import std.http.{Method};
import std.time;

pub struct Answer {
    approved: Bool;
    by: String;
}

pub enum Outcome {
    Paid(String),
    Refused(String),
    Expired,
    Unpaid(String),
}

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

pub step fn notifyManager(id: String, employee: String, cents: Int) {
    println("[mail] expense ${id} of ${employee}, ${cents} cents, waits for your word");
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

Started with `qoqi start Expense expense-1 '{"id": "1", "employee": "lin", "cents": 4200}'`, answered with `qoqi send expense-1 answers '{"approved": true, "by": "grace"}'`.

The two are close. Both keep the seven days in the database, both give `pay` a key that is the same on each try. In DBOS the key is built from the workflow ID; in Qoqi it is [`flow.key()`](../concepts/steps.md#keys).

## Recovery after a crash

DBOS writes a workflow's inputs and each step's output to its system database. After a crash it calls each interrupted workflow again with its saved inputs; before each step it looks for a checkpoint and, if there is one, returns the saved output instead of running the step. A step that was running when the process stopped runs again: steps are run at least once, and never again once their result is saved. Steps that write to your own database can be transactions through a DBOS datasource, committed with their checkpoint, run exactly once.

Qoqi writes the state of execution itself: the calls in progress and their variables. Taking an instance up thaws that state; nothing before it is run again. A step that may have half run is run again under the same key, as in DBOS. A step returning `Settled<T>` is never run twice: the flow gets `Unknown` and must say what that means. DBOS has no such kind of step. See [after a crash](../concepts/steps.md#after-a-crash).

## Determinism

DBOS asks that a workflow, given the same step results, call the same steps with the same inputs in the same order. Reading the clock, a random number, a call to a service: these go in steps. When a recovered workflow does something else at a step, DBOS raises an error at run time (`DBOSStepNondeterminismError`).

Qoqi does not replay, so it has no such rule. A flow can read a value, loop on it, branch on it: the values are in the frozen state, not computed again.

## Reaching the world

In DBOS, nothing stops a workflow function from calling `requests.post` directly. It is a convention, and a mistake shows only when a workflow recovers and the call is made a second time, or not at all.

In Qoqi, a flow reaches the world only through a `step fn`. The compiler checks `run` and every function it can reach, and refuses the program otherwise (`F0001`, with the call path). See [the one rule](../concepts/flows.md#the-one-rule).

## Waiting

DBOS has `DBOS.sleep`, which records its wake-up time in the database, and `DBOS.recv(topic, timeout_seconds)`, which waits for a message sent with `DBOS.send` and returns `None` at the timeout. A workflow can also publish values with `DBOS.set_event` for others to read with `DBOS.get_event`. Durable queues and schedules start workflows.

Qoqi has `wait` on a timer, an inbox, or another flow, and `wait match` for the first of several. Messages come through `qoqi send` or the HTTP API, with a key that makes a message sent twice one message; DBOS's `send` takes an idempotency key too. See [waiting](../concepts/waiting.md).

## New versions of the code

Each DBOS workflow is tagged with the application version it started on: a hash of the workflow source by default, or `application_version` in the configuration. DBOS recovers only the workflows whose version matches the running code, so old workflows are left to processes still running the old version, which drain. To move running workflows to new code, DBOS offers patching (`if DBOS.patch("name"):` around a change, later `DBOS.deprecate_patch`), or `DBOS.fork_workflow` from a chosen step onto a fixed version.

A Qoqi instance is pinned to its version too, until it reaches a `stage`. There it moves to the newer version, if the variables alive there still fit. Their shapes are recorded in `flow.lock`; a change that does not fit needs a `migrate fn`, and the daemon refuses the deploy otherwise. Old and new code do not need separate processes. See [versions](../concepts/versioning.md).

## Testing

DBOS workflows are Python functions, tested with pytest. The docs show a fixture that resets DBOS before each test (`DBOS.destroy`, `DBOS.reset_system_database`, `DBOS.launch`), SQLite as the test database, and `unittest.mock` to replace steps or `DBOS.recv`. They give no clock to skip time and no tool to test recovery.

A Qoqi trial runs an instance in memory on its own clock: `trial.advance(7.days())` takes no time, `trial.replace` swaps a step, and `everyCrash` runs a scenario again stopping the process after each write, checking that it ends the same and that no `Settled` step ran twice. See [testing flows](../guides/testing-flows.md).

## Running it

DBOS runs inside your application: `pip install dbos`, a configuration, `DBOS.launch()`. Its only infrastructure is the system database: Postgres, recommended for production, or SQLite, the default when none is given. Several processes on one Postgres share the work. DBOS Conductor, a proprietary control plane run by DBOS or self-hosted under a license, sits outside the execution path: it recovers the workflows of a process that died onto another, shows dashboards, and pauses, resumes and forks workflows. DBOS Cloud hosts applications.

Qoqi is one Go binary, `qoqi serve`, on SQLite, PostgreSQL or MySQL. Several daemons share a PostgreSQL or MySQL store with leases of 30 seconds; when one dies, another takes its instances. Your application talks to it through the HTTP API, and carries out steps declared `by` a queue, with types from `qoqi gen go|php|ts`. There is a web console. There is no managed hosting. See [running in production](../guides/production.md).

## Where DBOS is ahead

*   **Maturity.** DBOS has been in development since 2023, from research at MIT and Stanford, and its SDKs are past 1.0. Qoqi Flow is v0.1.0, from October 2026, with no years in production.
*   **Languages.** Workflows are written in Python, TypeScript, Go or Java, in the code you already have, with its libraries. Qoqi flows are written in Qoqi; your application reaches them over HTTP or carries out steps through queues.
*   **No server.** DBOS is a library and a database you likely run already. Qoqi needs its daemon.
*   **Operations.** Conductor gives recovery across machines, dashboards, Prometheus metrics, workflow management and autoscaling by version; DBOS Cloud hosts the whole application; OpenTelemetry tracing can be turned on. Qoqi has a console and an HTTP API, and no hosting.
*   **Ecosystem.** Documentation, examples, integrations and a community that Qoqi does not have yet.

## Where Qoqi Flow is different

*   **No replay.** The state of execution is frozen and thawed, so flow code need not be deterministic, and no error can come from a workflow that changed its mind.
*   **I/O refused at compile time.** A call to the world outside a step is a compile error, not a surprise at recovery.
*   **Steps that never run twice.** A `Settled` step gives `Unknown` after a crash instead of running again.
*   **Moving running instances.** Instances move to new code at a `stage`, checked against `flow.lock`, with a `migrate fn` when the shape changed, and a deploy that does not fit is refused.
*   **Crashes in tests.** `everyCrash` and a clock of the trial's own.

## Sources

*   https://docs.dbos.dev/
*   https://docs.dbos.dev/architecture
*   https://docs.dbos.dev/python/tutorials/workflow-tutorial
*   https://docs.dbos.dev/python/tutorials/step-tutorial
*   https://docs.dbos.dev/python/tutorials/transaction-tutorial
*   https://docs.dbos.dev/python/tutorials/workflow-communication
*   https://docs.dbos.dev/python/tutorials/upgrading-workflows
*   https://docs.dbos.dev/python/tutorials/workflow-management
*   https://docs.dbos.dev/python/tutorials/testing
*   https://docs.dbos.dev/python/reference/contexts
*   https://docs.dbos.dev/python/reference/configuration
*   https://docs.dbos.dev/conductor/overview
*   https://docs.dbos.dev/production/hosting-conductor
*   https://www.dbos.dev/about
*   https://github.com/dbos-inc/dbos-transact-py
