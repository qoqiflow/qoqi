# IR Generation Specification

This document describes how the desugared AST is translated into Qoqi IR, the last stage that works on the AST. It follows desugaring and closure analysis in the [compilation pipeline](pipeline.md); its output is consumed by [bytecode generation](bytecode-generation.md).

The IR data structures are in `internal/ir`, the translation in `internal/irgen`.

## The IR

An `ir.Program` is a list of functions. A function is a list of **basic blocks**; a block is a list of instructions that ends with a terminator (`RET`, `JMP` or `BR`).

```go
type Instruction struct {
    Op       OpType
    Dest     *Register        // result, or nil
    Operands []Value          // Register, Constant or *BasicBlock
    Pos      token.Position   // source position, for errors and debugging
}
```

*   **Registers are virtual and unlimited.** `%v1`, `%v2`, ... are numbered per function. Mapping them to the VM's registers is the backend's job.
*   **Registers are not single-assignment.** A variable keeps one register for its whole life and assignments write to it again. There are no phi nodes. This keeps generation direct; the backend recovers the lifetime information it needs.
*   **Constants** carry a type tag that tells the backend how to load them: `Int`, `Float`, `Bool`, `Null`, `String`, `FuncRef` (the name of a function), `Upvalue` (an upvalue index, only as a closure capture).

`qoqi dump --ir file.qi` prints it:

```
fn fib(%v1) {
b0:
	%v2 = MOV, 2
	%v3 = LT, %v1, %v2
	BR %v3, b1, b2
b1:
	RET %v1
b2:
	%v4 = MOV, "fib"
	...
```

### Operations

| Group | Operations |
|-------|------------|
| Values | `MOV` (from a register or a constant) |
| Arithmetic | `ADD` `SUB` `MUL` `DIV` `MOD` `NEG` |
| Logic and comparison | `NOT` `EQ` `NEQ` `LT` `LTE` `GT` `GTE` |
| Control flow | `JMP block`, `BR cond, then, else`, `RET [value]`, `CALL fn, args...` |
| Structs | `NEW_STRUCT name`, `GET_FIELD obj, name`, `SET_FIELD obj, name, value` |
| Length | `LEN value`, for an array or a string. Emitted in place of a call of `std.arrays.len` or `std.strings.len`, so that asking for a length in a loop condition is one instruction; the two functions exist as functions too, for when one is passed as a value |
| Enums | `NEW_VARIANT "Enum.Variant"`, its values set with `SET_FIELD obj, "0", value`, ... |
| Casts and type tests | no operation of their own: a call of the `__cast` / `__is` host function |
| Arrays | `NEW_ARRAY capacity`, `GET_INDEX arr, i`, `SET_INDEX arr, i, value`, `LEN value`, `PUSH arr, value` |
| Closures | `MAKE_CLOSURE fn, captures...`, `LOAD_UPVALUE i`, `STORE_UPVALUE i, value`, `CLOSE_UPVALUES regs...` |

`AND`, `OR` and `CALL_TRAIT` exist in the instruction set but are not produced: short-circuit operators become branches and method calls become a field read plus a call. The backend rejects them (`E6004`).

## Functions

One IR function is generated for each:

| Source | IR name | Parameters |
|--------|---------|------------|
| Top-level `fn name` | `name` | as declared |
| Method `fn m` in `impl ... for S` or `impl S` | `S.m` | `self`, then as declared |
| Function expression | `lambda_N`, numbered in source order | as declared |

A lambda is emitted **before** the function that contains it. Generic functions that were instantiated appear under their mangled names (see [monomorphization.md](monomorphization.md)).

Every function ends with a `RET`; one without a value is added if control can fall off the end.

## Variables and Scopes

Each function compiler keeps a stack of scopes mapping names to registers; a block pushes one and pops it at its end. Lookup goes from the innermost scope outwards, so shadowing works and a name declared in a block is gone after it.

**`let` gives a variable its own register.** The initializer is compiled first. If its result is a temporary (the register was created by this expression), the variable simply adopts it. If the result is *another variable's* register, it is copied:

```rust
let a = 1;      // %v1 = MOV, 1          a is %v1
let b = a;      // %v2 = MOV, %v1        b gets a copy, not a's register
b = 2;          // %v2 = MOV, 2          a is unaffected
```

Without the copy `b = 2` would overwrite `a`. The compiler tracks which registers belong to variables (`varRegs`) to tell the two cases apart.

**Identifiers** are looked up in this order:

1.  a local variable → its register;
2.  a captured variable → `LOAD_UPVALUE`;
3.  a top-level function or builtin → `MOV` of a `FuncRef`;
4.  otherwise `E5002`. Name resolution has already rejected unknown names, so this only catches names that are not values, such as a struct name used as an expression.

## Assignment

Assignments arrive as `BinaryExpr` with operator `"="`:

| Target | Generated |
|--------|-----------|
| local variable | `MOV` into the variable's register |
| captured variable | `STORE_UPVALUE` |
| `arr[i]` | `SET_INDEX arr, i, value` |
| `obj.field` | `SET_FIELD obj, "field", value` |
| anything else | `E5003` (for example assigning to a function) |

### Compound assignment

`target op= value` reads the target, applies the operator and stores the result. The target is compiled **once**, so side effects in it happen once:

| Target | Generated |
|--------|-----------|
| local variable | the operation with the variable's register as operand and destination: `%v1 = ADD, %v1, %v2` |
| captured variable | `LOAD_UPVALUE`, the operation, `STORE_UPVALUE` |
| `arr[i]` | `arr` and `i` once; `GET_INDEX`, the operation, `SET_INDEX` |
| `obj.field` | `obj` once; `GET_FIELD`, the operation, `SET_FIELD` |

A negative number literal (`-5`) is compiled as one constant rather than a negation, which is also what makes the smallest Int expressible.

## Control Flow

`if`, `while` and `for` produce the usual block structure:

```
while cond { body }          for v in xs { body }

    JMP cond                     index = 0; length = LEN xs
cond:                            JMP cond
    BR c, body, exit         cond:
body:                            BR index < length, body, exit
    ...                      body:
    JMP cond                     v = GET_INDEX xs, index
exit:                            index = index + 1
                                 ...
                                 JMP cond
                             exit:
```

`for` loops are normally rewritten by desugaring before they get here; IR generation still compiles any that reach it as a real loop, with the increment before the body so `continue` cannot skip it. With `for i, v in xs`, the body's `i` is a copy of the loop's own counter. The type checker rejects assignments to either loop variable, so the copy is never written.

`break` and `continue` jump to the exit and condition blocks of the innermost loop, tracked on a loop stack. Outside a loop they are `E5005`.

**Per-iteration variables.** Each loop on the stack also collects the registers of variables declared in its body that a closure captured. Before the jump back to the condition, and before each `continue`, `CLOSE_UPVALUES` is emitted for them, so closures made in one iteration do not see the next one's values (see [closure-spec.md](../closures.md#loops-one-variable-per-iteration)). A loop whose body creates no such closure gets nothing extra.

**Code after a terminator.** After `return`, `break` and `continue` the generator switches to a fresh block. Statements that follow (unreachable code) land there instead of after a terminator, which keeps every block well formed. Nothing jumps to these blocks and the backend drops them.

A `match` that reaches IR generation was not desugared, meaning its patterns are not supported; it is reported as `E5001` rather than compiled incorrectly.

## Defer

`defer` is compiled here because this is the stage that sees every way out of a block. Each scope keeps the list of what it deferred (`internal/irgen/defer.go`).

The ways out the compiler writes get a copy of the deferred statements, the last deferred first:

| Way out | Defers run |
|---------|------------|
| The end of a block | those of the block |
| `return`, and the return of a `?` | those of every open scope of the function, after the value is in its register |
| `break`, `continue` | those of the scopes inside the loop |

A failure can come from any instruction, so it is not a way out the compiler can write a copy for. Where the `defer` stands, the function leaves a handler with the VM instead:

```
defer close();        TRY handler, body
work();               body:    work()
                               END_TRY            ; the end of the block:
                               close()            ; the handler is taken back, then the cleanup
                      handler: close()
                               RETHROW            ; a failure in work() comes here
```

Every copy starts with `END_TRY`: from there a failure, in the cleanup itself for one, belongs to the handlers further out. The handler block is entered by the VM only; `TRY` names it as an operand so that the bytecode generator sees it as reachable and keeps the variables it reads in registers of their own.

The statements are compiled once per copy, each time with the variables that were in scope where the `defer` was written: a name declared after it does not hide the one it meant.

A function without `defer` compiles exactly as before.

## Values With Statements Inside

Desugaring turns a `match` that gives a value into `CondExpr` and `BlockExpr` ([desugaring.md](desugaring.md)). A `CondExpr` is a branch whose two sides move their value into one register, as `&&` does. A `BlockExpr` is a scope: its statements, then its value. One without a value ends in a `return`, `break` or `continue`, whose jump is already emitted; the register it gives is never read.

## Short-Circuit Operators

`&&`, `||` and `??` evaluate their right operand only when needed. The result lives in one register that is written on both paths:

```
a && b                       a ?? b

    result = a                   result = a
    BR result, rhs, end          isNull = EQ result, null
rhs:                             BR isNull, rhs, end
    result = b               rhs:
    JMP end                      result = b
end:                             JMP end
                             end:
```

`||` is the same as `&&` with the branch targets swapped. This is the only place the operators are lowered: the desugarer leaves them in the tree in every position (conditions, `let` initializers, return values, arguments), so the semantics are the same everywhere and the type checker still sees the whole condition.

### Conditions

Where `a && b` is the condition of an `if` or a `while`, its value is not needed, only where it leads. It is compiled for that (`compileCondition`): if `a` does not hold, straight to where "not" leads; otherwise `b` decides. The same for `||` and `!`, to any depth:

```rust
if n > 2 && n % 2 == 1 { odd += 1; }
```
```
	%v5 = GT, %v2, %v4
	BR %v5, b7, b5          ; not above 2: past the block
b7:
	%v8 = MOD, %v2, %v6
	%v10 = EQ, %v8, %v9
	BR %v10, b4, b5
b4:
	...                     ; odd += 1
```

No register holds the answer, and each test sits right before the branch on it, which is what lets bytecode generation write the two as one instruction ([bytecode_generation.md](bytecode-generation.md#one-instruction-for-several)).

The blocks made on the way are placed where they run: those of a condition before the block it guards, which was made first because they lead to it (`Builder.PlaceBefore`), and what follows a loop after the loop's own blocks (`Builder.PlaceLast`). Blocks are written out in the order they are in, and a block that follows the one leading to it needs no jump to be reached.

## Structs and Methods

A struct literal becomes an object with its fields set **by name**:

```rust
Point { x: 1, y: 2 }
```
```
%v3 = NEW_STRUCT, "Point"
SET_FIELD %v3, "x", %v1
SET_FIELD %v3, "y", %v2
```

Field values are evaluated in alphabetical order of the field names, which makes the output deterministic (the AST stores fields in a map).

### Method calls

How `receiver.name(args)` is compiled depends on the **static type of the receiver**, taken from the type information computed after desugaring:

| Receiver type | Meaning | Generated |
|---------------|---------|-----------|
| a struct that has the method | the target is known | direct call of `Struct.name` |
| a struct without such a method | `name` is a field holding a function | read the field, call it |
| a trait | any implementing struct may be behind it | ask the value for the method |

```rust
p.sum()                 // p: Point
```
```
%v4 = MOV, "Point.sum"          ; the function is named directly
%v5 = CALL, %v4, %v3            ; the receiver is passed as self
```

```rust
fn measure(s: Shape) -> Int { return s.area(); }
```
```
%v2 = GET_METHOD, %v1, "area"   ; which area() depends on the value
%v3 = CALL, %v2, %v1
```

Because the decision uses the receiver's type, a field and a method may have the same name, in the same struct or in different ones.

Type arguments of a method call (`stack.map<String>(f)`) only matter to the type checker. They leave no trace in the IR: like the struct's own parameters, they are erased, and one compiled `Stack.map` serves every call.

### Methods found on values

A value of a struct or of an enum carries the name of its type, and the methods of a type are functions named after it: `Square.area`, or `Shape.area` for every variant of the enum `Shape`. `GET_METHOD value, "area"` is therefore all a call through a trait needs: when the program runs, the value says what it is a value of and the machine finds the function of that name ([luvm.md](../vm.md#fields)). Nothing is kept on the value for it: making one sets its fields and no more, and a struct may have a field and a method of the same name.

The program's table of methods (`ir.Program.Methods`: type, method, function) tells bytecode generation which functions such a call may reach. A function no instruction names is otherwise left out of the program; the methods of a name a `GET_METHOD` asks for are all kept, whichever types the program makes values of.

The same lookup is the fallback when a receiver's type is not available to IR generation: if exactly one struct defines the method it is called directly; if several do, the value is asked.

"Not available" includes a receiver whose type names no struct of the program. That happens for the result of a generic method called on a value whose type arguments were lost when types were computed again after desugaring: in `outer.get().get()` with `outer` a `Box<Box<Int>>`, the inner receiver's type comes out as the bare parameter name `V`. It is treated as unknown, not as a struct without that method.

Earlier, the methods a trait could reach were stored on every value, as function-valued fields set when the value was made. That made every such value larger and slower to make, and a method whose name another type happened to have was stored too: a program with a method called `map` anywhere made every `Result` carry two functions.

### Generic structs

A generic struct is not duplicated per instantiation: `Box<Int>` and `Box<String>` are both values of `Box`, created with `NEW_STRUCT "Box"`, and the methods of `impl<T> Box<T>` are compiled once as `Box.get`, `Box.set`, ... Values do not carry their type arguments at runtime, so one copy serves all of them; the type checker is what keeps a `Box<Int>` and a `Box<String>` apart.

## Casts

`value as Type` is either free or a runtime check, and the type checker decides which (`types.Info.Casts`):

*   a cast to a type the value always has (`n as Any`, `n as Int?`) emits nothing: the result is the value's register;
*   a cast that can fail calls the host function `__cast` with the value and a **descriptor** of the type:

```rust
v as Int
```
```
%v2 = MOV, "__cast"
%v3 = MOV, "int"
%v4 = CALL, %v2, %v1, %v3       ; returns v, or stops with "cannot cast String to Int"
```

| Target type | Descriptor |
|-------------|------------|
| `Int` `Float` `Bool` `String` | `int` `float` `bool` `string` |
| `Any`, or a type parameter | `any` |
| a function type | `fn(D,D)->D`, with `...D` for a variadic parameter and `void` for no result |
| `[T]` | `[D]`, checked for every element |
| `T?` | `?D` |
| a struct | `{Name}` |
| a trait | `{A|B}`, the structs implementing it |
| a generic struct | `{Name:field=D,...}` |

A value does not record the type arguments of a generic struct, so a cast to `Box<Int>` is verified through the fields whose declared type mentions a type parameter: `{Box:value=int}`. Fields that do not depend on them are not listed. A struct that contains itself (`List<T>` with a `tail: List<T>?`) is described one level deep, `{List:head=int,tail=?{List}}`; descriptors are therefore always finite, which also bounds the check on values that contain themselves.

**Function types.** A function value cannot be inspected for its parameter types, so every compiled function records its own signature, in the same descriptor form, in its prototype (`ir.Function.Signature`, from `types.Info.Signatures`). A cast to `fn(Int) -> Int` compares the two with subtyping: the function must accept what the type passes and return what it promises, so `toString` (`fn(any)->string`) casts to `fn(Int) -> String`. Type parameters are recorded as `any`. Methods have no signature; they are not values.

### Type tests

`value is Type` uses the same descriptors through the host function `__is`, which answers `true` or `false` instead of failing. A `match` on types is desugared to a chain of these tests. The narrowing they give (`value` is an `Int` inside the branch) is purely the type checker's doing; the generated code is the test and a branch.

For the struct descriptors to be checkable, `NEW_STRUCT` records the struct's name on the value. Using a host function instead of a dedicated instruction keeps casts out of the instruction set; the name starts with `__` so it cannot be called from source.

## Calls and Variadic Functions

A call compiles the callee and the arguments to registers and emits `CALL fn, args...`.

When the callee is the **name of a user function whose last parameter is variadic**, the arguments beyond the fixed ones are packed into an array:

```rust
fn sum(first: Int, rest: ...Int) -> Int { ... }
sum(1, 2, 3)
```
```
%v5 = NEW_ARRAY, 2
PUSH %v5, %v3
PUSH %v5, %v4
%v6 = CALL, %v1, %v2, %v5        ; sum(1, [2, 3])
```

A call with no extra arguments passes an empty array. Packing depends on knowing the callee's declaration, so it happens only for direct calls by name; a variadic function called through a variable receives its arguments as written. The builtins (`print`, `println`) are variadic in the VM itself and are never packed.

## Closures

Closure analysis has determined, for each function, the ordered list of variables it captures. That order is the function's upvalue numbering.

Inside the function, a captured variable is read with `LOAD_UPVALUE i` and written with `STORE_UPVALUE i`.

Where the function expression appears, the enclosing function builds the closure:

```
%v3 = MAKE_CLOSURE, "lambda_0", %v1, Upvalue(0)
```

Each capture operand says where the enclosing function finds the variable:

*   a **register**: it is one of the enclosing function's own locals;
*   an **`Upvalue` constant**: the enclosing function captured it itself and passes it on by index.

A lambda that captures nothing is just a `FuncRef`. Captures are by reference; the runtime mechanics are in [closure-spec.md](../closures.md).

## Source Positions

Before compiling each statement and expression the generator sets the builder's current position, and every instruction emitted is stamped with it. Positions of operations (the `+` of an addition, the `[` of an index) are restored after their operands are compiled, so a runtime error points at the operation that failed rather than at its last operand.

## Error Handling

Errors use the `E5xxx` range; see [diagnosis.md](diagnosis.md).

| Code | Raised when |
|------|-------------|
| `E5001` | A construct cannot be lowered (an undesugared `match`, an unknown operator or node) |
| `E5002` | A name is used as a value but is not a variable or function |
| `E5003` | The target of an assignment cannot be written |
| `E5005` | `break` or `continue` outside of a loop |

## Testing Strategy

`internal/irgen/source_test.go` compiles snippets and checks the printed IR for the instructions that carry each rule above: the copy on `let b = a`, the branch around a short-circuit operand, fields set by name, the receiver passed as `self`, the packed variadic array, the capture operands of nested closures. The older `irgen_test.go` and `lambda_test.go` build ASTs by hand and check structure (function count, block count).

Behavior is verified end to end by the feature tests in `internal/compiler`, which run the programs.
