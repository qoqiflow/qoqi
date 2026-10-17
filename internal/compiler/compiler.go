// Package compiler orchestrates the Qoqi compilation pipeline, from source
// text to luvm bytecode.
package compiler

import (
	"fmt"
	"os"
	"runtime/debug"
	"strings"

	"github.com/qoqiflow/qoqi/internal/analysis"
	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/codegen"
	"github.com/qoqiflow/qoqi/internal/desugar"
	"github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/flowlock"
	"github.com/qoqiflow/qoqi/internal/flows"
	"github.com/qoqiflow/qoqi/internal/ir"
	"github.com/qoqiflow/qoqi/internal/irgen"
	"github.com/qoqiflow/qoqi/internal/modules"
	"github.com/qoqiflow/qoqi/internal/monomorph"
	"github.com/qoqiflow/qoqi/internal/parser"
	"github.com/qoqiflow/qoqi/internal/resolve"
	"github.com/qoqiflow/qoqi/internal/stdlib"
	"github.com/qoqiflow/qoqi/internal/types"
	"github.com/qoqiflow/qoqi/internal/vm"
)

// Error codes for the compiler driver
const (
	ErrUnsupportedFeature = "E7001" // Language feature the pipeline cannot compile yet
	ErrUnknownExtern      = "E7002" // extern fn that the runtime does not provide
)

// Result holds what each stage of a compilation produced. Stages that did
// not run because an earlier one reported errors leave their field nil.
type Result struct {
	// AST is the program as parsed: the entry file and the modules it
	// imports, as one program.
	AST *ast.Program

	// Lowered is the program after monomorphization and desugaring.
	Lowered *ast.Program

	// IR is the intermediate representation.
	IR *ir.Program

	// Chunk is the executable bytecode. It is nil unless the whole
	// pipeline succeeded.
	Chunk *vm.Chunk
}

// CompileFile reads and compiles a Qoqi source file.
func CompileFile(path string, errs *errors.Handler) (*Result, error) {
	return CompileFileOptions(path, Options{}, errs)
}

// CompileFileOptions is CompileFile with options.
func CompileFileOptions(path string, opts Options, errs *errors.Handler) (*Result, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", path, err)
	}
	return CompileOptions(string(src), path, opts, errs), nil
}

// Compile runs the pipeline on the source text of a program's entry file:
//
//	parse and load modules -> resolve -> type check -> monomorphize
//	      -> desugar -> closure analysis -> IR generation
//	      -> bytecode generation
//
// Modules the program imports are read from disk, next to file.
// Diagnostics go to errs. The pipeline stops after the first stage that
// reports an error, so later stages never see a broken program.
func Compile(src, file string, errs *errors.Handler) *Result {
	return CompileOptions(src, file, Options{}, errs)
}

// Options says where the modules of a program are found. The zero value
// reads them from disk, next to the entry file.
type Options struct {
	// Read returns the text of an imported file. Default: from disk.
	Read modules.ReadFile

	// Root is the folder module paths start at. Default: the folder of
	// the entry file.
	Root string

	// Packages maps the first part of a module path to the folder holding
	// that package's modules.
	Packages map[string]string

	// Hosted says which functions the host provides besides the runtime's
	// own, for a program to declare with `extern fn`. Default: none.
	Hosted func(name string) bool

	// WholeLibrary has the standard library checked and compiled in
	// full, used or not: for an editor, which reports what is wrong in a
	// file of the library that is open in it. Otherwise what a program
	// does not use of the library is left out (see prune.go).
	WholeLibrary bool

	// Unused has the variables, parameters, names of patterns and imports
	// of the program that nothing uses said, as warnings: for a person
	// reading what they wrote, not for a tool that compiles code it made.
	Unused bool

	// Queues are the queues the project declares, in its qoqi.json,
	// which the steps the application carries out are by
	Queues []string

	// Lock is the flow.lock of the version deployed last. Default: the one
	// next to the entry file, shop.flow.lock for shop.qi, if it is there
	Lock *flowlock.Lock
}

// lockOf is the lock a program is compared with: the one given, or the
// one kept next to its entry file.
func lockOf(file string, opts Options) *flowlock.Lock {
	if opts.Lock != nil {
		return opts.Lock
	}
	if !strings.HasSuffix(file, ".qi") {
		return nil
	}
	lock, err := flowlock.ReadLock(strings.TrimSuffix(file, ".qi") + ".flow.lock")
	if err != nil {
		return nil
	}
	return lock
}

func readSourceFile(path string) (string, error) {
	src, err := os.ReadFile(path)
	return string(src), err
}

// CompileWith is Compile with the files of imported modules supplied by
// read instead of taken from disk.
func CompileWith(src, file string, read modules.ReadFile, errs *errors.Handler) *Result {
	return CompileOptions(src, file, Options{Read: read}, errs)
}

// CompileOptions is Compile for a program whose modules are not simply
// next to the entry file: see Options.
func CompileOptions(src, file string, opts Options, errs *errors.Handler) *Result {
	// A compilation makes a great many small things and keeps most of
	// them to its end: collecting as often as a program that runs for
	// long would is a tenth of its time, for memory that is all given
	// back when it is over.
	defer debug.SetGCPercent(debug.SetGCPercent(400))

	if opts.Read == nil {
		opts.Read = readSourceFile
	}
	opts = withStandardLibrary(opts)
	result := &Result{}
	lines := parser.SourceLines(src)
	failed := errs.HasErrors

	// 1. Parsing, of the entry file and of the modules it imports. They
	// come back as one program in which every declaration has a unique
	// name, so the stages below know nothing about modules.
	prog := modules.Load(src, file, modules.Options{Read: opts.Read, Root: opts.Root, Packages: opts.Packages, Unused: opts.Unused}, errs)
	result.AST = prog
	if failed() {
		return result
	}
	declareTuples(prog)
	giveDefaultMethods(prog)
	giveShowMethods(prog)
	giveEqualMethods(prog)
	checkSupported(prog, errs, lines)
	checkExterns(prog, opts.Hosted, errs, lines)
	if failed() {
		return result
	}

	// The functions of the standard library that nothing can reach are
	// declarations only from here on: nothing in them is named or checked
	if !opts.WholeLibrary {
		prog = emptyLibrary(prog)
	}

	// 2. Name resolution
	resolver := resolve.NewResolver(errs)
	resolver.SetSourceLines(lines)
	resolver.WarnUnused(opts.Unused)
	table := resolver.Resolve(prog)
	if failed() {
		return result
	}

	// 3. Type checking
	checker := types.NewChecker(prog, table, errs)
	checker.SetSourceLines(lines)
	typeInfo := checker.Check()
	if failed() {
		return result
	}

	// The methods of the library that are its functions under another
	// name are called as those functions from here on
	forwardLibraryMethods(prog, typeInfo)

	// The variables outside functions are made by functions of their own
	// from here on, and the calls that leave parameters out give them
	lowerGlobals(prog, typeInfo)
	giveDefaults(typeInfo)

	// What nothing reaches of the standard library goes no further: the
	// stages below are most of the work, and would do it for all of it
	if !opts.WholeLibrary {
		prog = pruneLibrary(prog)
	}

	// 4. Monomorphization. It adds declarations and rewrites call sites, so
	// names and types are computed again for the new program.
	mono := monomorph.NewMonomorphizer(prog, typeInfo, table, errs)
	mono.SetSourceLines(lines)
	prog, _ = mono.Monomorphize()
	if failed() {
		return result
	}
	table, typeInfo = reanalyze(prog)

	// 5. Desugaring
	prog, _ = desugar.NewDesugarer(prog, typeInfo, table, errs, lines).Desugar()
	result.Lowered = prog
	if failed() {
		return result
	}
	table, typeInfo = reanalyze(prog)

	// The one rule of a flow: the world goes through steps
	flows.Check(prog, table, typeInfo, errs, lines, opts.Queues, lockOf(file, opts))
	if failed() {
		return result
	}

	// 6. Closure analysis
	analyzer := analysis.NewAnalyzer(prog, table, typeInfo, errs)
	analyzer.SetSourceLines(lines)
	captures := analyzer.Analyze()
	if failed() {
		return result
	}

	// 7. IR generation
	gen := irgen.NewIRGenerator(prog, typeInfo, table, captures, errs)
	gen.SetSourceLines(lines)
	result.IR = gen.Generate()
	if failed() {
		return result
	}

	// 8. Bytecode generation
	backend := codegen.NewGenerator(result.IR, hostFunctions(prog), errs)
	backend.SetSourceLines(lines)
	chunk := backend.Generate()
	if failed() {
		return result
	}

	result.Chunk = chunk
	return result
}

// declareTuples adds the enums that tuples are values of (see
// ast.TupleName). They are the same for every module, which is why they
// are added to the program once it is whole and not by the parser.
func declareTuples(prog *ast.Program) {
	declared := parser.Parse(ast.TupleSource(), "<tuples>", errors.NewHandler())
	prog.Decls = append(declared.Decls, prog.Decls...)
}

// giveDefaultMethods copies into each impl of a trait the methods the
// trait gives a body and the impl does not write itself. From here on
// they are methods of the impl like the others: checked with the type the
// impl is for, compiled once per type.
//
// In a trait with type parameters, the copy has the types the impl gives
// them: the T of `trait Source<T>` is the Int of `impl Source<Int>`.
func giveDefaultMethods(prog *ast.Program) {
	traits := make(map[string]*ast.TraitDecl)
	for _, decl := range prog.Decls {
		if trait, ok := decl.(*ast.TraitDecl); ok {
			traits[trait.Name] = trait
		}
	}
	for _, decl := range prog.Decls {
		impl, ok := decl.(*ast.ImplDecl)
		if !ok || traits[impl.TraitName] == nil {
			continue
		}
		trait := traits[impl.TraitName]

		written := make(map[string]bool, len(impl.Methods))
		for _, method := range impl.Methods {
			written[method.Name] = true
		}
		cloner := &ast.Cloner{Types: make(map[string]*ast.Type)}
		for i, param := range trait.TypeParams {
			if i < len(impl.TraitArgs) {
				cloner.Types[param] = impl.TraitArgs[i]
			}
		}
		for _, method := range trait.Methods {
			if method.Body == nil || written[method.Name] {
				continue
			}
			given := cloner.Fn(method)
			given.IsTraitMethod = false
			given.FromTrait = trait.Name
			given.Pub = true
			impl.Methods = append(impl.Methods, given)
		}
	}
}

// giveShowMethods gives each type that implements std.ops.Show a method
// the virtual machine calls to write a value of it found inside another:
// println([v]) writes what v.show() says, as println(v) does. Programs
// cannot name the method; the editor, which does not run programs, is
// not shown it.
func giveShowMethods(prog *ast.Program) {
	var template *ast.FnDecl
	for _, decl := range prog.Decls {
		impl, ok := decl.(*ast.ImplDecl)
		if !ok || impl.TraitName != types.TraitShow {
			continue
		}
		if template == nil {
			src := "impl T { pub fn " + vm.ShowMethod + "() -> String { return self." + types.MethodShow + "(); } }"
			parsed := parser.Parse(src, "<show>", errors.NewHandler())
			template = parsed.Decls[0].(*ast.ImplDecl).Methods[0]
		}
		impl.Methods = append(impl.Methods, (&ast.Cloner{}).Fn(template))
	}
}

// giveEqualMethods gives each type that implements std.ops.Equal a method
// the virtual machine calls to compare a value of it found inside another:
// [p] == [q] is what p.equals(q) says, as p == q is. Programs cannot name
// the method.
func giveEqualMethods(prog *ast.Program) {
	var template *ast.FnDecl
	for _, decl := range prog.Decls {
		impl, ok := decl.(*ast.ImplDecl)
		if !ok || impl.TraitName != types.TraitEqual {
			continue
		}
		if template == nil {
			src := "impl T { pub fn " + vm.EqualMethod + "(other: Self) -> Bool { return self.equals(other); } }"
			parsed := parser.Parse(src, "<equal>", errors.NewHandler())
			template = parsed.Decls[0].(*ast.ImplDecl).Methods[0]
		}
		impl.Methods = append(impl.Methods, (&ast.Cloner{}).Fn(template))
	}
}

// reanalyze recomputes names and types for a program produced by an earlier
// stage. The source program already passed both checks, so diagnostics here
// would only describe compiler-generated code and are discarded; anything
// that is really unusable is caught again by IR generation.
func reanalyze(prog *ast.Program) (*resolve.Table, *types.Info) {
	scratch := errors.NewHandler()
	table := resolve.NewResolver(scratch).Resolve(prog)
	typeInfo := types.NewChecker(prog, table, scratch).Check()
	return table, typeInfo
}

// withStandardLibrary makes the package "std" the library built into the
// compiler, unless the program brings a package of that name itself. Its
// files are not on disk: reads under its root are served from the binary.
func withStandardLibrary(opts Options) Options {
	if _, own := opts.Packages[stdlib.Package]; own {
		return opts
	}

	packages := map[string]string{stdlib.Package: stdlib.Root}
	for name, dir := range opts.Packages {
		packages[name] = dir
	}
	read := opts.Read
	opts.Packages = packages
	opts.Read = func(path string) (string, error) {
		if stdlib.IsPath(path) {
			return stdlib.Read(path)
		}
		return read(path)
	}
	return opts
}

// checkExterns reports `extern fn` declarations the runtime has nothing
// for. An extern function is found by its program-wide name, so one
// declared outside the standard library can only work in a build of qoqi
// that was given a native function of that name.
func checkExterns(prog *ast.Program, hosted func(string) bool, errs *errors.Handler, lines map[int]string) {
	for _, fn := range prog.Externs {
		if vm.IsNative(fn.Name) || (hosted != nil && hosted(fn.Name)) {
			continue
		}
		errs.ReportErrorWithSuggestion(ErrUnknownExtern,
			fmt.Sprintf("the runtime has no function '%s'", fn.Name),
			"An extern function is provided by qoqi itself; write the function in Qoqi, or use one of the standard library (import std.math; ...)",
			fn.Token(), lines)
	}
}

// hostFunctions lists the functions the virtual machine provides to this
// program: the builtins, the compiler's intrinsics and the program's
// extern functions.
func hostFunctions(prog *ast.Program) []string {
	var names []string
	for _, fn := range resolve.Builtins() {
		names = append(names, fn.Name)
	}
	// Intrinsics the compiler calls itself; they are not visible as names
	names = append(names, irgen.CastIntrinsic, irgen.TypeTestIntrinsic)
	for _, fn := range prog.Externs {
		names = append(names, fn.Name)
	}
	return names
}
