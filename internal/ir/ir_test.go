package ir

import (
	"fmt"
	"testing"
)

func TestIRBuilder(t *testing.T) {
	// Simulate:
	// fn test(p: Point) {
	//     let x = p.x;
	//     let y = p.clone(); // Trait call
	//     return x;
	// }

	builder := NewBuilder("test")
	p := builder.AddParam() // %v1 (p)

	// let x = p.x
	x := builder.CreateGetField(p, "x") // %v2

	// let y = p.clone() (Trait call)
	// Assuming p implements Cloneable
	builder.CreateCallTrait(p, "Cloneable", "clone") // %v3

	// return x
	builder.CreateRet(x)

	// Print the result
	output := builder.Func.Dump()
	fmt.Println(output)

	// Basic validation
	if len(builder.Func.Blocks) != 1 {
		t.Errorf("Expected 1 block, got %d", len(builder.Func.Blocks))
	}
	if len(builder.Func.Blocks[0].Instructions) != 3 {
		t.Errorf("Expected 3 instructions, got %d", len(builder.Func.Blocks[0].Instructions))
	}
}

func TestControlFlow(t *testing.T) {
	// Simulate:
	// fn max(a, b) {
	//     if a > b {
	//         return a;
	//     } else {
	//         return b;
	//     }
	// }

	builder := NewBuilder("max")
	a := builder.AddParam() // %v1
	b := builder.AddParam() // %v2

	thenBlock := builder.NewBlock()
	elseBlock := builder.NewBlock()

	// cond = a > b
	// Note: We don't have CreateGt in builder yet, let's use emit directly or add it.
	// For this test, I'll use emit directly to keep builder simple for now.
	cond := builder.NewRegister()
	builder.Emit(OpGt, cond, a, b)

	builder.CreateBr(cond, thenBlock, elseBlock)

	// Then block
	builder.SetBlock(thenBlock)
	builder.CreateRet(a)

	// Else block
	builder.SetBlock(elseBlock)
	builder.CreateRet(b)

	output := builder.Func.Dump()
	fmt.Println(output)

	if len(builder.Func.Blocks) != 3 {
		t.Errorf("Expected 3 blocks, got %d", len(builder.Func.Blocks))
	}
}
