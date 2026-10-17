package vm

import "fmt"

// Verify checks that a chunk can be run safely: that no instruction names
// a register its function does not have, a constant, a function or a
// string that is not there, or a place to jump to outside its function.
//
// The instruction loop relies on it. Bytecode from the compiler passes by
// construction; a file written by hand or damaged on the way may not, and
// is refused here with a message rather than run until it goes wrong.
//
// What depends on the values a program computes is not checked here but
// when it runs: that a register holds a function when it is called, an
// array when it is indexed.
func Verify(chunk *Chunk) error {
	if chunk == nil {
		return fmt.Errorf("invalid bytecode: no chunk")
	}
	for i := range chunk.Prototypes {
		if err := verifyFunction(chunk, &chunk.Prototypes[i]); err != nil {
			return fmt.Errorf("invalid bytecode: function '%s': %v", chunk.Prototypes[i].Name, err)
		}
	}
	return nil
}

// Limits on what a function may ask for. The operands that name a register
// are 16 bits wide, so no function can use more registers than that.
const maxRegisters = 1 << 16

func verifyFunction(chunk *Chunk, proto *FunctionPrototype) error {
	start, size := proto.CodeStart, proto.CodeSize
	if start < 0 || size < 0 || start > len(chunk.Code) || size > len(chunk.Code)-start {
		return fmt.Errorf("its code (%d instructions from %d) is outside the %d of the chunk", size, start, len(chunk.Code))
	}
	end := start + size
	if proto.RegisterCount < 1 || proto.RegisterCount > maxRegisters {
		// r0 is where what a call returns is put
		return fmt.Errorf("it declares %d registers, and must have between 1 and %d", proto.RegisterCount, maxRegisters)
	}
	if proto.ArgCount < 0 || proto.ArgCount > proto.RegisterCount {
		return fmt.Errorf("it takes %d arguments but has %d registers", proto.ArgCount, proto.RegisterCount)
	}
	if proto.UpvalueCount < 0 || proto.UpvalueCount > maxRegisters {
		return fmt.Errorf("it captures %d variables", proto.UpvalueCount)
	}

	v := verifier{chunk: chunk, proto: proto}

	// Where instructions start: a long jump takes three words, and the two
	// that follow the first are not instructions to land on
	starts := make([]bool, size)
	for ip := start; ip < end; ip++ {
		starts[ip-start] = true
		switch chunk.Code[ip].Op {
		case OpLONG:
			if ip+2 >= end {
				return fmt.Errorf("instruction %d: LONG is cut short by the end of the function", ip)
			}
			ip += 2
		case OpSWITCH:
			// Its cases are words of its own, not instructions
			cases := int(chunk.Code[ip].B)
			if cases > end-ip-1 {
				return fmt.Errorf("instruction %d: SWITCH with %d cases is cut short by the end of the function", ip, cases)
			}
			ip += cases
		}
	}
	jump := func(ip, target int) error {
		if target < start || target >= end || !starts[target-start] {
			return fmt.Errorf("instruction %d: jump to %d, which is not an instruction of the function", ip, target)
		}
		return nil
	}

	for ip := start; ip < end; ip++ {
		instr := chunk.Code[ip]
		var err error
		switch instr.Op {
		case OpNOP, OpGC, OpENDTRY, OpRETHROW:

		case OpMOV, OpNEG, OpNOT, OpTOINT, OpTONUM, OpARRLEN, OpARRPUSH:
			err = v.registers(instr.A, instr.B)

		case OpLOADK:
			if err = v.registers(instr.A); err == nil {
				err = v.constant(instr.B)
			}

		case OpLOADNULL, OpLOADBOOL, OpLOADI, OpRET, OpCLOSEUP, OpNEWARRAY:
			err = v.registers(instr.A)

		case OpADD, OpSUB, OpMUL, OpDIV, OpMOD,
			OpBAND, OpBOR, OpBXOR, OpSHL, OpSHR,
			OpEQ, OpNE, OpLT, OpLE, OpGT, OpGE,
			OpGETINDEX, OpSETINDEX, OpSETELEM:
			err = v.registers(instr.A, instr.B, instr.C)

		case OpJMP:
			err = jump(ip, int(instr.A))

		case OpJMPIF, OpJMPIFNOT:
			if err = v.registers(instr.A); err == nil {
				err = jump(ip, int(instr.B))
			}

		case OpLONG:
			jumpWord := chunk.Code[ip+1]
			switch jumpWord.Op {
			case OpJMP32, OpTRY32:
			case OpJMPIF32, OpJMPIFNOT32:
				err = v.registers(jumpWord.A)
			default:
				err = fmt.Errorf("LONG is followed by %v, which is not a long jump", jumpWord.Op)
			}
			if err == nil {
				err = jump(ip, ip+3+int(DecodeOffset32(chunk.Code[ip+2])))
			}
			if err == nil {
				ip += 2
				continue
			}

		case OpJMP32, OpJMPIF32, OpJMPIFNOT32, OpTRY32:
			err = fmt.Errorf("%v without LONG before it", instr.Op)

		case OpCALL:
			// The function is in A, its arguments in the registers after
			err = v.call(instr)

		case OpNEWFUNC:
			if err = v.registers(instr.A); err == nil && int(instr.B) >= len(chunk.Prototypes) {
				err = fmt.Errorf("function %d of %d", instr.B, len(chunk.Prototypes))
			}

		case OpNEWEXFUNC:
			if err = v.registers(instr.A); err == nil && int(instr.B) >= len(chunk.ExternNames) {
				err = fmt.Errorf("host function %d of %d", instr.B, len(chunk.ExternNames))
			}

		case OpCAPLOCAL:
			err = v.registers(instr.A, instr.C)

		case OpCAPUPVAL:
			if err = v.registers(instr.A); err == nil {
				err = v.upvalue(instr.C)
			}

		case OpLDCAP:
			if err = v.registers(instr.A); err == nil {
				err = v.upvalue(instr.B)
			}

		case OpSTCAP:
			if err = v.registers(instr.B); err == nil {
				err = v.upvalue(instr.A)
			}

		case OpCALLK:
			if err = v.call(instr); err == nil {
				err = v.calledFunction(instr)
			}

		case OpCALLX:
			if err = v.call(instr); err == nil && int(instr.C) >= len(chunk.ExternNames) {
				err = fmt.Errorf("host function %d of %d", instr.C, len(chunk.ExternNames))
			}

		case OpJNLT, OpJNLE, OpJNEQ, OpJNNE:
			if err = v.registers(instr.A, instr.B); err == nil {
				err = jump(ip, int(instr.C))
			}

		case OpJNLTI, OpJNLEI, OpJNGTI, OpJNGEI, OpJNEQI, OpJNNEI:
			if err = v.registers(instr.A); err == nil {
				err = jump(ip, int(instr.C))
			}

		case OpADDI, OpSUBI:
			err = v.registers(instr.A, instr.B)

		case OpADDK, OpSUBK, OpMULK, OpDIVK, OpMODK:
			if err = v.registers(instr.A, instr.B); err == nil {
				err = v.number(instr.C)
			}

		case OpJNLTK, OpJNLEK, OpJNGTK, OpJNGEK, OpJNEQK, OpJNNEK:
			if err = v.registers(instr.A); err == nil {
				err = v.number(instr.B)
			}
			if err == nil {
				err = jump(ip, int(instr.C))
			}

		case OpSWITCH:
			if err = v.registers(instr.A); err == nil {
				err = jump(ip, int(instr.C))
			}
			for i := 1; err == nil && i <= int(instr.B); i++ {
				option := chunk.Code[ip+i]
				if option.Op != OpCASE {
					err = fmt.Errorf("SWITCH is followed by %v where a CASE is expected", option.Op)
				} else if err = v.pooledString(option.A); err == nil {
					err = jump(ip, int(option.B))
				}
			}
			if err == nil {
				ip += int(instr.B)
				continue
			}

		case OpCASE:
			err = fmt.Errorf("CASE without SWITCH before it")

		case OpONCE:
			if err = v.registers(instr.A); err == nil {
				switch {
				case int(instr.B) >= len(chunk.Prototypes):
					err = fmt.Errorf("function %d of %d", instr.B, len(chunk.Prototypes))
				case chunk.Prototypes[instr.B].ArgCount != 0 || chunk.Prototypes[instr.B].UpvalueCount != 0:
					err = fmt.Errorf("the function of a constant takes nothing and captures nothing")
				}
			}

		case OpISTYPE, OpGETMETHOD:
			if err = v.registers(instr.A, instr.B); err == nil {
				err = v.pooledString(instr.C)
			}

		case OpCALLM:
			// The value the method is found on is the first argument
			if err = v.call(instr); err == nil && instr.B == 0 {
				err = fmt.Errorf("a call of a method without the value it is a method of")
			}
			if err == nil {
				err = v.pooledString(instr.C)
			}

		case OpJNTYPE:
			if err = v.registers(instr.A); err == nil {
				err = v.pooledString(instr.B)
			}
			if err == nil {
				err = jump(ip, int(instr.C))
			}

		case OpNEWSTR:
			if err = v.registers(instr.A); err == nil {
				err = v.pooledString(instr.B)
			}

		case OpNEWOBJ:
			err = v.registers(instr.A)
			if kind := instr.C & ObjectKindMask; err == nil && (kind == ObjectIsNamed || kind == ObjectIsVariant) {
				err = v.pooledString(instr.B)
			}

		case OpGETFIELD, OpHASFIELD, OpDELFIELD:
			if err = v.registers(instr.A, instr.B); err == nil {
				err = v.fieldName(instr.C)
			}

		case OpSETFIELD:
			if err = v.registers(instr.A, instr.C); err == nil {
				err = v.fieldName(instr.B)
			}

		default:
			err = fmt.Errorf("unknown instruction %d", instr.Op)
		}
		if err != nil {
			return fmt.Errorf("instruction %d: %v", ip, err)
		}
	}

	// A function must not run off its end into whatever follows it
	if size == 0 {
		return fmt.Errorf("it has no instructions")
	}
	last := end - 1
	for last > start && !starts[last-start] {
		last--
	}
	switch op := chunk.Code[last].Op; {
	case op == OpRET, op == OpJMP, op == OpRETHROW, op == OpSWITCH:
	case op == OpLONG && chunk.Code[last+1].Op == OpJMP32:
	case end == len(chunk.Code):
		// Nothing follows: the program ends there
	default:
		return fmt.Errorf("it does not end with a return or a jump")
	}
	return nil
}

type verifier struct {
	chunk *Chunk
	proto *FunctionPrototype
}

func (v *verifier) registers(registers ...uint16) error {
	for _, r := range registers {
		if int(r) >= v.proto.RegisterCount {
			return fmt.Errorf("register r%d of a function that has %d", r, v.proto.RegisterCount)
		}
	}
	return nil
}

// call checks that the arguments of a call are registers of the function.
func (v *verifier) call(instr Instr) error {
	if err := v.registers(instr.A); err != nil {
		return err
	}
	if int(instr.A)+int(instr.B) >= v.proto.RegisterCount {
		return fmt.Errorf("a call with %d arguments from r%d reaches past the %d registers", instr.B, instr.A, v.proto.RegisterCount)
	}
	return nil
}

// calledFunction checks a call of a function by its number: there is such
// a function, it captures nothing (one that does is a value of its own
// each time it is made, and is called as a value), and it takes the
// arguments it is given. The instruction loop does not look again.
func (v *verifier) calledFunction(instr Instr) error {
	if int(instr.C) >= len(v.chunk.Prototypes) {
		return fmt.Errorf("function %d of %d", instr.C, len(v.chunk.Prototypes))
	}
	callee := &v.chunk.Prototypes[instr.C]
	if callee.UpvalueCount != 0 {
		return fmt.Errorf("function '%s' captures variables, and cannot be called by its number", callee.Name)
	}
	if callee.ArgCount != int(instr.B) {
		return fmt.Errorf("function '%s' expects %d arguments, got %d", callee.Name, callee.ArgCount, instr.B)
	}
	return nil
}

func (v *verifier) constant(index uint16) error {
	if int(index) >= len(v.chunk.Constants) {
		return fmt.Errorf("constant %d of %d", index, len(v.chunk.Constants))
	}
	return nil
}

// number checks a constant an instruction takes as a number: it must be
// an Int or a Float, which the instruction is written for.
func (v *verifier) number(index uint16) error {
	if err := v.constant(index); err != nil {
		return err
	}
	if k := v.chunk.Constants[index]; !k.IsInt() && !k.IsNumber() {
		return fmt.Errorf("constant %d is not a number", index)
	}
	return nil
}

func (v *verifier) upvalue(index uint16) error {
	if int(index) >= v.proto.UpvalueCount {
		return fmt.Errorf("captured variable %d of a function that captures %d", index, v.proto.UpvalueCount)
	}
	return nil
}

// pooledString checks a constant that names a string of the pool.
func (v *verifier) pooledString(index uint16) error {
	if err := v.constant(index); err != nil {
		return err
	}
	constant := v.chunk.Constants[index]
	if !constant.IsInt() {
		return fmt.Errorf("constant %d is not the number of a string", index)
	}
	if n := constant.AsInt(); n < 0 || n >= int64(len(v.chunk.StringPool)) {
		return fmt.Errorf("string %d of %d", n, len(v.chunk.StringPool))
	}
	return nil
}

// fieldName checks the constant that names a field: a string of the pool
// or, in a chunk built in memory, a string made beforehand.
func (v *verifier) fieldName(index uint16) error {
	if err := v.constant(index); err != nil {
		return err
	}
	if _, ok := stringValue(v.chunk.Constants[index]); ok {
		return nil
	}
	return v.pooledString(index)
}
