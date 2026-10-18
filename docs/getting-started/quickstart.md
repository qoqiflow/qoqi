# Quickstart

In five minutes: a flow that asks a manager to approve an expense, waits for the answer, and asks the next manager up when nobody answers for two days. You will serve it, start an instance, answer it, and see it survive a restart of the daemon.

## 1. Write the flow

Make a folder with two files.

`qoqi.json`:

```json
{
    "name": "approvals",
    "entry": "approvals.qi"
}
```

`approvals.qi`:

```qoqi
import std.flow;
import std.flow.{Flow, Inbox};
import std.time;

pub struct Answer {
    approved: Bool;
    why: String;
}

pub enum Decision {
    Approved(String),
    Refused(String),
    Unanswered,
}

pub struct Expense {
    id: String;
    author: String;
    answers: Inbox<Answer> = flow.inbox<Answer>(1);
}

impl Flow<Decision> for Expense {
    fn run() -> Decision {
        let approver = managerOf(self.author);

        for _ in 0..3 {
            stage asking;

            ask(approver, self.id);

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

            approver = managerOf(approver);
        }

        return Decision.Unanswered;
    }
}

pub step fn managerOf(name: String) -> String {
    match name {
        case "lin": {
            return "grace";
        }

        case "grace": {
            return "ada";
        }
    }

    return "the board";
}

pub step fn ask(approver: String, id: String) {
    println("[mail] to ${approver}: expense ${id} waits for your word");
}

fn main() {}
```

Three things make this a flow:

*   **`Expense` implements `Flow<Decision>`.** Its fields are the state of one instance; `run` is what an instance does; `Decision` is what it returns at the end.
*   **`step fn`** is the only way it reaches the world. What a step returned is kept, and a finished step is never run again.
*   **`wait match`** waits for whichever comes first: a message on the inbox, or two days. While it waits, the instance is frozen in the store; nothing runs.

Check it compiles:

```console
$ qoqi check
approvals.qi: no problems
```

## 2. Serve it

Start the daemon, and leave it running:

```console
$ qoqi serve
qoqi: serving from /home/lin/.qoqi, store /home/lin/.qoqi/flows.db, at http://127.0.0.1:7070
qoqi: no program yet: qoqi deploy <program> gives it one
qoqi: the console is at http://127.0.0.1:7070: sign in as admin, password admin, and choose another
```

It keeps everything in its home, `~/.qoqi` (or `QOQI_PATH`): its store, its logs, and what the commands need to find it. In another terminal, in the project's folder, give it the program:

```console
$ qoqi deploy .
approvals deployed, version 43381189
```

## 3. Start an instance

```console
$ qoqi start Expense expense:1 '{"id": "1", "author": "lin"}'
```

The daemon prints what the steps print: `[mail] to grace: expense 1 waits for your word`. The instance now waits:

```console
$ qoqi ls
expense:1   Expense   waiting   asking   waits for a message on answers

$ qoqi show expense:1
Expense   expense:1   version 43381189
  waiting   waits for a message on answers
  stage     asking, pass 1
  at        approvals.qi:31 in Expense.run
  author    "lin"
  id        "1"
  step      managerOf#1 → "grace"
  step      ask#1 → null
```

`qoqi ui` opens the same in the console, in a browser: sign in as `admin`, password `admin`, and choose your own.

## 4. Survive a restart

Stop the daemon with `Ctrl-C`, and start it again with `qoqi serve`. The instance is still there, waiting at the same line, with the same variables: it was in the store all along. `managerOf` is not run again; its result was kept.

## 5. Answer it

```console
$ qoqi send expense:1 answers '{"approved": true, "why": ""}' --key mail-1
sent to expense:1, on answers

$ qoqi result expense:1
Approved("grace")
```

The `--key` makes a message sent twice, a mail link clicked twice, one message.

## 6. Test it

A test runs the flow on a clock of its own, so two days pass in a moment. `approvals_test.qi`:

```qoqi
import std.flow;
import std.test;
import std.time;
import approvals;
import approvals.{Expense, Answer};

fn testNobodyAnswersGoesUp() {
    let trial = flow.test(Expense { id: "2", author: "lin" });

    trial.go();
    trial.advance(2.days());
    trial.send("answers", Answer { approved: true, why: "" });

    test.equal("${trial.result()}", "Approved(\"ada\")");
}
```

```console
$ qoqi test
approvals_test.qi
  ok    nobody answers goes up
1 passed
```

## Next

*   [Flows](../concepts/flows.md), [steps](../concepts/steps.md) and [waiting](../concepts/waiting.md): the model, piece by piece.
*   [Testing flows](../guides/testing-flows.md), including `everyCrash`.
*   [Steps in your application](../guides/application-steps.md): a step carried out by a service in Go, PHP or TypeScript.
*   [examples/flows](../../examples/flows): an order with payments and undos, and a subscription that lives for months.
