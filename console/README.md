# The console of qoqi serve

The pages `qoqi serve` shows at its address: what each instance is doing, read as code; starting one from a form made from the types of its flow; cancelling one, with what it will do first; writing a program, checking and deploying it; a year of activity. See [running in production](../docs/guides/production.md#the-console).

```text
src/
  app/        the router and the frame of the pages (shell.tsx)
  auth/       setting up, signing in, who is there
  features/   one folder a section: overview, flows, instances, activity,
              editor (CodeMirror, loaded when opened), deployments, settings
  components/ what the sections share; ui/ is the AppSwite kit
  lib/        the API, its queries, the stream of events, the types
```

It is a React app built with the AppSwite kit (Radix, Tailwind, OKLCH tokens), and everything it shows comes from the daemon's API.

```text
$ make all              # the console, then qoqi with it
$ make console          # tests, then builds into internal/flowd/console, which qoqi embeds
$ make console-check    # typechecks and tests, builds nothing
$ make console-dev      # serves it from here, with /api sent to a daemon on 127.0.0.1:7070
```

With `make console-dev`, open `http://localhost:5173` and sign in (`admin`, `admin` the first time). `QOQI_FLOW=http://127.0.0.1:7071 npm run dev` for a daemon elsewhere.
