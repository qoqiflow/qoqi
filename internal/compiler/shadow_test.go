package compiler

import "testing"

// A name declared again inside a block of a function value, by a loop, a
// let or a case, is the block's until the block ends: after it, the name
// is the variable the function captured, shared with where it was made.
func TestNamesDeclaredAgainInsideFunctionValues(t *testing.T) {
	out, _, err := runProgram(t, shadowedProgram)
	if err != nil {
		t.Fatalf("the program failed: %v", err)
	}
	if out != shadowedPrinted {
		t.Errorf("printed\n%s\nexpected\n%s", out, shadowedPrinted)
	}
}

const shadowedProgram = `import std.arrays;

struct Counter {
    count: Int;
}

fn main() -> Int {
    // A loop of the same name inside a function made in a loop
    let made: [fn() -> String] = [];

    for i in 0..3 {
        made.push(fn() -> String {
            let seen: [Int] = [];

            for i in 10..12 {
                seen.push(i);
            }

            return "${seen} then ${i}";
        });
    }

    println(made.map(f -> f()));

    // A block of the same name, then the captured one, which the function
    // still shares with where it was made
    let total = 100;

    let add = fn(n: Int, local: Bool) -> Int {
        if local {
            let total = n;
            return total * 2;
        }

        total += n;
        return total;
    };

    println(add(5, true), add(5, false), add(1, false), total);

    // Two functions down, past a block of the one in the middle
    let name = "outer";

    let middle = fn() -> fn() -> String {
        for name in ["a", "b"] {
            print(name, "");
        }

        if true {
            let name = "block";
            print(name, "");
        }

        return () -> name.upper();
    };

    println(middle()());

    // A match binding of the same name
    let value: Int? = 41;
    let counter = Counter { count: 1 };

    let check = fn() -> Int {
        match value {
            case null: {
                return 0;
            }

            default: {
                let counter = Counter { count: 99 };
                print(counter.count, "");
            }
        }

        counter.count += 1;
        return counter.count;
    };

    println(check(), check(), counter.count);
    return 0;
}
`

const shadowedPrinted = `["[10, 11] then 0", "[10, 11] then 1", "[10, 11] then 2"]
10 105 106 106
a b block OUTER
99 99 2 3 3
`
