# Durable workflows without replay

Every durable workflow engine you have used asks the same thing of your code: be deterministic. Do not read the clock. Do not draw a random number. Do not range over a map. Do not start a goroutine. And when you change the code, make sure every workflow already running still takes the same path through it, or keep the old code around until the last of them is done.

That rule is not a quirk of one product. It is the price of the technique they all use to survive a crash: **replay**. Qoqi Flow does not replay. This post is about what it does instead, what that costs, and why it needs its own language to do it.

## Why everyone replays

A workflow that waits three days for a manager cannot keep a process alive for three days. When the process dies, or the machine, or when you deploy, the workflow has to come back somewhere else, at the line it was at, with its variables.

Temporal, Restate, DBOS, Inngest and Vercel's Workflow SDK all bring it back the same way. They record what each step returned. To take the workflow up, they call its function again from the top; every step already recorded returns its saved result instead of running, and the code races forward until it reaches the point where it stopped. The function has rebuilt its own stack by running again.

It works, as long as the second run takes exactly the same path as the first. Hence the rules. In Temporal's Go SDK:

```go
// time.Now() here would differ on replay, and the workflow would take
// another branch than the one its history recorded
if workflow.Now(ctx).Before(user.TrialEnds) {
	_ = workflow.Sleep(ctx, 48*time.Hour)
}
```

Each engine has its own set of replacements: `workflow.Now`, `ctx.date.now()`, a seeded `Math.random`, a sandbox that removes `fetch`. None of them is a mistake. They are what replay requires.

Why replay, then? Because the others are libraries. They run inside Go, Python, TypeScript or Java, languages whose call stacks they cannot read or write. There is no portable way to take a running Python function, write down where it is and what its locals hold, and resume it tomorrow on another machine. So they rebuild the stack the only way available to a library: by calling the function again.

## Owning the stack

Qoqi Flow is a language and a virtual machine. Its stack is data the runtime owns. So when a flow waits, the daemon simply writes down the state of execution: every task, every call in progress with its function and instruction pointer, the registers, and the objects of the heap they point to. When the wait ends, it reads that back and the flow goes on from the instruction it was at.

Here is an expense approval waiting for a manager:

```rust
impl Flow<Decision> for Expense {
    fn run() -> Decision {
        let approver = managerOf(self.author);

        for round in 0..3 {
            stage asking;
            ask(approver, self.id, self.amount, round);

            let answer = wait match {
                case let a = self.answers.receive(): a;
                case time.after(self.patience): null;
            };
            ...
```

And here is that instance in the store, at its `wait match`, trimmed:

```json
{
  "format": "qoqi.vm/2",
  "chunk": "sha256:7491ab8392ed436d99c866a25e43ef8d92c6464f49859eb671adf4992a4d6b2a",
  "tasks": [{
    "id": 0, "waiting": "suspendFor", "until": 1792338268167,
    "frames": [{"function": 12, "ip": 37, "base": 0, "size": 22},
               {"function": 13, "ip": 444, "base": 22, "size": 17}],
    "function": 14, "ip": 387,
    "stack": [{"ref": 0}, {"ref": 1}, {"ref": 2}, 0, 3, ..., 54000, 0, -1, ...]
  }],
  "heap": [{"kind": "object", "type": "std.task.Arm", "keys": ["given", "ready", "until", "watch"], ...}, ...]
}
```

One task, three calls in progress, 45 registers, 33 objects: about 3 KB. That is the whole instance. No process holds it while it waits. When the answer comes, minutes or months later, any daemon on the same store thaws it on the bytecode named by `chunk` and lets the task go on as if `wait match` had just returned.

Because nothing runs twice, the code between steps does not have to be deterministic. A flow may read the clock, draw a random number, iterate a map in whatever order it comes. It reads them once, and what it read is frozen with the rest.

## The one rule that remains

Freezing only works if nothing escapes the program between two writes. If a flow could post to a webhook in the middle of its code, a crash after the post and before the next write would post it again when the flow is taken up from the last write.

So Qoqi keeps one rule, the same one every engine has: the world is reached only through a **step**. The difference is that Qoqi checks it. The compiler follows every call a flow can make, through functions, closures and trait methods, and refuses the one that reaches the network, a file or a database outside a step:

```console
$ qoqi check renewal.qi
[ERROR] F0001: a flow reaches the network outside a step
  --> renewal.qi:26:67 in function 'Renewal.run'
    |
 26 |         http.to(Method.Post, "https://hooks.example.com/renewals").body(self.user.id).send();
    |                                                                   ^
    |
    = Suggestion: Move the call into a step fn, and call the step here
```

The other engines ask for the same discipline and find out at run time, on replay, or not at all.

A step's result is written before the flow goes on. The runtime keeps one invariant above all: **nothing leaves the program before the state is saved**. Before a step's request goes out, a row for that attempt is written, with its key and its deadline. After a crash, a step that may have half run is run again under the same key, `flow.key()`, which a payment provider uses to answer the second request like the first. A step declared `Settled<T>` is never run a second time: the flow is told `Unknown`, and decides what that means.

```rust
match capture(authorization, self.cart.amount) {
    case Done(reference): { return Outcome.Paid(reference); }
    // Stopped while the capture was on its way: ask the bank
    case Unknown: { return lookup(authorization); }
}
```

## What replay gave the others for free

Replay has one real advantage: new code is just code. When a workflow is replayed on a new version, it simply runs the new code; the danger is only that it takes another path than its history. Freezing has the opposite problem. A frozen instance holds instruction pointers into a specific bytecode. It cannot move to new code in the middle of a function: instruction 387 of the old version means nothing in the new one.

So in Qoqi an instance stays on the bytecode it started on, named by its fingerprint, and moves to a new version only at a **stage**: a named line in `run` where the state is nothing but named variables.

```rust
for round in 0..3 {
    stage asking;     // here, and only here, an instance moves to new code
    ...
```

At each deploy the shape of every stage, the variables alive there and their types, is written to `flow.lock`, which you commit. A new version that changes a shape is flagged by the compiler, and refused by the daemon while an instance is at that stage, until a `migrate fn` says how to carry the old variables over. The result is closer to a database migration than to a replay: explicit, checked, and run once.

## Crashing on purpose

If correctness depends on what happens at every write, then every write is where you test it. A Qoqi trial runs a flow on a clock of its own, so three days pass in a moment, and `everyCrash` runs a scenario again and again, stopping the process after each write the instance makes, and requires the same ending every time:

```rust
fn testEveryCrash() {
    flow.test(expense("4")).everyCrash(fn(t: flow.Trial<Decision>) {
        t.go();
        t.advance(2.days());
        t.send("answers", Answer { approved: true, why: "" });
    });
}
```

## What it costs

A new language is a real cost, and we know it. Two things soften it. Only the flow is written in Qoqi: a step that needs your code, your database or your models stays in your service, in any language that answers Qoqi's protocol, a signed HTTP POST; `qoqi gen` writes the typed interfaces for Go, PHP and TypeScript. And the language is small and ordinary on purpose: structs, enums, `match`, `Result`, closures, tasks.

The rest, plainly. The whole state is written at each save, at most once per ten milliseconds while steps are under way; writing only what changed is for later. An instance that loops and waits with no `stage` in its loop keeps everything it did since it started, and the compiler warns about it (`W0011`). And Qoqi Flow is at its first release: it works end to end and is tested by crashing it on purpose, but it does not have years of production behind it yet.

## Try it

```console
$ curl -fsSL https://qoqi.dev/install.sh | sh
$ qoqi serve &
$ qoqi deploy approvals.qi
$ qoqi start Expense expense:1 '{"id": "1", "author": "lin", "amount": 1200, "patience": "1m"}'
$ kill -9 %1 && qoqi serve &      # it comes back at the line it was at
$ qoqi send expense:1 answers '{"approved": true, "why": ""}'
```

The [quickstart](/docs/getting-started/quickstart) takes five minutes. [How the runtime works](/docs/internals/runtime) goes all the way down, and [the comparisons](/vs/) set Qoqi Flow beside each of the others.
