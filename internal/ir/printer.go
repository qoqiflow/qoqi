package ir

import (
	"bytes"
	"fmt"
)

// Dump returns a string representation of the program
func (p *Program) Dump() string {
	var buf bytes.Buffer
	for _, fn := range p.Functions {
		buf.WriteString(fn.Dump())
		buf.WriteString("\n")
	}
	return buf.String()
}

// Dump returns a string representation of the function
func (fn *Function) Dump() string {
	var buf bytes.Buffer
	buf.WriteString(fmt.Sprintf("fn %s(", fn.Name))
	for i, param := range fn.Params {
		if i > 0 {
			buf.WriteString(", ")
		}
		buf.WriteString(param.String())
	}
	buf.WriteString(") {\n")

	for _, bb := range fn.Blocks {
		buf.WriteString(fmt.Sprintf("%s:\n", bb.String()))
		for _, instr := range bb.Instructions {
			buf.WriteString(fmt.Sprintf("\t%s\n", instr.Dump()))
		}
	}
	buf.WriteString("}\n")
	return buf.String()
}

// Dump returns a string representation of the instruction
func (instr *Instruction) Dump() string {
	var buf bytes.Buffer
	if instr.Dest != nil {
		buf.WriteString(fmt.Sprintf("%s = ", instr.Dest.String()))
	}
	buf.WriteString(instr.Op.String())
	for i, op := range instr.Operands {
		if i == 0 && instr.Dest == nil {
			buf.WriteString(" ")
		} else {
			buf.WriteString(", ")
		}
		buf.WriteString(op.String())
	}
	return buf.String()
}
