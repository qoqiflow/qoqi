# Writing and Running Qoqi Programs

A practical guide to what works today. For the full syntax see the [language spec](spec.md); for how the compiler is built see the [pipeline](../../internals/compilation/pipeline.md).

## Setup

```bash
make build            # produces ./qoqi
```

or run without building: `go run ./cmd/qoqi <command>`.

## Hello, World

```rust
// hello.qi
fn main() -> Int {
    println("Hello, Qoqi!");
    return 0;
}
```

```bash
./qoqi run hello.qi
```

Every program needs a `main` function without parameters. If it returns an `Int`, that becomes the process exit code.

## Commands

| Command | What it does |
|---------|--------------|
| `qoqi serve program.qi` | Run the flows of a program, and keep them: the daemon ([flow.md](../../concepts/flows.md)). `qoqi deploy`, `start`, `ls`, `show`, `send`, `result` and the rest talk to it; `qoqi serve --help` lists them |
| `qoqi run file.qi` | Compile and run a program |
| `qoqi file.qi` | The same; also what a file that starts with `#!/usr/bin/env qoqi` is run with (see below) |
| `qoqi dump file.qi` | Print what the compiler made of a program: its bytecode, as a listing; `--ir` prints the intermediate representation instead |
| `qoqi check [--json] file.qi` | Compile without running and say what is wrong; `--json` for a CI step, an editor or an agent |
| `qoqi test` | Run the tests: the functions `test...()` of the files `*_test.qi` ([stdlib.md](stdlib.md#test-files-qoqi-test)) |
| `qoqi fmt` | Write `.qi` files the one way; `--check` only says which would change ([formatting.md](formatting.md)) |
| `qoqi pkg require vendor/name` | Add a package to the project, and install it; `qoqi pkg install`, `update`, `remove`, `show`, `why`, `outdated` do the rest ([packages.md](packages.md)) |

Exit codes: `0` on success (or the `Int` returned by `main`), `1` for a compile error, a runtime error or a usage error. Diagnostics go to stderr, program output to stdout.

**Scripts.** A program need not have a `main`: statements written outside any function run in the order they are written, with the declarations around them known everywhere. `return 2;` ends the script with that exit code.

```qoqi
#!/usr/bin/env qoqi
import std.os;

let names = os.args();

if names.isEmpty() {
    println("usage: greet name...");
    return 2;
}

for name in names {
    println(greeting(name));
}

fn greeting(name: String) -> String {
    return "hello ${name}";
}
```

The variables of the script belong to it: a function does not see them (`names` cannot be used inside `greeting`), it is given what it needs. Only the program's entry file runs statements; a module that has some is `E8007`.

A file whose first line is `#!/usr/bin/env qoqi` runs on its own once it may be executed (`chmod +x`), with or without `.qi` at the end of its name: `./deploy staging` is `qoqi run deploy staging`. The line is a comment to Qoqi, and `qoqi fmt` keeps it.

```bash
chmod +x deploy
./deploy staging
```

Commands that execute a program accept two limits, anywhere after the command:

| Option | Default | Exceeding it |
|--------|---------|--------------|
| `--heap-mb=N` | 256 | `out of memory: ... limit N` |
| `--max-depth=N` | 10000 | `stack overflow: max call depth N exceeded` |

```bash
qoqi run --heap-mb=1024 big.qi
qoqi run program.qi --max-depth=100000
```

`--cpuprofile=FILE` writes where the time went while the program ran, however it ends, for `go tool pprof`:

```bash
qoqi run --cpuprofile=cpu.prof slow.qi
go tool pprof -top qoqi cpu.prof
```

`run`, `serve`, `check` and `dump` also take the folder of a [project](#projects), or no file at all inside one.

## A Tour of the Language

The programs in [`examples/`](../../../examples) are runnable versions of what follows.

### Values and variables

```rust
let count = 3;              // type inferred: Int
let ratio: Float = 0.5;     // or stated
let name = "qoqi";
let ready = true;
let nothing: Int? = null;   // T? admits null
let n = nothing ?? 0;       // an optional needs a fallback...
if nothing != null {        // ...or a test: inside, `nothing` is an Int
    println(nothing + 1);
}
let names: [String] = [];   // an empty array needs its type stated

count = count + 1;          // reassignment
count += 1;                 // also -=, *=, /=, %=
count++;                    // count += 1; a statement, not an expression
{
    let count = 100;        // a block has its own scope
}
```

Most keywords can also be names: `case`, `match`, `in`, `as`, `defer`, `import`, `pub`, `extern`, `struct`, `enum`, `trait` and `impl`. Which one a word is depends on what is around it, so `for case in cases { println(case.name); }` and `let match = pattern.find(text);` read as they look. The words that start a statement or a value (`if`, `while`, `for`, `return`, `let`, `fn`, `true`...) are only ever keywords.

`%` gives the remainder for `Int`s and `Float`s alike: `7.5 % 2.0` is `1.5`, with the sign of the number divided. A text can hold any character by its number, `"caf\u{e9}"`, `"\u{1F600}"`.

A whole number is taken for a `Float` wherever one is expected, and the two work together as `Float`s: `1 + 2.5` is `3.5`, `let half: Float = 1;` holds `1.0`, `16.sqrt()` is fine. Between two whole numbers nothing changes: `7 / 2` is `3`. The other way is never implicit: a `Float` becomes an `Int` through `floor`, `round` and their like, which say how the fraction is lost.

An `Int` holds 48 bits, from -140737488355328 to 140737488355327. Arithmetic that leaves this range stops the program with `integer overflow`; it never wraps around.

### Strings

```rust
let s = "foo" + "bar";      // concatenation
s.len()              // 6
s[0]                        // "f" (a one-character string)
s[1..3]                     // "oo": characters, like the parts of an array
s == "foobar"               // compared by content
"a" < "b"                   // ordered
toString(42) + "!"          // convert other values first
```

A string can hold expressions, written `${...}`. Each is worked out and put in the text as `println` would write it:

```rust
let name = "Ada";
let items = [3, 4];
println("Hello, ${name}!");                                  // Hello, Ada!
println("${items.len()} items, total ${items[0] + items[1]}");  // 2 items, total 7
println("point: ${Point { x: 1, y: 2 }}");                   // point: {x: 1, y: 2}
```

Anything that is an expression can go between the braces: calls, arithmetic, fields, another string with expressions of its own. A `$` that is not followed by `{` is just a dollar sign, and braces without a `$` are just braces. To write the characters `${` themselves, escape the dollar: `"\${name}"`.

A text over several lines goes between `"""` and `"""`:

```rust
let help = """
    usage: todo <command>
      add "title"       quotes need no escaping here
      list              ${count} so far
    """;
```

The line end after the opening quotes and the one before the closing quotes are not part of the text, and what the closing quotes are indented by is taken off the front of every line: the text above starts with `usage` and its second line with two spaces. Put the closing quotes on a line of their own for that. Escapes and `${...}` work as in any text.

### Control flow

```rust
if n < 0 { ... } else if n == 0 { ... } else { ... }

while i < 10 {
    if i == 5 { break; }
    i = i + 1;
}

for item in [1, 2, 3] { ... }     // arrays
for char in "abc" { ... }         // strings
for i, item in items { ... }      // with the position, starting at 0
for i, item in items {
    items[i] = item * 2;          // change the collection through the index;
}                                 // `item = ...` and `i = ...` are errors
for i in 0..10 { ... }            // the numbers 0 to 9: no array is made
for i in 1..=10 { ... }           // 1 to 10, the last included

match n {
    case 0: { println("zero"); }
    case 1 | 2: { println("one or two"); }        // any of several patterns
    case 3..10: { println("a few"); }             // a range, 10 left out
    case 10..=99 if n % 10 == 0: { println("round"); }   // and a condition
    default: { println("many"); }
}
```

An `if` also stands where a value is expected, and gives the value of the branch taken. It then always has an `else`:

```rust
let size = if n > 100 { "large" } else if n > 10 { "medium" } else { "small" };
total += if item.taxed { item.price * 1.2 } else { item.price };

let label = if count > 1 {
    let rounded = count / 10 * 10;      // statements may come first
    "about ${rounded}"                  // the last expression, without ';', is the value
} else {
    return null;                        // a branch that leaves gives nothing
};
```

Both branches give one type (`null` in one makes it optional). What the condition establishes holds in the branch, as in the statement: `if user != null { user.name } else { "nobody" }`, `if shape is Shape.Circle(r) { r } else { 0 }`.


Where a value is expected, a `match` gives the value of the case that runs:

```rust
let word = match n {
    case 0: "zero";
    case 1: "one";
    default: "many";
};

fn area(shape: Shape) -> Float {
    return match shape {
        case Shape.Circle(r): 3.14159 * r * r;
        case Shape.Rect(w, h): w * h;
    };
}
```

Each case is an expression, all of the same type (a `null` among them makes the whole optional), or a block: statements, then the value without a `;`, as in the branch of an `if`. A block may also leave instead: `case 0: { return null; }`, `{ continue; }`, `{ panic("..."); }`. Such a match has to give a value whatever is matched: it handles every variant of an enum, both `true` and `false` of a `Bool`, or has a `default` (`E2028`).

```rust
let size = match bytes > 1024 {
    case true: {
        log.push("large file: ${name}");      // statements first
        "${bytes / 1024} KB"                   // then the value
    }
    case false: "${bytes} B";
};
```

In the braces of a value, an `if` without an `else`, a `match`, a loop are statements like anywhere else; what comes last, without `;`, is the value.

The two ends of a range are whole numbers. Its two ends are worked out once, before the loop starts, and `..` binds looser than arithmetic: `0..count - 1`.

A range can also be kept, like any value: it is then a `Range`, which knows its numbers without holding them.

```rust
let teens = 13..20;
teens.contains(age)                       // true from 13 to 19
teens.len()                               // 7

for i in (0..100).step(10) { ... }        // 0, 10, ... 90
for i in (0..5).reversed() { ... }        // 4, 3, 2, 1, 0
for i in (10..0).step(-2) { ... }         // 10, 8, 6, 4, 2

(1..=6).map(n -> n * n)                   // [1, 4, 9, 16, 25, 36]
(0..20).filter(n -> n % 3 == 0)           // [0, 3, 6, 9, 12, 15, 18]
```

In brackets, because a dot binds tighter than `..`: `0..5.reversed()` would ask the 5. A loop over a `Range` counts, as `for i in 0..10` does: no array is made, whatever its size. The type is written `Range`, with `import std.ops.{Range};`: see [`std.ops`](stdlib.md#stdops).

`defer` sets something aside to run when the block it is written in is left, whichever way: at its end, by `return`, `break`, `continue` or `?`, or because something failed.

```rust
fn export(path: String) -> Result<Int, String> {
    let lock = acquire(path);
    defer lock.release();            // one statement
    defer {                          // or several
        count++;
        println("export over");
    }
    let rows = read(path)?;          // leaves through both
    return Ok(write(rows));
}
```

Several defers of a block run last first. One written in the body of a loop runs at the end of every iteration. More in [errors.md](error-handling.md#cleaning-up-defer).

`&&` and `||` skip their right side when the left side decides the result. `a ?? b` is `a` unless it is `null`.

### Functions

```rust
fn add(a: Int, b: Int) -> Int {
    return a + b;
}

fn log(prefix: String, values: ...Int) -> Void {     // variadic
    println(prefix, values.len(), values);
}

fn first<T>(items: [T]) -> T {                       // generic
    return items[0];
}

fn page(items: [String], number: Int = 1, size: Int = 20) -> [String] {   // values of their own
    return items[(number - 1) * size..number * size];
}

log("got", 1, 2, 3);
first([7, 8]);               // T is inferred from the argument
first<Int>([7, 8]);          // and can always be written out
page(names);                 // page(names, 1, 20)
page(names, 3);              // page(names, 3, 20)
```

The last parameters may have values of their own, which a call that leaves them out gives them. The value is made where the call is, each time: a literal, a constant, a call of a function, but not another parameter. Methods take them too.

Functions are values: they can be stored in variables, passed and returned.

### Bits

Whole numbers have the operators on bits of C: `&`, `|`, `^`, `~`, `<<`, `>>`, and `&=`, `|=` and the rest. Numbers can be written by their bits: `0xFF`, `0b1010`.

```rust
const READ = 1;
const WRITE = 1 << 1;
const EXEC = 1 << 2;

let flags = READ | EXEC;
if flags & WRITE == 0 { ... }       // they bind tighter than a comparison
flags |= WRITE;
flags &= ~READ;

let red = color >> 16 & 0xFF;
```

### Methods on arrays, texts and numbers

The builtin types have the methods one expects, and calls follow one another:

```rust
let revenue = orders.filter(o -> o.paid).map(o -> o.total).sum();
let slug = title.trim().lower().split(" ").filter(w -> w.len() > 2).join("-");
let side = area.sqrt().round();

let shouted = "the quick brown fox"
    .split(" ")
    .filter(word -> word.len() > 3)
    .map(word -> word.upper())
    .join("-");                       // "QUICK-BROWN"
```

Nothing is imported for them: they are part of any program that calls one. They are all listed in [stdlib.md](stdlib.md): arrays under [`std.arrays`](stdlib.md#stdarrays), texts under [`std.strings`](stdlib.md#stdstrings), numbers under [`std.math`](stdlib.md#stdmath).

A program gives the builtin types methods of its own with `impl`, as it does for a struct:

```rust
impl String {
    fn shout() -> String { return self.upper() + "!"; }
}

impl Int {
    fn squared() -> Int { return self * self; }
}

impl<T> [T] {                         // every array; T is what it holds
    fn second() -> T? { return self.at(1); }
}

impl [Order] {                        // the arrays of one type
    fn revenue() -> Int { return self.filter(o -> o.paid).sumOf(o -> o.total); }
}

"hello".shout()         // "HELLO!"
7.squared()             // 49
[1, 2, 3].second()      // 2
orders.revenue()
```

A method of the arrays of one type comes before one of the same name for every array. In another module the methods have to be `pub`, like those of a struct. A trait can be implemented for the builtin types and for every array (`impl<T> Sized for [T]`), and a value seen through the trait finds the right method when the program runs.

One thing to know about a number written out: `-7.abs()` is `-(7.abs())`, as `-x.abs()` would be.

### Chains of functions

Methods follow one another by themselves. For the functions of a program, which are not methods, `value |> f(a, b)` is `f(value, a, b)`: what is on the left of `|>` becomes the first argument of the call on its right, and calls that would nest read in the order they happen:

```rust
fn paidOnly(orders: [Order]) -> [Order] { return orders.filter(o -> o.paid); }
fn totals(orders: [Order]) -> [Int] { return orders.map(o -> o.total); }
fn withTax(amount: Int, percent: Int) -> Int { return amount + amount * percent / 100; }

let due = orders |> paidOnly |> totals() |> report.sum() |> withTax(20);

// the same, nested
withTax(report.sum(totals(paidOnly(orders))), 20)
```

A function of one argument needs no parentheses: `orders |> paidOnly`. A `?` after the call is asked of what the call returns: `line.trim() |> parse()?`.

It binds looser than arithmetic and `??`, and tighter than comparisons, so these read as they look:

```rust
a + b |> clamp(0, 100)            // clamp(a + b, 0, 100)
if orders |> count == 3 { }       // count(orders) == 3
name ?? "" |> tidy                // tidy(name ?? "")
```

It is only another way to write the call: the types are checked, and mistakes are reported, as for the call it stands for.

### Arrays

```rust
let xs = [10, 20, 30];
xs[0] = 11;                 // replace an element
xs.push(40);               // grow
xs.len()                    // 4
```

A range between the brackets takes a part of the array, as a new array. It may leave out where it starts or where it ends:

```rust
let xs = [10, 20, 30, 40, 50];
xs[1..3]                    // [20, 30]: from 1, up to 3 left out
xs[1..=3]                   // [20, 30, 40]
xs[2..]                     // [30, 40, 50]: to the end
xs[..2]                     // [10, 20]: the first two
xs[(0..5).step(2)]          // [10, 30, 50]: a range kept as a value works too
```

Positions outside the array are left out, so `xs[3..10]` is the last two and `xs[9..]` is empty. Texts are taken apart the same way, by characters: `"déjà vu"[..4]` is `"déjà"`. A part cannot be assigned to: `xs[0..2] = ...` is an error.

A dictionary is written with its entries between braces. With texts for keys it is a `Dict`, with keys of any other type, numbers, tuples, enums, structs, a `Map`:

```rust
let ages = {"ada": 36, "alan": 41};        // Dict<Int>
ages["grace"] = 85;
let seats = {(1, 1): "ada", (1, 2): "alan"};   // Map<(Int, Int), String>
let empty: Map<Color, Int> = {};          // an empty one needs its type
```

See [`std.dict`](stdlib.md#stddict) and [`std.map`](stdlib.md#stdmap) for what they do.

What is done to an array is a method of it: see [Methods on arrays, texts and numbers](#methods-on-arrays-texts-and-numbers), and [`std.arrays`](stdlib.md#stdarrays) for all of them.

An array keeps the element type it was made with. Since it is changed in place, through every name it has, an `[Int]` is never taken for an `[Int?]`, which could be given a `null`, nor an array of `Square` for an array of the trait `Shape`, which could be given a `Circle`. A function that only reads takes any array of a trait with a type parameter, and a new array of the wider type is a `map` away:

```rust
fn total<T: Shape>(items: [T]) -> Int { ... }      // [Square], [Circle] and [Shape] alike
let shapes: [Shape] = squares.map(s -> s as Shape);  // a new array, which may hold any Shape
let maybe: [Int?] = [1, 2];                          // a literal is made with the type it is for
```

All elements have the same type. Reading or writing an index that does not exist is a runtime error; `xs[i] = v` replaces an element and never grows the array, `push` does. `xs.pop()`, `xs.removeAt(i)`, `xs.insertAt(i, v)` and `xs.clear()` take items out of the array itself, or put one in the middle: see [`std.arrays`](stdlib.md#stdarrays).

### Structs, traits and methods

```rust
struct Point { x: Int; y: Int; }

trait Shape {
    fn area() -> Int;
}

impl Shape for Point {
    fn area() -> Int { return self.x * self.y; }     // `self` is implicit
}

impl Point {                                         // methods outside a trait
    fn moved(dx: Int) -> Point { return Point { x: self.x + dx, y: self.y }; }
    static fn origin() -> Point { return Point { x: 0, y: 0 }; }   // a function of the type
}

fn show(s: Shape) -> Void { println(s.area()); }     // only structs implementing Shape

let p = Point { x: 2, y: 3 };
p.x = 5;
show(p.moved(1));
show(Point.origin());
```

A field is as visible as its struct. One written `priv` is its module's: other modules do not read it, change it or give it in a literal, and go through what the struct offers. A struct whose `priv` fields all have values of their own can still be written as a literal elsewhere; one that cannot is made by a `static fn`.

```rust
pub struct Account {
    owner: String;
    priv balance: Int = 0;
}
```

A `static fn` of an `impl` is a function of the type: it has no `self`, and is called on the type, `Point.origin()`, never on a value. It takes the type parameters of the `impl` (`Pair.twice(1)` is a `Pair<Int>`), and from another module it is `geo.Point.origin()`, or `Point.origin()` after `import geo.{Point};`, when it is `pub`. A method that never uses `self` is fine; the compiler only says it could be `static` (`E8011`).

Structs are references: `let q = p;` makes `q` and `p` the same value.

There is more to both, all of it in [traits.md](traits.md):

```rust
struct Config { name: String; port: Int = 8080; tags: [String] = []; }   // fields with a value of their own
let config = Config { name: "api" };

trait Entrant {
    fn name() -> String;
    fn line() -> String { return "* ${self.name()}"; }      // a method every implementer gets
}

trait Source<T> { fn next() -> T?; }                        // a trait with a type parameter

fn largest<T: Ordered>(items: [T]) -> T? { ... }            // T has to implement Ordered

impl Add for Money { fn plus(other: Money) -> Money { ... } }   // gives Money the + operator
let total = price + tax;

let lookup = ages.get;                                      // a method as a value
let names = lines.map(String.trim);                         // a method of a type: fn(String) -> String
let sorted = items.sorted(Item.cheaper);                    // fn(Item, Item) -> Bool, the value first
```

A method named by its type, `String.trim`, `Point.plus`, `Shape.area` for a trait, is a function that takes the value first and the method's arguments after it; `Point.norm(p)` is `p.norm()`. A method of a value, `ages.get`, is a function of the method's arguments, called on that value.

The traits of `std.ops` give a struct the syntax of the builtin types: `==` and `<`, `+ - * /`, `for item in it`, `it[key]`, and how it reads in `println` and `"${...}"`.

Inside the header of `if`, `while`, `for` and `match`, wrap a struct literal in parentheses: `if (Point { x: 1, y: 2 }).area() > 0 { ... }`. A pattern after `is` is never one: in `if shape is Shape.Empty { ... }` the brace opens the block.

### Enums

An enum is a type whose values are one of several variants, each with the values it holds:

```rust
enum Shape {
    Circle(Float),
    Rect(Float, Float),
    Empty,
}

fn area(shape: Shape) -> Float {
    return match shape {
        case Circle(r): 3.14 * r * r;
        case Rect(w, h): w * h;
        case Empty: 0.0;
    };
}

let shapes = [Circle(1.0), Rect(2.0, 3.0), Empty];
for s in shapes { println(s, area(s)); }      // Circle(1) 3.14 ...
```

A variant is written alone, `Circle(1.0)`, when an enum the file declares or imports by name has it; `Shape.Circle(1.0)` says the same and is always there. When two enums in scope have a variant of the same name, the one the value has to be is taken: a `Shape` where a `Shape` is expected, the type of the value a `match` or an `is` looks at. Where nothing says, the compiler asks for `Shape.Empty` or `Slot.Empty`. A variable or a function of that name hides the variant.

A `match` names what a variant holds (`r`, `w`, `h`) for the length of its case. It has to handle every variant, or have a `default`: leave `Empty` out and the program does not compile. That is what makes an enum safer than a number or a text standing for a kind: add a variant, and the compiler finds every place that has to deal with it.

Patterns can look inside values that are enums themselves, and match literals:

```rust
enum Expr { Num(Int), Add(Expr, Expr), Neg(Expr) }

fn simplify(e: Expr) -> Expr {
    return match e {
        case Add(Num(0), right): right;    // 0 + x
        case Neg(Neg(inner)): inner;       // --x
        default: e;
    };
}
```

Enums can be generic, and have methods and traits like structs:

```rust
enum Result<T, E> { Ok(T), Err(E) }

fn divide(a: Int, b: Int) -> Result<Int, String> {
    if b == 0 { return Err("division by zero"); }
    return Ok(a / b);
}

match divide(10, 0) {
    case Ok(n): { println(n); }
    case Err(message): { println("failed:", message); }
}
```

When only one variant matters, `is` asks for it without a whole `match`, and names what it holds for the block that follows:

```rust
if shape is Shape.Circle(r) {
    println("a circle of radius", r);
} else if shape is Shape.Rect(w, h) && w == h {       // the names serve the rest of the condition
    println("a square of side", w);
}

while reader.next() is Ok(line) { lines.push(line); }  // until the first failure

let round = shape is Shape.Circle;                    // a Bool like any other
```

What follows `is` is what follows `case`: a variant, with a name, `_`, a literal or another pattern for each of its values, or a tuple of those. The names exist where the test is known to have held: in the rest of an `&&`, and in the block of the `if` or `while`. Not in the `else`, and not after `!` or `||` (`E1003`): write `_` there. More in [enums.md](enums.md#one-variant-is).

Values of an enum are equal when they are the same variant holding equal values. More in [enums.md](enums.md).

A value of an enum never changes once made, so it is also a value of the same enum with wider type arguments: a `Result<Lock, String>` is a `Result<Lock?, String>`, and `return Ok(lock);` is fine in a function that returns the second. A struct's fields can be assigned, so a `Box<Int>` is not a `Box<Int?>`.

### Constants

A value with a name, declared outside any function:

```rust
const WIDTH = 80;
const HEIGHT = WIDTH / 4;
const TITLE = "report";
const RATIO: Float = 1.5;
```

The value is made of literals, operators and other constants, and is worked out when the program is compiled. It cannot be assigned. Order does not matter: a constant can use one declared further down.

A constant can also be a struct, an array or a value of an enum, made of constants:

```rust
const RED = Color { r: 230, g: 41, b: 55 };
const PRIMES = [2, 3, 5, 7];
const START = Light.Red;
const PALETTE = [RED, Color { r: 0, g: 0, b: 0 }];
```

Such a constant is made once, the first time the program uses it, and is that one value everywhere: reading it costs what reading a variable costs, however large it is. So nothing may change it. `RED.r = 0;` is refused when the program is compiled, and a write through another name (`let mine = RED; mine.r = 0;`, or `push` in a function it was given to) stops the program where it happens. To change something, make a copy and change that: `PRIMES.copy()`, or a new struct with the fields of the constant.

What a function works out is not a constant: write the function, and call it.

### Variables outside functions

A `let` outside any function, in a file with a `main` or in a module, declares a variable every function of the file sees and can change:

```rust
let nextId = 1;
let cache = dict.empty<Int>();

fn issue() -> Int {
    nextId += 1;
    return nextId - 1;
}
```

The variables are made before `main` starts, in the order they are written; one that uses another makes that one first. A variable of a module is the module's: another module reaches it through the module's functions. Since any function may change it, a test of one against `null` says nothing about the next line: use `?.`, `!`, or copy it into a local variable first.

In a script, a file without a `main`, the statements outside functions are what runs, and their variables are those of the script, which its functions do not see.

### Generic structs

```rust
struct Stack<T> { items: [T]; }

impl<T> Stack<T> {                       // T is the struct's parameter
    fn push(item: T) -> Void { self.items.push(item); }
    fn peek() -> T? {
        if self.items.len() == 0 { return null; }
        return self.items[self.items.len() - 1];
    }
    fn map<U>(f: fn(T) -> U) -> Stack<U> {   // U is the method's own parameter
        let out = Stack<U> { items: [] };
        for item in self.items { out.push(f(item)); }
        return out;
    }
}

let numbers = Stack<Int> { items: [] };
numbers.push(1);
let top = numbers.peek();                // Int?
let texts = numbers.map(toString);       // Stack<String>; U is inferred
```

### Optionals and casts

```rust
fn length(name: String?) -> Int {
    if name == null { return 0; }        // from here on, name is a String
    return name.len();
}

let boxed: Any = 41;
let n = (boxed as Int) + 1;              // checked when it runs

fn side(shape: Shape) -> Int {
    return (shape as Square).side;       // fails at runtime for a Circle
}
```

`value is Type` asks the same question without failing, and inside the branch the value has that type. (With the variant of an enum after it, `value is Shape.Circle(r)`, it takes the value apart as well: see [Enums](#enums).)

```rust
fn describe(value: Any) -> String {
    match value {                        // a match on types reads best
        case Int: { return "number " + toString(value + 1); }
        case String: { return "text of " + toString(value.len()); }
        case Shape: { return "shape of area " + toString(value.area()); }
        default: { return "something else"; }
    }
}

if shape is Square { println(shape.side); }
```

Null tests work on fields as well: after `if node.next == null { return; }`, `node.next` is a `Node`.

What every way through an `if` ends with holds after it. A value filled in when it is missing is there afterwards, whichever branch ran:

```rust
let found = cache.get(name);       // [Int]?
if found == null {
    found = load(name);            // [Int]
    cache.set(name, found);
}
return found;                      // [Int]: either it was there, or it is now
```

Three operators save most of those tests:

```rust
let city = user?.address?.city;       // String?: null if there is no user, or no address
let name = user?.name ?? "nobody";    // with a fallback
user?.rename("Ada");                  // called only if there is a user

fn port(config: Config) -> Int? {
    let text = config.port?;          // the value, or return null from port right here
    return text.parseInt()?;
}
```

*   **`a ?? b`** is `a`, or `b` when `a` is null. When `b` may be null too the result may be: `a ?? b ?? 0` tries `a`, then `b`, then gives `0`.
*   **`value?.member`** reads a field or calls a method when the value is not null, and gives null when it is. All that follows on the same chain is done with the value too: in `user?.address.city`, the city is read when there is a user. A step that may itself be null needs a `?.` of its own, as `address` above. On a value that can never be null it is an error (`E2035`): use a dot.
*   **`value?`** after an expression gives the value, and returns `null` from the enclosing function if there is none. The function has to be one that may return null, or one that returns nothing, which it then simply ends. The same `?` passes on the failure of a [`Result`](#failures).
*   **`value!`** gives the value where you know there is one: `users.get("ada")!`, `"42".parseInt()!`. If there is none after all, the program stops at that line, as a `panic` stops it (defers run, `attempt` catches it). It works on a `Result` too, stopping with the failure. On a value that can never be null it is an error.

Every optional also has the methods of [`std.option`](stdlib.md#stdoption), with no import, for a chain of steps that may each find nothing:

```rust
let manager = users.get(name).then(user -> user.manager);        // String?
let shout = nickname.map(n -> n.upper()) ?? "nobody";
let port = io.env("PORT").expect("PORT is not set");             // String, or stop with that
let user = users.get(name).okOr("no user called ${name}")?;      // as a Result, passed on
```

`map`, `then`, `filter`, `mapOr`, `or`, `orElse`, `each`, `expect`, `okOr`, `isSome` and `isNone`. A program can give optionals methods of its own the same way the library does: `impl<T> T? { fn shown() -> String { ... } }`, where `self` is a `T?`.

What a test established about a variable also holds inside a function written after it, as long as nothing ever assigns the variable again:

```rust
fn tagged(notes: [Note], tag: String?) -> [Note] {
    if tag == null { return notes; }
    return notes.filter(note -> note.tags.contains(tag));   // tag: String
}
```

A value of type `Any` cannot be used until it is cast or tested. A cast that cannot possibly succeed (`1 as String`) is a compile error; one that can fail stops the program with `cannot cast String to Int` and the source location.

Casts look inside the value: `v as [Int]` checks every element and reports the first that does not fit (`element 2: String is not an Int`), and `v as Box<Int>` checks the fields that depend on the type argument.

### Tuples

A tuple is a few values together, without a struct to name them: what a function returns when it has two things to say.

```rust
fn divide(a: Int, b: Int) -> (Int, Int) {
    return (a / b, a % b);
}

let (quotient, rest) = divide(17, 5);       // each value gets a name
let both = divide(17, 5);
println(both, both.0, both.1);              // (3, 2) 3 2

for (name, age) in arrays.zip(names, ages) { ... }
for i, (name, age) in pairs { ... }
```

*   The type is written like the value: `(Int, String)`, `[(String, Int)]`, `(Int, Int)?`.
*   Values are read by position, from 0 (`pair.0`), or all named at once with `let (a, b) = pair;`, where `_` skips one.
*   Two tuples are equal when they hold equal values, and a tuple prints as it is written.
*   A tuple cannot be changed once made; make a new one.
*   A `match` takes one apart, and is checked for completeness as for an [enum](enums.md):

```rust
match (item.quantity < 10, item.supplier != null) {
    case (true, true): { return "reorder"; }
    case (true, false): { return "find a supplier"; }
    case (false, _): { return "ok"; }
}
```

Two to six values. Beyond three, or when the values travel far from where they are made, a struct says more: `point.x` reads better than `point.0`.

### Closures

A function can be written where it is used. Its long form says every type; its short form says none, and takes them from what it is handed to:

```rust
let names = people.map(p -> p.name);                  // p is a Person, the result a String
let adults = people.filter(p -> p.age >= 18);
let total = people.fold(0, (sum, p) -> sum + p.age);  // several parameters go in parentheses
twice(() -> count++);                                         // none at all

let double: fn(Int) -> Int = n -> n * 2;                      // a variable or a field of a function type
lines.each(line -> {                                  // several statements go in a block
    let clean = line.trim();
    if clean != "" { kept.push(clean); }
});
```

*   The arrow is the one of `fn(n: Int) -> Int`: what is on its left gives what is on its right.
*   After it comes one expression, which is what the function gives; or one assignment, or a block.
*   It works wherever a function type is expected: an argument, a variable or a field declared with one, a `return`. Where nothing says what the parameters are (`let f = x -> x + 1;`), it is an error (`E2037`) and the long form is the one to write: `fn(x: Int) -> Int { return x + 1; }`.
*   A function in the long form given to a `let` sees its own name, and can call itself: `let walk = fn(folder: Folder) { for inner in folder.folders { walk(inner); } };`. The short form does not, its types coming from outside.
*   For a generic function, the other arguments say what the parameters are, and the body says what the result is: in `people.map(p -> p.name)`, `T` is `Person` from the array and `U` is `String` from `p.name`.

```rust
fn makeCounter() -> fn() -> Int {
    let count = 0;
    return fn() -> Int {
        count = count + 1;      // captured by reference
        return count;
    };
}

let next = makeCounter();
next();     // 1
next();     // 2
```

A closure shares the variables it captures with the function that declared them, and keeps them alive after that function returns.

In a loop, each iteration has its own variables, so closures made in different iterations do not interfere:

```rust
let readers: [fn() -> Int] = [];
for v in [1, 2, 3] {
    readers.push(fn() -> Int { return v; });
}
readers[0]();     // 1, not 3
```

### Programs in several files

A file is a module. Other files use it through `import`, with the path from the folder of the file you run:

```
shop/
  main.qi
  stock.qi
  util/
    text.qi
```

```rust
// main.qi
import stock;                    // stock.qi
import util.text;                // util/text.qi
import util.text.{shout};        // or just a name of it

fn main() -> Int {
    let s = stock.open();
    println(text.shout("open"), shout("really"));
    return 0;
}
```

```rust
// util/text.qi
pub fn shout(s: String) -> String { return upper(s) + "!"; }

fn upper(s: String) -> String { return s; }     // no 'pub': only this file can call it
```

*   Only what is declared with `pub` can be used from another file: `pub fn`, `pub struct`, `pub trait`, and `pub fn` for methods.
*   A module is named by the last part of its path (`text.shout`). Use `import util.text as t;` to pick another name.
*   Paths always start at the folder of the file you run, in every file. A folder is not a module: `import util;` needs a file `util.qi`.
*   `qoqi run shop/main.qi` is all it takes; imported files are found and compiled automatically.

The details, including how a file can pass names on with `pub import`, are in [modules.md](modules.md).

### Projects

A `qoqi.json` at the top of a program's folder says where it starts, so it can be run without naming a file, and where its modules are:

```json
{
    "entry": "src/main.qi",
    "root": "src",
    "packages": { "textkit": "../libs/textkit" },
    "max_depth": 20000
}
```

```bash
qoqi run            # in the folder, or anywhere below it
qoqi run path/to/project
qoqi serve          # the flows of the project
```

`root` is where import paths start (default: the folder of `qoqi.json`). `packages` gives a name to a folder of modules kept elsewhere: `import textkit.format.pad;` reads `../libs/textkit/format/pad.qi`. Packages with versions, downloaded from git, are listed under `require` and installed with `qoqi pkg install` ([packages.md](packages.md)). `heap_mb` and `max_depth` set the limits described under [Commands](#commands). All keys are optional. See [project.md](../configuration.md).

### Builtin functions

Available everywhere, without an import:

| Function | Description |
|----------|-------------|
| `print(values...)` | Write the values separated by spaces |
| `println(values...)` | Same, followed by a newline |
| `toString(value)` | The text `print` would write, as a `String` |
| `panic(values...)` | Stop the program with the values as its message |

### The standard library

Everything else comes from the library built into `qoqi`. Its modules need no import: a file that writes `io.ask(...)` or `dict.empty<Int>()` has `std.io` or `std.dict`, unless it declares something of its own by that name (a variable of that name inside a function just hides the module there). The same goes for the types `Result`, `Dict`, `List` and `Set`, and for the traits of operators, `Ordered`, `Equal`, `Show` and the rest. An `import` is still how a module of the program or of a package comes in, and how a library module gets another name: `import std.strings as text;`.

```rust
fn main() -> Int {
    let line = io.ask("Numbers, separated by spaces: ") ?? "";

    let numbers: [Int] = [];
    for word in line.words() {
        let n = word.parseInt();       // Int?: null when the word is not a number
        if n != null { numbers.push(n); }
    }

    println("sum", numbers.sum(), "largest", numbers.largest() ?? 0);
    println("root of the sum", numbers.sum().sqrt());
    return 0;
}
```

| Module | For |
|--------|-----|
| `std.math` | `abs`, `min`, `max`, `pow`, `sqrt`, `floor`, `round`, `toFloat`, ... |
| `std.strings` | `split`, `join`, `substring`, `indexOf`, `replace`, `trim`, `upper`, `parseInt`, ... |
| `std.arrays` | `range`, `fill`, `slice`, `map`, `filter`, `fold`, `sorted`, `contains`, `sum`, ... |
| `std.list` | `List<T>`: an array with methods (`add`, `removeAt`, `sort`, `map`, ...) |
| `std.dict` | `Dict<V>`: values by a text key (`set`, `get`, `has`, `keys`, ...); `{"ada": 36}` |
| `std.map` | `Map<K, V>`: values by a key of any type; `{(1, 2): "seat"}` |
| `std.option` | Chaining on values that may be `null`: `map`, `then`, `mapOr`, `orElse`, ... |
| `std.result` | `Result<T, E>` for failures with a reason; `attempt` to survive one that stops the program |
| `std.io` | `readLine`, `readFile`, `writeFile`, `exists`, `env`, ... |
| `std.time` | `now`, `sleep`, `stopwatch` |
| `std.random` | `below`, `between`, `pick`, `shuffle`, `seed` |

A function that may have no answer returns an optional, so the checker makes sure the program handles "none": `indexOf` returns `Int?`, `list.first()` returns `T?`. All functions are described in [stdlib.md](stdlib.md).

## Reading Errors

### Compile errors

```text
[ERROR] E2001: cannot assign type int to string
  --> main.qi:8:25
   |
 8 |     let label: String = double(4);
   |                         ^^^^^^
   |
   = Suggestion: Ensure the value is of type string
```

The code (`E2001`) identifies the kind of error; [compilation/diagnosis.md](../../internals/compilation/diagnosis.md) lists them all. The first digit tells which stage found it: `0` syntax, `1` names, `2` types, and so on.

The compiler reports the errors of one stage at a time. After fixing syntax errors you may see type errors that were not shown before; that is expected.

### Warnings

```text
[WARNING] E2023: unreachable code: the statement before it always leaves this block
  --> main.qi:14:5
```

A warning does not stop compilation: the program runs, and the exit code is not affected.

What nothing uses is said too: a variable, a parameter, a name a pattern gives (`E1004`), an import (`E8010`). Start a name with `_` (`_unit`, or `_` alone in a pattern) when it is there on purpose; the parameters a trait or a short function asks for are not counted, nor is anything of the library.

### Runtime errors

A program that fails while it runs is reported the same way, with the calls that led there:

```text
[ERROR] R0002: index out of bounds: 7 (length 3)
  --> main.qi:3:17 in function 'pick'
     |
 3 |     return items[index];
     |                 ^
     |
     = Suggestion: Compare the index with the length (items.len()) before using it, or use items.at(index), which gives null instead of failing
  called from main.qi:8:17 in function 'main'
```

The program stops and exits with code 1. A program stops itself the same way with `panic("what went wrong", value);`.

### Failures

A function whose failure is part of its job returns a `Result`: the value, or the reason there is none. The caller has to look.

```rust
import std.io;

match io.readFile("notes.txt") {
    case Ok(text): { println(text); }
    case Err(error): { println(error.describe()); }
}
```

Most often the caller has nothing better to do with a failure than to hand it to its own caller. `?` does that: it gives the value, or returns the failure from the enclosing function.

```rust
fn order(row: Dict<String>) -> Result<Order, Flaw> {
    let number = whole(row, "order")?;          // an Int, or order() returns the Flaw here
    let quantity = whole(row, "quantity")?;
    return Ok(Order { number: number, quantity: quantity });
}
```

The function has to return a `Result` with the same kind of failure; one of another kind is turned into it first with `mapErr(...)?`.

A failure that should never happen stops the program; `attempt` is for the few places that must survive it, and gives it back as a value with its message, kind and call stack:

```rust
import std.result.{attempt};

match attempt(fn() -> Int { return job(); }) {
    case Ok(n): { println(n); }
    case Err(failure): { println("failed:", failure.describe()); }
}
```

All of it is in [errors.md](error-handling.md).
