# Waiting

`wait` is how a flow waits: for a time, for a message from outside, for a task or a child flow, or for the first of several. While an instance waits, nothing runs and it costs nothing: it is in the store.

```qoqi
wait time.after(3.days());
let answer = wait self.answers.receive();
let outcome = wait payment;
```

`wait` marks a place in the code; it does not colour a function. A function with a `wait` in it is called like any other.

## What can be waited for

| Waitable | `wait` gives |
|----------|--------------|
| `time.after(length)`, `time.until(moment)` | Nothing: the time has passed |
| `inbox.receive()` | The next message on an [inbox](#messages-from-outside) |
| `flow.cancelled()` | The reason, when the instance is [cancelled](failures.md#cancellation) |
| An `Instance<F>` | `Result<T, Cancelled>`: what the child's `run` returned, or how it was cancelled |
| A `Task<T>` | `Result<T, Failure>`: what the task's function returned, or how it failed |
| `channel.receive()`, `channel.send(value)` | The next value (`null` once closed and empty); room on the channel |
| `task.all(tasks)`, `task.map(items, work)` | Every value, in order, or the first failure |
| `task.settle(tasks)` | Every outcome, in order: it never fails itself |

## Timers

```qoqi
wait time.after(2.days());
```

A timer is written down as the moment it ends. An instance woken late, because no daemon ran for an hour, finds its timer long past and goes on at once; its next `time.now()` is the time it is. What was due is kept in the store, not in the memory of a process.

The clock is the store's: several daemons on several machines read one time, and an instance taken up by another daemon does not see time go back.

## Messages from outside

An **inbox** is a field of the flow that the world sends messages to: a person's decision, a provider's webhook, a command.

```qoqi
struct Expense {
    id: String;
    answers: Inbox<Answer> = flow.inbox<Answer>(1);
}

let answer = wait self.answers.receive();
```

`flow.inbox<T>(capacity)` holds that many messages not yet taken. The message is sent through the daemon:

```console
$ qoqi send expense:1 answers '{"approved": true, "why": ""}' --key mail-1
```

or `POST /api/instances/{id}/inboxes/{inbox}` ([HTTP API](../reference/http-api.md)). The **key** makes a message sent twice one message. The message's type is checked against the inbox's.

## The first of several

`wait match` waits for whichever of its cases comes first:

```qoqi
let answer = wait match {
    case let a = self.answers.receive(): a;
    case time.after(2.days()): null;
    case let reason = flow.cancelled(): { return Decision.Withdrawn(reason); }
};
```

*   `case let x = w:` takes what `w` gives and names it.
*   `case w is P:` takes what fits the pattern `P`, as a `match` case does.
*   `case w:` takes what `w` gives, unnamed.

The first that happens is the only one taken; the others are given up. A `wait match` gives a value, as any `match` does, or each case has a block, which may `return`. The cases on one inbox, together, must take every value it can carry, as the cases of a `match` must.

`match wait x { ... }` is different: it waits for `x` alone, then matches what came.

## Several things at once

A flow may run tasks, as any program does: `task.start`, `wait task.all(...)`, `wait task.map(items, work)`. Tasks take turns: one runs at a time, and they change hands only where they `wait` or call a step. So the instance can always be written at a moment when nothing is half changed.

```qoqi
let receipts = (wait task.map(invoices, invoice -> charge(invoice)))?;
```

Twenty tasks that each reach a step cost one write of the instance, not twenty: the daemon runs every task that can go on, writes once, then lets the steps' requests leave.

When every task of an instance waits for another task of it, and nothing from outside can wake any of them, nothing ever will: the instance stops (`R0009`) instead of sleeping for good.

See also [tasks](../reference/language/tasks.md), in the language reference.
