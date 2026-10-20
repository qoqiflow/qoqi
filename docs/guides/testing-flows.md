# Testing flows

A test of a flow cannot wait two days, call a real bank, or hope a process dies at the right moment. `flow.test` runs an instance inside the test, on a store in memory and a clock of its own. Nothing is started: no daemon, no file.

```qoqi
import std.flow;
import std.test;
import std.time;
import approvals;
import approvals.{Expense, Answer};

fn testNoAnswerGoesUp() {
    let asked: [String] = [];
    let trial = flow.test(Expense { id: "2", author: "lin" });

    trial.replace(approvals.ask, fn(approver: String, id: String) {
        asked.push(approver);
    });

    trial.go();
    test.equal(asked, ["grace"]);

    trial.advance(2.days());
    test.equal(asked, ["grace", "ada"]);

    trial.send("answers", Answer { approved: true, why: "" });
    test.equal("${trial.result()}", "Approved(\"ada\")");
}
```

A test file is called `name_test.qi`, and its tests are the functions `testSomething()`:

```console
$ qoqi test
approvals_test.qi
  ok    no answer goes up
1 passed
```

## Driving a trial

| `trial.` | What it does |
|----------|--------------|
| `go()` | Runs the instance until it waits for the clock, or ends |
| `advance(length)` | Lets that much time pass: every timer due within it fires, at its moment, those set on waking too: `trial.advance(2.days())` |
| `send(inbox, message)` | Sends a message on one of the instance's inboxes, as the world would, then runs what it wakes |
| `cancel(reason)` | Cancels the instance, as a person would: it ends `failed`, once its undos have run |
| `replace(step, function)` | In this trial, calls of the step go to the function, which takes what the step takes and returns what it returns. A step not replaced runs as it is |
| `calls(step)` | How many times the step has run, attempts again included |
| `result()` | What `run` returned, or `null` while it has not |
| `status()` | `"waiting"`, `"running"`, `"done"`, `"failed"` or `"stopped"` |
| `stopped()` | Why it stopped, if it did |

`go`, `advance`, `send`, `cancel` and `replace` return the trial, so they chain: `flow.test(order).replace(charge, fake).go()`.

### How the clock moves

`advance(length)` lets `length` pass as it would in production. The clock moves to the earliest timer due within it, the instance runs until it waits again, and so on, timer after timer, until none is due before the end; then the clock is set at the end. A timer the flow sets on waking fires in the same `advance` when it falls within it, and `time.now()` in the flow, at each wake, is the moment that timer was due at.

So a flow that retries every 2 minutes three times, then waits 10 minutes, then 30, then escalates, has escalated after one `trial.advance(1.hours())`, minute 46, just as after six `trial.advance(10.minutes())`. `trial.advance(45.minutes())` leaves it waiting for the last timer.

An instance that keeps setting timers due at once (`wait time.after(1.milliseconds())` in a loop) would never let the span end: after 100 000 wakes in one `advance` the test stops, saying so.

## Every crash

A crash is where a flow's mistakes hide. `everyCrash` runs a scenario once as it is, then once more for **each write** the instance made, with the process stopped right after that write and the instance taken up again. Every run must end the same, and no step that is never run twice may have run twice. The test fails, saying where, if not.

```qoqi
fn testAnAnswerSurvivesEveryCrash() {
    flow.test(Expense { id: "3", author: "lin" }).everyCrash(fn(trial: flow.Trial<Decision>) {
        trial.go();
        trial.advance(2.days());
        trial.send("answers", Answer { approved: true, why: "" });

        test.equal("${trial.result()}", "Approved(\"ada\")");
    });
}
```

A flow that passes is safe against a stop at any point of that scenario, not only the one a person thought of.

## Steps that reach the world

Replace the steps that would reach a real service, a bank, a mail server: the trial then never leaves the test. `trial.calls(step)` says how many times one was called, which is how a test checks that a step was not run twice.

## Running

```console
$ qoqi test                         # every *_test.qi under the current folder
$ qoqi test flows/                  # a folder
$ qoqi test approvals_test.qi       # a file
$ qoqi test --filter=answer         # the tests whose name has the words
```

The process ends with status 1 if a test failed. In the editor, the VS Code extension runs the tests of a file or of the workspace.
