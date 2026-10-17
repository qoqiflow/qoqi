package dump

import (
	"fmt"
	"strings"

	"github.com/qoqiflow/qoqi/internal/vm"
)

// Disassemble converts a Chunk into human-readable assembly text.
func Disassemble(chunk *vm.Chunk) string {
	var sb strings.Builder

	// Build map of which constants are string pool indices
	stringConstIndices := make(map[int]bool)
	for _, instr := range chunk.Code {
		if instr.Op == vm.OpNEWSTR {
			stringConstIndices[int(instr.B)] = true
		}
	}

	// Write constants section
	sb.WriteString(".constants ")
	sb.WriteString(fmt.Sprintf("%d\n", len(chunk.Constants)))
	for i, val := range chunk.Constants {
		// Check if this constant is a string pool index
		if stringConstIndices[i] && val.IsInt() {
			strIdx := int(val.AsInt())
			if strIdx >= 0 && strIdx < len(chunk.StringPool) {
				sb.WriteString(fmt.Sprintf("  %d: %s\n", i, escapeString(chunk.StringPool[strIdx])))
				continue
			}
		}
		sb.WriteString(fmt.Sprintf("  %d: %s\n", i, formatValue(val)))
	}
	sb.WriteString("\n")

	// Write function prototypes (grouped by .func blocks)
	if len(chunk.Prototypes) > 0 {
		for _, proto := range chunk.Prototypes {
			// Write .func header
			if proto.UpvalueCount > 0 {
				sb.WriteString(fmt.Sprintf(".func %s %d %d %d\n", proto.Name, proto.RegisterCount, proto.ArgCount, proto.UpvalueCount))
			} else {
				sb.WriteString(fmt.Sprintf(".func %s %d %d\n", proto.Name, proto.RegisterCount, proto.ArgCount))
			}

			// Write instructions for this function
			endIP := proto.CodeStart + proto.CodeSize
			for i := proto.CodeStart; i < endIP && i < len(chunk.Code); i++ {
				instr := chunk.Code[i]

				// Check for LONG prefix
				if instr.Op == vm.OpLONG {
					if i+2 >= len(chunk.Code) {
						sb.WriteString(fmt.Sprintf("  %04d: LONG (incomplete)\n", i))
						break
					}

					opcodeInstr := chunk.Code[i+1]
					offsetInstr := chunk.Code[i+2]
					offset := vm.DecodeOffset32(offsetInstr)
					targetIP := i + 3 + int(offset)

					sb.WriteString(fmt.Sprintf("  %04d: LONG\n", i))
					sb.WriteString(fmt.Sprintf("  %04d: %s (target: %d, offset: %d)\n", i+1,
						formatLongInstruction(opcodeInstr, targetIP, offset), targetIP, offset))
					sb.WriteString(fmt.Sprintf("  %04d: [offset word]\n", i+2))

					i += 2 // Skip the opcode and offset words
				} else {
					sb.WriteString(fmt.Sprintf("  %s\n", FormatInstruction(instr, chunk)))
				}
			}

			// Write .endfunc footer
			sb.WriteString(".endfunc\n\n")
		}
	} else {
		// Fallback: if no prototypes, write all code in a single block (legacy support)
		sb.WriteString(".code\n")
		for i := 0; i < len(chunk.Code); i++ {
			instr := chunk.Code[i]

			// Check for LONG prefix
			if instr.Op == vm.OpLONG {
				if i+2 >= len(chunk.Code) {
					sb.WriteString(fmt.Sprintf("  %04d: LONG (incomplete)\n", i))
					break
				}

				opcodeInstr := chunk.Code[i+1]
				offsetInstr := chunk.Code[i+2]
				offset := vm.DecodeOffset32(offsetInstr)
				targetIP := i + 3 + int(offset)

				sb.WriteString(fmt.Sprintf("  %04d: LONG\n", i))
				sb.WriteString(fmt.Sprintf("  %04d: %s (target: %d, offset: %d)\n", i+1,
					formatLongInstruction(opcodeInstr, targetIP, offset), targetIP, offset))
				sb.WriteString(fmt.Sprintf("  %04d: [offset word]\n", i+2))

				i += 2 // Skip the opcode and offset words
			} else {
				sb.WriteString(fmt.Sprintf("  %04d: %s\n", i, FormatInstruction(instr, chunk)))
			}
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// escapeString adds quotes and escapes special characters.
func escapeString(s string) string {
	result := strings.Builder{}
	result.Grow(len(s) + 10) // Account for quotes and some escapes
	result.WriteByte('"')

	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			result.WriteString("\\\\")
		case '"':
			result.WriteString("\\\"")
		case '\n':
			result.WriteString("\\n")
		case '\t':
			result.WriteString("\\t")
		case '\r':
			result.WriteString("\\r")
		default:
			result.WriteByte(s[i])
		}
	}

	result.WriteByte('"')
	return result.String()
}

// formatValue converts a Value to its assembly representation.
func formatValue(val vm.Value) string {
	switch {
	case val.IsNumber():
		return fmt.Sprintf("%v", val.AsNumber())
	case val.IsInt():
		return fmt.Sprintf("%di", val.AsInt())
	case val.IsBool():
		if val.AsBool() {
			return "true"
		}
		return "false"
	case val.IsNull():
		return "null"
	case val.IsHeapPtr():
		return fmt.Sprintf("ptr(0x%x)", val.AsHeapPtr())
	default:
		return fmt.Sprintf("unknown(0x%x)", uint64(val))
	}
}

// FormatInstruction converts an instruction to assembly syntax.
func FormatInstruction(instr vm.Instr, chunk *vm.Chunk) string {
	switch instr.Op {
	case vm.OpNOP:
		return "NOP"

	case vm.OpMOV:
		return fmt.Sprintf("MOV r%d r%d", instr.A, instr.B)

	case vm.OpLOADK:
		return fmt.Sprintf("LOADK r%d %d", instr.A, instr.B)

	case vm.OpLOADNULL:
		return fmt.Sprintf("LOADNULL r%d", instr.A)

	case vm.OpLOADBOOL:
		val := "false"
		if instr.B != 0 {
			val = "true"
		}
		return fmt.Sprintf("LOADBOOL r%d %s", instr.A, val)

	case vm.OpLOADI:
		return fmt.Sprintf("LOADI r%d %d", instr.A, instr.B)

	case vm.OpADD:
		return fmt.Sprintf("ADD r%d r%d r%d", instr.A, instr.B, instr.C)

	case vm.OpSUB:
		return fmt.Sprintf("SUB r%d r%d r%d", instr.A, instr.B, instr.C)

	case vm.OpMUL:
		return fmt.Sprintf("MUL r%d r%d r%d", instr.A, instr.B, instr.C)

	case vm.OpDIV:
		return fmt.Sprintf("DIV r%d r%d r%d", instr.A, instr.B, instr.C)

	case vm.OpMOD:
		return fmt.Sprintf("MOD r%d r%d r%d", instr.A, instr.B, instr.C)

	case vm.OpNEG:
		return fmt.Sprintf("NEG r%d r%d", instr.A, instr.B)

	case vm.OpNOT:
		return fmt.Sprintf("NOT r%d r%d", instr.A, instr.B)

	case vm.OpTOINT:
		return fmt.Sprintf("TOINT r%d r%d", instr.A, instr.B)

	case vm.OpTONUM:
		return fmt.Sprintf("TONUM r%d r%d", instr.A, instr.B)

	case vm.OpBAND:
		return fmt.Sprintf("BAND r%d r%d r%d", instr.A, instr.B, instr.C)

	case vm.OpBOR:
		return fmt.Sprintf("BOR r%d r%d r%d", instr.A, instr.B, instr.C)

	case vm.OpBXOR:
		return fmt.Sprintf("BXOR r%d r%d r%d", instr.A, instr.B, instr.C)

	case vm.OpSHL:
		return fmt.Sprintf("SHL r%d r%d r%d", instr.A, instr.B, instr.C)

	case vm.OpSHR:
		return fmt.Sprintf("SHR r%d r%d r%d", instr.A, instr.B, instr.C)

	case vm.OpEQ:
		return fmt.Sprintf("EQ r%d r%d r%d", instr.A, instr.B, instr.C)

	case vm.OpNE:
		return fmt.Sprintf("NE r%d r%d r%d", instr.A, instr.B, instr.C)

	case vm.OpLT:
		return fmt.Sprintf("LT r%d r%d r%d", instr.A, instr.B, instr.C)

	case vm.OpLE:
		return fmt.Sprintf("LE r%d r%d r%d", instr.A, instr.B, instr.C)

	case vm.OpGT:
		return fmt.Sprintf("GT r%d r%d r%d", instr.A, instr.B, instr.C)

	case vm.OpGE:
		return fmt.Sprintf("GE r%d r%d r%d", instr.A, instr.B, instr.C)

	case vm.OpJMP:
		return fmt.Sprintf("JMP %d", instr.A)

	case vm.OpJMPIF:
		return fmt.Sprintf("JMPIF r%d %d", instr.A, instr.B)

	case vm.OpJMPIFNOT:
		return fmt.Sprintf("JMPIFNOT r%d %d", instr.A, instr.B)

	case vm.OpLONG:
		return "LONG"

	case vm.OpJMP32:
		return "JMP32"

	case vm.OpJMPIF32:
		return fmt.Sprintf("JMPIF32 r%d", instr.A)

	case vm.OpJMPIFNOT32:
		return fmt.Sprintf("JMPIFNOT32 r%d", instr.A)

	case vm.OpCALL:
		return fmt.Sprintf("CALL r%d %d", instr.A, instr.B)

	case vm.OpNEWFUNC:
		// Reverse lookup: show function name instead of numeric index
		funcName := "???"
		if int(instr.B) < len(chunk.Prototypes) {
			funcName = chunk.Prototypes[instr.B].Name
		}
		return fmt.Sprintf("NEWFUNC r%d %s", instr.A, funcName)

	case vm.OpNEWEXFUNC:
		// Reverse lookup: show extern function name instead of numeric index
		externName := "unknown"
		if int(instr.B) < len(chunk.ExternNames) {
			externName = chunk.ExternNames[instr.B]
		}
		return fmt.Sprintf("NEWEXFUNC r%d %s", instr.A, externName)

	case vm.OpRET:
		return fmt.Sprintf("RET r%d", instr.A)

	case vm.OpCAPLOCAL:
		return fmt.Sprintf("CAPLOCAL r%d %d r%d", instr.A, instr.B, instr.C)

	case vm.OpCAPUPVAL:
		return fmt.Sprintf("CAPUPVAL r%d %d %d", instr.A, instr.B, instr.C)

	case vm.OpLDCAP:
		return fmt.Sprintf("LDCAP r%d %d", instr.A, instr.B)

	case vm.OpSTCAP:
		return fmt.Sprintf("STCAP %d r%d", instr.A, instr.B)

	case vm.OpNEWSTR:
		return fmt.Sprintf("NEWSTR r%d %d", instr.A, instr.B)

	case vm.OpNEWARRAY:
		return fmt.Sprintf("NEWARRAY r%d %d", instr.A, instr.B)

	case vm.OpNEWOBJ:
		return fmt.Sprintf("NEWOBJ r%d", instr.A)

	case vm.OpGETINDEX:
		return fmt.Sprintf("GETINDEX r%d r%d r%d", instr.A, instr.B, instr.C)

	case vm.OpSETINDEX:
		return fmt.Sprintf("SETINDEX r%d r%d r%d", instr.A, instr.B, instr.C)

	case vm.OpGETFIELD:
		return fmt.Sprintf("GETFIELD r%d r%d %d", instr.A, instr.B, instr.C)

	case vm.OpSETFIELD:
		return fmt.Sprintf("SETFIELD r%d %d r%d", instr.A, instr.B, instr.C)

	case vm.OpARRLEN:
		return fmt.Sprintf("ARRLEN r%d r%d", instr.A, instr.B)

	case vm.OpARRPUSH:
		return fmt.Sprintf("ARRPUSH r%d r%d", instr.A, instr.B)

	case vm.OpHASFIELD:
		return fmt.Sprintf("HASFIELD r%d r%d %d", instr.A, instr.B, instr.C)

	case vm.OpDELFIELD:
		return fmt.Sprintf("DELFIELD r%d r%d %d", instr.A, instr.B, instr.C)

	case vm.OpSETELEM:
		return fmt.Sprintf("SETELEM r%d r%d r%d", instr.A, instr.B, instr.C)

	case vm.OpCLOSEUP:
		return fmt.Sprintf("CLOSEUP r%d", instr.A)

	case vm.OpENDTRY:
		return "ENDTRY"

	case vm.OpCALLK:
		funcName := "???"
		if int(instr.C) < len(chunk.Prototypes) {
			funcName = chunk.Prototypes[instr.C].Name
		}
		return fmt.Sprintf("CALLK r%d %d %s", instr.A, instr.B, funcName)

	case vm.OpCALLX:
		externName := "unknown"
		if int(instr.C) < len(chunk.ExternNames) {
			externName = chunk.ExternNames[instr.C]
		}
		return fmt.Sprintf("CALLX r%d %d %s", instr.A, instr.B, externName)

	case vm.OpJNLT, vm.OpJNLE, vm.OpJNEQ, vm.OpJNNE:
		name, _ := vm.ShortName(instr.Op)
		return fmt.Sprintf("%s r%d r%d %d", name, instr.A, instr.B, instr.C)

	case vm.OpJNLTI, vm.OpJNLEI, vm.OpJNGTI, vm.OpJNGEI, vm.OpJNEQI, vm.OpJNNEI:
		name, _ := vm.ShortName(instr.Op)
		return fmt.Sprintf("%s r%d %d %d", name, instr.A, instr.B, instr.C)

	case vm.OpADDI, vm.OpSUBI, vm.OpADDK, vm.OpSUBK, vm.OpMULK, vm.OpDIVK, vm.OpMODK:
		name, _ := vm.ShortName(instr.Op)
		return fmt.Sprintf("%s r%d r%d %d", name, instr.A, instr.B, instr.C)

	case vm.OpJNLTK, vm.OpJNLEK, vm.OpJNGTK, vm.OpJNGEK, vm.OpJNEQK, vm.OpJNNEK:
		name, _ := vm.ShortName(instr.Op)
		return fmt.Sprintf("%s r%d %d %d", name, instr.A, instr.B, instr.C)

	case vm.OpSWITCH:
		return fmt.Sprintf("SWITCH r%d %d %d", instr.A, instr.B, instr.C)

	case vm.OpCASE:
		return fmt.Sprintf("CASE %d %d", instr.A, instr.B)

	case vm.OpGETMETHOD:
		return fmt.Sprintf("GETMETHOD r%d r%d %d", instr.A, instr.B, instr.C)

	case vm.OpCALLM:
		return fmt.Sprintf("CALLM r%d %d %d", instr.A, instr.B, instr.C)

	case vm.OpONCE:
		return fmt.Sprintf("ONCE r%d %d", instr.A, instr.B)

	case vm.OpISTYPE:
		return fmt.Sprintf("ISTYPE r%d r%d %d", instr.A, instr.B, instr.C)

	case vm.OpJNTYPE:
		return fmt.Sprintf("JNTYPE r%d %d %d", instr.A, instr.B, instr.C)

	case vm.OpRETHROW:
		return "RETHROW"

	case vm.OpGC:
		return "GC"

	default:
		return fmt.Sprintf("UNKNOWN(%d) %d %d %d", instr.Op, instr.A, instr.B, instr.C)
	}
}

// formatLongInstruction formats a long instruction (after LONG prefix).
func formatLongInstruction(instr vm.Instr, targetIP int, offset int32) string {
	switch instr.Op {
	case vm.OpJMP32:
		return fmt.Sprintf("JMP32")
	case vm.OpJMPIF32:
		return fmt.Sprintf("JMPIF32 r%d", instr.A)
	case vm.OpJMPIFNOT32:
		return fmt.Sprintf("JMPIFNOT32 r%d", instr.A)
	case vm.OpTRY32:
		return "TRY32"
	default:
		return fmt.Sprintf("UNKNOWN_LONG(%d)", instr.Op)
	}
}
