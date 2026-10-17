package vm

import (
	"testing"
)

func TestSimpleClosure(t *testing.T) {
	// Test a simple closure that captures a local variable
	// outer() {
	//   x = 10
	//   inner() { return x }
	//   return inner()
	// }
	chunk := NewChunk()

	// String constants
	chunk.AddConstant(NewInt(10)) // const[0] = 10

	// Function prototypes
	// inner function (index 0)
	chunk.Prototypes = append(chunk.Prototypes, FunctionPrototype{
		Name:          "inner",
		CodeStart:     0,
		CodeSize:      2,
		RegisterCount: 1,
		ArgCount:      0,
		UpvalueCount:  1,
	})

	// outer function (index 1)
	chunk.Prototypes = append(chunk.Prototypes, FunctionPrototype{
		Name:          "outer",
		CodeStart:     2,
		CodeSize:      6,
		RegisterCount: 3,
		ArgCount:      0,
		UpvalueCount:  0,
	})

	// main function (index 2)
	chunk.Prototypes = append(chunk.Prototypes, FunctionPrototype{
		Name:          "main",
		CodeStart:     8,
		CodeSize:      3,
		RegisterCount: 2,
		ArgCount:      0,
		UpvalueCount:  0,
	})

	// Code for inner function (IP 0-1)
	// Captures upvalue[0] which is x from outer
	chunk.Code = append(chunk.Code,
		NewInstr(OpLDCAP, 0, 0, 0), // r0 = upvalue[0] (x)
		NewInstr(OpRET, 0, 0, 0),   // return r0
	)

	// Code for outer function (IP 2-10)
	chunk.Code = append(chunk.Code,
		NewInstr(OpLOADK, 1, 0, 0),    // r1 = 10
		NewInstr(OpNEWFUNC, 0, 0, 0),  // r0 = closure(inner)
		NewInstr(OpCAPLOCAL, 0, 0, 1), // capture r1 as upvalue[0] of r0
		NewInstr(OpMOV, 2, 0, 0),      // r2 = r0 (save closure)
		NewInstr(OpCALL, 2, 0, 0),     // call r2()
		// Result is in r0
		NewInstr(OpRET, 0, 0, 0), // return r0
	)

	// Code for main function (IP 11-14)
	chunk.Code = append(chunk.Code,
		NewInstr(OpNEWFUNC, 0, 1, 0), // r0 = outer function
		NewInstr(OpCALL, 0, 0, 0),    // call outer()
		NewInstr(OpRET, 0, 0, 0),     // return r0
	)

	vm := NewVM()
	result, err := vm.Exec(chunk)

	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}

	if !result.IsInt() || result.AsInt() != 10 {
		t.Errorf("expected 10, got %v", result)
	}
}

func TestClosureModification(t *testing.T) {
	// Test a closure that modifies a captured variable
	// counter() {
	//   count = 0
	//   inc() { count = count + 1; return count }
	//   inc()
	//   return inc()
	// }
	chunk := NewChunk()

	// Constants
	chunk.AddConstant(NewInt(0)) // const[0] = 0
	chunk.AddConstant(NewInt(1)) // const[1] = 1

	// Function prototypes
	// inc function (index 0)
	chunk.Prototypes = append(chunk.Prototypes, FunctionPrototype{
		Name:          "inc",
		CodeStart:     0,
		CodeSize:      5,
		RegisterCount: 2,
		ArgCount:      0,
		UpvalueCount:  1,
	})

	// counter function (index 1)
	chunk.Prototypes = append(chunk.Prototypes, FunctionPrototype{
		Name:          "counter",
		CodeStart:     5,
		CodeSize:      6,
		RegisterCount: 3,
		ArgCount:      0,
		UpvalueCount:  0,
	})

	// main function (index 2)
	chunk.Prototypes = append(chunk.Prototypes, FunctionPrototype{
		Name:          "main",
		CodeStart:     11,
		CodeSize:      3,
		RegisterCount: 2,
		ArgCount:      0,
		UpvalueCount:  0,
	})

	// Code for inc function (IP 0-4)
	// Captures upvalue[0] which is count from counter
	chunk.Code = append(chunk.Code,
		NewInstr(OpLDCAP, 0, 0, 0), // r0 = upvalue[0] (count)
		NewInstr(OpLOADK, 1, 1, 0), // r1 = 1
		NewInstr(OpADD, 0, 0, 1),   // r0 = r0 + r1
		NewInstr(OpSTCAP, 0, 0, 0), // upvalue[0] = r0
		NewInstr(OpRET, 0, 0, 0),   // return r0
	)

	// Code for counter function (IP 5-15)
	chunk.Code = append(chunk.Code,
		NewInstr(OpLOADK, 1, 0, 0),    // r1 = 0 (count)
		NewInstr(OpNEWFUNC, 2, 0, 0),  // r2 = closure(inc)
		NewInstr(OpCAPLOCAL, 2, 0, 1), // capture r1 as upvalue[0] of r2
		NewInstr(OpCALL, 2, 0, 0),     // call inc() - returns 1
		// Result is now in r0, but closure is still in r2
		NewInstr(OpCALL, 2, 0, 0), // call inc() again - returns 2
		NewInstr(OpRET, 0, 0, 0),  // return r0
	)

	// Code for main function (IP 16-19)
	chunk.Code = append(chunk.Code,
		NewInstr(OpNEWFUNC, 0, 1, 0), // r0 = counter function
		NewInstr(OpCALL, 0, 0, 0),    // call counter()
		NewInstr(OpRET, 0, 0, 0),     // return r0
	)

	vm := NewVM()
	result, err := vm.Exec(chunk)

	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}

	if !result.IsInt() || result.AsInt() != 2 {
		t.Errorf("expected 2, got %v", result)
	}
}

func TestNestedClosures(t *testing.T) {
	// Test nested closures capturing the same variable
	// makeAdder(x) {
	//   addX(y) { return x + y }
	//   return addX
	// }
	// main() {
	//   add5 = makeAdder(5)
	//   return add5(3)
	// }
	chunk := NewChunk()

	// Constants
	chunk.AddConstant(NewInt(5)) // const[0] = 5
	chunk.AddConstant(NewInt(3)) // const[1] = 3

	// Function prototypes
	// addX function (index 0) - 1 arg, captures x
	chunk.Prototypes = append(chunk.Prototypes, FunctionPrototype{
		Name:          "addX",
		CodeStart:     0,
		CodeSize:      3,
		RegisterCount: 2,
		ArgCount:      1,
		UpvalueCount:  1,
	})

	// makeAdder function (index 1) - 1 arg
	chunk.Prototypes = append(chunk.Prototypes, FunctionPrototype{
		Name:          "makeAdder",
		CodeStart:     3,
		CodeSize:      3,
		RegisterCount: 2,
		ArgCount:      1,
		UpvalueCount:  0,
	})

	// main function (index 2)
	chunk.Prototypes = append(chunk.Prototypes, FunctionPrototype{
		Name:          "main",
		CodeStart:     6,
		CodeSize:      7,
		RegisterCount: 4,
		ArgCount:      0,
		UpvalueCount:  0,
	})

	// Code for addX function (IP 0-2)
	// r0 = y (argument), upvalue[0] = x
	chunk.Code = append(chunk.Code,
		NewInstr(OpLDCAP, 1, 0, 0), // r1 = upvalue[0] (x)
		NewInstr(OpADD, 0, 0, 1),   // r0 = r0 (y) + r1 (x)
		NewInstr(OpRET, 0, 0, 0),   // return r0
	)

	// Code for makeAdder function (IP 3-6)
	// r0 = x (argument)
	chunk.Code = append(chunk.Code,
		NewInstr(OpNEWFUNC, 1, 0, 0),  // r1 = closure(addX)
		NewInstr(OpCAPLOCAL, 1, 0, 0), // capture r0 (arg x) as upvalue[0] of r1
		NewInstr(OpRET, 1, 0, 0),      // return r1
	)

	// Code for main function (IP 7-14)
	chunk.Code = append(chunk.Code,
		NewInstr(OpNEWFUNC, 0, 1, 0), // r0 = makeAdder function
		NewInstr(OpLOADK, 1, 0, 0),   // r1 = 5
		NewInstr(OpCALL, 0, 1, 0),    // r0 = makeAdder(5)
		NewInstr(OpMOV, 2, 0, 0),     // r2 = r0 (add5 closure)
		NewInstr(OpLOADK, 3, 1, 0),   // r3 = 3
		NewInstr(OpCALL, 2, 1, 0),    // r0 = add5(3)
		NewInstr(OpRET, 0, 0, 0),     // return r0
	)

	vm := NewVM()
	result, err := vm.Exec(chunk)

	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}

	if !result.IsInt() || result.AsInt() != 8 {
		t.Errorf("expected 8 (5+3), got %v", result)
	}
}
