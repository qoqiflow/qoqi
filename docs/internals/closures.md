# Closures and Upvalues Specification

This document describes the internal implementation of closures and upvalue management in the Qoqi virtual machine.

## Overview

A **closure** is a function instance that captures variables from its lexical environment (the parent scope). These captured variables are called **upvalues**.

Qoqi implements closures using an approach similar to Lua 5.1:
- Functions are first-class objects.
- Upvalues can be **open** (pointing to the stack) or **closed** (copied to the heap).
- The VM automatically manages the closing of upvalues when variables go out of scope.

## Object Structure

### FunctionObj (Closure)

In the heap, a function is represented by `FunctionObj`. To support closures, this object contains a list of upvalues:

```go
type FunctionObj struct {
    header   ObjHeader
    chunk    *Chunk
    protoIdx int
    upvalues []*UpvalueObj // List of captured variables
}
```

### UpvalueObj

An upvalue is an intermediate object that points to the actual value.

```go
type UpvalueObj struct {
    header   ObjHeader
    location *Value // Pointer to stack slot (nil when closed)
    closed   Value  // Closed-over value (used when location == nil)
}
```

- **Open Upvalue**: `location` points to a register in the VM stack (`&vm.regs[i]`).
- **Closed Upvalue**: `location` is `nil`. The value is stored in the `closed` field.

## Bytecode Instructions

The following instructions handle closure creation and upvalue manipulation:

### 1. NEWFUNC `A B`
Creates a new closure from a function prototype.
- **A**: Destination register.
- **B**: Prototype index (`protoIdx`).

This instruction allocates a new `FunctionObj`. The number of upvalues to allocate is determined by the `UpvalueCount` field in the function prototype.

### 2. CAPLOCAL `A B C`
Captures a local variable from the current stack frame into a closure's upvalue list.
- **A**: Destination closure register (must be a `FunctionObj`).
- **B**: Index of the upvalue in the closure's upvalue list.
- **C**: Register index of the local variable in the current stack frame.

This instruction finds or creates an open upvalue pointing to `vm.regs[C]` and stores it in `closure.upvalues[B]`.

### 3. CAPUPVAL `A B C`
Captures an existing upvalue from the current function (parent scope) into a new closure.
- **A**: Destination closure register.
- **B**: Index of the upvalue in the new closure's upvalue list.
- **C**: Index of the upvalue in the *current* function's upvalue list.

This instruction copies the `UpvalueObj` pointer from `currentFunc.upvalues[C]` to `newClosure.upvalues[B]`.

### 4. LDCAP `A B` (Load Capture)
Loads the value of an upvalue into a register.
- **A**: Destination register.
- **B**: Index of the upvalue in the current closure.

### 5. STCAP `A B` (Store Capture)
Stores the value of a register into an upvalue.
- **A**: Index of the upvalue.
- **B**: Source register.

## Upvalue Lifecycle

1. **Creation (Open)**:
   When `CAPLOCAL` is executed:
   - The VM checks the `openUpvalues` list to see if an upvalue already exists for the target register.
   - If yes, it reuses it (sharing).
   - If no, it creates a new "open" upvalue pointing to the stack register and adds it to the list.

2. **Access**:
   `LDCAP` and `STCAP` instructions read/write via the `UpvalueObj`.
   - If open (`location != nil`), they access the stack register directly.
   - If closed (`location == nil`), they access the `closed` field.

3. **Closing (Close)**:
   When a function returns (`RET`), its stack frame is about to be destroyed. The VM calls `closeUpvalues()`:
   - It iterates through the `openUpvalues` list.
   - For any upvalue pointing to a register in the dying stack frame:
     - The value is copied from the stack to the `closed` field.
     - The `location` pointer is set to `nil`.
     - The upvalue is removed from the `openUpvalues` list.

## Example Workflow

```assembly
; Function that returns a closure
.func makeCounter 1 0 0
    LOADK r0 0          ; r0 = count (local variable)
    
    ; Create closure 'inc' (prototype 0)
    ; 'inc' expects 1 upvalue
    NEWFUNC r1 0        
    
    ; Capture r0 as upvalue 0 of r1
    CAPLOCAL r1 0 r0    
    
    RET r1
.endfunc

; The closure function
.func inc 1 0 1         ; 1 reg, 0 args, 1 upvalue
    ; Load upvalue 0 into r0
    LDCAP r0 0          
    
    ; Increment
    ADD r0 r0 1         
    
    ; Store back to upvalue 0
    STCAP 0 r0          
    
    RET r0
.endfunc
```

## How the Compiler Uses These Instructions

The sections above describe the VM mechanism. This is how Qoqi source maps onto it.

```rust
fn makeCounter() -> fn() -> Int {
    let count = 0;
    return fn() -> Int {
        count = count + 1;
        return count;
    };
}
```

1.  **Closure analysis** (`internal/analysis`) finds that the function expression uses `count`, a local of the enclosing function, and records it as capture 0. The order of a function's captures is its upvalue numbering.
2.  **IR generation** (`internal/irgen`) compiles the lambda as its own function. Inside it, reads of `count` become `LOAD_UPVALUE 0` and writes `STORE_UPVALUE 0`. At the place of the expression it emits `MAKE_CLOSURE "lambda_0", <register of count>`.
3.  **Bytecode generation** (`internal/codegen`) turns `MAKE_CLOSURE` into `NEWFUNC` followed by one capture instruction per variable, and the upvalue operations into `LDCAP`/`STCAP`. The prototype's upvalue count comes from step 1.

### Captures are by reference

`CAPLOCAL` points the upvalue at the *register* of the variable, not at its current value. Consequences visible in the language:

*   A closure sees assignments made after it was created, and the enclosing function sees assignments made by the closure.
*   Two closures capturing the same variable share it (`findOrCreateUpvalue` returns the same upvalue for the same register).
*   When the enclosing function returns, `RET` closes the upvalue: the value moves into the upvalue object and lives as long as a closure references it.

For this to hold, the register of a captured variable must keep holding that variable for the whole function. The register allocator therefore never reuses the register of anything passed to `MAKE_CLOSURE` (see [compilation/bytecode_generation.md](compilation/bytecode-generation.md#register-allocation)).

### Loops: one variable per iteration

A variable declared in a loop body has one register for all iterations. If nothing were done, every closure created in the loop would point at that register and end up seeing the last iteration's value.

So when an iteration ends, the closures created in it are **detached** from the body's variables: `CLOSEUP r` closes the upvalue for a register, exactly as `RET` does for a whole frame. The closure keeps the value from that moment; the next iteration writes the register afresh and a closure created then gets a new upvalue.

```rust
for v in [1, 2, 3] {
    readers.push(fn() -> Int { return v; });
}
```
```
body:
    ... v = xs[index] ...
    %v9 = MAKE_CLOSURE "lambda_0", %v7      ; captures v
    ...
    CLOSE_UPVALUES %v7                      ; end of the iteration
    JMP cond
```

The IR generator records, per loop, which registers of variables declared in its body were captured, and emits `CLOSE_UPVALUES` for them before the jump back and before each `continue`. `break` and `return` need nothing: nothing writes the register again. In nested loops a variable of the inner body is detached by the inner loop and again by the outer one, since the outer body contains it.

Within one iteration nothing changes: the closure and the body share the variable until the iteration ends. Variables declared outside the loop are not touched, so closures from all iterations still share those.

### Nested closures

When a lambda uses a variable declared two or more functions up, every function in between has to carry it, even if it never mentions it:

```rust
fn main() -> Int {
    let a = 1;
    let outer = fn() -> Int {
        let inner = fn() -> Int { return a; };   // uses main's `a`
        return inner();
    };
    return outer();
}
```

| Function | Captures | How it obtains `a` |
|----------|----------|--------------------|
| `outer` (`lambda_0`) | `a` | `main` passes its register: `CAPLOCAL` |
| `inner` (`lambda_1`) | `a` | `outer` passes its own upvalue 0: `CAPUPVAL` |

Closure analysis adds the capture to the intermediate function and marks it `FromUpvalue` in the inner one. In the IR the two cases are distinguished by the capture operand: a register, or a constant of type `Upvalue`.

### `self` in methods

Inside a method `self` is an ordinary local (the first parameter), so a lambda in a method captures it like any other variable.

