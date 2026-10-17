package fuzz

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/sandbox"
	"github.com/qoqiflow/qoqi/internal/vm"
)

// marker ends each program in what formatAll prints. No program made
// here holds it.
const marker = "\n----8<----\n"

// formatAll formats programs with std.formatter, all in one run of one
// Qoqi program that holds them as texts: the formatter is compiled once.
// It gives the formatted texts back in the same order.
func formatAll(t *testing.T, programs []string) []string {
	t.Helper()
	quote := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`, "\n", `\n`, "\t", `\t`)
	var src strings.Builder
	src.WriteString("import std.formatter;\nimport std.result.{Result};\n\nfn main() {\n    let programs = [\n")
	for _, program := range programs {
		fmt.Fprintf(&src, "        \"%s\",\n", quote.Replace(program))
	}
	src.WriteString("    ];\n    for program in programs {\n        match formatter.format(program) {\n")
	src.WriteString("            case Result.Ok(tidy): { print(tidy); }\n")
	src.WriteString("            case Result.Err(problem): { print(\"refused: \" + problem.describe()); }\n")
	fmt.Fprintf(&src, "        }\n        print(\"%s\");\n    }\n}\n", quote.Replace(marker))

	chunk := compile(src.String())
	if chunk == nil {
		t.Fatal("the program that formats the others does not compile")
	}
	var out bytes.Buffer
	machine := vm.NewVM()
	machine.SetOutput(&out)
	// Bounded like everything here: formatting a few hundred small
	// programs is some tens of millions of steps
	machine.SetStepLimit(2_000_000_000)
	if _, err := machine.Exec(chunk); err != nil {
		t.Fatalf("the formatter failed: %v", err)
	}
	formatted := strings.Split(out.String(), marker)
	if len(formatted) != len(programs)+1 {
		t.Fatalf("expected %d formatted programs, got %d", len(programs), len(formatted)-1)
	}
	return formatted[:len(programs)]
}

// TestRandomProgramsMeanTheSameFormatted formats the programs made from
// a list of seeds and runs them: each has to print what it means, as it
// did before. Formatting a second time has to change nothing.
func TestRandomProgramsMeanTheSameFormatted(t *testing.T) {
	programs := 150
	if testing.Short() {
		programs = 40
	}
	var sources, wants []string
	var seeds []int64
	for seed := int64(1); len(sources) < programs; seed++ {
		funcs, main := generate(seed)
		want, ok := expected(funcs, main)
		if !ok {
			continue
		}
		sources, wants, seeds = append(sources, qoqiSource(funcs, main)), append(wants, want), append(seeds, seed)
	}

	tidy := formatAll(t, sources)
	changed := 0
	for i, text := range tidy {
		if strings.HasPrefix(text, "refused: ") {
			t.Fatalf("seed %d: the formatter refused a program that compiles: %s\n%s", seeds[i], text, sources[i])
		}
		if text != sources[i] {
			changed++
		}
		chunk := compile(text)
		if chunk == nil {
			t.Fatalf("seed %d: formatted, the program does not compile:\n%s", seeds[i], text)
		}
		got, err := sandbox.Run(chunk, sandbox.Limits{Steps: 5_000_000, Output: 1 << 20})
		if err != nil || got != wants[i] {
			t.Fatalf("seed %d: formatted, the program printed\n%s(%v)\nand means\n%s\n%s", seeds[i], got, err, wants[i], text)
		}
	}
	// Programs written the formatter's way already would show nothing
	if changed < programs/2 {
		t.Errorf("formatting changed %d of %d programs: too few to tell anything", changed, programs)
	}

	for i, text := range formatAll(t, tidy) {
		if text != tidy[i] {
			t.Fatalf("seed %d: formatting twice is not formatting once:\n%s\n---\n%s", seeds[i], tidy[i], text)
		}
	}
}
