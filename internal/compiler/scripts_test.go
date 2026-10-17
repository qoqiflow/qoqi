package compiler

import (
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/vm"
)

// inventory is a script: statements outside any function, around the
// declarations they use, some written after the statements that use them.
const inventory = `
import std.strings;

// The stock is written before the types it is made of are
let stock = [
    Item { name: "bolt", count: 40, kind: Kind.Part },
    Item { name: "drill", count: 2, kind: Kind.Tool },
    Item { name: "nut", count: 0, kind: Kind.Part },
];

enum Kind { Part, Tool }

struct Item {
    name: String;
    count: Int;
    kind: Kind;
}

impl Item {
    fn label() -> String {
        return match self.kind {
            case Kind.Part: "part ${self.name}";
            case Kind.Tool: "tool ${self.name}";
        };
    }
}

const LIMIT = 50;

fn over(n: Int) -> Bool {
    return n > LIMIT;
}

let missing: [String] = [];
let total = 0;

for i, item in stock {
    total += item.count;

    if item.count == 0 {
        missing.push(item.name);
        continue;
    }

    println("${i}: ${item.label()} x${item.count}");
}

// A closure sees the variables of the script; a function does not
let report = () -> "total ${total}, missing ${missing.len()}";
println(report());

let (first, second) = (stock[0].name, stock[1].name);
println(first.upper(), second);

{
    defer println("block done");
    println("in a block");
}

match missing.len() {
    case 0: {
        println("all there");
    }

    default: {
        println("restock ${missing.join(", ")}");
    }
}

if over(total) {
    return 3;
}

println("under the limit");
`

// exitCode is the status qoqi run leaves with for what main gave: an
// Int is the code, anything else 0.
func exitCode(value vm.Value) int64 {
	if value.IsInt() {
		return value.AsInt()
	}
	return 0
}

func TestScriptRunsItsStatementsInOrder(t *testing.T) {
	out, value, err := runProgram(t, inventory)
	if err != nil {
		t.Fatalf("runtime error: %v", err)
	}
	want := "0: part bolt x40\n1: tool drill x2\ntotal 42, missing 1\nBOLT drill\nin a block\nblock done\nrestock nut\nunder the limit\n"
	if out != want {
		t.Errorf("unexpected output\n--- want ---\n%s--- got ---\n%s", want, out)
	}
	if exitCode(value) != 0 {
		t.Errorf("a script that ends without returning has to succeed, got %v", value)
	}

	// Over the limit, it returns early with its code
	out, value, err = runProgram(t, strings.Replace(inventory, "LIMIT = 50", "LIMIT = 40", 1))
	if err != nil {
		t.Fatalf("runtime error: %v", err)
	}
	if strings.Contains(out, "under the limit") || exitCode(value) != 3 {
		t.Errorf("expected the script to stop with 3, got %v after\n%s", value, out)
	}
}

func TestScriptReturnsItsExitCode(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int64
	}{
		{"from a loop", "let n = 0;\nwhile true {\n    n += 1;\n    if n > 3 {\n        return n;\n    }\n}\n", 4},
		{"from a match", "let word = \"two\";\nmatch word {\n    case \"one\": {\n        return 1;\n    }\n\n    case \"two\": {\n        return 2;\n    }\n}\nprintln(\"none\");\n", 2},
		{"at its end", "let codes = [5, 6];\nreturn codes[1] - codes[0];\n", 1},
		{"not reached", "for n in 0..3 {\n    if n > 5 {\n        return 9;\n    }\n}\n", 0},
		{"from a case of a match that gives a value", "let word = \"x\";\nlet n = match word {\n    case \"one\": 1;\n    default: {\n        return 5;\n    }\n};\nprintln(n);\n", 5},
		{"a closure in a match is not the script's", "let pick = match 1 {\n    case 1: fn() -> Int {\n        return 8;\n    };\n    default: fn() -> Int {\n        return 9;\n    };\n};\nprintln(pick());\n", 0},
		{"a return of a closure is not the script's", "let f = fn() -> Int {\n    return 7;\n};\nprintln(f());\n", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, value, err := runProgram(t, c.src)
			if err != nil {
				t.Fatalf("runtime error: %v", err)
			}
			if exitCode(value) != c.want {
				t.Errorf("expected the code %d, got %v", c.want, value)
			}
		})
	}
}

func TestScriptMistakes(t *testing.T) {
	cases := []struct {
		name string
		src  string
		code string
		msg  string
		line int
	}{
		{"statements beside a main", "fn main() {\n    println(1);\n}\nprintln(2);\n", "E8007", "cannot run in a file that has a main", 4},
		{"a variable of the script in a function", "let total = 3;\nfn show() {\n    println(total);\n}\nshow();\n", "E1001", "'total': a variable of the script, which a function does not see", 3},
		{"a variable of the script in a method", "let n = 1;\nstruct S { a: Int; }\nimpl S {\n    fn get() -> Int {\n        return n;\n    }\n}\nprintln(S { a: 1 }.get());\n", "E1001", "a variable of the script", 5},
		{"a variable used before the script declares it", "println(x);\nlet x = 1;\n", "E1001", "undefined identifier 'x'", 1},
		{"a code that is not a number", "println(1);\nreturn \"done\";\n", "E2003", "cannot return type String", 2},
		{"a brace too many", "println(1);\n}\nprintln(2);\n", "E0001", "expected declaration or statement, found '}'", 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			errs := errors.NewHandler()
			Compile(c.src, "test.qi", errs)
			if !errs.HasErrors() {
				t.Fatal("expected a diagnostic, got none")
			}
			d := errs.Diagnostics[0]
			if d.Code != c.code || !strings.Contains(d.Message, c.msg) || d.Pos.Line != c.line {
				t.Errorf("expected %s on line %d containing %q, got %s on line %d: %s", c.code, c.line, c.msg, d.Code, d.Pos.Line, d.Message)
			}
			if strings.Contains(c.msg, "undefined identifier 'x'") && strings.Contains(d.Message, "of the script") {
				t.Errorf("the script itself sees its variables once declared; got %s", d.Message)
			}
		})
	}
}

func TestOnlyTheEntryFileIsAScript(t *testing.T) {
	expectModulesError(t, sources{
		"main.qi": "import tool;\ntool.run();\n",
		"tool.qi": "pub fn run() {\n    println(\"run\");\n}\nprintln(\"loaded\");\n",
	}, "E8007", "tool.qi", "only run in the program's entry file, not in module 'tool'")

	// A script imports modules like any program, and calls their main
	expectModulesOutput(t, sources{
		"main.qi": "import tool;\nlet code = tool.main();\nprintln(\"tool said\", code);\n",
		"tool.qi": "pub fn main() -> Int {\n    return 7;\n}\n",
	}, "tool said 7\n")
}
