# Versions

Instances live longer than the code that started them. A deploy sends a new version to the daemon while instances are running. An instance that waits moves to it at once, when the new version would have done what the instance did; the others move at their next **stage**, when their state there still fits.

```console
$ qoqi deploy approvals.qi
approvals deployed, version 7b02e1c4 in place of 43381189: 11 instances moved to it, 1 at their next stage
  exp-2207 (Expense, version 43381189): played again from stage asking, the newest version does something new before it asks again for exp-2207/k7f2/3:0:notify#1
```

## Never half of one version and half of another

An instance runs the bytecode of one version at a time. Code changed after a deploy does not reach it in the middle of what it is doing: an instance never runs half of one version and half of another. It moves to a newer version in two places: while it waits, and at a stage.

### While it waits

When a version is deployed, each instance that waits on an older one is **played again from its last stage, in the new version**, in memory, with nothing leaving. What its steps gave is found by their keys, what it read of the clock and drew at random is given back, and the messages it took since the stage are there to take again. If the new version asks each step for what it was asked, and takes the messages that were taken, what the instance did since its stage is something the new version did: the instance goes on in the new version, and waits there, until the moment it waited for. Played for real by the daemon, nothing leaves twice.

So a fix deployed while an instance waits reaches it before it wakes: the instance that was to send a reminder in two days runs the new code in two days, not the old one. What the new version computes of its own, with no step, it computes again: a change to how a total is counted counts the current pass too.

When the new version would do otherwise, the instance stays on its version until its next stage, and why is said: by `qoqi deploy`, for each instance that stays, and by `qoqi show`.

| Played again from the stage, the new version | |
|----------------------------------------------|--|
| Asks the same steps for the same things, takes the same messages | The instance moves, and waits in the new version |
| Asks the same, then does something new: a step, an end | It moves, and does it |
| Asks a step for something else (`F0102`), does something new before it asks for what was done, or leaves a message untaken | It stays, until its next stage |

A daemon also asks it of an instance on an older version as it takes it up, before it runs it: one that waited while the deploy ran, or that a daemon started again on a new version finds, does not wake into the code of the version before. `qoqi deploy --dry` says which instances would move, and which would stay.

### At a stage

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
*   A loop of `run` that waits with no stage in it is warned of (`W0011`): such an instance keeps everything it did since it started, and moves to a new version only by being played again from its start, all of it.

Code changed before a stage has run already, and changing it never breaks an instance at the stage. Only what is still to run matters.

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
#2   approvals   7b02e1c4   2026-10-18 10:02:11   serving   11 alive   1 stage changed
#1   approvals   43381189   2026-10-17 23:30:38             1 alive

$ qoqi diff approvals          # what the last deploy changed of the source
$ qoqi ls                      # an instance on an older version says which, beside its stage
$ qoqi show exp-2207           # and why it stays there
```

The bytecode of every version is kept in the store, for as long as an instance runs it.
