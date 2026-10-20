# Failures

A flow fails in three ways, and each is handled differently:

| | What it is | What happens |
|--|------------|--------------|
| **An answer** | A step returned `Err`, `run` decided | The flow handles it like any value; if `run` returns an `Err`, the instance ends `failed` and its undos run |
| **The world did not answer** | A step said `flow.retry(...)` | The step is run again later, under the same key ([steps](steps.md#asking-to-be-run-again)) |
| **A mistake in the program** | An index out of bounds, an `expect` that did not hold, a `panic` | The instance **stops**, for a person; nothing is undone |

## Errors are values

A step that can fail returns a `Result`, and the flow decides on it, as any Qoqi program does:

```qoqi
let charge = charge(order.customer, order.cents)?;
```

`?` hands the failure on: from `run`, it ends the instance with that `Err`. An instance that ends with an `Err` is `failed`, and its undos run.

## Undo

A step that does something which must be undone if the flow fails says so, next to the effect, with `flow.undo(step, argument)`:

```qoqi
step fn reserve(cart: Cart) -> Result<Hold, Problem> {
    let hold = warehouse.hold(cart, flow.key())?;
    flow.undo(release, hold);

    return Ok(hold);
}

step fn release(hold: Hold) -> Result<Void, Problem> { ... }
```

Written in the step, the undo is kept with the step's result, in the same write. When the flow fails, by an `Err` from `run` or by a cancellation, the undos run as steps, **the last noted first**. A flow that ends well forgets them.

An undo that returns an `Err` has not undone: the instance stops, for a person.

A step carried out by your application declares its undo where it is declared: `step fn charge(...) -> ... by payments undo refund;`.

## Cancellation

An instance is cancelled from outside, with a reason:

```console
$ qoqi cancel order:1 "changed my mind"
```

By default it is stopped where it is, its children are cancelled first, and its undos run; it ends `failed`.

A flow that wants to end its own way listens for it, in its `wait match`:

```qoqi
let answer = wait match {
    case let a = self.answers.receive(): a;
    case let reason = flow.cancelled(): {
        tell(approver, "expense ${self.id} was withdrawn: ${reason}");
        return Decision.Withdrawn(reason);
    }
};
```

The reason then comes as a value. A flow that listens must listen in every `wait match` of its `run` (`F0006`), so that a cancellation is never missed between two.

## Stopped instances

An instance **stops** when something needs a person: a mistake in the program, an undo that failed, a step that asked to be run again too many times, a step your application could not account for. Nothing is undone, and nothing more happens on its own. `qoqi ls --status stopped` lists them, with why.

What is written then is the instance as it is: the failing task's place and calls, the others at their waits or steps, the undos noted so far.

| | |
|--|--|
| `qoqi resume name` | Shows what the instance would do, taken up from its last stage in the newest version: what it would take as kept, what it would run next. `--go` does it |
| `qoqi answer name key '{...}'` | Says what a step did, for a step the application could not tell of; then `resume` |
| `qoqi end name [reason]` | Ends it as it is, `failed`, with no more undos: for when a person has put the world right by hand |
| `qoqi cancel name [reason]` | Ends it, running its undos |

### Resuming with a fix

A stopped instance cannot go on from the line it failed at in new code. It goes back to its **last [stage](versioning.md)**, moves to the newest version there, and runs what came after again. What came after has reached the world already, and is not done twice: every step and every wait since the stage kept what it gave under its key, and the new code, run from the same stage, finds it.

*   **A step found by its key, asked the same way**, gets what was kept; nothing leaves.
*   **Asked otherwise**, another argument to the same step, the instance stops with `F0102`: the world answered a question the new code does not ask.
*   **Something kept that the new code never asks for**: when the instance reaches its next stage with results nobody took, it stops with `F0101`. A person decides.

The clock and the random numbers it read since the stage are kept too, for the same reason. The code after the stage runs again minutes or days later, and a step given `"report of ${time.utc(time.now()).format(...)}"`, or a child started as `flow.detach("recovery:${code}:${random.token(3)}", ...)`, would otherwise be asked otherwise and stop it with `F0102`, or start a second child. So every value a flow draws without a step since its stage is written with the instance: `time.now()`, and what reads it (`time.seconds()`, `time.today()`, `time.since(...)`, `time.after(d)`, `30.days().fromNow()`), and `random.below`, `between`, `float`, `uuid`, `token`, and what draws from them (`chance`, `pick`, `shuffle`). The new code, run from the stage, is given them back:

*   **In the order they were read, task by task.** Each task gets what it read, in the order of its own code, whatever turns the tasks take; tasks are told apart by the order they were started in, as their steps' keys are.
*   **As long as it reads them the same way.** A value is given back to the same question: the clock where the clock was read, `token(3)` where `token(3)` was drawn. The first one read otherwise, the clock where a token was drawn, ends what that task is given back: from there, and past what was kept, it reads the clock as it is and draws new numbers.
*   **Until its next stage**, where what was kept is forgotten and the keeping starts again. `time.ticks()` is not kept: it measures how long something takes, and what it gave in another process means nothing.

An instance written before values were kept, by an older daemon, has none to be given back: it is resumed with the clock as it is.

A resume is a dry run first: the daemon runs the instance in memory up to the first step that would leave, and shows it, before `--go` lets it go for real.
