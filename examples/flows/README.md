# Flows to try

Three programs of flows, each with its tests:

| File | What it is |
|------|------------|
| [shop.qi](shop.qi) | An order holds the goods, has the payment made by a flow of its own, then books a van. Undos, a step asked to run again, a step never run twice, a child flow, cancelling |
| [approvals.qi](approvals.qi) | An expense waits for a manager's word, and goes one level up when nobody answers. A manager answers through the expense's inbox |
| [subscriptions.qi](subscriptions.qi) | A fourteen-day trial, then a subscription billed each month: nudges and reminders on their day, a declined card tried again a day apart, a change of plan, a subscription that outlives its trial. A day lasts what the instance says, so a month can pass in a minute and a half |

## Their tests

```text
$ qoqi test examples/flows
```

Each test runs an instance in a trial, on a store in memory and a clock that moves only when the test says so: two days of nobody answering pass in a moment. `everyCrash` runs a scenario again with the process stopped at each write the instance makes, and checks that it always ends the same.

## The daemon

From a folder of your own, where the store goes, one daemon for both programs:

```text
$ qoqi serve examples/flows
qoqi: Expense, Payment, Order, Signup, Subscription, on flows.db, at http://127.0.0.1:7070; qoqi ui opens the console
```

Each program has its tables in `flows.db`, named after it; four workers run instances side by side (`--workers` says how many). One program alone is served as `qoqi serve examples/flows/shop.qi`.

In another terminal, from the same folder:

```text
$ qoqi ui                                   # the pages, in a browser
$ qoqi start Order order:1 '{"cart": {"id": "1", "customer": "ada@example.com", "amount": 4200, "city": "Lyon"}}'
$ qoqi ls
$ qoqi result order:1 --wait 10s
```

What to try, by the cart:

| Cart | What happens |
|------|--------------|
| `"amount": 900000` | The card is refused: the goods are released |
| `"city": "Atlantis"` | Paid, but no van goes there: the money is refunded, then the goods released |
| an `"id"` ending in 7 | The bank is busy the first time: asked again two seconds later, under the same key |
| `qoqi cancel order:1 "changed my mind"` within three seconds of the start | The payment is cancelled first and voids its authorization, then the order releases the goods |

A new version needs no restart: `qoqi deploy examples/flows` sends it to the daemon, or serve with `--watch` and save. `qoqi versions` lists what was deployed, and `qoqi diff shop` what the last version changed.

Kill the daemon (`kill -9`) while an order waits for its payment, and start it again on the same folder: the order goes on where it was, and no step is done twice.

The approvals, served by the same daemon; a manager answers through the expense's inbox:

```text
$ qoqi start Expense expense:1 '{"id": "1", "author": "lin", "amount": 1200, "patience": "1m"}'
$ qoqi send expense:1 answers '{"approved": true, "why": ""}' --key mail-1
```

`qoqi cancel expense:1 "paid already"` withdraws it: the expense hears its own cancellation, tells the manager, and ends `Withdrawn`. Without an answer, the next manager is asked a minute later; `'{"approved": false, "why": "not a business trip"}'` refuses it. The key makes a message sent twice, a mail clicked twice, one answer.

Change the code and serve it again: an instance asleep goes on in the version it started in, and moves to the new one at its next stage, `stage asking` in the approvals, if the stage holds the same variables. `approvals.flow.lock` keeps the shape of each stage; the daemon says what a new version changed of them.

The fields are JSON as a person writes it: the daemon types them from the program, a struct inside them needs no `"$type"`, a variant is written by its name, `"Approved"` or `{"Refused": "why"}`, and a `Duration` as a text with its unit, `"1m"`, `"3s"`, `"1h30m"`. `qoqi ui` gives the same start as a form made from the types.

## Weeks in a few minutes

A trial whose day lasts three seconds: the nudge comes nine seconds in, the reminder thirty-three, and the end of the trial forty-two.

```text
$ qoqi start Signup signup:ada '{"email": "ada@example.com", "plan": "Team", "day": "3s"}'
$ qoqi send signup:ada events '"Activated"' --key evt-1
$ qoqi send signup:ada events '{"UsedFeature": "reports"}' --key evt-2
$ qoqi send signup:ada events '{"AddedCard": "visa-0341"}' --key evt-3
```

At the end of the trial `signup:ada` returns `Subscribed("subscription:ada@example.com")`, and the subscription goes on alone: the first month is paid, the second is declined, tried again each day, three times, and the subscription lapses. With `visa-4242` it is paid each month until it is told otherwise:

```text
$ qoqi send subscription:ada@example.com changes '{"SwitchTo": "Solo"}' --key chg-1
$ qoqi send subscription:ada@example.com changes '{"Cancel": "moving on"}' --key chg-2
```

A trial left alone is nudged, reminded, and expires. `qoqi ls` says what each instance is doing as it goes, and the pages show the line each one waits on.

