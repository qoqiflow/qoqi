# HTTP API

The daemon's API is everything it does: the command line and the console are clients of it, and so can any program be.

```console
$ curl -H "Authorization: Bearer $(cat flows.db.token)" http://127.0.0.1:7070/api/instances
```

## Conventions

*   **Address.** `qoqi serve --listen`, `127.0.0.1:7070` by default. The daemon answers only to its own host, `localhost`, `127.0.0.1` and `::1`; another `Host` is `403`.
*   **Authentication.** Every request but `POST /api/attempts` carries `Authorization: Bearer <token>`. The token is in `<store>.token` (`flows.db.token`), made when the daemon first starts, readable by its owner only. Wrong or missing: `401`.
*   **Bodies** are JSON, both ways.
*   **Errors** are `{"error": "<code>", "message": "<sentence>"}`, with the status of the table.

## Values

Fields, messages, step arguments and answers are written in the **boundary form**: plain JSON, read against the type the program declares.

| Qoqi | JSON |
|------|------|
| `Int` | A whole number, from −140 737 488 355 328 to 140 737 488 355 327 |
| `Float` | A number; `"NaN"`, `"+Inf"`, `"-Inf"` as texts |
| `String`, `Bool` | A string, `true` or `false` |
| `T?` | The value, or `null`. A missing optional field is `null`; a missing field that is not optional is refused |
| A struct | An object of its fields; an unknown field is refused |
| An enum value without values | Its name: `"Approved"` |
| An enum value with one value | `{"Refused": "too much"}` |
| An enum value with several | By position, `{"Refused": ["ada", "too much"]}`, or by name, `{"Refused": {"who": "ada", "why": "too much"}}` |
| `Result<T, E>` | `{"Ok": value}`, `{"Err": error}` |
| `Duration` | A text with units: `"90s"`, `"1h30m"`, `"2 days"`, `"250ms"`; units `ms`, `s`, `m`/`min`, `h`, `d`, `w` |
| An array | An array |
| A dictionary | An object |

## Instances

### `GET /api/instances`

The instances, sorted by name. `?flow=Expense`, `?status=waiting`.

```json
[
  {
    "id": "expense:1",
    "flow": "Expense",
    "status": "waiting",
    "birth": "e60eef3d",
    "wakeAt": "2026-10-19T22:30:39.065Z",
    "stage": "asking",
    "pass": 1,
    "version": "sha256:43381189…"
  }
]
```

Besides `id`, `flow`, `status` and `birth`, a field is there when it has a value: `wakeAt`, `fields`, `result`, `stopped` (why), `steps`, `undos`, `cancel`, `parent`, `children`, `stage`, `pass`, `version`, `ended`.

### `POST /api/instances`

Starts one: `{"flow": "Expense", "id": "expense:1", "fields": {"id": "1", "author": "lin"}}`. Without `id`, one is made: `expense:3f9c01ab`.

| Status | |
|--------|--|
| `201` | Started; the instance |
| `200` | The name was taken by an instance of this flow; that instance |
| `400 fields` | The fields do not fit the flow |
| `404 flow` | No such flow is served |
| `409 taken` | The name is an instance of another flow |

`?dry=1` checks the fields and starts nothing.

### `GET /api/instances/{id}`

The instance, and for people: `tasks` (where each is, its calls and their lines), `kept` (what its steps returned, by key), `tries`, `retries`, `output` (its last 200 lines). `404` if there is none.

### `GET /api/instances/{id}/result`

What its `run` returned. `?wait=30s` waits that long at most (a minute at most).

| Status | |
|--------|--|
| `200` | The result |
| `202` | Not ended yet; the instance |
| `409 stopped` | It is stopped, and why |
| `409 cancelled` | It was cancelled, and why |

### `POST /api/instances/{id}/inboxes/{inbox}`

A message: `{"message": {"approved": true, "why": ""}, "key": "mail-1"}`. The message is read against the inbox's type; a key sent before is one message.

| Status | |
|--------|--|
| `200` | `{"sent": "delivered"}`, or `{"sent": "already"}` for a key sent before |
| `400 message` | The message does not fit the inbox |
| `404 inbox` | The flow has no such inbox |
| `409 ended` | The instance has ended |
| `429 full` | The inbox holds all it can: send it again later |

### `POST /api/instances/{id}/cancel`

`{"reason": "withdrawn"}`, optional. `202` and the instance; `409 ended` if it has ended.

### `POST /api/instances/{id}/resume`

Takes a stopped instance up from its last stage. `{"dry": true}` answers `200` with what it would do: the results it would take as kept (`took`), the errors it would forget and run again (`forget`), what it would do next (`next`). `{}` does it: `202`. `409 resume` if it cannot be.

### `POST /api/instances/{id}/answer`

Says what a step of a stopped instance did, for a step its application could not tell of: `{"key": "<step key>", "value": {"Ok": "ch-1"}}`, or `{"key": "…", "nothing": true}` when it did nothing, so that it is sent again. `200 {"answered": true}`. Then resume.

### `POST /api/instances/{id}/end`

Ends a stopped instance as it is, `failed`, without running its undos: `{"reason": "refunded by hand"}`. `202`; `409 end` if it is not stopped.

## Programs and deploys

### `GET /api/flows`

The flows served, with how many instances are in each status: `[{"name": "Expense", "instances": {"waiting": 3, "done": 12}}]`.

### `POST /api/deploy`

What `qoqi deploy` sends: `{"programs": [...], "dry": false}`, each program compiled by the command. The answer is `{"reports": [{"program", "version", "before", "new", "same", "changes", "moving", "lock"}]}`. `409 refused` when a version changes the shape of a stage that an alive instance may be at, with no migration.

### `POST /api/undeploy`

`{"program": "approvals"}`. `200 {"removed": "approvals"}`; `409 refused` while one of its instances is alive.

### `GET /api/deployments`, `GET /api/deployments/{seq}`, `GET /api/deployments/{seq}/diff`

The deploys, the last first (`?program=`); one, with its sources; what it changed of the source, from the one before or `?from=seq`, as hunks.

### `GET /api/program`, `GET /api/source?file=`

The types of the flows served, their fields, inboxes and steps (what the console builds its forms from); the lines of a file of a program.

## Events

### `GET /api/events`

Server-sent events: one `data: {...}` line per event, of kind `instance` (with the instance), `step`, `output` or `deploy`. A comment line every 15 seconds keeps the connection alive.

### `GET /api/output`

What the instances printed, the last 500 lines: `[{"at", "id", "text"}]`. `?instance=` for one.

## Steps carried out by applications

### `POST /api/attempts`

An application's answer to a step it accepted to answer later: `{"key", "attempt", "token", ...answer}`, the answer in one of the shapes of [the protocol](../guides/application-steps.md#the-protocol). It carries no bearer token: the attempt's own `token`, sent with the step, is its authorization.

| Status | |
|--------|--|
| `200` | `{"kept": true}`, or `{"already": true}` if an answer was there |
| `403 token` | Not the attempt's token |
| `404 attempt` | No such attempt |
| `410 late` | The attempt was given up, past its `within` |
| `422 value` | The value does not fit the step's type; the instance stops |

`{"working": true}` keeps nothing: it asks whether the attempt is still alive (`410` if not).
