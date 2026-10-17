# The console of qoqi serve

The pages `qoqi serve` shows at its address: what each instance is doing, read as code; starting one from a form made from the types of its flow; cancelling one, with what it will do first. See [running in production](../docs/guides/production.md#the-console).

It is a React app built with the AppSwite kit (Radix, Tailwind, OKLCH tokens), and everything it shows comes from the daemon's API.

```text
$ make all              # the console, then qoqi with it
$ make console          # tests, then builds into internal/flowd/console, which qoqi embeds
$ make console-check    # typechecks and tests, builds nothing
$ make console-dev      # serves it from here, with /api sent to a daemon on 127.0.0.1:7070
```

With `make console-dev`, open it with the daemon's token: `http://localhost:5173/#token=$(cat flows.db.token)`. `QOQI_FLOW=http://127.0.0.1:7071 npm run dev` for a daemon elsewhere.
