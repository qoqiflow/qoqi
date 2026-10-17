# Qoqi Function Calling Convention

This document provides a comprehensive specification of the function calling convention implemented in Qoqi.

## Table of Contents

1. [Overview](#overview)
2. [Architecture](#architecture)
3. [User-Defined Functions](#user-defined-functions)
4. [Calling Convention](#calling-convention)
5. [Call Frame Management](#call-frame-management)
6. [External Functions](#external-functions)
7. [Recursion](#recursion)
8. [Implementation Details](#implementation-details)
9. [Performance Considerations](#performance-considerations)
10. [Examples](#examples)

## Overview

Qoqi implements a simple, register-based calling convention with the following features:

**Core Capabilities:**
- User-defined functions with labels
- Function calls with multiple arguments
- Deep recursion support (10000 levels by default)
- External (host) functions
- Register-based argument passing
- Automatic frame management

**Design Principles:**
- **Simplicity**: Minimal overhead, easy to understand
- **Safety**: Automatic register saving/restoration
- **Performance**: Register-based arguments, no stack manipulation
- **Flexibility**: Support for both static and dynamic calls

## Architecture

### Register Model

Qoqi uses a register-based virtual machine:
- Each function has its own register file
- Maximum 65536 registers per function (r0-r65535)
- Registers contain NaN-boxed values (numbers, ints, bools, null, heap pointers)

### Memory Organization

```
┌─────────────────────┐
│   Call Stack        │ ← frameCount, frames[]
│  (Call Frames)      │
├─────────────────────┤
│   Current Chunk     │ ← chunk pointer
├─────────────────────┤
│   Registers         │ ← regs[] (current function's registers)
├─────────────────────┤
│   Instruction       │ ← ip (instruction pointer)
│   Pointer           │
├─────────────────────┤
│   Heap              │ ← For heap-allocated objects
└─────────────────────┘
```

### Call Stack

The VM maintains a call stack of frames:

```go
type CallFrame struct {
    chunk  *Chunk   // The function's bytecode chunk
    ip     int      // Return address (instruction pointer)
    fp     int      // Frame pointer (unused in current implementation)
    regs   []Value  // Saved registers from caller
}
```

Maximum call depth: **10000 frames** by default (`VM.SetMaxCallDepth`, `--max-depth=N`)

## User-Defined Functions

### Function Declaration

Functions are declared using `.func` blocks:

```assembly
.func function_name registers args [upvalue_count]
  ; function body
  RET rX    ; return value from register X
.endfunc
```

**Function Block Parameters:**
- `function_name`: Function identifier
- `registers`: Number of registers needed by this function
- `args`: Number of function arguments
- `upvalue_count` (optional): Number of upvalues captured by this function (default 0)

**Label Rules:**
- Labels can be used within functions for jumps
- Label must end with colon (`:`)
- Label defines entry point at next instruction
- Labels can be referenced before definition (forward references)

**Example:**
```assembly
.func main 2 0
  ; Main code
  NEWFUNC r0 my_function  ; Create function object
  LOADK r1 0              ; Load argument
  CALL r0 1               ; Call function with 1 arg
  RET r0
.endfunc

.func my_function 2 1
  ; Function implementation
  ; Argument in r0
  ADD r1 r0 r0
  RET r1
.endfunc
```

### Function Body

A function body consists of:
1. **Prologue** (implicit): Register allocation happens automatically
2. **Body**: Function logic using available registers
3. **Epilogue** (explicit): `RET` instruction

**No explicit prologue/epilogue needed** - the VM handles frame management automatically.

## Calling Convention

### NEWFUNC Instruction

**Syntax:**
```assembly
NEWFUNC r_dest func_name
```

**Parameters:**
- `r_dest`: Destination register for the function object
- `func_name`: Name of the function to reference

**Behavior:**
- Creates a function object referencing the named function
- Stores the function object in the destination register
- Function object can be called using CALL instruction

**Example:**
```assembly
NEWFUNC r0 my_function  ; r0 = function object for my_function
```

### CALL Instruction

**Syntax:**
```assembly
CALL r_func argc
```

**Parameters:**
- `r_func`: Register containing function object (created by NEWFUNC)
- `argc`: Number of arguments

**Behavior:**
1. Save current call frame:
   - Save current chunk pointer
   - Save current instruction pointer (return address)
   - Save current frame pointer
   - Save all current registers
2. Create new register file for callee
3. Copy arguments from r(func_reg+1)..r(func_reg+argc) to callee's r0..r(argc-1)
4. Leave the remaining registers as they are: until the function writes one, it holds what an earlier call left there, or null. Code must write a register before reading it, as the compiler's always does; the machine only makes sure that whatever is there is safe to hold (see [memory-management.md](memory-management.md#gc-roots))
5. Jump to function address
6. Increment frame counter

**Example:**
```assembly
; Caller side
NEWFUNC r0 sum    ; r0 = function object
LOADK r1 0        ; r1 = 5 (first argument)
LOADK r2 1        ; r2 = 10 (second argument)
LOADK r3 2        ; r3 = 3 (third argument)
CALL r0 3         ; Call sum(5, 10, 3) - args are r1, r2, r3
; After return, r0 contains result
MOV r5 r0         ; Save result
```

### RET Instruction

**Syntax:**
```assembly
RET rX
```

**Parameters:**
- `rX`: Register containing return value

**Behavior:**
1. Save return value from register rX
2. Check frame depth:
   - If frameCount == 0: Return from top-level execution
   - Else: Restore caller's frame
3. Restore caller's state:
   - Restore chunk pointer
   - Restore instruction pointer
   - Restore frame pointer
   - Restore all registers
4. Place return value in caller's r0
5. Decrement frame counter
6. Continue execution at saved IP

**Example:**
```assembly
; Callee side
sum:
  ; Arguments in r0, r1, r2
  ADD r3 r0 r1      ; r3 = r0 + r1
  ADD r4 r3 r2      ; r4 = r3 + r2
  RET r4            ; Return r4
```

### Argument Passing

**Convention:**
- Function object placed in register (e.g., r0)
- Arguments passed in **consecutive registers after function register**: r(func_reg+1), r(func_reg+2), ..., r(func_reg+argc)
- Caller places arguments in these registers before CALL
- Callee receives arguments in **r0, r1, r2, ..., r(argc-1)** (arguments are remapped)
- Arguments are **copied** to callee's register file

**Example:**
```assembly
NEWFUNC r0 add     ; Function in r0
LOADK r1 5         ; First arg in r1 (r0+1)
LOADK r2 10        ; Second arg in r2 (r0+2)
CALL r0 2          ; Callee receives: r0=5, r1=10
```

**Constraints:**
- Arguments must fit in the caller's registers above the function register
- No argument type checking (caller's responsibility)
- Variadic functions: The compiler handles variadic arguments by ensuring the correct number of arguments are passed at the call site, or by constructing an array/slice if the language semantics require it (implementation detail). The VM sees them as normal arguments.

### Return Values

**Convention:**
- Return value placed in register specified in RET instruction
- After return, value appears in caller's **r0**
- Single return value only
- To return multiple values, use heap-allocated structures

**Example:**
```assembly
; Function returns multiple values via object
create_point:
  NEWOBJ r3           ; Create object
  SETFIELD r3 0 r0    ; Set field "x" = r0
  SETFIELD r3 1 r1    ; Set field "y" = r1
  RET r3              ; Return object in r0
```

## Call Frame Management

### Frame Structure

Each call frame saves:

```go
type CallFrame struct {
    chunk  *Chunk   // Current chunk being executed
    ip     int      // Return address (next instruction after CALL)
    fp     int      // Frame pointer (base of frame in stack)
    regs   []Value  // Complete snapshot of caller's registers
}
```

### Frame Stack

The VM maintains:
```go
frames     []CallFrame  // Frame stack, grows as calls nest
frameCount int          // Current frame depth
```

**Limits:**
- `maxCallDepth` = 10000 by default, adjustable per VM
- Stack overflow if exceeded
- Frames are allocated as needed; the limit guards against runaway recursion

### Frame Lifecycle

**On CALL:**
```
1. frames[frameCount] = CallFrame{
     chunk: vm.chunk,
     ip:    vm.ip,
     fp:    vm.fp,
     regs:  vm.regs,
   }
2. frameCount++
3. Create new register file
4. Copy arguments r0..r(argc-1)
5. Jump to function
```

**On RET:**
```
1. Save return value
2. frameCount--
3. frame = frames[frameCount]
4. vm.chunk = frame.chunk
5. vm.ip = frame.ip
6. vm.fp = frame.fp
7. vm.regs = frame.regs
8. vm.regs[0] = returnValue
```

### Register Isolation

Each function call gets:
- **Fresh register set**: Allocated from VM
- **Isolated state**: Cannot access caller's registers (except via arguments)
- **Automatic cleanup**: Registers deallocated on return

**Benefits:**
- No register conflicts between caller/callee
- No need for caller-saved/callee-saved conventions
- Simple reasoning about register lifetimes

## External Functions

### Overview

External functions are implemented in the host language (Go) and called from bytecode.

### Registration

**Go Side:**
```go
vm := vm.NewVM()
vm.RegisterExtern("function_name", func(vm *VM, args []Value) (Value, error) {
    // Implementation
    return result, nil
})
```

**Built-in Externals:**
- `print` - Print values without newline
- `println` - Print values with newline

### NEWEXFUNC Instruction

**Syntax:**
```assembly
NEWEXFUNC r_dest extern_name
```

**Parameters:**
- `r_dest`: Destination register for the external function object
- `extern_name`: Name of the external function (identifier)

**Behavior:**
1. Look up external function name in the extern function table
2. Create an `ExternFunctionObj` on the heap containing:
   - Function name
   - Go function pointer
   - Argument count (optional)
3. Store the function object pointer in the destination register
4. The function object can be called using the `CALL` instruction

**Example:**
```assembly
.func main 3 0
  NEWEXFUNC r0 print  ; r0 = print function object
  LOADK r1 0          ; r1 = 42
  LOADK r2 1          ; r2 = "Hello"
  CALL r0 2           ; print(42, "Hello") - args in r1, r2
  ; r0 now contains return value (typically Null)
  LOADNULL r0
  RET r0
.endfunc
```

**Note:** External functions are now first-class objects in memory:
- They are allocated on the heap like user-defined functions
- They participate in garbage collection
- They use the exact same calling convention as `CALL`

### Extern Function Signature

```go
type ExternFunc func(*VM, []Value) (Value, error)
```

**Parameters:**
- `*VM`: VM instance (for accessing heap, etc.)
- `[]Value`: Argument slice

**Return:**
- `Value`: Return value
- `error`: Error if function fails

**Example Implementation:**
```go
vm.RegisterExtern("add", func(vm *VM, args []Value) (Value, error) {
    if len(args) < 2 {
        return vm.NewNull(), errors.New("add requires 2 arguments")
    }
    a := args[0].AsInt()
    b := args[1].AsInt()
    return vm.NewInt(a + b), nil
})
```

### Serialization

External function names are serialized in bytecode:
```
[ExternCount:4]        Number of extern functions referenced
[ExternName1...]       First extern name (length-prefixed string)
[ExternName2...]       Second extern name
...
```

This allows bytecode to be portable across VMs with same extern functions registered.

## Recursion

### Support

Recursion is **fully supported** through automatic frame management.

**Properties:**
- Each recursive call creates new frame
- Each frame has isolated registers
- Automatic unwinding on return
- Maximum depth: 10000 by default (`--max-depth=N`)

### Example: Factorial

```assembly
.constants 3
  0: 5i
  1: 1i
  2: 0i

.func main 2 0
  NEWFUNC r0 factorial  ; Create function object
  LOADK r1 0            ; r1 = 5
  CALL r0 1             ; factorial(5)
  RET r0                ; Return result (120)
.endfunc

.func factorial 5 1
  ; factorial(n) = n * factorial(n-1), base case: factorial(0) = 1
  ; Argument n in r0
  LOADK r1 2        ; r1 = 0
  LE r3 r0 r1       ; r3 = (n <= 0)
  JMPIF r3 base_case

  ; Recursive case
  MOV r4 r0         ; Save n in r4
  LOADK r1 1        ; r1 = 1
  SUB r1 r4 r1      ; r1 = n - 1
  NEWFUNC r0 factorial  ; Create function object for recursive call
  CALL r0 1         ; recursive call, result in r0
  MUL r2 r4 r0      ; r2 = n * factorial(n-1)
  RET r2

base_case:
  LOADK r2 1        ; Return 1
  RET r2
.endfunc
```

### Stack Overflow

If recursion exceeds `maxCallDepth`:
```
Error: stack overflow: max call depth 10000 exceeded
```

**Solutions:**
- Rewrite as iteration
- Raise the limit: `qoqi run --max-depth=100000 program.qi`
- Use tail-call optimization (future enhancement)

## Implementation Details

### Opcode Encoding

**CALL Instruction:**
```
Opcode: OpCALL (0x13)
Format: [OpCALL:8][A:16][B:16][C:16]
Fields:
  A = Function address or register number
  B = Argument count (argc)
  C = Unused (reserved)
```

**RET Instruction:**
```
Opcode: OpRET (0x14)
Format: [OpRET:8][A:16][B:16][C:16]
Fields:
  A = Register containing return value
  B = Unused
  C = Unused
```

**NEWEXFUNC Instruction:**
```
Opcode: OpNEWEXFUNC
Format: [OpNEWEXFUNC:8][A:16][B:16][C:16]
Fields:
  A = Destination register
  B = Extern function index in chunk's ExternNames array
  C = Unused (reserved)
```

**Note:** `CALL` handles both user-defined and external functions uniformly.

### Assembly Process

**Two-Pass Assembly:**

**Pass 1: Label Collection**
```
1. Scan code section
2. For each line ending with ':':
   - Extract label name
   - Record current instruction index
3. Build label → address map
```

**Pass 2: Code Generation**
```
1. Parse each instruction
2. For CALL/JMP with label:
   - Look up label in map
   - Encode as byte offset
3. For CALLEXTERN:
   - Add extern name to chunk.ExternNames
   - Encode index in instruction
4. Generate bytecode
```

### VM Execution Loop

**Simplified pseudocode:**
```python
while ip < len(chunk.Code):
    instr = chunk.Code[ip]

    match instr.Op:
        case OpCALL:
            # Save frame
            frames[frameCount] = CallFrame(chunk, ip, fp, regs)
            frameCount++

            # Create new register file
            newRegs = allocate(chunk.RegisterCount)
            copy(newRegs[0:argc], regs[0:argc])
            regs = newRegs

            # Jump
            ip = instr.A
            continue

        case OpRET:
            retVal = regs[instr.A]

            if frameCount == 0:
                return retVal

            # Restore frame
            frameCount--
            frame = frames[frameCount]
            chunk = frame.chunk
            ip = frame.ip
            fp = frame.fp
            regs = frame.regs
            regs[0] = retVal

        case OpNEWEXFUNC:
            externIdx = instr.B
            externName = chunk.ExternNames[externIdx]
            externFunc = externFuncs[externName]
            funcObj = heap.AllocExternFunction(externName, externFunc, -1)
            regs[instr.A] = funcObj

    ip++
```

## Performance Considerations

### Costs

**Per Function Call:**
- Frame allocation: O(1) - fixed array access
- Register copy: O(R) where R = register count
- Frame save: O(R) - copy all registers
- Frame restore: O(R) - copy all registers

**Total: O(R) per call/return**

### Optimizations

**Current:**
- Register file allocated once per function
- Frame stack pre-allocated (no dynamic allocation)
- Direct jump to function address

**Potential:**
- **Register allocation optimization**: Only copy live registers
- **Tail call optimization**: Reuse current frame
- **Inline small functions**: Avoid frame overhead
- **JIT compilation**: Eliminate interpreter overhead

### Memory Usage

**Per Call Frame:**
```
sizeof(CallFrame) = sizeof(Chunk*) + sizeof(int)*2 + sizeof([]Value)
                  ≈ 8 + 4 + 4 + 24 = 40 bytes (on 64-bit)
```

**Maximum Stack:**
```
10000 frames * 40 bytes = 400,000 bytes at the default limit
Plus register data: each frame has its own registers, 8 bytes each
```

**Actual usage typically much lower** due to:
- Functions use a few dozen registers
- Average call depth < 10

## Examples

### Example 1: Simple Function Call

```assembly
; Calculate sum of three numbers

.constants 3
  0: 10i
  1: 20i
  2: 30i

.func main 4 0
  NEWFUNC r0 sum3   ; r0 = function object
  LOADK r1 0        ; r1 = 10
  LOADK r2 1        ; r2 = 20
  LOADK r3 2        ; r3 = 30
  CALL r0 3         ; sum3(10, 20, 30)
  RET r0            ; Return 60
.endfunc

.func sum3 5 3
  ; sum3(a, b, c) = a + b + c
  ; Arguments: r0=a, r1=b, r2=c
  ADD r3 r0 r1      ; r3 = a + b
  ADD r4 r3 r2      ; r4 = (a + b) + c
  RET r4
.endfunc
```

### Example 2: Nested Calls

```assembly
; Compute (a + b) * (c + d)

.constants 4
  0: 5i
  1: 3i
  2: 2i
  3: 4i

.func main 5 0
  NEWFUNC r0 add    ; r0 = add function
  LOADK r1 0        ; r1 = 5
  LOADK r2 1        ; r2 = 3
  CALL r0 2         ; add(5, 3) = 8
  MOV r4 r0         ; Save result

  NEWFUNC r0 add    ; r0 = add function
  LOADK r1 2        ; r1 = 2
  LOADK r2 3        ; r2 = 4
  CALL r0 2         ; add(2, 4) = 6
  MOV r1 r0         ; r1 = 6

  NEWFUNC r0 mul    ; r0 = mul function
  MOV r2 r1         ; r2 = 6
  MOV r1 r4         ; r1 = 8
  CALL r0 2         ; mul(8, 6) = 48
  RET r0
.endfunc

.func add 3 2
  ADD r2 r0 r1
  RET r2
.endfunc

.func mul 3 2
  MUL r2 r0 r1
  RET r2
.endfunc
```

### Example 3: Fibonacci (Recursive)

```assembly
; Compute fibonacci(7) = 13

.constants 3
  0: 7i
  1: 2i
  2: 1i

.func main 2 0
  NEWFUNC r0 fib    ; r0 = fib function
  LOADK r1 0        ; r1 = 7
  CALL r0 1         ; fib(7)
  RET r0            ; Return 13
.endfunc

.func fib 6 1
  ; fib(n) = fib(n-1) + fib(n-2)
  ; Base: fib(0)=0, fib(1)=1
  ; Argument n in r0
  LOADK r1 1        ; r1 = 2
  LT r3 r0 r1       ; r3 = (n < 2)
  JMPIF r3 fib_base

  ; Recursive case
  MOV r4 r0         ; Save n
  LOADK r1 2        ; r1 = 1
  SUB r1 r4 r1      ; r1 = n - 1
  NEWFUNC r0 fib    ; Create function object
  CALL r0 1         ; fib(n-1)
  MOV r5 r0         ; Save fib(n-1)

  LOADK r1 1        ; r1 = 1
  LOADK r2 1        ; r2 = 1
  SUB r1 r4 r2      ; r1 = n - 2
  SUB r1 r1 r2      ; r1 = n - 2
  NEWFUNC r0 fib    ; Create function object
  CALL r0 1         ; fib(n-2)

  ADD r2 r5 r0      ; r2 = fib(n-1) + fib(n-2)
  RET r2

fib_base:
  MOV r2 r0         ; Return n (0 or 1)
  RET r2
.endfunc
```

### Example 4: External Functions

```assembly
; Mixed extern and user functions

.constants 2
  0: 10i
  1: 20i

.func main 4 0
  NEWFUNC r0 add       ; r0 = add function
  LOADK r1 0           ; r1 = 10
  LOADK r2 1           ; r2 = 20
  CALL r0 2            ; add(10, 20) = 30
  MOV r1 r0            ; r1 = 30
  NEWEXFUNC r0 println ; r0 = println function
  CALL r0 1            ; println(30) - arg in r1

  LOADNULL r0
  RET r0
.endfunc

.func add 3 2
  ADD r2 r0 r1
  RET r2
.endfunc
```

## Calls in Compiled Code

The convention above is what hand-written assembly follows. The compiler (`internal/codegen`) follows it too, with a fixed way of meeting its two awkward requirements:

*   **The result of every call lands in `r0`.** Compiled functions never keep a variable in `r0`. A function's first argument arrives there, so the first instruction of a compiled function moves it to another register (omitted when the argument is unused).
*   **The callee and its arguments must be in consecutive registers.** Each function reserves a block of registers above all its variables, the *call window*. A call copies the callee and the arguments into it, executes `CALL`, then copies the result out of `r0`:

    ```assembly
    MOV  r5 r3      ; callee
    MOV  r6 r2      ; argument 1
    CALL r5 1
    MOV  r3 r0      ; result
    ```

*   **Methods** receive the value they were called on as their first argument (`self`), ahead of the declared parameters.
*   **Variadic functions** receive the extra arguments as one array in their last parameter; the caller builds it.
*   **The process exit code** of `qoqi run` is the `Int` returned by `main`.

Details: [compilation/bytecode_generation.md](compilation/bytecode-generation.md) and [compilation/ir_generation.md](compilation/ir-generation.md).

## See Also

- [Assembly Documentation](vm.md) - Complete assembly language reference
- [VM Architecture](vm.md) - Virtual machine implementation details
- [Memory Management](memory-management.md) - Heap and garbage collection

## Version History

- **v1.0** (2025-11-25): Initial implementation
  - Basic function calls with labels
  - Register-based argument passing
  - Recursion support
  - External function interface
