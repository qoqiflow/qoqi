// Package testrun is what `qoqi test` is made of: finding the test files
// of a program, the tests in each, and writing the main that runs them.
//
// A test file is a file whose name ends in _test.qi. Its tests are its
// functions whose name starts with `test`, that take nothing and give
// nothing back:
//
//	import std.test;
//
//	fn testATotalAddsUp() {
//	    test.equal(total([1, 2, 3]), 6);
//	}
//
// A test file has no main: the one written here hands each test to a
// suite of std.test, which runs them one after the other, catches the one
// that fails and reports. So the running and the reporting are Qoqi's;
// what is here is what a program cannot do for itself, which is to find
// its own functions.
package testrun

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/parser"
)

// Suffix is what the name of a test file ends with.
const Suffix = "_test.qi"

// Prefix is what the name of a test starts with.
const Prefix = "test"

// Test is one test of a file.
type Test struct {
	Function string // testATotalAddsUp
	Title    string // a total adds up
}

// Find lists the test files that the paths name: a path that is a file is
// taken as it is, whatever its name, and a folder gives the test files
// under it, however deep. Folders whose name starts with a dot are not
// gone into. The result is sorted and names no file twice.
func Find(paths []string) ([]string, error) {
	seen := map[string]bool{}
	var files []string
	add := func(file string) {
		if clean := filepath.Clean(file); !seen[clean] {
			seen[clean] = true
			files = append(files, clean)
		}
	}

	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("%s: no such file or folder", path)
		}
		if !info.IsDir() {
			add(path)
			continue
		}
		err = filepath.WalkDir(path, func(file string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			hidden := strings.HasPrefix(entry.Name(), ".") && file != path
			if entry.IsDir() && hidden {
				return filepath.SkipDir
			}
			if !entry.IsDir() && !hidden && strings.HasSuffix(entry.Name(), Suffix) {
				add(file)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(files)
	return files, nil
}

// Tests lists the tests a source file declares, in the order it does, and
// says whether it has a main of its own, written as one or as statements
// outside any function. A file that does not parse has none: compiling it
// is what says why.
func Tests(src string) (tests []Test, hasMain bool) {
	prog := parser.Parse(src, "", errors.NewHandler())
	hasMain = len(prog.Stmts) > 0
	for _, decl := range prog.Decls {
		fn, ok := decl.(*ast.FnDecl)
		if !ok || fn.Extern {
			continue
		}
		if fn.Name == "main" {
			hasMain = true
		}
		if isTest(fn) {
			tests = append(tests, Test{Function: fn.Name, Title: Title(fn.Name)})
		}
	}
	return tests, hasMain
}

// Compiles returns what keeps a source file from being read as a program,
// or nil. A file with a mistake in it shows no tests, and has to be
// compiled all the same for the mistake to be said.
func Compiles(src string) error {
	errs := errors.NewHandler()
	parser.Parse(src, "", errs)
	if errs.HasErrors() {
		return fmt.Errorf("%d syntax errors", len(errs.Diagnostics))
	}
	return nil
}

// isTest reports whether a function is a test: its name says so, and it
// can be called with nothing for nothing. A function called testData that
// returns the data of the tests is not one.
func isTest(fn *ast.FnDecl) bool {
	rest, named := strings.CutPrefix(fn.Name, Prefix)
	if !named || (rest != "" && !unicode.IsUpper(rune(rest[0])) && rest[0] != '_' && !unicode.IsDigit(rune(rest[0]))) {
		return false
	}
	returns := fn.ReturnType == nil || (fn.ReturnType.Kind == ast.TypeBasic && fn.ReturnType.Name == "Void")
	return returns && len(fn.Params) == 0 && len(fn.TypeParams) == 0
}

// Title is the name of a test as it is reported, the words its function
// is named with: testATotalAddsUp is "a total adds up". Words in capitals
// stay as they are: testReadsJSON is "reads JSON".
func Title(function string) string {
	name := []rune(strings.TrimPrefix(function, Prefix))
	var words []string
	start := 0
	flush := func(end int) {
		if word := strings.Trim(string(name[start:end]), "_"); word != "" {
			if len(word) == 1 || strings.ToUpper(word) != word {
				word = strings.ToLower(word)
			}
			words = append(words, word)
		}
		start = end
	}
	for i := 1; i < len(name); i++ {
		prev, cur := name[i-1], name[i]
		switch {
		case cur == '_':
			flush(i)
		case unicode.IsUpper(cur) && !unicode.IsUpper(prev):
			// addsUp: a word starts at each capital
			flush(i)
		case unicode.IsUpper(prev) && unicode.IsUpper(cur) && i+1 < len(name) && unicode.IsLower(name[i+1]):
			// JSONText: the last capital of a run starts the next word
			flush(i)
		}
	}
	flush(len(name))
	if len(words) == 0 {
		return function
	}
	return strings.Join(words, " ")
}

// Matching keeps the tests whose function or title holds a text,
// whatever its case. An empty text keeps them all.
func Matching(tests []Test, filter string) []Test {
	if filter == "" {
		return tests
	}
	filter = strings.ToLower(filter)
	var kept []Test
	for _, test := range tests {
		if strings.Contains(strings.ToLower(test.Function), filter) || strings.Contains(strings.ToLower(test.Title), filter) {
			kept = append(kept, test)
		}
	}
	return kept
}

// Program is the source of a test file with a main after it, which runs
// the tests in a suite called name and returns how many failed. What is
// added comes after the last line of the file, so the lines of the file
// keep their numbers, and goes by names no program writes.
func Program(src, name string, tests []Test) string {
	var sb strings.Builder
	sb.WriteString(src)
	sb.WriteString("\nimport std.test as __test;\n\nfn main() -> Int {\n")
	fmt.Fprintf(&sb, "    let __suite = __test.suite(%s);\n", quote(name))
	for _, test := range tests {
		fmt.Fprintf(&sb, "    __suite.test(%s, %s);\n", quote(test.Title), test.Function)
	}
	sb.WriteString("    return __suite.run().failed();\n}\n")
	return sb.String()
}

// quote writes a text as a Qoqi string literal.
func quote(text string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`, "\n", `\n`, "\r", `\r`, "\t", `\t`)
	return `"` + replacer.Replace(text) + `"`
}
