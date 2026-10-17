# Running in production

```console
$ qoqi serve --store postgres://flows:secret@db/flows --listen 127.0.0.1:7070 --workers 8
$ qoqi deploy shop/
```

## The store

| `--store` | |
|-----------|--|
| `flows.db`, `sqlite:path` | A SQLite file: one machine, no server to run. The default |
| `postgres://user:pass@host/db` | PostgreSQL |
| `mysql://user:pass@host/db` | MySQL |

Each program has its own tables, named after it. The bytecode of every version deployed is kept in the store, for as long as an instance runs it.

Next to a SQLite store, the daemon writes `flows.db.token` (its token, readable by its owner only) and `flows.db.daemon` (its address and process, while it runs). For a store on a server, the same files are kept under your user's configuration folder (`qoqi/stores/`).

## Several daemons

On PostgreSQL or MySQL, daemons on several machines share one store. An instance is held by one daemon at a time, under a **lease** of 30 seconds, renewed while it runs; when a daemon dies, its instances are taken up by another once their leases run out. Each daemon follows what the others deploy.

On one machine, one daemon serves a store: a second one is refused.

## Workers

`--workers N` is how many instances run side by side, one per goroutine; the number of processors by default. Each instance still runs its own tasks one at a time.

## Deploying

```console
$ qoqi deploy shop/              # every program of the folder that has flows
$ qoqi deploy shop/ --dry        # what it would do
$ qoqi versions
$ qoqi diff shop
```

A deploy is served at once, with no restart: what runs ends its turn, and instances move at their next stage ([versions](../concepts/versioning.md)). Commit the `flow.lock` it writes. `qoqi serve shop/ --watch` deploys again on every save, for development.

## Instances that need a person

```console
$ qoqi ls --status stopped
$ qoqi show order:4412
$ qoqi resume order:4412           # what it would do
$ qoqi resume order:4412 --go
```

See [stopped instances](../concepts/failures.md#stopped-instances).

## The console

`qoqi ui` opens the console in a browser, with the token in the address. It shows each instance as code, at the line it is at, with its variables and what its steps returned; starts instances from a form made from the flow's types; sends messages, cancels, resumes. It is served by the daemon at its own address, built into `qoqi`.

## Security

*   The daemon listens on `127.0.0.1` by default, and answers only to its own host names: put it behind your own proxy to reach it from elsewhere.
*   Every request of the API carries the token; the console keeps it in the browser, after `#`, never sent in a URL.
*   Requests to applications are signed with each queue's secret, and checked by the SDKs; keep the secrets in the environment (`"secret": "env:SHOP_SECRET"`).
*   What a flow is given and what its steps return is written in the store as JSON. Do not give a flow what must not be stored.

## Limits

Each instance runs under the limits of any Qoqi program: `--heap-mb` and `--max-depth`, or `heap_mb` and `max_depth` in `qoqi.json`. A step that asks to be run again more than 100 times, or a step by a queue that is uncertain 20 times in a row, stops its instance.
