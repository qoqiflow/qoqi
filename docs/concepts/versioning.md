# Versions

Instances live longer than the code that started them. A deploy sends a new version to the daemon while instances are running, and each instance moves to it at its next **stage**, when its state there still fits.

```console
$ qoqi deploy approvals.qi
approvals deployed, version 7b02e1c4 in place of 43381189: 12 instances move to it at their next stage
```

## Pinned, until a stage

An instance runs the bytecode of the version it started in. Code changed after a deploy does not reach it in the middle of what it is doing: an instance never runs half of one version and half of another.

It moves to a newer version only at a **stage**:

```qoqi
impl Flow<Decision> for Expense {
    fn run() -> Decision {
        let approver = managerOf(self.author);

        for _ in 0..3 {
            stage asking;

            ...
        }
    }
}
```

`stage name;` is a named place in `run`. When an instance passes it, a newer version of its flow exists, and `run` is its only task alive, it goes on in the newer version, from the same stage.

*   A stage is written in `run` itself, not in a function it calls, and each has its own name (`F0005`); not inside a `defer` (`F0002`).
*   A loop of `run` that waits with no stage in it is warned of (`W0011`): such an instance keeps everything it did since it started, and never moves.

Code changed before a stage has run already, and changing it never breaks an instance. Only what is still to run matters.

## `flow.lock`

At each deploy, the **shape** of every stage is written down: the variables alive there and the fields of the flow, with their types. It goes in `flow.lock`, next to the program (`approvals.flow.lock` beside `approvals.qi`), written by `qoqi deploy`. Commit it with the code.

```json
{
  "format": "qoqi.flow.lock/1",
  "version": "sha256:43381189...",
  "flows": {
    "Expense": {
      "asking": { "approver": "String", "self.answers": "Inbox<Answer>", "self.author": "String", "self.id": "String" }
    }
  }
}
```

| Between two versions | |
|----------------------|--|
| A stage with the same variables of the same types | Instances move on their own |
| A variable added, removed, or of another type; a stage removed | The compiler warns (`F0011`). The daemon refuses the deploy while an instance of the flow is alive, until a migration says what to do |

The daemon compares against the lock it kept with the last deploy, not the file; the file is how the compiler warns before you deploy.

## Migrations

A `migrate fn` of the flow, named after the stage, takes the variables the stage had in the version before and gives the struct of the stage in this one. The compiler gives each stage a struct of its variables, named after it: stage `counting` is `Counting`.

```qoqi
impl Counter {
    // counting now remembers the last value it saw
    migrate fn counting(count: Int) -> Counting {
        return Counting { count: count, last: "" };
    }
}
```

The parameters are the old shape, read from `flow.lock`; the result is checked against the new one (`F0007`). A migration goes from the version just before to its own; an instance that slept through three versions goes through each in turn.

## Steps run in the newest version

A step's body is not bound to an instance's version: a step runs in the newest version of its flow whose signature matches the call. A fix to a step reaches every instance at its next attempt, with no stage in between. Only `run`, and what it calls, are bound to the version.

## Taking a stopped instance up in a new version

A stopped instance is resumed from its last stage, in the newest version, with what its steps gave since then found by their keys ([failures](failures.md#resuming-with-a-fix)). If the newest version has not that stage as it was, and no migration takes an instance there, it cannot be resumed (`F0103`): cancel it, or serve the version it runs.

## Looking at versions

```console
$ qoqi versions
#2   approvals   7b02e1c4   2026-10-18 10:02:11   serving   3 alive   1 stage changed
#1   approvals   43381189   2026-10-17 23:30:38             9 alive

$ qoqi diff approvals          # what the last deploy changed of the source
```

The bytecode of every version is kept in the store, for as long as an instance runs it.
