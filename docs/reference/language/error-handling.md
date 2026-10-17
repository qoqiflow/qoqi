# When Things Go Wrong

There are two ways for something to go wrong in a program, and Qoqi handles them differently.

| | What it is | What happens |
|---|---|---|
| **A failure that is part of the job** | A file that is not there, input that makes no sense, a number out of range | The function **returns** it, as a `Result`. Whoever calls it has to look; the compiler checks |
| **A failure that should never happen** | An index outside an array, a division by zero, a state the program was sure it could not reach | The program **stops**, with a message that says where. `attempt` is for the few places that must survive it |

Complete programs using both: the [example flows](../../../examples/flows).

## Returning a Failure: `Result`

`Result<T, E>` is an enum of the standard library: the value a function produced, or the reason there is none. Its name needs no import.

```rust

enum Problem { Empty, NotANumber(String) }

fn count(text: String) -> Result<Int, Problem> {
    if text == "" { return Err(Problem.Empty); }
    let n = text.parseInt();
    if n == null { return Err(Problem.NotANumber(text)); }
    return Ok(n);
}
```

The caller takes it apart with a `match`, which has to handle both:

```rust
match count(input) {
    case Ok(n): { println("ordered", n); }
    case Err(Problem.Empty): { println("nothing was typed"); }
    case Err(Problem.NotANumber(text)): { println(text, "is not a number"); }
}
```

or with one of its methods:

| Method | Gives |
|--------|-------|
| `isOk()`, `isErr()` | |
| `value() -> T?` | The value, or `null` |
| `reason() -> E?` | The reason, or `null` |
| `or(fallback) -> T` | The value, or the fallback: `io.readFile(path).or("")` |
| `orElse(make: fn(E) -> T) -> T` | The value, or what the function makes of the reason; worked out only when there is no value |
| `map<U>(convert) -> Result<U, E>` | The value turned into another; a reason passes through |
| `mapErr<F>(convert) -> Result<T, F>` | The reason turned into another; a value passes through |
| `then<U>(step) -> Result<U, E>` | A next step that can fail too: runs only if there is a value; the first reason met is the one that comes out |
| `expect(message) -> T` | The value; **stops the program** if there is none. For where a failure would be a mistake in the program |

```rust
fn order(text: String) -> Result<Order, Problem> {
    return count(text).then(limited).map(fn(n: Int) -> Order { return Order { count: n }; });
}
```

### Passing a failure on: `?`

Most functions that call something that can fail have nothing to do about the failure but hand it to their own caller. A `?` after the call does that:

```rust
fn order(text: String) -> Result<Order, Problem> {
    let n = limited(count(text)?)?;
    return Ok(Order { count: n });
}
```

`count(text)?` is the number if there is one. If there is none, `order` returns right there, with the same failure. What comes after only runs with a value.

The function has to return a `Result` whose reason is of the same type. When it is not, turn one into the other on the way: `io.readFile(path).mapErr(asProblem)?`. In `main` there is no caller to hand a failure to, so `?` ends the program there, as a `panic` does: the `defer`s run, and the reason is the message, with what its `describe()` says when it has one. A script can then go straight on:

```rust
fn main() -> Int {
    let config = io.readFile("app.json")?;      // [ERROR] R0001: cannot read app.json: no such file or directory
    deploy(config)?;
    return 0;
}
```

Where a failure is to be looked at rather than end the program, a `match` is still the way. Inside a function written in `main`, `?` is the function's own, as anywhere else.

The same operator works on an optional, in a function that may return `null`: `let user = find(id)?;` gives the user, or returns `null`.

**Nothing to give back.** A function that does something and may fail, with nothing to say when it worked, returns a `Result<Void, E>`, and `Ok()` when it went well:

```rust
fn save(order: Order) -> Result<Void, String> {
    if full() { return Err("the disk is full"); }
    write(order);
    return Ok();
}
```

`save(order)?;` passes its failure on, and `case Ok(_):` matches the success. `Ok()` is refused where nothing says the value is `Void`.

**The reason can be any type.** A text is the least useful: a program cannot act on it. An enum lets the caller tell the cases apart; a struct can carry whatever helps.

### An optional or a `Result`?

`T?` says "there may be nothing", and that is all `indexOf` or `list.first()` have to say. `Result` is for when there is a *why*: `parseInt` returns an optional, `io.readFile` a `Result`.

## A Trace for an Error That Is a Value

An error that stops the program says where it happened. An error that is returned can too: `result.stack(skip)` gives the calls in progress, to keep in the error.

```rust
import std.result;
import std.result.{Location};

struct ConfigError {
    key: String;
    message: String;
    stack: [Location];
}

fn invalid(key: String, message: String) -> ConfigError {
    // stack(1): from whoever called this function, not from here
    return ConfigError { key: key, message: message, stack: result.stack(1) };
}
```

`result.trace(message, stack)` writes it over several lines:

```
brightness should be a number, not 'x'
  at config.qi:54:27 in function 'number'
  called from config.qi:61:12 in function 'percent'
  called from config.qi:96:17 in function 'main'
```

A `Location` has `function`, `file`, `line`, `column`, and `describe()`.

## Errors of the Standard Library: `io.Error`

Reading and writing files returns a `Result` whose reason is a struct:

```rust
pub struct Error {
    kind: ErrorKind;        // NotFound, PermissionDenied, IsDirectory, NotDirectory, AlreadyExists, Other
    operation: String;      // "read", "write" or "append"
    path: String;
    message: String;        // as the operating system puts it: "no such file or directory"
    stack: [Location];      // where the program asked for the file
}
```

The `kind` is for the program, the rest for telling a person:

```rust
match io.readFile("settings.conf") {
    case Ok(text): { load(text); }
    case Err(error): {
        match error.kind {
            case ErrorKind.NotFound: { useDefaults(); }
            default: { println(error.describe()); }     // cannot read settings.conf: permission denied
        }
    }
}
```

`error.trace()` adds where it was asked for.

## Stopping: `panic`

`panic` is a builtin. It stops the program with a message, written from its arguments as `println` writes them:

```rust
fn take(queue: Queue) -> Int {
    if queue.items.len() == 0 {
        panic("the queue is empty after", queue.taken, "items");
    }
    ...
}
```

It is for a state the program cannot do anything sensible about: a mistake to be fixed, not a case to be handled. A function that can fail in ways its caller should deal with returns a `Result` instead.

The compiler knows that nothing comes after a `panic`: a function may end with one in place of a `return`, a test against `null` followed by a `panic` leaves the value non-null afterwards, and code after one is reported as unreachable.

```rust
fn area(shape: Shape) -> Int {
    match shape {
        case Shape.Square(side): { return side * side; }
        case Shape.Unknown: { panic("no area for an unknown shape"); }
    }
}
```

## What a Runtime Error Looks Like

A program that stops, by `panic` or by any failure the virtual machine finds, is reported the way a compile error is: a code, the message, the place with its source line underlined, a suggestion where there is a usual fix, then the calls that led there.

```
[ERROR] R0002: index out of bounds: 7 (length 3)
  --> orders.qi:12:17 in function 'pick'
      |
 12 |     return items[index];
      |                 ^
      |
      = Suggestion: Compare the index with the length (items.len()) before using it, or use items.at(index), which gives null instead of failing
  called from orders.qi:31:20 in function 'total'
  called from orders.qi:48:13 in function 'main'
```

The exit code is 1.

| Code | Kind | Raised by |
|------|------|-----------|
| `R0001` | Panic | A call of `panic` |
| `R0002` | Bounds | An index outside an array or a text |
| `R0003` | Cast | `value as Type` on a value of another type |
| `R0004` | Arithmetic | A division by zero, a result too large for an `Int` |
| `R0005` | Depth | Calls nested deeper than the limit (`--max-depth`) |
| `R0006` | Memory | More memory than the limit (`--heap-mb`) |
| `R0007` | Library | A function of the standard library given arguments it cannot work with |
| `R0008` | Other | Anything else; in practice, bytecode written by hand |
| `R0009` | Deadlock | Every [task](tasks.md#how-a-program-ends) waits for another. Nothing catches it |
| `R0010` | Internal | A mistake of Qoqi itself, in the virtual machine or a native function, not of the program. Nothing catches it; please report it |
| `R0012` | Budget | The program ran for more steps than its host lets it (`VM.SetStepLimit`). Nothing catches it. `qoqi` itself sets no such limit |
| `R0011` | Interrupted | The program was stopped from outside: Ctrl-C, or `VM.Interrupt`. The message says where it was. Nothing catches it, and `qoqi` exits with status 130 |

A function calling itself gives the same line many times: it is shown once, with how often. Beyond twelve different calls the rest is counted, and the outermost, where it all started, is named.

## Surviving: `attempt`

`attempt` runs a function. If the function stops, for any of the reasons above, the program does not: the failure comes back as a value.

```rust
import std.result.{Kind, attempt};

for i, job in jobs {
    match attempt(job) {
        case Ok(n): { println("job", i, "gave", n); }
        case Err(failure): { println("job", i, "failed:", failure.message); }
    }
}
```

The function takes no arguments; wrap what you want to run: `attempt(fn() -> Int { return risky(input); })`. For a function run for what it does rather than for a value, `guard(action)` returns the `Failure`, or `null` if it completed.

A `Failure` holds everything the error message would have shown:

| Field | |
|-------|---|
| `kind: Kind` | `Panic`, `Bounds`, `Cast`, `Arithmetic`, `Depth`, `Memory`, `Library`, `Other`: the kinds of the table above, as an enum to `match` on |
| `message: String` | |
| `at: Location` | Where it happened |
| `stack: [Location]` | The calls in progress, innermost first; `stack[0]` is `at` |

and `describe()` (one line) and `trace()` (with the calls).

### What is left behind

Everything done before the failure stays done: an array that was pushed to still has the item, a field that was assigned keeps its value. Nothing after it happened. If a function changes several things that belong together and may stop half way, what `attempt` hands back is the half-way state; make the changes on a copy, or in an order that is safe to interrupt.

Closures made inside the function that failed keep working, with the variables they captured.

A program that ends itself with `os.exit` is not a failure: `attempt` does not catch it, and the program ends there.

The calls that were abandoned do not count against the depth limit any more, and what they allocated is collected like any other garbage.

### When to use it

Rarely. It is a boundary, not a way to handle errors:

*   a loop that must outlive the jobs it runs;
*   a tool that runs tests and reports the ones that fail ([`std.test`](stdlib.md#stdtest) is built on it);
*   a server that must answer the next request after a handler failed ([`std.http`](stdlib.md#stdhttp) answers 500 and goes on);
*   the edge of code you do not control.

A failure that is expected is better returned as a `Result` by the function that meets it: the caller is then made to deal with it, by the compiler, instead of having to remember to wrap the call.

## Cleaning Up: `defer`

A function that takes something it has to give back (a lock, a worker from a pool, a flag it set) can leave in many ways: at its end, by any of its `return`s, by a `?` that passes a failure on, or because something it called stopped the program and an `attempt` further out survived that. `defer` says once what has to happen, next to where the thing was taken:

```rust
fn transfer(from: Account, to: Account, amount: Int) -> Result<Int, String> {
    from.locked = true;
    defer { from.locked = false; }

    let fee = feeFor(amount)?;                 // leaves here: unlocked
    if amount + fee > from.balance {
        panic("not enough on the account");    // or here: unlocked
    }
    from.balance -= amount + fee;
    to.balance += amount;
    return Ok(from.balance);            // or here: unlocked
}
```

*   `defer statement;` or `defer { ... }`. It runs when the **block** it is written in is left: a function body, the body of a loop (then at the end of every iteration, and on `break` and `continue`), an `if`, a block of its own.
*   Several defers of a block run in the opposite order: what was taken last is given back first. Only those that were reached run.
*   What a function returns is worked out first, then its defers run: assigning to a variable in a defer does not change the value on its way out.
*   A defer sees its variables as they are when it runs, not as they were when it was written.
*   When a failure passes through, every call in progress runs its defers, the innermost first; then the failure goes on to the `attempt` that waits for it, or stops the program with the same message and the same calls as without them. If a defer fails itself, the others still run, and its failure is the one that goes on.
*   A defer cannot leave: no `return`, no `?`, no `break` or `continue` of a loop around it (`E2039`). Loops of its own are its business.
*   `os.exit` ends the program there and then, without running anything.

## What Is Not There

*   **`try` / `catch`.** By design: there is one way to return a failure (`Result`) and one to survive a stop (`attempt`). A second error mechanism next to `Result` would have to be chosen between at every function.
*   **`finally`.** There is `defer`, which does not need the `try` around it.
*   **`attempt` with arguments.** The function it runs takes none.
*   **A common shape for errors.** `io.Error`, `Failure` and an error type of your own each have a `describe()`, by convention only: traits cannot yet be required of a type parameter, so nothing can ask for "any error".

## How It Works

**The error.** A failure of a running program is a `vm.RuntimeError`: a kind, a message and the call stack, taken at the moment it is raised from the frames of the virtual machine (each frame remembers the instruction that made the call, and the debug information of an instruction gives its file, line, column, source line and function). It is turned into the same `Diagnostic` the compiler's errors are shown with, which is why both look alike.

**`panic`** is a host function that returns an error of its own type; the instruction that called it turns that into a `RuntimeError` of kind panic at the place of the call. The type checker treats a call of it as the end of a path, by name.

**`attempt`** needs two things the virtual machine did not have. The first is calling back into the program from a native function: the instruction loop (`VM.run`) can be entered again for one function and left when that function returns (`VM.callFunction`). The second is undoing a call that failed (`VM.unwind`): the frames above the one that asked are dropped, the variables of those frames that a closure still points at are moved to the closure, as they are when a function returns normally, and the registers, the function and the instruction of the frame that asked are put back.

The native behind `attempt`, `std.result.protect`, does only that and reports whether the function completed. What the function returned, or how it failed, is fetched by two more natives, and the `Result` and the `Failure` are built by Qoqi code in `std/result.qi`: values of enums and structs are made by the compiler's own code, which knows what each must hold.

**`result.stack`** is the same call stack, asked for without a failure.
