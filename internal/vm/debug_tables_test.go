package vm

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// Debug information is written as tables, each text once. Read back, it
// is what it was.

// located makes a program of n instructions over a few lines of two files,
// several instructions to a place and several places to a line.
func located(n int) *Chunk {
	chunk := NewChunk()
	for ip := 0; ip < n-1; ip++ {
		chunk.AddInstruction(NewInstr(OpLOADI, 0, uint16(ip%100), 0))
	}
	chunk.AddInstruction(NewInstr(OpRET, 0, 0, 0))
	chunk.Prototypes = []FunctionPrototype{{Name: "main", CodeSize: n, RegisterCount: 1}}

	for ip := 0; ip < n; ip++ {
		if ip%11 == 7 {
			continue // Some instructions come from nowhere
		}
		statement := ip / 4
		file := "main.qi"
		if statement%5 == 4 {
			file = "<std>/arrays.qi"
		}
		line := statement/3 + 1
		chunk.DebugInfo[ip] = &DebugInfo{
			FilePath:   file,
			Line:       line,
			Column:     statement%3*8 + 5,
			SourceLine: fmt.Sprintf("    let v%d = compute(%d) + étoile;", line, line),
			FuncName:   fmt.Sprintf("function%d", statement/40),
		}
	}
	return chunk
}

func TestDebugInfoIsReadBackAsItWasWritten(t *testing.T) {
	chunk := located(600)
	// The fields no compiler fills are kept all the same
	chunk.DebugInfo[3] = &DebugInfo{FilePath: "a.lasm", Line: 9, Column: 1, SourceLine: "MOV r0 r1", FuncName: "f", Description: "copies", Notes: "by hand"}

	data, err := SerializeChunk(chunk)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := DeserializeChunk(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.DebugInfo) != len(chunk.DebugInfo) {
		t.Fatalf("%d instructions had a place, %d have one now", len(chunk.DebugInfo), len(loaded.DebugInfo))
	}
	for ip, want := range chunk.DebugInfo {
		got := loaded.GetDebugInfo(ip)
		if got == nil || !reflect.DeepEqual(*got, *want) {
			t.Fatalf("instruction %d: read back %+v, written %+v", ip, got, want)
		}
	}
	if loaded.GetDebugInfo(7) != nil {
		t.Error("an instruction from nowhere got a place")
	}

	// The same chunk is the same bytes, however often: the order things
	// are kept in memory does not show
	for round := 0; round < 5; round++ {
		again, _ := SerializeChunk(chunk)
		if !bytes.Equal(again, data) {
			t.Fatal("the same chunk was written as other bytes")
		}
	}
	if rewritten, _ := SerializeChunk(loaded); !bytes.Equal(rewritten, data) {
		t.Error("read back and written again, the bytes differ")
	}
}

// What the tables are for: a source line is in the file once, not once
// per instruction compiled from it.
func TestDebugInfoTakesLittleRoom(t *testing.T) {
	chunk := located(4000)
	data, _ := SerializeChunk(chunk)
	bare, _ := SerializeChunk(chunk.Stripped())

	line := chunk.DebugInfo[0].SourceLine
	if n := bytes.Count(data, []byte(line)); n != 1 {
		t.Errorf("a source line is written %d times", n)
	}
	if n := bytes.Count(data, []byte("<std>/arrays.qi")); n != 1 {
		t.Errorf("a file path is written %d times", n)
	}
	perInstruction := float64(len(data)-len(bare)) / float64(len(chunk.Code))
	if perInstruction > 16 {
		t.Errorf("debug information takes %.1f bytes per instruction", perInstruction)
	}

	// Without it the program is the same, and says less when it fails
	if len(chunk.Stripped().DebugInfo) != 0 || len(chunk.DebugInfo) == 0 {
		t.Error("stripping changed the chunk it was asked of, or kept the information")
	}
	stripped, err := DeserializeChunk(bare)
	if err != nil || !reflect.DeepEqual(stripped.Code, chunk.Code) {
		t.Errorf("the stripped program is not the same program: %v", err)
	}
}

func TestDamagedDebugTablesAreRefused(t *testing.T) {
	chunk := located(40)
	data, _ := SerializeChunk(chunk)
	bare, _ := SerializeChunk(chunk.Stripped())
	// The tables are the end of the file: where they start is where a
	// file without them has its three empty ones
	tables := len(bare) - 12

	texts := int(binary.LittleEndian.Uint32(data[tables:]))
	afterTexts := tables + 4
	for i := 0; i < texts; i++ {
		afterTexts += 4 + int(binary.LittleEndian.Uint32(data[afterTexts:]))
	}
	places := int(binary.LittleEndian.Uint32(data[afterTexts:]))
	firstPlace := afterTexts + 4
	runs := firstPlace + places*28
	firstRun := runs + 4

	damage := map[string]struct {
		at    int
		value uint32
		want  string
	}{
		"a text that is not there":        {firstPlace, uint32(texts), "debug text"},
		"a function that is not there":    {firstPlace + 16, 0xFFFF, "debug text"},
		"a line beyond any file":          {firstPlace + 4, 0xFFFFFFFF, "line"},
		"a place that is not there":       {firstRun + 8, uint32(places), "debug place"},
		"instructions past the program":   {firstRun + 4, 41, "instructions"},
		"a run that starts past the end":  {firstRun, 1 << 30, "instructions"},
		"more texts than the file holds":  {tables, 0xFFFFFFF0, "truncated"},
		"more places than the file holds": {afterTexts, 0x0FFFFFFF, "truncated"},
		"more runs than the file holds":   {runs, 0x0FFFFFFF, "truncated"},
	}
	for name, d := range damage {
		t.Run(name, func(t *testing.T) {
			damaged := append([]byte(nil), data...)
			binary.LittleEndian.PutUint32(damaged[d.at:], d.value)
			if _, err := DeserializeChunk(damaged); err == nil || !strings.Contains(err.Error(), d.want) {
				t.Errorf("expected an error with %q, got %v", d.want, err)
			}
		})
	}

	// Bytes after the end are not part of a program
	if _, err := DeserializeChunk(append(append([]byte(nil), data...), 0, 0, 0)); err == nil || !strings.Contains(err.Error(), "after the end") {
		t.Errorf("expected what follows the program to be refused, got %v", err)
	}
}

// A failing program read back from a file says where it failed exactly as
// it did before it was written.
func TestErrorsAreTheSameFromAFile(t *testing.T) {
	chunk := NewChunk()
	chunk.Code = []Instr{
		NewInstr(OpNEWFUNC, 1, 1, 0), // main
		NewInstr(OpCALL, 1, 0, 0),
		NewInstr(OpRET, 0, 0, 0),
		NewInstr(OpLOADI, 1, 1, 0), // divide
		NewInstr(OpLOADI, 2, 0, 0),
		NewInstr(OpDIV, 0, 1, 2),
		NewInstr(OpRET, 0, 0, 0),
	}
	chunk.Prototypes = []FunctionPrototype{
		{Name: "main", CodeStart: 0, CodeSize: 3, RegisterCount: 2},
		{Name: "divide", CodeStart: 3, CodeSize: 4, RegisterCount: 3},
	}
	call := &DebugInfo{FilePath: "ratio.qi", Line: 7, Column: 12, SourceLine: "    return divide();", FuncName: "main"}
	chunk.DebugInfo[0], chunk.DebugInfo[1], chunk.DebugInfo[2] = call, call, call
	division := &DebugInfo{FilePath: "ratio.qi", Line: 3, Column: 14, SourceLine: "    return 1 / zero;", FuncName: "divide"}
	for ip := 3; ip < 7; ip++ {
		chunk.DebugInfo[ip] = division
	}

	_, before := NewVM().Exec(chunk)
	data, _ := SerializeChunk(chunk)
	loaded, err := DeserializeChunk(data)
	if err != nil {
		t.Fatal(err)
	}
	_, after := NewVM().Exec(loaded)
	if before == nil || after == nil || before.Error() != after.Error() {
		t.Fatalf("before:\n%v\nafter:\n%v", before, after)
	}
	for _, want := range []string{"R0004", "division by zero", "ratio.qi:3:14 in function 'divide'", "return 1 / zero;", "called from ratio.qi:7:12 in function 'main'"} {
		if !strings.Contains(after.Error(), want) {
			t.Errorf("expected %q in:\n%v", want, after)
		}
	}

	// Stripped, it still says what and at which instruction
	_, bare := NewVM().Exec(chunk.Stripped())
	if bare == nil || !strings.Contains(bare.Error(), "division by zero") || !strings.Contains(bare.Error(), "at instruction 5") || strings.Contains(bare.Error(), "ratio.qi") {
		t.Errorf("stripped: %v", bare)
	}
}
