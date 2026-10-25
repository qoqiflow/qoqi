# The Standard Library

The library is built into `qoqi`. There is nothing to install and no project file is needed: any program can import its modules.

```rust
import std.dict;

fn main() -> Int {
    let counts = dict.empty<Int>();
    for word in "to be or not to be".words() {
        counts.set(word, (counts.get(word) ?? 0) + 1);
    }
    println(counts.keys(), counts.values());      // ["to", "be", "or", "not"] [2, 2, 1, 1]
    return 0;
}
```

Whole programs built on it: a [report on a web server's log](../../../examples/logs/main.qi), a [sales report](../../../examples/sales/main.qi) from a CSV file, [invoices](../../../examples/invoices/main.qi) from packages, and the [flows](../../../examples/flows) of a shop and of subscriptions.

| Module | For |
|--------|-----|
| [`std.math`](#stdmath) | Numbers, conversion between `Int` and `Float` |
| [`std.strings`](#stdstrings) | Taking text apart, searching, changing, parsing numbers |
| [`std.arrays`](#stdarrays) | Functions on arrays: slice, map, filter, sort, ... |
| [`std.list`](#stdlist) | `List<T>`: an array with methods |
| [`std.dict`](#stddict) | `Dict<V>`: values found by a text key |
| [`std.map`](#stdmap) | `Map<K, V>`: values found by a key of any type |
| [`std.option`](#stdoption) | Chaining steps on values that may be `null` |
| [`std.result`](#stdresult) | `Result<T, E>`, surviving a failure with `attempt`, traces |
| [`std.io`](#stdio) | Reading input, files, the environment |
| [`std.time`](#stdtime) | The clock, dates, waiting, measuring |
| [`std.random`](#stdrandom) | Random numbers, identifiers |
| [`std.path`](#stdpath) | Paths taken apart and put together |
| [`std.fs`](#stdfs) | Folders: listing, walking, creating, copying |
| [`std.os`](#stdos) | Program arguments, exit, running other programs |
| [`std.log`](#stdlog) | Messages about what a program does, with levels and values, as text or JSON |
| [`std.format`](#stdformat) | Numbers, sizes, durations and tables written for people |
| [`std.json`](#stdjson) | Reading, searching and writing JSON |
| [`std.regex`](#stdregex) | Regular expressions |
| [`std.csv`](#stdcsv) | Tables as text |
| [`std.set`](#stdset) | `Set<T>`: values without repeats |
| [`std.semver`](#stdsemver) | Versions, 1.4.2, and the constraints on them, `^1.2 \|\| ^2.0` |
| [`std.encoding`, `std.hash`](#stdencoding-and-stdhash) | Base64, hex, URL encoding; SHA, MD5, HMAC |
| [`std.http`](#stdhttp) | Asking web servers, and being one |
| [`std.url`](#stdurl) | Addresses taken apart, changed and resolved |
| [`std.sql`](#stdsql) | Databases: SQLite, Postgres and MySQL, rows and transactions |
| [`std.task`](#stdtask) | Doing several things at once: tasks and channels |
| [`std.compiler`](#stdcompiler) | Asking the compiler about a program: its mistakes, its declarations, what a name is |
| [`std.test`](#stdtest) | Tests and checks; what `qoqi test` runs |
| [`std.layout`](#stdlayout) | Text laid out to fit a width |
| [`std.formatter`](#stdformatter) | The formatter of Qoqi: what `qoqi fmt` runs |
| [`std.ops`](#stdops) | The traits behind operators, loops and brackets; `sorted`, `max`, `join` for any type that has them |

The modules are imported and used like any other ([modules.md](modules.md)): `import std.path;` then `path.dir(file)`, or `import std.path.{dir};` then `dir(file)`. The methods of arrays, texts and numbers need no import (see below).

`print`, `println`, `toString`, `push` and `panic` are not part of the library: they are builtins, available without an import.

## Conventions

**Methods, not functions.** What the library does to an array, a text or a number is a method of it: `items.filter(wanted)`, `name.trim()`, `x.sqrt()`. The tables of `std.arrays` and `std.strings` below list them as `name(receiver, arguments)`, and that of `std.math` as the methods they are: the receiver is what stands before the dot, so `take(items, count)` is called as `items.take(count)`. Nothing is imported for a method: these three modules are part of any program that calls one of their methods. An import is only for the few functions that have nothing to stand on: `arrays.range`, `arrays.fill`, `arrays.flatten`, `arrays.unzip`, `strings.fromCode`, and the constants `math.pi` and `math.e`. A method is handed to another function as any function is, by its name: `texts.map(String.parseInt)` calls `parseInt` on each text, and `keys.map(users.get)` calls `get` of that dictionary.

**Nothing instead of failure.** A function that may have no answer returns an optional, and `null` means "none": the position of something not found, the first item of an empty array, a text that is not a number. Where there is a reason worth knowing, as for a file that cannot be read, it returns a [`Result`](#stdresult) with the reason in it. The type checker then makes sure the program deals with it:

```rust
let n = line.parseInt();      // Int?
if n != null { total += n; }         // an Int in here
let port = text.parseInt() ?? 8080;
```

**A failure is for a mistake in the program.** Asking for characters 2 to 9 of a three-letter text, the square root of a negative number, or a random number below zero stops the program, like an index out of bounds does. The message names the function as the program called it and the line that called it:

```
[ERROR] R0007: std.strings.substring: range 0..5 is outside a text of 3 characters
  --> main.qi:8:19 in function 'shorten'
     |
 8 |     return name.substring(0, 5) + "...";
     |                   ^^^^^^^^^^
  called from main.qi:13:13 in function 'main'
```

That holds however deep in the library the failure happened: the lines of the library are left out, and what is named is the function the program called, `std.csv.parseWith` for a separator it cannot use, not the private one that found out.

**Arrays are not changed** by the functions of `std.arrays`; they return new ones. The exceptions say so (`pop`, `removeAt`, `insertAt`, `clear`, `swap`, `random.shuffle`). A `List` is the reverse: its methods change it, unless they return a new list.

**Ranges** are given as `from, to`: `from` is included, `to` is not. Positions count from 0. In texts they count characters, not bytes, like `text[i]` and `text.len()`.

---

## `std.math`

An `Int` is taken for a `Float` where one is expected, and the two work together as `Float`s (`1 + 2.5` is `3.5`), so `toFloat` is rarely needed: it is there for when the conversion should be seen. Going to an `Int` is never implicit, and there are four ways to lose the fraction.

What there is to do with a number is a method of it: `n.abs()`,
`x.sqrt()`, `price.round()`. Only the constants `math.pi` and `math.e` need
`import std.math;`.

| Method | Result |
|--------|--------|
| `n.toFloat() -> Float` | The same number, as a `Float` |
| `x.floor() -> Int` | The largest whole number not above `x`: `2.7.floor()` is 2, `(-2.7).floor()` is -3 |
| `x.ceil() -> Int` | The smallest whole number not below `x`: `2.1.ceil()` is 3 |
| `x.round() -> Int` | The nearest whole number; halves go away from zero: `2.5.round()` is 3 |
| `x.trunc() -> Int` | `x` without its fraction: `(-2.7).trunc()` is -2 |

The four fail if the number does not fit in an `Int`.

**Whole numbers**

| Method | Result |
|--------|--------|
| `n.abs()` | `n` without its sign |
| `n.min(m)`, `n.max(m)` | The smaller, the larger |
| `n.clamp(low, high)` | `n`, or the nearest limit if it is outside them |
| `n.sign()` | -1, 0 or 1 |
| `n.pow(exponent)` | `n` multiplied by itself `exponent` times. A negative exponent gives 0, except for 1 and -1. Fails past what an `Int` holds, as `*` does: `2.pow(47)` |
| `n.gcd(m)` | Greatest common divisor, never negative: `12.gcd(18)` is 6 |
| `n.lcm(m)` | Least common multiple: `4.lcm(6)` is 12 |
| `n.isEven()`, `n.isOdd()` | |
| `n.sqrt() -> Float` | Square root. Fails for a negative number |
| `n.toString()` | The number as a text |

**Numbers with a fraction**

| Method | Result |
|--------|--------|
| `math.pi`, `math.e` | The constants |
| `x.abs()`, `x.min(y)`, `x.max(y)` | As for whole numbers |
| `x.clamp(low, high)` | `x`, or the nearest limit if it is outside them |
| `x.sign() -> Int` | -1, 0 or 1; 0 for zero and for a `Float` that is not a number |
| `x.sqrt()` | Square root. Fails for a negative number |
| `x.pow(y)` | `x` raised to `y`, which need not be whole: `2.0.pow(0.5)` |
| `x.exp()`, `x.log()` | e raised to `x`; the natural logarithm, which fails for zero and negative numbers |
| `x.sin()`, `x.cos()`, `x.tan()` | Angles in radians |
| `y.atan2(x)` | The angle of the point (x, y), from -π to π |
| `x.radians()`, `x.degrees()` | From degrees to radians, and back |
| `x.toString()` | The number as a text |

```rust
// The length of the diagonal of a 3 by 4 rectangle
let a = 3;
let b = 4;
let diagonal = (a * a + b * b).sqrt();    // 5.0
```

---

## `std.strings`

| Function | Result |
|----------|--------|
| `len(text) -> Int` | How many characters the text has. This is the only way to ask: a text has no `.length` |
| `byteLength(text) -> Int` | How many bytes it takes in UTF-8: `"é"` is one character and two bytes |

**Taking apart**

| Function | Result |
|----------|--------|
| `substring(text, from, to) -> String` | The characters from `from` up to, not including, `to`. Fails if the range is not inside the text |
| `from(text, from)` | From a position to the end |
| `first(text, count)`, `last(text, count)` | The first or last `count` characters; the whole text if it is shorter |
| `split(text, separator) -> [String]` | The pieces between occurrences of the separator: `split("a,b", ",")` is `["a", "b"]`. An empty separator gives the characters |
| `chars(text) -> [String]` | The characters |
| `lines(text) -> [String]` | The lines, without their line ends |
| `words(text) -> [String]` | What is between spaces, tabs and line ends |

**Putting together**

| Function | Result |
|----------|--------|
| `join(parts, separator) -> String` | The parts with the separator between them. `[Int]` and `[Float]` have it too: `[1, 2, 3].join(", ")` is `"1, 2, 3"` |
| `repeat(text, count)` | The text `count` times over. Fails for a negative count |
| `padLeft(text, width, fill = " ")`, `padRight(text, width, fill = " ")` | The text with `fill` before or after it, up to `width` characters: `"7".padLeft(3, "0")` is `"007"` |
| `reverse(text)` | The characters in the opposite order |

Building a long text piece by piece with `+=` copies everything so far at each step. Collect the pieces in an array and `join` them once.

**Searching**

| Function | Result |
|----------|--------|
| `indexOf(text, part) -> Int?` | Where `part` first occurs, or `null` |
| `cut(text, separator) -> (String, String)?` | What is before the first separator, and what is after it: `let (name, value) = "port=80".cut("=") ?? ("", "");` |
| `lastIndexOf(text, part) -> Int?` | Where it last occurs, or `null` |
| `contains(text, part)`, `startsWith(text, start)`, `endsWith(text, end)` | |
| `count(text, part) -> Int` | How many times `part` occurs, not counting overlaps |

**Changing**

| Function | Result |
|----------|--------|
| `replace(text, old, with)` | Every occurrence of `old` replaced. Fails if `old` is empty |
| `upper(text)`, `lower(text)` | |
| `capitalize(text)` | The first character in upper case |
| `trim(text)` | Without the spaces, tabs and line ends at both ends |
| `trimStart(text)`, `trimEnd(text)` | Without them at the start only, or at the end only: `"  a b  ".trimStart()` is `"a b  "` |

**Asking**

| Function | Result |
|----------|--------|
| `isEmpty(text)` | No characters at all |
| `isBlank(text)` | Empty, or only spaces, tabs and line ends |
| `isDigits(text)` | Every character is a digit, and there is at least one |
| `isLetters(text)`, `isAlphanumeric(text)` | Every character is a letter of any alphabet (`"été"`), or a letter or a digit; `false` for `""` |
| `isSpaces(text)` | Every character is a space, a tab or a line end; `false` for `""`, which `isBlank` accepts |
| `isUpper(text)`, `isLower(text)` | There are letters, all capitals or none: `"HTTP/2".isUpper()` |

What a lexer asks of the character it is at: `if c.isLetters() || c == "_" { ... }`.

**Numbers and characters**

| Function | Result |
|----------|--------|
| `parseInt(text) -> Int?` | The whole number the text spells, or `null`: `parseInt("42")`, `parseInt(" -7 ")`. Spaces around it are ignored |
| `parseFloat(text) -> Float?` | Likewise, for a number written the plain way: a sign or none, digits with a point among them or not, an exponent or none. `"2.5"`, `"-1e3"`, `".5"` and `"5."` are numbers; `"nan"`, `"inf"`, `"1_000"` and `"0x1p4"` are not |
| `code(text) -> Int` | The number of the first character: `code("A")` is 65. Fails for `""` |
| `fromCode(code) -> String` | The character with that number: `fromCode(65)` is `"A"` |
| `hash(text) -> Int` | A number that is the same for equal texts and almost always different otherwise. Never negative |

Going the other way, any value becomes text with the builtin `toString`.

```rust
let line = "name = Ada Lovelace";
let at = line.indexOf("=");
if at != null {
    let key = line.first(at).trim();          // "name"
    let value = line.from(at + 1).trim();     // "Ada Lovelace"
}
```

---

## `std.arrays`

Generic functions on plain arrays. Type arguments are inferred: `[1, 2, 3].reversed()`. Where there is nothing to infer from, write them: `[].first()`.

| Function | Result |
|----------|--------|
| `len<T>(items) -> Int` | How many items the array holds. This is the only way to ask: an array has no `.length` |

**Making**

| Function | Result |
|----------|--------|
| `range(from, to) -> [Int]` | The whole numbers from `from` up to, not including, `to` |
| `fill<T>(count, value) -> [T]` | `count` times the same value. This is how to get an array of a given size |
| `copy<T>(items)` | A new array with the same items |
| `slice<T>(items, from, to)` | The items from `from` up to, not including, `to`. Positions outside the array are brought back to its ends |
| `concat<T>(first, second)` | One after the other |
| `reversed<T>(items)` | |
| `without<T>(items, index)` | Without the item at `index`. An index outside the array changes nothing |
| `inserted<T>(items, index, value)` | With `value` at `index`, the rest moved up. An index at or past the end puts it at the end; a negative one changes nothing |

**Changing the array itself**

| Function | Does |
|----------|------|
| `pop<T>(items) -> T?` | Takes the last item out and returns it; `null` for an empty array. Takes the same time however long the array is |
| `removeAt<T>(items, index) -> T?` | Takes the item at `index` out and returns it, the items after it moving down. `null`, and nothing changed, if there is none there |
| `insertAt<T>(items, index, value)` | Puts `value` at `index`, the items from there on moving up. An index at or past the end puts it at the end; a negative one changes nothing, as with `inserted` |
| `clear<T>(items)` | Takes every item out |

With `push`, these make an array a stack (`push`, `pop`) or a queue (`push`, `removeAt(items, 0)`). They change the array they are given, so everything that holds it sees the change, and they make no new array: one made shorter keeps the room it had, to grow into again.

```rust
let stack = [1, 2];
stack.push(3);
let top = stack.pop();          // 3, an Int?; stack is [1, 2]
stack.insertAt(0, 0);         // [0, 1, 2]
let first = stack.removeAt(0);

while !stack.isEmpty() {        // until nothing is left
    let item = stack.pop();
}
```

Two things to know:

- **`null` from an array of optionals.** In a `[Int?]` an item can itself be `null`, and `pop` then gives `null` for that item as it does for an empty array. Ask `isEmpty` (or `len`) before, as the loop above does, instead of waiting for `null`.
- **Not inside `for ... in` over the same array, to go over what is left.** That loop goes over the items there when it began, those pushed after not among them, and ends where it is when the array is made shorter under it: the items that moved into a place it passed are not seen. Use `while` with an index that only moves on when nothing was removed, go down from the end, or loop over `items.copy()`:

```rust
let i = 0;
while i < tasks.len() {
    if tasks[i].done { tasks.removeAt(i); } else { i++; }
}
```

**One item**

| Function | Result |
|----------|--------|
| `first<T>(items) -> T?`, `last<T>(items) -> T?` | `null` for an empty array |
| `at<T>(items, index) -> T?` | The item there, or `null`. `items[index]` fails instead |
| `find<T>(items, wanted) -> T?` | The first item the function accepts |
| `findIndex<T>(items, wanted) -> Int?` | Its position |
| `indexOf<T>(items, wanted) -> Int?` | The position of the first item equal to `wanted`, or `null` |

**Asking**

| Function | Result |
|----------|--------|
| `isEmpty<T>(items)` | |
| `contains<T>(items, wanted)` | An item is equal to `wanted` |
| `any<T>(items, wanted)` | The function accepts at least one item |
| `all<T>(items, wanted)` | The function accepts every item. True for an empty array |
| `count<T>(items, wanted) -> Int` | How many it accepts |

**Every item**

| Function | Result |
|----------|--------|
| `map<T, U>(items, convert) -> [U]` | What the function makes of each item |
| `filter<T>(items, wanted) -> [T]` | The items the function accepts |
| `fold<T, A>(items, start, combine) -> A` | One value from all the items: starts with `start`, then `combine(value so far, item)` for each |
| `reduce<T>(items, combine) -> T?` | The items combined two by two from the first, `combine(so far, item)`; `null` for none |
| `each<T>(items, action)` | Calls the function with each item |

**Order**

| Function | Result |
|----------|--------|
| `sorted<T>(items, before) -> [T]` | In order, where `before(a, b)` says whether `a` must come first. Items equal in that order keep the order they had |
| `swap<T>(items, a, b)` | **Changes the array**: the two items change places |

**Numbers and texts**

| Function | Result |
|----------|--------|
| `sum(numbers) -> Int` | |
| `smallest(numbers) -> Int?`, `largest(numbers) -> Int?` | `null` for an empty array |
| `sortedNumbers(numbers)`, `sortedTexts(texts)` | Smallest first; alphabetical |

These are methods of `[Int]`, and all but `sortedTexts` of `[Float]` as well, giving a `Float`: `prices.sum()`, `areas.largest()`. `[Int]`, `[Float]` and `[String]` also take `sorted()` without a function, smallest first or alphabetical, and with one like any array: `scores.sorted((a, b) -> a > b)` for the largest first.

**Pieces and groups**

| Function | Result |
|----------|--------|
| `take(items, count)`, `drop(items, count)` | The first `count` items; all but those |
| `chunks(items, size) -> [[T]]` | Pieces of `size`, the last one shorter if it does not divide |
| `flatten(arrays) -> [T]`, `flatMap(items, convert)` | |
| `groupBy(items, name) -> Dict<[T]>` | The items sorted into groups by a text worked out from each |
| `countBy(items, name) -> Dict<Int>` | How many items give each text |
| `unique(items)`, `uniqueBy(items, name)` | Without repeats; the first of each is kept. `unique` is for `[String]`, `[Int]` and `[Float]` |
| `partition(items, wanted) -> ([T], [T])` | The items a function accepts, and the others: `let (even, odd) = numbers.partition(n -> n.isEven());` |
| `zip(first, second) -> [(A, B)]` | The items of two arrays side by side, as many as the shorter has |
| `unzip(pairs) -> ([A], [B])` | The other way round |
| `sortedBy<R: Ordered>(items, rank: fn(T) -> R)` | In the order of a value worked out from each, smallest first: a number, a text, anything [`Ordered`](#stdops). `people.sortedBy(p -> p.name)`, `people.sortedBy(p -> p.height)` |
| `sortedByText(items, name)` | The same, for a text |
| `maxBy<R: Ordered>(items, rank) -> T?`, `minBy<R: Ordered>(items, rank) -> T?` | The item ranked highest, or lowest; the first of several ranked the same. `null` for an empty array |
| `sumOf(items, amount) -> Int` | |

**Equal**, for `indexOf` and `contains`, means what `==` means: numbers, texts and booleans are equal when they have the same value, arrays and structs when what they hold is, and a type that implements `Equal` when its `equals` says so.

```rust
let people = [Person { name: "Ada", age: 36 }, Person { name: "Linus", age: 21 }];

let adults = people.filter(p -> p.age >= 18);
let names = adults.map(p -> p.name);
let oldestFirst = people.sorted((a, b) -> a.age > b.age);
let totalAge = people.fold(0, (sum, p) -> sum + p.age);
```

---

## `std.list`

`List<T>` is an array in a struct, with the functions of `std.arrays` as methods. Calls read left to right and can be chained.

```rust
import std.list;

let names = list.of(["ada", "linus"]);
names.add("grace");
let long = names.filter(n -> n.len() > 3).sorted((a, b) -> a < b);
println(long);                          // ["grace", "linus"]: a list prints as an array does

for name in names { println(name); }    // a list goes where an array does
names[0] = "Ada";                       // as with an array, a position that is not there stops the program
println(names[0]);
```

| Function | Result |
|----------|--------|
| `list.empty<T>() -> List<T>` | An empty list. The type has to be written |
| `list.of<T>(items) -> List<T>` | A list of the items of an array. The list has its own copy |

**Methods that change the list**

They change the array the list holds, as the functions of [`std.arrays`](#stdarrays) that change an array do. As there, do not take items out of a list inside a `for ... in` over that same list: the loop counted them before it started. Loop over `names.toArray()`, or use `while` with `names.len()`.

| Method | Does |
|--------|------|
| `add(value)`, `addAll(values)` | Adds at the end |
| `set(index, value)` | Replaces the item there. Fails if there is none |
| `insert(index, value)` | Puts `value` at `index`, moving the rest up. An index at or past the end adds it at the end; a negative one changes nothing |
| `removeAt(index) -> T?` | Takes the item out and returns it; `null` if there is none there |
| `pop() -> T?` | Takes out the last item; `null` for an empty list |
| `clear()` | |
| `sort(before)` | Puts the list itself in order |

**Methods that read**

| Method | Result |
|--------|--------|
| `len()`, `isEmpty()` | |
| `get(index) -> T?` | The item there, or `null` |
| `first() -> T?`, `last() -> T?` | |
| `find(wanted) -> T?`, `findIndex(wanted) -> Int?` | |
| `contains(value)`, `indexOf(value) -> Int?` | By equality; see [`std.arrays`](#stdarrays) |
| `any(wanted)`, `all(wanted)`, `count(wanted)` | |
| `maxBy<R: Ordered>(rank) -> T?`, `minBy<R: Ordered>(rank) -> T?` | As for arrays |
| `fold<A>(start, combine) -> A`, `reduce(combine) -> T?` | |
| `sumOf(amount) -> Int` | |
| `countBy(name) -> Dict<Int>` | How many items give each text |
| `each(action)` | |
| `toArray() -> [T]` | An array of the items; changing it does not change the list |

**Methods that return a new list**

`map<U>(convert)`, `filter(wanted)`, `sorted(before)`, `sortedBy(rank)`, `reversed()`, `slice(from, to)`, `take(count)`, `drop(count)`, `flatMap<U>(convert)`, `uniqueBy(name)`, `concat(other: List<T>)`, `copy()`. They do what the methods of arrays of the same names do, and return a `List`; so do those that give several: `chunks(size) -> List<List<T>>`, `partition(wanted) -> (List<T>, List<T>)`, `groupBy(name) -> Dict<List<T>>`.

`map` and `fold` have a type parameter of their own, for what they produce. It is worked out from the function passed, or can be written: `names.map<Int>(n -> n.len())`.

A list prints as an array does, `[1, 2, 3]`, alone, inside another value and in `"${...}"`.

---

## `std.dict`

`Dict<V>` (a dictionary) finds values by a text key: finding a key takes about the same time however many there are.

Its keys and values are two arrays, in the order the keys were set. Up to eight keys are simply looked through, so a small dictionary is three objects and costs next to nothing to make; a program can have one per record, per scope, per request. Past eight the machine keeps an index of where each key is.

```rust
let ages = {"ada": 36, "linus": 21};     // a literal, or:
let more = dict.empty<Int>();
more.set("grace", 85);

let age = ages.get("ada");          // Int?
if age != null { println(age + 1); }
println(ages.get("nobody") ?? 0);
```

| Function / method | Result |
|-------------------|--------|
| `{"ada": 36}`, `dict.of(pairs) -> Dict<V>` | A dictionary of entries; a literal with texts for keys is a `Dict`, and `let d: Dict<Int> = {};` an empty one |
| `dict.empty<V>() -> Dict<V>` | An empty dictionary. The type of the values has to be written |
| `set(key, value)` | Sets the value of a key, replacing the one it had |
| `get(key) -> V?` | The value, or `null` if the key has none |
| `getOr(key, fallback) -> V` | The value, or the fallback |
| `update(key, start, change)` | Sets the key to what a function makes of its value, or of `start` if it has none. Counting: `counts.update(word, 0, n -> n + 1)` |
| `getOrSet(key, make) -> V` | The value, set first to what `make()` gives if the key has none. Grouping: `groups.getOrSet(letter, () -> []).push(word)` |
| `has(key) -> Bool` | |
| `remove(key) -> Bool` | Removes a key; whether it was there |
| `len()`, `isEmpty()` | |
| `keys() -> [String]` | The keys, in the order they were first set |
| `sortedKeys() -> [String]` | In alphabetical order |
| `entries() -> [(String, V)]` | Each key with its value: `for (name, age) in ages.entries() { ... }` |
| `values() -> [V]` | The values, in the order of their keys |
| `each(action)` | Calls `action(key, value)` for each |
| `copy() -> Dict<V>` | A dictionary of its own with the same entries: setting a key of one leaves the other alone |

A dictionary takes brackets and loops: `ages["ada"]` is what `get` gives, a value or `null`; `ages["ada"] = 36;` is `set`; and `for name in ages { ... }` goes through the keys. Counting reads `counts[word] = (counts[word] ?? 0) + 1;`. Two dictionaries are `==` when they have the same keys with equal values, and one prints as its literal: `{"ada": 36}`.

Keys are compared exactly: `"a"` and `"A"` are two keys, and `""` is a key.

The values can be of any type, including arrays, structs and other dictionaries (`dict.empty<Dict<Int>>()`). For keys that are not texts, there is [`Map`](#stdmap).

```rust
// Group words by their first letter
let groups = dict.empty<[String]>();
for word in words {
    groups.getOrSet(word.first(1), () -> []).push(word);
}
```

## `std.map`

`Map<K, V>` finds values by a key of any type: a number, a value of an enum, a struct, a tuple. Two keys are the same key when they are written the same, as `toString` writes them, and are `==`: so `1` and `"1"` are two keys of a `Map<Int | String, V>`. It takes brackets and loops as a `Dict` does, and prints as its literal: `{(1, 1): "ada"}`.

```rust
let seats = {(1, 1): "ada", (1, 2): "alan"};   // keys that are not texts: a Map
seats[(2, 1)] = "grace";
let counts = map.empty<Fruit, Int>();
for fruit in basket { counts.update(fruit, 0, n -> n + 1); }
let scores: Map<Player, Int> = {};
```

| Function / method | Result |
|-------------------|--------|
| `{key: value}`, `map.of(pairs) -> Map<K, V>` | A map of entries; a key given twice has the value given last |
| `map.empty<K, V>() -> Map<K, V>` | An empty map |
| `set`, `get`, `getOr`, `getOrSet`, `update`, `has`, `remove`, `len`, `isEmpty` | As for `Dict`, with `K` for `String` |
| `keys() -> [K]`, `values() -> [V]`, `entries() -> [(K, V)]`, `each(action)` | In the order the keys were first set |
| `copy() -> Map<K, V>` | A map of its own with the same entries |

A `Map` has no `sortedKeys`, which is a `Dict`'s: keys of any type have no alphabetical order. `keys().sortedBy(...)` puts them in one.

A key is found by how it is written, then by `==` among the keys written the same way: finding one takes about the same time however many there are.

---

## `std.option`

Functions on optionals: `T?`, a value or `null`. They carry "or nothing" through a series of steps, so that each step does not need its own null test.

```rust
import std.option;

// Without: one test per step
let line = io.readLine();
if line != null {
    let n = line.parseInt();
    if n != null {
        println(n * 2);
    }
}

// With: each step runs only if the one before gave a value
let doubled = option.map(option.then(io.readLine(), text -> text.parseInt()), n -> n * 2);
```

| Function | Result |
|----------|--------|
| `map<T, U>(value: T?, convert: fn(T) -> U) -> U?` | The value turned into another, or `null` if there was none |
| `then<T, U>(value: T?, step: fn(T) -> U?) -> U?` | The same, for a step that may itself have no answer |
| `filter<T>(value: T?, wanted) -> T?` | The value if the function accepts it, `null` otherwise |
| `mapOr<T, U>(value: T?, fallback: U, convert) -> U` | The value turned into another, or `fallback` |
| `or<T>(first: T?, second: T?) -> T?` | The first value if there is one, otherwise the second |
| `orElse<T>(value: T?, make: fn() -> T) -> T` | The value, or what the function makes. Unlike `value ?? fallback`, the fallback is only worked out when needed |
| `each<T>(value: T?, action)` | Calls the function with the value, if there is one |
| `isSome<T>(value)`, `isNone<T>(value)` | |
| `expect<T>(value: T?, message: String) -> T` | The value, or the program stops with the message |
| `okOr<T, E>(value: T?, failure: E) -> Result<T, E>` | `Ok` with the value, or `Err` with the failure when there is none |
| `values<T>(items: [T?]) -> [T]` | The items of an array that are not `null` |

All of them but `values` are also methods of every optional, with no import: `users.get(key).map(u -> u.name)`, `io.env("HOME").expect("HOME is not set")`. They are written `impl<T> T? { ... }`, which a program can use for methods of its own.

`map` is for a step that always has an answer (`fn(Int) -> String`), `then` for one that may not (`fn(String) -> Int?`). Using `map` with the second kind would give an optional of an optional; `then` keeps it flat.

What comes out is an ordinary optional: `??` gives it a fallback, and a test against `null` makes it a plain value inside the test.

```rust
// The name of a user's manager, through two lookups and a field that may be null
let manager = option.then(users.get(key), u -> u.manager);
let found = option.then(manager, users.get);
println(option.mapOr(found, "no manager", u -> u.name));
```

Type arguments are worked out from the values and functions passed. A function is passed by its name, `toString`, and so is a method: of a type, `String.parseInt` or `String.trim`, which takes the text as its argument, or of a value, `users.get`, which looks in that dictionary. `option.then(io.readLine(), String.parseInt)` reads as the steps it takes.

There is no separate `Option` type with `Some` and `None`: `T?` is that type, and the type checker already refuses to use one as a plain value. These functions add the chaining.

---

## `std.result`

For failures: returning one with its reason, and surviving one that would stop the program. The whole subject is in [errors.md](error-handling.md); this is the reference.

**`Result<T, E>`**: `enum Result<T, E> { Ok(T), Err(E) }`, the value a function produced or the reason there is none. A `Result<Void, E>` is for an action that has nothing to give back: `Ok()` is its success. The name `Result` needs no import: every file has it, unless it declares a `Result` of its own. The rest of the module (`attempt`, `Failure`, `Kind`) is imported as usual.

| Method | Gives |
|--------|-------|
| `isOk()`, `isErr()` | |
| `value() -> T?`, `reason() -> E?` | One or the other, or `null` |
| `or(fallback) -> T` | The value, or the fallback |
| `orElse(make: fn(E) -> T) -> T` | The value, or what the function makes of the reason: `io.readFile(path).orElse(error -> "(${error.describe()})")`. Unlike `or`, worked out only when needed |
| `map<U>(convert)`, `mapErr<F>(convert)` | The value, or the reason, turned into another |
| `then<U>(step) -> Result<U, E>` | A next step that can fail too |
| `expect(message) -> T` | The value; stops the program if there is none |

`or` and `orElse` are named as those of an optional are: `or` takes the fallback itself, `orElse` a function that makes it.

| Function | Gives |
|----------|-------|
| `collect<T, E>(items: [Result<T, E>]) -> Result<[T], E>` | `Ok` with the values of all the results, in their order, or the first `Err` among them: `result.collect(lines.map(parseOrder))?` |

**Failures that stop the program**

| Function | Does |
|----------|------|
| `attempt<T>(action: fn() -> T) -> Result<T, Failure>` | Runs the function; if it stops, the program goes on and the failure is returned |
| `guard(action: fn() -> Void) -> Failure?` | The same, for a function run for what it does |
| `stack(skip: Int) -> [Location]` | The calls in progress, innermost first, leaving out `skip` of them: for an error of your own that says where it comes from |
| `trace(message, stack) -> String` | The message and the calls, over several lines |

| Type | Holds |
|------|-------|
| `Failure` | `kind: Kind`, `message`, `at: Location`, `stack: [Location]`; `describe()`, `trace()`. It prints as `describe()` writes it |
| `Kind` | `Panic`, `Bounds`, `Cast`, `Arithmetic`, `Depth`, `Memory`, `Library`, `Other` |
| `Location` | `function`, `file`, `line`, `column`; `describe()` |

The builtin `panic(values...)`, which stops the program, needs no import.

---

## `std.io`

Output goes through the builtins `print` and `println`. Reading and writing a file can fail for reasons outside the program; those functions return a [`Result`](#stdresult) and never stop the program.

| Function | Result |
|----------|--------|
| `readLine() -> String?` | A line of input, without its line end. `null` at the end of the input |
| `readBytes(count) -> String?` | The next `count` bytes of the input, as a text: for input that says first how long it is. `null` if the input ends before |
| `ask(question) -> String?` | Prints the question, then reads the answer |
| `readLines() -> [String]` | Every remaining line of the input |
| `readFile(path) -> Result<String, Error>` | The whole content of a file, or why it cannot be read |
| `readFileLines(path) -> Result<[String], Error>` | Its lines |
| `writeFile(path, text) -> Result<Int, Error>` | Writes the text, replacing what was in the file. How many characters were written |
| `appendFile(path, text) -> Result<Int, Error>` | Adds to the end of the file, creating it if needed |
| `exists(path) -> Bool` | |
| `remove(path) -> Result<String, Error>` | Deletes a file, or a folder that is empty. Gives the path. A folder with all it holds: [`fs.removeAll`](#stdfs) |
| `printError(text)` | Writes to the error output. No line end is added |
| `env(name) -> String?` | An environment variable, or `null` if it is not set |

An `io.Error` says what kind of failure it was, for the program to act on, and the rest for telling a person:

| Field | |
|-------|---|
| `kind: ErrorKind` | `NotFound`, `PermissionDenied`, `IsDirectory`, `NotDirectory`, `AlreadyExists`, `Other` |
| `operation` | What was being done: `"read"`, `"write"`, `"append"`, `"remove"`; from [`std.fs`](#stdfs) `"inspect"` (`info`), `"list"`, `"walk"` (`walk`, `find`), `"create"` (`makeDir`, `makeTempDir`), `"remove"` (`removeAll`), `"rename"`, `"copy"`; from [`std.os`](#stdos) `"run"` |
| `path` | As it was given |
| `message` | As the operating system puts it: `no such file or directory` |
| `stack: [Location]` | Where the program asked for the file |

`describe()` gives one sentence (`cannot read notes.txt: no such file or directory`), `trace()` adds where. An `Error` is [`Show`](#stdops): `println(error)` and `"${error}"` write the sentence, and so does `expect` when it stops.

Input is what the user types, or what is piped in: `qoqi run sum.qi < numbers.txt`.

```rust
import std.io;
import std.io.{ErrorKind};
import std.strings;

fn main() -> Int {
    match io.readFile("notes.txt") {
        case Ok(content): {
            for i, line in content.lines() {
                println(toString(i + 1).padLeft(4), line);
            }
        }
        case Err(error): {
            match error.kind {
                case ErrorKind.NotFound: { println("no notes yet"); }
                default: { io.printError(error.describe() + "\n"); return 1; }
            }
        }
    }
    return 0;
}
```

When the reason does not matter: `io.readFile(path).or("")`.

Paths are relative to the directory the program is run from, not to the program's file. For a file that comes with the program, start from [`os.program()`](#stdos).

---

## `std.time`

| Function | Result |
|----------|--------|
| `now() -> Int` | The time now, a moment: milliseconds since 1 January 1970 |
| `seconds() -> Int` | The same, in whole seconds |
| `ticks() -> Int` | Microseconds since the program started. For measuring: it never jumps when the clock is set |
| `after(length) -> After` | That long from now, to wait for: `wait time.after(2.seconds())` |
| `until(moment) -> After` | A moment, to wait for: `wait time.until(renewal)`. One that has passed is ready at once |
| `since(moment) -> Duration` | How long ago a moment was |
| `stopwatch() -> Stopwatch` | Starts measuring |

A `Stopwatch` has `microseconds()`, `milliseconds()` and `restart()`.

```rust
let watch = time.stopwatch();
work();
println("took", watch.milliseconds(), "ms");
```

### Lengths of time

A length of time is a `Duration`, written as what it is: a number and its unit. A number alone is not one, so that seconds are never taken for milliseconds: `time.after(500)` does not compile, and says to write `500.milliseconds()`.

```rust
wait time.after(2.days());
let renewal = 30.days().fromNow();
wait time.until(renewal);
println(90.minutes(), 1.5.hours() > 80.minutes());      // 1 h 30 min true
```

| Method | Result |
|--------|--------|
| `n.milliseconds()`, `seconds()`, `minutes()`, `hours()`, `days()`, `weeks()` | A `Duration` of that many: `2.days()`. On a `Float`, `seconds()` to `days()`, to the nearest millisecond: `1.5.hours()` |
| `inMilliseconds()`, `inSeconds()`, `inMinutes()`, `inHours()`, `inDays()` | How long it is in a unit, the rest dropped: `90.minutes().inHours()` is `1` |
| `fromNow() -> Int` | The moment that long from now |
| `after(moment) -> Int`, `before(moment) -> Int` | The moment that long after or before another: `3.days().after(started)` |
| `times(count) -> Duration` | So many of it: `self.day.times(30)` |
| `isZero()` | |
| `+`, `-`, `<`, `==`, `toString` | `Duration` is `Add`, `Subtract`, `Ordered`, `Equal` and `Show`: `1.days() + 3.hours()`, and one prints as people say it, `1 day 3 h` |

A moment and a length are not added with `+`: `+` adds two values of one type. `30.days().fromNow()` and `3.days().after(started)` say which is which.

In a flow's fields, from the command line or the pages, a `Duration` is a text with its unit: `"90s"`, `"1h30m"`, `"2d"`, `"250ms"`.

### Dates

A moment is a number, which is what `now` gives. A `Date` is that moment told the way people do, in some time zone.

| Function / method | Result |
|-------------------|--------|
| `time.today() -> Date` | Now, where the program runs |
| `time.utc(moment)`, `time.local(moment)` | A moment as told in UTC, or where the program runs |
| `time.date(year, month, day)`, `dateTime(year, month, day, hour, minute, second)` | In UTC. Numbers out of range carry over: the 32nd of January is the 1st of February |
| `time.parse(text) -> Date?` | Reads `2026-10-13`, `2026-10-13 14:30`, `2026-10-13T14:30:00.250+02:00` (or `+0200`), and the dates of e-mail and of web server logs. The date is told in UTC, whatever zone the text names |
| `timestamp() -> Int` | The moment |
| `atOffset(minutes) -> Date` | The same moment told at a fixed offset from UTC: `atOffset(120)` is `+02:00`, `atOffset(-330)` is `-05:30`. It stays at that offset when moved, across the dates summer time begins or ends |
| `format(pattern)`, `iso()`, `isoTime()` | See below; `2026-10-13`; `2026-10-13T14:30:00Z`, with the milliseconds when there are some, `2026-10-13T14:30:00.250+02:00` |
| `zone() -> String` | The offset as written in a date: `Z` for UTC, `+02:00`, `-05:30` |
| `plus(length)`, `plusDays(n)`, `plusMonths(n)`, `startOfDay()` | New dates: `plus(36.hours())`, a negative length going back |
| `daysUntil(other) -> Int` | Days of the calendar, not lengths of 24 hours |
| `isBefore(other)`, `isAfter(other)`, `isSameDay(other)` | |
| `time.daysIn(year, month)`, `isLeapYear(year)`, `monthName(month)` | |
| `<`, `==`, `compare`, `toString` | `Date` is `Ordered`, `Equal` and `Show`: dates compare as the moments they are, whatever zone each is told in, so `ops.sorted(dates)` and `ops.largest(dates)` work, and one prints as `isoTime()` writes it |

A `Date` has the fields `year`, `month` (1 for January), `day`, `hour`, `minute`, `second`, `millisecond`, `weekday` (an enum, `Weekday.Monday` to `Weekday.Sunday`, with `name()` and `isWeekend()`), `dayOfYear` and `offset` (minutes ahead of UTC).

In a pattern for `format`:

| | | | |
|---|---|---|---|
| `YYYY` 2026 | `YY` 26 | `MMMM` October | `MMM` Oct |
| `MM` 10 | `M` 10 | `DD` 03 | `D` 3 |
| `dddd` Tuesday | `ddd` Tue | `HH` 09 | `H` 9 |
| `hh` 09 (of 12) | `h` 9 | `A` AM | `mm` 05 |
| `ss` 07 | `SSS` 042 | `Z` +02:00 | `[text]` kept as it is |

```rust
let today = time.today();
println(today.format("dddd D MMMM YYYY"));       // Tuesday 13 October 2026
println(today.plusDays(30).iso(), today.daysUntil(time.date(2027, 1, 1)));
```

---

## `std.random`

| Function | Result |
|----------|--------|
| `seed(n)` | Makes the numbers that follow the same on every run, for the same `n` |
| `below(limit) -> Int` | From 0 up to, not including, `limit`. Fails if the limit is below 1 |
| `between(low, high) -> Int` | From `low` to `high`, both included. Fails if `low` is above `high` |
| `float() -> Float` | From 0.0 up to, not including, 1.0 |
| `chance(probability) -> Bool` | True with that probability: `chance(0.25)` one time in four |
| `pick<T>(items) -> T?` | One of the items; `null` for an empty array |
| `shuffle<T>(items)` | **Changes the array**: puts its items in a random order |
| `uuid() -> String` | A random identifier: `3f2b8c1e-9d4a-4f6b-8a1c-0e5d7b9f2a4c`, from the secure generator: `seed` does not repeat it |
| `token(bytes) -> String` | A secret nobody can guess, in hexadecimal: `token(16)` for a session |

Without `seed` the numbers differ on every run. With it they repeat, which is what a test or a replay needs.

These numbers are for games and simulations. They are not fit for passwords or keys: `uuid` and `token` are, coming from the secure generator of the system.

---

## `std.path`

Paths taken apart and put together. Nothing here touches the disk. The separator is that of the system the program runs on.

| Function | Result |
|----------|--------|
| `join(parts) -> String` | The parts put together and tidied: `join(["a", "b/", "../c.txt"])` is `a/c.txt` |
| `inside(folder, name) -> String` | `inside("notes", "todo.txt")` is `notes/todo.txt` |
| `base(path)`, `dir(path)` | The last part; all but the last part |
| `extension(path)` | With its dot: `.txt`, `.gz` for `archive.tar.gz`; `""` if there is none. The dots a name starts with are not one: `.bashrc` has none |
| `stem(path)` | The last part without its extension: `.bashrc` for `.bashrc` |
| `withExtension(path, extension)` | `withExtension("a/b.md", ".html")` is `a/b.html`; `withExtension(".bashrc", ".bak")` is `.bashrc.bak` |
| `parts(path) -> [String]` | `"a/b/c.txt"` gives `["a", "b", "c.txt"]` |
| `clean(path)` | The shortest path that means the same |
| `isAbsolute(path)`, `absolute(path)` | |
| `matches(pattern, name) -> Bool` | `*` stands for any characters, `?` for one: `matches("*.txt", "todo.txt")` |
| `relative(from, to) -> String?` | The path from a folder to another path: `relative("app/src", "app/lib")` is `../lib`; `null` where there is none, as between two drives |
| `separator()` | `/`, or `\` on Windows |

---

## `std.fs`

Folders and the files in them. Reading and writing the *content* of a file is in [`std.io`](#stdio). Like there, what can fail returns a `Result` whose reason is an `io.Error`.

| Function | Result |
|----------|--------|
| `list(folder) -> Result<[Entry], Error>` | What a folder holds, sorted by name; one level |
| `walk(folder) -> Result<[String], Error>` | The path of every file under it, however deep |
| `find(folder, pattern) -> Result<[String], Error>` | Those whose name fits: `fs.find("src", "*.qi")` |
| `info(path) -> Result<Entry, Error>` | What is known about one file or folder |
| `isDir(path)`, `isFile(path)` | |
| `makeDir(folder) -> Result<String, Error>` | Creates it and the folders above it. One already there is fine |
| `removeAll(path) -> Result<String, Error>` | Deletes a file, or a folder with all it holds. Nothing there is fine |
| `rename(from, to)`, `copy(from, to)` | Give the new path |
| `currentDir()`, `tempDir()` | |
| `makeTempDir(prefix) -> Result<String, Error>` | A new, empty folder for the program's own use |

An `Entry` has `name`, `path`, `kind` (`Kind.File` or `Kind.Dir`), `size` in bytes (0 for a folder), `modified` (a moment, as `time.now`), and `isFile()`, `isDir()`.

```rust
match fs.list("notes") {
    case Ok(entries): {
        for entry in entries { println(entry.name, format.bytes(entry.size)); }
    }
    case Err(error): { println(error.describe()); }     // cannot list notes: no such file or directory
}
```

---

## `std.os`

| Function | Result |
|----------|--------|
| `args() -> [String]` | What followed the program on the command line: `qoqi run notes.qi add milk` gives `["add", "milk"]` |
| `program() -> String` | The file the program was run from. Files that come with a program are found from it: `path.inside(path.dir(os.program()), "data.csv")` |
| `exit(status)` | Ends the program now. Nothing after it runs, not even an `attempt` around it |
| `platform()` | `"linux"`, `"darwin"`, `"windows"` |
| `hostname()` | |
| `environment() -> Dict<String>` | Every environment variable. One alone: `io.env` |
| `run(program, arguments) -> Result<Output, Error>` | Runs another program and waits for it |
| `runWithInput(program, arguments, input)` | The same, with a text for it to read |
| `command(program, arguments) -> Command` | A program to run, told a part at a time: `.inDir(folder)`, `.env(name, value)`, `.withInput(text)`, then `.run()`, which gives what `run` gives |

An `Output` has `status`, `out`, `err`, and `succeeded()`, `lines()`. A program that ran and reports a failure is not an error: look at `output.status`. The error is for a program that could not be started.

```rust
let status = os.command("git", ["status", "--short"])
    .inDir("checkout")
    .env("GIT_TERMINAL_PROMPT", "0")       // added to the environment it inherits
    .run()?;
```

**Arguments on the command line.** What follows the file is for the program, except the options of `qoqi` itself (`--heap-mb`, `--max-depth`). After a lone `--` everything is for the program:

```
qoqi run todo.qi add "Buy milk" --due=2026-10-20
qoqi run --heap-mb=64 report.qi -- --heap-mb=this-one-is-mine
qoqi run . list                 # a project: name its folder, then the arguments
```

**Being asked to stop.** `os.signals() -> Channel<String>` gives the signals the program is sent, as they come: `"interrupt"` for Ctrl-C, `"terminate"` for `kill` or a service manager stopping it. Once a program listens, they no longer stop it: it stops its own way, and a second Ctrl-C stops it at once. Waiting for a signal does not keep a program alive whose other tasks are done.

```rust
let stop = os.signals();
task.run(fn() {
    let name = wait stop.receive();
    log.info("stopping", {"signal": name});
    server.close();                       // what is being answered is answered first
});
```


---

## `std.log`

```rust
import std.log;

log.info("list saved", {"items": 12, "file": path});
log.warn("slow answer", {"ms": 950});
log.error("cannot reach the database", {"reason": error.describe()});
```

```text
2026-10-16T09:41:07.120Z INFO list saved items=12 file=todo.json
```

The messages go to the error output, so that what the program prints stays its result. A value with a space, a quote or an `=` in it is written in quotes, so the line reads back.

| Function | |
|----------|--------|
| `debug`, `info`, `warn`, `error` `(message, fields = {})` | A message at that level, with the values that go with it: a `Dict<Any>` |
| `setLevel(level)` | Messages below it are not written: `log.Level.Debug`, `Info` (at first), `Warn`, `Error` |
| `useJson()`, `useText()` | One line of JSON per message, `{"time": ..., "level": "info", "message": ..., and the values}`, for messages that are collected and searched; or text, as at first |
| `writeTo(write)` | The lines go to a function instead: a file, a list a test looks at |
| `with(fields) -> Logger` | A logger that adds these values to every message: `let request = log.with({"id": id});` in a handler, then `request.info("answered")` |

A `Logger` has `debug`, `info`, `warn`, `error`, `with` and `write(level, message, fields)`; the level, the form and where the lines go are those of the module, shared by every logger.

---

## `std.format`

Numbers and tables written for people.

Every function rounds the same way, to the nearest with a half going away
from zero, as `Float.round()` does: `fixed(2.5, 0)` is `3`, `fixed(-2.5, 0)`
is `-3`, `money(0.125)` is `0.13`. The number is rounded as it is written,
so `fixed(1.005, 2)` is `1.01`, though the Float nearest 1.005 is a little
below it. A number that rounds to zero has no minus sign. The rounding
comes before the unit is chosen: `duration(59999)` is `1m 00s`, not
`60.0s`, and `bytes(1048575)` is `1.0 MB`, not `1024.0 KB`.

| Function | Result |
|----------|--------|
| `fixed(x, digits)` | `fixed(3.14159, 2)` is `3.14`, `fixed(1.0, 2)` is `1.00` |
| `grouped(n)`, `groupedWith(n, separator)` | `1,234,567` |
| `money(amount)` | `1,234,567.50` |
| `percent(share, digits)` | `percent(0.125, 1)` is `12.5%` |
| `padded(n, width)` | `padded(7, 3)` is `007` |
| `hex(n)`, `binary(n)`, `inBase(n, base)`, `fromBase(text, base) -> Int?` | |
| `bytes(size)` | `512 B`, `1.5 MB` |
| `duration(milliseconds)` | `350ms`, `2.5s`, `1m 05s`, `1h 02m 03s`, `3d 04h`; the last unit written is rounded |
| `ordinal(n)` | `1st`, `2nd`, `11th`, `22nd` |
| `plural(count, one, many)` | `1 file`, `3 files` |
| `pad(text, width, align)` | `Align.Left`, `Align.Right`, `Align.Center` |
| `table(titles, rows) -> String` | Columns as wide as their widest value; a column of numbers is set to the right, numbers as this module writes them included (`-1,234`, `5,862.69`, `12.5%`) |
| `tableWith(titles, aligns, rows)` | The same, saying how each column is set |
| `bar(value, largest, width)` | A bar of a chart, in `#` |

```rust
print(format.table(["name", "age"], [["ada", "36"], ["grace", "45"]]));
```
```
name   age
-----  ---
ada     36
grace   45
```

---

## `std.json`

A document is a value of an enum, so a `match` takes it apart and the compiler checks every kind is handled:

```rust
pub enum Json { Null, Bool(Bool), Number(Float), String(String), Array([Json]), Object(Dict<Json>) }
```

| Function / method | Result |
|-------------------|--------|
| `json.parse(text) -> Result<Json, ParseError>` | The error has `message`, `line`, `column`, `describe()`, which is also how it prints. A number no `Float` holds, `1e400`, is an error too |
| `get(key) -> Json?`, `at(index) -> Json?` | Into an object, into an array |
| `find(path) -> Json?` | Keys and positions separated by dots: `find("user.tags.0")`. A key with a dot in it is out of its reach: `get("a.b")` |
| `text()`, `number()`, `int()`, `bool()`, `items()`, `fields()` | The value if it is of that kind, else `null` |
| `isNull()` | |
| `a == b`, `a != b` | By content: the same kind of value, equal items in the same order, equal values under the same keys |
| `stringify()`, `pretty()` | On one line; over several, indented by two spaces. `println(document)` and `"${document}"` write it as `stringify` does |
| `prettyWith(indent)` | Over several lines, each level indented by `indent`: `"    "`, `"\t"` |
| `json.text(value)`, `json.int(n)`, `json.number(n)`, `json.bool(value)`, `json.texts(items)`, `json.ints(items)`, `json.array(items, convert)` | Making documents: `json.array(prices, json.number)` |
| `json.object()` | An object built a key at a time: `.text(k, v)`, `.int`, `.number`, `.bool`, `.none`, `.set(k, json)`, then `.done()`. What `done` gives stays as it is when the builder goes on |
| `json.textOf`, `intOf`, `numberOf`, `boolOf`, `itemsOf` | The same accessors as functions, to hand to `map` and the like |

```rust
let document = json.parse(text).expect("a document");
let name = document.get("name")?.text() ?? "nobody";
let firstTag = document.find("user.tags.0")?.text();

let reply = json.object().text("name", name).int("visits", 3).set("tags", json.texts(tags)).done();
println(reply.stringify());         // {"name":"ada","visits":3,"tags":["a","b"]}
```

The keys of an object stay in the order they were written. Numbers are `Float`s; `int()` gives one that is whole and that an `Int` holds, up to 140737488355327 either side of 0, and `null` for `1e15`.

---

## `std.regex`

Regular expressions, in the syntax of RE2: no look-behind and no back-references, and a search that always takes time proportional to the text. `\w`, `\d` and `\b` know the letters and digits of English only.

| Function / method | Result |
|-------------------|--------|
| `regex.compile(pattern) -> Result<Regex, PatternError>` | Checks a pattern. The error has `pattern`, `message` and `describe()`, which is also how it prints |
| `regex.must(pattern) -> Regex` | The same for a pattern written in the program: a wrong one stops it |
| `matches(text) -> Bool` | Whether it fits somewhere |
| `find(text) -> Match?`, `findAll(text) -> [Match]` | |
| `capture(text) -> [String]?` | What the groups of the first match took |
| `all(text) -> [String]` | The text of every match |
| `replace(text, with)` | `$1` is what the first group took, `$0` the whole match, `${name}` a named group |
| `replaceWith(text, convert)` | Each match replaced by what a function makes of it |
| `split(text) -> [String]` | The pieces between the matches |
| `regex.escape(text)` | A text as a pattern that matches exactly it |

A `Match` has `text`, `start`, `end` (positions in characters), `groups` (what each group took, the first at 0), and `group(n)`, which counts as `$n` does in a replacement: `group(0)` is the whole match, `group(1)` the first group; `named(name)` is for a group written `(?P<name>...)`.

In a text of a program a backslash is written twice, and `${` would start a value to insert: write `"\\d+"` and `"\${name}"`. In a replacement a name runs on as far as letters, digits and `_` go, so `$1_x` is the group named `1_x`, which there is none of: the first group followed by `_x` is written `"\${1}_x"`.

```rust
let date = regex.must("(\\d{4})-(\\d{2})-(\\d{2})");
date.replace("on 2026-10-13", "$3/$2/$1")           // on 13/10/2026
```

---

## `std.csv`

| Function / method | Result |
|-------------------|--------|
| `csv.parse(text) -> Result<[[String]], ParseError>` | The rows, each an array of its values |
| `csv.parseWith(text, separator)` | With `";"` or `"\t"` |
| `csv.write(rows)`, `csv.writeWith(rows, separator)` | Rows as text, quoting what needs it |
| `csv.table(text) -> Result<Table, ParseError>` | The first row names the columns; every row must have a value for each |
| `table.column(name) -> [String]?`, `cell(row, name) -> String?` | |
| `table.records() -> [Dict<String>]` | Each row as a dictionary from column to value |
| `table.where(wanted) -> Table` | The rows a function accepts |
| `table.write()`, `len()`, `header`, `rows` | |

A `ParseError` has `message`, `line` and `column`, the line being the one the row starts on in the text, whatever values over several lines or blank lines come before it, and `describe()`, which is also how it prints. A byte-order mark in front of the text, which some programs write, is passed over.

---

## `std.set`

Values without repeats, of any type. A set is the keys of a [`Map`](#stdmap), and tells its items apart as a `Map` tells its keys: two items are the same when they are written the same and are `==`. One made with `set.by` tells them apart by the text its function gives alone. It prints as its items in braces: `{"a", "b"}`.

| Function / method | Result |
|-------------------|--------|
| `set.empty<T>()`, `set.texts()`, `set.numbers()` | Empty sets |
| `set.of(items)` | From an array of anything: `set.of(["a", "b"])`, `set.of([3, 1, 3])`, `set.of(points)` |
| `set.by(key)` | Of anything, given how to tell two apart; two items are the same when `key` gives the same text: `set.by(fn(name: String) -> String { return name.lower(); })` |
| `add(item) -> Bool`, `addAll(items)`, `remove(item) -> Bool`, `has(item)` | `add` says whether the item was new |
| `len()`, `isEmpty()`, `items()`, `copy()` | Items come in the order they were first added |
| `union(other)`, `intersection(other)`, `difference(other)`, `filter(wanted)` | New sets |
| `isSubsetOf(other)`, `a == b` | Two sets are equal when they hold the same items, in whatever order |
| `for item in set` | Goes through the items |

---

## `std.semver`

Versions numbered the semantic way, `major.minor.patch`, and the constraints written on them in the syntax of Composer and npm. The [package manager](packages.md) chooses versions with it.

```rust
import std.semver;

let installed = semver.parse("v1.4.2").expect("a version");
let wanted = semver.constraint("^1.2 || ^2.0").expect("a constraint");

wanted.allows(installed)                       // true
semver.highest(available, wanted)              // the newest that fits, or null
installed < semver.parse("1.10.0").expect("")  // true: by the numbers, not the letters
```

| Function / method | Result |
|-------------------|--------|
| `semver.parse(text) -> Result<Version, ParseError>` | `1.4.2`, `v2`, `1.4` (2.0.0, 1.4.0), `2.0.0-beta.3`, `1.0.0+build.7` |
| `semver.constraint(text) -> Result<Constraint, ParseError>` | See the table below |
| `semver.highest(versions, constraint) -> Version?` | The newest the constraint accepts |
| `semver.sorted(versions)` | Oldest first |
| `semver.caret(version) -> String` | The constraint to require a version with: `^1.4` for 1.4.2, `^0.3.1` for 0.3.1 |
| `version.major`, `minor`, `patch`, `pre`, `build` | `pre` is the parts after `-`: `["beta", "3"]` |
| `version.isPrerelease()`, `release()` | `release()` leaves out what follows the patch |
| `version.nextMajor()`, `nextMinor()`, `nextPatch()` | |
| `<`, `==`, `toString`, ... | `Version` is `Ordered`, `Equal` and `Show`. A pre-release comes before its release; the build plays no part |
| `constraint.allows(version)`, `isAny()` | |

| Constraint | Accepts |
|------------|---------|
| `1.2.3`, `=1.2.3` | That version |
| `^1.2.3`, `^0.3.1`, `^0.0.3` | Up to the next change of the first number that is not 0: `<2.0.0`, `<0.4.0`, `<0.0.4` |
| `~1.2`, `~1.2.3` | The last number written may grow: `<2.0.0`; `<1.3.0` |
| `1.2.*`, `1.2`, `1.x`, `*` | Every version that starts so |
| `>=1.2 <2.0`, `>=1.2, <2.0`, `!=1.5.0` | Comparisons that all hold |
| `1.0 - 2.1` | `>=1.0.0 <2.2.0` |
| `^1.0 \|\| ^2.0`, `^1.0 \| ^2.0` | Either |

A pre-release is accepted only by a range that names a pre-release of the same version, so that `^1.0` never takes `2.0.0-beta.1`, nor `1.3.0-rc.1`.

A number is written without zeros in front, as semver.org has it: `01.2.3` and `1.0.0-alpha.01` are not versions, `0.1.0` and `1.0.0-0a` are. A `ParseError` says why, with `describe()`, which is also how it prints.

---

## `std.encoding` and `std.hash`

| Function | Result |
|----------|--------|
| `encoding.toBase64(text)`, `fromBase64(encoded) -> String?` | Reading back gives `null` for a text not in that encoding; with its padding of `=` or without |
| `toBase64Url`, `fromBase64Url` | The variant that is safe in an address, written without padding and read with it or without |
| `toHex`, `fromHex` | Two hexadecimal digits per byte |
| `toUrl`, `fromUrl` | As a value in the query of an address |
| `toHtml`, `fromHtml` | Safe in a page: `<`, `>`, `&`, `'` and `"` as `&lt;` and the like. Whatever a visitor typed goes through `toHtml` before `http.html` |
| `bytes(text) -> [Int]`, `fromBytes(bytes) -> String` | The bytes of a text in UTF-8, and the text made of some bytes. Bytes that are not UTF-8 stay as they are, as in a text `fromHex` gives |
| `hash.sha256(text)`, `sha1`, `md5` | Hexadecimal digits |
| `hash.crc32(text) -> Int` | |
| `hash.hmacSha256(key, text)` | A signature |
| `hash.matches(text, sha256Sum) -> Bool` | Whether the text has that `sha256`: for checking a download against its published sum |

These hashes are made to be fast: they are for telling contents apart and for signing, not for storing passwords.

---

## `std.url`

```rust
import std.url;

let page = url.parse("https://shop.example:8443/search?q=red+shoes&page=2#top").expect("an address");
page.host                       // "shop.example"
page.port                       // 8443, an Int?
page.param("q")                 // "red shoes": decoded
page.withParam("page", "3")     // a new Url; println writes it out encoded
page.resolve("../help")         // a link found on that page, made whole: Result<Url, ParseError>
```

A `Url` is a struct with every part: `scheme`, `user: String?`, `host`, `port: Int?`, `path` (decoded), `query: [(String, String)]` in the order written, `fragment: String?`. One can be written as a literal, `url.Url { scheme: "https", host: "api.example", path: "/v1/users" }`, and the parts that need it are encoded when it is written out.

| Function | Result |
|----------|--------|
| `url.parse(text) -> Result<Url, ParseError>` | A relative address (`/a?b=c`, `../d`) is one too, with no scheme and no host |
| `url.queryOf(text) -> [(String, String)]` | `"a=1&b=x+y"` taken apart and decoded |
| `u.param(name) -> String?`, `u.params(name) -> [String]` | The first value of a name in the query; all of them |
| `u.withParam(name, value)`, `u.withoutParam(name)`, `u.withPath(path)` | A new `Url` |
| `u.resolve(link) -> Result<Url, ParseError>` | A link against this address, as a browser does |
| `u.origin()` | `"https://shop.example:8443"` |
| `u.isAbsolute()` | It has a scheme |
| `u.queryText()`, `u.toString()` | Written out, encoded |

---

## `std.http`

**Asking a server.**

```rust
match http.get("https://example.com/api/users/1") {
    case Ok(response): { println(response.status, response.body); }
    case Err(error): { println(error.describe()); }
}

let created = http.to(Method.Post, url).header("Authorization", token).json(document).timeout(5000).send();
```

A server that answers "not found" has answered: that is a `Response` with status 404. An `Error` is for when there was no answer at all, and its `kind` says why: `Timeout`, `Refused`, `UnknownHost`, `BadUrl`, `Other`, and for a server of your own that cannot listen, `InUse` when the port is taken. `error.describe()` says it in a sentence, which is also how an `Error` prints: `no answer from https://example.com/x: ...`, or for a server `cannot listen on port 8080: the port is taken`.

| Function / method | Result |
|-------------------|--------|
| `http.get(url)`, `post(url, body, contentType)`, `postJson(url, document)` | `Result<Response, Error>` |
| `http.to(method, url)` | A request built a part at a time: `.header`, `.query`, `.body`, `.json`, `.timeout`, then `.send()` |
| `response.status`, `body`, `headers` | Header names are in lower case |
| `response.ok()`, `header(name)`, `json()` | |

**Being one.** A handler is a function from a `Request` to a `Response`. A `Router` chooses one from the method and the path:

```rust
let app = http.router();
app.get("/hello/:name", fn(request: Request) -> Response {
    return http.text("hello, ${request.params.get("name") ?? "you"}");
});
app.post("/notes", createNote);
http.serve(8080, app.handler()).expect("a free port");
```

| Function / method | Result |
|-------------------|--------|
| `http.serve(port, handler, host = "127.0.0.1")` | Answers every request, each in a [task](#stdtask) of its own, for as long as the program runs. It returns only if the port cannot be listened on: nothing can close the server it makes |
| `http.listen(port, host = "127.0.0.1") -> Result<Server, Error>` | Port 0 lets the system choose: `server.port`. `server.host` is the IP listened on |
| `server.handleAll(handler)` | Answers every request, each in a task of its own, until the server is closed |
| `server.handle(count, handler)`, `next()`, `reply(response)` | That many requests one after the other, or one at a time by hand. `next()` is `null` once the server is closed |
| `server.nextWithin(milliseconds) -> Request?` | `next`, giving up after that long: `null` if no request came |
| `server.answer(request, response)` | Answers a request `next` gave, whichever it was: several can be in progress |
| `server.close()` | Stops taking requests, waits for those in progress to be answered, ten seconds at most, then closes |
| `router.get`, `post`, `put`, `patch`, `delete`, `on(method, path, handler)` | `:name` takes one part of the path into `request.params`; a last `*` takes the rest, as `"rest"` |
| `router.answer(request) -> Response` | The answer, without a server: how routes are tested |
| `http.request(method, address, body = "") -> Request` | A request as a server hands it over, to give `router.answer`: `http.request(Method.Get, "/notes?tag=work")`. No headers; its sender is `"test"` |
| `http.methodOf(name) -> Method?` | `methodOf("post")` is `Method.Post`, whatever the case; `method.name()` is `"POST"` |
| `http.queryOf(text) -> Dict<String>` | `"a=1&b=x+y"` as a dictionary, decoded |
| `http.text(body)`, `html(body)`, `sendJson(status, document)`, `respond(status, body)`, `notFound()`, `redirect(to)` | Answers |
| `http.files(folder)` | A handler that serves the files of a folder, named by what a route's `*` takes: `app.get("/static/*", http.files("public"))`. A folder gives its `index.html`; the content type comes from the extension; what is not there, or tries to leave the folder with `..`, is 404 |
| `response.with(name, value)` | One more header |
| `request.method`, `path`, `query`, `headers`, `body`, `params`, `sender`, `header(name)`, `json()` | |

A path nobody takes is answered with 404, one taken only for other methods with 405. A `HEAD` request that no route takes for `HEAD` is answered by the `GET` route of its path, with that route's status and headers and no body. A handler that stops, by a `panic` or any other failure, does not stop the server: its request is answered with 500 and the failure goes to the error output.

`request.query` holds one value for a name: in `?a=1&a=2`, the last, `"2"`. For all of them, read the address with [`std.url`](#stdurl): `url.queryOf(text)` keeps every pair in order, and `u.params("a")` gives `["1", "2"]`.

**Stopping a server.** `serve` answers for as long as the program runs. A server that is to stop is listened on, answered with `handleAll` in a task, and closed:

```rust
let server = http.listen(8080).expect("a free port");
let serving = task.run(fn() { server.handleAll(app.handler()); });
// ... until it is time to stop
server.close();     // the requests in progress are answered first
wait serving;       // handleAll has returned
```

While it closes, a request that comes in, or one still unanswered after ten seconds, is answered 503, `the server is closing`. Once `close` returns, the port is free.

With `serve` and `handleAll` a handler that waits, for another server or for the clock, does not keep the other requests waiting; handlers still run one at a time between their waits ([tasks.md](tasks.md)). `handle` answers one request after the other.

**Where a server listens.** On this machine only, `127.0.0.1`, unless told another host, an IP or a name: `"0.0.0.0"` is every interface of IPv4, `"::"` every interface of IPv6. A webhook receiver that a container or another machine must reach listens on one of these:

```rust
http.serve(8080, app.handler(), "0.0.0.0").expect("a free port");
```

A server listening on another address than `127.0.0.1` can be asked by anyone who can reach the machine: check who asks (a token, a signature) before doing what they ask. A host that is not one is an `Error` of kind `BadUrl`, a name no machine has `UnknownHost`, an address that is not this machine's `Other`; the error says `cannot listen on port 8080 of 10.1.2.3: ...`.


---

## `std.sql`

```rust
import std.sql;
import std.sql.{Value};

let db = sql.open("shop.db").expect("the database");
defer db.close();

db.exec("CREATE TABLE IF NOT EXISTS parcels (id TEXT PRIMARY KEY, city TEXT, grams INTEGER)")?;
db.exec("INSERT INTO parcels VALUES (?, ?, ?)", [Value.Text("p-1"), Value.Text("Lyon"), Value.Int(1200)])?;

for row in db.query("SELECT id, grams FROM parcels WHERE city = ?", [Value.Text("Lyon")])? {
    println(row.text("id"), row.int("grams"));        // p-1 1200
}
```

A database is a file of SQLite (a path, or `:memory:`), or a server, by its address: `postgres://user:secret@host:5432/shop`, `mysql://user:secret@host:3306/shop`, what follows `?` passed to the server (`?sslmode=require`, `?tls=true`). A program is the same on all three: a parameter is written `?` everywhere, and Postgres is given `$1`, `$2`. Waiting for the database lets the other tasks go on.

| | |
|---|---|
| `sql.open(address) -> Result<Db, Error>` | A file that is not there is made |
| `db.exec(statement, params = []) -> Result<Int, Error>` | A statement that gives no rows: how many rows it changed |
| `db.query(statement, params = []) -> Result<[Row], Error>` | The rows a statement gives |
| `db.one(statement, params = []) -> Result<Row?, Error>` | The first row, `null` when there is none |
| `db.begin() -> Result<Tx, Error>` | A transaction: `tx.exec`, `tx.query`, `tx.one` as on `db`, then `tx.commit() -> Result<Void, Error>` or `tx.rollback()` |
| `db.transaction(fn(tx: Tx) -> Result<T, Error>) -> Result<T, Error>` | Committed when the function gives `Ok`, rolled back when it gives `Err` |
| `db.kind()`, `db.close()` | `"sqlite"`, `"postgres"` or `"mysql"`; closing twice is closing once |
| `row.text(column) -> String?`, `int`, `float`, `bool` | `null` when the column is `NULL`, of another kind, or not there; `bool` takes 0 and 1, as SQLite and MySQL keep a truth |
| `row.get(column) -> Value?`, `row.isNull(column)`, `row.columns()` | The value as it came; `println(row)` shows `{id: "p-1", grams: 1200}` |

A `Value` is `Null`, `Int(Int)`, `Float(Float)`, `Text(String)` or `Bool(Bool)`, given as parameters and found in rows. A decimal comes as an `Int` when it is whole (`SUM`, `COUNT` on Postgres and MySQL), a `Float` otherwise; a number beyond an `Int` as its digits, a `Text`; a moment as a text of ISO 8601; bytes that are not text as `\x` and their hex.

In a transaction, a statement that fails leaves the transaction as it was, on every database (Postgres is given a savepoint around each). A table made, changed or dropped in a transaction is so in it on SQLite and Postgres; MySQL, which would commit the transaction for it, has it done beside, at once. `Error` has a `kind`: `Unreachable`, `Query` (the words, a table or a column), `Constraint` (a key taken, a `NULL`, a reference), `Conflict`, `Closed` or `Other`, and a `message`. `Conflict` is the database ending a transaction for another that ran at the same time: nothing more runs in it, its commit fails, and the work is to be done again in a new one.

In a flow, a database is the world: it is reached from a step. A step that writes only to the database the flows are kept in asks for it with `flow.store()`, and what it writes is kept with what it returns, in one transaction ([flow.md](../../concepts/steps.md#steps-in-the-stores-database)).

A depot's parcels, run on each of the three: [`test/e2e/cases/sql_depot.qi`](../../../test/e2e/cases/sql_depot.qi).

---

## `std.ops`

The traits that give a struct or an enum the syntax of the builtin types, and what a generic function asks of its type parameters. They are explained, with how to implement them, in [traits.md](traits.md#operators-loops-and-brackets).

| Trait | Method to write | Gives |
|-------|-----------------|-------|
| `Equal` | `equals(other: Self) -> Bool` | `==`, `!=` |
| `Ordered` | `compare(other: Self) -> Int` | `<`, `<=`, `>`, `>=`; `isBefore`, `isAfter` |
| `Add`, `Subtract`, `Multiply`, `Divide` | `plus`, `minus`, `times`, `dividedBy` | `+ - * /`, `+=` ... |
| `Show` | `show() -> String` | What `println`, `toString` and `"${...}"` write, for the value alone and wherever it is held: in an array, a field, an enum, a tuple |
| `Iterable<T>` | `iterate() -> [T]` | `for item in value` |
| `Indexed<K, V>`, `IndexedSet<K, V>` | `at(key) -> V`, `put(key, value)` | `value[key]`, `value[key] = v;` |

`Int`, `Float`, `String` and `Bool` implement `Equal` and `Show`; all but `Bool` implement `Ordered`.

### `Range`

What `0..10` is when it is kept instead of looped over: `let teens = 13..20;`. A struct, made by writing a range (no import is needed for that; `import std.ops.{Range};` to name the type). It knows its numbers without holding them, and a loop over one counts.

| Field or method | |
|-----------------|--|
| `from`, `to`, `inclusive`, `by` | The ends as written, whether `to` is one of the numbers (`..=`), and the step, 1 unless `step` said otherwise |
| `step(by: Int) -> Range` | One number every `by`: `(0..10).step(3)` is 0, 3, 6, 9. Below 0 it goes down from `from`: `(10..0).step(-2)` is 10, 8, 6, 4, 2. A step of 0 stops the program |
| `reversed() -> Range` | The same numbers, from the last to the first |
| `len() -> Int`, `isEmpty() -> Bool` | How many numbers. A range that cannot move towards its end (`10..0`) holds none |
| `first() -> Int?`, `last() -> Int?` | `null` when there is none |
| `contains(n: Int) -> Bool` | Whether `n` is one of them, the step counted |
| `toArray() -> [Int]`, `map<U>(f) -> [U]`, `filter(wanted) -> [Int]` | The numbers, or what a function makes or keeps of them |

It is written as its range: `0..10`, `(0..=10).step(5)`.

For any type that implements the trait named:

| Function | Result |
|----------|--------|
| `max<T: Ordered>(a, b)`, `min(a, b)` | The first when neither comes first |
| `largest<T: Ordered>(items) -> T?`, `smallest(items) -> T?` | `null` for an empty array |
| `sorted<T: Ordered>(items) -> [T]` | From first to last; equal items keep their order |
| `contains<T: Equal>(items, wanted)`, `indexOf(items, wanted) -> Int?` | |
| `same<T: Equal>(first, second) -> Bool` | Equal items in the same order |
| `join<T: Show>(items, separator) -> String` | The texts of the items |

```rust
import std.ops;
import std.ops.{Ordered};

struct Version { major: Int; minor: Int; }
impl Ordered for Version {
    fn compare(other: Version) -> Int {
        if self.major != other.major { return self.major - other.major; }
        return self.minor - other.minor;
    }
}

let newest = ops.largest(versions);
if newest != null && newest > installed { ... }
```

### Templates

A `Template` is a text written with `${...}` in it, kept in its pieces: `parts`, what was written around the values (one more than there are values), and `values`, what each `${...}` gave, as text. `text()` puts them together. A function whose parameter is a `Template` gets those, where one taking a `String` would get the text:

```rust
fn shout(t: Template) -> String { ... }
shout("hello ${name}, ${count} times");   // parts ["hello ", ", ", " times"], values [name, count]
```

The function can then tell what the program wrote from what was put in it, which is how a query or a page can escape what is put in them.

A text written in the program becomes a `Template` wherever that is the type it must have: an argument, a variable or a field declared `Template`, a `return` from a function that returns one, an item of a `[Template]`. So a command can be kept, built by a function or carried in a struct:

```rust
struct Job { name: String; command: Template; }

fn deploy(target: String) -> Template { return "make deploy ENV=${target}"; }

let jobs = [Job { name: "lint", command: "composer pint-test" }, Job { name: "deploy", command: deploy(env) }];
for job in jobs { sh.run(job.command); }
```

Like `Result`, the name `Template` needs no import. A `String` made elsewhere is refused where a `Template` is expected, since nothing would then say which of it was written and which was put in. Everywhere else, `"${...}"` is a `String` as always, and `t.text()` turns a `Template` into one.

---

## `std.task`

Functions running at the same time, and channels between them. All of it, with how a program of several tasks ends, is in [tasks.md](tasks.md).

```rust
let pages = wait task.map(urls, url -> http.get(url));     // all asked at once

let lines = task.channel<String>(16);
task.run(fn() {
    for name in files { wait lines.send(io.readFile(name).or("")); }
    lines.close();
});
lines.each(fn(text: String) { println(text.len()); });
```

One task runs at a time, until it waits: for a task, a channel, the clock, the network, a line of input, another program. What a task waits for in the program is written `wait`: `wait job`, `wait lines.receive()`, `wait time.after(500.milliseconds())`, and `wait match { ... }` for the first of several. Tasks share everything and need no locks, as long as a change does not have a wait in its middle.

| Function / method | Result |
|-------------------|--------|
| `task.start(work) -> Task<T>` | Runs `work` on its own; returns at once |
| `task.run(action) -> Task<Bool>` | The same for a function that returns nothing |
| `wait task -> Result<T, Failure>` | What it returned, or the failure that stopped it |
| `task.isDone()` | |
| `wait task.all(tasks) -> Result<[T], Failure>` | Every value in order, or the first failure |
| `wait task.settle(tasks) -> [Result<T, Failure>]` | How each ended |
| `wait task.map(items, work) -> Result<[U], Failure>` | One task per item |
| `task.pause()`, `task.current()` | Lets the others have a turn; the number of the task that asks |
| `task.channel<T>(capacity) -> Channel<T>` | Capacity 0: a send waits for its receive |
| `wait channel.send(value)`, `wait channel.receive() -> T?`, `close()` | `receive` gives `null` once closed and empty |
| `channel.each(action)`, `drain()`, `len()`, `isClosed()` | |
| `wait match { case let v = channel.receive(): ...; case time.after(2.seconds()): ... }` | Whichever comes first: [tasks.md](tasks.md#the-first-of-several-wait-match) |
| `Waitable<T>` | What `wait` waits for; a type of the program can implement it: [tasks.md](tasks.md#waitables) |

A task that stops does not stop the program: whoever waits for it gets the failure. If nobody ever does, the failure ends the program once the other tasks are done. Tasks that all wait for each other end it with error `R0009`.

---

## `std.compiler`

The front of the compiler, asked about a program instead of told to run it. Every function takes the path of a file and its text: the text is what is looked at, the path says where its modules are and how places are named.

```rust
let source = io.readFile("main.qi").expect("a file");
for problem in compiler.check("main.qi", source) {
    println(problem.describe());        // main.qi:4:9: error E1001: undefined identifier 'totl'
}
```

| Function | Result |
|----------|--------|
| `compiler.check(path, source) -> [Problem]` | Errors and warnings: `severity`, `code`, `message`, `at`, `endColumn`, `suggestion` |
| `compiler.symbols(path, source) -> [Symbol]` | What the file declares: `kind`, `name`, `detail`, `container`, `line`, `column` |
| `compiler.describe(path, source, line, column) -> Description?` | The name at that place: its `signature`, the `doc` above its declaration, where it is `declared` |
| `compiler.complete(path, source, line, column) -> [Suggestion]` | What could be written there: after a dot, what the value before it has. The line being typed need not make sense |
| `compiler.open(path, text)`, `close(path)` | A file being edited: importing it gives this text, not what is on disk |

Lines and columns count from 1, columns in characters. `describe` answers `null` for a program with errors that keep it from being understood; `complete` then works from the file without the line being typed.

It is what the [language server](editor.md) is written with, and serves as well for a linter or a documentation generator: [`internal/lsp/server.qi`](../../../internal/lsp/server.qi) is the longest example.

---

## `std.test`

```rust
import std.test;

fn main() -> Int {
    return test.suite("prices")
        .test("a total adds up", fn() { test.equal(total([1, 2, 3]), 6); })
        .test("an empty basket costs nothing", fn() { test.equal(total([]), 0); })
        .run().exitCode();
}
```
```
prices
  ok    a total adds up
  FAIL  an empty basket costs nothing
        expected 0, got 1
        at prices.qi:6
1 passed, 1 failed
```

A check that does not hold stops its test; the suite goes on with the next and reports at the end. So does any other failure in a test: an index out of bounds is a failed test, not the end of the run.

| Function / method | |
|-------------------|---|
| `test.suite(name)`, `.test(name, body)` | |
| `.run() -> Report` | Runs and prints. `.check()` runs without printing |
| `report.ok()`, `passed()`, `failed()`, `exitCode()`, `lines()`, `results` | |
| `equal(got, want)`, `notEqual` | As `==` has it: by what the values hold, arrays and structs too |
| `same(got, want)` | Two arrays with equal items in the same order; a failure says which item differs |
| `near(got, want, tolerance)` | For numbers with a point |
| `isTrue(condition, what)`, `isFalse` | |
| `present(value, what) -> T`, `absent(value, what)` | For optionals; `present` gives the value |
| `contains(text, part)` | |
| `fails(kind, action) -> Failure` | The function must stop with a failure of that [kind](error-handling.md#surviving-attempt) |
| `fail(message)` | For a check of your own |

### Test files: `qoqi test`

A suite needs a `main` that builds it. `qoqi test` writes that `main` for you: put the tests in a file whose name ends in `_test.qi`, one function each, named `test...`.

```rust
// prices_test.qi
import std.test;
import shop.prices;

fn testATotalAddsUp() {
    test.equal(prices.total([1, 2, 3]), 6);
}

fn testAnEmptyBasketCostsNothing() {
    test.equal(prices.total([]), 0);
}
```
```text
$ qoqi test
shop/prices_test.qi
  ok    a total adds up
  ok    an empty basket costs nothing
2 passed
```

| | |
|---|---|
| `qoqi test` | Every test file under the current folder, however deep. Folders whose name starts with a dot are left out |
| `qoqi test shop prices_test.qi` | The test files under these folders, and these files |
| `qoqi test --filter=basket` | Only the tests whose name holds the text, whatever its case |

A test is a function at the top of a test file whose name starts with `test`, that takes nothing and gives nothing back. It is reported by the words of its name: `testReadsJSONText` is "reads JSON text". The other functions of the file are helpers, and so is a `testData()` that returns something.

Each test file is a program of its own, with the modules of its [project](../configuration.md) in reach, and no `main`. Without a project file, module paths start at the folder `qoqi test` runs in: from the folder that holds `shop`, `shop/prices_test.qi` imports `shop.prices`. The tests of a file run in the order they are written; one that fails does not stop the next. The status of `qoqi test` is 0 when every test passed, and 1 when one failed or a file could not be run: it does not compile, or a test ended the program with `os.exit`.

The example flows have test files: `qoqi test examples/flows`.

---

## `std.layout`

Text laid out to fit a width. What is to be written is described once, as a document: pieces of text, and the places where a line may end. Writing it out decides which of those places are used.

```rust
import std.layout;
import std.layout.{Doc};

fn list(items: [Doc]) -> Doc {
    return layout.group(layout.concat([
        layout.text("["),
        layout.indent(layout.concat([Doc.Soft, layout.join(items, layout.concat([layout.text(","), Doc.Line]))])),
        Doc.Soft,
        layout.text("]"),
    ]));
}

print(layout.render(list(words), 80, 4));     // [one, two, three], or an item to a line
```

A group is written on one line if it fits, and otherwise every line end directly in it is taken: all or none. Groups inside it decide for themselves.

| | |
|---|---|
| `layout.text(text)`, `concat(parts)`, `join(items, separator)` | Text, and documents one after the other |
| `layout.group(doc)` | On one line if it fits. `layout.broken(doc)` takes its line ends whether it fits or not |
| `layout.indent(doc)` | The lines that start inside it start one step further in |
| `Doc.Line`, `Doc.Soft` | Where a line may end: a space, or nothing, in a group that fits |
| `Doc.Hard` | Where a line ends. Two in a row leave an empty line; more leave no more than that |
| `Doc.IfBroken(taken, fitting)` | One document or the other, by whether the group around it fits: the comma after a last item |
| `layout.fill(items)` | Items separated by commas that fill each line before starting the next |
| `Doc.Trailing(comment)` | A comment for the end of the line, wherever that comes to be; those of lines that follow each other start in one column |
| `Doc.Item(doc, open)`, `Doc.Gap` | Things written under each other: one that comes to take several lines gets an empty line around it |
| `Doc.Verbatim(text, indent)` | Text over several lines, written as it is |
| `layout.render(doc, width, step) -> String` | The text: lines of `width` characters at most where it can be helped, `step` spaces a level |
| `layout.forces(doc) -> Bool` | Whether it holds a line end that is always taken |

It is what the formatter is written with, and serves as well for printing JSON, a query, or a program in another language.

---

## `std.formatter`

The formatter of Qoqi as a function: the one [`qoqi fmt`](formatting.md) and the language server run.

```rust
import std.formatter;

match formatter.format(source) {
    case Ok(tidy): { io.writeFile("main.qi", tidy); }
    case Err(problem): { println(problem.describe()); }      // 12:5: expected ';', found 'let'
}
```

| | |
|---|---|
| `formatter.format(source) -> Result<String, Problem>` | The text of a program, formatted. A text that is formatted already comes back as it is |
| `Problem` | Where the text stops reading as a program: `line`, `column`, `message`, `describe()` |
| `formatter.width`, `formatter.step` | 100 and 4: how long a line may be, how far a level is indented |

What it decides is in [formatting.md](formatting.md). It needs a text whose brackets close and whose statements end; names and types it does not look at.

---

## How It Is Built

The library has two halves.

**Qoqi source.** Each module is a `.qi` file in `internal/stdlib/lib/`, compiled into the `qoqi` binary. Most of the library is written here, in the language itself: `gcd`, `sorted`, all of `List` and `Dict`. These are ordinary modules; a program importing one gets its functions compiled along with its own.

**Native functions.** What cannot be written in Qoqi, because it needs the machine (files, the clock) or would be too slow (searching a text), is declared in the module without a body:

```rust
pub extern fn sqrt(x: Float) -> Float;
```

and provided by a Go function in the virtual machine (`internal/vm/native_*.go`) registered under the function's full name, `std.math.sqrt`. See [`extern fn`](modules.md#functions-the-runtime-provides).

The package `std` is found like any [package](../configuration.md#packages-modules-from-somewhere-else); the only difference is that its folder is inside the binary. In messages its files appear as `<std>/strings.qi`. A project that lists a package named `std` in its `qoqi.json` gets that one instead, whole.

**Natives hand back simple things.** A native function cannot make a struct or a value of an enum: those are made by compiled code, which knows what each must hold. So a native returns a number, a text, or arrays of them, and the Qoqi half of the module builds the types the program sees. `json.parse` is the clearest case: Go checks the text and lists its tokens, and thirty lines of Qoqi build the tree of `Json` values from them. An operation that fails gives `null` and keeps the reason, which the Qoqi half fetches to build its error value.

**The web server** is split the same way, for another reason: only one task of a program runs at a time. Go accepts connections and reads requests, each in its own goroutine, and queues them; the program takes them one at a time, works out each answer in Qoqi, in a task of its own if it wants several in progress, and hands it back under the number of its request.

**Tasks** are mostly Qoqi as well. The machine provides tasks by number and three things to do with them: suspend the one that asks, resume another, wait for one to end. `Task<T>` and `Channel<T>` are written on those, which is how they come to be typed and collected like any other struct ([tasks.md](tasks.md#how-it-works)).

A folder called `std` among a program's own modules is not reachable as `std.something`: the name belongs to the library.
