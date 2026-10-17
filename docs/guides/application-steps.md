# Steps in your application

A flow's steps are usually written in Qoqi. Some belong in your application: they need its database, its models, a library only it has. Such a step is declared in the flow, and **carried out by your application**, in Go, PHP or TypeScript. The daemon sends it over HTTP, signed, and keeps what the application answers.

A complete shop, its application written three times, is in [examples/shop](../../examples/shop).

## 1. Declare the step

```qoqi
step fn reserve(order: String, items: [Item]) -> Result<Hold, Problem> by warehouse undo release;
step fn release(hold: Hold) -> Result<Void, Problem> by warehouse;

step fn charge(customer: String, cents: Int) -> Result<String, Decline>
    within 30.seconds() by payments undo refund;
step fn refund(charge: String) -> Result<Void, Decline> by payments;
```

| | |
|--|--|
| `by queue` | The queue that carries it out. The step has no body |
| `within d` | How long the application has, a minute by default. Written before `by` |
| `undo step` | The step that undoes it if the flow fails ([undo](../concepts/failures.md#undo)) |

A step by a queue is a step like any other for the flow: its key, its result kept, `Settled<T>` for one never sent twice.

## 2. Say where the queue is

In `qoqi.json`:

```json
{
    "entry": "orders.qi",
    "queues": {
        "warehouse": { "url": "env:WAREHOUSE_URL", "secret": "env:SHOP_SECRET", "concurrency": 4 },
        "payments": { "url": "env:PAYMENTS_URL", "secret": "env:SHOP_SECRET" }
    }
}
```

`env:NAME` is read from the daemon's environment. A step by a queue the project does not declare is an error (`F0012`). See [configuration](../reference/configuration.md#queues).

## 3. Generate the types

`qoqi gen` writes, from the flow, the types of the steps, an interface for each queue, and the handler that serves it:

```console
$ qoqi gen go  orders.qi --package steps > steps/steps_gen.go
$ qoqi gen php orders.qi --namespace 'App\Flow' --out src/Flow
$ qoqi gen ts  orders.qi > steps.ts
```

`--queue name` writes one queue only. Generate again after a step's signature changes.

## 4. Serve the queue

### Go

```go
import "github.com/qoqiflow/qoqi/pkg/flow"

type warehouse struct{}

func (warehouse) Reserve(ctx context.Context, task *flow.Task, order string, items []steps.Item) (flow.Result[steps.Hold, steps.Problem], *steps.Hold, error) {
    hold := steps.Hold{ID: "hold-" + order}
    // ... hold the stock, under task.Key
    return flow.Ok[steps.Hold, steps.Problem](hold), &hold, nil   // the value, and the undo's argument
}

func main() {
    keeper := &flow.Keeper{DB: db}          // the answers, kept in your database
    keeper.Setup(ctx)

    stock := steps.WarehouseQueue(warehouse{}, os.Getenv("SHOP_SECRET"))
    stock.KeepOnce(keeper)

    http.Handle("/warehouse", stock)
    http.ListenAndServe("127.0.0.1:8090", nil)
}
```

### TypeScript

```ts
import { Keeper } from "@qoqi/flow";
import { listen } from "@qoqi/flow/node";
import { warehouseQueue } from "./steps.ts";

const keeper = new Keeper(db);
keeper.setup();

const warehouse = warehouseQueue(new Stock(db), process.env.SHOP_SECRET!).keepOnce(keeper);
listen({ "/warehouse": (request) => warehouse.fetch(request) }, 8090, "127.0.0.1");
```

`queue.fetch(request)` takes a `Request` and gives a `Response`, so it also serves from any runtime that speaks the Fetch API.

### PHP

```php
use App\Flow\WarehouseQueue;

$keeper = new Keeper($pdo);
$keeper->setup();

WarehouseQueue::make(new Warehouse($pdo), getenv('SHOP_SECRET'))->keepOnce($keeper)->serve();
```

The packages are `github.com/qoqiflow/qoqi/pkg/flow` for Go, `@qoqi/flow` on npm (Node 23.6 or later), `qoqi/flow` on Packagist (PHP 8.4 or later).

## What the application answers

A handler returns one of these; the SDKs have a helper for each:

| Answer | Meaning |
|--------|---------|
| A value | The step's result, kept for good. `Err` included: it is an answer |
| A value with its undo | For a step declared with `undo`: what the undo step will be given. Required with every value that is not an `Err` |
| Retry, with a reason and a delay | The world did not answer; run the step again later, under the same key |
| Later | Accepted; the answer will be posted to the daemon when it is known ([below](#answering-later)) |
| Under way | An earlier attempt of this key is still acting; ask again later |
| Unknown | The application cannot tell what it did. A `Settled` step gets `Unknown`; a plain one stops the instance, for `qoqi answer` |
| Failed | A mistake in the application: the instance stops |

The value is read against the step's return type; one that does not fit stops the instance.

## Running a step once

The daemon may send a step again: after a crash, a timeout, a lost connection. Each request carries the step's **key**, the same for every attempt, and its **attempt** number. Use the key as an idempotency key with a provider, or as a unique column in your own tables.

The SDKs keep answers for you, in your database, inside the transaction of the step's own writes: `KeepOnce` (Go), `keepOnce` (TypeScript, PHP) for a step that writes only to your database, `KeepStarted` for a step that reaches a provider and must be asked before it is sent again. In Go, `flow.TxOf(ctx)` is that transaction.

## Answering later

A step that takes minutes, a batch, a person, answers **later** at once, then posts its answer to the daemon when it has it:

```go
flow.Daemon{URL: daemonURL}.Complete(ctx, task, flow.Value(result))
```

`POST /api/attempts` takes `{"key", "attempt", "token", ...answer}`; the attempt's token is in the task, and is the only authorization needed. An answer that comes after the step's `within` is refused (`410`), and the step is run again or, if `Settled`, gives `Unknown` ([HTTP API](../reference/http-api.md#post-apiattempts)).

## The protocol

The SDKs implement it; this is for writing one.

*   **The request.** `POST <queue url>`, `Content-Type: application/json`, body `{"key", "attempt", "token", "until", "keep", "step", "shape", "args"}`: `args` by parameter name, in the [boundary form](../reference/http-api.md#values); `until` the deadline, in milliseconds since the epoch; `shape` a hash of the step's name and signature, which names the handler.
*   **The signature.** Header `X-Flow-Signature: t=<ms>, v1=<hex>`, the HMAC-SHA256 with the queue's secret of `"<ms>\n<METHOD>\n<path>\n<queue name>\n" + body`. Requests more than five minutes off are refused. Several `v1=` are accepted, for rotating a secret.
*   **The manifest.** A signed `GET` on the queue's URL is answered `{"shapes": [...], "once": [...], "started": [...]}`: the steps the application has handlers for, and how each is kept. A step whose shape is not listed is held until it is.
*   **Answers.** `200 {"value": v}` (with `"undo": u`), `202` (later), `200 {"working": true}`, `200 {"retry": {"reason", "after": ms}}`, `200 {"unknown": true}`, `200 {"failed": "why"}`, `200 {"unknown_shape": shape}`.
*   **Failures.** A request that never left is sent again, the same attempt. Another status, or a cut connection, is uncertain: a plain step is sent again, a `Settled` one gives `Unknown`. A `401` or `403` pauses the queue: the secret is wrong on one side.
*   **Health.** After five failures in a row a queue is paused, and probed until it answers; backoff starts at a second and doubles to a minute. At most `concurrency` steps of a queue are under way at once.
