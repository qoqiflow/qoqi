# Memory Management

Qoqi uses a simple mark-and-sweep garbage collector to manage heap-allocated objects. This document describes the memory management system and how to use it.

## Overview

The memory management system consists of:

1. **Bounded heap** - A limit on what the program may hold at once (256MB by default); memory is taken as needed, not pre-allocated
2. **NaN-boxed values** - Efficient 64-bit value representation with heap pointers
3. **Mark-sweep GC** - Simple, stop-the-world garbage collection
4. **Five heap object types** - String, Array, Object/Map, Function, and Upvalue

## Heap Architecture

### Heap Structure

The heap is a fixed-size memory region that tracks all allocated objects:

```go
type Heap struct {
    objects        *ObjHeader  // Linked list of all objects
    bytesAllocated uint64      // Current memory usage
    nextGC         uint64      // GC trigger threshold
    maxBytes       uint64      // Heap size limit
}
```

**Default configuration:**
- Heap limit: 256MB (`DefaultHeapSize`), `--heap-mb=N` on the command line
- First collection: after 1MB has been allocated (`DefaultGCStart`), or at 80% of a heap smaller than that
- After each collection: the threshold is twice what survived, never below the first one and never above the limit
- Allocation tracking: per object, including growth after creation (array capacity, object fields)

The threshold following the live data is what keeps collection cheap. A
threshold that stays near the live size makes the collector run after every
few allocations, and each run walks everything alive; appending to one large
array then costs time proportional to the square of its length.

### Object Layout

All heap objects start with a common header:

```go
type ObjHeader struct {
    Type   ObjType     // String, Array, or Object
    Marked bool        // GC mark flag
    Next   *ObjHeader  // Next object in linked list
}
```

Objects are linked together in a singly-linked list for easy traversal during garbage collection.

## Heap Object Types

### 1. String Objects

Immutable strings stored on the heap:

```go
type StringObj struct {
    header ObjHeader
    data   string
}
```

**Operations:**
- `AllocString(s string)` - Allocate a new string
- `Data()` - Get string contents

**Example:**
```go
str, shouldGC, err := vm.heap.AllocString("hello world")
if err != nil {
    // Handle allocation failure
}
```

A text written in the program is made once and kept for as long as the program runs (`NEWSTR` hands out the same object each time). `AllocJoined(a, b)` makes the text of two texts one after the other, which is what `+` does: up to 64 bytes it is one allocation holding the object and its bytes.

### 2. Array Objects

Dynamic arrays that can grow:

```go
type ArrayObj struct {
    header   ObjHeader
    elements []Value
}
```

**Operations:**
- `AllocArray(capacity int)` - Allocate array with initial capacity
- `Get(i int)` - Get element at index (returns null if out of bounds)
- `Set(i int, v Value)` - Set element at index (grows array if needed)
- `Append(v Value)` - Append element to end
- `Len()` - Get current length

**Example:**
```go
arr, shouldGC, err := vm.heap.AllocArray(10)
arr.Append(NewInt(1))
arr.Append(NewInt(2))
arr.Set(0, NewInt(42))
val := arr.Get(0)  // Returns 42
```

### 3. Object/Map

The values of an object in slots, and a shape that says which field is in
which slot. Objects with the same fields, added in the same order, share one
shape: the names are kept once, not in every object.

```go
type ObjectObj struct {
    header ObjHeader
    shape  *Shape   // Which field is in which slot, and what the object is a value of
    slots  []Value
}
```

The shape also carries the name of the struct or of the variant, so an object is 48 bytes and its values. Made with the number of its fields known (`AllocObjectOf(shape, fields)`, which is what `NEWOBJ` does), it comes with room for them in the same allocation: the values lie right after the object. With more than 16 fields, or more fields than was said, the values are a second allocation, as an array's are.

A value of an enum that holds nothing is made once for each place of the program that makes it, and kept for as long as the program runs.

**Operations:**
- `AllocObject()` - Allocate an empty object of no type
- `RootShape(name, variant)` - The shape the values of a struct, or of a variant of an enum, start with
- `AllocObjectOf(shape, fields)` - Allocate a value of that type, with room for its fields
- `Get(key string)` - Get value by key (returns value and bool)
- `Set(key string, v Value)` - Set key-value pair
- `Delete(key string)` - Take a field away
- `Keys()` - The names, in the order the fields were added

**Example:**
```go
obj, shouldGC, err := vm.heap.AllocObject()
obj.Set("name", NewInt(123))
obj.Set("age", NewInt(25))
val, ok := obj.Get("name")  // Returns 123, true
```

### 4. Function Objects

First-class functions and closures:

```go
type FunctionObj struct {
    header   ObjHeader
    chunk    *Chunk
    protoIdx int
    upvalues []*UpvalueObj
}
```

**Operations:**
- `AllocFunction(chunk *Chunk, protoIdx int)` - Allocate a new function closure

### 5. Upvalue Objects

Captured variables for closures:

```go
type UpvalueObj struct {
    header ObjHeader
    stack  *regStack // The stack the variable is on; nil once closed
    slot   int       // Its place there
    closed Value     // The value, once closed
}
```

**Operations:**
- `AllocUpvalue(stack, slot)` - Allocate a new upvalue, open on a register of a stack

## Garbage Collection

### Mark-Sweep Algorithm

Qoqi uses a classic two-phase mark-and-sweep garbage collector:

#### Phase 1: Mark

Starting from the **GC roots**, mark all reachable objects. Marking an object
that can hold others puts it on a list; the list is then emptied, marking
what each object on it holds:

```go
func (vm *VM) CollectGarbage() {
    vm.markRoots()      // What the program holds directly
    vm.markReachable()  // What those objects hold, and so on
    vm.heap.sweep()
}
```

The marking process:
1. Check if value is a heap pointer
2. Skip if already marked (prevents cycles)
3. Set `Marked = true` on the object header
4. Put it on the list of objects to look into, unless it holds none (a string)

Nothing here calls itself for each object held: how deep a structure goes
costs memory for the list, not stack. A linked list of millions of nodes is
marked like any other value.

#### Phase 2: Sweep

Walk the object list and free unmarked objects:

```go
func (h *Heap) sweep() {
    current := h.objects
    for current != nil {
        if current.Marked {
            // Keep object, clear flag for next cycle
            current.Marked = false
        } else {
            // Free unreachable object
            freeObject(current)
        }
        current = current.Next
    }
}
```

### GC Triggers

Garbage collection can be triggered in three ways:

#### 1. Automatic Trigger

After any allocation, check if threshold is exceeded:

```go
obj, shouldGC, err := vm.heap.AllocArray(10)
if shouldGC {
    vm.CollectGarbage()  // Automatically triggered
}
```

Threshold: `bytesAllocated >= nextGC`. `nextGC` starts at 1MB and is set to twice the surviving bytes after each collection (see the configuration above).

#### 2. Manual Trigger

Use the `GC` opcode or call directly:

```go
vm.CollectGarbage()
```

#### 3. Allocation Failure

An allocation, or the growth of an array or object, that would exceed the heap limit runs a collection first and is retried. Only if there is still no room does the program stop with `out of memory`.

### Texts joined

Two texts joined whose length reaches 1 KB are not copied: the new text is the two (a rope), and its bytes are written out the first time something reads them, then kept. A text built a part at a time, `text = text + part`, is then a chain of parts written out once, instead of a copy of everything so far at every part. A rope knows its length in characters from its parts. The collector keeps the parts of a rope while it is not written out. A rope is counted for itself when it is made, and for its bytes when it is written out.

### Go's collector

Every object of a program is also a Go object: the VM's collector frees it by forgetting it, and Go's collector takes the memory back. A program that makes many small values would otherwise have Go collect every few megabytes it allocates. `qoqi run` and the executables of `qoqi native` set Go's collector to run a quarter as often (`GOGC=400`), with a soft limit of twice the program's heap limit and 512 MB, past which it collects sooner: 8 to 15% less time for such programs, for some more memory. `GOGC` and `GOMEMLIMIT`, when set, are left as they are.

### GC Roots

Objects reachable from these locations are never collected:

1. **Registers** - of every call in progress, of every task: each task's stack up to the end of its innermost call
2. **Functions** - whose calls are in progress
3. **Pins** - values a native function is working with, and what a protected call returned until the program takes it
4. **Caches** - the function objects made once (see `NEWFUNC`), the texts of one character

A call does not clear its registers, so those of a new call may still hold
what a call that returned left there. A collection marks a stack only up to
the end of its innermost call and makes null what lies above it, up to the
highest register used since the last collection. Every value below the end
was then marked by each collection since it was written, or made null by
one: a call that takes registers again never holds an object that was freed.
The end is that of the task whose turn it is, where it is now, and that of
each other task where it stopped.

### What is counted

Every object counts against the limit for its own size plus what it holds
directly: the bytes of a text, the capacity of an array, the slots of an
object, the captured variables of a closure. Growth after creation is counted
when it happens.

The same function gives the size when an object is made and when it is freed
(`sizeOf` in `heap.go`). The two have to agree: an object counted for more
going in than coming out leaves the difference behind for good, and a program
that runs long enough is refused memory it has. A test makes and drops every
kind of object and checks that the count comes back to where it started.

Room is reserved before the memory is asked of the machine: an array larger
than the limit is refused without being made.

### Performance Characteristics

**Stop-the-world GC:**
- Pauses execution during collection
- Predictable behavior
- Simple to implement and debug

**Time complexity:**
- Mark phase: O(reachable objects + edges)
- Sweep phase: O(total objects)

**Space overhead:**
- 1 bit per object (marked flag)
- Pointer per object (linked list)

## Bytecode Operations

### Allocation Opcodes

#### NEWARRAY
Allocate an array with specified capacity:

```
NEWARRAY r0 10    ; r0 = new array with capacity 10
```

Format: `NEWARRAY dest capacity`

#### NEWOBJ
Allocate an empty object/map:

```
NEWOBJ r0         ; r0 = new object
```

Format: `NEWOBJ dest`

#### NEWSTR
Load a string constant (strings pre-allocated in constant pool):

```
NEWSTR r0 0       ; r0 = string from constant[0]
```

Format: `NEWSTR dest const_index`

### Array Operations

#### GETINDEX
Get array element:

```
GETINDEX r2 r0 r1  ; r2 = r0[r1]
```

Format: `GETINDEX dest array index`

#### SETINDEX
Set array element:

```
SETINDEX r0 r1 r2  ; r0[r1] = r2
```

Format: `SETINDEX array index value`

#### ARRLEN
Get array length:

```
ARRLEN r1 r0       ; r1 = len(r0)
```

Format: `ARRLEN dest array`

### Object Operations

#### GETFIELD
Get object field:

```
GETFIELD r2 r0 0   ; r2 = r0[constant[0]]
```

Format: `GETFIELD dest object field_name_const_index`

Field name must be a string constant in the constant pool.

#### SETFIELD
Set object field:

```
SETFIELD r0 0 r1   ; r0[constant[0]] = r1
```

Format: `SETFIELD object field_name_const_index value`

### Garbage Collection

#### GC
Manually trigger garbage collection:

```
GC                 ; Run garbage collection
```

Format: `GC`

## Examples

### Example 1: Simple Array

Assembly code:

```asm
; Create array and populate it
NEWARRAY r0 3          ; r0 = new array(3)
LOADK r1 0             ; r1 = 10
LOADK r2 1             ; r2 = 0
SETINDEX r0 r2 r1      ; r0[0] = 10

LOADK r1 2             ; r1 = 20
LOADK r2 3             ; r2 = 1
SETINDEX r0 r2 r1      ; r0[1] = 20

LOADK r2 3             ; r2 = 1
GETINDEX r3 r0 r2      ; r3 = r0[1]
RET r3                 ; return 20
```

Constants: `[10, 0, 20, 1]`

### Example 2: Object with Fields

Assembly code:

```asm
; Create object and set fields
NEWOBJ r0              ; r0 = new object
LOADK r1 2             ; r1 = 123
SETFIELD r0 0 r1       ; r0["name"] = 123

LOADK r1 3             ; r1 = 25
SETFIELD r0 1 r1       ; r0["age"] = 25

GETFIELD r2 r0 1       ; r2 = r0["age"]
RET r2                 ; return 25
```

Constants: `["name", "age", 123, 25]` (as heap pointers)

### Example 3: Nested Structures

Create an object with an array field:

```asm
NEWOBJ r0              ; r0 = new object
NEWARRAY r1 3          ; r1 = new array(3)

LOADK r2 1             ; r2 = 100
LOADK r3 2             ; r3 = 0
SETINDEX r1 r3 r2      ; r1[0] = 100

SETFIELD r0 0 r1       ; r0["data"] = r1

GETFIELD r4 r0 0       ; r4 = r0["data"]
LOADK r3 2             ; r3 = 0
GETINDEX r5 r4 r3      ; r5 = r4[0]
RET r5                 ; return 100
```

Constants: `["data", 100, 0]`

### Example 4: Manual GC

Allocate garbage and trigger collection:

```asm
NEWARRAY r0 10         ; r0 = array (garbage)
NEWARRAY r0 10         ; r0 = array (overwrites, making previous garbage)
NEWOBJ r1              ; r1 = object (garbage)
GC                     ; Collect unreachable objects
LOADK r2 0             ; r2 = 42
RET r2                 ; return 42
```

After `GC`, the first array and object are collected since nothing references them.

## Memory Usage Tracking

### Query Heap Statistics

```go
vm := NewVM()

// Get current memory usage
bytes := vm.heap.BytesAllocated()

// Get heap limit
maxBytes := vm.heap.MaxBytes()

// Check if GC should run
shouldGC := vm.heap.ShouldCollect()
```

### Custom Heap Size

Create VM with custom heap size:

```go
// 10KB heap for testing
vm := NewVMWithHeapSize(10 * 1024)

// Or 10MB heap for production
vm := NewVMWithHeapSize(10 * 1024 * 1024)
```

## Best Practices

### 1. Minimize Allocations

Reuse objects when possible:

```asm
; Bad: Creates new array each iteration
LOOP:
    NEWARRAY r0 10
    ; ... use array ...
    JMP LOOP

; Better: Reuse array
NEWARRAY r0 10
LOOP:
    ; ... use same array ...
    JMP LOOP
```

### 2. Keep Live Set Small

Don't hold references to objects you don't need:

```asm
; Bad: Holds all arrays
NEWARRAY r0 10
NEWARRAY r1 10
NEWARRAY r2 10
; All 3 arrays stay alive

; Better: Overwrite registers
NEWARRAY r0 10
; ... use r0 ...
NEWARRAY r0 10    ; First array can be collected
; ... use new r0 ...
```

### 3. Monitor Heap Usage

Check `ShouldCollect()` and adjust allocation patterns if GC runs too frequently.

### 4. Test with Small Heaps

Use small heap sizes during testing to catch memory leaks:

```go
vm := NewVMWithHeapSize(1024)  // 1KB - very tight
```

## Limitations

Current implementation has these limitations:

1. **No heap growth** - Fixed size, allocation fails when full
2. **No compaction** - Fragmentation can reduce available memory
3. **Stop-the-world** - GC pauses execution
4. **Simple allocator** - Not optimized for performance
5. **No weak references** - All reachable objects are kept
6. **No finalizers** - No cleanup hooks when objects are freed
7. **One thread** - The collector assumes nothing else touches the heap while it runs, and nothing does: tasks take turns. Tasks on several processors would need a collector made for it
8. **Pointers kept as numbers** - A value holds the address of its object as a number, which Go's own collector does not follow; the heap's list of objects is what keeps them alive. This relies on Go not moving heap objects, and is why the race detector's pointer check is turned off for the tests (`make test-race`)

## Future Improvements

Potential enhancements:

- **Growing heap** - Expand heap when needed
- **Generational GC** - Separate young/old generations
- **Incremental GC** - Reduce pause times
- **Compaction** - Reduce fragmentation
- **Better allocator** - Free lists, size classes, etc.
- **Write barriers** - Enable incremental/concurrent GC
- **GC statistics** - Collection counts, pause times, etc.

## Debugging Tips

### Count Objects

```go
func countObjects(h *Heap) int {
    count := 0
    current := h.objects
    for current != nil {
        count++
        current = current.Next
    }
    return count
}
```

### Print Heap Stats

```go
fmt.Printf("Heap: %d / %d bytes (%.1f%%)\n",
    heap.BytesAllocated(),
    heap.MaxBytes(),
    float64(heap.BytesAllocated())/float64(heap.MaxBytes())*100)
```

### Verify GC Correctness

```go
// Allocate objects
obj1 := heap.AllocObject()
obj2 := heap.AllocString("test")

// Keep only obj1 in registers
vm.regs[0] = NewHeapPtr(uintptr(unsafe.Pointer(obj1)))

// Run GC
vm.CollectGarbage()

// obj1 should survive, obj2 should be collected
```

## Summary

Qoqi's memory management provides:
- ✓ Simple, predictable garbage collection
- ✓ Three essential heap object types
- ✓ Automatic and manual GC triggering
- ✓ Fixed-size heap with overflow detection
- ✓ Comprehensive bytecode operations

The design prioritizes simplicity and debuggability over performance, making it ideal for learning and prototyping language implementations.
