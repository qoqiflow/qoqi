package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"slices"
	"strconv"
	"strings"

	"github.com/qoqiflow/qoqi/internal/compiler"
	"github.com/qoqiflow/qoqi/internal/dump"
	"github.com/qoqiflow/qoqi/internal/errors"
	_ "github.com/qoqiflow/qoqi/internal/flowtest" // The trials of std.flow
	"github.com/qoqiflow/qoqi/internal/formatter"
	_ "github.com/qoqiflow/qoqi/internal/inspect" // The natives of std.compiler
	"github.com/qoqiflow/qoqi/internal/lsp"
	"github.com/qoqiflow/qoqi/internal/pkgmgr"
	"github.com/qoqiflow/qoqi/internal/project"
	"github.com/qoqiflow/qoqi/internal/runner"
	"github.com/qoqiflow/qoqi/internal/testrun"
	"github.com/qoqiflow/qoqi/internal/vm"
)

// version is what qoqi version says: set when a release is built
// (-ldflags "-X main.version=v1.2.3"), "dev" otherwise.
var version = "dev"

// Limits of the virtual machine, settable from the command line and from
// the project file; the command line wins.
var (
	heapMB   = 0 // 0: the VM's default
	maxDepth = 0
)

// Where --cpuprofile writes what the processor was doing, for go tool pprof
var cpuProfile = ""

// startProfile begins recording for --cpuprofile, if it was given.
func startProfile() {
	if cpuProfile == "" {
		return
	}
	file, err := os.Create(cpuProfile)
	if err != nil {
		fatal(err)
	}
	if err := pprof.StartCPUProfile(file); err != nil {
		fatal(err)
	}
}

// exit ends the process, with the profile written out first: os.Exit
// does not wait for anything.
func exit(code int) {
	if cpuProfile != "" {
		pprof.StopCPUProfile()
	}
	os.Exit(code)
}

// fatal prints an error and ends the process.
func fatal(err error) {
	fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	exit(1)
}

// sourceFile returns the .qi file a command should compile. arg is what
// was given on the command line: a file, a project folder, or nothing, in
// which case the project of the current directory is used. usage is
// printed when there is nothing to go by.
func sourceFile(arg, usage string) string {
	dir := "."
	if arg != "" {
		info, err := os.Stat(arg)
		if err != nil || !info.IsDir() {
			return arg // A file; a missing one is reported when it is read
		}
		dir = arg
	}

	cfg, err := project.Find(dir)
	if err != nil {
		fatal(err)
	}
	if cfg == nil {
		if arg == "" {
			fmt.Println(usage)
			exit(1)
		}
		fatal(fmt.Errorf("%s is a folder without a %s; name a .qi file, or add a project file with an \"entry\"", arg, project.FileName))
	}
	entry, err := cfg.EntryFile()
	if err != nil {
		fatal(err)
	}
	return entry
}

// projectOptions returns where the modules of a source file are found,
// from the project file in its folder or above, and applies the limits the
// project sets unless the command line already did.
func projectOptions(inputFile string) compiler.Options {
	cfg, err := project.Find(filepath.Dir(inputFile))
	if err != nil {
		fatal(err)
	}
	if cfg == nil {
		return compiler.Options{Unused: true}
	}
	if heapMB == 0 {
		heapMB = cfg.HeapMB
	}
	if maxDepth == 0 {
		maxDepth = cfg.MaxDepth
	}
	if err := cfg.CheckInstalled(); err != nil {
		fatal(err)
	}
	return compiler.Options{Root: cfg.RootDir(), Packages: cfg.PackageDirs(), Unused: true, Queues: cfg.QueueNames()}
}

// argument returns the i-th command line argument, or "".
func argument(i int) string {
	if i < len(os.Args) {
		return os.Args[i]
	}
	return ""
}

// parseOptions removes the --name=value options of qoqi itself from the
// arguments and applies them. They may appear anywhere after the command.
//
// What follows the file to run is for the program (std.os.args), except
// those options; after a lone "--" everything is for the program, whatever
// it looks like. The commands that have options of their own read them
// themselves.
func parseOptions(args []string) []string {
	var rest []string
	for i, arg := range args {
		name, value, isOption := strings.Cut(arg, "=")
		afterFile := len(rest) >= 3
		if arg == "--" && afterFile {
			return append(rest, args[i+1:]...)
		}
		isOurs := name == "--heap-mb" || name == "--max-depth" || name == "--cpuprofile"
		ofTool := len(rest) >= 2 && rest[1] != "run"
		if !strings.HasPrefix(arg, "--") || ((afterFile || ofTool) && !isOurs) {
			rest = append(rest, arg)
			continue
		}
		if name == "--cpuprofile" {
			if !isOption || value == "" {
				fmt.Fprintf(os.Stderr, "Error: option --cpuprofile needs a file, as in --cpuprofile=cpu.prof\n")
				exit(1)
			}
			cpuProfile = value
			continue
		}

		n, err := strconv.Atoi(value)
		if !isOption || err != nil || n <= 0 {
			fmt.Fprintf(os.Stderr, "Error: option %s needs a positive number, as in %s=64\n", name, name)
			exit(1)
		}
		switch name {
		case "--heap-mb":
			heapMB = n
		case "--max-depth":
			maxDepth = n
		default:
			fmt.Fprintf(os.Stderr, "Error: unknown option %s\n", name)
			exit(1)
		}
	}
	return rest
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		os.Args[1] = "version"
	}
	if len(os.Args) > 1 && isProgram(os.Args[1]) {
		// qoqi script.qi is qoqi run script.qi: what a file that starts
		// with #!/usr/bin/env qoqi is run with
		os.Args = append([]string{os.Args[0], "run"}, os.Args[1:]...)
	}
	os.Args = parseOptions(os.Args)
	startProfile()
	defer pprof.StopCPUProfile()
	if len(os.Args) < 2 {
		printHelp()
		return
	}

	command := os.Args[1]

	switch command {
	case "run":
		cmdRun(sourceFile(argument(2), "Usage: qoqi run [file.qi | project folder] [arguments]"))

	case "test":
		cmdTest(os.Args[2:])

	case "check":
		cmdCheck(os.Args[2:])

	case "fmt":
		cmdFmt()

	case "lsp":
		cmdLsp()

	case "dump":
		cmdDump(os.Args[2:])

	case "pkg":
		cmdPackages(os.Args[2:])

	case "version":
		// The machine as it calls itself (uname -m), as the releases are named
		arch := runtime.GOARCH
		if arch == "amd64" {
			arch = "x86_64"
		}
		fmt.Printf("qoqi %s %s/%s\n", version, runtime.GOOS, arch)

	case "help", "--help", "-h":
		printHelp()

	default:
		if slices.Contains(flowCommands, command) {
			cmdFlow(os.Args[1:])
			return
		}
		fmt.Printf("Unknown command: %s\n\n", command)
		printHelp()
		exit(1)
	}
}

// commands are the first words qoqi understands. A file of that name is
// run with qoqi run.
var commands = append([]string{"run", "test", "check", "fmt", "lsp", "dump", "pkg", "version", "help"}, flowCommands...)

// isProgram reports whether a first argument names a program to run
// rather than a command: a .qi file, or any file that is not called as a
// command is (a script without an extension, run through #!).
func isProgram(arg string) bool {
	if strings.HasSuffix(arg, ".qi") {
		return true
	}
	if slices.Contains(commands, arg) || strings.HasPrefix(arg, "-") {
		return false
	}
	info, err := os.Stat(arg)
	return err == nil && info.Mode().IsRegular()
}

func printHelp() {
	fmt.Println("Qoqi Flow: durable workflows as plain programs")
	fmt.Println()
	fmt.Println("Usage: qoqi <command> [arguments]")
	fmt.Println()
	fmt.Println("Flows:")
	fmt.Println("  qoqi serve [program]        Run the flows of a program, kept in flows.db; qoqi serve --help for the rest")
	fmt.Println("  qoqi deploy <program>       A new version of a program, to the daemon that serves it")
	fmt.Println("  qoqi start <Flow> <name>    Start an instance, with its fields as JSON")
	fmt.Println("  qoqi ls                     The instances, and what each is doing")
	fmt.Println("  qoqi show <name>            An instance: where it is, what it was given, what its steps returned")
	fmt.Println("  qoqi result <name>          What its run returned")
	fmt.Println("  qoqi send <name> <inbox>    A message for an inbox of an instance")
	fmt.Println("  qoqi cancel <name>          End it: what it gave flow.undo runs")
	fmt.Println("  qoqi ui                     Open the console in a browser")
	fmt.Println("  qoqi gen go|php|ts <program>  What an application needs to carry out the steps of its queues")
	fmt.Println("  Also: undeploy, versions, diff, resume, answer, end. Each says its usage with --help")
	fmt.Println()
	fmt.Println("Programs:")
	fmt.Println("  qoqi run <file> [args]      Run a program; qoqi <file.qi> is the same")
	fmt.Println("  qoqi test [files]           Run the tests: the functions test...() of the files *_test.qi")
	fmt.Println("  qoqi check [--json] [files] Compile without running and say what is wrong; --json for tools")
	fmt.Println("  qoqi fmt [files]            Format .qi files where they are; --check only says which would change")
	fmt.Println("  qoqi dump <file> [--ir]     What the compiler made of a program: its bytecode, or --ir its intermediate form")
	fmt.Println("  qoqi pkg <command>          The packages of the project: require, install, update, remove, show, why, outdated")
	fmt.Println("  qoqi lsp                    Start the language server, for an editor to talk to")
	fmt.Println("  qoqi version                Say which qoqi this is")
	fmt.Println()
	fmt.Println("Options for commands that execute a program:")
	fmt.Println("  --heap-mb=N                 Memory the program may use, in MB (default 256)")
	fmt.Println("  --max-depth=N               How deep calls may nest (default 10000)")
	fmt.Println("  --cpuprofile=FILE           Write where the time went, for go tool pprof")
	fmt.Println()
	fmt.Println("Projects:")
	fmt.Println("  A qoqi.json in the program's folder, or above it, says where the program")
	fmt.Println("  starts (\"entry\"), where module paths start (\"root\"), which packages it")
	fmt.Println("  uses (\"packages\"), the queues of its steps (\"queues\") and its limits")
	fmt.Println("  (\"heap_mb\", \"max_depth\"). With one, a command takes a project folder, or")
	fmt.Println("  nothing at all: qoqi serve runs the project of the current folder.")
}

// compileSource runs the compilation pipeline on a .qi file. Diagnostics
// are printed to stderr; the process exits if the program does not compile.
func compileSource(inputFile string) *compiler.Result {
	programFile = inputFile
	errs := errors.NewHandler()
	result, err := compiler.CompileFileOptions(inputFile, projectOptions(inputFile), errs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		exit(1)
	}

	for _, d := range errs.Diagnostics {
		fmt.Fprintln(os.Stderr, d.String())
	}
	if errs.HasErrors() {
		exit(1)
	}
	return result
}

// newVM creates a virtual machine with the limits from the command line
// and the project file, and what follows the file as the program's
// arguments.
func newVM() *vm.VM {
	var args []string
	if len(os.Args) > 3 {
		args = os.Args[3:]
	}
	return runner.Machine(runner.Limits{HeapMB: heapMB, MaxDepth: maxDepth}, args, programFile, exit)
}

// leaveIfAsked ends the process with the status the program asked for, if
// the run ended by a call of std.os.exit and not by a failure. A program
// stopped from the keyboard ends as the shell expects, having said where
// it was.
func leaveIfAsked(err error) {
	runner.LeaveIfAsked(err, exit)
}

// programFile is the file being run, for the program to find what comes
// with it (std.os.program).
var programFile string

// cmdRun runs a program. An Int returned by main becomes the process exit
// code; the run writes nothing of its own.
func cmdRun(inputFile string) {
	chunk := compileSource(inputFile).Chunk
	result, err := newVM().Exec(chunk)
	runner.Finish(result, err, exit)
}

// runTool runs a Qoqi program kept in the binary, with what follows the
// command as its arguments, and returns the status it ended with: what
// its main returned, or 1 if it failed.
func runTool(source, file string) int {
	return runToolWith(source, file, compiler.Options{}, os.Args[2:])
}

// runToolWith is runTool for a program of several modules, whose files
// opts says how to read, given the arguments it gets.
func runToolWith(source, file string, opts compiler.Options, args []string) int {
	errs := errors.NewHandler()
	result := compiler.CompileOptions(source, file, opts, errs)
	if errs.HasErrors() {
		for _, d := range errs.Diagnostics {
			fmt.Fprintln(os.Stderr, d.String())
		}
		return 1
	}
	programFile = file
	machine := newVM()
	machine.SetArgs(args)
	value, err := machine.Exec(result.Chunk)
	if err != nil {
		leaveIfAsked(err)
		fmt.Fprint(os.Stderr, strings.TrimRight(err.Error(), "\n")+"\n")
		return 1
	}
	if value.IsInt() {
		return int(value.AsInt() & 0xFF)
	}
	return 0
}

// cmdLsp runs the language server: a Qoqi program kept in the binary,
// which talks to an editor over the input and the output. It ends when
// the editor says so, by a call of os.exit; coming back from its main
// means the editor went away without a word.
func cmdLsp() {
	runTool(lsp.Source, lsp.File)
	exit(1)
}

// cmdPackages runs the package manager, a Qoqi program too, with the
// command as its first argument: qoqi pkg install, update, require,
// remove, show, why, outdated.
func cmdPackages(args []string) {
	if len(args) == 0 || !slices.Contains(pkgmgr.Commands, args[0]) {
		fmt.Printf("Usage: qoqi pkg <%s> [arguments]\n", strings.Join(pkgmgr.Commands, "|"))
		exit(1)
	}
	exit(runToolWith(pkgmgr.Source, pkgmgr.File, pkgmgr.Options(), args))
}

// cmdFmt runs the formatter: a Qoqi program too.
func cmdFmt() {
	exit(runTool(formatter.Source, formatter.File))
}

// cmdCheck compiles programs without running them and says what is wrong
// with them: for an editor, a CI step or an agent that writes code. With
// --json the diagnostics are one JSON document on the standard output.
func cmdCheck(args []string) {
	const usage = "Usage: qoqi check [--json] [file.qi | project folder]..."
	asJSON := false
	var paths []string
	for _, arg := range args {
		switch {
		case arg == "--json":
			asJSON = true
		case strings.HasPrefix(arg, "-"):
			fmt.Fprintf(os.Stderr, "Error: unknown option %s\n%s\n", arg, usage)
			os.Exit(2)
		default:
			paths = append(paths, arg)
		}
	}
	if len(paths) == 0 {
		paths = []string{""}
	}

	type position struct {
		File      string `json:"file"`
		Line      int    `json:"line"`
		Column    int    `json:"column"`
		EndColumn int    `json:"endColumn,omitempty"`
	}
	type diagnostic struct {
		Severity string `json:"severity"`
		Code     string `json:"code"`
		Message  string `json:"message"`
		position
		Suggestion string `json:"suggestion,omitempty"`
	}
	report := struct {
		OK          bool         `json:"ok"`
		Files       []string     `json:"files"`
		Diagnostics []diagnostic `json:"diagnostics"`
	}{OK: true, Diagnostics: []diagnostic{}}

	for _, path := range paths {
		file := sourceFile(path, usage)
		report.Files = append(report.Files, file)
		errs := errors.NewHandler()
		if _, err := compiler.CompileFileOptions(file, projectOptions(file), errs); err != nil {
			fatal(err)
		}
		for _, d := range errs.Diagnostics {
			if d.Severity == errors.SeverityError {
				report.OK = false
			}
			if !asJSON {
				fmt.Fprintln(os.Stderr, d.String())
				continue
			}
			at := position{File: d.Pos.File, Line: d.Pos.Line, Column: d.Pos.Col}
			if len(d.Highlights) > 0 {
				at.EndColumn = d.Highlights[0].EndCol
			}
			report.Diagnostics = append(report.Diagnostics, diagnostic{
				Severity: strings.ToLower(d.Severity.String()), Code: d.Code, Message: d.Message,
				position: at, Suggestion: d.Suggestion,
			})
		}
	}

	if asJSON {
		out, _ := json.MarshalIndent(report, "", "  ")
		fmt.Println(string(out))
	} else if report.OK {
		fmt.Printf("%s: no problems\n", strings.Join(report.Files, ", "))
	}
	if !report.OK {
		os.Exit(1)
	}
}

// cmdTest runs the tests of the files and folders named, or of the
// current folder: every function test...() of every file *_test.qi.
// Each file is a program of its own, compiled with a main that runs its
// tests (see internal/testrun). The process ends with 1 if a test failed
// or a file could not be run.
func cmdTest(args []string) {
	const usage = "Usage: qoqi test [--filter=text] [file_test.qi | folder]..."
	filter := ""
	var paths []string
	for _, arg := range args {
		if value, ok := strings.CutPrefix(arg, "--filter="); ok {
			filter = value
		} else if strings.HasPrefix(arg, "--") {
			fmt.Fprintf(os.Stderr, "Error: unknown option %s\n%s\n", arg, usage)
			exit(2)
		} else {
			paths = append(paths, arg)
		}
	}
	if len(paths) == 0 {
		paths = []string{"."}
	}

	files, err := testrun.Find(paths)
	if err != nil {
		fatal(err)
	}
	if len(files) == 0 {
		fmt.Printf("no test files: a test file is called name%s, and its tests fn %sSomething()\n", testrun.Suffix, testrun.Prefix)
		return
	}

	ran, tests, failed, broken := 0, 0, 0, 0
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			broken++
			continue
		}
		src := string(data)
		found, hasMain := testrun.Tests(src)
		if hasMain {
			fmt.Fprintf(os.Stderr, "%s: a test file has no main, nor statements outside a function: qoqi test writes the main that runs its tests\n", file)
			broken++
			continue
		}
		selected := testrun.Matching(found, filter)
		if len(selected) == 0 && !(filter == "" && testrun.Compiles(src) != nil) {
			// Nothing to run here; a file that does not parse is still
			// compiled below, for what is wrong with it to be said
			continue
		}

		count, ok := runTests(file, testrun.Program(src, file, selected), ran > 0)
		if !ok {
			broken++
			continue
		}
		ran++
		tests += len(selected)
		failed += count
	}

	switch {
	case ran == 0 && broken == 0 && filter != "":
		fmt.Printf("no test matches '%s'\n", filter)
	case ran == 0 && broken == 0:
		fmt.Printf("no tests: a test is a function fn %sSomething() of a test file\n", testrun.Prefix)
	case ran+broken > 1:
		summary := fmt.Sprintf("\n%s in %s: %d passed", plural(tests, "test"), plural(ran, "file"), tests-failed)
		if failed > 0 {
			summary += fmt.Sprintf(", %d failed", failed)
		}
		if broken > 0 {
			summary += fmt.Sprintf("; %s did not run", plural(broken, "file"))
		}
		fmt.Println(summary)
	}
	if failed > 0 || broken > 0 {
		exit(1)
	}
}

// plural is a count with its noun: 1 test, 2 tests.
func plural(count int, noun string) string {
	if count == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", count, noun)
}

// runTests compiles and runs a test file with its main, and returns how
// many of its tests failed. ok is false if it did not get to the end:
// it does not compile, or it stopped outside a test. apart leaves an
// empty line before what the tests print, after those of another file.
func runTests(file, src string, apart bool) (failed int, ok bool) {
	programFile = file
	errs := errors.NewHandler()
	options := projectOptions(file)
	if options.Root == "" {
		// Without a project, module paths start at the folder the tests
		// are run from: shop/price_test.qi imports shop.price, as it
		// would in a project there
		if here, err := os.Getwd(); err == nil {
			options.Root = here
		}
	}
	result := compiler.CompileOptions(src, file, options, errs)
	for _, d := range errs.Diagnostics {
		fmt.Fprintln(os.Stderr, d.String())
	}
	if errs.HasErrors() {
		return 0, false
	}
	if apart {
		fmt.Println()
	}

	machine := newVM()
	machine.SetArgs(nil)
	value, err := machine.Exec(result.Chunk)
	if err != nil {
		if vm.IsInterrupted(err) {
			leaveIfAsked(err)
		}
		if status, asked := vm.ExitCode(err); asked {
			// os.exit is not a failure a suite catches: it ends the program
			fmt.Fprintf(os.Stderr, "%s: a test ended the program with os.exit(%d): the tests after it did not run\n", file, status)
			return 0, false
		}
		fmt.Fprint(os.Stderr, strings.TrimRight(err.Error(), "\n")+"\n")
		return 0, false
	}
	return int(value.AsInt()), true
}

// cmdDump prints what the compiler made of a program: its bytecode, or
// with --ir the intermediate form of its source. For looking at what the
// virtual machine runs, when a flow does not do what its source says.
func cmdDump(args []string) {
	usage := "Usage: qoqi dump [file.qi | project folder] [--ir]"
	ir := false
	var rest []string
	for _, arg := range args {
		if arg == "--ir" {
			ir = true
			continue
		}
		rest = append(rest, arg)
	}
	if len(rest) > 1 {
		fmt.Println(usage)
		exit(1)
	}
	result := compileSource(sourceFile(firstOf(rest), usage))
	if ir {
		fmt.Print(result.IR.Dump())
		return
	}
	fmt.Print(dump.Disassemble(result.Chunk))
}

// firstOf is the first of the arguments, or "".
func firstOf(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}
