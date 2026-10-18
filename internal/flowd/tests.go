package flowd

import (
	"context"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/qoqiflow/qoqi/internal/compiler"
	qerrors "github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/flowtest" // the trials of std.flow
	"github.com/qoqiflow/qoqi/internal/testrun"
	"github.com/qoqiflow/qoqi/internal/vm"
)

// The tests of a draft, run by the daemon: each file name_test.qi of the
// program is compiled with a main that runs its tests, as qoqi test does,
// and run on a machine of its own, under a limit of memory and of time.
// A flow is tried there by flow.test, on a store in memory and a clock of
// its own: nothing is deployed, nothing is written in the store.

const (
	testHeap = 256 << 20
	testTime = 60 * time.Second
)

// quietMachine is a machine for a program the daemon runs for a person:
// what it prints is written to out, and nothing of the process is its,
// not the input, not the signals.
func quietMachine(out io.Writer, program string) *vm.VM {
	machine := vm.NewVMWithHeapSize(testHeap)
	machine.SetOutput(out)
	machine.SetInput(strings.NewReader(""))
	machine.SetMaxCallDepth(10_000)
	machine.SetProgram(program)
	return machine
}

// TestCase is a test of a file, and how it went.
type TestCase struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
	File    string `json:"file,omitempty"` // where it failed
	Line    int    `json:"line,omitempty"`
}

// TestFile is what became of the tests of a file.
type TestFile struct {
	File     string     `json:"file"`
	Tests    []TestCase `json:"tests"`
	Passed   int        `json:"passed"`
	Failed   int        `json:"failed"`
	Problems []Problem  `json:"problems,omitempty"` // it does not compile
	Failure  string     `json:"failure,omitempty"`  // the run itself ended badly
	Output   string     `json:"output,omitempty"`   // what the tests printed
	Millis   int64      `json:"ms"`
}

// TestRun is what became of the tests of a draft.
type TestRun struct {
	OK     bool       `json:"ok"`
	Files  []TestFile `json:"files"`
	Passed int        `json:"passed"`
	Failed int        `json:"failed"`
}

// runTests runs the tests of the files of a draft: of one file, if only
// names it, and those whose names hold filter.
func (d *Daemon) runTests(ctx context.Context, name string, files map[string]string, only, filter string) TestRun {
	run := TestRun{OK: true, Files: []TestFile{}}
	var names []string
	for f := range files {
		if strings.HasSuffix(f, testrun.Suffix) && (only == "" || f == only) {
			names = append(names, f)
		}
	}
	sort.Strings(names)
	opts, _, problem := d.draftOptions(name, files)
	for _, f := range names {
		result := TestFile{File: f, Tests: []TestCase{}}
		if problem != nil {
			result.Problems = []Problem{*problem}
		} else {
			d.runTestFile(ctx, name, f, files[f], opts, filter, &result)
		}
		if result.Failed > 0 || len(result.Problems) > 0 || result.Failure != "" {
			run.OK = false
		}
		run.Passed += result.Passed
		run.Failed += result.Failed
		run.Files = append(run.Files, result)
	}
	return run
}

func (d *Daemon) runTestFile(ctx context.Context, name, file, src string, opts compiler.Options, filter string, out *TestFile) {
	start := time.Now()
	defer func() { out.Millis = time.Since(start).Milliseconds() }()
	found, hasMain := testrun.Tests(src)
	if hasMain {
		out.Failure = "a test file has no main, nor statements outside a function: the tests are run by a main made for them"
		return
	}
	selected := testrun.Matching(found, filter)
	if len(selected) == 0 {
		if err := testrun.Compiles(src); err != nil {
			out.Failure = err.Error()
		}
		return
	}
	path := name + "/" + file
	errs := qerrors.NewHandler()
	result := compiler.CompileOptions(testrun.Program(src, file, selected), path, opts, errs)
	for _, p := range problemsOf(errs, name) {
		if p.Severity == "error" {
			out.Problems = append(out.Problems, p)
		}
	}
	if errs.HasErrors() || result == nil || result.Chunk == nil {
		return
	}

	var printed strings.Builder
	machine := quietMachine(&limited{w: &printed, room: 256 << 10}, path)
	defer flowtest.Release(machine)
	ctx, cancel := context.WithTimeout(ctx, testTime)
	defer cancel()
	_, err := machine.ExecContext(ctx, result.Chunk)
	text := printed.String()
	if err != nil {
		out.Failure = strings.TrimRight(err.Error(), "\n")
		if ctx.Err() != nil {
			out.Failure = "the tests ran longer than " + testTime.String() + ": they were stopped"
		}
	}
	out.Output, out.Tests = readReport(text, file, name)
	for _, t := range out.Tests {
		if t.OK {
			out.Passed++
		} else {
			out.Failed++
		}
	}
}

// readReport reads the report a suite prints last (see std.test): its
// name, then a line for each test, "  ok    name" or "  FAIL  name", the
// reason and the place under one that failed, and a count. What comes
// before is what the tests printed.
func readReport(text, suite, root string) (string, []TestCase) {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	at := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if lines[i] == suite {
			at = i
			break
		}
	}
	if at < 0 {
		return text, []TestCase{}
	}
	tests := []TestCase{}
	for _, line := range lines[at+1:] {
		switch {
		case strings.HasPrefix(line, "  ok    "):
			tests = append(tests, TestCase{Name: strings.TrimPrefix(line, "  ok    "), OK: true})
		case strings.HasPrefix(line, "  FAIL  "):
			tests = append(tests, TestCase{Name: strings.TrimPrefix(line, "  FAIL  ")})
		case strings.HasPrefix(line, "        at ") && len(tests) > 0:
			place := strings.TrimPrefix(line, "        at ")
			if i := strings.LastIndex(place, ":"); i > 0 {
				t := &tests[len(tests)-1]
				t.File = strings.TrimPrefix(place[:i], root+"/")
				t.Line, _ = strconv.Atoi(place[i+1:])
			}
		case strings.HasPrefix(line, "        ") && len(tests) > 0:
			t := &tests[len(tests)-1]
			if t.Message != "" {
				t.Message += "\n"
			}
			t.Message += strings.TrimPrefix(line, "        ")
		}
	}
	return strings.Join(lines[:at], "\n"), tests
}

// limited keeps the start of what is written to it.
type limited struct {
	w    io.Writer
	room int
}

func (l *limited) Write(p []byte) (int, error) {
	if keep := len(p); l.room > 0 {
		if keep > l.room {
			keep = l.room
		}
		l.w.Write(p[:keep])
		l.room -= keep
	}
	return len(p), nil
}

// test is POST /api/workspace/{program}/test: the tests of a draft, with
// what the editor has; {"file": ...} for those of one file, {"filter":
// ...} for those whose names hold it.
func (d *Daemon) test(w http.ResponseWriter, r *http.Request) {
	if d.noEditor(w) {
		return
	}
	name := r.PathValue("program")
	if err := checkProgramName(name); err != nil {
		fail(w, http.StatusBadRequest, "program", err.Error())
		return
	}
	files, asked, err := d.filesAndAsked(name, r)
	if err != nil {
		fail(w, http.StatusBadRequest, "body", err.Error())
		return
	}
	// One run of tests at a time: they take a machine each, for long
	select {
	case d.testing <- struct{}{}:
		defer func() { <-d.testing }()
	case <-r.Context().Done():
		return
	}
	answer(w, http.StatusOK, d.runTests(r.Context(), name, files, asked.File, asked.Filter))
}
