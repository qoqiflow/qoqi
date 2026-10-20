# Running in production

```console
$ export QOQI_PATH=/var/lib/qoqi
$ qoqi serve --store postgres://flows:secret@db/flows --listen 127.0.0.1:7070 --hosts flows.example.com --workers 8
```

and from a laptop, or a CI job:

```console
$ export QOQI_URL=https://flows.example.com QOQI_TOKEN=...   # the daemon's $QOQI_PATH/token
$ qoqi deploy shop/
```

## Its home

The daemon keeps its files in `QOQI_PATH`, `~/.qoqi` by default: the store when it is SQLite (`flows.db`), `daemon.json` and `token` for the commands, readable by its user alone, and `logs/` (`daemon.log`, what it says; `flows.log`, what the flows print). See [the home](../reference/cli.md#the-home-qoqi_path).

## The store

| `--store`, `QOQI_STORE` | |
|-----------|--|
| `$QOQI_PATH/flows.db`, `sqlite:path` | A SQLite file: one machine, no server to run. The default |
| `postgres://user:pass@host/db` | PostgreSQL |
| `mysql://user:pass@host/db` | MySQL |

Each program has its own tables, named after it. The bytecode of every version deployed is kept in the store, for as long as an instance runs it.

## Several daemons

On PostgreSQL or MySQL, daemons on several machines share one store. An instance is held by one daemon at a time, under a **lease** of 30 seconds, renewed while it runs; when a daemon dies, its instances are taken up by another once their leases run out. Each daemon follows what the others deploy.

On one machine, one daemon runs from a home: a second one is refused. Each daemon of a shared store has a home of its own.

## Workers

`--workers N` is how many instances run side by side, one per goroutine; the number of processors by default. Each instance still runs its own tasks one at a time.

## Deploying

```console
$ qoqi deploy shop/              # every program of the folder that has flows, with its modules
$ qoqi deploy shop/ --dry        # what it would do
$ qoqi versions
$ qoqi diff shop
```

A deploy is served at once, with no restart: what runs ends its turn, and instances move at their next stage ([versions](../concepts/versioning.md)). Commit the `flow.lock` it writes. `qoqi deploy shop/ --watch` deploys again on every save, for development.

## Instances that need a person

```console
$ qoqi ls --status stopped
$ qoqi show order:4412
$ qoqi resume order:4412           # what it would do
$ qoqi resume order:4412 --go
```

See [stopped instances](../concepts/failures.md#stopped-instances).

## The console

The console is at the daemon's address; `qoqi ui` opens it in a browser. It shows each instance as code, at the line it is at, with its variables and what its steps returned; starts instances from a form made from the flow's types; sends messages, cancels, resumes. Its editor writes programs, checks them as they are typed, runs their tests, tries their flows in memory with a clock moved by hand, and deploys them; its activity pages show a year of what the flows did, day by day. It is served by the daemon at its own address, built into `qoqi`.

It starts with one user, `admin`, password `admin`, changed at the first sign in before anything else can be done: sign in at once on a daemon reachable by others. People sign in with a name and a password; each can do everything. See [`qoqi users`](../reference/cli.md#qoqi-users).

## Security

*   The daemon listens on `127.0.0.1` by default, and answers only to its own host names and those of `--hosts`: put it behind your own proxy, with TLS, to reach it from elsewhere.
*   Every request of the API carries the token, or a person's session: a cookie `HttpOnly` and `SameSite=Strict`, `Secure` behind TLS (the daemon reads `X-Forwarded-Proto` of a proxy). Passwords are kept as PBKDF2-SHA512; guessing is slowed down.
*   The token is for the commands and the programs: the console never asks for it, nor is it given one.
*   Requests to applications are signed with each queue's secret, and checked by the SDKs; keep the secrets in the environment (`"secret": "env:SHOP_SECRET"`).
*   What a flow is given and what its steps return is written in the store as JSON. Do not give a flow what must not be stored.

## Limits

Each instance runs under the limits of any Qoqi program: `--heap-mb` and `--max-depth`, or `heap_mb` and `max_depth` in `qoqi.json`. A step that asks to be run again more than 100 times, or a step by a queue that is uncertain 20 times in a row, stops its instance.
