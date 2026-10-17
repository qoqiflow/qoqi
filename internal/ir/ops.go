package ir

// OpType defines the type of IR operation
type OpType int

const (
	OpNop OpType = iota

	// Arithmetic & Logic
	OpAdd // Dest = Src1 + Src2
	OpSub // Dest = Src1 - Src2
	OpMul // Dest = Src1 * Src2
	OpDiv // Dest = Src1 / Src2
	OpMod // Dest = Src1 % Src2

	// On the bits of whole numbers
	OpBitAnd // Dest = Src1 & Src2
	OpBitOr  // Dest = Src1 | Src2
	OpBitXor // Dest = Src1 ^ Src2
	OpShl    // Dest = Src1 << Src2
	OpShr    // Dest = Src1 >> Src2

	OpNeg // Dest = -Src1
	OpNot // Dest = !Src1
	OpEq  // Dest = Src1 == Src2
	OpNeq // Dest = Src1 != Src2
	OpLt  // Dest = Src1 < Src2
	OpLte // Dest = Src1 <= Src2
	OpGt  // Dest = Src1 > Src2
	OpGte // Dest = Src1 >= Src2
	OpAnd // Dest = Src1 && Src2
	OpOr  // Dest = Src1 || Src2

	// Control Flow
	OpJmp       // Jump to Block
	OpBr        // Branch if Src1 then Block1 else Block2
	OpRet       // Return Src1 (optional)
	OpCall      // Dest = Call Func(Args...)
	OpCallTrait // Dest = CallTrait Receiver, TraitName, MethodName, Args...

	// Cleanup (defer). Between a Try and its EndTry, a failure does not
	// leave the function at once: the handler block runs first, and passes
	// the failure on with Rethrow when it is done.
	OpTry     // Try Handler, Body: from here on a failure goes to Handler; go on at Body
	OpEndTry  // EndTry: the innermost Try of this function no longer applies
	OpRethrow // Rethrow: pass on the failure the handler was run for

	// Memory & Variables
	OpMov // Dest = Src1

	// Structures
	OpNewStruct  // Dest = NewStruct TypeName, Fields...
	OpNewVariant // Dest = NewVariant "Enum.Variant": a value of an enum; its values are set as fields "0", "1", ...
	OpGetField   // Dest = Src1.Field
	OpSetField   // Src1.Field = Src2
	OpGetMethod  // Dest = the method of that name of the type Src1 is a value of, found when the program runs

	// Arrays
	OpNewArray // Dest = NewArray Type, Size
	OpGetIndex // Dest = Src1[Src2]
	OpSetIndex // Src1[Src2] = Src3
	OpLen      // Dest = length of Src1 (array or string)
	OpOnce     // Dest = what the function Src1 returns, which is only run the first time
	OpPush     // Append Src2 to array Src1

	// Closures
	// A capture is either a Register (a local of the enclosing function) or
	// a Constant of type "Upvalue" (an upvalue index of the enclosing function).
	OpMakeClosure   // Dest = MakeClosure Func, Captures...
	OpCloseUpvalues // CloseUpvalues Registers...: closures stop following them
	OpLoadUpvalue   // Dest = LoadUpvalue Index
	OpStoreUpvalue  // StoreUpvalue Index, Value

	// OpStage Name, (VarName, Register)...: a stage of a flow, right after
	// the call that passes it. Nothing is done; the variables are alive
	// there, and the function says where each one is
	OpStage
)

func (op OpType) String() string {
	switch op {
	case OpNop:
		return "NOP"
	case OpAdd:
		return "ADD"
	case OpSub:
		return "SUB"
	case OpMul:
		return "MUL"
	case OpDiv:
		return "DIV"
	case OpMod:
		return "MOD"
	case OpBitAnd:
		return "BIT_AND"
	case OpBitOr:
		return "BIT_OR"
	case OpBitXor:
		return "BIT_XOR"
	case OpShl:
		return "SHL"
	case OpShr:
		return "SHR"
	case OpNeg:
		return "NEG"
	case OpNot:
		return "NOT"
	case OpEq:
		return "EQ"
	case OpNeq:
		return "NEQ"
	case OpLt:
		return "LT"
	case OpLte:
		return "LTE"
	case OpGt:
		return "GT"
	case OpGte:
		return "GTE"
	case OpAnd:
		return "AND"
	case OpOr:
		return "OR"
	case OpJmp:
		return "JMP"
	case OpBr:
		return "BR"
	case OpRet:
		return "RET"
	case OpCall:
		return "CALL"
	case OpCallTrait:
		return "CALL_TRAIT"
	case OpTry:
		return "TRY"
	case OpEndTry:
		return "END_TRY"
	case OpRethrow:
		return "RETHROW"
	case OpMov:
		return "MOV"
	case OpNewStruct:
		return "NEW_STRUCT"
	case OpNewVariant:
		return "NEW_VARIANT"
	case OpGetField:
		return "GET_FIELD"
	case OpSetField:
		return "SET_FIELD"
	case OpGetMethod:
		return "GET_METHOD"
	case OpNewArray:
		return "NEW_ARRAY"
	case OpGetIndex:
		return "GET_INDEX"
	case OpSetIndex:
		return "SET_INDEX"
	case OpOnce:
		return "ONCE"
	case OpLen:
		return "LEN"
	case OpPush:
		return "PUSH"
	case OpMakeClosure:
		return "MAKE_CLOSURE"
	case OpLoadUpvalue:
		return "LOAD_UPVALUE"
	case OpStoreUpvalue:
		return "STORE_UPVALUE"
	case OpCloseUpvalues:
		return "CLOSE_UPVALUES"
	case OpStage:
		return "STAGE"
	default:
		return "UNKNOWN"
	}
}
