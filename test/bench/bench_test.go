// Package bench measures Qoqi on whole programs.
//
// Each program in programs/ leans on one part of the language (calls,
// arrays, structs, traits, closures, texts, enums) the way a real program
// does, and prints what it worked out; the .expected file next to it is what it
// must print. A change that makes Qoqi faster is measured here before and
// after, and one that makes a program print something else has not made
// it faster.
//
//	make bench
//	go test ./test/bench -run '^$' -bench . -benchmem -count 6 > new.txt
//
// Comparing two such files (benchstat old.txt new.txt) tells a difference
// from noise.
package bench

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/compiler"
	"github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/memguard"
	"github.com/qoqiflow/qoqi/internal/vm"
)

func TestMain(m *testing.M) {
	memguard.EnforceInTests()
	os.Exit(m.Run())
}

type program struct {
	name  string
	file  string
	chunk *vm.Chunk
	want  string
}

// programs compiles every program of the folder.
func programs(t testing.TB) []program {
	t.Helper()
	files, err := filepath.Glob("programs/*.qi")
	if err != nil || len(files) == 0 {
		t.Fatalf("no programs to measure: %v", err)
	}
	var found []program
	for _, file := range files {
		errs := errors.NewHandler()
		result, err := compiler.CompileFile(file, errs)
		if err != nil || errs.HasErrors() {
			t.Fatalf("%s does not compile: %v\n%v", file, err, errs.Diagnostics)
		}
		want, err := os.ReadFile(strings.TrimSuffix(file, ".qi") + ".expected")
		if err != nil {
			t.Fatalf("%s has nothing to be compared with: %v", file, err)
		}
		name := strings.TrimSuffix(filepath.Base(file), ".qi")
		found = append(found, program{name: name, file: file, chunk: result.Chunk, want: string(want)})
	}
	return found
}

// run executes a compiled program on a machine of its own.
func (p program) run(t testing.TB) (string, *vm.VM) {
	t.Helper()
	var out bytes.Buffer
	machine := vm.NewVM()
	machine.SetOutput(&out)
	if _, err := machine.Exec(p.chunk); err != nil {
		t.Fatalf("%s failed: %v", p.file, err)
	}
	return out.String(), machine
}

// TestProgramsPrintWhatTheyShould is what makes the measurements mean
// something: the programs are right.
func TestProgramsPrintWhatTheyShould(t *testing.T) {
	for _, p := range programs(t) {
		t.Run(p.name, func(t *testing.T) {
			if got, _ := p.run(t); got != p.want {
				t.Errorf("printed\n%s\nexpected\n%s", got, p.want)
			}
		})
	}
}

// TestBytecodeSize keeps an eye on how large a compiled program is. The
// bounds are loose: they are there to notice a program doubling in size,
// not to forbid it growing.
func TestBytecodeSize(t *testing.T) {
	for _, p := range programs(t) {
		data, err := vm.SerializeChunk(p.chunk)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%-12s %6d instructions, %4d functions, %7d bytes", p.name, len(p.chunk.Code), len(p.chunk.Prototypes), len(data))
		if len(data) > 4<<20 {
			t.Errorf("%s compiles to %d bytes", p.name, len(data))
		}
	}
}

// BenchmarkPrograms runs each program, compiled beforehand: the time is
// the virtual machine's.
func BenchmarkPrograms(b *testing.B) {
	for _, p := range programs(b) {
		b.Run(p.name, func(b *testing.B) {
			collections := 0
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				got, machine := p.run(b)
				if got != p.want {
					b.Fatalf("%s printed something else", p.file)
				}
				collections += machine.Collections()
			}
			b.ReportMetric(float64(collections)/float64(b.N), "collections/op")
		})
	}
}

// BenchmarkCompile compiles each program from its text: the time is the
// compiler's, the standard library it brings in included.
func BenchmarkCompile(b *testing.B) {
	for _, p := range programs(b) {
		b.Run(p.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				errs := errors.NewHandler()
				if _, err := compiler.CompileFile(p.file, errs); err != nil || errs.HasErrors() {
					b.Fatal("does not compile")
				}
			}
		})
	}
}
