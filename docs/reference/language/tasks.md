# Tasks: Doing Several Things at Once

A program can run several of its functions at the same time: ask three servers at once, answer a request while another is still being worked on, read a file while the user types. Each such function is a **task**, and tasks talk through **channels**. Both are in the module [`std.task`](stdlib.md#stdtask). What the language adds is one word, **`wait`**, written wherever a task waits: for another task, a channel, the clock, or the first of several.

```rust
import std.task;
import std.http;

fn main() -> Int {
    let first = task.start(fn() -> Int { return http.get("https://example.com/a").map(r -> r.status).or(0); });
    let second = task.start(fn() -> Int { return http.get("https://example.com/b").map(r -> r.status).or(0); });
    println(wait first, wait second);           // both were asked at the same moment
    return 0;
}
```

A complete program: [`test/e2e/cases/tasks.qi`](../../../test/e2e/cases/tasks.qi); in a flow, [subscriptions](../../../examples/flows/subscriptions.qi).

## One task at a time

Only one task runs at any moment. It keeps running until it has to **wait**, and only then does another get its turn:

| A task waits when it | Until |
|----------------------|-------|
| waits for a task: `wait job` | that task has ended |
| waits to receive from a channel that is empty, or to send to one that is full: `wait jobs.receive()`, `wait jobs.send(job)` | a value or room comes, or the channel is closed |
| waits for the clock: `wait time.after(500.milliseconds())` | the time has passed |
| waits for the first of several: `wait match { ... }` | one of them has come |
| asks a server (`http.get` ...), or waits for a request (`server.next`) | the answer or the request has come |
| reads a line (`io.readLine`), runs another program (`os.run`) | the line is typed, the program has ended |
| calls `task.pause()` | the other tasks that can go on have had their turn |

What a task waits for in the program itself is written `wait`, so that each place it may lose its turn is in sight. Nothing else switches tasks. Between two such moments a task is alone: it can read a value, work on it and write it back, and no other task has changed it meanwhile. There are no locks to take and nothing to declare as shared: tasks use the same lists, structs and variables, and closures capture them as they always do.

What needs care is a change with a wait in the middle of it:

```rust
let before = account.balance;
let rate = http.get(ratesUrl);          // other tasks run here
account.balance = before - amount;      // and may have changed the balance
```

Read after the wait, or give the account to one task and send it requests through a channel.

This also means tasks do not make a computation faster: they share one processor. They are for waiting on several things at once. A long computation that should let others go on meanwhile calls `task.pause()` now and then.

The order is always the same: a new task starts when the one that made it next waits, and tasks that can go on do so in the order they became able to. A program that only waits for its own tasks and channels prints the same thing on every run.

## Tasks

`task.start(work)` runs a function and returns at once with a `Task<T>`, `T` being what the function returns. `task.run(action)` is the same for a function run for what it does: it gives a `Task<Bool>`.

```rust
let total = task.start(fn() -> Int { return count(file); });
// ... something else ...
match wait total {
    case Ok(n): { println(n); }
    case Err(failure): { println("counting failed:", failure.describe()); }
}
```

`wait total` gives a `Result<T, Failure>`: what the function returned, or the [failure](error-handling.md) that stopped it. **A task that stops does not stop the others**: a `panic` or an index out of bounds in a task is a value for whoever waits. A task can be waited for several times and by several tasks; all get the same.

| | |
|---|---|
| `wait task` | `Result<T, Failure>`; `(wait task).expect(message)` for the value |
| `task.isDone()` | Whether it has ended |
| `wait task.all(tasks)` | `Result<[T], Failure>`: every value in order, or the first failure, once all have ended |
| `wait task.settle(tasks)` | `[Result<T, Failure>]`: how each ended, failures among them |
| `wait task.map(items, work)` | One task per item, as `task.all` of them: `wait task.map(urls, url -> http.get(url))` |
| `task.pause()` | Lets the others have a turn |
| `task.current()` | The number of the task that asks: 0 is `main` |

## Channels

A `Channel<T>` carries values from the tasks that send to the tasks that receive, in order. `task.channel<T>(capacity)` makes one that holds up to `capacity` values waiting to be received.

```rust
let lines = task.channel<String>(16);
task.run(fn() {
    for name in files { wait lines.send(io.readFile(name).or("")); }
    lines.close();
});
lines.each(fn(text: String) { println(text.len()); });
```

| | |
|---|---|
| `wait channel.send(value)` | Waits while the channel is full. With a capacity of 0, waits until a task has received the value |
| `wait channel.receive()` | The next value, waiting for one; `null` once the channel is closed and empty |
| `channel.each(action)` | Every value until the channel is closed |
| `channel.drain()` | The same, as an array |
| `channel.close()` | Nothing more will be sent. What was sent is still received |
| `channel.isClosed()`, `len()` | |

Closing is how a receiver learns there is nothing more: a loop on `each` ends, and tasks waiting in `receive` go on with `null`. `send` and `receive` alone, without `wait`, only say what to wait for: `jobs.send(job);` as a statement sends nothing, and the compiler says so (`E2043`). Sending to a closed channel is a mistake in the program and stops the task that does it.

A channel with room for one value is a lock, for the change with a wait in its middle: take the value out before, put it back after ([`tasks.qi`](../../../test/e2e/cases/tasks.qi) does this for an account). A channel holding the numbers of the free workers limits how many tasks work at once (`build.qi`).

## The first of several: `wait match`

`wait match` waits for several things together, and goes on with the first that comes, once:

```rust
let running = true;
while running {
    wait match {
        case let job = jobs.receive(): { handle(job); }
        case quit.receive(): { running = false; }
        case time.after(5000.milliseconds()): { log.info("no job for five seconds"); }
    }
}
```

Each case is something to wait for, written in one of three ways:

| | |
|---|---|
| `case let name = waitable:` | Takes what it gives, and names it: `job` is a `Job?`, null once the channel is closed |
| `case waitable is Pattern:` | Takes what fits the pattern, as a `case` of a `match` does: `case commands.receive() is Stop:`, `case commands.receive() is Resize(w, h):`, `case commands.receive() is null:` |
| `case waitable:` | Takes what it gives, unnamed |

The cases on one channel together take every value it can give, `null` included: a value no case takes would stay first in the channel, and the match would wait for it for good. The compiler checks them as it checks a `match` (`E2028`).

A `wait match` gives a value as a `match` does, each case ending in one:

```rust
let answer = wait match {
    case let reply = replies.receive(): reply ?? "closed";
    case time.after(2000.milliseconds()): "no answer in two seconds";
};
```

Several cases ready at once take turns from one wait to the next, so that one always busy does not keep the others waiting. A `wait match` no task can ever end is a deadlock, as a lone `wait channel.receive()` would be. A receive that gives up after a while is a `wait match` of the receive and `time.after`.

### Waitables

What `wait` waits for is a **waitable**: a value of a type that implements `Waitable<T>` of `std.task`, `T` being what `wait` gives.

| Waitable | `wait` gives |
|----------|--------------|
| A `Task<T>` | `Result<T, Failure>` |
| `channel.receive()` | `T?`: the next value, null once the channel is closed and empty |
| `channel.send(value)` | Nothing: the value was taken |
| `time.after(length)` | Nothing: the time has passed |
| `task.all(tasks)`, `task.map(items, work)` | `Result<[T], Failure>` |
| `task.settle(tasks)` | `[Result<T, Failure>]` |

A waitable does nothing until it is waited for: a receive, a send, `time.after` made and thrown away is a mistake (`E2043`). A task is the exception: it runs whether anyone waits for it or not, and `task.run(work);` alone is how one is left to itself. `wait` on a value that cannot be waited for is `E2044`.

A type of the program can be waited for by implementing `Waitable<T>`: whether it has come (`ready`), what it gives without taking it (`peek`) and taking it (`take`), and how a task that waits is woken (`watch`), which it does through the waitables above.

`wait` applies to what comes right after it, a name, a call, a member: `wait jobs.receive() ?? 0` waits, then gives 0 for null; `(wait job).expect("a count")` waits, then takes the value. It is a word only there: a function or a variable called `wait` is used as before, `wait(x)`, `wait + 1`.

## How a program ends

A program ends when `main` has returned **and every task has ended**. `main` does not have to wait for the tasks it started.

*   **`main` fails**, or any task calls `os.exit`: the program ends at once, whatever the other tasks are doing.
*   **A task fails and somebody waits for it**: the failure is theirs, and the program goes on.
*   **A task fails and nobody ever waits for it**: the other tasks finish, then the failure ends the program as if it had come from `main`, with its message, its place and status 1. A failure is never lost.
*   **Every task waits for another** and nothing can wake any of them, a *deadlock*: the program stops with error `R0009` instead of hanging.

```
[ERROR] R0009: deadlock: 2 tasks wait for each other, and none can go on
  --> greet.qi:9:21 in function 'lambda_0'
     |
   9 |         let heard = wait hears.receive() ?? "nothing";
     |                     ^^^^
```

The place is where the last task went to wait. The usual cause is a channel nobody closes: a task in `each` or `wait channel.receive()` waits for values that will never come. `attempt` does not catch a deadlock: no task is left to do anything about it.

## A server

`http.serve` and `server.handleAll` answer each request in a task of its own: a handler that waits, for another server or for the clock, does not keep the other requests waiting. To do something else while serving, start the server in a task:

```rust
let server = http.listen(8080).expect("a free port");
let serving = task.run(fn() { server.handleAll(app.handler()); });
// ... the program goes on; server.close() ends the task
```

## How it works

Each task has its own calls in progress and its own registers; everything else, the heap first, is shared. In the virtual machine a task is a goroutine, and the goroutines pass a single turn around: the one that has it runs bytecode, the others are parked. A task gives the turn up inside the native function that makes it wait, and takes up there when the turn comes back. Because only one goroutine touches the heap at a time, the collector and every instruction are as they were for a program of one task.

While a task waits for the world (the clock, the network, input, another program) its goroutine does the waiting without the turn, so the others go on; it queues for the turn when the answer is there.

Channels and waitables are written in Qoqi, on a few natives: suspend the task that asks, with a time limit or not, resume another, be woken when a task ends, and say which task is running. `wait value` is the call `task.waitFor(value)`, which the parser writes; a `wait match` puts its task in the line of each thing it waits for, so that the first to come wakes it. That is why a `Channel<T>` is typed like any other generic struct, and collected like one.

## What is not there

| Missing | Today |
|---------|-------|
| Using several processors | Tasks take turns on one; a computation is not faster for being split |
| Stopping a task from outside | Close the channel it reads, or have it check a flag |
| Interrupting a task that never waits | It keeps the turn; call `task.pause()` in long loops |
| `for value in channel` | `channel.each(...)`, or `while` around `wait channel.receive()` |
| Files read without the others waiting | Reading and writing a file keeps the turn; they are short |
