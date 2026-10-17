package stdlib_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/vm"
)

// A flow taken up from a stage, from its variables alone: the program is
// run once as it is, its variables kept at every stage it passes; then,
// for each of those passes, a new machine starts at the stage with them,
// and has to print what the program printed after it, to the letter.

type pass struct {
	stage   string
	vars    map[string]json.RawMessage
	printed int // how much the program had printed when it passed
}

func passes(t *testing.T, chunk *vm.Chunk, flow, fields string) (string, []pass) {
	t.Helper()
	var out bytes.Buffer
	var kept []pass
	machine := vm.NewVM()
	machine.SetOutput(&out)
	machine.SetOnStage(func(m *vm.VM, name string) error {
		vars, err := m.StageData()
		if err != nil {
			t.Fatalf("stage %s: %v", name, err)
		}
		kept = append(kept, pass{name, vars, out.Len()})
		return nil
	})
	if _, err := machine.Start(chunk, flow+".run", []byte(fields)); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	return out.String(), kept
}

func expectTakenUpAtEveryStage(t *testing.T, src, flow, fields string, want ...string) []pass {
	t.Helper()
	chunk := compiled(t, src)
	plain, kept := passes(t, chunk, flow, fields)
	if got := strings.TrimSuffix(plain, "\n"); got != strings.Join(want, "\n") {
		t.Fatalf("the program as it is:\nwant:\n%s\ngot:\n%s", strings.Join(want, "\n"), got)
	}
	if len(kept) == 0 {
		t.Fatalf("the program passes no stage")
	}
	for k, p := range kept {
		var out bytes.Buffer
		machine := vm.NewVM()
		machine.SetOutput(&out)
		if _, err := machine.StartAtStage(chunk, flow+".run", p.stage, p.vars); err != nil {
			t.Fatalf("taken up at pass %d, stage %s: %v\n%s", k+1, p.stage, err, out.String())
		}
		if want := plain[p.printed:]; out.String() != want {
			t.Errorf("taken up at pass %d, stage %s, from %s:\nwant:\n%s\ngot:\n%s", k+1, p.stage, varsOf(p.vars), want, out.String())
		}
	}
	return kept
}

func varsOf(vars map[string]json.RawMessage) string {
	var parts []string
	for name, data := range vars {
		parts = append(parts, name+"="+string(data))
	}
	sort.Strings(parts)
	return strings.Join(parts, " ")
}

// A loop over numbers, one over a list, a match, structs and enums
// changed between stages, and the fields of the flow.
func TestTakenUpAtAStage(t *testing.T) {
	kept := expectTakenUpAtEveryStage(t, `
import std.flow.{Flow};

enum Mood { Calm, Busy(Int) }
struct Tally { count: Int; seen: [String]; }

struct Count { n: Int; names: [String]; }

impl Flow<String> for Count {
    fn run() -> String {
        let tally = Tally { count: 0, seen: [] };
        let mood = Mood.Calm;

        for i in 0..self.n {
            stage counting;
            tally.count += i;
            if i > 1 {
                mood = Mood.Busy(i);
            }
            println("count", i, tally.count);
        }

        for name in self.names {
            stage naming;
            tally.seen.push(name);
            println("name", name, tally.seen.len());
        }

        stage done;
        self.n += 100;
        match mood {
            case Calm: { return "calm ${tally.count}"; }
            case Busy(k): { return "busy ${k} ${tally.count} ${self.n} ${tally.seen}"; }
        }
    }
}

fn main() {}
`, "Count", `{"$type": "Count", "n": 4, "names": ["ada", "lin"]}`,
		"count 0 0", "count 1 1", "count 2 3", "count 3 6",
		"name ada 1", "name lin 2")

	if len(kept) != 7 {
		t.Fatalf("expected seven passes, got %d", len(kept))
	}
	var names []string
	for name := range kept[0].vars {
		names = append(names, name)
	}
	sort.Strings(names)
	if got := strings.Join(names, " "); got != "i loop.end#1 mood self tally" {
		t.Errorf("the variables of stage counting: %s", got)
	}
	if string(kept[0].vars["self"]) != `{"$type":"Count","n":4,"names":["ada","lin"]}` {
		t.Errorf("the fields, as they are at the stage: %s", kept[0].vars["self"])
	}
}

// A stage refused what is not given, and a stage the function has not.
func TestTakingUpAtAStageSaysWhatIsMissing(t *testing.T) {
	chunk := compiled(t, `
import std.flow.{Flow};
struct One { n: Int; }
impl Flow<Int> for One {
    fn run() -> Int {
        let x = self.n * 2;
        stage half;
        return x;
    }
}
fn main() {}
`)
	if _, err := vm.NewVM().StartAtStage(chunk, "One.run", "half", map[string]json.RawMessage{"self": json.RawMessage(`{"$type": "One", "n": 1}`)}); err == nil || !strings.Contains(err.Error(), "x, which is not given") {
		t.Errorf("expected the missing variable named, got %v", err)
	}
	if _, err := vm.NewVM().StartAtStage(chunk, "One.run", "whole", nil); err == nil || !strings.Contains(err.Error(), "has no stage whole") {
		t.Errorf("expected the missing stage named, got %v", err)
	}
	result, err := vm.NewVM().StartAtStage(chunk, "One.run", "half", map[string]json.RawMessage{"self": json.RawMessage(`{"$type": "One", "n": 1}`), "x": json.RawMessage(`21`)})
	if err != nil || result.AsInt() != 21 {
		t.Errorf("expected 21, got %v %v", result, err)
	}
}

// The stages are written with the bytecode, and read back: a program
// compiled to a file is taken up from its stages as its source is.
func TestStagesInTheBytecode(t *testing.T) {
	chunk := compiled(t, `
import std.flow.{Flow};
struct Two { n: Int; }
impl Flow<Int> for Two {
    fn run() -> Int {
        let x = self.n;
        stage first;
        x *= 2;
        stage second;
        return x + 1;
    }
}
fn main() {}
`)
	data, err := vm.SerializeChunk(chunk)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := vm.DeserializeChunk(data)
	if err != nil {
		t.Fatal(err)
	}
	var shape map[string]string
	for _, proto := range loaded.Prototypes {
		for _, stage := range proto.Stages {
			if stage.Name == "second" {
				shape = stage.Shape
			}
		}
	}
	if got := fmt.Sprint(shape); got != "map[self:Two self.n:Int x:Int]" {
		t.Errorf("the shape of stage second: %s", got)
	}
	result, err := vm.NewVM().StartAtStage(loaded, "Two.run", "second", map[string]json.RawMessage{
		"self": json.RawMessage(`{"$type": "Two", "n": 5}`), "x": json.RawMessage(`10`)})
	if err != nil || result.AsInt() != 11 {
		t.Errorf("expected 11 from the bytecode read back, got %v %v", result, err)
	}
}
