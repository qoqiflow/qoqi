# luvm - Qoqi Virtual Machine Specification

## Goals

luvm is designed to be:

- **Fast**: Register-based execution with minimal overhead
- **Compact**: NaN-boxed values reduce memory footprint
- **Simple**: Clear execution model, easy to debug and optimize
- **Safe**: Automatic memory management via garbage collection

## Value Representation

luvm uses 64-bit NaN-boxing to represent all values uniformly.

### NaN-Boxing Layout

A 64-bit IEEE 754 double has this structure:

```
[sign:1][exponent:11][mantissa:52]
```

When all exponent bits are set (0x7FF), the value is NaN. We exploit this:

- If the value is a normal double (not NaN), it represents a **Number**
- If it's a NaN, we use the mantissa bits to encode type and payload:

```
[0][11111111111][tttt pppppppppppppppppppppppppppppppppppppppppppppp]
 ^       ^        ^                       ^
 sign  exp=0x7FF  tag (4 bits)          payload (48 bits)
```

### Tag Encoding

| Tag | Type     | Payload                              |
|-----|----------|--------------------------------------|
| 0x0 | (Number) | infinity and NaN: floats that have this exponent themselves |
| 0x1 | Bool     | 0 = false, 1 = true                  |
| 0x2 | Int      | 48-bit signed integer                |
| 0x3 | HeapPtr  | 48-bit pointer to heap object        |
| 0x4 | Null     | (unused)                             |

Tag 0 is deliberately not given to a boxed kind. `+Inf` is the bit pattern
`0x7FF0000000000000`, exponent all ones and everything else zero; if null had
tag 0 it would be the same pattern, and a float overflowing to infinity would
turn into null. NaN is stored as one canonical pattern with tag 0.

### Bit Masks

```go
expMask     = 0x7FF0000000000000  // exponent bits
mantMask    = 0x000FFFFFFFFFFFFF  // mantissa bits
tagMask     = 0x000F000000000000  // tag nibble (high 4 bits of mantissa)
payloadMask = 0x0000FFFFFFFFFFFF  // payload (low 48 bits)
```

The tag occupies bits 48-51 and the payload bits 0-47, so they never overlap.
A full 48-bit payload matters for heap pointers: the Go runtime may place its
heap anywhere in the 48-bit address space, and a narrower payload would
truncate the pointer.

## VM Execution Model

### Registers and Stack

luvm is a **register machine**: an instruction names the registers it reads
and writes, and a function declares how many it needs.

The registers of every call in progress are on one **stack**, a call after
the other. A call takes its registers from the top (the arguments first, the
rest set to null) and gives them back when it returns, so calling a function
allocates nothing. Each task has a stack of its own.

- **Registers**: the window on the stack that belongs to the function running
- **Base**: where that window starts
- **IP (Instruction Pointer)**: the instruction being executed
- **Frames**: for each call in progress, where its caller was: function,
  instruction, base and number of registers

The stack grows by moving to a larger array, so nothing keeps the address of
a register. A variable a closure captured is named by its place on the stack
while the call that owns it is in progress, and moves into the closure when
that call returns (see [closure-spec.md](closures.md)).

`RET` puts the result in the caller's `r0`.

### Execution Loop

1. Fetch instruction at IP
2. Decode opcode and operands (A, B, C)
3. Execute operation
4. Advance IP
5. Repeat

The loop reads the code, the registers and the instruction pointer from
variables of its own, and takes them from the VM again after what can change
them: a call, a return, a host function (which may call back into the program
or let another task have the turn).

### Checked before it runs

`Exec` verifies a chunk before running it (`vm.Verify`), and refuses one that
could make the VM read outside its tables:

- a function's code lies inside the chunk, it has between 1 and 65536
  registers and no more arguments than registers;
- every operand that names a register names one the function has, and a call
  does not reach past them with its arguments;
- constants, strings, functions, host functions and captured variables named
  by an instruction exist;
- a jump lands on an instruction of the same function, not in the middle of a
  long jump;
- a function ends with a return or a jump, so that it cannot run into the next.

Bytecode from the compiler passes by construction. Bytecode damaged on the way,
in a store or a file, is refused with a message that names the function and the
instruction:

```
invalid bytecode: function 'fib': instruction 0: register r60000 of a function that has 7
```

Reading serialized bytecode checks every length it gives against what is left of the
file before making anything to hold it, and refuses a constant that is a
pointer. What depends on the values a program computes is checked when it
runs: that a register holds a function when it is called, an array when it is
indexed.

## Chunk Structure

A **Chunk** represents a compiled function:

```go
type Chunk struct {
    Constants     []Value  // constant pool
    Code          []Instr  // bytecode instructions
    RegisterCount int      // number of registers needed
    ArgCount      int      // number of arguments
}
```

## Opcode Families

### Data Movement

- `MOV r_dest r_src` - Copy value between registers
- `LOADK r_dest const_idx` - Load constant to register
- `LOADNULL r_dest` - Load null
- `LOADBOOL r_dest value` - Load boolean

### Arithmetic

- `ADD r_dest r_a r_b` - Addition, or concatenation of two strings
- `SUB r_dest r_a r_b` - Subtraction
- `MUL r_dest r_a r_b` - Multiplication
- `DIV r_dest r_a r_b` - Division
- `MOD r_dest r_a r_b` - Remainder
- `NEG r_dest r_a` - Negation
- `SWITCH r_value count else` followed by `count` words `CASE name target` - Go on at the target of the case that names the type of the value, or at `else` for a value of none of them
- `ONCE r_dest function` - The value of a constant that is made (`const RED = Color { ... };`): what the function of the chunk with that number returns. The function takes nothing and captures nothing (verified), is run the first time the instruction is reached in a run, and its result is kept for the others and for the collector. The result is frozen with everything it holds: `SETFIELD`, `SETELEM`, `SETINDEX`, `ARRPUSH`, `DELFIELD` and the native functions that change an array or a field refuse a frozen value.
- `ISTYPE r_dest r_value name`, `JNTYPE r_value name target` - Whether a value is of a struct or a variant, by the name it carries; the second goes on at `target` unless it is
- `ADDI r_dest r_a k`, `SUBI r_dest r_a k` - Addition and subtraction of a number `k` (0 to 65535) written in the instruction
- `ADDK r_dest r_a k`, `SUBK`, `MULK`, `DIVK`, `MODK` - The same operations with the constant `k` of the chunk, an Int or a Float (verified): what `LOADK` and the operation do, without the register
- `JNLTK r_a k target`, `JNLEK`, `JNGTK`, `JNGEK`, `JNEQK`, `JNNEK` - Go on at `target` unless `r_a` is less than (and so on) the constant `k`, an Int or a Float
- `NOT r_dest r_a` - Boolean negation

Both operands must be of the same kind (two Ints or two Numbers); there is no
implicit conversion. Int division and remainder by zero raise a runtime error,
and so does an Int result outside the 48-bit range (`integer overflow`): Ints
never wrap around. Number arithmetic follows IEEE 754, including infinity and
NaN.

### Comparison

- `EQ r_dest r_a r_b` - Equal
- `NE r_dest r_a r_b` - Not equal
- `LT r_dest r_a r_b` - Less than
- `LE r_dest r_a r_b` - Less than or equal
- `GT r_dest r_a r_b` - Greater than
- `GE r_dest r_a r_b` - Greater than or equal

`EQ`/`NE` compare strings, arrays and objects by content: arrays item by item, objects of one type field by field. An object whose type has the method `__equal`, which the compiler gives every type that implements `Equal`, is compared by calling it. A value that holds itself is compared without going round for ever: a pair met again while it is being compared counts as equal. Functions are compared by identity.
The ordering instructions accept two Ints, two Numbers or two Strings.

### Heap Objects

- `NEWSTR r_dest const_idx` - Create a string from a string constant
- `NEWARRAY r_dest capacity` - Create an empty array
- `NEWOBJ r_dest [const_idx 1]` - Create an empty object (used for struct values). The compiler passes the struct's name, which is recorded on the object
- `GETINDEX r_dest r_arr r_idx` / `SETINDEX r_arr r_idx r_val` - Element access
- `SETELEM r_arr r_idx r_val` - Replace an existing element; never grows the array. What the compiler emits for `xs[i] = v`
- `ARRPUSH r_arr r_val` - Append to an array
- `ARRLEN r_dest r_val` - Length of an array or string
- `GETFIELD r_dest r_obj const_idx` / `SETFIELD r_obj const_idx r_val` - Field access
- `HASFIELD` / `DELFIELD` - Field presence and removal

These instructions check the type of their operand and report a runtime error
instead of reinterpreting memory: indexing a number, reading a field of an
array, or going past the end of an array all stop the program with a message.
`qoqi dump` prints a program's bytecode in the notation used here.

### Control Flow

- `JMP offset` - Unconditional jump
- `JMPIF r_cond offset` - Jump if true
- `JMPIFNOT r_cond offset` - Jump if false
- `LONG` + `JMP32` / `JMPIF32` / `JMPIFNOT32` + offset word - The same jumps
  with a 32-bit relative offset, for a function that lies beyond the first
  65536 instructions of its chunk. The compiler uses the short form wherever
  the address fits.
- `JNLT r_a r_b target`, `JNLE`, `JNEQ`, `JNNE` - A test and a jump in one: go
  on at `target` unless `r_a < r_b` (`<=`, `==`, `!=`). No Bool is made.
- `JNLTI r_a k target`, `JNLEI`, `JNGTI`, `JNGEI`, `JNEQI`, `JNNEI` - The same
  against a number `k` written in the instruction.

### Function Calls

### Function Calls & Closures

- `NEWFUNC r_dest func_name` - Create a function object (or closure) from a prototype.
- `NEWEXFUNC r_dest extern_name` - Create an external function object.
- `CALL r_func nargs` - Call a function object in `r_func` with `nargs` arguments.
- `CALLK r_window nargs function` - Call a function of the chunk by its number, with the arguments in the registers after `r_window`. The function must capture nothing; that, and the number of arguments, are checked when the chunk is verified, not at each call.
- `CALLX r_window nargs host_function` - The same for a host function.
- `CALLM r_window nargs name` - Call the method `name` of the first argument, found by the type that value carries; `GETMETHOD r_dest r_value name` gives the method as a value.
- `RET r_val` - Return from function with value.
- `CAPLOCAL r_func upval_idx local_reg` - Capture a local variable into a closure's upvalue slot.
- `CAPUPVAL r_func upval_idx parent_upval_idx` - Capture an existing upvalue into a closure's upvalue slot.
- `CLOSEUP r` - Detach the closures that captured register `r`: they keep its current value and stop following it.
- `LDCAP r_dest upval_idx` - Load value from an upvalue.
- `STCAP upval_idx r_src` - Store value to an upvalue.

## Host Functions

A chunk lists the host functions it uses by name (`ExternNames`). `NEWEXFUNC`
turns a name into a callable object, and `CALL` invokes it like any other
function. The VM registers these by default:

| Name       | Arguments        | Result  | Description                                   |
|------------|------------------|---------|-----------------------------------------------|
| `print`    | any number       | null    | Writes the values separated by spaces         |
| `println`  | any number       | null    | Same, followed by a newline                   |
| `toString` | 1                | String  | The text `print` would write for the value    |
| `push`     | array, value     | null    | Appends the value to the array                |
| `__is`     | value, descriptor | Bool | Implements `value is Type`: whether the value matches the descriptor |
| `__cast`   | value, descriptor | the value | Implements `value as Type`: fails unless the value matches the descriptor (`int`, `[int]`, `?string`, `{Circle|Square}`, `{Box:value=int}`, ...; the grammar is at the top of `internal/vm/cast.go`). Arrays are checked element by element. Emitted by the compiler; not callable from Qoqi source |

### Values of an enum

A value of an enum is an object, like a value of a struct, created by `NEWOBJ`
with a third operand of 2 (`vm.ObjectIsVariant`; 1 is a struct). The object
carries the name `Enum.Variant` and holds the values of the variant as the
fields `"0"`, `"1"`, ...

The mark changes two things:

- **Equality.** `EQ` and `NE` compare two such objects by content: the same
  name, and each value equal in turn, recursively, as structs and arrays
  are.
- **Printing.** `FormatValue` writes them as they are made, without the
  enum's name: `Rect(2, 3)`, `Empty`.

Testing for a variant is `__is` with the descriptor `{Enum.Variant}`, the
same check of the object's name as for a struct. A test or cast to the enum
type lists all its variants: `{Shape.Circle|Shape.Rect|Shape.Empty}`.

### Runtime errors and protected calls

A failure is a `*vm.RuntimeError`: a kind (`ErrorKind`), a message and the
call stack, taken from the frames when it is raised. `Exec` returns it as
its error; `Error()` renders it as a diagnostic (see
[errors.md](../reference/language/error-handling.md#what-a-runtime-error-looks-like)).

The instruction loop is `VM.run(base)`: it runs until the function that was
current when it started returns. `Exec` calls it once for `main`. A native
function can call it again through `VM.callFunction(fn, args)`, which sets
up a frame as `CALL` does and waits for the result; that is how a native
runs a function of the program.

`VM.protect(fn)` is `callFunction` with a way back: it saves the frame
count, registers, function and instruction, and if the call fails, `unwind`
drops the frames above, closes the upvalues that point into their
registers, and restores what was saved. The failure is kept for the caller
instead of ending the program. It is what `std.result.attempt` is built on.

### Native functions of the standard library

The part of the [standard library](../reference/language/stdlib.md) that is not written in Qoqi is
a set of host functions named after the library function they implement:
`std.math.sqrt`, `std.strings.split`, `std.io.readFile`, and so on
(`internal/vm/native_*.go`; `vm.NativeNames()` lists them). Every VM has them.
A Qoqi module reaches one by declaring it with `extern fn`; the compiler
emits `NEWEXFUNC` with the function's full name.

They differ from the functions above in three ways:

- **Arguments are checked**: count and types, although the type checker has
  already done so for calls from Qoqi. Bytecode can be written by hand.
- **Errors name the function as the program does**:
  `std.strings.substring: range 2..9 is outside a text of 3 characters`.
- **They may allocate more than once**, which the others never do. A second
  allocation can collect garbage, and what the function has built so far is
  held only in Go variables, where the collector cannot see it. Such a
  function *pins* its intermediate values (`vm.pin`): pinned values are roots
  until the function returns. `newStringArray` does this for the common case
  of an array of strings.

Per-VM state: the input `std.io.readLine` reads from (`VM.SetInput`, default
standard input) and the generator behind `std.random`, so that seeding in one
VM does not affect another.

Values are written the way a program shows them: `42`, `2.5`, `true`, `null`,
the text of a string, `[1, "a"]` for arrays and `{x: 1, y: 2}` for objects
(fields sorted by name, function-valued fields omitted). This is
`vm.FormatValue`; `Value.String()` remains the debug form (`Int(42)`).

Output goes to standard output unless redirected with `VM.SetOutput`, which is
how tests capture it. More functions can be added with `VM.RegisterExtern`, or
with `VM.RegisterTypedExtern` to give the function a type
(`"fn(any)->string"`); only a typed function can be cast to a function type.

### Function signatures

Each function prototype carries a `Signature`: the function's type as a cast
descriptor, e.g. `fn(int,string)->bool`. The compiler fills it in. It is the
only type information kept at
runtime, and exists so that `value as fn(Int) -> Int` and
`value is fn(Int) -> Int` can be answered.

### Bytecode file version

Serialized bytecode starts with a format version, currently **6** (1 predates the
48-bit value payload, 2 the tag of null, 3 had one-byte operands, 4 wrote
debug information once per instruction;
5 did not list the `priv` fields of each struct, which `std.reflect` leaves
out).
The same chunk is always written as the same bytes. A file of
another version
is refused with `unsupported bytecode version` rather than misread; rebuild it
from source.

## Operand Limits

Every operand is 16 bits (`vm.MaxOperand` = 65535), which bounds what a single
function or chunk can address:

| What                              | Limit | Reported by the compiler as |
|-----------------------------------|-------|-----------------------------|
| Registers per function            | 65536 | `E6001`                     |
| Distinct constants per chunk      | 65536 | `E6002`                     |
| Functions per chunk               | 65536 | `E6003`                     |
| Arguments per call                | bounded by the register window | `E6007` |

Strings have no length limit of their own: a string constant stores an index
into the string pool, and bytecode files encode string lengths in 32 bits.
What counts against the constant limit is the number of *distinct* strings,
field names, floats and integers outside 0-65535.

Operands were one byte until format version 4, and each of these limits was
256: a program with a few dozen structs ran out of constants.

## Instruction Encoding

Every instruction has an opcode and three operands:

```
[opcode:8][A:16][B:16][C:16]
```

In memory this is the `Instr` struct. In a `.lc` file an instruction takes 7
bytes, operands in little-endian order.

The offset word of a long jump (`LONG`, `JMP32`, offset) is not an
instruction: it holds a signed 32-bit offset, the high half in A and the low
half in B (`EncodeOffset32` / `DecodeOffset32`).

- **opcode**: Operation code
- **A**: First operand (usually destination register)
- **B**: Second operand
- **C**: Third operand

## Memory Management

### Garbage Collection

luvm uses **mark-sweep** garbage collection:

1. **Mark Phase**: Starting from roots (the registers of every call in progress, of every task), mark all reachable objects. Objects found are put on a list to be looked into, rather than followed by a call each: a list of a million nodes is marked without a million calls inside each other
2. **Sweep Phase**: Iterate through heap, free unmarked objects

**When it runs.** The first collection happens once 1 MB has been allocated.
After each collection the threshold is set to twice what survived (never below
that first megabyte), so collections spread out as the live data grows and
the total work stays proportional to what the program allocates. Everything
that takes memory is counted, including an array's growth as it is appended to
and the fields added to an object.

**The limit.** A program may hold 256 MB at once by default
(`NewVMWithHeapSize`, or `--heap-mb=N` on the command line). An allocation that
would exceed it first triggers a collection; if that does not make room, the
program stops with `out of memory`.

**Function objects are reused.** `NEWFUNC` for a function without captured
variables and `NEWEXFUNC` for a host function return the same object every
time, so calling a function by its name allocates nothing. A closure
(`UpvalueCount > 0`) gets a new object each time, since it carries its own
captured variables.

### Call depth

Calls may nest 10000 deep by default (`VM.SetMaxCallDepth`, `--max-depth=N`);
beyond that the program stops with `stack overflow`. The frame stack grows as
needed, so the limit is a guard against runaway recursion, not a reserved
size. The registers of all the calls in progress are bounded as well, at 256
per call the depth limit allows.

### Fields

A struct value holds its fields in slots, and shares with every other value
made the same way a **shape**: which field is in which slot. Adding a field
moves an object to the shape of those that have it; values of one struct all
end up with the same one.

The shape also holds what the values of a type have in common besides their
fields: the name of the struct, or `Enum.Variant` for a value of an enum. A
value is then its shape and its slots, 48 bytes before the fields, and
`NEWOBJ` says how many fields it is about to be given, so that the value and
the room for them are one allocation (up to 16 fields; more are a second).

A value of an enum that holds nothing, `Light.Red` or `Option.None`, is
made once per place that makes it and handed out each time: nothing can
tell two of them apart, and nothing can change one.

Asking what a value is, as every case of a `match` on an enum does, is
`JNTYPE value name target` (or `ISTYPE dest value name`): it compares the
name the value's shape carries. The instruction remembers the shapes it
answered yes and no for, so the names themselves are compared once each.
A `match` with three cases or more is one `SWITCH`: the cases are gone
through the first time a type is met, and where its values go on is kept
with the type, so that the twelfth variant costs what the first does.

A call through a trait finds its method the same way (`CALLM window nargs
name`, or `GETMETHOD dest value name`): the method `area` of a value of
`Square` is the function `Square.area` of the program, and of a value of
the variant `Shape.Circle` the function `Shape.area`. The functions found
are kept with the type, and each instruction remembers the type it met
last. No value holds its methods.

An instruction that reads or writes a field remembers the shape of the last
object it met and where the field was. While it keeps meeting values of the
same struct, which is nearly always, the field is found by comparing one
pointer, without looking the name up. A struct value takes the room of its
slots; the names are kept once per shape.

### Texts

Positions in a text are characters; its bytes are UTF-8. A text of plain
ASCII is indexed directly. For any other, where every 32nd character starts is
worked out the first time a position is asked for and kept, so `text[i]`,
the length and `substring` do not depend on how long the text is. The
texts of one ASCII character, which is what indexing mostly gives, are made
once each.

A text written in the program is one object, made the first time the
program reaches it: a text never changes, so a loop that compares each
character with `"."` does not make a `"."` at every turn. Two texts joined
(`a + b`) that come to 64 bytes or less are one allocation, the object and
its bytes together. A text is 48 bytes before its characters.

### When the VM itself goes wrong

A Go panic in the VM or in a native function does not take the process down:
`Exec` turns it into an error of kind `internal` (`R0010`), with the place the
program was at, and the VM can run the next program. Nothing in the program
catches it.

### Stopping a program from outside

`VM.Interrupt()` may be called from any thread. The program ends with an error
of kind `interrupted` (`R0011`) that says where it was: at its next call or
jump backwards if it is computing, at once if it sleeps. `attempt` does not
catch it and no `defer` runs. The `qoqi` command does this on Ctrl-C, and
exits with status 130.

### Programs that are not trusted

A program written by someone else, or made by a machine, gets more limits
than the two on memory and depth, set by its host before `Exec`:

- `SetStepLimit(n)`: the program ends with an error of kind `budget`
  (`R0012`) after `n` steps, a step being a call or a jump backwards. Steps
  are counted, not time, so the same program ends at the same place on any
  machine;
- `PermitNatives(allow)`: which native functions it may use, by name. Reaching
  for another is a failure of kind `library`;
- `ExecContext(ctx, chunk)`: `Exec`, stopped when the context is done.

`internal/sandbox` puts these together: a small heap, a bound on steps, depth,
output and time, and only the natives that compute (no files, network, other
programs, clock or exit). It is what the fuzzers run programs in.

### Threads

A VM runs one program at a time, and a program one [task](../reference/language/tasks.md) at a
time: the heap, the collector and the registers are only touched by whoever
has the turn, which is what lets them go without locks. From any thread, at
any time:

- `Exec`: a second program waits for the first to end;
- `Interrupt`;
- anything on another VM. VMs share nothing that changes, so several run side
  by side, one per thread, each on a processor of its own.

Everything else configures a VM (`SetOutput`, `RegisterExtern`, the limits)
and is done before `Exec`. `make test-race` runs the tests under Go's race
detector.

Tasks of one program take turns on one processor. Running them on several
would take a collector that stops every thread at a safe point, and
allocation and objects safe to use from two threads at once.

### What the process holds

The heap limit is on the values of the program. `qoqi` also watches what
the whole process holds, and ends it with `out of memory` when that passes
four times the limit plus 1 GB: a guard for what the count does not cover,
which would otherwise only stop when the machine has no memory left
(`internal/memguard`). The test packages that run programs use the same guard,
at 4 GB.

### Heap Object Header

Every heap-allocated object begins with:

```go
type ObjHeader struct {
    Type   ObjType  // object type (string, array, closure, etc.)
    Marked bool     // GC mark bit
    Next   *Obj     // linked list for GC
}
```

Object types:

- `StringObj` - Immutable string
- `ArrayObj` - Dynamic array
- `ObjectObj` - Slots and a shape (struct and enum values)
- `FunctionObj` - Function closure
- `ExternFunctionObj` - Host function
- `UpvalueObj` - Captured variable

## Example Execution

Given this bytecode:

```
LOADK r0 0      ; load constant 0 (value: 10) into r0
LOADK r1 1      ; load constant 1 (value: 20) into r1
ADD r2 r0 r1    ; r2 = r0 + r1
RET r2          ; return r2
```

Execution:

1. r0 ← 10
2. r1 ← 20
3. r2 ← 10 + 20 = 30
4. Return 30

## Design Decisions

- **Register-based**: Reduces instruction count vs stack-based VMs
- **NaN-boxing**: Efficient value representation without heap allocation
- **Simple ISA**: Easy to implement, optimize, and debug
- **Fixed-width instructions**: Fast decode, predictable performance
