package stdlib_test

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/qoqiflow/qoqi/internal/compiler"
	"github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/vm"
)

func newListener() (net.Listener, error) {
	return net.Listen("tcp", "127.0.0.1:0")
}

// quote writes a path as a Qoqi string literal.
func quote(path string) string {
	return fmt.Sprintf("%q", path)
}

func TestPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the expected paths are written with /")
	}
	expect(t, "import std.path;", `
		println(path.join(["a", "b/", "../c.txt"]), path.inside("notes", "todo.txt"), path.join([]) == "");
		println(path.base("notes/todo.txt"), path.dir("notes/todo.txt"), path.extension("a/b.tar.gz"), "[" + path.extension("a/b") + "]");
		println(path.stem("x/y.tar.gz"), path.stem("readme"), path.stem(".hidden"));
		// The dots a name starts with are not an extension
		println(path.stem(".bashrc"), "[" + path.extension(".bashrc") + "]", path.withExtension(".bashrc", ".bak"), path.withExtension("home/.bashrc", ".bak"));
		println(path.extension(".config.json"), path.stem("a/.config.json"), "[" + path.extension("..") + "]", path.stem("archive.tar.gz"), path.withExtension("archive.tar.gz", ".zip"));
		println(path.withExtension("a/b.md", ".html"), path.withExtension("b.md", ".html"), path.withExtension("./b", ".txt"), path.withExtension("/b.md", ""));
		println(path.parts("/a/b/c.txt"), path.parts("a//b/../c"), path.parts(""), path.parts("/"));
		println(path.clean("a//b/../c/"), path.isAbsolute("/a"), path.isAbsolute("a"), path.separator());
		println(path.matches("*.txt", "todo.txt"), path.matches("*.txt", "todo.md"), path.isAbsolute(path.absolute("x")));
	`,
		"a/c.txt notes/todo.txt true",
		"todo.txt notes .gz []",
		"y.tar readme .hidden",
		".bashrc [] .bashrc.bak home/.bashrc.bak",
		".json .config [] archive.tar archive.tar.zip",
		"a/b.html b.html b.txt /b",
		`["a", "b", "c.txt"] ["a", "c"] ["."] []`,
		"a/c true false /",
		"true false true",
	)
	expectFailure(t, "import std.path;", `println(path.matches("[", "x"));`, "std.path.matches: '[' is not a valid pattern")

	// From one folder to another path
	expect(t, "import std.path;", `
		println(path.relative("app", "app/vendor/x"), path.relative("app/src", "app/lib"), path.relative("/a/b", "/a/b"));
		println(path.relative("/a/b", "/c/d.txt"), path.relative("a", "../b"), path.relative(path.absolute("x"), "x/y"));
		let back = path.relative("/srv/app", "/srv/app/vendor/acme/log");
		println(back, path.join(["/srv/app", back ?? ""]));
	`,
		"vendor/x ../lib .",
		"../../c/d.txt ../../b y",
		"vendor/acme/log /srv/app/vendor/acme/log",
	)
}

func TestFolders(t *testing.T) {
	dir := t.TempDir()
	expect(t, `
		import std.fs;
		import std.fs.{Kind, Entry};
		import std.io;
		import std.io.{ErrorKind};
		import std.path;
		import std.result.{Result};
		import std.arrays;
		import std.strings;`, `
		let root = `+quote(dir)+`;
		let deep = fs.makeDir(path.join([root, "x", "y"])).expect("a folder");
		println(fs.isDir(deep), fs.isFile(deep), fs.makeDir(deep).isOk());
		io.writeFile(path.inside(deep, "a.txt"), "hello").expect("a file");
		io.writeFile(path.inside(root, "b.qi"), "").expect("a file");

		// One level, sorted, with what each entry is
		for entry in fs.list(root).expect("a listing") {
			println(entry.name, entry.kind, entry.isFile(), entry.isDir(), entry.size, entry.path == path.inside(root, entry.name));
		}
		let about = fs.info(path.inside(deep, "a.txt")).expect("facts");
		println(about.name, about.size, about.kind == Kind.File, about.modified > 0);

		// Every file, however deep
		let relative = fn(file: String) -> String { return file.from(root.len() + 1); };
		println(fs.walk(root).expect("a walk").map(relative));
		println(fs.find(root, "*.txt").expect("a search").map(relative), fs.find(root, "*.go").expect("a search").len());

		let copied = fs.copy(path.inside(deep, "a.txt"), path.inside(root, "c.txt")).expect("a copy");
		println(path.base(copied), io.readFile(copied).or("?"));
		let moved = fs.rename(copied, path.inside(root, "d.txt")).expect("a move");
		println(path.base(moved), io.exists(copied), io.exists(moved));

		// Failures say what, on what, and of which kind
		match fs.list(path.inside(root, "nope")) {
			case Result.Ok(_): { println("?"); }
			case Result.Err(error): {
				println(error.kind, error.operation, path.base(error.path), error.message);
				println(error.kind == ErrorKind.NotFound, error.stack.len(), error.stack[0].function);
			}
		}
		let problems = [
			fs.list(moved).reason(),
			fs.info(path.inside(root, "nope")).reason(),
			fs.walk(path.inside(root, "nope")).reason(),
			fs.copy(path.inside(root, "nope"), moved).reason(),
			fs.copy(deep, moved).reason(),
			fs.rename(path.inside(root, "nope"), moved).reason(),
			fs.makeDir(path.inside(moved, "inside")).reason(),
		];
		for problem in problems {
			if problem != null { println(problem.operation, problem.kind, path.base(problem.path), problem.stack.len(), problem.stack[0].function); }
		}

		println(fs.removeAll(path.inside(root, "x")).isOk(), fs.isDir(deep), fs.removeAll(path.inside(root, "x")).isOk());
		let temp = fs.makeTempDir("qoqi-test-").expect("a folder");
		println(fs.isDir(temp), path.base(temp).startsWith("qoqi-test-"), fs.removeAll(temp).isOk(), fs.isDir(temp));
		println(fs.isDir(fs.currentDir()), fs.isDir(fs.tempDir()));
	`,
		"true false true",
		"b.qi File true false 0 true",
		"x Dir false true 0 true",
		"a.txt 5 true true",
		`["b.qi", "x/y/a.txt"]`,
		`["x/y/a.txt"] 0`,
		"c.txt hello",
		"d.txt false true",
		"NotFound list nope no such file or directory",
		"true 1 main",
		"list NotDirectory d.txt 1 main",
		"inspect NotFound nope 1 main",
		"walk NotFound nope 1 main",
		"copy NotFound nope 1 main",
		"copy IsDirectory y 1 main",
		"rename NotFound nope 1 main",
		"create NotDirectory inside 1 main",
		"true false true",
		"true true true false",
		"true true",
	)
}

func TestOs(t *testing.T) {
	t.Setenv("QOQI_TEST_VARIABLE", "a=b")
	expect(t, "import std.os;\nimport std.strings;", `
		println(os.args(), os.platform() == `+quote(runtime.GOOS)+`);
		let all = os.environment();
		println(all.get("QOQI_TEST_VARIABLE"), all.get("QOQI_NO_SUCH_VARIABLE"), all.len() > 1);
	`,
		"[] true",
		"a=b null true",
	)
	if runtime.GOOS == "windows" {
		return
	}
	expect(t, "import std.os;\nimport std.io.{ErrorKind};\nimport std.result.{Result};\nimport std.arrays;", `
		let out = os.run("sh", ["-c", "echo one; echo two; echo no >&2; exit 3"]).expect("a run");
		println(out.status, out.lines(), out.err == "no\n", out.succeeded());
		let shouted = os.runWithInput("tr", ["a-z", "A-Z"], "hello").expect("a run");
		println(shouted.out, shouted.status, shouted.succeeded());
		// A program that cannot be started is the error; one that fails is not
		match os.run("no-such-program-of-qoqi", ["x"]) {
			case Result.Ok(_): { println("?"); }
			case Result.Err(error): { println(error.describe(), error.kind == ErrorKind.NotFound, error.stack.len()); }
		}
		println(os.runWithInput("no-such-program-of-qoqi", [], "x").isErr());
	`,
		`3 ["one", "two"] true false`,
		"HELLO 0 true",
		"cannot run no-such-program-of-qoqi: there is no program called 'no-such-program-of-qoqi' true 1",
		"true",
	)

	// A command told where to run, with what in its environment and input
	dir := t.TempDir()
	t.Setenv("QOQI_KEPT", "kept")
	expect(t, "import std.os;\nimport std.arrays;", `
		let out = os.command("sh", ["-c", "basename \"$(pwd)\"; echo $GREETING $QOQI_KEPT; cat; exit 4"])
			.inDir(`+quote(dir)+`)
			.env("GREETING", "hello")
			.env("GREETING", "hi")
			.withInput("piped")
			.run()
			.expect("a run");
		println(out.lines(), out.status);
		let plain = os.command("sh", ["-c", "echo $GREETING."]).run().expect("a run");
		println(plain.out.trim(), os.command("no-such-program-of-qoqi", []).inDir("/").run().isErr());
		println(os.command("true", []).inDir(`+quote(dir+"/missing")+`).run().isErr());
	`,
		`["`+filepath.Base(dir)+`", "hi kept", "piped"] 4`,
		". true",
		"true",
	)
}

// TestExit checks that os.exit ends the program at once, whatever is
// around the call, and hands its status to whoever ran the program.
func TestExit(t *testing.T) {
	cases := []struct {
		name, body, printed string
		status              int
	}{
		{"plain", `println("before"); os.exit(3); println("after");`, "before\n", 3},
		{"zero", `os.exit(0); println("after");`, "", 0},
		{"deep in calls", `let leave = fn(n: Int) { if n > 2 { os.exit(n); } }; for i in arrays.range(0, 9) { println(i); leave(i); }`, "0\n1\n2\n3\n", 3},
		// An attempt survives failures; leaving is not one
		{"inside attempt", `let r = attempt(fn() -> Int { os.exit(7); return 1; }); println("after", r.isOk());`, "", 7},
		{"inside guard, nested", `guard(fn() { guard(fn() { os.exit(9); }); println("inner"); }); println("after");`, "", 9},
		{"after a caught failure", `println(attempt(fn() -> Int { panic("x"); }).isErr()); os.exit(4);`, "true\n", 4},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := run(t, "import std.os;\nimport std.arrays;\nimport std.result.{attempt, guard};", c.body, "")
			status, asked := vm.ExitCode(err)
			if !asked || status != c.status || out != c.printed {
				t.Errorf("expected status %d after printing %q, got %v (%v) after %q", c.status, c.printed, status, err, out)
			}
		})
	}
}

func TestJson(t *testing.T) {
	expect(t, `
		import std.json;
		import std.json.{Json};
		import std.option;
		import std.result.{Result};
		import std.arrays;`, `
		let text = "{\"name\": \"ada\", \"age\": 36, \"tags\": [\"a\", \"b\"], \"x\": {\"y\": [1, 2.5, null, true]}, \"e\": {}, \"l\": []}";
		let doc = json.parse(text).expect("a document");
		println(doc.stringify());
		println(doc.pretty());

		// Looking in, a step at a time or along a path
		println(option.then(doc.get("name"), json.textOf) ?? "nobody", option.then(doc.get("age"), json.intOf), option.then(doc.get("nope"), json.textOf));
		println(doc.find("x.y.1"), doc.find("x.y.2"), doc.find("x.y.9"), doc.find("tags.0"), doc.find("name.first"), doc.find("x.nope.y"));
		println(doc.get("name"), doc.at(0), (doc.find("tags") ?? Json.Null).at(1));
		let tags = option.then(doc.get("tags"), json.itemsOf) ?? [];
		println(tags.len(), tags.map(fn(tag: Json) -> String { return tag.text() ?? "?"; }));
		let fields = doc.fields();
		if fields != null { println(fields.keys(), doc.isNull(), (doc.find("x.y.2") ?? Json.Bool(false)).isNull()); }

		// Each kind answers only for itself
		let kinds = [Json.Null, Json.Bool(true), Json.Number(2.0), Json.Number(2.5), Json.String("s"), json.ints([1]), json.object().done()];
		for kind in kinds {
			println(kind.stringify(), kind.text(), kind.number(), kind.int(), kind.bool(), kind.items() != null, kind.fields() != null);
		}

		// Every kind of value is taken apart by a match the compiler checks
		let describe = fn(value: Json) -> String {
			match value {
				case Json.Null: { return "nothing"; }
				case Json.Bool(b): { return "a boolean"; }
				case Json.Number(x): { return "a number"; }
				case Json.String(s): { return "a text of ${(json.texts([s]).items() ?? []).len()}"; }
				case Json.Array(items): { return "${items.len()} items"; }
				case Json.Object(fields): { return "${fields.len()} fields"; }
			}
		};
		println(describe(doc), describe(Json.Null), describe(doc.find("tags") ?? Json.Null), describe(Json.String("x")));
	`,
		`{"name":"ada","age":36,"tags":["a","b"],"x":{"y":[1,2.5,null,true]},"e":{},"l":[]}`,
		`{`,
		`  "name": "ada",`,
		`  "age": 36,`,
		`  "tags": [`,
		`    "a",`,
		`    "b"`,
		`  ],`,
		`  "x": {`,
		`    "y": [`,
		`      1,`,
		`      2.5,`,
		`      null,`,
		`      true`,
		`    ]`,
		`  },`,
		`  "e": {},`,
		`  "l": []`,
		`}`,
		"ada 36 null",
		`2.5 null null "a" null null`,
		`"ada" null "b"`,
		`2 ["a", "b"]`,
		`["name", "age", "tags", "x", "e", "l"] false true`,
		"null null null null null false false",
		"true null null null true false false",
		"2 null 2 2 null false false",
		"2.5 null 2.5 null null false false",
		`"s" s null null null false false`,
		"[1] null null null null true false",
		"{} null null null null false true",
		"6 fields nothing 2 items a text of 1",
	)

	// Writing, and reading back what was written
	expect(t, "import std.json;\nimport std.json.{Json};\nimport std.option;", `
		let made = json.object()
			.text("name", "é\"x\n\t\\")
			.int("n", -3)
			.number("x", 0.5)
			.bool("ok", true)
			.none("z")
			.set("t", json.ints([1, 2]))
			.set("w", json.texts(["a"]))
			.set("p", json.array([[1, 2], [3]], json.ints))
			.done();
		let text = made.stringify();
		println(text);
		let back = json.parse(text).expect("what was written");
		println(back == made, made.equals(back), back != made, back.stringify() == text);
		println(json.parse(made.pretty()).expect("the long form").equals(made));
		println(option.then(back.get("name"), json.textOf) == "é\"x\n\t\\");

		// == is by content, in order for arrays and by key for objects
		let a = json.parse("{\"a\": 1, \"b\": [1, 2]}").expect("a");
		println(a == json.parse("{\"b\": [1, 2], \"a\": 1}").expect("b"),
			a == json.parse("{\"a\": 1, \"b\": [2, 1]}").expect("c"),
			a == json.parse("{\"a\": 1}").expect("d"),
			a == json.parse("{\"a\": 1, \"b\": [1, 2], \"c\": 0}").expect("e"),
			a == Json.Null, Json.Null == Json.Null, json.int(1) == Json.Number(1.0), Json.String("1").equals(json.int(1)));
		println(json.parse(" [ ] ").expect("x").stringify(), json.parse("1e3").expect("x").int(), json.parse("\"\\u00e9\"").expect("x").text(), json.parse("-0.5").expect("x").number());
		println(json.int(7).pretty(), json.ints([]).pretty(), json.texts(["a"]).pretty() == "[\n  \"a\"\n]");
	`,
		`{"name":"é\"x\n\t\\","n":-3,"x":0.5,"ok":true,"z":null,"t":[1,2],"w":["a"],"p":[[1,2],[3]]}`,
		"true true false true",
		"true",
		"true",
		"true false false false false true true false",
		"[] 1000 é -0.5",
		"7 [] true",
	)

	// What is not JSON says where
	expect(t, "import std.json;\nimport std.result.{Result};", `
		let wrong = ["", "[1, 2", "1 2", "{\"a\": [1,\n }", "{\"a\" 1}", "[1,]", "nul", "{'a': 1}", "[\n\n  é]"];
		for text in wrong {
			match json.parse(text) {
				case Result.Ok(document): { println("?", document); }
				case Result.Err(error): { println(error.line, error.column, error.describe()); }
			}
		}
	`,
		"1 1 line 1, column 1: the text ends before the value does",
		"1 6 line 1, column 6: the text ends before the value does",
		"1 3 line 1, column 3: there is something after the value",
		"2 2 line 2, column 2: invalid character '}' looking for beginning of value",
		"1 6 line 1, column 6: invalid character '1' after object key",
		"1 4 line 1, column 4: invalid character ']' looking for beginning of value",
		"1 4 line 1, column 4: the text ends before the value does",
		"1 2 line 1, column 2: invalid character '\\'' looking for beginning of object key string",
		"3 3 line 3, column 3: invalid character 'é' looking for beginning of value",
	)

	// A number no Float holds is not read as 0, and says where it is
	expect(t, "import std.json;\nimport std.result.{Result};", `
		for text in ["1e400", "[1,\n  -2e999]", "{\"big\": 1e308}"] {
			match json.parse(text) {
				case Result.Ok(document): { println(document.stringify()); }
				case Result.Err(error): { println(error.line, error.column, error.describe()); }
			}
		}
	`,
		"1 1 line 1, column 1: the number 1e400 is out of range",
		"2 3 line 2, column 3: the number -2e999 is out of range",
		`{"big":1e+308}`,
	)

	// A whole number is an Int only as far as an Int reaches
	expect(t, "import std.json;\nimport std.json.{Json};\nimport std.arrays;", `
		let numbers = json.parse("[140737488355327, -140737488355327, 140737488355328, -140737488355328, 1e15, 1e300, 2.5]").expect("numbers");
		println((numbers.items() ?? []).map(json.intOf));
		println(Json.Number(1e20).int(), Json.Number(0.0 / 0.0).int(), json.parse("12").expect("a number").int());
	`,
		"[140737488355327, -140737488355327, null, null, null, null, null]",
		"null null 12",
	)

	// What done gave stays as it was when the builder goes on
	expect(t, "import std.json;", `
		let builder = json.object().text("name", "ada");
		let first = builder.done();
		let second = builder.int("age", 36).text("name", "grace").done();
		println(first.stringify(), second.stringify());
		println(builder.none("z").done().stringify(), first.stringify());
	`,
		`{"name":"ada"} {"name":"grace","age":36}`,
		`{"name":"grace","age":36,"z":null} {"name":"ada"}`,
	)

	// A key with a dot is found by get, not find
	expect(t, "import std.json;", `
		let doc = json.parse("{\"a.b\": 1, \"a\": {\"b\": 2}}").expect("a document");
		println(doc.get("a.b"), doc.find("a.b"));
	`,
		"1 2",
	)
}

func TestRegex(t *testing.T) {
	expect(t, `
		import std.regex;
		import std.regex.{Match, Regex};
		import std.result.{Result};
		import std.strings;
		import std.arrays;`, `
		let date = regex.must("(?P<year>\\d{4})-(\\d{2})-(\\d{2})");
		let found = date.find("été: released on 2026-10-13 and 2027-01-02");
		if found != null {
			println(found.text, found.start, found.end, found.groups);
			println(found.named("year"), found.named("month"), found.named(""), found.group(3), found.group(4), found.group(-1));
			// Counted as $n is: 0 the whole match, 1 the first group
			println(found.group(0), found.group(1), found.group(1) == found.groups[0]);
		}
		println(date.find("no date here"), date.matches("on 2026-10-13"), date.matches("2026-1-1"));
		println(date.all("2026-10-13 and 2027-01-02"), date.findAll("").len(), date.names);
		println(date.replace("on 2026-10-13", "$3/$2/\${year}"), date.replace("nothing", "x"));
		// $0 is the whole match; a name runs on through _, which \${1} stops
		println(date.replace("on 2026-10-13", "[$0]"), date.replace("2026-10-13", "$1_x"), date.replace("2026-10-13", "\${1}_x"));

		let numbers = regex.must("\\d+");
		println(numbers.replaceWith("3 apples, 12 pears", fn(m: Match) -> String { return toString((m.text.parseInt() ?? 0) * 2); }));
		println(numbers.replaceWith("none", fn(m: Match) -> String { return "?"; }), numbers.replaceWith("7", fn(m: Match) -> String { return "<${m.start}-${m.end}>"; }));
		println(regex.must("\\s*,\\s*").split("a , b,c"), regex.must(",").split(""), regex.must("x").split("abc"));
		println(regex.escape("1+1?"), regex.must(regex.escape("1+1?")).matches("is 1+1?"), regex.must("^1+1?$").matches("1+1?"));

		// A group that took no part, and a pattern without groups
		let either = regex.must("(a)|(b)").find("b");
		if either != null { println(either.groups, numbers.findAll("42")[0].groups.len()); }
		// Case insensitive, across accents (\\w and \\b only know the letters of English)
		println(regex.must("(?i)é[a-zé]+").all("Été, étude et Ecole"));

		match regex.compile("(a") {
			case Result.Ok(_): { println("?"); }
			case Result.Err(error): { println(error.pattern, "/", error.message, "/", error.describe().startsWith("'(a' is not a regular expression")); }
		}
		println(regex.compile("[a-z]+").isOk(), regex.compile("a{2,1}").isErr(), regex.compile("(?<=a)b").isErr());
	`,
		`2026-10-13 17 27 ["2026", "10", "13"]`,
		"2026 null null 13 null null",
		"2026-10-13 2026 true",
		"null true false",
		`["2026-10-13", "2027-01-02"] 0 ["year", "", ""]`,
		"on 13/10/2026 nothing",
		"on [2026-10-13]  2026_x",
		"6 apples, 24 pears",
		"none <0-1>",
		`["a", "b", "c"] [""] ["abc"]`,
		`1\+1\? true false`,
		`["", "b"] 0`,
		`["Été", "étude"]`,
		"(a / missing closing ): `(a` / true",
		"true true true",
	)
	expectFailure(t, "import std.regex;", `regex.must("(a");`, "'(a' is not a regular expression")
}

func TestCsv(t *testing.T) {
	expect(t, `
		import std.csv;
		import std.csv.{Table};
		import std.dict.{Dict};
		import std.result.{Result};
		import std.strings;
		import std.arrays;`, `
		let people = csv.table("name,age,city\nada,36,London\n\"grace, h\",45,\"New\nYork\"\nalan,41,\n").expect("a table");
		println(people.header, people.len(), people.column("name"), people.column("nope"));
		println(people.cell(1, "age"), people.cell(1, "city") == "New\nYork", people.cell(2, "city") == "", people.cell(9, "age"), people.cell(0, "nope"), people.cell(-1, "age"));
		let records = people.records();
		println(records.len(), records[0].get("age"), records[1].keys());

		// The rows a function accepts, and the table back as text
		let older = people.where(fn(row: Dict<String>) -> Bool { return ((row.get("age") ?? "").parseInt() ?? 0) > 40; });
		println(older.len(), older.column("name"));
		print(older.write());
		println(csv.table(people.write()).expect("what was written").rows == people.rows, csv.table(people.write()).expect("x").write() == people.write());

		println(csv.parse("a,b\n1\n").expect("rows"), csv.parse("").expect("rows"), csv.parseWith("a;b,c\n", ";").expect("rows"), csv.parseWith("a\tb", "\t").expect("rows"));
		print(csv.write([["a b", "c\"d"], ["e,f", ""]]));
		print(csv.writeWith([["a,b", "c;d"]], ";"));

		let wrong = [csv.table("a,b\n1\n").reason(), csv.table("").reason(), csv.table("a,b\n1,2\n3,4,5\n").reason()];
		for error in wrong { if error != null { println(error.line, error.column, error.describe()); } }
		match csv.parse("a,b\nc,\"d\n") {
			case Result.Ok(rows): { println("?", rows); }
			case Result.Err(error): { println(error.line, error.describe().contains("line 2, column")); }
		}
	`,
		`["name", "age", "city"] 3 ["ada", "grace, h", "alan"] null`,
		"45 true true null null null",
		`3 36 ["name", "age", "city"]`,
		`2 ["grace, h", "alan"]`,
		"name,age,city",
		`"grace, h",45,"New`,
		`York"`,
		"alan,41,",
		"true true",
		`[["a", "b"], ["1"]] [] [["a", "b,c"]] [["a", "b"]]`,
		`a b,"c""d"`,
		`"e,f",`,
		`a,b;"c;d"`,
		"2 1 line 2, column 1: expected 2 values, as in the header, found 1",
		"0 0 there is no header row",
		"3 1 line 3, column 1: expected 2 values, as in the header, found 3",
		"2 true",
	)
	expectFailure(t, "import std.csv;", `csv.parseWith("a", "::");`, "std.csv.parseWith: the separator must be one character, got '::'")

	// A byte-order mark in front is not part of the first name
	expect(t, "import std.csv;", `
		let marked = csv.table("\u{FEFF}name,age\nada,36\n").expect("a table");
		println(marked.header, marked.header[0].len(), marked.column("name"));
		println(csv.parseWith("\u{FEFF}a;b", ";").expect("rows"), csv.parse("\u{FEFF}").expect("rows"));
	`,
		`["name", "age"] 4 ["ada"]`,
		`[["a", "b"]] []`,
	)

	// The line a wrong row starts on, past values over several lines and
	// blank lines
	expect(t, "import std.csv;", `
		let texts = [
			"name,note\nada,\"one\ntwo\nthree\"\ngrace\n",
			"name,note\n\n\nada,x\n\ngrace,y,z\n",
			"\u{FEFF}a,b\n\"1\n2\",3\n4,\"5\n6\"\n7\n",
		];
		for text in texts {
			let error = csv.table(text).reason();
			if error != null { println(error.describe()); }
		}
	`,
		"line 5, column 1: expected 2 values, as in the header, found 1",
		"line 6, column 1: expected 2 values, as in the header, found 3",
		"line 6, column 1: expected 2 values, as in the header, found 1",
	)
}

// TestLibraryValuesShowAsSentences checks that the errors of the library,
// and a JSON document, print and read in a text as a person reads them,
// not as the fields they are made of.
func TestLibraryValuesShowAsSentences(t *testing.T) {
	if runtime.GOOS == "windows" {
		return
	}
	expect(t, `
		import std.io;
		import std.json;
		import std.json.{Json};
		import std.csv;
		import std.regex;`, `
		let document = json.parse("{\"a\": [1, 2.5, null], \"b\": \"x\"}").expect("a document");
		println(document);
		println("got ${document.get("a")} and ${document.show()}");

		let parsing = json.parse("[1,").reason();
		if parsing != null { println(parsing, "/", "${parsing}" == parsing.describe()); }

		let reading = io.readFile("no-such-file-of-qoqi.txt").reason();
		if reading != null { println(reading, "/", reading.show() == reading.describe()); }

		let table = csv.table("a,b\n1\n").reason();
		if table != null { println(table); }

		let pattern = regex.compile("(a").reason();
		if pattern != null { println("${pattern}"); }
	`,
		`{"a":[1,2.5,null],"b":"x"}`,
		`got [1,2.5,null] and {"a":[1,2.5,null],"b":"x"}`,
		"line 1, column 4: the text ends before the value does / true",
		"cannot read no-such-file-of-qoqi.txt: no such file or directory / true",
		"line 2, column 1: expected 2 values, as in the header, found 1",
		"'(a' is not a regular expression: missing closing ): `(a`",
	)

	// What expect says of the reason is its sentence
	expectFailure(t, "import std.json;", `json.parse("{").expect("a document");`, "a document: line 1, column 2: the text ends before the value does")
}

func TestEncodingAndHash(t *testing.T) {
	expect(t, "import std.encoding;\nimport std.hash;\nimport std.option;\nimport std.strings;", `
		println(encoding.toBase64("héllo"), encoding.fromBase64("aMOpbGxv"), encoding.fromBase64("!!"), encoding.fromBase64(encoding.toBase64("")) == "");
		println(encoding.toBase64Url("??>>"), encoding.fromBase64Url("Pz8-Pg"), encoding.toHex("hi"), encoding.fromHex("6869"), encoding.fromHex("zz"));
		println(encoding.toUrl("a b&c=é"), encoding.fromUrl("a+b%26c%3D%C3%A9"), encoding.fromUrl("%zz"));
		println(encoding.bytes("hé"), encoding.fromBytes([104, 105]), encoding.fromBytes(encoding.bytes("héλ☃")));
		// Bytes that are not UTF-8 make a text all the same, as fromHex does
		let raw: String = encoding.fromBytes([255, 0, 104]);
		println(encoding.bytes(raw), raw == encoding.fromHex("ff0068"), encoding.toHex(raw));
		// Base64 read with its padding or without, either alphabet's own
		println(encoding.fromBase64("aGk="), encoding.fromBase64("aGk"), encoding.fromBase64Url("aGk="), encoding.fromBase64Url("aGk"), encoding.fromBase64("Pz8-Pg"), encoding.fromBase64("a"));

		// There and back, chained: each step runs if the one before gave a text
		let round = fn(text: String) -> String? {
			return option.then(option.then(encoding.fromHex(encoding.toHex(encoding.toBase64(text))), encoding.fromBase64), encoding.fromUrl);
		};
		println(round("plain"), round(encoding.toUrl("a b/c?d=é")) == "a b/c?d=é");

		println(hash.sha256("abc"));
		println(hash.sha1("abc"), hash.md5("abc"), hash.crc32("abc"));
		println(hash.hmacSha256("Jefe", "what do ya want for nothing?"));
		println(hash.matches("abc", hash.sha256("abc")), hash.matches("abd", hash.sha256("abc")), hash.sha256("").len(), hash.sha256("a") == hash.sha256("a"));
	`,
		"aMOpbGxv héllo null true",
		"Pz8-Pg ??>> 6869 hi null",
		"a+b%26c%3D%C3%A9 a b&c=é null",
		"[104, 195, 169] hi héλ☃",
		"[255, 0, 104] true ff0068",
		"hi hi hi hi null null",
		"plain true",
		"ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
		"a9993e364706816aba3e25717850c26c9cd0d89d 900150983cd24fb0d6963f7d28e17f72 891568578",
		"5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843",
		"true false 64 true",
	)
	expectFailure(t, "import std.encoding;", `encoding.fromBytes([1, 256]);`, "a byte is between 0 and 255, element 1 is 256")
}

func TestFormat(t *testing.T) {
	expect(t, "import std.format;\nimport std.format.{Align};", `
		println(format.fixed(3.14159, 2), format.fixed(2.5, 0), format.fixed(1.0, 2), format.fixed(-0.125, 1), format.fixed(1234.5, 0));
		println(format.grouped(1234567), format.grouped(-1000), format.grouped(999), format.grouped(0), format.grouped(1000), format.groupedWith(1234567, " "), format.grouped(-123456));
		println(format.padded(7, 3), format.padded(-7, 3), format.padded(1234, 2), format.padded(0, 1), format.padded(5, 0));
		println(format.percent(0.125, 1), format.percent(1.0, 0), format.percent(0.0, 2));
		println(format.money(1234567.5), format.money(0.0), format.money(-0.004), format.money(-3.999), format.money(0.005), format.money(12.345), format.money(-1000.0));
		println(format.hex(255), format.binary(5), format.inBase(-255, 16), format.fromBase("ff", 16), format.fromBase("zz", 10), format.fromBase(format.inBase(123456789, 36), 36));
		println(format.bytes(0), format.bytes(512), format.bytes(1023), format.bytes(1024), format.bytes(1536000), format.bytes(5368709120), format.bytes(-2048));
		println(format.duration(0), format.duration(350), format.duration(999), format.duration(1000), format.duration(2500), format.duration(59999));
		println(format.duration(60000), format.duration(65000), format.duration(3723000), format.duration(86400000), format.duration(273600000), format.duration(-65000));
		println(format.ordinal(1), format.ordinal(2), format.ordinal(3), format.ordinal(4), format.ordinal(11), format.ordinal(12), format.ordinal(13), format.ordinal(21), format.ordinal(22), format.ordinal(101), format.ordinal(113), format.ordinal(0));
		println(format.plural(1, "file", "files"), format.plural(0, "file", "files"), format.plural(2, "child", "children"), format.plural(-1, "degree", "degrees"));
		println("[" + format.pad("ab", 6, Align.Left) + "]", "[" + format.pad("ab", 6, Align.Right) + "]", "[" + format.pad("ab", 6, Align.Center) + "]", "[" + format.pad("abc", 6, Align.Center) + "]", "[" + format.pad("too long", 3, Align.Right) + "]");
		println("[" + format.bar(3, 10, 20) + "]", "[" + format.bar(10, 10, 5) + "]", "[" + format.bar(99, 10, 5) + "]", "[" + format.bar(0, 10, 5) + "]", "[" + format.bar(1, 0, 5) + "]", "[" + format.bar(-1, 10, 5) + "]");
	`,
		"3.14 3 1.00 -0.1 1235",
		"1,234,567 -1,000 999 0 1,000 1 234 567 -123,456",
		"007 -007 1234 0 5",
		"12.5% 100% 0.00%",
		"1,234,567.50 0.00 0.00 -4.00 0.01 12.35 -1,000.00",
		"ff 101 -ff 255 null 123456789",
		"0 B 512 B 1023 B 1.0 KB 1.5 MB 5.0 GB -2.0 KB",
		"0ms 350ms 999ms 1.0s 2.5s 1m 00s",
		"1m 00s 1m 05s 1h 02m 03s 1d 00h 3d 04h -1m 05s",
		"1st 2nd 3rd 4th 11th 12th 13th 21st 22nd 101st 113th 0th",
		"1 file 0 files 2 children -1 degrees",
		"[ab    ] [    ab] [  ab  ] [ abc  ] [too long]",
		"[######] [#####] [#####] [] [] []",
	)

	// One rounding everywhere, a half away from zero, before the unit is
	// chosen
	expect(t, "import std.format;", `
		println(format.fixed(0.125, 2), format.fixed(-0.125, 2), format.fixed(1.005, 2), format.fixed(9.995, 2), format.fixed(-2.5, 0), format.fixed(-0.001, 2), format.fixed(99.96, 1), format.fixed(0.5, 0));
		println(format.money(1.005), format.money(-0.005), format.money(999999.995), format.money(5862.685), format.percent(0.125, 0), format.percent(-0.0005, 1));
		println(format.duration(59949), format.duration(59950), format.duration(59999), format.duration(1049), format.duration(1050), format.duration(61499), format.duration(61500));
		println(format.duration(3599499), format.duration(3599500), format.duration(86399499), format.duration(86399500), format.duration(88199999), format.duration(88200000));
		println(format.bytes(1023), format.bytes(1024), format.bytes(1048524), format.bytes(1048525), format.bytes(1048575), format.bytes(-1048575), format.bytes(1073741823), format.bytes(1099511627775), format.bytes(140737488355327));
	`,
		"0.13 -0.13 1.01 10.00 -3 0.00 100.0 1",
		"1.01 -0.01 1,000,000.00 5,862.69 13% -0.1%",
		"59.9s 1m 00s 1m 00s 1.0s 1.1s 1m 01s 1m 02s",
		"59m 59s 1h 00m 00s 23h 59m 59s 1d 00h 1d 00h 1d 01h",
		"1023 B 1.0 KB 1023.9 KB 1.0 MB 1.0 MB -1.0 MB 1.0 GB 1.0 TB 128.0 TB",
	)

	// Numbers as format writes them are numbers to a table
	expect(t, "import std.format;", `
		print(format.table(["item", "total", "share", "count", "delta"], [["a", "5,862.69", "12.5%", "30,600", "-3"], ["b", "0.50", "100%", "7", "-1,200.5"]]));
		print(format.table(["code"], [["1,2x"], ["%"]]));
	`,
		"item     total  share   count     delta",
		"----  --------  -----  ------  --------",
		"a     5,862.69  12.5%  30,600        -3",
		"b         0.50   100%       7  -1,200.5",
		"code",
		"----",
		"1,2x",
		"%",
	)

	// Tables: columns as wide as their widest value, numbers to the right
	expect(t, "import std.format;\nimport std.format.{Align};", `
		print(format.table(["name", "age", "city"], [["ada", "36", "London"], ["grace", "45.5", ""], ["a very long name", "7"]]));
		println("--");
		print(format.table(["only", "titles"], []));
		println("--");
		print(format.tableWith(["a", "b"], [Align.Center, Align.Right], [["xxxxx", "1"], ["y", "22"], ["z", "333", "dropped"]]));
		println("--");
		print(format.tableWith(["é", "n"], [Align.Right], [["été", "1"]]));
	`,
		"name               age  city",
		"----------------  ----  ------",
		"ada                 36  London",
		"grace             45.5",
		"a very long name     7",
		"--",
		"only  titles",
		"----  ------",
		"--",
		"  a      b",
		"-----  ---",
		"xxxxx    1",
		"  y     22",
		"  z    333",
		"--",
		"  é  n",
		"---  -",
		"été  1",
	)
	expectFailure(t, "import std.format;", `format.fixed(1.0, 200);`, "the number of digits must be between 0 and 100, got 200")
	expectFailure(t, "import std.format;", `format.inBase(1, 99);`, "the base must be between 2 and 36, got 99")
}

func TestDates(t *testing.T) {
	expect(t, "import std.time;\nimport std.time.{Date, Weekday};\nimport std.arrays;", `
		let d = time.dateTime(2026, 10, 13, 14, 5, 7);
		println(d.year, d.month, d.day, d.hour, d.minute, d.second, d.millisecond, d.weekday, d.dayOfYear, d.offset);
		println(d.format("dddd D MMMM YYYY"), "|", d.format("ddd DD MMM YY, HH:mm:ss.SSS"), "|", d.format("h:mm A [on] M/D"), "|", d.format("[YYYY is] YYYY"));
		println(d.iso(), d.isoTime(), d.zone(), d.timestamp(), d.format(""), d.format("no pattern in ...") == "no pattern in ...");
		println(time.dateTime(2026, 1, 3, 0, 7, 9).format("D/M h:m:s A"), time.dateTime(2026, 1, 3, 12, 0, 0).format("hh A"), time.dateTime(2026, 1, 3, 23, 0, 0).format("h A"));

		// Moving about the calendar
		println(d.plusDays(30).iso(), d.plusDays(-13).iso(), d.plusMonths(3).iso(), d.plusMonths(-10).iso(), d.plus((-2).hours()).format("HH:mm"), d.plus(1.weeks()).iso());
		println(time.date(2026, 1, 31).plusMonths(1).iso(), time.date(2024, 2, 28).plusDays(1).iso(), time.date(2026, 12, 31).plusDays(1).iso());
		println(d.daysUntil(time.date(2027, 1, 1)), time.date(2027, 1, 1).daysUntil(d), d.daysUntil(d.plus(23.hours())), d.startOfDay().isoTime());
		println(d.isBefore(d.plus(1.milliseconds())), d.isAfter(d.plus(1.milliseconds())), d.isSameDay(d.plus(9.hours())), d.isSameDay(d.plus(10.hours())), d.plus(0.seconds()).timestamp() == d.timestamp());

		println(time.date(2026, 1, 32).iso(), time.date(2026, 13, 1).iso(), time.date(2026, 3, 0).iso());
		println(time.daysIn(2024, 2), time.daysIn(2026, 2), time.daysIn(2026, 12), time.isLeapYear(2000), time.isLeapYear(1900), time.isLeapYear(2024), time.monthName(1), time.monthName(12));
		println([0, 1, 2, 3, 4, 5, 6].map(fn(n: Int) -> String { return time.date(2026, 10, 11).plusDays(n).weekday.name(); }));
		println(Weekday.Saturday.isWeekend(), Weekday.Sunday.isWeekend(), Weekday.Monday.isWeekend(), time.utc(0).isoTime(), time.utc(-1).format("YYYY-MM-DD HH:mm:ss.SSS"));

		// Reading
		let read = fn(text: String) -> String {
			let date = time.parse(text);
			if date == null { return "?"; }
			return date.isoTime();
		};
		println(read("2026-10-13"), read("2026-10-13 14:30"), read("2026-10-13T14:30:00Z"), read("2026-10-13T14:30:00+02:00"), read("13/Oct/2026:13:55:36 +0000"));
		println(read("nope"), read("2026-13-45"), read(""), (time.parse(d.isoTime()) ?? time.utc(0)).timestamp() == d.timestamp());

		// Here and now
		let now = time.today();
		println(now.timestamp() / 10000 == time.now() / 10000, now.plusDays(1).daysUntil(now), time.local(d.timestamp()).timestamp() == d.timestamp());
		println(time.local(d.timestamp()).plusDays(7).daysUntil(time.local(d.timestamp())), time.local(d.timestamp()).startOfDay().hour, time.local(d.timestamp()).plus(1.days()).timestamp() - d.timestamp());
		println(1.seconds(), 1.minutes(), 1.hours(), 1.days(), 1.weeks());
	`,
		"2026 10 13 14 5 7 0 Tuesday 286 0",
		"Tuesday 13 October 2026 | Tue 13 Oct 26, 14:05:07.000 | 2:05 PM on 10/13 | YYYY is 2026",
		"2026-10-13 2026-10-13T14:05:07Z Z 1791900307000  true",
		"3/1 12:7:9 AM 12 PM 11 PM",
		"2026-11-12 2026-09-30 2027-01-13 2025-12-13 12:05 2026-10-20",
		"2026-03-03 2024-02-29 2027-01-01",
		"80 -80 1 2026-10-13T00:00:00Z",
		"true false true false true",
		"2026-02-01 2027-01-01 2026-02-28",
		"29 28 31 true false true January December",
		`["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"]`,
		"true true false 1970-01-01T00:00:00Z 1969-12-31 23:59:59.999",
		"2026-10-13T00:00:00Z 2026-10-13T14:30:00Z 2026-10-13T14:30:00Z 2026-10-13T12:30:00Z 2026-10-13T13:55:36Z",
		"? ? ? true",
		"true -1 true",
		"-7 0 86400000",
		"1 s 1 min 1 h 1 day 7 days",
	)
	expectFailure(t, "import std.time;", `time.monthName(13);`, "there is no month 13")

	// A fixed offset, milliseconds, offsets written +0200, and dates that
	// compare, sort and print as the moments they are
	expect(t, "import std.time;\nimport std.time.{Date};\nimport std.ops;", `
		let d = time.dateTime(2026, 10, 13, 14, 5, 7);
		let paris = d.atOffset(120);
		let newYork = d.atOffset(-240);
		let delhi = d.atOffset(330);
		println(paris.isoTime(), newYork.isoTime(), delhi.isoTime(), paris.zone(), paris.hour, paris.offset);
		println(paris.timestamp() == d.timestamp(), paris == d, paris == newYork, paris.compare(d), paris.format("HH:mm Z"), d.atOffset(0).isoTime());
		println(paris.plusDays(1).isoTime(), paris.plus(1.hours()).isoTime(), paris.startOfDay().isoTime(), paris.plusMonths(2).isoTime(), paris.atOffset(-60).isoTime());
		println(time.parse(paris.isoTime()), time.parse("2026-10-13T16:05:07+0200"), time.parse("2026-10-13 16:05:07-0130"), time.parse("2026-10-13 16:05:07+02:00"));

		let precise = d.plus(250.milliseconds());
		println(precise.isoTime(), precise.plus(750.milliseconds()).isoTime(), time.utc(-1).isoTime(), time.parse("2026-10-13T14:05:07.250Z") == precise, time.parse("2026-10-13T14:05:07.250+0000"));
		println(time.parse(precise.atOffset(90).isoTime()) == precise, precise.atOffset(90));

		// Ordered, Equal and Show
		let dates = [time.date(2027, 1, 1), d.atOffset(600), time.date(2025, 6, 1).atOffset(-300), d];
		println(ops.sorted(dates), ops.largest(dates), ops.smallest(dates));
		println(d.isBefore(d.plus(1.milliseconds())), d.isAfter(d.plus(1.milliseconds())), d < d.plus(1.milliseconds()), d >= paris, "${d}", [paris]);
	`,
		"2026-10-13T16:05:07+02:00 2026-10-13T10:05:07-04:00 2026-10-13T19:35:07+05:30 +02:00 16 120",
		"true true true 0 16:05 +02:00 2026-10-13T14:05:07Z",
		"2026-10-14T16:05:07+02:00 2026-10-13T17:05:07+02:00 2026-10-13T00:00:00+02:00 2026-12-13T16:05:07+02:00 2026-10-13T13:05:07-01:00",
		"2026-10-13T14:05:07Z 2026-10-13T14:05:07Z 2026-10-13T17:35:07Z 2026-10-13T14:05:07Z",
		"2026-10-13T14:05:07.250Z 2026-10-13T14:05:08Z 1969-12-31T23:59:59.999Z true 2026-10-13T14:05:07.250Z",
		"true 2026-10-13T15:35:07.250+01:30",
		"[2025-05-31T19:00:00-05:00, 2026-10-14T00:05:07+10:00, 2026-10-13T14:05:07Z, 2027-01-01T00:00:00Z] 2027-01-01T00:00:00Z 2025-05-31T19:00:00-05:00",
		"true false true true 2026-10-13T14:05:07Z [2026-10-13T16:05:07+02:00]",
	)
	expectFailure(t, "import std.time;", `time.utc(0).atOffset(1440);`, "time.Date.atOffset: an offset is less than a day, got 1440 minutes")
}

func TestSet(t *testing.T) {
	src := `
		import std.set;
		import std.set.{Set};
		import std.arrays;

		struct Point { x: Int; y: Int; }

		fn main() -> Int {
			let seen = set.of(["a", "b", "a"]);
			let other = set.of(["b", "c"]);
			println(seen.len(), seen.isEmpty(), set.texts().isEmpty(), seen.has("a"), seen.has("z"), seen.items());
			println(seen.union(other).items(), seen.intersection(other).items(), seen.difference(other).items(), other.difference(seen).items());
			println(seen.isSubsetOf(other), set.of(["b"]).isSubsetOf(other), set.texts().isSubsetOf(other), seen.equals(set.of(["b", "a"])), seen.equals(other), seen.equals(set.of(["a"])));
			println(seen.add("a"), seen.add("z"), seen.remove("a"), seen.remove("a"), seen.items(), seen.len());

			// A copy is its own set
			let copy = seen.copy();
			copy.add("new");
			println(seen.has("new"), copy.has("new"), copy.len() - seen.len());

			// Numbers, and anything else given how to tell two apart
			let numbers = set.of([3, 3, 4, -1, 3]);
			println(numbers.items(), numbers.has(4), numbers.has(5), numbers.filter(fn(n: Int) -> Bool { return n > 0; }).items());
			let points = set.by(fn(p: Point) -> String { return "${p.x},${p.y}"; });
			println(points.add(Point { x: 1, y: 2 }), points.add(Point { x: 1, y: 2 }), points.add(Point { x: 2, y: 1 }), points.has(Point { x: 2, y: 1 }), points.len());
			println(points.items().map(fn(p: Point) -> Int { return p.x * 10 + p.y; }));

			// The primes below 50, by crossing out
			let crossed = set.numbers();
			let primes: [Int] = [];
			for n in arrays.range(2, 50) {
				if crossed.has(n) { continue; }
				primes.push(n);
				let multiple = n * n;
				while multiple < 50 { crossed.add(multiple); multiple += n; }
			}
			println(primes);
			return 0;
		}`
	errs := errors.NewHandler()
	result := compiler.Compile(src, "sets.qi", errs)
	for _, d := range errs.Diagnostics {
		t.Fatalf("unexpected diagnostic:\n%s", d.String())
	}
	var out bytes.Buffer
	machine := vm.NewVM()
	machine.SetOutput(&out)
	if _, err := machine.Exec(result.Chunk); err != nil {
		t.Fatalf("runtime error: %v", err)
	}
	want := `2 false true true false ["a", "b"]
["a", "b", "c"] ["b"] ["a"] ["c"]
false true true true false false
false true true false ["b", "z"] 2
false true 1
[3, 4, -1] true false [3, 4]
true false true true 2
[12, 21]
[2, 3, 5, 7, 11, 13, 17, 19, 23, 29, 31, 37, 41, 43, 47]
`
	if out.String() != want {
		t.Errorf("unexpected output:\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestMoreArraysAndDict(t *testing.T) {
	src := `
		import std.arrays;
		import std.strings;
		import std.dict;
		import std.random;
		import std.option;
		import std.regex;

		struct Person { name: String; age: Int; }

		fn main() -> Int {
			let people = [Person { name: "grace", age: 45 }, Person { name: "ada", age: 36 }, Person { name: "alan", age: 41 }, Person { name: "bob", age: 36 }];
			let age = fn(p: Person) -> Int { return p.age; };
			let name = fn(p: Person) -> String { return p.name; };
			println(people.sortedBy(age).map(name), people.sortedByText(name).map(name));
			println(option.map(people.maxBy(age), name), option.map(people.minBy(age), name), [].maxBy(age), people.sumOf(age), [].sumOf(age));
			println([1, 2, 3].take(2), [1].take(5), [1].take(0), [1, 2, 3].drop(2), [1].drop(5), [1, 2].drop(-1));
			println([1, 2, 3, 4, 5].chunks(2), [1, 2].chunks(5), [1].take(0).chunks(3).len(), arrays.flatten([[1], [], [2, 3]]), ["a b", "", "c"].flatMap(text -> text.words()));

			let groups = people.groupBy(fn(p: Person) -> String { return p.name.first(1); });
			println(groups.keys(), (groups.get("a") ?? []).map(name), (groups.get("z") ?? []).len());
			let counts = "to be or not to be".words().countBy(fn(word: String) -> String { return word; });
			println(counts.keys(), counts.values(), counts.sortedKeys());
			println(["b", "a", "b", "a", "c"].unique(), people.uniqueBy(fn(p: Person) -> String { return toString(p.age); }).map(name));
			println([1, 2, 3, 4, 5].partition(fn(n: Int) -> Bool { return n % 2 == 0; }), [1].take(0).partition(fn(n: Int) -> Bool { return true; }));

			// Side by side, and apart again
			let names = people.map(name);
			let (even, odd) = [1, 2, 3, 4, 5].partition(fn(n: Int) -> Bool { return n % 2 == 0; });
			let (letters, digits) = arrays.unzip([("a", 1), ("b", 2)]);
			println(arrays.zip(names.sortedTexts(), [36, 36]).take(2), arrays.zip(names, [1].take(0)), (letters, digits), arrays.unzip(arrays.zip(names.take(0), [1])));

			// The originals are left alone
			println(people.map(name));

			let tally = dict.empty<Int>();
			for word in ["x", "y", "x"] { tally.update(word, 0, fn(n: Int) -> Int { return n + 1; }); }
			tally.set("a", 0);
			println(tally.getOr("x", 0), tally.getOr("zz", -1), tally.sortedKeys(), tally.keys(), dict.empty<Int>().sortedKeys());
			let shown: [String] = [];
			for (word, count) in tally.entries() { shown.push("${word}=${count}"); }
			println(tally.entries(), tally.entries().len(), shown.join(" "));
			println("port=8080".cut("="), "a=b=c".cut("="), "none".cut("="), "=x".cut("="), "x=".cut("="),
				regex.must("(\\d+)-(\\d+)").capture("on 2026-10"), regex.must("x").capture("abc"));

			random.seed(7);
			let first = random.uuid();
			let second = random.uuid();
			random.seed(7);
			println(first == random.uuid(), first == second, first.len(), first.count("-"), first.substring(14, 15));
			return 0;
		}`
	errs := errors.NewHandler()
	result := compiler.Compile(src, "more.qi", errs)
	for _, d := range errs.Diagnostics {
		t.Fatalf("unexpected diagnostic:\n%s", d.String())
	}
	var out bytes.Buffer
	machine := vm.NewVM()
	machine.SetOutput(&out)
	if _, err := machine.Exec(result.Chunk); err != nil {
		t.Fatalf("runtime error: %v", err)
	}
	want := `["ada", "bob", "alan", "grace"] ["ada", "alan", "bob", "grace"]
grace ada null 158 0
[1, 2] [1] [] [3] [] [1, 2]
[[1, 2], [3, 4], [5]] [[1, 2]] 0 [1, 2, 3] ["a", "b", "c"]
["g", "a", "b"] ["ada", "alan"] 0
["to", "be", "or", "not"] [2, 2, 1, 1] ["be", "not", "or", "to"]
["b", "a", "c"] ["grace", "ada", "alan"]
([2, 4], [1, 3, 5]) ([], [])
[("ada", 36), ("alan", 36)] [] (["a", "b"], [1, 2]) ([], [])
["grace", "ada", "alan", "bob"]
2 -1 ["a", "x", "y"] ["x", "y", "a"] []
[("x", 2), ("y", 1), ("a", 0)] 3 x=2 y=1 a=0
("port", "8080") ("a", "b=c") null ("", "x") ("x", "") ["2026", "10"] null
false false 36 4 4
`
	if out.String() != want {
		t.Errorf("unexpected output:\n%s\nwant:\n%s", out.String(), want)
	}
	expectFailure(t, "import std.arrays;", `[1].chunks(0);`, "the size of a chunk must be at least 1, got 0")
}

// serve runs a Qoqi program that serves requests and, next to it, a
// Go function that asks them. It gives what the program printed.
func serve(t *testing.T, imports, body string, client func(base string)) string {
	t.Helper()
	src := imports + "\nfn main() -> Int {\n" + body + "\nreturn 0;\n}\n"
	errs := errors.NewHandler()
	result := compiler.Compile(src, "server.qi", errs)
	for _, d := range errs.Diagnostics {
		t.Errorf("unexpected diagnostic:\n%s", d.String())
	}
	if t.Failed() {
		t.FailNow()
	}

	// The program prints its port first; the client waits for that line
	reader, writer := io.Pipe()
	var out bytes.Buffer
	var wait sync.WaitGroup
	wait.Add(1)
	go func() {
		defer wait.Done()
		machine := vm.NewVM()
		machine.SetOutput(io.MultiWriter(&out, writer))
		if _, err := machine.Exec(result.Chunk); err != nil {
			t.Errorf("the server stopped: %v", err)
		}
		writer.Close()
	}()

	var port int
	if _, err := fmt.Fscanln(reader, &port); err != nil || port == 0 {
		t.Fatalf("expected the program to print its port first, got %v", err)
	}
	go io.Copy(io.Discard, reader)
	client(fmt.Sprintf("http://127.0.0.1:%d", port))
	wait.Wait()
	return out.String()
}

func TestHttpServer(t *testing.T) {
	ask := func(method, address, body string) string {
		t.Helper()
		request, _ := http.NewRequest(method, address, strings.NewReader(body))
		request.Header.Set("X-Token", "secret")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatalf("%s %s: %v", method, address, err)
		}
		defer response.Body.Close()
		content, _ := io.ReadAll(response.Body)
		return fmt.Sprintf("%d %s [%s] %s", response.StatusCode, response.Header.Get("Content-Type"), response.Header.Get("X-Made-By"), content)
	}

	var answers []string
	// Failures of a handler go to the error output of the test; that is fine
	printed := serve(t, `
		import std.http;
		import std.http.{Request, Response, Method};
		import std.json;
		import std.json.{Json};
		import std.arrays;
		import std.strings;
		import std.option;

		struct Counter { hits: Int; }
	`, `
		let seen = Counter { hits: 0 };
		let app = http.router();
		app.get("/", fn(request: Request) -> Response { return http.html("<h1>home</h1>"); })
			.get("/hello/:name", fn(request: Request) -> Response {
				seen.hits++;
				return http.text("hello, ${request.params.get("name") ?? "you"} (${request.query.get("q") ?? "-"}) ${request.header("x-token") ?? "?"}");
			})
			.get("/users/:id/posts/:post", fn(request: Request) -> Response {
				return http.text(request.params.keys().join(",") + " " + request.params.values().join(","));
			})
			.post("/echo", fn(request: Request) -> Response {
				let n = option.then(option.then(request.json().value(), fn(document: Json) -> Json? { return document.find("n"); }), json.intOf) ?? -1;
				return http.sendJson(201, json.object().int("twice", n * 2).text("type", request.header("Content-Type") ?? "").done()).with("X-Made-By", "qoqi");
			})
			.delete("/hello/:name", fn(request: Request) -> Response { return http.respond(204, ""); })
			.get("/files/*", fn(request: Request) -> Response { return http.text("[" + (request.params.get("rest") ?? "?") + "]"); })
			.get("/boom", fn(request: Request) -> Response { let items = [1]; return http.text(toString(items[5])); })
			.get("/go", fn(request: Request) -> Response { return http.redirect("/hello/there"); })
			.on(Method.Put, "/hits", fn(request: Request) -> Response { return http.text(toString(seen.hits)); });

		let server = http.listen(0).expect("a free port");
		println(server.port);
		server.handle(13, app.handler());
		server.close();
		println("served", seen.hits, server.next() == null);
	`, func(base string) {
		// Redirects are followed by Go's client: /go counts for two
		for _, r := range [][3]string{
			{"GET", "/", ""},
			{"GET", "/hello/ada?q=a+b%26c", ""},
			{"GET", "/hello/", ""},
			{"GET", "/users/7/posts/42", ""},
			{"POST", "/echo", `{"n": 21}`},
			{"POST", "/echo", `not json`},
			{"DELETE", "/hello/ada", ""},
			{"POST", "/hello/ada", ""},
			{"GET", "/files/a/b.txt", ""},
			{"GET", "/files", ""},
			{"GET", "/boom", ""},
			{"GET", "/go", ""},
		} {
			answers = append(answers, ask(r[0], base+r[1], r[2]))
		}
	})

	want := []string{
		"200 text/html; charset=utf-8 [] <h1>home</h1>",
		"200 text/plain; charset=utf-8 [] hello, ada (a b&c) secret",
		"404 text/plain; charset=utf-8 [] not found",
		"200 text/plain; charset=utf-8 [] id,post 7,42",
		`201 application/json [qoqi] {"twice":42,"type":""}`,
		`201 application/json [qoqi] {"twice":-2,"type":""}`,
		"204 text/plain; charset=utf-8 [] ",
		"405 text/plain; charset=utf-8 [] method not allowed",
		"200 text/plain; charset=utf-8 [] [a/b.txt]",
		"200 text/plain; charset=utf-8 [] []",
		"500 text/plain; charset=utf-8 [] the server failed",
		"200 text/plain; charset=utf-8 [] hello, there (-) secret",
	}
	for i := range want {
		if i >= len(answers) || answers[i] != want[i] {
			t.Errorf("answer %d:\n  want %s\n   got %v", i+1, want[i], answers[i:min(i+1, len(answers))])
		}
	}
	if !strings.HasSuffix(printed, "served 2 true\n") {
		t.Errorf("expected the program to carry on after serving, got %q", printed)
	}
}

func TestHttpClient(t *testing.T) {
	origin := http.NewServeMux()
	origin.HandleFunc("/users/7", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Seen", r.Method+" "+r.URL.RawQuery+" "+r.Header.Get("Authorization"))
		body, _ := io.ReadAll(r.Body)
		if len(body) > 0 {
			w.WriteHeader(201)
			fmt.Fprintf(w, `{"got": %s, "type": %q}`, body, r.Header.Get("Content-Type"))
			return
		}
		io.WriteString(w, `{"name": "ada", "langs": ["qoqi", "go"]}`)
	})
	origin.HandleFunc("/text", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		fmt.Fprintf(w, "%s %s %s", r.Method, r.Header.Get("Content-Type"), body)
	})
	listener := newLocalServer(t, origin)

	expect(t, `
		import std.http;
		import std.http.{Method, ErrorKind, Error};
		import std.json;
		import std.option;
		import std.result.{Result};`, `
		let base = `+quote(listener)+`;
		let user = http.get(base + "/users/7").expect("an answer");
		println(user.status, user.ok(), user.header("Content-Type"), user.header("CONTENT-TYPE"), user.header("nope"));
		let document = user.json().expect("json");
		println(document.find("name"), option.then(document.find("langs.1"), json.textOf));

		let created = http.postJson(base + "/users/7", json.object().int("n", 1).done()).expect("an answer");
		println(created.status, created.body, created.ok());
		let call = http.to(Method.Put, base + "/users/7").header("Authorization", "token").query("q", "a b").query("r", "é&").timeout(2000).send().expect("an answer");
		println(call.header("x-seen"));
		println(http.post(base + "/text", "héllo", "text/plain").expect("an answer").body);
		println(http.to(Method.Patch, base + "/text").body("x").send().expect("an answer").body, http.to(Method.Delete, base + "/text").send().expect("an answer").body);

		// A status that says no is an answer; no answer is an error
		let missing = http.get(base + "/nope").expect("an answer");
		println(missing.status, missing.ok(), missing.json().isErr());
		match http.get("example.com/x") {
			case Result.Ok(_): { println("?"); }
			case Result.Err(error): { println(error.kind, error.kind == ErrorKind.BadUrl, error.describe()); }
		}
		let kind = fn(error: Error) -> ErrorKind { return error.kind; };
		println(option.map(http.get("http://127.0.0.1:1/").reason(), kind), option.map(http.to(Method.Get, "ftp://x/").send().reason(), kind));
		println(Method.Get.name(), http.methodOf("post"), http.methodOf("Delete") == Method.Delete, http.methodOf("fetch"));
		let query = http.queryOf("a=1&b=x+y%26z&c&=d&");
		println(query.keys(), query.get("b"), query.get("c") == "", http.queryOf("").len());
	`,
		"200 true application/json application/json null",
		`"ada" go`,
		`201 {"got": {"n":1}, "type": "application/json"} true`,
		"PUT q=a+b&r=%C3%A9%26 token",
		"POST text/plain héllo",
		"PATCH  x DELETE  ",
		"404 false true",
		"BadUrl true no answer from example.com/x: an address starts with http:// or https:// and names a host",
		"Refused BadUrl",
		"GET Post true null",
		`["a", "b", "c", ""] x y&z true 0`,
	)
}

// newLocalServer serves a handler on a port of this machine until the
// test ends, and gives its address.
func newLocalServer(t *testing.T, handler http.Handler) string {
	t.Helper()
	server := &http.Server{Handler: handler}
	listener, err := newListener()
	if err != nil {
		t.Fatal(err)
	}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })
	return "http://" + listener.Addr().String()
}

func TestTestModule(t *testing.T) {
	out, err := run(t, "import std.test;\nimport std.result.{Kind};\nimport std.strings;", `
		let checks = test.suite("prices")
			.test("adds", fn() { test.equal(1 + 1, 2); test.notEqual("a", "b"); test.isTrue(1 < 2, "one below two"); test.isFalse(2 < 1, "two below one"); })
			.test("wrong", fn() { test.equal(1 + 1, 3); println("not reached"); })
			.test("bounds", fn() { let items = [1]; println(items[4]); })
			.test("same", fn() { test.same([1, 2], [1, 2]); test.near(0.1 + 0.2, 0.3, 0.0001); test.contains("hello", "ell"); })
			.test("same not", fn() { test.same(["a", "b"], ["a", "c"]); })
			.test("same length", fn() { test.same([1], [1, 2]); })
			.test("fails", fn() { let f = test.fails(Kind.Bounds, fn() { let items = [1]; println(items[4]); }); test.contains(f.message, "out of bounds"); })
			.test("fails not", fn() { test.fails(Kind.Bounds, fn() { panic("x"); }); })
			.test("fails nothing", fn() { test.fails(Kind.Panic, fn() { }); })
			.test("present", fn() { test.equal(test.present("4".parseInt(), "a number"), 4); test.absent("x".parseInt(), "number"); })
			.test("absent", fn() { test.present("x".parseInt(), "a number"); })
			.test("near", fn() { test.near(1.0, 2.0, 0.5); })
			.test("own", fn() { test.fail("my own reason"); })
			.test("truth", fn() { test.isTrue(2 < 1, "two below one"); });
		let report = checks.run();
		println(report.passed(), report.failed(), report.ok(), report.exitCode(), report.suite);
		println(report.results[1].passed(), report.results[0].passed(), report.results[0].microseconds >= 0);

		// Without printing, and a suite where all is well
		let quiet = test.suite("fine").test("one", fn() { }).check();
		println(quiet.ok(), quiet.exitCode(), quiet.lines());
		println(test.suite("empty").check().lines());
	`, "")
	if err != nil {
		t.Fatalf("runtime error: %v", err)
	}
	want := `prices
  ok    adds
  FAIL  wrong
        expected 3, got 2
        at test.qi:8
  FAIL  bounds
        index out of bounds: 4 (length 1)
        at test.qi:9
  ok    same
  FAIL  same not
        expected c at 1, got b: ["a", "b"]
        at test.qi:11
  FAIL  same length
        expected 2 items, got 1: [1]
        at test.qi:12
  ok    fails
  FAIL  fails not
        expected a failure of kind Bounds, got Panic: x
        at test.qi:14
  FAIL  fails nothing
        expected a failure, and there was none
        at test.qi:15
  ok    present
  FAIL  absent
        expected a number, got nothing
        at test.qi:17
  FAIL  near
        expected 2 give or take 0.5, got 1
        at test.qi:18
  FAIL  own
        my own reason
        at test.qi:19
  FAIL  truth
        expected two below one
        at test.qi:20
4 passed, 10 failed
4 10 false 1 prices
false true true
true 0 ["fine", "  ok    one", "1 passed"]
["empty", "0 passed"]
`
	if out != want {
		t.Errorf("unexpected report:\n%s\nwant:\n%s", out, want)
	}

	// equal compares as == does, by what values hold: two arrays or two
	// structs made apart are equal when their items or fields are
	expectProgram(t, `
		import std.test;

		struct Point { x: Int; y: Int; }

		fn main() -> Int {
			let report = test.suite("equal")
				.test("contents", fn() { test.equal([1, 2], [1, 2]); test.equal(Point { x: 1, y: 2 }, Point { x: 1, y: 2 }); test.equal((1, "a"), (1, "a")); })
				.test("fields", fn() { test.equal(Point { x: 1, y: 2 }, Point { x: 1, y: 3 }); })
				.test("not a number", fn() { test.equal(0.0 / 0.0, 0.0 / 0.0); })
				.check();
			for line in report.lines() { println(line); }
			return 0;
		}`,
		"equal",
		"  ok    contents",
		"  FAIL  fields",
		"        expected {x: 1, y: 3}, got {x: 1, y: 2}",
		"        at tasks.qi:9",
		"  FAIL  not a number",
		"        expected NaN, got NaN",
		"        at tasks.qi:10",
		"1 passed, 2 failed",
	)
}

// TestLibraryUnderMemoryPressure runs the functions whose natives build
// arrays of arrays with a heap so small that it is collected many times
// while they work: a value a native lost track of would come back wrong.
func TestLibraryUnderMemoryPressure(t *testing.T) {
	src := `
		import std.json;
		import std.regex;
		import std.csv;
		import std.strings;
		import std.arrays;
		import std.encoding;
		import std.option;
		import std.dict;
		import std.json.{Json};

		fn main() -> Int {
			let rows: [String] = [];
			let fields: [String] = [];
			for i in arrays.range(0, 300) {
				rows.push("row${i},\"value, ${i}\",${i * 7}");
				fields.push("\"key${i}\": [${i}, \"text ${i}\", {\"n\": ${i}}]");
			}
			let table = rows.join("\n");
			let document = "{" + fields.join(", ") + "}";
			let numbers = regex.must("(\\d+),\"value, (\\d+)\",(\\d+)");

			let total = 0;
			for round in arrays.range(0, 40) {
				let parsed = csv.parse(table).expect("csv");
				let found = numbers.findAll(table);
				let tree = json.parse(document).expect("json");
				let bytes = encoding.bytes(table);
				for i in arrays.range(0, 300) {
					if parsed[i][1] != "value, ${i}" || found[i].groups[2] != toString(i * 7) { panic("row ${i} of round ${round} is wrong"); }
					if option.then(tree.find("key${i}.2.n"), json.intOf) != i || option.then(tree.find("key${i}.1"), json.textOf) != "text ${i}" { panic("key ${i} of round ${round} is wrong"); }
				}
				total += parsed.len() + found.len() + (tree.fields() ?? dict.empty<Json>()).len() + bytes.len() / bytes.len();
			}
			println(total);
			return 0;
		}`
	errs := errors.NewHandler()
	result := compiler.Compile(src, "pressure.qi", errs)
	for _, d := range errs.Diagnostics {
		t.Fatalf("unexpected diagnostic:\n%s", d.String())
	}

	var out bytes.Buffer
	machine := vm.NewVMWithHeapSize(3 << 20)
	machine.SetOutput(&out)
	if _, err := machine.Exec(result.Chunk); err != nil {
		t.Fatalf("runtime error: %v", err)
	}
	if out.String() != "36040\n" {
		t.Errorf("expected 36040, got %q", out.String())
	}
	if machine.Collections() < 5 {
		t.Errorf("expected the heap to be collected many times, got %d", machine.Collections())
	}
}

// TestProgramArguments checks that a program reads its own files and
// arguments through the library, end to end in one process.
func TestProgramArguments(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "in.txt"), []byte("3\n4\n"), 0644); err != nil {
		t.Fatal(err)
	}
	src := `
		import std.os;
		import std.io;
		import std.path;
		import std.strings;
		import std.arrays;
		fn main() -> Int {
			let args = os.args();
			let numbers = io.readFileLines(path.inside(args[0], args[1])).expect("the file").map(fn(line: String) -> Int { return line.parseInt() ?? 0; });
			println(args.len(), numbers.sum());
			return 0;
		}`
	errs := errors.NewHandler()
	result := compiler.Compile(src, "args.qi", errs)
	for _, d := range errs.Diagnostics {
		t.Fatalf("unexpected diagnostic:\n%s", d.String())
	}
	var out bytes.Buffer
	machine := vm.NewVM()
	machine.SetOutput(&out)
	machine.SetArgs([]string{dir, "in.txt", "--verbose"})
	if _, err := machine.Exec(result.Chunk); err != nil {
		t.Fatalf("runtime error: %v", err)
	}
	if out.String() != "3 7\n" {
		t.Errorf("expected 3 arguments and a sum of 7, got %q", out.String())
	}
}

func TestOps(t *testing.T) {
	src := `
		import std.ops;
		import std.ops.{Equal, Ordered, Show};
		import std.arrays;
		import std.list;
		import std.dict;
		import std.set;
		import std.json;
		import std.test;

		struct Card { rank: Int; suit: String; }
		impl Ordered for Card {
			fn compare(other: Card) -> Int {
				if self.rank != other.rank { return self.rank - other.rank; }
				return self.suit.compare(other.suit);
			}
		}
		impl Equal for Card { fn equals(other: Card) -> Bool { return self.compare(other) == 0; } }
		impl Show for Card { fn show() -> String { return "${self.rank}${self.suit}"; } }

		fn main() -> Int {
			// The builtin types have the traits
			println(3.compare(5), 5.compare(3), 4.compare(4), 2.5.compare(1.5), "a".compare("b"), "b".compare("b"));
			println(3.equals(3), 3.equals(4), "x".equals("x"), true.equals(false), 1.5.equals(1.5));
			println(3.isBefore(5), "b".isAfter("a"), 7.show() + "!", true.show(), 2.5.show(), "s".show());

			// For anything that implements them
			let hand = [Card { rank: 9, suit: "h" }, Card { rank: 2, suit: "s" }, Card { rank: 9, suit: "c" }, Card { rank: 14, suit: "d" }];
			println(ops.join(ops.sorted(hand), " "), ops.largest(hand), ops.smallest(hand), ops.max(hand[0], hand[2]), ops.min(hand[0], hand[2]));
			println(ops.sorted([3, 1, 2]), ops.sorted(["pear", "fig", "apple"]), ops.sorted([2.5, 0.5]), ops.sorted([1].take(0)), ops.sorted([7]));
			println(ops.largest([3, 9, 4]), ops.smallest(["b", "a"]), ops.largest([1].take(0)), ops.max(1, 1), ops.min("b", "a"), ops.max(1.5, 2.5));
			println(ops.contains(hand, Card { rank: 2, suit: "s" }), ops.contains(hand, Card { rank: 2, suit: "h" }), ops.indexOf(hand, Card { rank: 9, suit: "c" }), ops.indexOf([1, 2], 3), ops.contains(["a"], "a"));
			println(ops.same(hand, hand), ops.same([1, 2], [1, 2]), ops.same([1, 2], [2, 1]), ops.same([1], [1, 2]), ops.same([1].take(0), [1].take(0)));
			println(ops.join(hand, ", "), ops.join([1, 2], "+"), ops.join([1].take(0), ","), ops.join([true], ","));

			// Sorting keeps what is equal in the order it was in
			let ties = ops.sorted([Card { rank: 5, suit: "x" }, Card { rank: 1, suit: "x" }, Card { rank: 5, suit: "x" }]).map(fn(c: Card) -> Int { return c.rank; });
			println(ties, hand[0] < hand[3], hand[0] == Card { rank: 9, suit: "h" }, hand[0] >= hand[2]);

			// What the library itself implements
			let names = list.of(["b", "a"]);
			names[0] = "c";
			let ages = dict.empty<Int>();
			ages["x"] = 1;
			let seen = set.of(["p", "q"]);
			let walked: [String] = [];
			for name in names { walked.push(name); }
			for key in ages { walked.push(key); }
			for item in seen { walked.push(item); }
			println(walked, names[1], ages["x"], ages["y"], seen == set.of(["q", "p"]), json.ints([1]) == json.ints([1]), json.ints([1]) != json.texts(["1"]));

			// A check that two values are equal uses what the type says
			let report = test.suite("equal")
				.test("by content", fn() { test.equal(Card { rank: 1, suit: "a" }, Card { rank: 1, suit: "a" }); test.equal(json.int(1), json.int(1)); })
				.test("not equal", fn() { test.equal(Card { rank: 1, suit: "a" }, Card { rank: 2, suit: "a" }); })
				.check();
			println(report.passed(), report.failed(), report.lines()[3]);
			return 0;
		}`
	errs := errors.NewHandler()
	result := compiler.Compile(src, "ops.qi", errs)
	for _, d := range errs.Diagnostics {
		t.Fatalf("unexpected diagnostic:\n%s", d.String())
	}
	var out bytes.Buffer
	machine := vm.NewVM()
	machine.SetOutput(&out)
	if _, err := machine.Exec(result.Chunk); err != nil {
		t.Fatalf("runtime error: %v", err)
	}
	want := `-1 1 0 1 -1 0
true false true false true
true true 7! true 2.5 s
2s 9c 9h 14d 14d 2s 9h 9c
[1, 2, 3] ["apple", "fig", "pear"] [0.5, 2.5] [] [7]
9 a null 1 a 2.5
true false 2 null true
true true false false true
9h, 2s, 9c, 14d 1+2  true
[1, 5, 5] true true true
["c", "a", "x", "p", "q"] a 1 null true true true
1 1         expected 2a, got 1a
`
	if out.String() != want {
		t.Errorf("unexpected output:\n%s\nwant:\n%s", out.String(), want)
	}
}
