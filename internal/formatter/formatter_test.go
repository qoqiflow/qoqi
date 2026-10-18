package formatter_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/qoqiflow/qoqi/internal/compiler"
	"github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/memguard"
	"github.com/qoqiflow/qoqi/internal/parser"
	"github.com/qoqiflow/qoqi/internal/token"
	"github.com/qoqiflow/qoqi/internal/vm"
)

// TestMain guards the tests: they run a Qoqi program, the formatter, and
// one that takes memory without end must end the tests, not the machine.
func TestMain(m *testing.M) {
	memguard.EnforceInTests()
	os.Exit(m.Run())
}

// driver is a program that formats what it is given on its input, as many
// bytes as its argument says, with std.formatter. Its output starts with
// a line that says how it went.
const driver = `
import std.formatter;
import std.io;
import std.os;
import std.strings;
import std.result.{Result};

fn main() -> Int {
    let source = io.readBytes(os.args()[0].parseInt() ?? 0) ?? "";
    match formatter.format(source) {
        case Result.Ok(tidy): { print("ok\n" + tidy); }
        case Result.Err(problem): { print("problem\n" + problem.describe()); }
    }
    return 0;
}
`

var (
	compileDriver sync.Once
	driverChunk   *vm.Chunk
	driverErrors  string
)

// format runs std.formatter on a text. It returns the formatted text, or
// the problem the formatter reported, as "line:column: message".
func format(t testing.TB, src string) (tidy, problem string) {
	t.Helper()
	compileDriver.Do(func() {
		errs := errors.NewHandler()
		driverChunk = compiler.Compile(driver, "driver.qi", errs).Chunk
		for _, d := range errs.Diagnostics {
			driverErrors += d.String() + "\n"
		}
	})
	if driverErrors != "" || driverChunk == nil {
		t.Fatalf("the formatter does not compile:\n%s", driverErrors)
	}

	var out bytes.Buffer
	machine := vm.NewVM()
	machine.SetOutput(&out)
	machine.SetInput(strings.NewReader(src))
	machine.SetArgs([]string{fmt.Sprint(len(src))})
	if _, err := machine.Exec(driverChunk); err != nil {
		t.Fatalf("the formatter failed: %v", err)
	}

	status, rest, _ := strings.Cut(out.String(), "\n")
	switch status {
	case "ok":
		return rest, ""
	case "problem":
		return "", rest
	}
	t.Fatalf("unexpected output of the formatter: %q", out.String())
	return "", ""
}

// formatted is format for a text that has to be formatted.
func formatted(t testing.TB, src string) string {
	t.Helper()
	tidy, problem := format(t, src)
	if problem != "" {
		t.Fatalf("the formatter refused the text: %s\n%s", problem, src)
	}
	return tidy
}

// program is a text as the compiler reads it: its tokens, by the
// compiler's own lexer, in which a text between quotes is the value it
// stands for and the expressions inside it are tokens too. Two texts that
// give the same are the same program. Commas and semicolons are left
// out: a list written one item to a line gets a comma after its last.
// ok is false for a text that does not parse.
func program(src string) (text string, ok bool) {
	var sb strings.Builder
	for _, tok := range parser.NewLexer(src, "test.qi").Tokenize() {
		if tok.Type == token.COMMA || tok.Type == token.SEMICOLON {
			continue
		}
		fmt.Fprintf(&sb, "%s %q\n", tok.Type, tok.Literal)
	}
	errs := errors.NewHandler()
	parser.Parse(src, "test.qi", errs)
	return sb.String(), !errs.HasErrors()
}

// repository is the root of the repository, two folders up from here.
func repository(t testing.TB) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// sources lists the .qi files of the repository: the standard library,
// the tools written in Qoqi, the examples and the end-to-end cases.
func sources(t testing.TB) []string {
	t.Helper()
	root := repository(t)
	var files []string
	for _, folder := range []string{"internal", "examples", "test"} {
		if _, err := os.Stat(filepath.Join(root, folder)); err != nil {
			continue
		}
		err := filepath.WalkDir(filepath.Join(root, folder), func(path string, entry os.DirEntry, err error) error {
			if err == nil && !entry.IsDir() && strings.HasSuffix(path, ".qi") {
				files = append(files, path)
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(files) < 40 {
		t.Fatalf("expected the .qi files of the repository, found %d", len(files))
	}
	return files
}

// Every Qoqi file of the repository, with all of the language in them,
// is the same program after formatting, to the parser
// that compiles it; and formatting what is formatted changes nothing.
func TestFormattingKeepsEveryProgramOfTheRepository(t *testing.T) {
	root := repository(t)
	checked := 0
	for _, file := range sources(t) {
		name, _ := filepath.Rel(root, file)
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		src := string(data)
		before, parses := program(src)

		tidy, problem := format(t, src)
		if problem != "" {
			if parses {
				t.Errorf("%s: a program that parses was refused: %s", name, problem)
			}
			continue
		}
		checked++

		if after, ok := program(tidy); parses && (!ok || after != before) {
			t.Errorf("%s: formatting changed the program:\n%s", name, firstDifference(before, after))
		}
		if again := formatted(t, tidy); again != tidy {
			t.Errorf("%s: formatting twice is not formatting once:\n%s", name, firstDifference(tidy, again))
		}
		for i, line := range strings.Split(tidy, "\n") {
			if strings.TrimRight(line, " \t") != line {
				t.Errorf("%s:%d: a line ends with spaces: %q", name, i+1, line)
			}
		}
		if strings.Contains(tidy, "\n\n\n") || strings.HasPrefix(tidy, "\n") || (tidy != "" && !strings.HasSuffix(tidy, "\n")) || strings.HasSuffix(tidy, "\n\n") {
			t.Errorf("%s: empty lines in a row, at the top or at the bottom", name)
		}
	}
	if checked < 40 {
		t.Errorf("expected to format the files of the repository, formatted %d", checked)
	}
}

// firstDifference shows the first line two texts differ at.
func firstDifference(a, b string) string {
	as, bs := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := 0; i < len(as) || i < len(bs); i++ {
		x, y := "<nothing>", "<nothing>"
		if i < len(as) {
			x = as[i]
		}
		if i < len(bs) {
			y = bs[i]
		}
		if x != y {
			return fmt.Sprintf("line %d:\n  first  %q\n  second %q", i+1, x, y)
		}
	}
	return "no difference"
}

// lines joins lines into a text that ends with a line end.
func lines(all ...string) string {
	return strings.Join(all, "\n") + "\n"
}

// What the formatter decides, a rule at a time: each text is formatted
// and compared with what it has to become, which formatting again must
// leave alone.
func TestStyle(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"a case does one statement without a block",
			`fn f(x:Result<Int,String>)->Int{let n=0;match x{case Ok(v):n+=v;case Err(e):return -1;} for i in [1]{match i{case 1:continue;default:break;}} let m=match n{case 0:return 0;default:n*2,};return m;}`,
			lines(
				"fn f(x: Result<Int, String>) -> Int {",
				"    let n = 0;",
				"",
				"    match x {",
				"        case Ok(v): n += v;",
				"        case Err(e): return -1;",
				"    }",
				"",
				"    for i in [1] {",
				"        match i {",
				"            case 1: continue;",
				"            default: break;",
				"        }",
				"    }",
				"",
				"    let m = match n {",
				"        case 0: return 0;",
				"        default: n * 2;",
				"    };",
				"",
				"    return m;",
				"}",
			),
		},
		{"keywords used as names are names",
			`fn f(case:Case,in:[Int])->Int{for case in cases{log(case.name);} let match=in[0];match.count+=1;if match{log(match);} match match{case 1:{log(1);} default:{}} return in.len()+match;}`,
			lines(
				"fn f(case: Case, in: [Int]) -> Int {",
				"    for case in cases {",
				"        log(case.name);",
				"    }",
				"",
				"    let match = in[0];",
				"    match.count += 1;",
				"",
				"    if match {",
				"        log(match);",
				"    }",
				"",
				"    match match {",
				"        case 1: {",
				"            log(1);",
				"        }",
				"",
				"        default: {}",
				"    }",
				"",
				"    return in.len() + match;",
				"}",
			)},
		{"a migration, and migrate as a name",
			`impl Sub{migrate   fn month(paid:Int)->Month{let migrate=paid;return Month{paid:migrate};}}`,
			lines(
				"impl Sub {",
				"    migrate fn month(paid: Int) -> Month {",
				"        let migrate = paid;",
				"        return Month { paid: migrate };",
				"    }",
				"}",
			)},
		{"steps the application carries out, and by, undo, within as names",
			`pub step fn reserve(cart:Cart)->Result<Hold,Problem>by   warehouse undo release;step fn wire(to:Account)->Settled<Wire>within 2.hours()  by bank;step fn capture(id:String)->Settled<String>within   5.minutes(){return Settled.Done(id);}fn f(by:Int,undo:Int,within:Int)->Int{return by+undo+within;}`,
			lines(
				"pub step fn reserve(cart: Cart) -> Result<Hold, Problem> by warehouse undo release;",
				"",
				"step fn wire(to: Account) -> Settled<Wire> within 2.hours() by bank;",
				"",
				"step fn capture(id: String) -> Settled<String> within 5.minutes() {",
				"    return Settled.Done(id);",
				"}",
				"",
				"fn f(by: Int, undo: Int, within: Int) -> Int {",
				"    return by + undo + within;",
				"}",
			)},
		{"a stage, and stage as a name",
			`fn run()->Int{let stage=1;   stage   polling ;stage+=1;return stage;}`,
			lines(
				"fn run() -> Int {",
				"    let stage = 1;",
				"    stage polling;",
				"    stage += 1;",
				"    return stage;",
				"}",
			)},
		{"an if and a match before the value of an if are statements",
			`fn f(n:Int)->Int{let v=if n>0{if n>9{log(n);} match n{case 1:{log(1);} default:{}} n*2}else{0};return v;}`,
			lines(
				"fn f(n: Int) -> Int {",
				"    let v = if n > 0 {",
				"        if n > 9 {",
				"            log(n);",
				"        }",
				"",
				"        match n {",
				"            case 1: {",
				"                log(1);",
				"            }",
				"",
				"            default: {}",
				"        }",
				"",
				"        n * 2",
				"    } else {",
				"        0",
				"    };",
				"",
				"    return v;",
				"}",
			)},
		{"a case of a match that gives a value may end with its value",
			`fn f(n:Int)->String{return match n>0{case true:{if n>9{log(n);} "big"} case false:"small";};}`,
			lines(
				"fn f(n: Int) -> String {",
				"    return match n > 0 {",
				"        case true: {",
				"            if n > 9 {",
				"                log(n);",
				"            }",
				"",
				"            \"big\"",
				"        }",
				"",
				"        case false: \"small\";",
				"    };",
				"}",
			)},
		{"a match that is the value of a branch keeps its comments",
			"fn f(n:Int)->Int{let v=if n>0{n}else{match n{case 0:1;\n// the one\ncase 1:2; default:3;}};return v;}",
			lines(
				"fn f(n: Int) -> Int {",
				"    let v = if n > 0 {",
				"        n",
				"    } else {",
				"        match n {",
				"            case 0: 1;",
				"            // the one",
				"            case 1: 2;",
				"            default: 3;",
				"        }",
				"    };",
				"",
				"    return v;",
				"}",
			)},
		{"a block is never on the line of its brace",
			`fn double(n:Int)->Int{return n*2;}`,
			lines(
				"fn double(n: Int) -> Int {",
				"    return n * 2;",
				"}",
			)},
		{"the #! line of a script stays first, as it was",
			"#!/usr/bin/env qoqi\nfn main(){println(1);}",
			lines(
				"#!/usr/bin/env qoqi",
				"fn main() {",
				"    println(1);",
				"}",
			)},
		{"the statements of a script follow each other as in a block, declarations stand apart",
			"#!/usr/bin/env qoqi\nimport std.strings;\nlet names=[\"ada\",\"grace\"];let count=names.len();\nfn greet(name:String)->String{return \"hi ${name}\";}\n// each one\nfor name in names{println(greet(name));}\nprintln(count);let done=true;\nconst LIMIT=3;\nif count>LIMIT{return 1;}",
			lines(
				"#!/usr/bin/env qoqi",
				"import std.strings;",
				"",
				"let names = [\"ada\", \"grace\"];",
				"let count = names.len();",
				"",
				"fn greet(name: String) -> String {",
				"    return \"hi ${name}\";",
				"}",
				"",
				"// each one",
				"for name in names {",
				"    println(greet(name));",
				"}",
				"",
				"println(count);",
				"let done = true;",
				"",
				"const LIMIT = 3;",
				"",
				"if count > LIMIT {",
				"    return 1;",
				"}",
			)},
		{"a choice stays on one line while it fits, then each value goes under the condition",
			"fn grade(score:Int)->String{return score>=90?\"A\":score>=75?\"B\":\"C\";}\nfn main(){let label=someVeryLongConditionName&&anotherQuiteLongCondition?\"the first quite long value\":\"the other long value\";}",
			lines(
				"fn grade(score: Int) -> String {",
				"    return score >= 90 ? \"A\" : score >= 75 ? \"B\" : \"C\";",
				"}",
				"",
				"fn main() {",
				"    let label = someVeryLongConditionName && anotherQuiteLongCondition",
				"        ? \"the first quite long value\"",
				"        : \"the other long value\";",
				"}",
			)},
		{"the ? of a failure is not a choice",
			"fn f(r:Result<Int,String>)->Result<Int,String>{let a=r?-1;let b=r?>0?a:-a;let c=r? ?1:2;return Result.Ok(n?[0]+a+b+c);}",
			lines(
				"fn f(r: Result<Int, String>) -> Result<Int, String> {",
				"    let a = r? - 1;",
				"    let b = r? > 0 ? a : -a;",
				"    let c = r? ? 1 : 2;",
				"    return Result.Ok(n?[0] + a + b + c);",
				"}",
			)},
		{"a script may make a function without naming it",
			"fn(){println(1);}();\nlet twice=fn(n:Int)->Int{return n*2;};",
			lines(
				"fn() {",
				"    println(1);",
				"}();",
				"",
				"let twice = fn(n: Int) -> Int {",
				"    return n * 2;",
				"};",
			)},
		{"an empty block stays closed",
			"fn nothing(){}\nfn main(){while true{}}",
			lines(
				"fn nothing() {}",
				"",
				"fn main() {",
				"    while true {}",
				"}",
			)},
		{"a statement of several lines stands apart, short ones stay together",
			`fn main(){let a=1;let b=2;if a<b{println(a);}let c=3;let d=4;for n in 0..3{println(n);}while false{}return;}`,
			lines(
				"fn main() {",
				"    let a = 1;",
				"    let b = 2;",
				"",
				"    if a < b {",
				"        println(a);",
				"    }",
				"",
				"    let c = 3;",
				"    let d = 4;",
				"",
				"    for n in 0..3 {",
				"        println(n);",
				"    }",
				"",
				"    while false {}",
				"    return;",
				"}",
			)},
		{"the empty lines of the writer are kept, one at a time, and none at the edges of a block",
			"fn main() {\n\n\n    let a = 1;\n\n\n\n    let b = 2;\n    let c = 3;\n\n}\n\n\n\n",
			lines(
				"fn main() {",
				"    let a = 1;",
				"",
				"    let b = 2;",
				"    let c = 3;",
				"}",
			)},
		{"declarations stand apart; imports, constants and externs follow each other",
			"import std.io;\nimport std.os;\n\nimport std.math;\nconst a = 1;\nconst b = 2;\nextern fn now() -> Int;\nextern fn later() -> Int;\nfn f() {}\nfn g() {}\nstruct P { x: Int; }\nenum E { A, B }",
			lines(
				"import std.io;",
				"import std.os;",
				"",
				"import std.math;",
				"",
				"const a = 1;",
				"const b = 2;",
				"",
				"extern fn now() -> Int;",
				"extern fn later() -> Int;",
				"",
				"fn f() {}",
				"",
				"fn g() {}",
				"",
				"struct P {",
				"    x: Int;",
				"}",
				"",
				"enum E { A, B }",
			)},
		{"methods stand apart, the ones a trait only names follow each other",
			"trait Shape{fn area()->Float;fn name()->String;fn show(){println(self.name());}}\nimpl Shape for Dot{fn area()->Float{return 0.0;}fn name()->String{return \"dot\";}}",
			lines(
				"trait Shape {",
				"    fn area() -> Float;",
				"    fn name() -> String;",
				"",
				"    fn show() {",
				"        println(self.name());",
				"    }",
				"}",
				"",
				"impl Shape for Dot {",
				"    fn area() -> Float {",
				"        return 0.0;",
				"    }",
				"",
				"    fn name() -> String {",
				"        return \"dot\";",
				"    }",
				"}",
			)},
		{"comments stay with what they are about",
			"// The file.\n\n// About f.\nfn f() {\n    // first\n    let a = 1; // the one\n    let bbbbbb = 2; // the other\n    let c = 3;\n\n    // detached\n\n    work(); /* done */\n    // last in the block\n}\n// last in the file",
			lines(
				"// The file.",
				"",
				"// About f.",
				"fn f() {",
				"    // first",
				"    let a = 1;       // the one",
				"    let bbbbbb = 2;  // the other",
				"    let c = 3;",
				"",
				"    // detached",
				"",
				"    work();  /* done */",
				"    // last in the block",
				"}",
				"",
				"// last in the file",
			)},
		{"a comment inside a list keeps the list one item to a line",
			"fn main() { call(first, // why\n second); let p = [\n // about one\n 1, 2]; }",
			lines(
				"fn main() {",
				"    call(",
				"        first,  // why",
				"        second,",
				"    );",
				"",
				"    let p = [",
				"        // about one",
				"        1,",
				"        2,",
				"    ];",
				"}",
			)},
		{"a comment with code after it stays on its line, and before what closes it stands off",
			"fn main() { /* first */ work(a /* why */, b); return /* nothing */; }\nstruct S { /* one */ a: Int; }",
			lines(
				"fn main() {",
				"    /* first */ work(a /* why */, b);",
				"    return /* nothing */;",
				"}",
				"",
				"struct S {",
				"    /* one */ a: Int;",
				"}",
			)},
		{"a comment that ends a line is followed by nothing: no comment changes places with another",
			"fn main() { if a { b(); } // after if\n// before else\nelse { c(); } let d = e // why\n|| f; }",
			lines(
				"fn main() {",
				"    if a {",
				"        b();",
				"    }  // after if",
				"    // before else",
				"    else {",
				"        c();",
				"    }",
				"",
				"    let d = e  // why",
				"        || f;",
				"}",
			)},
		{"a comment over several lines moves with its first line",
			"fn main() {\n        /* one\n           two */\n        work();\n}",
			lines(
				"fn main() {",
				"    /* one",
				"       two */",
				"    work();",
				"}",
			)},
		{"arguments that do not fit are one to a line, with a comma after the last",
			"fn main() { report(theFirstArgument, theSecondArgument, theThirdArgument, theFourthArgument, theFifthArgument, 6); short(1, 2,); }",
			lines(
				"fn main() {",
				"    report(",
				"        theFirstArgument,",
				"        theSecondArgument,",
				"        theThirdArgument,",
				"        theFourthArgument,",
				"        theFifthArgument,",
				"        6,",
				"    );",
				"",
				"    short(1, 2);",
				"}",
			)},
		{"a function with a body starts on the line of its call",
			"fn main() { items.each(fn(item: Item) { println(item); }); arrays.each(items, item -> { count++; }); task.run(fn() {}); }",
			lines(
				"fn main() {",
				"    items.each(fn(item: Item) {",
				"        println(item);",
				"    });",
				"",
				"    arrays.each(items, item -> {",
				"        count++;",
				"    });",
				"",
				"    task.run(fn() {});",
				"}",
			)},
		{"a struct literal is on one line, or one field to a line where it was started so",
			"fn main() { let a = Point{x:1,y:2}; let b = Point {\n x: 1, y: 2 }; let c = Empty{}; let d = geo.Point{x:1,y:2}; let e = Box<Int>{value:1}; }",
			lines(
				"fn main() {",
				"    let a = Point { x: 1, y: 2 };",
				"",
				"    let b = Point {",
				"        x: 1,",
				"        y: 2,",
				"    };",
				"",
				"    let c = Empty {};",
				"    let d = geo.Point { x: 1, y: 2 };",
				"    let e = Box<Int> { value: 1 };",
				"}",
			)},
		{"calls that follow each other break before each dot, all or none",
			"fn main() { let a = json.object().text(\"name\", name).done(); let b = json.object().text(\"jsonrpc\", \"2.0\").set(\"id\", id).set(\"result\", value).int(\"code\", code).done(); self.documents.set(document.path, document); }",
			lines(
				"fn main() {",
				"    let a = json.object().text(\"name\", name).done();",
				"",
				"    let b = json.object()",
				"        .text(\"jsonrpc\", \"2.0\")",
				"        .set(\"id\", id)",
				"        .set(\"result\", value)",
				"        .int(\"code\", code)",
				"        .done();",
				"",
				"    self.documents.set(document.path, document);",
				"}",
			)},
		{"operators that do not fit each start a line, and the brace of the block gets its own",
			"fn main() { let total = theFirstOperandOfTheSum + theSecondOperandOfTheSum * theFactorOfTheSecond + theThirdOperandOfTheSum; if theFirstConditionHolds && theSecondConditionHolds || theThirdConditionHolds && theFourthConditionHolds { work(); } }",
			lines(
				"fn main() {",
				"    let total = theFirstOperandOfTheSum",
				"        + theSecondOperandOfTheSum * theFactorOfTheSecond",
				"        + theThirdOperandOfTheSum;",
				"",
				"    if theFirstConditionHolds && theSecondConditionHolds",
				"        || theThirdConditionHolds && theFourthConditionHolds",
				"    {",
				"        work();",
				"    }",
				"}",
			)},
		{"a pipeline that does not fit has a step to a line",
			"fn main() { let short = xs |> arrays.sum; let long = words |> arrays.map(word -> strings.lower(word)) |> arrays.filter(word -> strings.len(word) > 3) |> arrays.take(10); }",
			lines(
				"fn main() {",
				"    let short = xs |> arrays.sum;",
				"",
				"    let long = words",
				"        |> arrays.map(word -> strings.lower(word))",
				"        |> arrays.filter(word -> strings.len(word) > 3)",
				"        |> arrays.take(10);",
				"}",
			)},
		{"the cases of a match are a line each, and end with a semicolon where they are values",
			"fn name(l: Light) -> String { return match l { case Light.Red: \"red\", case Light.Amber | Light.Green: \"go\", default: \"?\" }; }\nfn show(l: Light) { match l { case Light.Red: { stop(); } case Light.Green if clear(): go(); default: {} } }",
			lines(
				"fn name(l: Light) -> String {",
				"    return match l {",
				"        case Light.Red: \"red\";",
				"        case Light.Amber | Light.Green: \"go\";",
				"        default: \"?\";",
				"    };",
				"}",
				"",
				"fn show(l: Light) {",
				"    match l {",
				"        case Light.Red: {",
				"            stop();",
				"        }",
				"",
				"        case Light.Green if clear(): go();",
				"        default: {}",
				"    }",
				"}",
			)},
		{"an impl says what it is for: a struct, a trait for a struct, arrays",
			"impl<T:Show> Box<T>{}\nimpl<T> Source<T> for Box<T>{fn next()->T?{return null;}}\nimpl<T> Sized for [T]{fn size()->Int{return 0;}}\nimpl [Int]{}\nimpl Show for geo.Point{}",
			lines(
				"impl<T: Show> Box<T> {}",
				"",
				"impl<T> Source<T> for Box<T> {",
				"    fn next() -> T? {",
				"        return null;",
				"    }",
				"}",
				"",
				"impl<T> Sized for [T] {",
				"    fn size() -> Int {",
				"        return 0;",
				"    }",
				"}",
				"",
				"impl [Int] {}",
				"",
				"impl Show for geo.Point {}",
			)},
		{"operators of one level break together, and the tighter ones stay whole",
			"fn main() { let found = theFirstNameThatWasGiven ?? theSecondNameThatWasGiven ?? theNameForEverybody == theNameThatWasExpectedHere; let count = theBasket |> arrays.filter(item -> item.price > theLimit) |> arrays.len ?? 0 > 3; }",
			lines(
				"fn main() {",
				"    let found = theFirstNameThatWasGiven ?? theSecondNameThatWasGiven ?? theNameForEverybody",
				"        == theNameThatWasExpectedHere;",
				"",
				"    let count = theBasket |> arrays.filter(item -> item.price > theLimit) |> arrays.len ?? 0 > 3;",
				"}",
			)},
		{"an enum is on one line or a variant to a line, as it was started; a struct is a field to a line",
			"enum A{X,Y(Int),}\nenum B{\nX,Y(Int,String)}\nstruct P{x:Int,y:Int=0}",
			lines(
				"enum A { X, Y(Int) }",
				"",
				"enum B {",
				"    X,",
				"    Y(Int, String),",
				"}",
				"",
				"struct P {",
				"    x: Int;",
				"    y: Int = 0;",
				"}",
			)},
		{"an if that gives a value is on one line when it fits",
			"fn main() { let size = if n>10{\"big\"}else{\"small\"}; let cost = if n>10{log(n);n*2}else{n}; }",
			lines(
				"fn main() {",
				"    let size = if n > 10 { \"big\" } else { \"small\" };",
				"",
				"    let cost = if n > 10 {",
				"        log(n);",
				"        n * 2",
				"    } else {",
				"        n",
				"    };",
				"}",
			)},
		{"operators get spaces, and what would read as another token keeps its distance",
			"fn main() { let a=-b+c*-d; let e=- -f; let g=!h&&~i==j; let k=l<<2|m>>3; k<<=1; let n=o? .p; let q=r?.s??t; let u=v as Int; let w=x is String?; for i in 0..=n-1{} let y=z[0].1; let f=fn(a:Int,b:...Int)->[Int]?{return null;}; }",
			lines(
				"fn main() {",
				"    let a = -b + c * -d;",
				"    let e = - -f;",
				"    let g = !h && ~i == j;",
				"    let k = l << 2 | m >> 3;",
				"    k <<= 1;",
				"    let n = o? .p;",
				"    let q = r?.s ?? t;",
				"    let u = v as Int;",
				"    let w = x is String?;",
				"    for i in 0..=n - 1 {}",
				"    let y = z[0].1;",
				"",
				"    let f = fn(a: Int, b: ...Int) -> [Int]? {",
				"        return null;",
				"    };",
				"}",
			)},
		{"type arguments are told from comparisons",
			"fn main() { let a=first<second; let b=max<Int>(1,2); let c=Option<Int>.None; let d=a<b&&c>d; let e=dict.empty<[String]>(); let f=Dict<List<Int>>{items:[]}; }",
			lines(
				"fn main() {",
				"    let a = first < second;",
				"    let b = max<Int>(1, 2);",
				"    let c = Option<Int>.None;",
				"    let d = a < b && c > d;",
				"    let e = dict.empty<[String]>();",
				"    let f = Dict<List<Int>> { items: [] };",
				"}",
			)},
		{"an array of plain values fills its lines",
			"const squares = [0, 1, 4, 9, 16, 25, 36, 49, 64, 81, 100, 121, 144, 169, 196, 225, 256, 289, 324, 361, 400, 441, 484, 529, 576, 625];",
			lines(
				"const squares = [",
				"    0, 1, 4, 9, 16, 25, 36, 49, 64, 81, 100, 121, 144, 169, 196, 225, 256, 289, 324, 361, 400, 441,",
				"    484, 529, 576, 625,",
				"];",
			)},
		{"tabs and the line ends of other systems are not kept",
			"fn main() {\r\n\tlet a = 1;\t// one\r\n\tif a > 0 {\r\n\t\twork();\r\n\t}\r\n}\r\n",
			lines(
				"fn main() {",
				"    let a = 1;  // one",
				"",
				"    if a > 0 {",
				"        work();",
				"    }",
				"}",
			)},
		{"a file of comments, and a file of nothing",
			"\n\n// only this\n\n\n// and this\n",
			lines(
				"// only this",
				"",
				"// and this",
			)},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := formatted(t, c.src)
			if got != c.want {
				t.Fatalf("formatting\n%s\ngave\n%s\nwanted\n%s\n%s", c.src, got, c.want, firstDifference(c.want, got))
			}
			if again := formatted(t, got); again != got {
				t.Errorf("formatting it again changed it:\n%s", firstDifference(got, again))
			}
		})
	}

	if got := formatted(t, ""); got != "" {
		t.Errorf("an empty text has to stay empty, got %q", got)
	}
	if got := formatted(t, " \n\n\t\n"); got != "" {
		t.Errorf("a text of spaces has to become empty, got %q", got)
	}
}

// A text between """ is a value: its lines move with the line it starts
// on, when that keeps the value, and stay where they are when it would
// not.
func TestTextsOverSeveralLinesKeepTheirValue(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"moved in with what holds it",
			"fn main() {\nlet help = \"\"\"\nusage:\n  add\n\n\"\"\";\n}",
			lines(
				"fn main() {",
				"    let help = \"\"\"",
				"    usage:",
				"      add",
				"",
				"    \"\"\";",
				"}",
			)},
		{"moved out",
			"fn main() {\n            let help = \"\"\"\n                usage:\n                  add\n                \"\"\";\n}",
			lines(
				"fn main() {",
				"    let help = \"\"\"",
				"        usage:",
				"          add",
				"        \"\"\";",
				"}",
			)},
		{"left alone when a line has less room than the closing quotes",
			"fn main() {\n        let help = \"\"\"\n    usage:\n        \"\"\";\n}",
			lines(
				"fn main() {",
				"    let help = \"\"\"",
				"    usage:",
				"        \"\"\";",
				"}",
			)},
		{"left alone when the closing quotes follow text",
			"fn main() {\n        let help = \"\"\"\n            usage:\n            add\"\"\";\n}",
			lines(
				"fn main() {",
				"    let help = \"\"\"",
				"            usage:",
				"            add\"\"\";",
				"}",
			)},
		{"what is inserted is not touched",
			"fn main() { println(\"${ a+b } and ${names[\"x\"]} and ${Point{x:1}.x}\", \"\"\"{\"a\": ${ n }}\"\"\"); }",
			lines(
				"fn main() {",
				"    println(\"${ a+b } and ${names[\"x\"]} and ${Point{x:1}.x}\", \"\"\"{\"a\": ${ n }}\"\"\");",
				"}",
			)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := formatted(t, c.src)
			if got != c.want {
				t.Fatalf("formatting\n%s\ngave\n%s\nwanted\n%s\n%s", c.src, got, c.want, firstDifference(c.want, got))
			}
			before, _ := program(c.src)
			if after, _ := program(got); after != before {
				t.Errorf("the text changed its value")
			}
		})
	}
}

// A text that does not read as a program is refused, with the place
// where it stops reading as one; nothing is formatted by halves.
func TestProblems(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"a missing semicolon", "fn main() {\n    let a = 1\n    let b = 2;\n}", "3:5: expected ';', found 'let'"},
		{"a missing brace", "fn main() {\n    work();\n", "3:1: expected '}', found the end of the file"},
		{"a brace too many", "fn main() {}\n}", "2:1: expected a declaration or a statement, found '}'"},
		{"an expression that is missing", "fn main() { let a = ; }", "1:21: expected an expression, found ';'"},
		{"a type that is missing", "fn f(a: ) {}", "1:9: expected a type, found ')'"},
		{"a parenthesis left open", "fn main() { call(1, 2; }", "1:22: expected ')', found ';'"},
		{"a text left open", "fn main() {\n    println(\"hello);\n}", "2:13: unterminated string literal"},
		{"a text over several lines left open", "const a = \"\"\"\nhello", "1:11: unterminated string literal"},
		{"an insertion left open", "const a = \"${name\";", "1:18: unterminated string literal"},
		{"a comment left open", "fn main() {}\n/* never closed", "2:1: unterminated block comment"},
		{"a character that is no token", "fn main() { let a = 1 # 2; }", "1:23: unexpected character '#'"},
		{"a case without its end", "fn f() { match a { case 1: b } }", "1:30: expected ';', found '}'"},
		{"a value case without its end", "fn f() { let x = match a { case 1: b c }; }", "1:38: expected ';' after the value of a case, found 'c'"},
		{"an arrow from another language", "fn main() { let f = x => x; }", "1:23: expected '->', found '=>'"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tidy, problem := format(t, c.src)
			if tidy != "" || problem != c.want {
				t.Errorf("expected the problem %q, got %q and the text %q", c.want, problem, tidy)
			}
		})
	}
}

// The programs of testdata are written the way nobody should, and each
// comes with what it has to become: name.txt, and name.qi.
//
//	depot       all of the language, packed as tightly as it parses
//	comments    a comment in every place one can be written
//
// Run the tests with UPDATE_FORMAT=1 to write the .qi files again, and
// read the difference before keeping it.
func TestDensePrograms(t *testing.T) {
	inputs, err := filepath.Glob(filepath.Join("testdata", "*.txt"))
	if err != nil || len(inputs) < 2 {
		t.Fatalf("expected the programs of testdata, found %v (%v)", inputs, err)
	}
	for _, input := range inputs {
		t.Run(strings.TrimSuffix(filepath.Base(input), ".txt"), func(t *testing.T) {
			dense, err := os.ReadFile(input)
			if err != nil {
				t.Fatal(err)
			}
			got := formatted(t, string(dense))
			wanted := strings.TrimSuffix(input, ".txt") + ".qi"
			if os.Getenv("UPDATE_FORMAT") != "" {
				if err := os.WriteFile(wanted, []byte(got), 0644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(wanted)
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Errorf("not formatted as %s has it:\n%s", wanted, firstDifference(string(want), got))
			}
			if again := formatted(t, got); again != got {
				t.Errorf("formatting it again changed it:\n%s", firstDifference(got, again))
			}

			before, parses := program(string(dense))
			after, stillParses := program(got)
			if !parses || !stillParses || before != after {
				t.Errorf("it has to parse, before and after, as the same program (%v, %v)", parses, stillParses)
			}
			for i, line := range strings.Split(got, "\n") {
				if len([]rune(line)) > 100 {
					t.Errorf("line %d is longer than 100 characters: %s", i+1, line)
				}
			}
		})
	}
}

// The tools written in Qoqi are written the way the formatter writes:
// what formats has to be formatted.
func TestTheToolsAreFormatted(t *testing.T) {
	root := repository(t)
	for _, name := range []string{
		"internal/stdlib/lib/layout.qi",
		"internal/stdlib/lib/formatter.qi",
		"internal/formatter/fmt.qi",
	} {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		if got := formatted(t, string(data)); got != string(data) {
			t.Errorf("%s is not formatted: run qoqi fmt on it\n%s", name, firstDifference(string(data), got))
		}
	}
}
