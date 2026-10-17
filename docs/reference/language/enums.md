# Enums and Pattern Matching

An enum is a type whose values are one of several **variants**. Each variant may hold values of its own.

```rust
enum Shape {
    Circle(Float),          // holds a radius
    Rect(Float, Float),     // holds a width and a height
    Empty,                  // holds nothing
}
```

A value is made by naming a variant, and taken apart by a `match`:

```rust
fn area(shape: Shape) -> Float {
    match shape {
        case Shape.Circle(r): { return 3.14 * r * r; }
        case Shape.Rect(w, h): { return w * h; }
        case Shape.Empty: { return 0.0; }
    }
}

area(Shape.Rect(2.0, 3.0))      // 6
```

A complete program: [`test/e2e/cases/enums_json.qi`](../../../test/e2e/cases/enums_json.qi) builds, searches and prints a JSON document.

## Declaring

```rust
enum Light { Red, Amber, Green }                    // only names

enum Event { Deposit(Int), Withdraw(Int), Closed }  // some with values

enum Expr { Num(Int), Add(Expr, Expr), Neg(Expr) }  // holding itself

enum Json { Null, Bool(Bool), String(String), Array([Json]) }
```

*   Variants are separated by commas; a comma after the last one is allowed.
*   The values of a variant have types and no names. Any type will do: arrays, structs, functions, other enums, optionals, the enum itself.
*   A variant may have the name of a builtin type (`Bool`, `String`, `Int`): after a dot it cannot be taken for the type.
*   `pub enum` makes it usable from other [modules](modules.md). Its variants come with it; they are not exported one by one.

## Making Values

```rust
let a = Circle(2.0);
let b = Empty;                      // no parentheses for a variant without values
let shapes = [a, b, Shape.Rect(1.0, 2.0)];   // with its enum, the same
```

A variant can be written alone when an enum the file declares, or imports by name (`import geo.{Shape};`), has it. Written with its enum, `Shape.Circle`, it is always there; from a module imported whole it is `geo.Shape.Circle`.

When two enums in scope have a variant of that name, the value says which: where a `Slot` is expected (a variable, an argument, what a function returns, the other items of an array), `Empty` is `Slot.Empty`, and in a `match` or after `is` it is a variant of the type of the value tested. Where nothing tells, the compiler says so (`E2041`) and offers both. A variable, a function, a constant or a type of the same name hides a variant: `let Empty = 3;` makes `Empty` the number.

A variant may name its values, which are then given by name, in any order, and named in patterns, where the ones left out are not looked at:

```rust
enum Event {
    Purchase { user: String, item: String, cents: Int },
    Logout(String),
}

let e = Purchase { user: "ada", item: "lamp", cents: 4500 };
match e {
    case Purchase { user, cents } if cents > 1000: { println(user, "spends a lot"); }
    case Purchase { item }: { println("sold", item); }
    case Logout(who): { println(who, "left"); }
}
```

The names only say which value is which: the values are still in the order declared, `Purchase("ada", "lamp", 4500)` is the same value, and it prints that way.

A variant with values, not called, is the function that makes one:

```rust
let circles = [1.0, 2.0, 3.0].map(Shape.Circle);    // [Shape]
let make: fn(Float) -> Shape = Shape.Circle;
```

## Matching

A `match` on an enum tries its cases in order and runs the first that fits.

```rust
match event {
    case Event.Deposit(amount): { balance += amount; }
    case Event.Withdraw(amount): { balance -= amount; }
    case Event.Closed: { open = false; }
}
```

**Names.** A name in a pattern becomes a variable of that case, holding the value at that position. It exists only in the case. `_` takes a value without naming it.

```rust
case Shape.Rect(w, _): { return w; }
```

**Without parentheses** a pattern takes the variant whatever it holds: `case Shape.Rect: { ... }`.

**Literals** match only that value. Put the specific cases before the general one:

```rust
match shape {
    case Shape.Circle(0.0): { return "a point"; }
    case Shape.Circle(r): { return "a circle"; }
    default: { return "something else"; }
}
```

**Patterns inside patterns** look into a value that is itself an enum, to any depth:

```rust
fn simplify(e: Expr) -> Expr {
    match e {
        case Expr.Add(Expr.Num(0), right): { return right; }      // 0 + x is x
        case Expr.Neg(Expr.Neg(inner)): { return inner; }         // --x is x
        case Expr.Add(Expr.Num(a), Expr.Num(b)): { return Expr.Num(a + b); }
        default: { return e; }
    }
}
```

**`default`** takes whatever the cases above did not.

A name of a pattern is a copy of what the variant holds: assigning to it does not change the value that was matched.

### Every case must be handled

A `match` on an enum without a `default` has to cover every value the enum can have. Leaving something out is a compile error that names it:

```rust
fn next(light: Light) -> Light {
    match light {
        case Light.Red: { return Light.Green; }
        case Light.Green: { return Light.Amber; }
    }
}
```
```
[ERROR] E2028: match on Light does not handle Light.Amber
   = Suggestion: Add a case for each, or a 'default' case: case Light.Amber: { ... }
```

This is the point of an enum over a set of numbers or strings: add a variant, and the compiler lists every `match` that has to learn about it.

The check looks inside patterns. These three cases are complete, because between them they take every `Option<Result<Int, String>>`:

```rust
case Option.Some(Ok(n)): { ... }
case Option.Some(Err(message)): { ... }
case Option.None: { ... }
```

and when part of a variant is missing, the message says which part:

```
match on Report does not handle Report.Line(_, Event.Withdraw)
```

What can be listed is: the variants of an enum, and `true` and `false`. Numbers and texts cannot, so cases with literals of those never complete a match by themselves; a case with a name or `_` in that position, or a `default`, has to follow.

A function whose `match` is complete and returns in every case returns on every path: no `return` is needed after it.

A case that can never run, because a case above already takes its variant whole, is a warning (`E2023`).

### Other things a `match` can test

The patterns of an enum mix with the [other kinds](tour.md#control-flow): a type (`case Shape:` takes any `Shape`, `case Int:`) and a literal. When the value is an `Any` or a trait, a variant pattern tests for the enum as well:

```rust
fn describe(value: Any) -> String {
    match value {
        case Shape.Circle(r): { return "circle"; }
        case Shape: { return "another shape"; }
        case Int: { return "a number"; }
        default: { return "?"; }
    }
}
```

### A match that gives a value

Where a value is expected, a `match` is an expression. Each case gives the value of the whole:

```rust
fn area(shape: Shape) -> Float {
    return match shape {
        case Shape.Circle(r): 3.14159 * r * r;
        case Shape.Rect(w, h): w * h;
        case Shape.Empty: 0.0;
    };
}

let total = price * match customer {
    case Customer.Member(years) if years >= 5: 80;
    case Customer.Member(_): 90;
    case Customer.Guest: 100;
} / 100;
```

*   A case is an expression followed by `;` (a `,` does as well, and the last case needs neither), or a block that leaves: `{ return ...; }`, `{ break; }`, `{ continue; }`, `{ panic(...); }`. A block that reaches its end gives nothing, and is refused (`E2040`).
*   The cases give the same type (`E2001`). One of them `null` makes the value optional; a type expected where the match stands (a declared variable, a return type) takes cases of different structs that implement the same trait.
*   It must have a value whatever is matched: every variant handled, or a `default` (`E2028`). On anything but an enum that means a `default`.

### Conditions, and several patterns for a case

```rust
match shape {
    case Shape.Circle(r) if r > 10.0: { println("a big circle"); }
    case Shape.Circle(_) | Shape.Empty: { println("round, or nothing"); }
    case Shape.Rect(w, h) if w == h: { println("a square"); }
    case Shape.Rect(_, _): { println("a rectangle"); }
}
```

`if` after the pattern adds a condition, which may use the names of the pattern. It is only asked when the pattern matched, and when it does not hold the cases below are tried. A case with a condition handles nothing for certain: the check that a match is complete does not count it, so the variant needs another case (`case Shape.Circle(_)` above).

`|` between patterns makes a case run for any of them. They can be variants, structs, literals, ranges or types (`case Int | Float:`). They may name values, if every one of them names the same values with the same types: `case Add(x, _) | Sub(x, _):` has an `x` whichever matched, and a condition after them is asked of the one that did. A name that only some of them give is an error (`E2029`).

### Structs, and null

A struct is matched by the values of its fields, written as in a struct literal. A field given a value is compared with it; a field written alone gives its value a name; the fields left out are not looked at. Patterns nest inside each other, variants in structs and structs in variants:

```rust
match event {
    case Click(Point { x: 0, y }): { println("the left edge, at", y); }
    case Drag(Line { from, to: Point { x, y } }): { println("from", from, "to", x, y); }
    default: { }
}

fn describe(figure: Figure) -> String {          // a trait
    return match figure {
        case Box { size: Size { width, height } } if width == height: "a square";
        case Box { at }: "a box at ${at}";
        default: "something else";
    };
}
```

A value whose type is a trait or a union is tested for the struct first; in the case it is that struct. A struct pattern whose fields are all names takes every value of the struct, which makes a match complete. After `is`, a struct pattern would read as the start of the block, so it is only written after `case`.

`case null:` takes a value that is null, and the cases below it see a value that is not. A match on an optional that handles every variant still has to say what null does: a match that gives a value without `case null:` or `default` is an error, and a statement runs no case for null.

The same braces build a struct with a field named like a variable: `Point { x, y }` is `Point { x: x, y: y }`.

A case on a number can be a range: `case 0..10:` (10 left out), `case 90..=100:`, with `Int` or `Float` ends.

## One Variant: `is`

A `match` asks which variant a value is. Often the question is smaller: is it *this* one? `value is Pattern` asks that, and gives a `Bool`:

```rust
if right is Value.Number(number) {
    return Ok(Value.Number(-number));
}
return Err(failed(operator, "Operand must be a number."));
```

The pattern is whatever a `case` takes for a variant: a name, `_`, a literal or another pattern for each value, to any depth; no parentheses to take the variant whatever it holds; a tuple.

```rust
if shape is Shape.Empty { ... }
if event is Event.Withdraw(amount) && amount > balance { ... }
if e is Expr.Add(Expr.Num(0), right) { return right; }
if (left, right) is (Value.Number(a), Value.Number(b)) { return a + b; }
if stock.take(label, 1) is Err(reason) { log(reason); }
```

**Where the names exist.** A name of the pattern is a variable of the code that runs only when the test held:

*   the rest of a chain of `&&`: `shape is Shape.Rect(w, h) && w == h`, and a test further on may look into it: `next() is Option.Some(shape) && shape is Shape.Circle(r)`;
*   the block of the `if` or the `while` whose condition the test is, alone or in such a chain;
*   what follows an `if` that leaves when the test fails: the test under `!`, no `else`, and a block that ends with `return`, `break`, `continue` or `panic`. The happy path then reads down the page:

```rust
let parsed = command(line);
if !(parsed is Ok(cmd)) {
    println("skipped:", line);
    continue;
}
run(cmd);                      // cmd is the command from here on
```

They do not exist in the `else`, in the next `else if`, or after the statement. Each branch of an `else if` chain has its own:

```rust
if shape is Shape.Circle(r) {
    return r * r * 3.14;
} else if shape is Shape.Rect(w, h) {
    return w * h;
}
return 0.0;
```

Under `!`, on a side of `||`, or where the test is simply a value (`let round = shape is Shape.Circle(r);`), nothing runs only when it held, so a name there is an error (`E1003`) and `_` is what to write:

```rust
if !(shape is Shape.Circle(_)) { return; }
let flat = shape is Shape.Empty || shape is Shape.Rect(_, 0.0);
let big = shape is Shape.Circle(r) && r > 10.0;        // fine: the && uses r
```

There is no form that names values for the code *after* an `if` that left (`if !(shape is Shape.Circle(r)) { return; }` and then `r`): put the code in the block, or use a `match`.

**In a loop.** `while` asks again on every turn, `continue` included, which makes it the loop over something that hands out values until it has none:

```rust
while reader.next() is Ok(line) {
    if line == "" { continue; }
    lines.push(line);
}
```

A function that says it has nothing left with `null` (`items.pop()`, `queue.take()`) is no enum, and its loop tests against `null`:

```rust
let job = queue.take();
while job != null {
    run(job);                // a Job here
    job = queue.take();
}
```

**The value** is worked out once, before its names are read, and only when its turn in the condition comes: in `ready && load() is Ok(text)`, nothing is loaded unless `ready`. A name keeps the value it was given, whatever the rest of the condition or the block does to the variable that was tested.

**What can be tested** is what a `match` with that case accepts. A value that may be null fits no pattern when it is, and is not null where the test held. An `Any`, a trait or a union is tested for the enum as well, and is of that enum in the block. A pattern the value can never fit is an error (`E2029`); one it always fits (the one variant of an enum, a tuple of names) is a warning (`E2023`), since nothing is being asked: `let (a, b) = pair;` names the values of a tuple.

`is` binds tighter than every operator with two sides, as it does with a type: `a is A.X && b`, `!v is A.X` is `!(v is A.X)`. After it, `Name.Name` is a variant, or a type of another module (`value is geo.Shape`); a tuple of names that are all types is the tuple type, as before: `value is (Point, Label)`.

What `is` does not take is the rest of what a `case` takes: several patterns (`is A.X | A.Y`), a range, a literal by itself (`n is 3`). Those are `||`, a comparison and `==`.

## Generic Enums

```rust
enum Option<T> { Some(T), None }
enum Result<T, E> { Ok(T), Err(E) }
enum List<T> { Cons(T, List<T>), Nil }
```

The type arguments are worked out, in this order, from:

1.  what is written: `Option<Int>.None`
2.  the values given: `Option.Some(1)` is an `Option<Int>`
3.  the type expected where the value goes:

```rust
let a: Option<Int> = Option.None;
fn find() -> Option<String> { return Option.None; }
if a == Option.None { ... }                    // the other side of a comparison
let b: Option<Result<Int, String>> = Option.Some(Ok(1));    // reaches inner values
```

When none of these says what a type argument is, it is an error: `let x = Option.None;` gives `nothing here says what T of Option is`, and the fix is to write it.

In a pattern the type arguments are those of the value being matched; the names it binds have the matching types:

```rust
fn first(r: Result<[Int], String>) -> Int {
    match r {
        case Ok(items): { return items[0]; }       // items: [Int]
        case Err(message): { return message.len(); }  // message: String
    }
}
```

## Methods and Traits

An enum has methods and implements traits exactly as a struct does. `self` is the value; a `match` on it is the usual body.

```rust
impl Shape {
    fn scaled(by: Float) -> Shape {
        match self {
            case Shape.Circle(r): { return Shape.Circle(r * by); }
            case Shape.Rect(w, h): { return Shape.Rect(w * by, h * by); }
            case Shape.Empty: { return self; }
        }
    }
}

impl Describe for Shape { fn describe() -> String { ... } }

impl<T> Option<T> {
    fn map<U>(f: fn(T) -> U) -> Option<U> {
        match self {
            case Option.Some(v): { return Option.Some(f(v)); }
            case Option.None: { return Option.None; }
        }
    }
}
```

A method cannot have the name of a variant.

## Equality and Printing

Two values of an enum are **equal** when they are the same variant holding equal values:

```rust
Shape.Circle(1.0) == Shape.Circle(1.0)      // true
Shape.Empty == Shape.Empty                  // true
Shape.Circle(1.0) == Shape.Circle(2.0)      // false
```

This goes all the way down, through enums, arrays and structs inside enums, so two trees built the same way are equal. A type that implements `Equal` is compared by its `equals` wherever it is found.

A value **prints** the way it is made, without the enum's name: `Circle(2)`, `Empty`, `Some("x")`, `Cons(1, Cons(2, Nil))`.

## Tuples

A tuple, `(1, "a")` of type `(Int, String)`, is a value of an enum that the compiler declares for each size from two to six:

```rust
enum __Tuple2<A, B> { Of(A, B) }
```

The parser writes everything about tuples in terms of it: the type `(Int, String)` is `__Tuple2<Int, String>`, the value `(1, "a")` is `__Tuple2.Of(1, "a")`, `case (n, text):` is a pattern on that variant, and `let (a, b) = pair;` is a variable per value. So what this page says of enums holds for tuples with no code of their own: type arguments worked out from the values, equality by content, patterns inside patterns, and the check that a `match` is complete.

```rust
match (wet, cold) {
    case (true, true): { return "snow"; }
    case (true, false): { return "rain"; }
    case (false, _): { return "dry"; }
}
```

What tuples add is reading a value by its position, `pair.0`, and being printed and named in messages the way they are written: `(1, "a")`, `(Int, String)`. See the [guide](tour.md#tuples).

## What an Enum Value Is Not

*   It has no fields: `shape.radius` is an error. A `match`, or `is` for one variant, is the way in.
*   The enum is not a value: `let s = Shape;` is an error.
*   It is not a struct: `Shape { ... }` is an error.

## Errors

| Code | Meaning | Example |
|------|---------|---------|
| `E2028` | A `match` on an enum leaves values out | no case for `Light.Amber` |
| `E2029` | A pattern that cannot match: unknown variant, wrong number of values, the variant of another enum, a literal of the wrong type | `case Shape.Rect(w):` |
| `E2030` | An enum used wrongly: unknown variant, wrong number of values, a type argument nothing determines, the enum itself as a value | `Shape.Square(1.0)` |
| `E2023` (warning) | A case that is never reached, or a test with `is` that always holds | a second `case Shape.Circle(r):`; `pair is (a, b)` |
| `E1003` | A name in a pattern after `is` where nothing could use it | `!(shape is Shape.Circle(r))` |

## What Is Not There

*   **Patterns on arrays**: an array is bound to a name and looked at with its methods.
*   **Several patterns, ranges and literals after `is`**: `value is A.X | A.Y`, `n is 1..10`, `n is 3`. Write `||`, comparisons, `==`.

## How It Is Compiled

**Types.** To the type checker a value of `Shape` has a type named `Shape`, built like the type of a struct. That is what gives enums type arguments, methods and traits with no code of their own. What differs, making and matching, is in `internal/types/enums.go`.

**Exhaustiveness** is checked with the usual method for pattern matching: the cases form a table with one column, the value being matched. For each variant, the rows that accept it are kept and the patterns for what the variant holds become new columns in front; the table is complete if that is true, in turn, for every variant. A column in which no row looks inside the value is dropped without being split, which is what makes the method end for an enum that holds itself. When a table is found empty, the path taken to it is the example in the error message.

**Lowering.** The parser cannot tell `Shape.Circle(2.0)` from a method call, so it is an ordinary call until desugaring, which knows what `Shape` is. There:

| Source | Becomes |
|--------|---------|
| `Shape.Circle(2.0)`, `Shape.Empty` | a `VariantExpr`: enum, variant, values |
| `Shape.Circle` not called | a lambda that makes one |
| `case Shape.Rect(w, 0.0): body` | `if value is Shape.Rect && value.Rect#1 == 0.0 { let w = value.Rect#0; body }` |
| the last case of a complete match | the final `else`, without a test |
| `if value is Shape.Rect(w, 0.0) { body }` | the same test, and `let w = value.Rect#0;` in front of the body ([desugaring.md](../../internals/compilation/desugaring.md#2c-pattern-tests-value-is-pattern)) |

A value of a variant is only read after the test that the value is that variant, which `&&` guarantees.

**At runtime** a value of an enum is an object that carries the name `Enum.Variant` and holds its values as the fields `"0"`, `"1"`, ... The bytecode is `NEWOBJ` with a third operand of 2, which marks the object as a variant: the VM compares two such objects by content and prints them as they are made. The test for a variant is the same host function as `value is Type`, with the variant's name as what to look for, and a test or cast to the enum type lists the names of all its variants.

A generic enum is not duplicated per type argument: `Option<Int>` and `Option<String>` share one set of methods, as generic structs do.
