# Bytecode Generation Specification

This document describes the last stage of the [compilation pipeline](pipeline.md): lowering [Qoqi IR](ir-generation.md) to a luvm chunk that the VM can execute or that can be written to a `.lc` file.

The implementation is in `internal/codegen`. The instruction set it targets is specified in [luvm.md](../vm.md).

## Objective

*   Map the IR's unlimited virtual registers onto the VM's registers.
*   Lay out basic blocks and turn block references into jumps.
*   Build the chunk's tables: constants, strings, functions, host functions.
*   Record where each instruction came from, for runtime errors and for the console to show where an instance is.
*   Fail with a clear error, never with a corrupt chunk, when a program exceeds what the bytecode format can express.

## Usage

```go
gen := codegen.NewGenerator(irProgram, []string{"print", "println", "toString", "push"}, errs)
gen.SetSourceLines(lines)
chunk := gen.Generate()

if errs.HasErrors() {
    // chunk must not be executed
}
```

The second argument lists the host functions the program may reference. A function reference that is neither defined in the IR nor in this list is an error (`E6005`), so a typo cannot turn into a failure at runtime.

## Constraints of the Target

Three properties of the VM shape most of this stage.

1.  **Operands are 16 bits.** A function can address 65536 registers, a chunk 65536 constants and 65536 functions.
2.  **`CALL` uses consecutive registers.** The callee must be in some register `rN` and its arguments in `rN+1`, `rN+2`, ...
3.  **Every call returns its result in `r0` of the caller**, and the callee receives its arguments in `r0`, `r1`, ... of a fresh frame.

Point 3 means `r0` is overwritten by every call, including calls to host functions. Nothing that must survive a call may live there, and that includes the function's own first argument.

## Register Allocation

### Frame layout

```
r0              result of the last call (and the first argument, on entry)
r1 .. rN-1      the remaining arguments, where the caller put them
permanents      values that live across basic blocks
temporaries     values used within one block; slots are reused
call window     callee and arguments of the call being made
```

*   **The first argument is moved out of `r0`** by a `MOV` at function entry, into a permanent register. If the function never uses it, the move is omitted.
*   **The other arguments stay** in `r1..rN-1`; those are their registers for the whole function.
*   **A function without parameters** still leaves `r0` unused.

### Permanent or temporary?

A virtual register is a **temporary** when all of these hold:

*   it is written exactly once;
*   every read is in the same basic block as the write, and after it;
*   it is not captured by a closure;
*   it is not a parameter.

Everything else is **permanent** and keeps one physical register for the whole function. That covers variables that are reassigned, anything read in another block (loop counters, values used after an `if`), and captured variables.

This classification needs no dataflow analysis: a block has no jumps into its middle, so a value that is written and read within one block is dead at the block's end by construction.

### Reusing temporaries

Blocks are walked in order with a free list of temporary slots. At each instruction:

1.  operands that are temporaries and are read here for the last time release their slot;
2.  if the result is a temporary, it takes a slot from the free list, or a new one if the list is empty.

Because operands are released before the result is placed, an instruction can write its result into a register it just read (`ADD r3 r3 r4`). The VM reads all operands before it writes.

Expression-heavy code needs very few registers this way: a chain of 400 additions compiles into a handful (covered by `TestGenerateReusesTemporaryRegisters`).

**Captured variables are never reused.** `CAPLOCAL` makes the closure point at the register itself (see [closure-spec.md](../closures.md)), so that register must keep holding that variable. Marking every operand of `MAKE_CLOSURE` as permanent guarantees it.

### The call window

One block of registers above all others is reserved for calls. For `CALL fn, a, b`:

```
MOV  rW   fn        ; callee
MOV  rW+1 a         ; arguments
MOV  rW+2 b
CALL rW   2
MOV  dest r0        ; fetch the result
```

Calls never nest at this level — arguments are already computed into registers before the sequence starts — so a single window is enough. Its size is one more than the largest argument count in the function.

That is the general form. Most calls are written shorter, by the rules of [One instruction for several](#one-instruction-for-several) below: `fib(n - 1)` is

```
SUBI  rW+1 n 1      ; the argument, made in its place
CALLK rW   1 fib    ; the function by its number: no value of it is made
```

The function's `RegisterCount` is `permanents + temporaries + window`. If it exceeds 65536 the function is rejected with `E6001`.

## Control Flow

### Reachability

Only blocks reachable from the entry block are emitted. IR generation leaves empty or dead blocks after `return`, `break` and `continue`; dropping them here keeps that stage simple.

Within a block, instructions after the first terminator are ignored for the same reason.

The same is done one level up, for functions: only those `main` can come to run are compiled. Starting from `main`, every function an instruction names is followed, to call it or to make a value of it (a method attached to a struct, a closure, what a task runs), then the functions those name, and so on. Nothing finds a function by its name while a program runs, so following the names is exact, and what is left out can never be missed.

This is what keeps a program small that imports a module of the library for one function: it carries that function and what it needs. The seven modules a small report imports compile to some 500 KB whole; the report itself to under 10 KB. Prototype numbers are given to the functions kept, in their original order.

Without a `main` there is nowhere to start from: everything is compiled, and the missing entry point reported.

### Jumps

Blocks are emitted in their IR order. A jump is one instruction holding the address of its target:

```
JMP target | JMPIFNOT r_cond target
```

An operand holds 16 bits, so that only reaches the first 65536 instructions of a chunk. A function that ends beyond them is generated a second time with every jump in the three-word long form, which holds a 32-bit offset instead:

```
LONG
JMP32 | JMPIFNOT32 r_cond
<offset word>
```

Targets are filled in once all blocks of the function are placed. Now that only the functions a program uses are compiled, it takes a very large program to need the long form.

| IR | Bytecode |
|----|----------|
| `JMP target` | long jump, **omitted** when `target` is the next block |
| `BR cond, then, else` | `JMPIFNOT cond → else`, then a jump to `then` unless it is the next block. When `cond` is a test made just before and read nowhere else, the test and the jump are one instruction (below) |
| `RET value` | `RET r` |
| `RET` | `LOADNULL r0`, `RET r0` |

A reachable block that does not end in a terminator gets `LOADNULL r0; RET r0`, so execution can never run into whatever follows it.

## One Instruction for Several

Before a function's instructions are written, `shorten` (in `function.go`) decides where one can do the work of several. Each case concerns a *temporary read once*: a value written once and read once in the block it was made in, which nothing else can depend on.

| What the IR says | Written as | Instead of |
|---|---|---|
| A call of a function named on the spot, which captures nothing | `CALLK window nargs function` | `NEWFUNC`, `MOV`, `CALL` |
| The same of a host function | `CALLX window nargs host-function` | `NEWEXFUNC`, `MOV`, `CALL` |
| A method found on a value (`GET_METHOD`) and called on that same value | `CALLM window nargs name` | `GETMETHOD`, `MOV`, `CALL` |
| A value made only to be an argument, with no other call before the one it is for | made in the window | made elsewhere, then `MOV` |
| The result of a call, read once before the next call | read from `r0` | `MOV dest r0` first |
| The result of a call nothing reads | nothing | `MOV dest r0` |
| A test read only by the branch after it | `JNLT a b else`, `JNLE`, `JNEQ`, `JNNE` (go to `else` unless the test holds; greater-than swaps the registers) | the test, then `JMPIFNOT` |
| The same against a number in 0..65535 | `JNLTI a k else`, `JNLEI`, `JNGTI`, `JNGEI`, `JNEQI`, `JNNEI` | `LOADI`, the test, `JMPIFNOT` |
| A call of `__is` with a descriptor naming one struct or variant (`{Shape.Circle}`), which is what a case of a `match` on an enum is | `JNTYPE value name else` when a branch reads it, `ISTYPE dest value name` otherwise | `NEWSTR` of the descriptor, `MOV`, a call of the host function, `JMPIFNOT` |
| Three or more such tests of one value in a row, each going on to the next when it fails: a `match` on an enum | `SWITCH value count else`, then one `CASE name target` word per type: one jump to the case of the value's type | a `JNTYPE` per case, tried in order |
| Adding or subtracting a number in 0..65535 | `ADDI dest a k`, `SUBI dest a k` | `LOADI`, then `ADD` or `SUB` |
| Any other number, Int or Float, that is only an operand of `+ - * / %` | `ADDK dest a k`, `SUBK`, `MULK`, `DIVK`, `MODK`, with the number among the constants | `LOADK`, then the operation |
| The same number on the left of `+` or `*`, where the order makes no difference (the types make the other operand a number, so `+` is not joining texts) | `ADDK`, `MULK` with the operands the other way round | `LOADK`, then the operation |
| A test against such a number, read only by the branch after it | `JNLTK a k else`, `JNLEK`, `JNGTK`, `JNGEK`, `JNEQK`, `JNNEK` | `LOADK`, the test, `JMPIFNOT` |
| A call of `std.math.toFloat` (`n.toFloat()`), the host function | `TONUM dest n` | the arguments moved into the window, `CALLX` |
| A result that the next instruction only moves into a variable (`total = total + n`) | written to the variable | the operation into a temporary, then `MOV` |
| The first argument of a function that calls nothing | stays in `r0` | `MOV home r0` on entry |

A function held in a variable, passed along or read twice is a value and is called with `CALL`. So is a closure, which is a new value each time it is made.

The tests that jump hold an address, so they are not used in a function written with long jumps; everything else is.

Each short instruction does exactly what the sequence it replaces did, for every kind of value, failures included: `internal/vm/short_ops_test.go` runs both and compares. A loop that counts to a limit and adds as it goes is three instructions per turn:

```
JNLTI i 100 done
ADD   sum sum i
ADDI  i i 1
JMP   head
```

## Instruction Selection

| IR | Bytecode |
|----|----------|
| `MOV` from a register | `MOV` (nothing if source and destination coincide) |
| `MOV` of `Null` / `Bool` | `LOADNULL` / `LOADBOOL` |
| `MOV` of an `Int` in 0..65535 | `LOADI` (no constant slot used) |
| `MOV` of any other `Int`, or a `Float` | `LOADK` from the constant pool |
| `MOV` of a `String` | `NEWSTR` (allocates a string object) |
| `MOV` of a `FuncRef` | `NEWFUNC` for a compiled function, `NEWEXFUNC` for a host function |
| `ADD` `SUB` `MUL` `DIV` `MOD` `NEG` `NOT` | the instruction of the same name |
| `EQ` `NEQ` `LT` `LTE` `GT` `GTE` | `EQ` `NE` `LT` `LE` `GT` `GE` |
| `NEW_STRUCT` | `NEWOBJ` with the struct's name as a string constant, which casts check. The third operand is 1 in its two lowest bits and, from the fourth up, the number of `SET_FIELD` on the value that follow in the block: the room it is made with |
| `NEW_VARIANT` | `NEWOBJ` with the name `Enum.Variant` and 2 in the lowest bits of the third operand, which marks a value of an enum; the number of fields as for a struct. With no field set anywhere in the function, the third bit is set too: the value holds nothing, and is made once |
| `GET_FIELD` / `SET_FIELD` | `GETFIELD` / `SETFIELD` with the field name as a string constant |
| `GET_METHOD` | `GETMETHOD` with the method name as a string constant |
| `NEW_ARRAY` | `NEWARRAY` (capacity capped at 65535; it is only a hint) |
| `GET_INDEX` / `LEN` / `PUSH` | `GETINDEX` / `ARRLEN` / `ARRPUSH` |
| `SET_INDEX` | `SETELEM`, which refuses an index at or past the length (`SETINDEX` would grow the array up to its capacity) |
| `MAKE_CLOSURE` | `NEWFUNC`, then one `CAPLOCAL` (register capture) or `CAPUPVAL` (upvalue capture) per captured variable |
| `LOAD_UPVALUE` / `STORE_UPVALUE` | `LDCAP` / `STCAP` |
| `CLOSE_UPVALUES` | one `CLOSEUP` per register |
| `TRY handler, body` | `LONG`, `TRY32`, the offset of `handler`; then a jump to `body` unless it is the next block. Ends its block, with both as successors: the handler is reachable though nothing jumps to it |
| `END_TRY` / `RETHROW` | `ENDTRY` / `RETHROW` (`RETHROW` ends its block) |
| `AND` `OR` `CALL_TRAIT` | none: `E6004` |

## Chunk Tables

*   **Function table.** Prototype indices are assigned to all IR functions before any code is generated, so a function can refer to one defined later (mutual recursion, a lambda defined after its use). Each prototype records its name, code range, register count, argument count, upvalue count and its signature (the function's type as a cast descriptor, used when a function value is cast or tested).
*   **Constant pool.** Deduplicated by value: a constant used a hundred times takes one slot.
*   **String pool.** Deduplicated by text. A string constant in the constant pool holds the *index* of its text in the string pool; this is the representation `NEWSTR` and the field instructions expect, and it survives serialization (a heap pointer would not). A string literal and a field name with the same text share one entry.
*   **Host functions.** Names are added to `ExternNames` on first use.

### Limits

| Limit | Error | What counts |
|-------|-------|-------------|
| 65536 registers per function | `E6001` | permanents + temporaries + call window |
| 65536 constants per program | `E6002` | distinct floats, ints outside 0..65535, strings and field names |
| 65536 functions per program | `E6003` | functions, methods, lambdas and generic instances |
| call window | `E6007` | a call whose arguments would pass register 65535 |

The limits are those of a 16-bit operand (`vm.MaxOperand`); the generator takes them from there.

There is **no limit on the length of a string**: the constant stores an index, and `.lc` files encode string lengths in 32 bits. What is limited is how many *different* constants a program has.

These are properties of the bytecode format, not of this stage. Lifting them means widening operands in the VM (or adding extended instruction forms), after which only the `max...` constants here change.

## Debug Information

Each emitted instruction gets a `vm.DebugInfo` built from the IR instruction's position: file, line, column, the text of the source line and the function name. It is stored in the chunk and serialized with it.

Two consumers use it:

*   the VM, to say where a runtime error happened:
    ```text
    [ERROR] R0002: index out of bounds: 7 (length 3)
      --> main.qi:3:17 in function 'pick'
         |
     3 |     return items[index];
         |                 ^
      called from main.qi:8:17 in function 'main'
    ```
*   the console of `qoqi serve`, to show the line an instance waits at.

Instructions the generator adds on its own (the entry `MOV`, the call sequence) inherit the position of the IR instruction they belong to.

## Entry Point

The VM starts at the function named `main`. Generation reports `E6006` if there is none or if it takes parameters, since the VM calls it without arguments.

## Error Handling

Errors use the `E6xxx` range and go through the shared handler like every other stage; see [diagnosis.md](diagnosis.md). IR has no AST nodes, so positions come from the IR instruction being lowered.

`E6004` and `E6005` indicate a mismatch between this stage and the one before it (an IR operation or function reference it cannot handle) rather than a mistake in the user's program. They are still reported as diagnostics, with the source position, instead of panicking.

## Testing Strategy

`internal/codegen/codegen_test.go` builds small IR functions with `ir.Builder`, generates bytecode and **runs it**, asserting on the returned value. Asserting on behavior instead of on the instruction listing means the tests keep passing when the allocator or the instruction selection is improved.

They cover: each kind of constant; sharing of constants; branches and loops; arguments surviving calls that clobber `r0`; host function calls; closures sharing a variable by reference; arrays and structs; register reuse; dropped unreachable blocks; debug information; and one case per error code.
