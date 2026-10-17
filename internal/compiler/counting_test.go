package compiler

import (
	"testing"

	"github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/vm"
)

// A loop over numbers counts with its own variable where nothing can tell
// them apart, and keeps a counter of its own where something can: a
// function made in the body, a deferred statement. The program goes
// through both, with continue, break, matches, loops inside loops and
// failures; what it prints is what it printed when every loop kept a
// counter.
func TestLoopsOverNumbers(t *testing.T) {
	out, _, err := runProgram(t, loopsProgram)
	if err != nil {
		t.Fatalf("the program failed: %v", err)
	}
	if out != loopsPrinted {
		t.Errorf("printed\n%s\nexpected\n%s", out, loopsPrinted)
	}
}

// Adding up the numbers below n is four instructions a turn: the test,
// the sum, the count, the jump back. No copy of the variable is made.
func TestALoopOverNumbersIsFourInstructionsATurn(t *testing.T) {
	src := `fn sum(n: Int) -> Int {
    let total = 0;

    for i in 0..n {
        total += i;
    }

    return total;
}

fn main() -> Int {
    return sum(10);
}
`
	errs := errors.NewHandler()
	result := Compile(src, "test.qi", errs)
	if errs.HasErrors() || result.Chunk == nil {
		t.Fatalf("does not compile: %v", errs.Diagnostics)
	}
	value, err := vm.NewVM().Exec(result.Chunk)
	if err != nil || !value.IsInt() || value.AsInt() != 45 {
		t.Fatalf("expected 45, got %v, %v", value, err)
	}

	p := result.Chunk.Prototypes[result.Chunk.FindPrototype("sum")]
	code := result.Chunk.Code[p.CodeStart : p.CodeStart+p.CodeSize]
	for at, instr := range code {
		if instr.Op != vm.OpJMP || int(instr.A) > p.CodeStart+at {
			continue
		}
		// The jump back: the turn is from where it goes to here
		turn := code[int(instr.A)-p.CodeStart : at+1]
		if len(turn) != 4 {
			t.Errorf("expected four instructions a turn, got %d: %v", len(turn), turn)
		}
		for _, step := range turn {
			if step.Op == vm.OpMOV {
				t.Errorf("expected no copy in the loop, got %v", turn)
			}
		}
		return
	}
	t.Fatalf("no jump back in %v", code)
}

const loopsProgram = `import std.arrays;
import std.result.{attempt};

struct Grid {
    width: Int;
    height: Int;
}

impl Grid {
    fn cells() -> [(Int, Int)] {
        let out: [(Int, Int)] = [];

        for y in 0..self.height {
            for x in 0..self.width {
                if (x + y) % 3 == 0 {
                    continue;
                }

                out.push((x, y));
            }
        }

        return out;
    }
}

enum Step {
    Skip,
    Stop,
    Add(Int),
}

fn steps(n: Int) -> [Step] {
    let out: [Step] = [];

    for i in 0..n {
        if i % 4 == 1 {
            out.push(Step.Skip);
        } else if i == 9 {
            out.push(Step.Stop);
        } else {
            out.push(Step.Add(i * 10));
        }
    }

    return out;
}

fn sumOf<T>(items: [T], weight: fn(T) -> Int) -> Int {
    let total = 0;

    for i in 0..items.len() {
        total += weight(items[i]) * (i + 1);
    }

    return total;
}

fn bound(name: String, value: Int) -> Int {
    println("bound", name, value);
    return value;
}

fn main() -> Int {
    // continue and break, in a match
    let total = 0;

    for i in 0..20 {
        let step = steps(12)[i % 12];

        match step {
            case Step.Skip: {
                continue;
            }

            case Step.Stop: {
                break;
            }

            case Step.Add(n): {
                total += n + i;
            }
        }
    }

    println("total", total);

    // Loops inside loops, one with the same name inside a function made
    // in the outer one
    let grid = Grid { width: 4, height: 3 };
    println(grid.cells().len(), grid.cells()[0], grid.cells()[5]);

    let made: [fn() -> Int] = [];

    for i in 0..3 {
        made.push(fn() -> Int {
            let inner = 0;

            for k in 0..4 {
                inner += k;
            }

            return inner * 100 + i;
        });
    }

    println(made.map(f -> f()));

    for i in 0..3 {
        for i in 10..12 {
            print(i, "");
        }

        print(i, "");
    }

    println("");

    // Each iteration has its own variable for what is made in it, and for
    // what it defers
    let later: [fn() -> String] = [];

    for i in 1..=3 {
        later.push(() -> "seen ${i}");
        defer println("deferred", i);
    }

    println(later.map(f -> f()));

    // Bounds worked out once; the end does not move with what it came from
    let n = 3;
    let turns = 0;

    for i in bound("low", 1)..bound("high", n + 1) {
        n += 10;
        turns += i;
    }

    println("turns", turns, "n", n);

    // Empty, single, negative
    for i in 5..5 {
        println("never", i);
    }

    for i in 5..=5 {
        println("once", i);
    }

    for i in -3..0 {
        print(i, "");
    }

    println("");

    // A generic function, a closure that loops, a failure caught inside
    println(sumOf(["a", "bb", "ccc"], s -> s.len()), sumOf([2.5, 1.5], x -> x.floor()));

    let caught = 0;

    for i in 0..6 {
        let result = attempt(fn() -> Int {
            if i % 3 == 2 {
                panic("at ${i}");
            }

            return i * i;
        });

        match result {
            case Result.Ok(v): {
                caught += v;
            }

            case Result.Err(e): {
                println("failed", e.describe());
            }
        }
    }

    println("caught", caught);
    return 0;
}
`

const loopsPrinted = `total 875
8 (1, 0) (0, 2)
[600, 601, 602]
10 11 0 10 11 1 10 11 2 
deferred 1
deferred 2
deferred 3
["seen 1", "seen 2", "seen 3"]
bound low 1
bound high 4
turns 6 n 33
once 5
-3 -2 -1 
14 4
failed at 2, at test.qi:164:17 in function 'lambda_6'
failed at 5, at test.qi:164:17 in function 'lambda_6'
caught 26
`

// A loop that includes its end ends at the largest Int, instead of failing
// to count past it, whether it counts with its variable or with a number
// of its own, and with continue, break and a position.
func TestLoopsThatEndAtTheLargestInt(t *testing.T) {
	out, _, err := runProgram(t, endsProgram)
	if err != nil {
		t.Fatalf("the program failed: %v", err)
	}
	if out != endsPrinted {
		t.Errorf("printed\n%s\nexpected\n%s", out, endsPrinted)
	}
}

const endsProgram = `import std.arrays;

fn main() -> Int {
    let top = 140737488355327;

    // Counting with the variable, up to the largest Int
    let seen: [Int] = [];

    for i in (top - 2)..=top {
        seen.push(i - top);
    }

    println(seen);

    // With a counter of its own, the variable kept by functions
    let kept: [fn() -> Int] = [];

    for i in (top - 1)..=top {
        kept.push(() -> top - i);
    }

    println(kept.map(f -> f()));

    // continue on the last number, and with a position
    let sum = 0;

    for i in (top - 3)..=top {
        if i == top {
            continue;
        }

        sum += top - i;
    }

    for n, i in (top - 1)..=top {
        print("${n}:${top - i} ");
    }

    println("");
    println("sum", sum);

    // One number, none, and leaving early
    for i in top..=top {
        println("only", i == top);
    }

    for i in top..=(top - 1) {
        println("never", i);
    }

    for i in 1..=top {
        if i > 3 {
            break;
        }

        print(i, "");
    }

    println("");

    // At the smallest Int too, counting down is not what a range does
    let bottom = -140737488355328;
    let low: [Int] = [];

    for i in bottom..=(bottom + 2) {
        low.push(i - bottom);
    }

    println(low);
    return 0;
}
`

const endsPrinted = `[-2, -1, 0]
[1, 0]
0:1 1:0 
sum 6
only true
1 2 3 
[0, 1, 2]
`
