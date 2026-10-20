# Steps

A **step** is the only way a flow reaches the world. Its result is kept, and a finished step is never run again.

```qoqi
step fn charge(invoice: Invoice) -> Result<Charge, Decline> {
    return gateway.charge(invoice.id, invoice.amount, flow.key());
}
```

A step is an ordinary function with `step` before `fn`. Its body can do anything, with tasks and the whole library. What is kept is what the step was **given** and what it **returned**; its body is not durable: it runs to its end, or fails.

## The rule, checked by the compiler

Inside a flow, and in every function it calls, nothing reaches outside the program except through a step. The compiler follows every call from `run`, through function values and trait methods, and refuses a call that reaches the world outside a step:

```text
[ERROR] F0001: a flow reaches the network outside a step
  --> refund.qi:24:16 in function 'notify'
   |
24 |     let sent = http.post(hook, body);
   |                ^^^^^^^^^
   |
   = Note: 'notify' is called by Refund.run at refund.qi:12:9
   = Suggestion: make 'notify' a step fn, or move the call into one
```

Three things need no step: the clock (`time.now()`), random numbers, and the program's own output (`println`, `std.log`). The clock and random numbers are given back to the flow when it is taken up: after a crash, what it read is in what was written, and resumed from its stage with a fix, it is given back, in order, what it read since the stage ([failures](failures.md#resuming-with-a-fix)). A line printed twice after a crash costs nothing.

## What a step takes and returns

What a step is given and what it returns must be **data**: numbers, texts, booleans, structs, enums, arrays and dictionaries of those, `Result`, optionals. A task, a channel, a connection or a function means nothing once kept, and the compiler refuses it (`F0004`).

A step called from a step is a plain call.

## Keys

Each call of a step has a **key**: the same each time that call of that instance runs, and no other step of any instance has it. `flow.key()` gives it, inside the step. Given to a payment provider as an idempotency key, it makes a second attempt the first:

```qoqi
step fn charge(invoice: Invoice) -> Result<Charge, Decline> {
    return gateway.charge(invoice.id, invoice.amount, flow.key());
}
```

In a database of your own, a unique column holding the key makes a second insert find the first.

## After a crash

A step is done when the world was told and the answer was kept. A process that stops between the two leaves a question: did the provider take the payment? Each step says what to do then.

**Run it again** (the default). A plain step is run again, under the same key. That is right for a provider that takes an idempotency key, and for a step that changes nothing. `flow.attempt()` is `1` the first time and `2` the next, so a step for a provider that takes no key can ask what it did before acting again:

```qoqi
step fn transfer(to: Account, amount: Int) -> Result<Transfer, Refusal> within 2.hours() {
    if flow.attempt() > 1 {
        let found = bank.transferByReference(flow.key());

        if found != null {
            return Ok(found);
        }
    }

    return bank.transfer(to, amount, flow.key());
}
```

**Never run it twice.** A step that returns `Settled<T>` is not run again. The flow gets `Done(value)`, or `Unknown` when the process stopped while the step ran and nothing says whether the world was told. The `match` must say what `Unknown` means:

```qoqi
step fn wire(to: Account, amount: Int) -> Settled<Result<Wire, Refusal>> within 2.hours() {
    return Done(bank.wire(to, amount));
}

match wire(account, amount) {
    case Done(Ok(sent)): { ... }
    case Done(Err(refusal)): { ... }
    case Unknown: { ... }
}
```

A `Settled` step must say its time limit (`F0009`), so that a default never decides a transfer.

## Time limits

A step has a minute, unless its declaration says otherwise, after its signature:

```qoqi
step fn report(month: Int) -> Report within 2.hours() { ... }
```

Past its limit the step is given up, as if the process had stopped in the middle of it: it is run again, or a `Settled` step gives `Unknown`.

## Asking to be run again

What a step returns is its answer, kept for good: an `Err(Declined)` is an answer, and the flow decides on it. A network down for a minute is not an answer. A step that meets one says so with `flow.retry`, and is run again later, **under the same key**:

```qoqi
step fn charge(invoice: Invoice) -> Result<Charge, Decline> within 5.minutes() {
    let answer = gateway.charge(invoice.id, invoice.amount, flow.key());

    if answer is Err(Unreachable) {
        flow.retry("the gateway is unreachable", 2.seconds());
    }

    return answer.mapErr(declineOf);
}
```

Between two tries the instance waits in the store, like any wait. When to give up is the step's to say: `if flow.attempt() > 5 { return Err(Decline.Unreachable); }`. A step that keeps asking past the daemon's limit stops the instance, for a person.

Calling the step a second time from the flow would make a second key, and perhaps a second payment. `flow.retry` is the way to run a step again on purpose.

## Steps in the store's database

A step that writes only to the database the flows are kept in asks for it with `flow.store()`. Its rows and its result are one transaction, committed by the daemon: a step stopped half way runs again from nothing, and one that finished never runs again. No key is needed in its rows.

```qoqi
step fn settle(invoice: Invoice) -> Result<Receipt, sql.Error> {
    let store = flow.store()?;

    store.exec("INSERT INTO receipts (invoice, cents) VALUES (?, ?)", [Value.Text(invoice.id), Value.Int(invoice.cents)])?;

    return Ok(Receipt { invoice: invoice.id });
}
```

The transaction is the step's to use, not to end: it may not `commit` it. A step may `CREATE TABLE IF NOT EXISTS` before it writes.

## Steps carried out by your application

A step declared `by` a queue has no body: the daemon hands it to your application, in Go, PHP or TypeScript, and keeps what it answers. See [steps in your application](../guides/application-steps.md).

```qoqi
step fn charge(customer: String, cents: Int) -> Result<String, Decline>
    within 30.seconds() by payments undo refund;
```
