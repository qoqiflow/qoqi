package vm

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"
)

// ptrFromValue is the heap object a value points at.
func ptrFromValue(v Value) unsafe.Pointer {
	return unsafe.Pointer(v.AsHeapPtr())
}

// session is one VM used for several calls: what a native keeps for the
// next (the reason of a failure, a server) lives in it.
type session struct {
	t       *testing.T
	machine *VM
}

func newSession(t *testing.T) *session {
	return &session{t: t, machine: NewVM()}
}

func (s *session) call(name string, args ...interface{}) string {
	s.t.Helper()
	v, err := callNative(s.t, s.machine, name, args...)
	if err != nil {
		s.t.Fatalf("%s%v: unexpected error: %v", name, args, err)
	}
	return quoted(v)
}

func (s *session) expect(want, name string, args ...interface{}) {
	s.t.Helper()
	if got := s.call(name, args...); got != want {
		s.t.Errorf("%s%v:\n  want %s\n   got %s", name, args, want, got)
	}
}

func TestNativePaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the expected paths are written with /")
	}
	expectNative(t, `"a/c.txt"`, "std.path.joined", []string{"a", "b/", "../c.txt"})
	expectNative(t, `""`, "std.path.joined", []string{})
	expectNative(t, `"/x"`, "std.path.joined", []string{"/", "x"})
	expectNative(t, `"todo.txt"`, "std.path.base", "notes/todo.txt")
	expectNative(t, `"."`, "std.path.base", "")
	expectNative(t, `"notes"`, "std.path.dir", "notes/todo.txt")
	expectNative(t, `"."`, "std.path.dir", "todo.txt")
	expectNative(t, `".gz"`, "std.path.extension", "a/b.tar.gz")
	expectNative(t, `""`, "std.path.extension", "a.b/c")
	expectNative(t, `""`, "std.path.extension", ".bashrc")
	expectNative(t, `""`, "std.path.extension", "home/.bashrc")
	expectNative(t, `".json"`, "std.path.extension", "a/.config.json")
	expectNative(t, `""`, "std.path.extension", "..")
	expectNative(t, `""`, "std.path.extension", "")
	expectNative(t, `"a/c"`, "std.path.clean", "a//b/../c/")
	expectNative(t, "true", "std.path.isAbsolute", "/a")
	expectNative(t, "false", "std.path.isAbsolute", "a")
	expectNative(t, `"/"`, "std.path.separator")
	expectNative(t, "true", "std.path.matches", "*.txt", "todo.txt")
	expectNative(t, "false", "std.path.matches", "*.txt", "todo.md")
	expectNative(t, "true", "std.path.matches", "no?e[sz]", "notes")
	expectNativeError(t, "'[' is not a valid pattern", "std.path.matches", "[", "x")

	abs, err := callNative(t, NewVM(), "std.path.absolute", "x.txt")
	if got, _ := stringValue(abs); err != nil || !filepath.IsAbs(got) || filepath.Base(got) != "x.txt" {
		t.Errorf("expected an absolute path ending in x.txt, got %q, %v", got, err)
	}
}

func TestNativeFolders(t *testing.T) {
	dir := t.TempDir()
	s := newSession(t)
	deep := filepath.Join(dir, "a", "b")
	file := filepath.Join(deep, "one.txt")

	s.expect("false", "std.fs.isDir", deep)
	s.expect("true", "std.fs.makeFolder", deep)
	s.expect("true", "std.fs.makeFolder", deep) // already there: fine
	s.expect("true", "std.fs.isDir", deep)
	s.expect("false", "std.fs.isFile", deep)
	s.expect("true", "std.io.writeText", file, "hello")
	s.expect("true", "std.fs.isFile", file)
	s.expect("false", "std.fs.isDir", file)

	// Listing is sorted and one level deep; walking gives every file
	s.expect("true", "std.io.writeText", filepath.Join(dir, "z.txt"), "")
	s.expect("true", "std.io.writeText", filepath.Join(dir, "B.txt"), "")
	s.expect(`["B.txt", "a", "z.txt"]`, "std.fs.names", dir)
	empty := filepath.Join(dir, "empty")
	s.expect("true", "std.fs.makeFolder", empty)
	s.expect(`[]`, "std.fs.names", empty)
	s.expect("true", "std.fs.removeTree", empty)

	want := fmt.Sprintf("[%q, %q, %q]", filepath.Join(dir, "B.txt"), file, filepath.Join(dir, "z.txt"))
	s.expect(want, "std.fs.tree", dir)

	// Size, then the time of the last change
	stat, err := callNative(t, s.machine, "std.fs.stat", file)
	if err != nil {
		t.Fatal(err)
	}
	facts := (*ArrayObj)(ptrFromValue(stat))
	if facts.Len() != 2 || facts.Get(0).AsInt() != 5 {
		t.Errorf("expected 5 bytes, got %s", FormatValue(stat))
	}
	if age := time.Now().UnixMilli() - facts.Get(1).AsInt(); age < 0 || age > 60000 {
		t.Errorf("expected a file changed just now, got %d ms ago", age)
	}

	// Copying, renaming
	copied := filepath.Join(dir, "copy.txt")
	s.expect("true", "std.fs.duplicate", file, copied)
	s.expect(`"hello"`, "std.io.readText", copied)
	s.expect("true", "std.fs.duplicate", filepath.Join(dir, "z.txt"), copied) // replaces
	s.expect(`""`, "std.io.readText", copied)
	moved := filepath.Join(dir, "moved.txt")
	s.expect("true", "std.fs.move", copied, moved)
	s.expect("false", "std.io.exists", copied)
	s.expect("true", "std.io.exists", moved)

	// What cannot be done says why
	nothing := filepath.Join(dir, "nothing")
	s.expect("null", "std.fs.names", nothing)
	s.expect(`["not found", "no such file or directory"]`, "std.io.problem")
	s.expect("null", "std.fs.names", file)
	s.expect(`["not a directory", "not a directory"]`, "std.io.problem")
	s.expect("null", "std.fs.tree", nothing)
	s.expect("null", "std.fs.stat", nothing)
	s.expect("false", "std.fs.duplicate", nothing, moved)
	s.expect(`["not found", "no such file or directory"]`, "std.io.problem")
	s.expect("false", "std.fs.duplicate", deep, moved)
	s.expect(`["is a directory", "is a directory"]`, "std.io.problem")
	s.expect("false", "std.fs.move", nothing, moved)
	s.expect("false", "std.fs.makeFolder", filepath.Join(file, "inside"))
	s.expect(`["not a directory", "not a directory"]`, "std.io.problem")

	// Removing takes what is inside, and nothing there is fine
	s.expect("true", "std.fs.removeTree", filepath.Join(dir, "a"))
	s.expect("false", "std.io.exists", file)
	s.expect("true", "std.fs.removeTree", filepath.Join(dir, "a"))

	temp, err := callNative(t, s.machine, "std.fs.newTempDir", "qoqi-test-")
	made, _ := stringValue(temp)
	if err != nil || !strings.HasPrefix(filepath.Base(made), "qoqi-test-") {
		t.Fatalf("expected a temporary folder, got %q, %v", made, err)
	}
	defer os.RemoveAll(made)
	if info, err := os.Stat(made); err != nil || !info.IsDir() {
		t.Errorf("expected %s to exist", made)
	}
	if got := s.call("std.fs.currentDir"); got == `""` || got == `"."` {
		t.Errorf("expected the current folder, got %s", got)
	}
}

func TestNativeOs(t *testing.T) {
	s := newSession(t)
	s.expect(`[]`, "std.os.args")
	s.machine.SetArgs([]string{"add", "--all", "two words"})
	s.expect(`["add", "--all", "two words"]`, "std.os.args")
	s.expect(fmt.Sprintf("%q", runtime.GOOS), "std.os.platform")

	t.Setenv("QOQI_TEST_VARIABLE", "a=b")
	if got := s.call("std.os.variables"); !strings.Contains(got, `"QOQI_TEST_VARIABLE=a=b"`) {
		t.Errorf("expected the variable among the environment, got %d characters", len(got))
	}

	// Asking to end is an error of its own kind, carrying the status
	_, err := callNative(t, s.machine, "std.os.exit", 3)
	if code, asked := ExitCode(err); !asked || code != 3 {
		t.Errorf("expected a request to end with status 3, got %v", err)
	}
	if _, asked := ExitCode(errors.New("exit 3")); asked {
		t.Error("an ordinary error is not a request to end")
	}
	if _, asked := ExitCode(nil); asked {
		t.Error("no error is not a request to end")
	}

	if runtime.GOOS == "windows" {
		return
	}
	s.expect(`["0", "hi\n", ""]`, "std.os.execute", "sh", []string{"-c", "echo hi"}, "")
	s.expect(`["3", "", "bad\n"]`, "std.os.execute", "sh", []string{"-c", "echo bad >&2; exit 3"}, "")
	s.expect(`["0", "HELLO", ""]`, "std.os.execute", "tr", []string{"a-z", "A-Z"}, "hello")
	s.expect("null", "std.os.execute", "no-such-program-of-qoqi", []string{}, "")
	s.expect(`["0", "/\nyes\n", ""]`, "std.os.executeIn", "sh", []string{"-c", "pwd; echo $QOQI_SAID"}, "", "/", []string{"QOQI_SAID=yes"})
	s.expect(`["not found", "there is no program called 'no-such-program-of-qoqi'"]`, "std.io.problem")
}

func TestNativeRegex(t *testing.T) {
	expectNative(t, "null", "std.regex.mistake", `\d+`)
	expectNative(t, "\"missing closing ): `(a`\"", "std.regex.mistake", `(a`)
	expectNative(t, "true", "std.regex.test", `^\d+$`, "123")
	expectNative(t, "false", "std.regex.test", `^\d+$`, "12a")

	// Positions count characters, not bytes
	expectNative(t, `[["5", "7", "42", "4", "2"]]`, "std.regex.search", `(\d)(\d)`, "été: 42", -1)
	expectNative(t, `[["0", "1", "a"], ["2", "3", "a"]]`, "std.regex.search", `a`, "a-a-a", 2)
	expectNative(t, `[]`, "std.regex.search", `x`, "abc", -1)
	// A group that took no part is an empty text
	expectNative(t, `[["0", "1", "b", "", "b"]]`, "std.regex.search", `(a)|(b)`, "b", -1)

	expectNative(t, `["", "year", ""]`, "std.regex.groupNames", `(?P<year>\d+)-(\d+)`)
	expectNative(t, `"13/10/2026"`, "std.regex.substitute", `(?P<y>\d+)-(\d+)-(\d+)`, "2026-10-13", "$3/$2/${y}")
	expectNative(t, `["a", "b", "c"]`, "std.regex.pieces", `\s*,\s*`, "a , b,c")
	expectNative(t, `"1\\+1\\?"`, "std.regex.escape", "1+1?")

	// The library checks patterns first; one that reaches a native wrong is refused
	expectNativeError(t, "'(a' is not a regular expression", "std.regex.test", "(a", "x")
	expectNativeError(t, "'[' is not a regular expression", "std.regex.search", "[", "x", -1)
}

func TestNativeJson(t *testing.T) {
	tokens := func(want, text string) {
		t.Helper()
		expectNative(t, want, "std.json.tokens", text)
	}
	tokens(`["n1"]`, "1")
	tokens(`["sx"]`, `"x"`)
	tokens(`["t"]`, " true ")
	tokens(`["z"]`, "null")
	tokens(`["{", "sa", "[", "n1", "n2.5", "n-3e2", "]", "sb", "f", "s", "s{", "}"]`, `{"a": [1, 2.5, -3e2], "b": false, "": "{"}`)
	tokens(`["[", "s\"\n\t\\é☃", "]"]`, `["\"\n\t\\\u00e9☃"]`)
	// A number too large for a float is still a token; the library reads it
	tokens(`["n123456789012345678901234567890"]`, "123456789012345678901234567890")

	s := newSession(t)
	refused := func(text, where, message string) {
		t.Helper()
		s.expect("null", "std.json.tokens", text)
		got := s.call("std.json.problem")
		if !strings.HasPrefix(got, fmt.Sprintf("[%q, ", where)) || !strings.Contains(got, message) {
			t.Errorf("%q: expected a problem at %s about %q, got %s", text, where, message, got)
		}
	}
	refused("", "1:1", "ends before")
	refused("[1, 2", "1:6", "ends before")
	refused(`{"a": `, "1:7", "ends before")
	refused("1 2", "1:3", "something after the value")
	refused("{\"a\": [1,\n }", "2:2", "invalid character '}'")
	refused(`{"a" 1}`, "1:6", "invalid character '1'")
	refused("[1,]", "1:4", "invalid character ']'")
	refused("nul", "1:4", "")
	refused(`"é`+"\n"+`"`, "1:3", "")
	refused("[\n\n  é]", "3:3", "invalid character")
	// One no Float holds is refused where it starts, not read as 0
	refused("1e400", "1:1", "the number 1e400 is out of range")
	refused(`{"a": [1, -5e999, 2]}`, "1:11", "the number -5e999 is out of range")

	expectNative(t, `"\"a\\\"b\\n<é>\""`, "std.json.quote", "a\"b\n<é>")
	expectNative(t, `"1"`, "std.json.numeral", 1.0)
	expectNative(t, `"2.5"`, "std.json.numeral", 2.5)
	expectNative(t, `"-0.001"`, "std.json.numeral", -0.001)
	expectNative(t, `"1e+21"`, "std.json.numeral", 1e21)
}

func TestNativeCsv(t *testing.T) {
	expectNative(t, `[["a", "b"], ["1", "x, y"], ["", "say \"hi\""]]`, "std.csv.rows", "a,b\n1,\"x, y\"\n,\"say \"\"hi\"\"\"\n", ",")
	expectNative(t, `[]`, "std.csv.rows", "", ",")
	expectNative(t, `[["a", "b,c"]]`, "std.csv.rows", "a;b,c", ";")
	expectNative(t, `[["a"], ["b", "c"]]`, "std.csv.rows", "a\r\nb,c\r\n", ",") // rows may differ in length
	expectNative(t, `[["two\nlines", "é"]]`, "std.csv.rows", "\"two\nlines\",é\n", ",")
	expectNativeError(t, "the separator must be one character", "std.csv.rows", "a", "")
	expectNativeError(t, "the separator must be one character", "std.csv.rows", "a", "::")
	// A byte-order mark in front is passed over
	expectNative(t, `[["a", "b"]]`, "std.csv.rows", "\uFEFFa,b", ",")
	// The line each row starts on
	expectNative(t, `[1, 2, 6, 8]`, "std.csv.starts", "\uFEFFa,b\n1,\"x\ny\nz\"\n\nc,d\n\ne\n", ",")
	expectNative(t, `[]`, "std.csv.starts", "", ",")
	expectNativeError(t, "the separator must be one character", "std.csv.starts", "a", "")

	s := newSession(t)
	s.expect("null", "std.csv.rows", "a,b\nc,\"d\n", ",")
	if got := s.call("std.csv.problem"); !strings.HasPrefix(got, `["2:`) || !strings.Contains(got, "quote") {
		t.Errorf("expected a problem on line 2 about a quote, got %s", got)
	}

	expectNative(t, `"a b,\"c\"\"d\"\n\"e,f\",\n"`, "std.csv.text", [][]string{{"a b", `c"d`}, {"e,f", ""}}, ",")
	expectNative(t, `"a,b;c\n"`, "std.csv.text", [][]string{{"a,b", "c"}}, ";")
	expectNative(t, `""`, "std.csv.text", [][]string{}, ",")
	expectNativeError(t, "must be an array of arrays of Strings", "std.csv.text", []string{"a"}, ",")
}

func TestNativeEncoding(t *testing.T) {
	expectNative(t, `"aMOpbGxv"`, "std.encoding.toBase64", "héllo")
	expectNative(t, `"héllo"`, "std.encoding.fromBase64", "aMOpbGxv")
	expectNative(t, `""`, "std.encoding.fromBase64", "")
	expectNative(t, "null", "std.encoding.fromBase64", "not base64!")
	// Padding may be left out, and not half written
	expectNative(t, `"hi"`, "std.encoding.fromBase64", "aGk=")
	expectNative(t, `"hi"`, "std.encoding.fromBase64", "aGk")
	expectNative(t, `"?>"`, "std.encoding.fromBase64", "Pz4")
	expectNative(t, "null", "std.encoding.fromBase64", "a")
	expectNative(t, "null", "std.encoding.fromBase64", "Pz8-Pg")
	expectNative(t, `"Pz8-Pg"`, "std.encoding.toBase64Url", "??>>")
	expectNative(t, `"??>>"`, "std.encoding.fromBase64Url", "Pz8-Pg")
	expectNative(t, `"??>>"`, "std.encoding.fromBase64Url", "Pz8-Pg==") // padding is accepted
	expectNative(t, `"6869c3a9"`, "std.encoding.toHex", "hié")
	expectNative(t, `"hié"`, "std.encoding.fromHex", "6869C3A9")
	expectNative(t, "null", "std.encoding.fromHex", "686")
	expectNative(t, "null", "std.encoding.fromHex", "zz")
	expectNative(t, `"a+b%26c%3D%C3%A9%2F"`, "std.encoding.toUrl", "a b&c=é/")
	expectNative(t, `"a b&c=é/"`, "std.encoding.fromUrl", "a+b%26c%3D%C3%A9%2F")
	expectNative(t, "null", "std.encoding.fromUrl", "%zz")
	expectNative(t, `[104, 195, 169]`, "std.encoding.bytes", "hé")
	expectNative(t, `[]`, "std.encoding.bytes", "")
	expectNative(t, `"hé"`, "std.encoding.fromBytes", []int{104, 195, 169})
	expectNative(t, `"\xff"`, "std.encoding.fromBytes", []int{255}) // as fromHex("ff") gives
	expectNative(t, `"\xff"`, "std.encoding.fromHex", "ff")
	expectNative(t, `[255, 104]`, "std.encoding.bytes", "\xffh")
	expectNativeError(t, "a byte is between 0 and 255, element 1 is 256", "std.encoding.fromBytes", []int{1, 256})
	expectNativeError(t, "must be an array of Ints, element 0 is String", "std.encoding.fromBytes", []string{"a"})
}

func TestNativeHash(t *testing.T) {
	expectNative(t, `"ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"`, "std.hash.sha256", "abc")
	expectNative(t, `"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"`, "std.hash.sha256", "")
	expectNative(t, `"a9993e364706816aba3e25717850c26c9cd0d89d"`, "std.hash.sha1", "abc")
	expectNative(t, `"900150983cd24fb0d6963f7d28e17f72"`, "std.hash.md5", "abc")
	expectNative(t, "891568578", "std.hash.crc32", "abc")
	expectNative(t, "0", "std.hash.crc32", "")
	// RFC 4231, test case 2
	expectNative(t, `"5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843"`, "std.hash.hmacSha256", "Jefe", "what do ya want for nothing?")
}

func TestNativeFormat(t *testing.T) {
	expectNative(t, `"3.14"`, "std.format.fixed", 3.14159, 2)
	expectNative(t, `"3"`, "std.format.fixed", 2.5, 0)
	expectNative(t, `"-3"`, "std.format.fixed", -2.5, 0)
	expectNative(t, `"0.13"`, "std.format.fixed", 0.125, 2)
	expectNative(t, `"1.01"`, "std.format.fixed", 1.005, 2)
	expectNative(t, `"10.00"`, "std.format.fixed", 9.995, 2)
	expectNative(t, `"100.0"`, "std.format.fixed", 99.96, 1)
	expectNative(t, `"0.00"`, "std.format.fixed", -0.001, 2)
	expectNative(t, `"0.000"`, "std.format.fixed", 1e-7, 3)
	expectNative(t, `"1000000000000000000000"`, "std.format.fixed", 1e21, 0)
	expectNative(t, `"-1.50"`, "std.format.fixed", -1.5, 2)
	expectNative(t, `"1.00"`, "std.format.fixed", 1.0, 2)
	expectNativeError(t, "the number of digits must be between 0 and 100, got -1", "std.format.fixed", 1.0, -1)
	expectNative(t, `"ff"`, "std.format.inBase", 255, 16)
	expectNative(t, `"-101"`, "std.format.inBase", -5, 2)
	expectNative(t, `"z"`, "std.format.inBase", 35, 36)
	expectNativeError(t, "the base must be between 2 and 36, got 1", "std.format.inBase", 5, 1)
	expectNative(t, "255", "std.format.fromBase", "FF", 16)
	expectNative(t, "-5", "std.format.fromBase", "-101", 2)
	expectNative(t, "null", "std.format.fromBase", "12", 2)
	expectNative(t, "null", "std.format.fromBase", "", 10)
	expectNative(t, "null", "std.format.fromBase", "ffffffffffffffff", 16) // too large for an Int
	expectNativeError(t, "the base must be between 2 and 36, got 37", "std.format.fromBase", "1", 37)
}

func TestNativeDates(t *testing.T) {
	// 13 October 2026, 14:05:07.042 UTC: a Tuesday, the 286th day
	moment := 1791900307042
	expectNative(t, `[2026, 10, 13, 14, 5, 7, 42, 2, 286, 0]`, "std.time.fields", moment, false)
	expectNative(t, `[1970, 1, 1, 0, 0, 0, 0, 4, 1, 0]`, "std.time.fields", 0, false)
	expectNative(t, `[1969, 12, 31, 23, 59, 59, 999, 3, 365, 0]`, "std.time.fields", -1, false)
	expectNative(t, fmt.Sprint(moment), "std.time.moment", []int{2026, 10, 13, 14, 5, 7, 42}, false)
	// What is out of range carries over
	expectNative(t, `[2026, 2, 1, 0, 0, 0, 0, 0, 32, 0]`, "std.time.fields", int(time.Date(2026, 1, 32, 0, 0, 0, 0, time.UTC).UnixMilli()), false)
	expectNative(t, fmt.Sprint(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()), "std.time.moment", []int{2026, 13, 1, 0, 0, 0, 0}, false)
	expectNativeError(t, "a moment takes 7 numbers, got 2", "std.time.moment", []int{2026, 1}, false)

	// In the zone of the machine, the two are each other's inverse
	local, err := callNative(t, NewVM(), "std.time.fields", moment, true)
	if err != nil {
		t.Fatal(err)
	}
	f := (*ArrayObj)(ptrFromValue(local))
	_, offset := time.UnixMilli(int64(moment)).Zone()
	if f.Get(9).AsInt() != int64(offset/60) {
		t.Errorf("expected the offset of the machine, %d minutes, got %d", offset/60, f.Get(9).AsInt())
	}
	parts := make([]int, 7)
	for i := range parts {
		parts[i] = int(f.Get(i).AsInt())
	}
	expectNative(t, fmt.Sprint(moment), "std.time.moment", parts, true)

	day := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC).UnixMilli()
	expectNative(t, fmt.Sprint(day), "std.time.read", "2026-10-13")
	expectNative(t, fmt.Sprint(day), "std.time.read", "  2026-10-13 ")
	expectNative(t, fmt.Sprint(day+14*3600000+30*60000), "std.time.read", "2026-10-13 14:30")
	expectNative(t, fmt.Sprint(day+14*3600000+30*60000), "std.time.read", "2026-10-13T14:30:00Z")
	expectNative(t, fmt.Sprint(day+12*3600000+30*60000), "std.time.read", "2026-10-13T14:30:00+02:00")
	expectNative(t, fmt.Sprint(day+13*3600000+55*60000+36000), "std.time.read", "13/Oct/2026:13:55:36 +0000")
	expectNative(t, "null", "std.time.read", "13 October")
	expectNative(t, "null", "std.time.read", "2026-13-45")
	expectNative(t, "null", "std.time.read", "")
}

func TestNativeRandomUuid(t *testing.T) {
	s := newSession(t)
	s.call("std.random.seed", 7)
	first := s.call("std.random.uuid")
	second := s.call("std.random.uuid")
	s.call("std.random.seed", 7)
	if again := s.call("std.random.uuid"); again == first || first == second {
		t.Errorf("expected a new identifier every time, the seed notwithstanding, got %s, %s, %s", first, second, again)
	}
	token := strings.Trim(s.call("std.random.token", 16), `"`)
	if len(token) != 32 || strings.Trim(token, "0123456789abcdef") != "" || token == strings.Trim(s.call("std.random.token", 16), `"`) {
		t.Errorf("expected 32 hexadecimal digits, new every time, got %s", token)
	}
	id := strings.Trim(first, `"`)
	if len(id) != 36 || id[14] != '4' || !strings.ContainsRune("89ab", rune(id[19])) || strings.Count(id, "-") != 4 {
		t.Errorf("expected a version 4 UUID, got %s", id)
	}
}

func TestNativeHttpClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/slow":
			time.Sleep(300 * time.Millisecond)
		case "/missing":
			w.WriteHeader(404)
			return
		}
		w.Header().Set("X-Seen", r.Method+" "+r.URL.RawQuery+" "+r.Header.Get("X-Token"))
		w.Header().Add("X-Twice", "a")
		w.Header().Add("X-Twice", "b")
		w.WriteHeader(201)
		io.WriteString(w, "got "+string(body))
	}))
	defer server.Close()

	s := newSession(t)
	got := s.call("std.http.exchange", "POST", server.URL+"/echo?q=1", []string{"X-Token", "secret"}, "héllo", 5000)
	for _, want := range []string{`["201", "got héllo", `, `"x-seen", "POST q=1 secret"`, `"x-twice", "a, b"`} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %s in the answer, got %s", want, got)
		}
	}
	// A status that says no is still an answer
	if got := s.call("std.http.exchange", "GET", server.URL+"/missing", []string{}, "", 5000); !strings.HasPrefix(got, `["404", ""`) {
		t.Errorf("expected a 404, got %s", got)
	}

	problem := func(kind string, method, address string, timeout int) {
		t.Helper()
		s.expect("null", "std.http.exchange", method, address, []string{}, "", timeout)
		if got := s.call("std.http.problem"); !strings.HasPrefix(got, fmt.Sprintf("[%q, ", kind)) {
			t.Errorf("%s: expected a problem of kind %q, got %s", address, kind, got)
		}
	}
	problem("timeout", "GET", server.URL+"/slow", 50)
	problem("bad url", "GET", "example.com/x", 1000)
	problem("bad url", "GET", "ftp://example.com/x", 1000)
	problem("bad url", "GET", "http://", 1000)
	problem("unknown host", "GET", "http://no-such-host.invalid/", 5000)

	closed := httptest.NewServer(http.NotFoundHandler())
	address := closed.URL
	closed.Close()
	problem("refused", "GET", address, 2000)
}

// A server listens where it is told: 0.0.0.0 is every interface of IPv4,
// and says so; a host that is not one is refused, without listening
func TestNativeHttpServerHost(t *testing.T) {
	s := newSession(t)
	opened, err := callNative(t, s.machine, "std.http.open", 0, "0.0.0.0")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(FormatValue(opened), `"`)
	if len(parts) != 7 || parts[5] != "0.0.0.0" {
		t.Fatalf("expected a number, a port and 0.0.0.0, got %s", FormatValue(opened))
	}
	id, _ := strconv.Atoi(parts[1])
	port, _ := strconv.Atoi(parts[3])
	if port <= 0 {
		t.Fatalf("expected a port, got %s", FormatValue(opened))
	}
	go http.Get(fmt.Sprintf("http://127.0.0.1:%d/here", port))
	if got := s.call("std.http.accept", id, 5000); !strings.HasPrefix(got, `["1", "GET", "/here"`) {
		t.Errorf("expected the request through 0.0.0.0, got %s", got)
	}
	s.call("std.http.respondTo", id, 1, 204, []string{}, "")
	s.call("std.http.shut", id)

	for _, host := range []string{"", "not a host!", "http://x", "a/b", "[::1]"} {
		s.expect("null", "std.http.open", 0, host)
		if got := s.call("std.http.problem"); !strings.HasPrefix(got, `["bad url", `) {
			t.Errorf("%q: expected a bad url, got %s", host, got)
		}
	}
}

func TestNativeHttpServer(t *testing.T) {
	s := newSession(t)
	opened, err := callNative(t, s.machine, "std.http.open", 0, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	// Its number, its port and the IP it listens on, as text
	parts := strings.Split(FormatValue(opened), `"`)
	if len(parts) != 7 || parts[5] != "127.0.0.1" {
		t.Fatalf("expected a number, a port and 127.0.0.1, got %s", FormatValue(opened))
	}
	id, _ := strconv.Atoi(parts[1])
	port, _ := strconv.Atoi(parts[3])
	if port <= 0 {
		t.Fatalf("expected a port, got %s", FormatValue(opened))
	}
	base := fmt.Sprintf("http://127.0.0.1:%d", port)

	// The port is taken now
	s.expect("null", "std.http.open", port, "127.0.0.1")
	s.expect(`["in use", "the port is taken"]`, "std.http.problem")

	// Nobody asked yet
	s.expect("null", "std.http.accept", id, 30)

	type reply struct {
		status int
		body   string
		header string
		err    error
	}
	ask := func(method, path, body string) chan reply {
		out := make(chan reply, 1)
		go func() {
			request, _ := http.NewRequest(method, base+path, strings.NewReader(body))
			request.Header.Set("X-Token", "secret")
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				out <- reply{err: err}
				return
			}
			defer response.Body.Close()
			content, _ := io.ReadAll(response.Body)
			out <- reply{status: response.StatusCode, body: string(content), header: response.Header.Get("X-Made-By")}
		}()
		return out
	}

	pending := ask("POST", "/users/7?page=2&q=a+b", "héllo")
	got := s.call("std.http.accept", id, 5000)
	if !strings.HasPrefix(got, `["1", "POST", "/users/7", "page=2&q=a+b", "héllo", "127.0.0.1:`) || !strings.Contains(got, `"x-token", "secret"`) {
		t.Errorf("unexpected request: %s", got)
	}
	s.call("std.http.respondTo", id, 1, 201, []string{"X-Made-By", "qoqi"}, "created é")
	if r := <-pending; r.err != nil || r.status != 201 || r.body != "created é" || r.header != "qoqi" {
		t.Errorf("unexpected answer: %+v", r)
	}

	// Requests that arrive together are both taken before either is
	// answered, and answered in the other order: each gets its own
	first, second := ask("GET", "/a", ""), ask("GET", "/b", "")
	var taken [][]string
	for i := 0; i < 2; i++ {
		taken = append(taken, strings.Split(s.call("std.http.accept", id, 5000), `"`))
	}
	for i := 1; i >= 0; i-- {
		number, _ := strconv.Atoi(taken[i][1])
		s.call("std.http.respondTo", id, number, 200, []string{}, taken[i][5])
	}
	if a, b := <-first, <-second; a.body != "/a" || b.body != "/b" {
		t.Errorf("expected each request to get its own answer, got %q and %q", a.body, b.body)
	}

	// A request is answered once
	if _, err := callNative(t, s.machine, "std.http.respondTo", id, 3, 200, []string{}, ""); err == nil || !strings.Contains(err.Error(), "no request to answer") {
		t.Errorf("expected an answer without a request to be refused, got %v", err)
	}
	if _, err := callNative(t, s.machine, "std.http.accept", 99, 0); err == nil || !strings.Contains(err.Error(), "there is no server 99") {
		t.Errorf("expected an unknown server to be refused, got %v", err)
	}

	// A bad status is refused, and the request can still be answered
	pending = ask("GET", "/c", "")
	s.call("std.http.accept", id, 5000)
	if _, err := callNative(t, s.machine, "std.http.respondTo", id, 4, 99, []string{}, ""); err == nil || !strings.Contains(err.Error(), "99 is not a status") {
		t.Errorf("expected status 99 to be refused, got %v", err)
	}
	s.call("std.http.respondTo", id, 4, 204, []string{}, "")
	if r := <-pending; r.status != 204 {
		t.Errorf("expected 204, got %+v", r)
	}

	// Closed: nothing more comes, and closing twice is fine
	s.call("std.http.shut", id)
	s.call("std.http.shut", id)
	s.expect("null", "std.http.accept", id, 0)
	if r := <-ask("GET", "/d", ""); r.err == nil {
		t.Errorf("expected no answer from a closed server, got %+v", r)
	}
}
