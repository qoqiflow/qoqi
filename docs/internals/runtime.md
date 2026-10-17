# The Runtime

How the daemon runs flows: the store, the life of an instance, the moments it is written and taken up, the tables. Where this page and the code disagree, the code and its `everyCrash` tests decide.

## How it runs

The daemon, `qoqi serve`, runs the flows of a folder against a store:

```text
$ qoqi serve flows/ --store flows.db --name shop-1
```

It is the scheduler of [tasks](../reference/language/tasks.md), made durable: today the tasks of a program wait in a queue in memory; the tasks of flows wait in the store, and the daemon is the scheduler of all of them. Here is all it does, in order. Every word in `this face` below is a column or a call of [the store](#the-store).

### What an instance can be

| `status` | |
|----------|--|
| `waiting` | In the store, with the waits that can wake it. Nothing to do until one happens |
| `running` | Held by a daemon under a lease: its tasks run, or its steps are under way |
| `stopped` | Not ended, and going nowhere alone: a mistake in the program, a step that asked to be tried again once too often, an undo that failed. A person resumes or cancels it |
| `done` | `run` returned |
| `failed` | It was cancelled and its undos have run, or a person ended it |

And a `phase`, `run` or `finish`: whether the instance is carrying out its `run`, or ending (cancelling its children, running its undos). `done` and `failed` are **ended**: what `run` gave is in `result`, and an instance that waits for this one is woken. A `stopped` instance is not ended: its parent, or anything that waits for it, goes on waiting, and `qoqi ls --status stopped` is where it shows.

Every change of status is one of these, and nothing else changes it:

| An instance is | and | becomes | by |
|----------------|-----|---------|-----|
| new | `start` from outside, or a parent's write | `waiting`, `run`, a wait already over | `create`, or the parent's `save` |
| `waiting` or `stopped`, a wait has happened or `cancel` is set | a daemon takes it | `running` | `lease` |
| `running`, the lease has run out | a daemon takes it | `running`, another owner | `lease` |
| `running` | no task can go on, steps are to leave | `running`, the attempts written | `save` |
| `running` | no task can go on, nothing under way, its next wake far | `waiting`, its waits written, the lease let go | `save` |
| `running` | no task can go on, nothing under way, its next wake near | `running`, its waits written, the lease kept: it sleeps in memory, and is told of a message | `save` |
| `running`, `run` | `run` returned | `done` | `save` |
| `running`, `run` | `cancel` is set, or `run` returned an `Err` and there are undos or children | `running`, `finish`: the children's `cancel` set | `save` |
| `running`, `finish` | the children have ended and the undos have run | `failed` (cancelled) or `done` (an `Err` returned) | `save` |
| `running` | a mistake, a step's attempts spent, an undo gave `Err`, or `flow.stop(question)` | `stopped`, in its phase | `save` |
| `stopped`, `run` | a person | `waiting`, its state that of its last stage, in the newest version, a wait already over | `resume` |
| `stopped`, `finish` | a person | `waiting`, a wait already over | `resume` |
| `stopped` | a person | `failed`, no more undos | `end` |
| ended, past `keep_until`, nothing has it | the daemon | gone | `sweep` |

A `cancel` asked from outside sets a column and makes the instance due; it changes the status only through the row above that reads it. A `cancel` on an instance in its `finish` phase changes nothing: it is ending already.

### 1. The daemon

```text
daemon(folder, store, name):
    me = name + a fresh incarnation id                     # shop-1/7c3e: this process, not the last
    compile the flows of the folder, check them against flow.lock,
    store the bytecode of each version under its hash
    at the same time: heartbeat(), wakeups(), the commands from outside

heartbeat():
    every ten seconds: store.alive(me, now + 30 s)        # the daemons table, for `qoqi daemons`

wakeups():
    forever:
        for each id in store.due(now, 50):
            if s, cancel, ok = store.lease(id, me, now + 30 s); ok:
                start run(id, s, cancel)                   # one goroutine per instance
        sleep until the next `at` of a wait, at most a second,
        or less when a message or a cancellation comes in
```

A **lease** says "mine for thirty seconds, renewed while I run it". Only the daemon that holds it runs the instance, and the store refuses a write of the instance's row from any other (the attempts are not under it: [4](#4-a-step-ends)); one that stops lets it run out, and `due` then offers the instance to any daemon, whatever its status was. Several daemons can share one store once the store is a database server; a file of SQLite is on one machine, so with it there is one daemon, and the lease matters when it restarts.

A daemon is known by its **incarnation**: its name and an id drawn at each start, and an attempt is written under the incarnation that sent it. Nothing is concluded from a heartbeat that is over: a paused process and a dead one look the same from outside, and only `until` says when a step may be given up. The heartbeat is for people (`qoqi daemons`), and an incarnation that comes back after a pause finds its leases gone and its writes refused, as it should.

### 2. Taking an instance up

```text
run(id, state, cancel):                                    # state: what lease gave, or memory
    vm = thaw(the bytecode of state.version, state)
    renew the lease every ten seconds while it runs; refused, drop the instance from memory
    (its steps under way finish writing their results, which are not under the lease)
    if cancel is set and state.phase is run: cancelled(vm, cancel); return   # in finish, cancel is noise
    if state.phase is finish:                              # taken up while ending: its undos are steps
        the tasks at a step below are its undos; then finish(vm); return

    for each task t stopped at a step, with key k:
        a = store.attempt(k)                               # the last attempt under k
        a.result is a value         ->  give it to t; t can go on
        a.result is NULL            ->  wait until it is not, or a.until is past; then giveUp(a)
        a.result is "retry"         ->  attempts left: t waits `retry` until a.until + a.delay
                                        none left: stopped(vm, "charge gave no answer in 100 attempts")
        a.result is "timeout"       ->  Settled: give Unknown to t; else as "retry"
        a.result is "failed"        ->  stopped(vm, the failure): a mistake in the step's body
                                        (a resume deletes such attempts, so the fixed step runs as a new one;
                                         for a Settled step it leaves it, and Unknown is given: the body may
                                         have failed after its request left)

    for each task t at a wait:
        e = the first of its waits that has happened, by the time it did:
            a message by its seq, a time by its at, an instance by when it ended
        if there is one:
            give (its case, its value) to t; t can go on
            (a message it took is marked taken with the next write)

    turn(vm)
```

`lease` is one statement: it takes the instance if it is `waiting` or `stopped` and due, or `running` under a lease that has run out, and gives back its state and `cancel` ([the store](#the-store)). A step under way belongs to its attempt, not to the lease: a daemon that lost the instance but whose step still runs may still answer before `until`, and the daemon that took the instance waits until then. So an instance whose daemon died in the middle of a wave is taken up like any other: its attempts are rows, under way or given up, and the daemon that takes it goes on from them.

A step's limit is written in its attempt before the step leaves: `until`. The daemon that sends it gives it up then. A daemon that takes the instance up finds the attempt without a result and waits for `until`: before it, the first daemon may still be running the step, and a second attempt that asks the bank first would find nothing yet and send the money a second time. After it, `giveUp` settles who was first ([4](#4-a-step-ends)): the row is written before anyone acts on it. The limit bounds the daemon's patience, not the provider's: a step that asks first on its second attempt sets its limit past the longest the provider may take, or its first request may still be on its way when it asks.

One window stays open, in this design as in any: a daemon that wrote the attempt, then froze before sending, and wakes past `until` to send after all. The daemon checks its lease just before a request leaves, which closes all of it but a freeze between that check and the send; `flow.key()` makes the late request the first at a provider that takes keys, and that is why a `Settled` step is "not run again by the daemon" and not "never twice in the world".

### 2b. Taking a stopped instance up

```text
resume(id):                                                # a person asked; `resume` did this in the store:
    phase finish  ->  status waiting, a wait already over; the undo attempts that gave Err deleted,
                      so that finish(vm) runs them again and finds the rest by key
    phase run     ->  state = at_stage, version = the newest, status waiting, a wait already over,
                      the messages taken since at_stage untaken   # UPDATE messages SET taken = NULL
                                                                  # WHERE taken_pass >= at_stage.pass

run(id, ...), as for any instance:
    vm = thaw(state), through each migration between its version and the newest, if any
    turn(vm), right after the stage, in the same pass: every step and wait asks store.attempt(key) first,
    as always (3), so a replay that sleeps and wakes is an instance like any other:
        a value, the same `asked`   ->  given what was kept; nothing leaves, nothing is waited for
        a value, another `asked`    ->  stopped(vm, F0102)
        nothing                     ->  as always; a dry run (below) ends here, before anything leaves
    at the next stage, or the end, for any instance:
        attempts of this pass with a value that nothing took  ->  stopped(vm, F0101)
        (nothing is marked: an attempt is untaken when its count
         is past the task's counter for that name, in the state)
```

A stopped instance whose stage no longer exists in the newest version is not resumed through a migration to another stage: what was done since the old stage was done on its path, and the new stage's keys would be another path's. It stays `stopped`, with `F0103` and the two stages named, until a person cancels it or pins its version and fixes that. An instance resumed is an instance whose state is its stage's, in the newest version (the JSON says which, so a daemon that dies between `lease` and its first `save` leaves nothing untrue), and the rules above hold for every instance. `qoqi resume` first runs **dry**: the daemon runs the instance in memory without a lease to write under, up to the first step that would leave or the first wait that would wait, and shows what it took by key and what it would do next; `--go` then resumes for real. A person reads what the new code would take and what it would do, and lets it go on or not.

This is the one place an instance is played again. What was done since the stage is found by its key, and what is new runs; what was done and is no longer asked for stops it, with the list ([taking up a stopped instance](../concepts/failures.md#stopped-instances)). `asked` is what the step was given, as JSON, read into the newest shape of its types as any kept value is (a field the JSON lacks takes the default its struct declares, and a field with no default stops the instance with `F0102`), then compared, or for a wait its channels and cases in order, without its lengths of time: the new code asking `charge#1` for another invoice, or reading the cases of a `wait match` in another order, is not the same question, and gets no silent answer. A step whose attempts were spent gets new ones, numbered on from the last.

### 3. Turns: everything that can go on, goes on

```text
turn(vm):
    forever:
        while a task can go on:
            t = the next one that can
            run the bytecode of t until it:
                calls a step       ->  store.attempt(key) first: a value asked the same is given,
                                       as 2b says, and nothing is noted; else note (t, key, args, limit); t is stopped
                waits              ->  if what it waits for is there, go on; else t is stopped
                passes a stage     ->  see 5
                starts a task      ->  name it after t and its function; it can go on
                starts an instance ->  note it, to be created with the next write
                ends               ->  wake the tasks of the instance that wait for it
                fails              ->  the failure is given to whoever waits for t;
                                       if t is run: stopped(vm, failure); return
                (a step's body that fails writes "failed" under its attempt first)

        if run has ended: finish(vm); return

        new = the steps noted since the last write
        if new is empty and no step is under way:
            if no wait can be woken from outside: stopped(vm, R0009 deadlock); return
            far = its next wake is more than 30 s away
            cancel = store.save(id, me, freeze(vm), release = far)   # waiting if far, else still running
            if cancel and phase is run: cancelled(vm, cancel); return
            if far: leave memory; return
            look once more at messages and cancel, in case one landed during the save
            (store.messages(id): the messages not taken, which a sleeper in memory reads when told);
            then wait in memory for its next wake, a message or a cancellation
            told to this daemon, renewing the lease; continue

        if new is empty:                                   # only steps under way
            wait until a step ends or a wait is woken; continue

        if steps are under way and the first of new was noted less than 10 ms ago:
            wait until a step ends, a wait is woken or the 10 ms are over; continue

        cancel = store.save(id, me, freeze(vm))            # before anything leaves: the attempts
        if cancel and phase is run: cancelled(vm, cancel); return   # the attempts of new are rows now
        for each step in new:
            run it in the background, without the turn, given up at its until

        wait until a step ends or a wait is woken; continue
```

Without the ten milliseconds, a `task.map` over fifty invoices whose answers come one by one would write the whole instance once per answer. With them, the answers of ten milliseconds make one wave, and one write: a step that would leave at once leaves at most ten milliseconds later. When nothing else is under way, nothing more can come, and the write is not put off.

Nothing leaves the program before `save`: that is the one thing the algorithm guarantees, and what lets it take an instance up without playing anything again. `save` also gives back a cancellation asked for meanwhile: an instance learns of it at the latest at its next write.

### 4. A step ends

```text
stepEnded(t, key, attempt, result):
    kept = store.putResult(key, attempt, result)           # UPDATE result WHERE result IS NULL
    if kept is "timeout" or "retry":                                 # too late: another daemon, or a retry, went on
        store.late(key, attempt, result)                   # kept aside, for people; t is where giveUp put it
    else: give kept to t; t can go on                      # what was written first stands

giveUp(key, attempt, delay):                               # until is past, or flow.retry was said
    gaveUp, kept = store.giveUp(key, attempt, delay)       # UPDATE result = "timeout" or "retry", until = now, delay
                                                           #        WHERE result IS NULL AND (until <= now,
                                                           #        unless flow.retry said so from inside the step)
                                                           #        WHERE result IS NULL
    if not gaveUp and kept is a value: give kept to t; t can go on   # an answer came first
    if not gaveUp and kept is "timeout" or "retry": return             # another daemon gave it up first: t is theirs now
    else:
        returns Settled, until was past   ->  give Unknown to t; t can go on
        attempt < the daemon's tries      ->  t waits `retry` until now + delay; then it is noted as new
        otherwise                         ->  stopped(vm, the reason)

working(key, attempt) -> Bool:                             # flow.working(), between two requests of a step
    return store.attempt(key, attempt).result is NULL      # false: the step was given up; stop what you do
```

An attempt is a row, `(key, attempt)`, made by the `save` before the step leaves, with the incarnation that sends it and its `until`. Two writes can touch it, and each says `WHERE result IS NULL`: the first to land wins, and the others read what landed. So a late answer finds `given up` and is kept aside, not given; a `giveUp` that comes after the answer finds the answer and gives it; `Unknown` is never given while an answer can still arrive. The limit is the step's declaration, `within d`, and nothing moves it; `flow.working()` reads the row, and gives `false` once the step was given up, so that a step that makes several requests checks it between two, so that it does not go on acting beside its own second attempt. A step that does not check may: that is the one way two attempts of a step overlap, and `flow.key()` is what keeps the provider from counting them twice. A late answer is not lost to people: `qoqi pkg show` lists it next to the `Unknown` the flow was given, since it is what the world did. Between two tries the instance is `waiting`, with a `retry` wait and its `at`: a flow that retries for an hour is not in memory for an hour.

### 5. A stage

```text
stage(vm, name):
    the instance is at the stage name, pass + 1            # for flow show, and the keys
    if run is the only task alive:
        keep the stage's variables as `at_stage`, with the next write
        the attempts of the passes before go with that write: dropped
        if a newer version of the flow exists and this one is not pinned:
            variables = the stage's variables
            for each version from this one to the newest:
                if the stage's shape changed: variables = that version's migration(variables)
            store.save(id, me, the instance at the stage, with variables, in the newest version)
            vm = thaw(the newest version's bytecode, at the stage, with variables)
            (at a stage, the pass has no attempts yet; a migration to another stage
             changes nothing there. A resume is different: see 2b)
    go on
```

While other tasks of the instance are alive, they have calls under way in the version they started in: the instance stays in that version until a stage where `run` is alone.

### 6. The end

```text
finish(vm):                                                # run returned, or cancelled(vm)
    if run returned, and not an Err:
        store.save(done, `result` = what it returned, `keep_until` = now + keep)

    else:
        if phase is run:
            store.save(phase = finish, `result` = the Err, or the cancellation and its reason)
                                                           # in that transaction: `cancel` set on
                                                           # every child not ended; an `all` wait on them
        wait for the children to end                        # as any wait: the instance may sleep here
        for each step given to flow.undo, the last first:  # a step as any: key undo#n, its attempts
            Ok(_)      ->  the next
            Err(e)     ->  stopped(vm, "release could not undo: " + e); return
                           # the one answer that is not final: a resume runs undo#n
                           # again, as a new attempt, since a person said the world changed
        store.save(cancelled: failed; an Err from run: done; `result` as written above)

    # the write that ends it also takes one off the parent's all wait,
    # and makes the instances that wait for this one due
    give the lease back

stopped(vm, why):                                          # nothing more will happen alone
    store.save(stopped, in its phase, freeze(vm) as it is now,
               `stopped_by` = why, the line and the calls, release = true)
    give the lease back                                    # a person: resume (2b), cancel (8), end

cancelled(vm, reason):                                     # found at a lease or a save, in phase run
    if the flow listens (a case for flow.cancelled() in its run), and cancel is not --now:
        the reason is given to the next `wait match` that has a case for it;
        such a flow has that case in every `wait match` of its `run` (F0006), so there is always one;
        `cancel` is cleared with that save (WHERE cancel = :seen); turn(vm) goes on as usual
    else:
        no new step leaves; every task stops where it is
        the steps under way end, or reach their until, and their results are kept,
        the undos they registered with them
        a step that reached its until with no answer: stopped(vm, "reserve may have acted"), in phase finish
        finish(vm)
```

What waits for an ended instance gets a `Result<T, Cancelled>`. `Ok` holds what `run` returned, an `Err` of its own among them: an `Order` that returned `Err(Problem.Unpaid)` is `done`, its undos have run, and whoever waits for it gets `Ok(Err(Problem.Unpaid))`, to match on as the flow's own type says. `Err(Cancelled { reason })` is the one way an instance ends without a value of its own; an instance that is gone, swept while something still held an `Instance<F>` of it in a variable, gives `Err(Cancelled { reason: "gone" })`. A `Failure` is for a task that panicked; a flow's ending is never that. A `stopped` instance gives nothing: whoever waits for it waits on, and `flow ls` shows both.

A parent that returns `Ok` with children still running leaves them running: they are its children for sweeping, nothing else. A flow that starts a name another flow already started gets that instance, and is not its parent: it can wait for it, and its own ending does not cancel it. A parent that ends otherwise cancels them first. A child `stopped` in its `run` phase is cancelled like any other: it ends, its undos run from the state it stopped with. A child `stopped` in its `finish` phase, an undo of its own having failed, cannot end alone, and holds its parent in `finish` until a person resumes or ends the child; `flow ls` shows the chain.

An undo whose step returns `Err` has not undone: the instance stops, with the undos left shown, rather than ending as if it had. A person fixes the world and resumes, or ends it as it is (`qoqi end`, which runs no more undos). On a resume, that `Err` is the one answer a step gave that is not final: the undo runs again as a new attempt, because a person said the world has changed; every other step finds its key and is not run again.

### 7. A message from outside

```text
send(id, channel, key, value):
    in one transaction:
        if the instance has ended:                                        Err(Ended)
        if a message of that key was ever sent to this channel:           Ok(Already)
        if its messages not taken, and `inside`, fill the channel:   Err(Full)   # the table decides, alone
        add the message; if the instance is waiting, it is due
    tell the daemon that holds the lease, if any, or any daemon (the same process; LISTEN/NOTIFY on Postgres)
    Ok(Delivered)
```

A send from outside never waits: an application answering a webhook gets `Err(Full)` at once, and can tell the provider to try again later. A send may name the instance's `birth`, which `start` gave: a message meant for an instance that is gone then gets `Err(Ended)` instead of reaching a namesake started since. The key is the sender's: a provider that delivers a webhook twice (`evt_9f2` both times, a week apart or not) puts one `Paid` in the channel, not two, because a message is kept, marked taken, for as long as a sender may repeat it: `--keep` again, thirty days, and then the sweeper deletes it; a subscription of three years holds a month of webhooks, not three years, and a sender that repeats after a month is sending again. Without a key, every send is a message.

What comes from outside is taken into the channel at the instance's next write, behind the values already in it from inside, and from then on it is an ordinary value of the channel: the order of a channel is the order things were put in it. A message that lands as the instance ends, between its last turn and its last write, stays untaken: the sender was told `Delivered`, which was true, and `flow show` lists it with the ended instance.

### 8. A cancellation

```text
cancel(id, reason):
    in one transaction:
        if the instance has ended:   Err(Ended)
        `cancel` = reason; if waiting or stopped, it is due
    tell the daemon that holds it, if any
    Ok
```

A cancellation does not wait for the instance, wherever it runs: it is noted in its row, and the daemon that holds the lease finds it at its next write or lease, or at once when it is told; the `save` that acts on it clears it (`cancel = NULL WHERE cancel = :seen`), so that a second one asked meanwhile is not lost, and a `save` in the `finish` phase ignores it. A step under way is not stopped halfway: the world was told, so its answer is kept, and with it the undo it registered ([several systems, undone in reverse](../concepts/failures.md#undo)). A step given up at its `until` after a cancellation is not tried again; what it did and the undo it would have registered are unknown with it, and the instance does not end over that: it is `stopped` in its `finish` phase, the step named as one that may have acted, for a person to look at and then `resume` or `end`. A flow that wants to know it is being cancelled waits for it: a flow whose code has `case let reason = flow.cancelled():` anywhere is one that handles its own cancellation, and the compiler knows it. For such a flow, every `wait match` of its `run` has the case (`F0006` otherwise), and a cancellation is a value: it is delivered at the next `wait match`, and until then and after, its tasks go on and its steps leave, until `run` returns. One that is `stopped`, or waits elsewhere than in `run`, is cancelled as a flow that does not listen: stopped where it is, its undos run; `qoqi cancel --now` does the same to a listening flow that will not end. It then ends as any run does: `done` with its value, and an `Err` from `run` runs the undos as an `Err` always does. A flow with no such case is stopped where it is, and its undos run. Which of the two a flow is does not depend on the line the cancellation finds it at. An instance already in its `finish` phase is ending: a cancellation then is `Ok`, and changes nothing.

### Two days, from end to end

1.  `turn`: task `0` of the approval reaches its `wait match`, and nothing is there. No task can go on, no step is under way: `save`, `waiting`, with two waits (a `receive` on channel 1, and an `after` two days on).
2.  Its next wake is far: the lease is given back, and the instance leaves memory.
3.  Three hours later, `send("expense:4411", 1, "click-77", Approved)` adds the message: the instance is due.
4.  `wakeups`: `due` gives it, and a daemon takes the lease.
5.  `run`: `thaw`, then the first of its waits that has happened is the message: (case 0, `Approved`) goes to task `0`.
6.  `turn`: `return d`, `run` has ended, `finish`: `done`, and the message is marked taken with the last write.

### Three payments, and a crash

`Retries` runs `retryInvoice` on three invoices, each of which calls the step `charge`, then the step `record`. Its birth is `k7f2`, and it has passed no stage:

1.  Tasks `0/retryInvoice#1`, `#2` and `#3` each reach `charge`; task `0` waits for them. One write: the three attempts `retries-1/k7f2/0:0/retryInvoice#1:charge#1` and so on, each with `until` a minute on, under `shop-1`. Then the three requests leave.
2.  The answer for `#2` comes first: `putResult(..., Ok(...))`. Task `#2` goes on to `record`: a write, with that attempt, then its request leaves.
3.  The answer for `#3` comes: `putResult(...)`.
4.  The process stops.
5.  `shop-1` starts again, as a new incarnation. The instance is `running` under a lease that has run out: `due` offers it, `lease` takes it. Two attempts are open, `#1`'s `charge` and `#2`'s `record`: the new incarnation cannot know they died with the old one rather than hang in a paused one, so it waits for their `until`, and gives them up then.
6.  `#1` waits `retry` a second; then the second attempt of `charge` is noted, written, and leaves with the same key: the payment provider knows the key, and answers as the first time. `#2` the same for `record`. `#3` is at `charge`, and finds its result: it is not run again.

A restart costs a step's limit, a minute by default, once. Any other daemon taking the instance up at 5 does the same.

## The store

The first store is a file of SQLite; a server of Postgres or MySQL is the same tables, in the types each has ([sql_schema.go](../../internal/flowrt/sql_schema.go)), every write there a serializable transaction. Every table below is named in the algorithm above, and nothing the algorithm reads or writes is outside them.

```sql
CREATE TABLE instances (
    id          TEXT PRIMARY KEY,
    birth       TEXT NOT NULL,                -- k7f2: drawn at the start, never given again; in every key
    flow        TEXT NOT NULL,
    version     TEXT NOT NULL,                -- the hash of its bytecode
    parent      TEXT,                         -- the instance that started it; NULL if detached
    status      TEXT NOT NULL,                -- waiting, running, stopped, done, failed
    phase       TEXT NOT NULL,                -- run, finish
    since       INTEGER NOT NULL,             -- when the status was set
    stage       TEXT,                         -- the last stage passed
    pass        INTEGER NOT NULL,             -- how many stages passed
    state       TEXT NOT NULL,                -- the JSON of what is kept
    at_stage    TEXT,                         -- the same, at its last stage where run was alone
    result      TEXT,                         -- ended: what run gave, or the cancellation
    stopped_by  TEXT,                         -- stopped: why, the line and the calls
    cancel      TEXT,                         -- a reason, once a cancellation was asked for
    lease_owner TEXT,                         -- an incarnation; NULL when no daemon holds it
    lease_until INTEGER,
    keep_until  INTEGER                       -- ended: when it may be deleted
);
CREATE INDEX instances_parent ON instances(parent);

CREATE TABLE daemons (
    incarnation TEXT PRIMARY KEY,             -- shop-1/7c3e
    alive_until INTEGER NOT NULL              -- its heartbeat
);

CREATE TABLE attempts (                       -- every step and wait: what it gave, by key
    key      TEXT NOT NULL,                   -- retries-1/k7f2/0:0/retryInvoice#2:charge#1
    attempt  INTEGER NOT NULL,                -- 1, 2, ...; a wait has one
    instance TEXT NOT NULL,
    pass     INTEGER NOT NULL,                -- dropped once the instance passes a later stage
    asked    TEXT NOT NULL,                   -- the arguments, as JSON; a wait's channels and cases, not its times
    owner    TEXT,                            -- the incarnation it left from
    until    INTEGER,                         -- its limit; once given up, when
    delay    INTEGER,                         -- given up by flow.retry: how long it asked to wait
    result   TEXT,                            -- NULL under way; or what it gave; or how it ended without one:
                                              -- "timeout" (past until), "retry" (flow.retry), "failed" (a panic in the body)
    late     TEXT,                            -- an answer that came after it was given up
    PRIMARY KEY (key, attempt)
);
CREATE INDEX attempts_open ON attempts(owner) WHERE result IS NULL;

CREATE TABLE waits (                          -- what can wake an instance
    instance TEXT NOT NULL,
    task     TEXT NOT NULL,
    kase     INTEGER NOT NULL,
    kind     TEXT NOT NULL,                   -- receive, send, after, retry, flow, all, cancelled
    channel  INTEGER,                         -- receive, send
    other    TEXT,                            -- flow: the instance waited for
    left     INTEGER,                         -- all: the members not ended; never rewritten
    any      INTEGER,                         -- all: 1 wakes at the first member cancelled (task.all), 0 waits for all (task.settle)
    at       INTEGER,                         -- after, retry: when it is over
    PRIMARY KEY (instance, task, kase)
);
CREATE INDEX waits_at ON waits(at);

CREATE TABLE members (                        -- the instances an all waits for
    instance TEXT NOT NULL, task TEXT NOT NULL, kase INTEGER NOT NULL,
    child    TEXT NOT NULL
);
CREATE INDEX members_child ON members(child);

CREATE TABLE channels (                       -- so that send never opens a state
    instance TEXT NOT NULL, channel INTEGER NOT NULL,
    name     TEXT NOT NULL,                   -- the field: decisions
    capacity INTEGER NOT NULL,
    inside   INTEGER NOT NULL,                -- values in the channel at the last write, from inside or taken in
    PRIMARY KEY (instance, channel)
);

CREATE TABLE messages (                       -- sent from outside; kept --keep after taken
    seq      INTEGER PRIMARY KEY,             -- in the order they came
    instance TEXT NOT NULL, channel INTEGER NOT NULL,
    key      TEXT,                            -- the sender's, if any
    value    TEXT NOT NULL,
    taken    INTEGER,                         -- when the instance took it in; NULL until then
    taken_pass INTEGER,                       -- in which pass: a replay untakes those since its stage
    UNIQUE (instance, channel, key)
);

CREATE TABLE versions (
    hash     TEXT PRIMARY KEY,
    flow     TEXT NOT NULL,
    previous TEXT,                            -- the version this one was built after, from flow.lock
    bytecode BLOB NOT NULL                    -- run, its steps, and its migrations
);

CREATE TABLE pins (                           -- versions whose instances stay on them
    flow     TEXT NOT NULL, version TEXT NOT NULL,
    PRIMARY KEY (flow, version)
);
```

**What is due** is read from these alone, never from a state:

```sql
SELECT i.id FROM instances i
WHERE (i.lease_owner IS NOT NULL AND i.lease_until < :now)            -- its daemon died holding it, or let it lapse
   OR (i.lease_owner IS NULL AND i.status = 'waiting' AND EXISTS (
          SELECT 1 FROM waits w WHERE w.instance = i.id AND (
              w.at <= :now
           OR w.left = 0
           OR (w.any = 1 AND EXISTS (SELECT 1 FROM members x JOIN instances c ON c.id = x.child
                                     WHERE (x.instance, x.task, x.kase) = (w.instance, w.task, w.kase)
                                       AND c.status = 'failed'))
           OR EXISTS (SELECT 1 FROM messages m WHERE m.instance = i.id AND m.channel = w.channel AND m.taken IS NULL)
           OR EXISTS (SELECT 1 FROM instances c WHERE c.id = w.other AND c.status IN ('done', 'failed')))))
   OR (i.lease_owner IS NULL AND i.status IN ('waiting', 'stopped') AND i.phase = 'run' AND i.cancel IS NOT NULL)
LIMIT :limit;
```

A `stopped` instance is due only for a cancellation; its waits, if it has any, do not wake it. An instance in its `finish` phase is woken by its waits (its children ending, an undo's retry), not by a cancellation.

**`lease`** is one statement, and takes the instance only in the states `due` names:

```sql
UPDATE instances SET lease_owner = :me, lease_until = :until, status = 'running', since = :now
WHERE id = :id
  AND ((lease_owner IS NOT NULL AND lease_until < :now)
    OR (lease_owner IS NULL AND status IN ('waiting', 'stopped')))
RETURNING state, cancel, phase;
```

**`save` is one transaction**, and it is the only write of an instance's row besides `lease`, `renew`, `cancel`, `resume` and `end`. It is refused once the lease is over, and gives back `cancel`:

```sql
UPDATE instances SET state = :state, status = :status, phase = :phase, stage = :stage, pass = :pass,
                     lease_owner = CASE WHEN :release THEN NULL ELSE lease_owner END, ...
WHERE id = :id AND lease_owner = :me AND lease_until > :now
RETURNING cancel;
```

In the same transaction, from the snapshot:

| | |
|--|--|
| The waits | `DELETE` the instance's rows that are not in the snapshot, `INSERT` the new ones; an `all` still in the snapshot is left as it is, `INSERT OR IGNORE`, and `left` is never written from the instance |
| A new `all` | `INSERT` its `members`, and its `left` = how many of them are not ended now |
| The messages taken | `UPDATE messages SET taken = :now, taken_pass = :pass` |
| The new steps | `INSERT INTO attempts (key, attempt, instance, pass, owner, until)`, `result` NULL |
| What the waits gave | `INSERT INTO attempts (key, 1, ..., result)` |
| The undos | In `state`, under `"undo"`: those registered in `run`, and those a step's result carried, copied there by the first `save` after the result came; `attempts` is not where an undo lives, only where it arrives |
| The channels | `INSERT OR REPLACE INTO channels` |
| New instances | `INSERT INTO instances` (`waiting`, `parent` = this one, or NULL for `flow.detach`), each with a wait `after` already over |
| At a stage, `run` alone | `at_stage` = the state there; `DELETE FROM attempts WHERE instance = :id AND pass < :pass` |
| Into `finish` | `result` = the `Err` or the cancellation, so that it survives a week of waiting for children; `UPDATE instances SET cancel = 'its parent ended' WHERE parent = :id AND status NOT IN ('done', 'failed') AND cancel IS NULL`; an `all` wait on them |
| Ended | `result`, `keep_until`, `lease_owner` NULL; `UPDATE waits SET left = left - 1 WHERE (instance, task, kase) IN (SELECT instance, task, kase FROM members WHERE child = :id)` |
| Asleep, its wake far | `lease_owner` NULL: `due` may offer it |
| Asleep, its wake near | `status` stays `running`, `lease_owner` kept: it stays this daemon's, in memory; `due` passes it by until the lease runs out, and `send` and `cancel` tell its daemon ([7](#7-a-message-from-outside)) |

A daemon that stopped for a minute (its machine paused, its collector ran) and comes back finds its `save` refused, drops the instance from memory, and lets the one that holds the lease now go on. The two writes of an attempt (`putResult`, `giveUp`) are not under the lease: each is `WHERE result IS NULL`, and the first to land stands, whoever ran the step ([4](#4-a-step-ends)).

**`resume`** and **`end`** are a person's, on a `stopped` instance with no lease. `resume` sets `waiting` and a wait `after` already over, so that `due` finds it; it deletes the attempts that ended `failed`, except those of `Settled` steps, so that the fixed steps run again; for an instance stopped in its `finish` phase it marks the attempts of the undos that gave `Err` as `retry`, so that they run again as a next attempt, and `flow.attempt()` tells them so; for an instance stopped in its `run` phase it also sets `state` = `at_stage`, `version` = the newest, and untakes the messages taken since: the daemon that takes it up then migrates it on the way, as it would at a stage, and the first `save` writes the migrated state. `end` ends the instance as it would have ended had its undos run: `done` with the `Err` that `run` returned, or `failed` for a cancellation, `result` as written when it entered `finish`, and a note that a person ended it; it sets `cancel` on its living children as an ending `save` does, and takes one off the parent's wait.

**Sweeping.** Now and then the daemon deletes the ended instances past their `keep_until` that nothing has: no `waits.other`, no `members.child`, no living child (`parent`). An `Instance<F>` kept in a variable is not seen by it, and a `wait` on it after that gives `Err(Cancelled { reason: "gone" })`. Their rows in every table go with them, their messages and attempts too, and their names are free. It also deletes the messages of living instances taken more than `--keep` ago, and forgets `daemons` whose heartbeat is long over.

A store on another database, Postgres first, is the same tables; its daemons are told of a message or a cancellation at once (`LISTEN`/`NOTIFY`) instead of on their next look, and the clock is the database's `now()`. Two counts are read and then written, `left` of a new `all` and the fullness of a channel: with several writers they lock the rows they count (`SELECT ... FOR UPDATE` on the children's rows in `instances`, on the channel's row), so that a child ending or a message landing between the count and the write is counted; `due` reads with `SKIP LOCKED`, so that daemons do not queue on one another.

## What Go provides

Three pieces, each behind an interface, and the names are those of the algorithm.

**The virtual machine** freezes an instance and thaws it, and asks the runtime of flows what to do at the moments above:

```go
// Freeze writes an instance in the format qoqi.flow/1. It is called only
// when no task runs bytecode: each is at a wait, or at a step.
func (vm *VM) Freeze() (Snapshot, error)

// Thaw takes an instance up on the bytecode of its version.
func Thaw(code *Chunk, s Snapshot, host Host) (*VM, error)

// Host is what the virtual machine asks of the runtime of flows.
type Host interface {
	// No task can go on: write the instance; then these steps may leave.
	Still(vm *VM, s Snapshot, release bool) (cancel string, err error)

	// The last attempt under a key: under way, given up, or its result.
	// Taking up, and playing again from a stage, ask it before each step and wait.
	Attempt(key string) (a Attempt, found bool, err error)

	// A step ended, was given up, or answered too late.
	// The host knows which incarnation it is, so none of these says it.
	PutResult(key string, attempt int, result []byte) (kept []byte, err error)
	GiveUp(key string, attempt int, delay time.Duration) (gaveUp bool, kept []byte, err error)
	Late(key string, attempt int, result []byte) error
	Working(key string, attempt int, until time.Time) (moved bool, err error)
}
```

**The store** (`internal/flow`), with SQLite as its first form (`internal/flow/sqlite`):

```go
type Store interface {
	Create(inst Instance) (created bool, err error)               // a name taken gives created = false; at_stage = the state at start
	Lease(id, me string, until time.Time) (s Snapshot, cancel string, ok bool, err error)
	Save(id, me string, s Snapshot, release bool) (cancel string, err error)   // one transaction; ErrLeaseLost
	Due(now time.Time, limit int) ([]string, error)

	Attempt(key string) (Attempt, bool, error)                    // the last attempt under key
	PutResult(key string, attempt int, result []byte) (kept []byte, err error)
	GiveUp(key string, attempt int, delay time.Duration) (gaveUp bool, kept []byte, err error)
	Late(key string, attempt int, result []byte) error
	Working(key string, attempt int, until time.Time) (moved bool, err error)
	Untake(id string, sincePass int) error                        // a replay: its messages back

	Alive(me string, until time.Time) error                       // the heartbeat, for people
	Renew(id, me string, until time.Time) error                   // the lease, every ten seconds
	Pin(flow, version string, on bool) error
	Instances(f Filter) ([]Row, error)                            // for ls and the API

	Send(id string, channel int, key string, value []byte) (Sent, error)   // never waits
	Cancel(id, reason string) error                                        // never waits
	Resume(id string) error                                                // stopped -> waiting, due now; from at_stage if in run
	End(id string) error                                                   // stopped -> failed, no more undos
	Sweep(now time.Time) (deleted int, err error)
	Bytecode(hash string) ([]byte, error)
	PutVersion(v Version) error
	Messages(id string) ([]Message, error)                                // not taken yet
	PostResult(key string, result []byte) error                           // a person's answer, as the next attempt
	Daemons() ([]Daemon, error)
}
```

**The daemon** (`qoqi serve`) puts the two together: it beats, loops on `Due`, takes a lease, thaws, and turns. A step's body runs in a virtual machine of its own, on the daemon, given the step's arguments: that is why they are data, and why the instance's own machine is still while its steps are out.

## Decided

*   **The whole state is written each time**, at most once per ten milliseconds while steps are under way. Writing only what changed is for when it is needed.
*   **A step has a time limit**, a minute by default, which its declaration says otherwise, `within d` after its signature, and nothing moves. A daemon that takes an instance up waits for the limit of a step under way before it runs it again.
*   **A step's key names what was asked**, `charge#1`; a new version that adds a step does not move the keys of the others.
*   **A replay is an instance whose state is its stage's**, in the newest version, its messages since untaken; nothing else marks it. `qoqi resume` runs it dry first, and `--go` for real; the `F0101` check at the next stage holds for every instance.
*   **Lengths of time are not in a key**, nor compared: a deadline computed each round asks the same question.
*   **Every step and wait looks for its key before it is noted**, replay or not.
*   **A late answer is not given, and not lost**: an attempt is a row made before the step leaves, and its three writes each say `WHERE result IS NULL`; the first to land stands, and what comes after is kept aside for people.
*   **A daemon is an incarnation**, name and id; what it left under way is given up at its `until`, by whoever holds the instance then, and by nothing sooner.
*   **A stopped instance is written as it is**, not as it was at its last write: a `flow.undo` noted before the mistake is kept.
*   **A sleeping instance keeps its lease while it stays in memory**; `due` offers only what no one holds.
*   **An instance has a status and a phase**, and the table of [what an instance can be](#what-an-instance-can-be) is the whole of its life; every row of it is one store call.
*   **A result found by key is checked against what is asked**: other arguments, other cases, and the instance stops (`F0102`) rather than answer the wrong question.
*   **A task is named as a step is**, by its function and a count, so that a new version that starts one more task renames none.
*   **A send from outside may carry the sender's key**; a second delivery is one message.
*   **An instance is small.** Thousands of things are thousands of instances, which a flow starts in one write and waits for in one wait.
*   **A name is free again once its instance is deleted**, after `--keep`; the keys of the new one never meet the old one's.
*   **Every request to the daemon carries a token**, on `127.0.0.1` too.
*   **The first store is SQLite**, through `modernc.org/sqlite`: SQLite translated to Go, so `qoqi` stays one file with nothing to install.
