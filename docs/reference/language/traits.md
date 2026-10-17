# Traits and Structs

A trait says what a type can do, without saying how. Types that implement it can then be used wherever the trait is asked for, each doing it its own way.

```rust
trait Shape {
    fn area() -> Float;
}

struct Circle { radius: Float; }
impl Shape for Circle {
    fn area() -> Float { return 3.14159 * self.radius * self.radius; }
}

struct Square { side: Float; }
impl Shape for Square {
    fn area() -> Float { return self.side * self.side; }
}

let shapes: [Shape] = [Circle { radius: 1.0 }, Square { side: 2.0 }];
for shape in shapes { println(shape.area()); }
```

A complete program: [`test/e2e/cases/traits.qi`](../../../test/e2e/cases/traits.qi) works out the standings of a tournament with everything on this page.

## Structs

```rust
struct Config {
    name: String;
    host: String = "localhost";     // a field with a value of its own
    port: Int = 8080;
    tags: [String] = [];
    owner: String? = null;
}

let config = Config { name: "api" };            // the rest is as the struct says
let other = Config { name: "db", port: 5432 };
```

**A field with a value of its own** may be left out of a literal. The value is any expression that makes sense outside a function: literals, constants, calls, other literals. It is worked out again for each struct made, so two configs do not share one `tags` array. A field without one has to be given, as before.

**Methods** are written in an `impl`. Inside, `self` is the value:

```rust
impl Config {
    fn address() -> String { return "${self.host}:${self.port}"; }
    pub fn rename(to: String) { self.name = to; }       // pub: callable from other modules
}
```

**A method without its call is a function**, bound to the value it was taken from:

```rust
let ages = dict.empty<Int>();
let names = candidates.filter(ages.has);      // fn(String) -> Bool
let lookup = ages.get;                                // fn(String) -> Int?
```

The value has to be a variable or a field of one, and it is read when the function is called: assign another dictionary to `ages`, and `lookup` looks there. A method with type parameters of its own cannot be taken this way.

**A field that holds a function** is called like a method: `button.onClick(label)`.

## Traits

```rust
trait Entrant {
    fn name() -> String;
    fn score() -> Int;

    // A method with a body: every type that implements the trait has it,
    // unless it writes its own
    fn line() -> String { return "${self.name()}: ${self.score()}"; }
    fn leads(other: Self) -> Bool { return self.score() > other.score(); }
}
```

*   A method without a body has to be written by every `impl`.
*   A method **with a body** is there for all of them. It can call the other methods of the trait, and an `impl` may replace it.
*   `Self` is the type that implements the trait: `leads` of a `Player` takes a `Player`.
*   The methods of a trait impl are always visible from other modules; the trait itself needs `pub` to be named there.
*   A type implements a given trait once.

Traits can be implemented for structs, for enums, for the builtin types `Int`, `Float`, `String` and `Bool`, and for every array (`impl<T> Sized for [T] { ... }`):

```rust
impl Entrant for Int {
    fn name() -> String { return "number ${self}"; }
    fn score() -> Int { return self; }
}

7.line()        // "number 7: 7"
```

A number or a text gets a method through a trait someone wrote, or through `impl Int { ... }`, `impl String { ... }`: that is how `std.time` gives every number `days()`, so that `2.days()` is a `Duration` ([language-spec.md](spec.md)).

## Traits with Type Parameters

```rust
trait Source<T> {
    fn next() -> T?;

    fn all() -> [T] {
        let out: [T] = [];
        let item = self.next();
        while item != null { out.push(item); item = self.next(); }
        return out;
    }
}

struct Counter { n: Int; }
impl Source<Int> for Counter {
    fn next() -> Int? { self.n++; return self.n; }
}

struct Repeat<T> { item: T; }
impl<T> Source<T> for Repeat<T> {
    fn next() -> T? { return self.item; }
}
```

`Source<Int>` and `Source<String>` are two types. A function that takes `Source<Int>` takes a `Counter` and a `Repeat<Int>`, not a `Repeat<String>`. Where the type argument is a type parameter, it is worked out from the value:

```rust
fn drain<T>(source: Source<T>) -> [T] { return source.all(); }

drain(Counter { n: 0 })             // [Int]
drain(Repeat<String> { item: "x" }) // [String]
```

## Asking Something of a Type Parameter

Inside `fn f<T>(x: T)` nothing is known of a `T`, so nothing can be done with `x` but pass it on. A requirement says what `T` has to implement, and that is then what can be called:

```rust
import std.ops.{Ordered, Show};

fn largest<T: Ordered>(items: [T]) -> T? {
    let best = items.first()?;
    for item in items {
        if item > best { best = item; }        // T is Ordered
    }
    return best;
}

fn clampTo<T: Ordered + Show>(value: T, low: T, high: T) -> String { ... }   // several, with +
```

Calling it with a type that does not implement the trait is an error where the call is, that names both:

```
[ERROR] E2036: Point does not implement Ordered, which function 'largest' requires of T
   = Suggestion: Add: impl Ordered for Point { ... }
```

Requirements go wherever type parameters are introduced:

```rust
struct Sorted<T: Ordered> { items: [T]; }              // only of ordered things
enum Tree<T: Ordered> { Leaf, Node(Tree<T>, T, Tree<T>) }

impl<T: Show> Show for Box<T> { ... }                  // a Box of something shown can be shown
impl<T: Ordered> Box<T> { fn bigger(other: T) -> Bool { ... } }   // methods only some boxes have

fn firstOf<T, S: Source<T>>(source: S) -> T? { ... }   // one parameter in the requirement of another
```

`impl<T: Ordered> Box<T>` gives its methods to boxes of ordered things only: `Box<Point>` exists, and calling `bigger` on it is `E2036`.

## Operators, Loops and Brackets

The module `std.ops` declares the traits that stand behind the syntax. Implement one, and the type has the syntax:

| Trait | Method | Gives |
|-------|--------|-------|
| `Equal` | `equals(other: Self) -> Bool` | `a == b`, `a != b` |
| `Ordered` | `compare(other: Self) -> Int` | `a < b`, `a <= b`, `a > b`, `a >= b`; also `isBefore`, `isAfter` |
| `Add`, `Subtract`, `Multiply`, `Divide` | `plus`, `minus`, `times`, `dividedBy` | `a + b`, `a - b`, `a * b`, `a / b`, and `a += b` |
| `Show` | `show() -> String` | what `println(a)`, `toString(a)` and `"${a}"` write |
| `Iterable<T>` | `iterate() -> [T]` | `for item in a { ... }` |
| `Indexed<K, V>` | `at(key: K) -> V` | `a[key]` |
| `IndexedSet<K, V>` | `put(key: K, value: V)` | `a[key] = value;` |

```rust
import std.ops.{Equal, Ordered, Add, Show};

struct Money { cents: Int = 0; }

impl Equal for Money { fn equals(other: Money) -> Bool { return self.cents == other.cents; } }
impl Ordered for Money { fn compare(other: Money) -> Int { return self.cents - other.cents; } }
impl Add for Money { fn plus(other: Money) -> Money { return Money { cents: self.cents + other.cents }; } }
impl Show for Money { fn show() -> String { return "${self.cents / 100}.${format.padded(self.cents % 100, 2)}"; } }

let total = Money { };
for price in prices { total += price; }
if total > limit { println("over by ${total}"); }
```

*   `compare` gives a number below zero when the value comes first, zero when neither does, above zero when it comes after.
*   **Without `Equal`, two structs are equal when their fields are.** With it, `==` asks the struct, and so do `contains`, `indexOf` and `test.equal`, which are written with `==`, and so does `==` on an array or an enum that holds the struct. Whether two names are of the one value is `reflect.identical(a, b)`.
*   A comparison with `null` is always a test for null, whatever the type implements.
*   The key of `Indexed` is whatever the type asks for: a grid can be indexed by a pair, `grid[(2, 3)]`.
*   `Int`, `Float`, `String` and `Bool` implement `Equal` and `Show`, and all but `Bool` implement `Ordered`: a function that requires those takes numbers and texts too.

The standard library uses them itself: a `List` and a `Dict` take brackets and loops, a `Set` loops and compares, and two `Json` documents are `==` when they say the same.

`std.ops` also has what can be written once for every type that implements them: `max`, `min`, `largest`, `smallest`, `sorted`, `contains`, `indexOf`, `same`, `join`.

### What `Show` does not reach

`show` is used where a value is turned into text directly. A value inside another, an array of them or a field of a struct that has no `show` of its own, is written the way any value is: `println([price])` prints `[{cents: 1250}]`. Use `ops.join(prices, ", ")`.

## What Is Not There

*   **Traits for tuples, optionals or functions**, and a trait for the arrays of one type only (`impl Sized for [Int]`): what an array holds is not known when the program runs. Methods without a trait are there for all of them: `impl String { ... }`, `impl<T> [T] { ... }`, `impl [Int] { ... }`.
*   **Two impls of one trait for one type**: `impl Source<Int>` and `impl Source<String>` for the same struct.
*   **Types a trait leaves to its impls** (`type Item;`): a trait says them as type parameters, `Iterable<T>`.
*   **Functions of a type rather than of a value** (`Point.origin()`): write a function next to the struct.
*   **Equality by content inside other values.** Two tuples, or two values of an enum, holding structs are compared by the machine, which does not ask the structs. `Equal` for the outer type, written with a `match`, does (see `Json` in `std.json`).
*   **Operators beyond the four**: no `%`, no unary `-`, and `a[k] += v` on a struct has to be written out.
*   **A value of a builtin type tested for a trait when the program runs**: `value is Ordered` is false for a number, though numbers are `Ordered`.
*   **Fields hidden from other modules**: a `pub struct` shows all of them.

## How It Is Compiled

**Methods with a body in a trait** are copied into every impl that does not write them, before anything else looks at the program. From there they are methods of the impl like the others, checked with the type the impl is for. In a trait with type parameters the copy has the types the impl gives them.

**A requirement** is checked by the type checker, at every place type arguments are given or worked out. What it changes afterwards depends on where the type parameter is:

*   A generic *function* is compiled once for each choice of types (see [monomorphization](../../internals/compilation/monomorphization.md)). In the copy for `Int`, `item > best` is the comparison of two numbers; in the copy for `Version`, it is a call of `Version.compare`. Nothing is looked up when the program runs.
*   The methods of a generic *struct* or *enum* are compiled once for all type arguments. There, a call on a `T` asks the value for the method: a struct or an enum value carries the name of its type, which is what a call through the trait itself goes by too.

**A number or a text carries nothing**, so a call that is decided when the program runs first tests the value for being of a builtin type that has a method of that name, and calls that type's function directly. The test is only made for method names that some builtin type has.

**Operators, loops and brackets** are rewritten by the desugarer into the method calls of the table above, at the places the type checker found a type that implements the trait. `a != b` is `!a.equals(b)`, `a < b` is `a.compare(b) < 0`, `a += b` is `a = a.plus(b)`, `for x in a` is `for x in a.iterate()`. This happens after generic functions are instantiated, which is why `contains`, written long before with a plain `==`, compares structs by content in its copy for a type that implements `Equal`.

**A method as a value** becomes the function that calls it, `fn(key: String) -> Int? { return ages.get(key); }`, with the types the checker found; **a field's own value** is added to each literal that leaves the field out.

**Traits with type parameters** leave nothing behind when the program runs: `Source<Int>` and `Source<String>` are the same lookup of `next` on a value.
