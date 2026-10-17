package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestScripts runs programs the way scripts are run: named right after
// qoqi, and as files that start with #!/usr/bin/env qoqi.
func TestScripts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("#! lines are for Unix")
	}
	script := "#!/usr/bin/env qoqi\n" +
		"import std.os;\n" +
		"import std.io;\n" +
		"fn main() -> Int {\n" +
		"    println(\"args\", os.args());\n" +
		"    io.printError(\"to stderr\\n\");\n" +
		"    let items: [Int] = [];\n" +
		"    println(items[3]);\n" +
		"    println(\"not reached\");\n" +
		"    return 0;\n" +
		"}\n"
	dir := project(t, map[string]string{"check.qi": script, "tidy": script})
	for _, name := range []string{"check.qi", "tidy"} {
		if err := os.Chmod(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Dir(qoqi) + string(os.PathListSeparator) + os.Getenv("PATH")

	for _, c := range []struct {
		name string
		args []string
	}{
		{"qoqi run", []string{qoqi, "run", "check.qi", "a b", "--flag"}},
		{"qoqi and the file", []string{qoqi, "check.qi", "a b", "--flag"}},
		{"qoqi and a file without .qi", []string{qoqi, "tidy", "a b", "--flag"}},
		{"the file itself", []string{"./check.qi", "a b", "--flag"}},
		{"a file without .qi itself", []string{"./tidy", "a b", "--flag"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			var stdout, stderr strings.Builder
			cmd := exec.Command(c.args[0], c.args[1:]...)
			cmd.Dir, cmd.Stdout, cmd.Stderr = dir, &stdout, &stderr
			cmd.Env = append(os.Environ(), "PATH="+path)
			cmd.Run()

			if want := "args [\"a b\", \"--flag\"]\n"; stdout.String() != want {
				t.Errorf("expected the output %q, got %q", want, stdout.String())
			}
			file := filepath.Base(c.args[len(c.args)-3])
			for _, want := range []string{"to stderr", "R0002: index out of bounds: 3 (length 0)", file + ":8:"} {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("expected %q in what was written as errors:\n%s", want, stderr.String())
				}
			}
			if status := cmd.ProcessState.ExitCode(); status != 1 {
				t.Errorf("expected the status 1, got %d", status)
			}
		})
	}

	// A command keeps its meaning when a file has its name
	write(t, dir, map[string]string{"fmt": "not a program"})
	cmd := exec.Command(qoqi, "fmt", "check.qi")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(out), "1 file") {
		t.Errorf("qoqi fmt: %v\n%s", err, out)
	}
	if text := read(t, dir, "check.qi"); !strings.HasPrefix(text, "#!/usr/bin/env qoqi\n") {
		t.Errorf("expected qoqi fmt to keep the #! line, got\n%s", text)
	}
}

// tally is a script without a main: its statements run as written, and a
// return gives the status it leaves with.
const tally = `#!/usr/bin/env qoqi
import std.io;
import std.os;
import std.strings;

// Counts the words given, and fails when there are none
let words = os.args();

if words.isEmpty() {
    io.printError("usage: tally word...\n");
    return 2;
}

let counts = Counts {};

for word in words {
    counts.add(word.lower());
}

println(counts.describe());

struct Counts {
    seen: [String] = [];
    times: [Int] = [];
}

impl Counts {
    fn add(word: String) {
        for i, known in self.seen {
            if known == word {
                self.times[i] += 1;
                return;
            }
        }

        self.seen.push(word);
        self.times.push(1);
    }

    fn describe() -> String {
        let parts: [String] = [];

        for i, word in self.seen {
            parts.push("${word}=${self.times[i]}");
        }

        return parts.join(" ");
    }
}
`

func TestScriptsWithoutMain(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("#! lines are for Unix")
	}
	// The REPL runs what it takes in a main of its own, which returns
	// nothing: a script for it leaves without a status
	session := strings.Replace(tally, "    return 2;\n", "", 1)
	dir := project(t, map[string]string{"tally": tally, "tally.qi": tally, "session.qi": session})
	if err := os.Chmod(filepath.Join(dir, "tally"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Dir(qoqi) + string(os.PathListSeparator) + os.Getenv("PATH")
	run := func(input string, args ...string) (string, string, int) {
		var stdout, stderr strings.Builder
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir, cmd.Stdout, cmd.Stderr = dir, &stdout, &stderr
		cmd.Env = append(os.Environ(), "PATH="+path)
		cmd.Stdin = strings.NewReader(input)
		cmd.Run()
		return stdout.String(), stderr.String(), cmd.ProcessState.ExitCode()
	}

	for _, args := range [][]string{{"./tally"}, {qoqi, "run", "tally"}} {
		if out, errs, status := run("", append(args, "Go", "qoqi", "go", "GO")...); out != "go=3 qoqi=1\n" || status != 0 {
			t.Errorf("%v: expected the counts and the status 0, got %q (%d)\n%s", args, out, status, errs)
		}
		if out, errs, status := run("", args...); out != "" || !strings.Contains(errs, "usage: tally word...") || status != 2 {
			t.Errorf("%v: expected the usage and the status 2, got %q %q (%d)", args, out, errs, status)
		}
	}

	// It is formatted already, and checks clean
	if out, errs, status := run("", qoqi, "fmt", "--check", "tally.qi"); status != 0 {
		t.Errorf("expected the script to be formatted, got (%d)\n%s%s", status, out, errs)
	}
	if out, errs, status := run("", qoqi, "check", "tally.qi"); status != 0 || out != "tally.qi: no problems\n" {
		t.Errorf("expected the script to check clean, got %q (%d)\n%s", out, status, errs)
	}
}

// TestCheck compiles programs without running them, and says what is
// wrong as text or as JSON, for a CI step or an agent.
func TestCheck(t *testing.T) {
	dir := project(t, map[string]string{
		"good.qi": "fn main() -> Int {\n    println(\"never run\");\n    return 0;\n}\n",
		"bad.qi":  "fn main() -> Int {\n    let s = [1, 2];\n    return s.lenght();\n}\n",
	})
	run := func(args ...string) (string, string, int) {
		var stdout, stderr strings.Builder
		cmd := exec.Command(qoqi, args...)
		cmd.Dir, cmd.Stdout, cmd.Stderr = dir, &stdout, &stderr
		cmd.Run()
		return stdout.String(), stderr.String(), cmd.ProcessState.ExitCode()
	}

	if out, _, status := run("check", "good.qi"); status != 0 || out != "good.qi: no problems\n" {
		t.Errorf("expected good.qi to have no problems, without running, got %q (%d)", out, status)
	}
	if _, errs, status := run("check", "bad.qi"); status != 1 || !strings.Contains(errs, "an array has no method 'lenght'") {
		t.Errorf("expected the mistake of bad.qi, got %q (%d)", errs, status)
	}

	out, _, status := run("check", "--json", "good.qi", "bad.qi")
	var report struct {
		OK          bool     `json:"ok"`
		Files       []string `json:"files"`
		Diagnostics []struct {
			Severity, Code, Message, File, Suggestion string
			Line, Column                              int
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("expected JSON, got %v:\n%s", err, out)
	}
	if status != 1 || report.OK || len(report.Files) != 2 || len(report.Diagnostics) != 1 {
		t.Fatalf("unexpected report (%d): %+v", status, report)
	}
	d := report.Diagnostics[0]
	if d.Severity != "error" || d.Code != "E2015" || d.File != "bad.qi" || d.Line != 3 || d.Suggestion != "Did you mean 'len'?" {
		t.Errorf("unexpected diagnostic: %+v", d)
	}
}
