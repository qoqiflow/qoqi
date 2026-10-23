package vm

import (
	"encoding/binary"
	"fmt"
	"math"
	"sort"
)

const (
	// Magic number "LUMN" in ASCII
	bytecodeFileMagic uint32 = 0x4C554D4E
	// Current bytecode format version
	// 2: value tags moved to bits 48-51; prototypes carry a signature
	// 3: null has its own tag, so float infinity is no longer read as null
	// 4: instruction operands are 16 bits wide
	// 5: debug information is written as tables, each text once
	// 6: the priv fields of each struct
	// 7: prototypes say whether they are steps of a flow
	bytecodeVersion uint32 = 11

	// Bytes per instruction in a file: opcode, then three 16-bit operands
	instrSize = 1 + 3*2
)

// byteWriter writes the parts of a bytecode file in order.
type byteWriter struct {
	data []byte
}

func (w *byteWriter) uint32(n int) {
	w.data = binary.LittleEndian.AppendUint32(w.data, uint32(n))
}

func (w *byteWriter) string(s string) {
	w.uint32(len(s))
	w.data = append(w.data, s...)
}

// SerializeChunk converts a Chunk to binary bytecode format.
//
// Format, every number 4 bytes unless said otherwise:
//
//	magic, version
//	constants:  count, then 8 bytes each
//	code:       count, then 7 bytes each (opcode, three 16-bit operands)
//	prototypes: count, then name, code start, code size, registers,
//	            arguments, captured variables, signature, whether it is a step
//	externs:    count, then a name each
//	strings:    count, then a text each
//	private:    count, then for each struct by name, its name, a count
//	            and the names of its priv fields
//	debug info: see writeDebugInfo
//
// A text is its length in bytes followed by the bytes. The same chunk is
// always written as the same bytes.
func SerializeChunk(chunk *Chunk) ([]byte, error) {
	w, err := writeCode(chunk)
	if err != nil {
		return nil, err
	}
	writeDebugInfo(w, chunk)
	return w.data, nil
}

// writeCode writes all of a chunk but where its instructions come from
// in the source: what it does, and what the runtime reads of it.
func writeCode(chunk *Chunk) (*byteWriter, error) {
	if chunk == nil {
		return nil, fmt.Errorf("cannot serialize nil chunk")
	}
	w := &byteWriter{data: make([]byte, 0, 64+len(chunk.Constants)*8+len(chunk.Code)*(instrSize+4))}

	w.uint32(int(bytecodeFileMagic))
	w.uint32(int(bytecodeVersion))

	w.uint32(len(chunk.Constants))
	for _, val := range chunk.Constants {
		w.data = binary.LittleEndian.AppendUint64(w.data, uint64(val))
	}

	w.uint32(len(chunk.Code))
	for _, instr := range chunk.Code {
		w.data = append(w.data, byte(instr.Op))
		w.data = binary.LittleEndian.AppendUint16(w.data, instr.A)
		w.data = binary.LittleEndian.AppendUint16(w.data, instr.B)
		w.data = binary.LittleEndian.AppendUint16(w.data, instr.C)
	}

	w.uint32(len(chunk.Prototypes))
	for _, proto := range chunk.Prototypes {
		w.string(proto.Name)
		w.uint32(proto.CodeStart)
		w.uint32(proto.CodeSize)
		w.uint32(proto.RegisterCount)
		w.uint32(proto.ArgCount)
		w.uint32(proto.UpvalueCount)
		w.string(proto.Signature)
		step := 0
		if proto.Step {
			step = 1
		}
		if proto.Once {
			step |= 2
		}
		if proto.Migrate {
			step |= 4
		}
		if proto.Listens {
			step |= 8
		}
		if proto.ByQueue {
			step |= 16
		}
		w.uint32(step)
		w.uint32(len(proto.Params))
		for _, name := range proto.Params {
			w.string(name)
		}
		w.uint32(len(proto.Inboxes))
		for _, inbox := range proto.Inboxes {
			w.string(inbox.Name)
			w.uint32(inbox.Capacity)
			w.string(inbox.Message)
		}
		w.uint32(len(proto.Stages))
		for _, stage := range proto.Stages {
			w.string(stage.Name)
			w.uint32(stage.At)
			w.uint32(len(stage.Vars))
			for _, v := range stage.Vars {
				w.string(v.Name)
				w.uint32(v.Reg)
			}
			names := make([]string, 0, len(stage.Shape))
			for name := range stage.Shape {
				names = append(names, name)
			}
			sort.Strings(names)
			w.uint32(len(names))
			for _, name := range names {
				w.string(name)
				w.string(stage.Shape[name])
			}
		}
	}

	w.uint32(len(chunk.ExternNames))
	for _, name := range chunk.ExternNames {
		w.string(name)
	}

	w.uint32(len(chunk.StringPool))
	for _, str := range chunk.StringPool {
		w.string(str)
	}

	owners := make([]string, 0, len(chunk.Private))
	for owner := range chunk.Private {
		owners = append(owners, owner)
	}
	sort.Strings(owners)
	w.uint32(len(owners))
	for _, owner := range owners {
		w.string(owner)
		w.uint32(len(chunk.Private[owner]))
		for _, field := range chunk.Private[owner] {
			w.string(field)
		}
	}

	return w, nil
}

// Debug information says, for an instruction, where in the source it comes
// from: what an error message shows. Most of it is the same from one
// instruction to the next, a statement being several instructions and a
// line several statements, so it is written as three tables, each thing
// once:
//
//	texts:   count, then a text each: the file paths, the source lines,
//	         the function names
//	places:  count, then seven numbers each: file, line, column, source
//	         line, function, description, notes (the texts by their number)
//	runs:    count, then three numbers each: the first instruction, how
//	         many instructions in a row, and the place they all come from
//
// Read back, it is what it was: every instruction has the place it had.
func writeDebugInfo(w *byteWriter, chunk *Chunk) {
	var texts []string
	textIndex := map[string]int{}
	text := func(s string) int {
		idx, ok := textIndex[s]
		if !ok {
			idx = len(texts)
			texts = append(texts, s)
			textIndex[s] = idx
		}
		return idx
	}

	type run struct{ start, length, place int }
	var places []DebugInfo
	placeIndex := map[DebugInfo]int{}
	var runs []run

	for ip := range chunk.Code {
		info := chunk.DebugInfo[ip]
		if info == nil {
			continue
		}
		place, ok := placeIndex[*info]
		if !ok {
			place = len(places)
			places = append(places, *info)
			placeIndex[*info] = place
		}
		if n := len(runs); n > 0 && runs[n-1].place == place && runs[n-1].start+runs[n-1].length == ip {
			runs[n-1].length++
			continue
		}
		runs = append(runs, run{start: ip, length: 1, place: place})
	}

	// The texts are numbered as the places name them, then written first:
	// a reader has them all before it meets a number that refers to one
	numbered := make([][7]int, len(places))
	for i, info := range places {
		numbered[i] = [7]int{text(info.FilePath), info.Line, info.Column, text(info.SourceLine), text(info.FuncName), text(info.Description), text(info.Notes)}
	}

	w.uint32(len(texts))
	for _, s := range texts {
		w.string(s)
	}
	w.uint32(len(numbered))
	for _, fields := range numbered {
		for _, field := range fields {
			w.uint32(field)
		}
	}
	w.uint32(len(runs))
	for _, r := range runs {
		w.uint32(r.start)
		w.uint32(r.length)
		w.uint32(r.place)
	}
}

// readDebugInfo reads what writeDebugInfo wrote.
func readDebugInfo(r *byteReader, chunk *Chunk) error {
	textCount, err := r.count("debug texts", 4)
	if err != nil {
		return err
	}
	texts := make([]string, textCount)
	for i := range texts {
		if texts[i], err = r.string("debug text"); err != nil {
			return err
		}
	}
	text := func(n uint32) (string, error) {
		if int(n) >= len(texts) {
			return "", fmt.Errorf("invalid bytecode: debug text %d of %d", n, len(texts))
		}
		return texts[n], nil
	}

	placeCount, err := r.count("debug places", 7*4)
	if err != nil {
		return err
	}
	places := make([]*DebugInfo, placeCount)
	for i := range places {
		var fields [7]uint32
		for j := range fields {
			fields[j], _ = r.uint32("debug place")
		}
		if fields[1] > math.MaxInt32 || fields[2] > math.MaxInt32 {
			return fmt.Errorf("invalid bytecode: debug place %d is at line %d, column %d", i, fields[1], fields[2])
		}
		info := &DebugInfo{Line: int(fields[1]), Column: int(fields[2])}
		for _, part := range []struct {
			into *string
			from uint32
		}{{&info.FilePath, fields[0]}, {&info.SourceLine, fields[3]}, {&info.FuncName, fields[4]}, {&info.Description, fields[5]}, {&info.Notes, fields[6]}} {
			if *part.into, err = text(part.from); err != nil {
				return err
			}
		}
		places[i] = info
	}

	runCount, err := r.count("debug runs", 3*4)
	if err != nil {
		return err
	}
	for i := 0; i < runCount; i++ {
		start, _ := r.uint32("debug run")
		length, _ := r.uint32("debug run")
		place, _ := r.uint32("debug run")
		if int(place) >= len(places) {
			return fmt.Errorf("invalid bytecode: debug place %d of %d", place, len(places))
		}
		if uint64(start)+uint64(length) > uint64(len(chunk.Code)) {
			return fmt.Errorf("invalid bytecode: debug info for instructions %d to %d of %d", start, uint64(start)+uint64(length), len(chunk.Code))
		}
		// Instructions of one place share its record
		for ip := int(start); ip < int(start)+int(length); ip++ {
			chunk.DebugInfo[ip] = places[place]
		}
	}
	return nil
}

// byteReader reads the parts of a bytecode file in order. Nothing in a
// file is trusted: every length is checked against what is left of the
// file before anything is read or made to hold it, so that a damaged file
// is refused with a message instead of asking for memory it names but
// does not have.
type byteReader struct {
	data   []byte
	offset int
}

func (r *byteReader) left() int {
	return len(r.data) - r.offset
}

func (r *byteReader) uint32(what string) (uint32, error) {
	if r.left() < 4 {
		return 0, fmt.Errorf("bytecode truncated: cannot read %s", what)
	}
	v := binary.LittleEndian.Uint32(r.data[r.offset:])
	r.offset += 4
	return v, nil
}

// count reads a number of items that each take at least size bytes.
func (r *byteReader) count(what string, size int) (int, error) {
	n, err := r.uint32(what + " count")
	if err != nil {
		return 0, err
	}
	if int64(n)*int64(size) > int64(r.left()) {
		return 0, fmt.Errorf("bytecode truncated: cannot read %d %s", n, what)
	}
	return int(n), nil
}

func (r *byteReader) string(what string) (string, error) {
	n, err := r.uint32(what + " length")
	if err != nil {
		return "", err
	}
	if int64(n) > int64(r.left()) {
		return "", fmt.Errorf("bytecode truncated: cannot read %s", what)
	}
	s := string(r.data[r.offset : r.offset+int(n)])
	r.offset += int(n)
	return s, nil
}

// DeserializeChunk converts binary bytecode to a Chunk.
//
// It checks that the file is whole and well formed, not that the program
// in it makes sense: that is Verify, which Exec applies to every chunk.
func DeserializeChunk(data []byte) (*Chunk, error) {
	if len(data) < 8 {
		return nil, fmt.Errorf("bytecode too short: minimum 8 bytes required")
	}
	r := &byteReader{data: data}

	if magic, _ := r.uint32("magic number"); magic != bytecodeFileMagic {
		return nil, fmt.Errorf("invalid magic number: expected 0x%X, got 0x%X", bytecodeFileMagic, magic)
	}
	if version, _ := r.uint32("version"); version != bytecodeVersion {
		return nil, fmt.Errorf("unsupported bytecode version: expected %d, got %d", bytecodeVersion, version)
	}

	chunk := NewChunk()

	// Constant pool
	constCount, err := r.count("constants", 8)
	if err != nil {
		return nil, err
	}
	chunk.Constants = make([]Value, constCount)
	for i := range chunk.Constants {
		constant := Value(binary.LittleEndian.Uint64(r.data[r.offset:]))
		r.offset += 8
		if constant.IsHeapPtr() {
			// An address means something in the program that wrote it,
			// and nothing here
			return nil, fmt.Errorf("invalid bytecode: constant %d is a pointer", i)
		}
		chunk.Constants[i] = constant
	}

	// Code
	codeCount, err := r.count("instructions", instrSize)
	if err != nil {
		return nil, err
	}
	chunk.Code = make([]Instr, codeCount)
	for i := range chunk.Code {
		at := r.data[r.offset:]
		chunk.Code[i] = Instr{
			Op: OpCode(at[0]),
			A:  binary.LittleEndian.Uint16(at[1:]),
			B:  binary.LittleEndian.Uint16(at[3:]),
			C:  binary.LittleEndian.Uint16(at[5:]),
		}
		r.offset += instrSize
	}

	// Function prototypes: a name, five numbers and a signature each
	protoCount, err := r.count("prototypes", 4+20+4)
	if err != nil {
		return nil, err
	}
	chunk.Prototypes = make([]FunctionPrototype, protoCount)
	for i := range chunk.Prototypes {
		proto := &chunk.Prototypes[i]
		if proto.Name, err = r.string("function name"); err != nil {
			return nil, err
		}
		if r.left() < 20 {
			return nil, fmt.Errorf("bytecode truncated: cannot read function metadata")
		}
		for _, field := range []*int{&proto.CodeStart, &proto.CodeSize, &proto.RegisterCount, &proto.ArgCount, &proto.UpvalueCount} {
			n, _ := r.uint32("function metadata")
			if n > math.MaxInt32 {
				return nil, fmt.Errorf("invalid bytecode: function '%s' has a size of %d", proto.Name, n)
			}
			*field = int(n)
		}
		if proto.Signature, err = r.string("function signature"); err != nil {
			return nil, err
		}
		step, err := r.uint32("whether a function is a step")
		if err != nil {
			return nil, err
		}
		proto.Step, proto.Once, proto.Migrate, proto.Listens = step&1 != 0, step&2 != 0, step&4 != 0, step&8 != 0
		proto.ByQueue = step&16 != 0
		params, err := r.count("parameters", 4)
		if err != nil {
			return nil, err
		}
		for range params {
			name, err := r.string("parameter")
			if err != nil {
				return nil, err
			}
			proto.Params = append(proto.Params, name)
		}
		inboxes, err := r.count("inboxes", 8)
		if err != nil {
			return nil, err
		}
		for range inboxes {
			var inbox Inbox
			if inbox.Name, err = r.string("inbox"); err != nil {
				return nil, err
			}
			capacity, err := r.uint32("inbox")
			if err != nil {
				return nil, err
			}
			inbox.Capacity = int(capacity)
			if inbox.Message, err = r.string("inbox"); err != nil {
				return nil, err
			}
			proto.Inboxes = append(proto.Inboxes, inbox)
		}
		stages, err := r.count("stages", 12)
		if err != nil {
			return nil, err
		}
		for range stages {
			var stage Stage
			if stage.Name, err = r.string("stage name"); err != nil {
				return nil, err
			}
			at, err := r.uint32("stage")
			if err != nil {
				return nil, err
			}
			stage.At = int(at)
			vars, err := r.count("stage variables", 8)
			if err != nil {
				return nil, err
			}
			for range vars {
				var v StageVar
				if v.Name, err = r.string("stage variable"); err != nil {
					return nil, err
				}
				reg, err := r.uint32("stage variable")
				if err != nil {
					return nil, err
				}
				v.Reg = int(reg)
				stage.Vars = append(stage.Vars, v)
			}
			shaped, err := r.count("stage shape", 8)
			if err != nil {
				return nil, err
			}
			if shaped > 0 {
				stage.Shape = make(map[string]string, shaped)
			}
			for range shaped {
				name, err := r.string("stage shape")
				if err != nil {
					return nil, err
				}
				if stage.Shape[name], err = r.string("stage shape"); err != nil {
					return nil, err
				}
			}
			proto.Stages = append(proto.Stages, stage)
		}
	}

	// Extern names
	externCount, err := r.count("extern names", 4)
	if err != nil {
		return nil, err
	}
	chunk.ExternNames = make([]string, externCount)
	for i := range chunk.ExternNames {
		if chunk.ExternNames[i], err = r.string("extern name"); err != nil {
			return nil, err
		}
	}

	// String pool
	stringCount, err := r.count("strings", 4)
	if err != nil {
		return nil, err
	}
	chunk.StringPool = make([]string, stringCount)
	for i := range chunk.StringPool {
		if chunk.StringPool[i], err = r.string("string"); err != nil {
			return nil, err
		}
	}

	// The priv fields of each struct
	ownerCount, err := r.count("structs with priv fields", 8)
	if err != nil {
		return nil, err
	}
	for i := 0; i < ownerCount; i++ {
		owner, err := r.string("struct name")
		if err != nil {
			return nil, err
		}
		fieldCount, err := r.count("priv fields", 4)
		if err != nil {
			return nil, err
		}
		if chunk.Private == nil {
			chunk.Private = make(map[string][]string)
		}
		for j := 0; j < fieldCount; j++ {
			field, err := r.string("priv field")
			if err != nil {
				return nil, err
			}
			chunk.Private[owner] = append(chunk.Private[owner], field)
		}
	}

	if err := readDebugInfo(r, chunk); err != nil {
		return nil, err
	}
	if r.left() != 0 {
		return nil, fmt.Errorf("invalid bytecode: %d bytes after the end of the program", r.left())
	}

	return chunk, nil
}
