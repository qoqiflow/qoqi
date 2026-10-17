# Qoqi Flow documentation

## Getting started

*   [Installation](getting-started/installation.md)
*   [Quickstart](getting-started/quickstart.md): a flow that waits for a person, served, started, answered and tested

## Concepts

*   [Flows](concepts/flows.md): flows and instances, what is durable, the life of an instance
*   [Steps](concepts/steps.md): the one rule, keys, what happens after a crash, time limits, retries
*   [Waiting](concepts/waiting.md): timers, messages from outside, the first of several, tasks
*   [Failures](concepts/failures.md): errors, undo, cancellation, stopped instances
*   [Versions](concepts/versioning.md): deploying new code under running instances, stages and migrations

## Guides

*   [Testing flows](guides/testing-flows.md): trials, a clock of their own, `everyCrash`
*   [Steps in your application](guides/application-steps.md): queues, `qoqi gen`, the SDKs for Go, PHP and TypeScript
*   [Running in production](guides/production.md): stores, several daemons, the console, security

## Reference

*   [Command line](reference/cli.md)
*   [HTTP API](reference/http-api.md)
*   [Configuration](reference/configuration.md): `qoqi.json`
*   [Error codes](reference/errors.md)
*   The language
    *   [A tour](reference/language/tour.md)
    *   [Specification](reference/language/spec.md)
    *   [Standard library](reference/language/stdlib.md)
    *   [Modules](reference/language/modules.md), [enums](reference/language/enums.md), [traits](reference/language/traits.md), [tasks](reference/language/tasks.md), [error handling](reference/language/error-handling.md)
    *   [Packages](reference/language/packages.md), [formatting](reference/language/formatting.md), [the editor](reference/language/editor.md)

## Internals

For contributors: how it is built.

*   [The runtime](internals/runtime.md): the daemon, the store, the life of an instance
*   [The virtual machine](internals/vm.md), [memory](internals/memory-management.md), [calling convention](internals/calling-convention.md), [closures](internals/closures.md)
*   [The compiler](internals/compilation/pipeline.md), one page per stage

See also [CONTRIBUTING.md](../CONTRIBUTING.md) and, for an agent writing Qoqi, [llms.txt](../llms.txt).
