# Error codes

Every diagnostic has a code, and usually a suggestion that says the fix. `qoqi check --json` gives them to a tool or an agent.

```text
[ERROR] F0001: a flow reaches the network outside a step
  --> refund.qi:24:16 in function 'notify'
   |
24 |     let sent = http.post(hook, body);
   |                ^^^^^^^^^
   |
   = Note: 'notify' is called by Refund.run at refund.qi:12:9
   = Suggestion: make 'notify' a step fn, or move the call into one
```

| Prefix | |
|--------|--|
| `F00xx` | Flows, when the program is compiled |
| `F01xx` | Flows, when a stopped instance is taken up |
| `W` | Warnings of flows |
| `E` | The language, when the program is compiled |
| `R` | When the program runs ([runtime errors](language/error-handling.md#what-a-runtime-error-looks-like)) |

## Flows

| Code | | See |
|------|--|-----|
| `F0001` | A flow reaches the world (a file, the network, a database, another program) outside a step. The message gives the chain of calls | [The rule](../concepts/steps.md#the-rule-checked-by-the-compiler) |
| `F0002` | A `stage` inside the block of a `defer` | [Versions](../concepts/versioning.md) |
| `F0004` | A step given or giving what is not data: a task, a channel, a connection, a function | [Steps](../concepts/steps.md#what-a-step-takes-and-returns) |
| `F0005` | A `stage` elsewhere than in `run` itself, or under a name another stage has | [Versions](../concepts/versioning.md) |
| `F0006` | A flow that listens for its cancellation in one `wait match` of its `run`, and not in another | [Cancellation](../concepts/failures.md#cancellation) |
| `F0007` | A `migrate fn` that does not fit its flow: no such flow, no such stage, or not the shape of the stage | [Migrations](../concepts/versioning.md#migrations) |
| `F0009` | A step that returns `Settled<T>` without saying its time limit (`within d`) | [Steps](../concepts/steps.md#after-a-crash) |
| `F0011` | A warning: since the version deployed, a stage changed shape, and no `migrate fn` says how an instance there goes on. The daemon refuses the deploy while an instance is there | [flow.lock](../concepts/versioning.md#flowlock) |
| `F0012` | A step `by` a queue that `qoqi.json` does not declare | [Queues](configuration.md#queues) |
| `F0013` | A flow that would wait (call a step, `wait`) inside a function the machine calls from Go: the one given to `result.attempt` or `result.guard`, directly or through a function that hands it on, or the `show`, `equals` or `compare` of a type, which it calls as it formats, compares or sorts. The instance could not be written there. Call the step outside, and give what it returned | [Steps](../concepts/steps.md) |
| `W0011` | A loop of `run` that waits with no stage in it: the instance keeps everything it did since it started, and never moves to a new version | [Versions](../concepts/versioning.md) |

### When a stopped instance is taken up

| Code | | |
|------|--|--|
| `F0101` | The instance did something its new version does not account for: a step's result was kept, and nothing asked for it again | Another fix, a migration, or a cancellation |
| `F0102` | The new version asks a step otherwise than it was asked under the same key: the world answered a question the new code does not ask | |
| `F0103` | The stage the instance is at is not in the newest version as it was, and no `migrate fn` takes an instance there | Cancel it, or serve the version it runs |

See [resuming with a fix](../concepts/failures.md#resuming-with-a-fix).

## The language

| Range | Stage |
|-------|-------|
| `E0xxx` | Reading the text: tokens and syntax |
| `E1xxx` | Names |
| `E2xxx` | Types |
| `E3xxx` | Generics |
| `E4xxx`–`E6xxx` | Lowering and code generation |
| `E7xxx` | What the runtime provides |
| `E8xxx` | Modules and imports |

| Code | |
|------|--|
| `E0001` | Unexpected token or malformed construct |
| `E0002` | Invalid character, unterminated string or comment |
| `E0003` | Statement not ended by `;` |
| `E0004` | Left side of `=` is not assignable |
| `E0005` | Field given twice in a struct literal |
| `E1001` | Undefined name, type or trait |
| `E1002` | A name declared twice in the same scope |
| `E1003` | A pattern names a value where nothing could use the name |
| `E1004` | A variable, parameter or name of a pattern nothing uses (a warning) |
| `E1005` | A name alone as the pattern of a case that is not a constant declared at the top level (a variable, a parameter, a function): compare it in a condition, `case Int if n == limit:` |
| `E2001` | Types do not match |
| `E2002` | `return` outside a function |
| `E2003` | A return of the wrong type |
| `E2004` | A path of the function does not return |
| `E2005` | A condition that is not a `Bool` |
| `E2006` | An operator on values it does not apply to |
| `E2007` | A unary operator on a value it does not apply to |
| `E2008` | A call of what is not a function |
| `E2009` | The wrong number of arguments |
| `E2010` | An argument of the wrong type |
| `E2011` | `for` over what cannot be gone through |
| `E2012` | A trait method an `impl` leaves out |
| `E2013` | A method whose signature is not the trait's |
| `E2014` | A field the struct has not |
| `E2015` | A method the type has not |
| `E2016` | A struct that is not declared |
| `E2017` | A value that may be `null`, used as if it could not |
| `E2018` | A field left out of a struct literal |
| `E2019` | A method declared twice |
| `E2020` | An empty array with nothing to say its type |
| `E2021` | A cast that can never succeed |
| `E2022` | An assignment to a constant |
| `E2023` | Code that can never run (a warning) |
| `E2024` | Type arguments on a method that takes none, or the wrong number |
| `E2025` | A whole number outside what an `Int` holds |
| `E2026` | An assignment to the variable of a `for` loop |
| `E2027` | A method that is not `pub`, called from another module |
| `E2028` | A `match` on an enum that leaves variants out |
| `E2029` | A pattern that cannot match the value |
| `E2030` | An unknown variant, the wrong number of values, an enum used as a value |
| `E2031` | A constant defined in terms of itself |
| `E2032` | A constant whose value is not made of constants |
| `E2033` | `.length` on an array or a text: it is `.len()` |
| `E2034` | `value?` where there is no failure to pass on, or nowhere to pass it |
| `E2035` | `value?.member` on a value that cannot be `null` |
| `E2036` | A type argument that does not implement what its parameter requires |
| `E2037` | A function written with `->` where nothing says its types |
| `E2038` | A range that is not of whole numbers, or used as a value |
| `E2039` | A `defer` that tries to leave: `return`, `?`, `break` or `continue` of an outer loop |
| `E2040` | A `match` that gives a value, with a case that gives none |
| `E2041` | A variant written alone that several enums have, and nothing says which |
| `E2042` | A field of a struct of another module that is not `pub` |
| `E2043` | A waitable made and thrown away: put `wait` before it |
| `E2044` | `wait` on what cannot be waited for |
| `E2045` | A method that takes `Self` called on a value known only by its trait: any other implementor would be taken. Call it where the type is known, or on a type parameter, `fn f<T: Trait>(a: T, b: T)` |
| `E3001` | A generic function or type whose type arguments cannot be worked out |
| `E3002` | Generic types that would make each other forever |
| `E3003` | The wrong number of type arguments |
| `E4101` | Closures that capture each other in a circle |
| `E5002` | A name that cannot be used as a value |
| `E5003` | The target of an assignment cannot be written |
| `E5005` | `break` or `continue` outside a loop |
| `E6001`–`E6003` | A function, a program or its constants too large for the bytecode: 65 536 registers, constants and functions |
| `E6006` | A program with no usable `main` |
| `E6007` | A call with more arguments than an instruction can take |
| `E7002` | An `extern fn` the runtime does not provide |
| `E8001` | No file for an imported module |
| `E8002` | A module that does not declare the name asked for |
| `E8003` | A name that is not `pub` |
| `E8004` | An import that clashes with another name |
| `E8005` | A module or type used where a value is expected, or the reverse |
| `E8006` | An import that cannot work: the entry file, a module re-exported |
| `E8007` | Statements outside a function, in a module where they cannot run |
| `E8008` | A variant built or matched by names it has not |
| `E8009` | A `pub` declaration that uses a type other modules cannot name |
| `E8010` | An import nothing uses (a warning) |
| `E8011` | A method that does not use `self` (a warning) |
| `E8012` | A static function that uses `self` |

Codes not listed (`E4001`–`E4006`, `E5001`, `E6004`, `E6005`, `E7001`) are raised only by a mistake of the compiler itself: please report one with the program that caused it.
