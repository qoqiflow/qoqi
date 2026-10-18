# Qoqi Language – Core Spec (v0.1 draft)

Focus: a small, statically checked core that compiles cleanly to the luvm register VM. HTML/routing/macros live above this layer.

## 0. Goals
- Static checks before execution
- Familiar syntax (TS/Rust/Go vibes)
- Straightforward lowering to luvm

## 1. Lexical Structure
- Source files: UTF-8 `.qi`, one file = one module (see [Modules & Imports](#6-modules--imports)).
- Identifiers: `Letter {Letter | Digit | _}`, case-sensitive.
- Soft keywords: `case`, `match`, `in`, `as`, `defer`, `import`, `pub`, `extern`, `struct`, `enum`, `trait`, `impl` are identifiers where the tokens around them say so, and keywords everywhere else. One is an identifier after `let`, `const`, `fn`, `for`, `.` or `?.`, and before a token that only follows a value (`.`, `?.`, an assignment, `++`, `--`, `;`, `,`, `:`, `)`, `]`, `}`, `->`, `in`, a binary operator other than `-`, `<` and `>`). It is one before `(` or `[` too, except `match`, `case`, `as` and `impl`, and before `{` after `if`, `while`, `match` or `in`. A statement that is the word alone (`import;`) keeps the keyword. `in` is the keyword only right after the variable of a loop (a name or `)`). `wait` is a word only where a name could not be: before a name, a number, a text or `match`; before `(` or `[`, and anywhere else, it is a name. The other keywords are reserved.
- Keywords: `fn let const struct enum trait impl if else match case while for in break continue defer return true false null import as pub extern`, plus the type names `Int Float Bool String Any Void Self`.
- Literals: ints (`123`), floats (`1.0`, `10e3`), strings (`"hi"`), bools, `null`. No char literal in v0. A leading `-` is the unary operator, so `-42` is the negation of `42`.
- String literals stay on one line and support the escapes `\n \t \r \0 \\ \" \$`, and `\u{...}`: a character by its number, one to six hexadecimal digits (`\u{e9}`, `\u{1F600}`).
- **Interpolation.** `${expr}` inside a string literal is replaced by the text of the expression's value, as `toString` gives it: `"total: ${n} items"`. The expression is any expression, including strings with interpolation of their own, and must close on the same line. The literal is then an expression of type `String`, equal to the concatenation of its parts; the expressions are evaluated left to right, each time the string is. `$` not followed by `{` and `{` not preceded by `$` are ordinary characters; `\$` writes a dollar sign that does not start an interpolation.
- Comments: `// line`, `/* block */` (non-nested). A first line that starts with `#!` is a comment too, for scripts: `#!/usr/bin/env qoqi`.

## 2. Types

### Union Types
- Syntax: `T1 | T2 | ... | Tn` (e.g. `Int | String`)
- A union type accepts a value of any listed type.
- Usage:
  - Variable declaration: `let x: Int | String = ...`
  - Function parameter: `fn foo(val: Int | Float) -> String { ... }`
  - Function return: `fn parse(val: String) -> Int | Error { ... }`
- Compatibility:
  - A value of type `T` can be assigned to a union type variable if `T` is included in the union.
  - Operations on a union type variable require type checking (e.g. via `match` or future refinement syntax).
  - Unions can be combined with optional types: `Int | String?`.
  - Unions are not implicitly converted: a value must explicitly correspond to one of the types.
  - Unions are statically checked.
- `let` mutable, `const` immutable; must initialize at declaration. Reassigning a `const` is a static error (`E2022`).
- Assignment: `x = x + 1;`. Compound forms `+=`, `-=`, `*=`, `/=`, `%=` read the target, combine and store back; the target is evaluated once (`xs[next()] += 1` calls `next` once). `x++;` and `x--;` are `x += 1;` and `x -= 1;`. All of these are statements: they have no value.
- Blocks create scope: `{ let x = 1; }`.
- Control flow:
  - `if/else` with `Bool` condition.
  - `while` with `Bool` condition.
  - `for item in items { ... }` over arrays or strings; a string yields its characters.
  - `for i, item in items { ... }` also gives the position, from 0.
  - `for i in low..high { ... }` counts from `low` up to, not including, `high`; `low..=high` includes it. Both are `Int`, worked out once before the first iteration; nothing runs when `low` is not below `high`. There and as a `match` pattern a range is counted or compared with, and nothing is made for it. Anywhere else it is a value: a `Range` of `std.ops` (a struct: `from`, `to`, `inclusive`, `by`), which the program need not import to make. `(0..10).step(2)`, `.reversed()`, `.contains(n)`, `.len()`, `.toArray()`, `.map(f)`, `.filter(f)`; a loop over one counts without making an array. Its ends are `Int` (`E2038`).
  - The loop's variables cannot be assigned (`E2026`). Assigning to the index would not move the loop, and assigning to the element would not change the collection; both look like they would. To change the collection write `items[i] = ...`; to control the position use `while`. Changing a value *through* the element is fine: `for p in points { p.x = 0; }`.
  - `break`/`continue` only in loops.

### Enums
Full description: [enums.md](enums.md).

- `enum Shape { Circle(Float), Rect(Float, Float), Empty }`: a type whose values are one of the variants, each with the values it holds. Variants may hold any types, the enum itself included. `enum Option<T> { Some(T), None }` is generic.
- A variant may name its values: `Rect { width: Float, height: Float }`. It is built with every value by name in any order, `Rect { height: 1.0, width: 2.0 }`, and matched by name, `case Rect { width, height: 0.0 }:`, where a value not named is `_`. Both are the forms by position with the values in the order declared, which remain valid. A missing, unknown or doubled name, or names for a variant declared by position, are `E8008`.
- A value is written `Shape.Circle(2.0)`, `Shape.Empty`, or the variant alone, `Circle(2.0)`, `Empty`, in expressions, patterns (`case Circle(r):`, `case Node(Leaf(a), _):`) and tests (`value is Empty`). A variant alone is of the enum among those the file declares or imports by name that has it, unless a variable, a parameter, a function, a constant or a type of that name is in scope. When several such enums have it, it is of the one expected: the declared type of a variable, a parameter or a return, the type of the value a `match` or an `is` tests, the type of the other items of an array literal, the other side of `==`. Otherwise it is `E2041`. A variant with values, not called, is the function that makes one.
- Type arguments of a generic enum come from what is written (`Option<Int>.None`), from the values (`Option.Some(1)`), then from the type expected where the value goes. If none determines one, it is an error (`E2030`).
- A value has no fields; it is taken apart by `match`, or by `value is Pattern` when one variant is asked for.
- Two values are equal (`==`) when they are the same variant holding equal values.
- Enums have methods and implement traits like structs: `impl Shape { ... }`, `impl Named for Shape { ... }`.

### Constants
- `const LIMIT = 100;` at the top level of a file, optionally with a type (`const RATIO: Float = 1.5;`) and `pub`.
- The value is made of literals, operators and other constants; nothing else (`E2032`). It is worked out when the program is compiled.
- Constants may be declared in any order and refer to each other, but not, through each other, to themselves (`E2031`).
- A constant cannot be assigned (`E2022`). A local variable or a parameter of the same name hides it.
- A constant can also be a struct, an array or a value of an enum made of constants: it is made once, the first time it is used, and is read-only with all it holds. A variable outside functions is a `let` (see Variables outside functions).

### Behaviors (Traits)
- Definition:
  ```
  trait ErrorLike {
      fn Error() -> String;
  }
  ```
- Implementation:
  ```
  struct MyStruct {}
  impl ErrorLike for MyStruct {
      fn Error() -> String { return "error"; }
  }
  ```
- Usage: Behaviors define a set of methods that a type must implement. They are similar to interfaces in other languages.
- A trait may be implemented for a struct, an enum, or one of `Int`, `Float`, `String`, `Bool`; once per type.
- Default methods: a trait method with a body is copied into every impl that does not write it.
- Type parameters: `trait Source<T> { fn next() -> T?; }`, implemented as `impl Source<Int> for Counter` or `impl<T> Source<T> for List<T>`. `Source<Int>` and `Source<String>` are different types.
- Requirements: `<T: Ordered + Show>` after `fn name`, `struct Name`, `enum Name` or `impl`. Inside, the methods of those traits can be called on a `T`; where type arguments are given or inferred, each has to implement them (`E2036`).
- The traits of `std.ops` give meaning to syntax on types that implement them: `Equal` (`==`, `!=`), `Ordered` (`<`, `<=`, `>`, `>=`), `Add`/`Subtract`/`Multiply`/`Divide` (`+ - * /` and their assignments), `Show` (`println`, `toString`, `"${}"`), `Iterable<T>` (`for`), `Indexed<K, V>` (`a[k]`), `IndexedSet<K, V>` (`a[k] = v`). See [traits.md](traits.md).
- Struct fields may have a default: `port: Int = 8080;`. A literal may leave such a field out; the expression is evaluated for each literal.
- A method without a call, on a variable or a field path, is a function value bound to it: `ages.get`.
- Self Type: `Self` can be used in trait definitions and implementations to refer to the implementing type.
  ```
  trait Cloneable {
      fn clone() -> Self;
  }
  ```

## 4. Expressions
- Primary: literals, identifiers, grouping `(expr)`, tuples `(a, b)`.
- Text over several lines: `"""` ... `"""`. The line end after the opening quotes and the one before the closing quotes are dropped, and the indentation of the closing quotes is removed from every line. `"` needs no escape inside; escapes and `${...}` work.
- Unary: `-` on `Int`/`Float`, `!` on `Bool`.
- Binary precedence (high → low): `* / %`, `+ -`, `<< >>`, `&`, `^`, `|`, `??`, `|>`, `< <= > >=`, `== !=`, `&&`, `||`. `??` binds tighter than a comparison and than `|>`: `count ?? 0 > 3` compares the count, and `name ?? "".trim()` trims either.
  - On bits: `& | ^ << >>` and unary `~`, on `Int` only, with `&= |= ^= <<= >>=`. They bind tighter than comparisons: `flags & MASK == 0` tests the mask. `>>` keeps the sign. A shift by a negative count, or one that pushes bits past the 48 an `Int` has, is a runtime error. In the pattern of a `case`, `|` separates patterns (`case 1 | 2:`); in brackets it is the operator.
  - Whole numbers are also written by their bits: `0xFF`, `0b1010`.
  - Arithmetic: `Int`/`Float`. Two `Int` give an `Int` (`7 / 2` is `3`), two `Float` a `Float`. An `Int` with a `Float` is worked out as two `Float`: `7 / 2.0` is `3.5`.
  - Comparison: numbers or strings → `Bool`.
  - Equality: any type → `Bool`.
  - Null-coalescing `a ?? b`: `a: T?`, `b: T|T?`, result `T`.
- Member/index: `user.name`, `items[0]` (`Int` index; OOB is runtime error). Indexing a string yields a one-character string.
- Unwrap: `value!` after an expression of type `T?` is a `T`; on a `Result<T, E>` it is a `T`. When there is no value (null, or `Err`) the program stops at that point as `panic` stops it, with "'!' found null where a value was expected" or "'!' found a failure:" and the failure (described when it has `describe()`). `!` on a type that cannot be null is `E2034`.
- Methods of optionals: `impl<T> T? { ... }` gives every `T?` its methods, `self` being the `T?` (it may be null). `std.option` gives them `map`, `then`, `filter`, `mapOr`, `or`, `orElse`, `each`, `expect`, `okOr`, `isSome`, `isNone`, without an import. A call on an optional finds these first; any other method of `T` still needs a null test, `?.` or `!` (`E2017`).
- Dictionary literal: `{key: value, ...}`. With texts for keys it is a `Dict<V>` (`std.dict`), with keys of any other type a `Map<K, V>` (`std.map`); where a `Dict` or a `Map` is expected, that one. Keys are of one type (`E2001`). `{}` needs the type it is to be (`E2020`). In the header of `if`, `while`, `for` and `match` a `{` is the block, as for struct literals.
- Slice: a range as the index of an array or a string gives a new array or string of the items or characters at the positions the range goes through: `items[1..3]`, `items[1..=3]`, `items[(0..9).step(3)]`, or any value of type `Range`. Between the brackets a range may leave out its start (`items[..2]`, from 0) or its end (`items[1..]`, to the end). Positions outside the collection are left out rather than failing. A slice is not assignable (`E2006`). A range without its end anywhere else is a syntax error.
- Length: `items.len()` and `text.len()` (or `items.len()`, `text.len()`) give the number of elements of an array and of characters of a string. `items.length` is an error (`E2033`) that says what to write.
- Methods on builtin types: `impl String { ... }`, `impl Int`, `impl Float`, `impl Bool` give the type methods; `impl<T> [T] { ... }` gives them to every array, `T` being the type of its items, and `impl [Name] { ... }` to the arrays of one named type, which are looked at first. `self` is the value. The call is decided when the program is compiled; through a trait (`impl Trait for String`, `impl<T> Trait for [T]`) it is found when the program runs. `std.arrays`, `std.strings` and `std.math` declare such methods for everything they do to an array, a text or a number. These three modules are part of every program that calls one of their methods, without an import; the functions the methods are made of are private to them. A call of such a method is compiled as a call of that function, for the types it is used with.
- Safe access `a?.b`, `a?.m(args)`: `a: T?`; null when `a` is null, otherwise the rest of the postfix chain applied to `a` as a `T` (`a?.b.c` reads `c` only when `a` is not null). The result is optional. An error (`E2035`) when `a` cannot be null.
- Try `a?` (postfix): for `a: T?`, the value as a `T`, or `return null;` from the enclosing function, which must return an optional or nothing. For `a: Result<T, E>`, the value as a `T`, or a return of the failure, from a function that returns `Result<U, E>`. Anything else is `E2034`. In a function expression the enclosing function is that expression.
- If as a value: `if cond { a } else { b }` where an expression is expected, with `else if` chained. An `else` is required. A branch is braces around an expression, after statements if any; one that always leaves (`return`, `break`, `continue`, `panic`) gives nothing. The branches are of one type, or `null` (the type is then optional), or two types the place expects both of. Narrowing and the names of `value is Pattern` in the condition hold in the first branch. Only the branch taken is evaluated. At the start of a statement `if` is the statement.
- Choice: `cond ? a : b` is `if cond { a } else { b }` written short, with the same types, narrowing and evaluation: `owed == null ? "in credit" : "owes ${owed}"`. It is looser than every operator and than a range, and read from the right: `s >= 90 ? "A" : s >= 75 ? "B" : "C"`. A `?` right after a value with no `:` to match is the `value?` of a failure: `r? - 1` subtracts, `r? > 0 ? a : b` tests what `r?` gives.
- Waiting: `wait w` waits until `w` has come and gives what it gives; `w` is a value of a type that implements `Waitable<T>` of `std.task` (a task, `channel.receive()`, `channel.send(v)`, `time.after(2.seconds())`, `task.all(tasks)`), anything else is `E2044`. `wait` takes the postfix expression after it, and binds tighter than `is`, `as` and every binary operator: `wait ch.receive() ?? 0`, `(wait job).expect("x")`. `wait match { case ...: ... }` waits for the first of its cases, each `case let name = w:`, `case w is Pattern:` or `case w:`, and runs that one; as a statement or as a value, like `match`. The cases on one channel take every value it can give, `null` included (`E2028`). A waitable made and thrown away by a statement is `E2043`, a task excepted. The parser writes `wait w` as the call `std.task.waitFor(w)`, importing `std.task` for it. See [tasks.md](tasks.md).
- Match as a value: `match x { case p: expr; ... }` where an expression is expected. The cases are expressions of one type (`null` among them makes it optional; the type expected at that place may take them all), or blocks that always leave, or a `return`, `break` or `continue` alone: `default: return Err(e);`. Without a `default` it must be complete over an enum (`E2028`); a case that gives nothing is `E2040`.
- Tuples: `(a, b)` of type `(A, B)`, two to six values. `pair.0`, `pair.1` read by position; `let (a, b) = pair;` and `for (a, b) in pairs` name the values, `_` skipping one. Compared by content, immutable, matched with `case (a, b):` and checked for completeness like an enum (which is what a tuple is: see [enums.md](enums.md#tuples)).
- Calls: `callee(args...)` with arity/type check. Method calls: `value.method(args...)`.
- Pipe: `value |> f(args...)` is `f(value, args...)`, and `value |> f` is `f(value)`. What follows `|>` is one call, or a function (a name, a member, a function expression); a `?` after the call applies to its result. Left to right: `a |> f |> g(x)` is `g(f(a), x)`. The parser writes the call, so every rule that applies to a call applies to it.
- Array literals: `[1, 2, 3]`, `[]`. All elements have the same type.
- Struct literals: `Point { x: 1, y: 2 }`. In the header of `if`, `while`, `for` and `match`, a `{` after a name starts the body, so a struct literal there needs parentheses: `if (Point { x: 1 }).x == 1 { ... }`.
- Short functions: `x -> x + 1`, `(a, b) -> a + b`, `() -> now()`, `x -> { ...; return y; }`. No types are written: the parameters take theirs from the function type expected where the expression stands (a parameter, a declared variable or field, a return type), and the result is that type's, or the type of the body when it is a type parameter still open. The body is one expression, one assignment, or a block. Without an expected function type it is `E2037`, except for one without parameters.
- Anonymous functions: `fn(x: Int) -> Int { return x + 1; }`. They can use the variables of the enclosing function; see Closures below.
- `&&` and `||` evaluate their right operand only if the left one does not decide the result. `a ?? b` evaluates `b` only if `a` is `null`.

## 5. Functions
- Form: `fn add(a: Int, b: Int) -> Int { return a + b; }`.
- Return type required (`Void` allowed). In `-> T`, all paths return `T`; in `-> Void`, `return;` only.
- Entry point convention: `fn main() -> Int { ... }`, or statements outside any function (a script, see §11).
- Generics: `fn map<T, U>(xs: [T], f: fn(T) -> U) -> [U] { ... }`; instantiate with `map<Int, String>(nums, toStr)`. `name<...>` is read as type arguments only when the closing `>` is followed by `(` or `{`; otherwise `<` is the comparison operator.
- Variadic arguments: `fn printf(fmt: String, args: ...Any) -> Void { ... }`. The last parameter can be prefixed with `...` to accept zero or more arguments of that type. Inside the function the parameter is an array (`args.len()`, `for a in args`). Arguments are packed when the function is called by its name; a variadic function called through a variable receives its arguments as written.
- Generic Variadic: `fn collect<T>(items: ...T) -> [T] { ... }`.
- Values of their own: `fn page(items: [String], number: Int = 1, size: Int = PAGE)`. Parameters with a value come after those without (`E0001`). A call to the function or method by its name may leave out any number of the last ones, which then take their values (`E2009` gives the range of counts accepted); the value is checked against the parameter's type, and is evaluated at the call, each time, in the scope of the function's module: it may name constants and functions, not parameters.

### Methods
- Fields are as visible as their struct. `priv name: Type;` makes a field its module's: another module reading, assigning, giving in a literal or matching it is `E2042`, and a literal there that leaves out a `priv` field without a value of its own is `E2042` too. The fields of an `extern struct` are never private. `priv` is a word only before a field's name. `std.reflect` neither shows nor changes a `priv` field.
- `static fn` in a plain `impl` (not of a trait) declares a function of the type: `Point.origin()`, `geo.Point.origin()`, `Box.of(1)` for an `impl<T> Box<T>`, whose type parameters it takes. It has no `self` (`E8012`) and is not called on a value (`E2015` says how it is). It is as visible as `pub` makes it; `Point.nothing()` is `E2015`. `static` is a word only before `fn` in an `impl`. A method that never names `self` is valid, with a warning (`E8011`).
- `impl Trait for Type { fn ... }` implements a trait; `impl Type { fn ... }` adds methods that belong to no trait.
- For a generic struct the type parameters are introduced after `impl` and repeated on the struct: `impl<T> Stack<T> { fn peek() -> T? { ... } }`, `impl<T> Sized for Stack<T> { ... }`. On a `Stack<Int>`, `peek()` then returns `Int?`.
- A method can have type parameters of its own, given or inferred at the call like those of a generic function: `fn map<U>(f: fn(T) -> U) -> Stack<U>` is called as `stack.map<String>(toString)` or just `stack.map(toString)`.
- Inside a method, `self` is the value the method was called on. It is implicit: methods do not list it among their parameters.
- Method calls are fully checked: the method must exist on the receiver's struct or trait, the arguments must match its signature, and the call has the method's return type. `Self` in a signature is the struct the method belongs to.
- A field and a method may share a name; `value.name` reads the field and `value.name(...)` calls the method. A method cannot be used as a value without calling it.
- A parameter typed with a trait accepts exactly the structs that implement it (checked at the call), and only the trait's methods can be called on it. Which struct's method runs is decided at runtime.

### Methods of a type as values
- `Type.method`, where `Type` is a struct, an enum, a trait or `Int`, `Float`, `String`, `Bool`, is a function value whose first parameter is the type and the rest are the method's: `Point.plus: fn(Point, Point) -> Point`. Calling it, `Point.plus(a, b)`, is `a.plus(b)`. On an enum a variant of that name comes first. A method with type parameters of its own, or one of a generic type, cannot be named so.

### Variables outside functions
- `let name = value;` (or `let name: Type = value;`) at the top level of a module, or of an entry file that has a `main`, declares a variable of the file. Its functions read and assign it; it is private to its module. Every such variable is made before `main` runs, in the order written, a variable whose value uses another making that one first. Any other statement outside a function there is `E8007`. A null test does not narrow one. In a script (an entry file without `main`), the top-level statements are its body and their variables are not seen by its functions.

### Closures
- In `let name = fn(params) -> Type { body };`, the long form with its types, `name` is in scope in `body`: the function can call itself. A short function (`x -> ...`) does not see the name it is given.
- A function expression captures the variables it uses **by reference**: it sees later assignments made by the enclosing function, and its own assignments are visible outside. A captured variable lives on after the enclosing function returns.
- Each iteration of a loop has its own variables: closures created in different iterations keep the values of their own iteration, including the loop's variables (`for i, item in items`). Within one iteration a closure and the loop body still share them. A variable declared outside the loop is shared by all of them.

## 6. Modules & Imports
Full description: [modules.md](modules.md).

- One module per `.qi` file. Its name is its path from the folder of the program's entry file, with dots: `physics/body.qi` is `physics.body`. A folder is not a module.
- Paths always start at that folder; there are no relative imports.
- `import physics.body;` then `body.new(1)`, `body.Body`. The module is used through the last part of its path, or through an alias: `import physics.body as pb;`.
- `import physics.body.{Body, new};` brings names in directly.
- A declaration is private to its module unless marked `pub` (`pub fn`, `pub struct`, `pub trait`). Fields are as visible as their struct, unless written `priv`. A method of a plain `impl` needs `pub fn` to be called from another module; methods that implement a trait are always visible.
- `pub import physics.body.{Body};` exports an imported name again.
- A `pub` function, or a `pub` method of a type other modules can name, may only use in its parameters and its result the types of its module that are `pub` (`E8009`): a caller would otherwise hold a value whose type it cannot write.
- Modules may import each other. Nothing runs when a module is loaded.
- A local variable hides a module of the same name.
- `extern fn name(params) -> Type;` declares a function without a body, provided by the runtime under its full name (module path and name). Only the functions of the standard library exist there today.
- The package `std` is the [standard library](stdlib.md), built into the compiler: `import std.strings;`.
- Implicit imports: a file of the program that writes the name of a module of `std` before a dot (`dict.empty<Int>()`) is read as if it imported that module, unless it imports or declares something of that name at its top level. It is read as importing `Result`, `Template`, `Dict`, `List`, `Set` and the traits of `std.ops` (`Equal`, `Ordered`, `Show`, `Add`, `Subtract`, `Multiply`, `Divide`, `Iterable`, `Indexed`, `IndexedSet`) when it names them and declares and imports none of its own. The files of `std` write their imports.
- The program starts in the `main` of the entry file, which cannot itself be imported. Only the entry file may have statements outside a function: in a module they are `E8007`.
- A project file, [`qoqi.json`](../configuration.md), can name the entry file, move the folder paths start at (`root`), and name folders of modules outside it (`packages`). It is not part of the language: a program means the same with or without it, given the same files at the same module paths.

## 7. Type Rules (grab bag)
- `if`/`while` conditions must be `Bool`.
- `for in` requires array or string.
- Names every file has without an import: the builtins (`print`, `println`, `toString`, `panic`), the methods of arrays, texts, numbers and optionals, and the modules and types of the library (see Implicit imports in §6), unless the file declares something of that name itself.
- **Equality.** `==` compares numbers as numbers, and texts, arrays, structs and values of enums by content: item by item, field by field, variant and values, recursively. A type that implements `Equal` is compared by its `equals`, also where it is held by an array, a struct or an enum. Functions are equal only to themselves. `reflect.identical(a, b)` asks whether two names are of the one value.
- Constants: `const NAME = value;` at the top level. The value is made of literals, operators, other constants, and structs, arrays and enum values made of those. A constant that is a struct, an array or a value of an enum is made the first time it is used and is that one value from then on, read-only with all it holds: assigning to one of its fields or items is `E2022` where the compiler can see it, and a runtime error through any other name.
- An `Int` is taken for a `Float` where one is expected: an argument, a variable or field declared `Float`, a value returned as one, an item among `Float`s (`[1, 2.5]`), one branch of an `if` whose other is a `Float`. It is converted there, so what is kept is a `Float`. With an operator, an `Int` and a `Float` give a `Float`, and compare as numbers (`2 == 2.0`). Never the other way: a `Float` becomes an `Int` only through `floor`, `ceil`, `round` or `trunc`. A type parameter is not guessed: `largest([1, 2.5])` works because the array is one of `Float`, `max(1, 2.5)` does not.
- **Optionals.** `T?` is "a `T` or `null`". `null` only fits `T?` or `Any`. A `T` can be used where a `T?` is expected, not the other way round. An optional cannot be operated on, indexed, or have fields read or methods called directly (`E2017`); `value ?? fallback` yields a `T`. A test against `null` narrows a local variable, a parameter or a field path (`node.next`) to `T` where the test guarantees it: in the body of `if x != null`, after `if x == null { return; }` (or `break`/`continue`), in a `while x != null` body, and on the right of `x != null && ...` / `x == null || ...`. Narrowing ends at an assignment to the variable, and does not apply inside lambdas or to a variable that a lambda assigns (it could be set to `null` whenever the lambda runs). What is known about a field also ends at the next assignment to any field and at the next call of something that may assign one, since those could change it through another reference. Calls that cannot assign a field (builtins, functions that only read) do not affect it; a call of a function value or of a function stored in a field always does.
- **Type tests.** `value is Type` is a `Bool` computed when the program runs; where it is true, the tested variable or field has that type: `if shape is Square { shape.side }`. A test that can never be true (`1 is String`) is a compile error (`E2021`). `match` accepts types as patterns (`case Int:`, `case [String]:`, `case Point:`, `case fn(Int) -> Int:`), with the same narrowing inside each case.
- **Pattern tests.** `value is P`, where `P` is the pattern of a variant or of a tuple as a `case` takes it, is a `Bool`: whether the value fits. The value is evaluated once. The names of `P` are variables of what runs only when the test held: the operands to its right in a chain of `&&`, and, when the test is the condition of an `if` or a `while` or an operand of the `&&` chain that is, the block that statement guards. They are not in scope in an `else`. A test in any other place (under `!`, an operand of `||`, a value by itself or the last operand of a chain that is a value) may not name anything (`E1003`). `P` is checked against the type of the value as a case is (`E2029`); a test that always holds is a warning (`E2023`). Where the test held, a tested variable or field is not null, and is of the enum if it was an `Any`, a trait or a union. After `is`, `Name.Name` not followed by parentheses is a type when `Name` is a module, and a tuple of names that are all types is a tuple type.
- **Patterns on enums.** `case Shape.Rect(w, h):` matches a variant and names the values it holds; the names are variables of that case. A position may also hold `_` (any value, unnamed), a literal (only that value) or the pattern of another variant, to any depth. Without parentheses (`case Shape.Rect:`) the variant is matched whatever it holds. A `match` on an enum value without a `default` must handle every value the enum can have, looking inside nested patterns (`E2028`); a case that can never run is a warning (`E2023`). Assigning a non-null value to an optional variable narrows it too.
- **Guards.** `if !(T) { ... }`, where `T` is a pattern test or an `&&` chain of them and other conditions, with no `else` and a block that ends with `return`, `break`, `continue` or a call of `panic`: the names of `T` are variables of the statements that follow the `if` in its block, which only run when `T` held. Inside the block they do not exist.
- **Patterns on structs.** `case Point { x: 0, y }:` matches a struct by its fields: a field with a pattern (a literal, `_`, a variant, another struct) is matched by it, a field written alone is named, the fields not written are not looked at. A value of a trait, a union or `Any` is tested for the struct first, and is that struct in the case. Struct patterns nest in variant patterns and the other way. One whose every field is a name or `_`, recursively, takes every value of the struct for completeness. They are written after `case`, not after `is`. The same shorthand builds a struct: `Point { x, y }` is `Point { x: x, y: y }`.
- **Several patterns.** `case A | B:` runs for any of them. They may name values when each of them names the same names with the same types (`E2029` otherwise); each name is the value of the first pattern that matched, and a condition is asked of that pattern's values.
- **Null.** `case null:` matches null; the cases below it see the value as not null. A match that gives a value, on an optional, handles null with `case null:` or `default` (`E2028`); a statement runs no case for null.
- **`Any`** accepts every value, and that is all it does: a value of type `Any` can be passed along, printed, converted with `toString` and compared with `==`. It cannot be used as a number, indexed, called, iterated or assigned to a more specific type without a cast.
- **Casts.** `value as Type` treats a value as a more specific type: out of `Any`, out of an optional or union, or from a trait to a struct implementing it. The cast is verified when it runs and stops the program if the value is something else. A cast that can never succeed (`1 as String`) is a compile error (`E2021`); one to a wider type is free. `as` binds tighter than the binary operators: `v as Int + 1` is `(v as Int) + 1`. The check goes into the value: every element of an array, and the fields of a generic struct that depend on its type arguments. A function type is compared with the signature of the function: it must accept at least what the type passes and return what it promises.
- **Constants.** A name declared with `const` cannot be assigned (`E2022`). The value it refers to can still be changed: `const xs = [1]; xs[0] = 2;` is fine.
- **Cases.** What a case of a match statement does is a block, or one statement without braces, which is the block of it: `case Err(e): return Err(e);`, `case Light.Red: stops += 1;`, `case 0: continue;`. `case a | b:` runs for either pattern and names nothing (`E2029`). `case p if cond:` runs when `p` matches and `cond`, which sees the names of `p`, holds; it is not counted when a match is checked for completeness. `case low..high:` and `case low..=high:` match numbers (`Int` or `Float`).
- **Defer.** `defer stmt;` and `defer { ... }` run when the enclosing block is left, by any way out, a failure passing through included; the defers of a block run last first, after the value of a `return` has been worked out. The body cannot leave the defer: `return`, `?`, and `break`/`continue` of an outer loop are `E2039`.
- **Returns.** A function with a return type other than `Void` must return a value on every path (`E2004`). `while true` without a `break`, and a `match` whose cases all return and which has a `default` or handles every variant of an enum, count as returning. Statements that can never run, after a `return`, `break` or `continue`, produce a warning (`E2023`).
- **Arrays.** All elements share one type. A literal takes its element type from its first element, or from the place it is used when that place has a type: `let shapes: [Shape] = [rect, square];`. An empty literal needs such a place: `let xs: [Int] = [];` (a bare `let xs = [];` is `E2020`). `xs.push(v)` requires `v` to fit the element type.
- **Structs.** A literal gives every field a value (`E2018`), and only declared fields can be read or written (`E2014`).
- **Functions as values.** A function fits a function type if it accepts at least what that type passes and returns what it promises, so `toString` (`fn(Any) -> String`) can be used as a `fn(Int) -> String`.
- **Type parameters** are opaque inside a generic function: a `T` only fits `T`, and has no operators or members. At a call, `f<Int>` has its signature with `Int` substituted, so arguments and result are checked.
- **Type argument inference.** The type arguments of a call may be left out when the arguments determine them: `first([1, 2])` is `first<Int>([1, 2])`, `map(names, toString)` infers both parameters, `stack.map(toString)` infers the method's. A parameter the arguments leave open is taken from the type expected of the result: `let xs: [Int] = none();`, `return none();`, `size(none())`. If neither determines it, it has to be written (`E3001`, or `E2024` for methods).
- **Traits.** An impl method matches the trait's by position of its type parameters, not by their names: `fn convert<U>(v: U) -> [U]` implements `fn convert<T>(v: T) -> [T]`.
- Generic type args must be concrete types (no higher-kinded). Each instantiation gets its own monomorphized copy.

## 8. luvm Mapping (sketch)
- Values: `Int`→TAG_INT, `Float`→double, `Bool`→TAG_BOOL, `String`/`Struct`/`Array`→heap ptr, `Null`→TAG_NULL, `Void`→none or TAG_UNDEF.
- Locals/temps become registers per function chunk; params occupy first registers.
- Example lowering:
  ```
  fn add(a: Int, b: Int) -> Int {
      let c = a + b;
      return c;
  }
  // a->R0, b->R1, c->R2
  0: ADD R2, R0, R1
  1: RET R2
  ```

## 9. Errors
- Static: unknown identifier, type mismatch, bad arity, assign to `const`.
- Warnings, with `qoqi run`, `check`, `test` and in the editor: a variable, a parameter or a name of a pattern nothing uses (`E1004`), an import nothing uses (`E8010`). Names starting with `_`, the parameters of a trait's method and of a short function, and the library are not counted.
- Runtime: array index out of bounds (read or write), integer overflow (an `Int` is 48 bits and never wraps), division by zero, a failed cast. The program stops with a message naming the source line.

## 10. Minimal Stdlib (host-provided)
- `fn print(args: ...Any) -> Void;` writes its arguments separated by spaces.
- `fn println(args: ...Any) -> Void;` same, then a newline.
- `fn toString(v: Any) -> String;` the text `print` would write.
- `fn panic(values: ...Any) -> Void;` stops the program with the values as its message ([errors.md](error-handling.md)). It does not return: a function may end with it in place of a `return`, and what follows it is unreachable.
- `items.push(value)` appends to an array. Assigning to `items[i]` replaces an existing element; `push` is how an array grows. It is a method of arrays like the others, and one instruction.

Values print as `42`, `2.5`, `true`, `null`, the text of a string, `[1, 2]` for arrays, `{x: 1, y: 2}` for structs and `Rect(2, 3)` or `Empty` for values of an enum.

## 11. Program Entry and Exit
- Execution starts at `fn main()`, which takes no parameters (`E6006` otherwise).
- A script has no `main`: the statements written outside any function in the entry file are its main, and run in the order they are written. Declarations may come before, between or after them, and are known everywhere. A file has a `main` or statements outside a function, not both (`E8007`).
- The variables of a script are those of its main: a closure made in the script sees them, a function or a method does not (`E1001`, which says so). What a function needs is passed to it, or declared with `const`.
- A script stops at a `return;`. When one of its returns gives a value, it is an `Int`, the exit code; reaching the end of the script exits with 0.
- When `main` returns an `Int`, `qoqi run` uses it as the process exit code (low 8 bits). Any other result exits with 0.
- A runtime error prints the message with the source location and exits with 1.
- Two limits end a runaway program with an error rather than letting it take the machine down: calls may nest 10000 deep (`--max-depth=N`), and the program may hold 256 MB at once (`--heap-mb=N`).

## 12. Grammar (EBNF sketch)
```
Program      = { Import | [ "pub" ] Decl } EOF ;
Import       = [ "pub" ] "import" ModulePath [ "." "{" Ident { "," Ident } "}" | "as" Ident ] ";" ;
ModulePath   = Ident { "." Ident } ;
Decl         = FnDecl | ExternDecl | ConstDecl | StructDecl | EnumDecl | TraitDecl | ImplDecl ;   (* "pub" not allowed before ImplDecl *)
ConstDecl    = "const" Ident [ ":" Type ] "=" Expr ";" ;      (* Expr of literals, operators and constants *)
EnumDecl     = "enum" Ident [BoundedParams] "{" [ Variant { "," Variant } [ "," ] ] "}" ;
StructDecl   = "struct" Ident [BoundedParams] "{" { Ident ":" Type [ "=" Expr ] ( ";" | "," ) } "}" ;
TraitDecl    = "trait" Ident [TypeParams] "{" { FnSignature ( ";" | Block ) } "}" ;
ImplDecl     = "impl" [BoundedParams] [ TypeName [TypeArgs] "for" ] TypeName [TypeParams] "{" { [ "pub" ] FnDecl } "}" ;
BoundedParams = "<" BoundedParam { "," BoundedParam } ">" ;   (* also after the name of a function *)
BoundedParam = Ident [ ":" TraitRef { "+" TraitRef } ] ;
TraitRef     = TypeName [TypeArgs] ;
Variant      = Ident [ "(" Type { "," Type } ")" ] ;
ExternDecl   = "extern" "fn" Ident "(" [ParamList] ")" [ "->" Type ] ";" ;
TypeName     = Ident [ "." Ident ] ;                          (* a type of an imported module *)

TraitDecl    = "trait" Ident "{" { TraitMethod } "}" ;
TraitMethod  = "fn" Ident "(" [ParamList] ")" "->" Type ";" ;

ImplDecl     = "impl" [TypeParams] [ Ident "for" ] Ident [TypeParams] "{" { FnDecl } "}" ;

FnDecl       = "fn" Ident [TypeParams] "(" [ParamList] ")" "->" Type Block ;
TypeParams   = "<" TypeParam { "," TypeParam } ">" ;
TypeParam    = Ident ;
ParamList    = Param { "," Param } [ "," VariadicParam ] | VariadicParam ;
Param        = Ident ":" Type ;
VariadicParam = Ident ":" "..." Type ;

StructDecl   = "struct" Ident [TypeParams] "{" { Field [ ";" | "," ] } "}" ;
Field        = Ident ":" Type ;

Block        = "{" { Stmt } "}" ;
Stmt         = LetDecl | IfStmt | WhileStmt | ForStmt | ReturnStmt | MatchStmt | WaitMatch
             | BreakStmt | ContinueStmt | DeferStmt | Block | AssignStmt | ExprStmt ;
DeferStmt    = "defer" ( Block | AssignStmt | ExprStmt ) ;
BreakStmt    = "break" ";" ;
ContinueStmt = "continue" ";" ;
AssignStmt   = AssignTarget AssignOp Expr ";" | AssignTarget ( "++" | "--" ) ";" ;
AssignOp     = "=" | "+=" | "-=" | "*=" | "/=" | "%=" ;
AssignTarget = Ident | PostfixExpr Index | PostfixExpr Member ;
LetDecl      = ("let" | "const") Ident [ ":" Type ] "=" Expr ";" ;
IfStmt       = "if" Expr Block [ "else" (Block | IfStmt) ] ;
WhileStmt    = "while" Expr Block ;
ForStmt      = "for" [ Ident "," ] ( Ident | TupleNames ) "in" ( Expr | Range ) Block ;
Range        = OrExpr ( ".." | "..=" ) OrExpr ;
LetTuple     = ( "let" | "const" ) TupleNames "=" Expr ";" ;
TupleNames   = "(" Ident "," Ident { "," Ident } ")" ;         (* "_" skips a value *)
ReturnStmt   = "return" [ Expr ] ";" ;
MatchStmt    = "match" Expr "{" { MatchCase } "}" ;
MatchCase    = ( "case" Pattern { "|" Pattern } [ "if" Expr ] | "default" ) ":" ( Block | Statement ) ;
                                 (* one statement is the block of it: case Err(e): return Err(e); *)
MatchExpr    = MatchStmt ;       (* as a primary expression, a case is Block, Expr, or return, break, continue;
                                    an Expr may end with "," and the last with nothing *)
WaitMatch    = "wait" "match" "{" { WaitCase } "}" ;   (* also a value, as MatchExpr *)
WaitCase     = "case" ( "let" Ident "=" Expr | PostfixExpr [ "is" Pattern { "|" Pattern } ] ) ":" ( Block | Expr ";" ) ;
Pattern      = Literal | [ "-" ] Literal | Range | Type | VariantPat | TuplePat ;
TuplePat     = "(" SubPattern "," SubPattern { "," SubPattern } ")" ;
VariantPat   = Ident [TypeArgs] { "." Ident } [ "(" [ SubPattern { "," SubPattern } ] ")" ] ;
SubPattern   = Ident | "_" | Literal | [ "-" ] Literal | VariantPat | TuplePat ;
ExprStmt     = Expr ";" ;

Expr         = OrExpr ;
OrExpr       = AndExpr { "||" AndExpr } ;
AndExpr      = EqExpr { "&&" EqExpr } ;
EqExpr       = RelExpr { ("==" | "!=") RelExpr } ;
RelExpr      = PipeExpr { ("<" | "<=" | ">" | ">=") PipeExpr } ;
PipeExpr     = NullCoalesceExpr { "|>" UnaryExpr } ;
NullCoalesceExpr = BitOrExpr { "??" BitOrExpr } ;
BitOrExpr    = BitXorExpr { "|" BitXorExpr } ;
BitXorExpr   = BitAndExpr { "^" BitAndExpr } ;
BitAndExpr   = ShiftExpr { "&" ShiftExpr } ;
ShiftExpr    = AddExpr { ("<<" | ">>") AddExpr } ;   (* what a pipe gives can be added to: n |> f + 1 *)
AddExpr      = MulExpr { ("+" | "-") MulExpr } ;
MulExpr      = UnaryExpr { ("*" | "/" | "%") UnaryExpr } ;
UnaryExpr    = ( "-" | "!" | "~" ) UnaryExpr | CastExpr ;
CastExpr     = WaitExpr { "as" OptType | "is" ( OptType | VariantPat | TuplePat ) } ;
WaitExpr     = "wait" ( PostfixExpr | WaitMatch ) | PostfixExpr ;
IfExpr       = "if" Expr ValueBlock "else" ( IfExpr | ValueBlock ) ;   (* a Primary *)
ValueBlock   = "{" { Stmt } [ Expr ] "}" ;
PostfixExpr  = Primary { Call | Index | Member | SafeAccess | "?" } ;
SafeAccess   = "?." ( Ident [TypeArgs] | IntLit ) { Call | Index | Member } ;
Call         = "(" [ ArgList ] ")" ;
ArgList      = Expr { "," Expr } ;
Index        = "[" Expr "]" ;
Member       = "." ( Ident [TypeArgs] | IntLit ) ;            (* a number: a value of a tuple *)
Primary      = Literal | Ident [TypeArgs] | "(" Expr ")" | TupleLit
             | ArrayLit | StructLit | FnExpr ;
TupleLit     = "(" Expr "," Expr { "," Expr } [ "," ] ")" ;
ArrayLit     = "[" [ ArgList ] "]" ;
StructLit    = Ident [TypeArgs] "{" [ FieldInit { "," FieldInit } [ "," ] ] "}" ;
FieldInit    = Ident ":" Expr ;
FnExpr       = "fn" "(" [ParamList] ")" [ "->" Type ] Block | ShortFn ;
ShortFn      = ( Ident | "(" [ Ident { "," Ident } ] ")" ) "->" ( Block | Expr | Assignment ) ;
TypeArgs     = "<" Type { "," Type } ">" ;

Type         = OptType { "|" OptType } ;
OptType      = SimpleType [ "?" ] ;
SimpleType   = BuiltinType | Ident [TypeArgs] | "[" Type "]" | "(" Type ")" | TupleType | FunctionType ;
TupleType    = "(" Type "," Type { "," Type } ")" ;
BuiltinType  = "Int" | "Float" | "Bool" | "String" | "Any" | "Void" | "Self" ;
FunctionType = "fn" [TypeParams] "(" [TypeList] ")" "->" Type ;
TypeList     = Type { "," Type } [ "," "..." Type ] | "..." Type ;

Ident        = Letter { Letter | Digit | "_" } ;
Literal      = IntLit | FloatLit | StringLit | "true" | "false" | "null" ;
```
