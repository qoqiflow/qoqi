package parser

import (
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/errors"
)

func parseDecls(t *testing.T, src string) []ast.Decl {
	t.Helper()
	errs := errors.NewHandler()
	prog := Parse(src, "test.qi", errs)
	for _, d := range errs.Diagnostics {
		t.Fatalf("unexpected diagnostic: %s", d.Message)
	}
	return prog.Decls
}

// boundsOf writes the requirements of type parameters: "T: A + B; U: C<Int>".
func boundsOf(names []string, bounds map[string][]*ast.Type) string {
	var parts []string
	for _, name := range names {
		var traits []string
		for _, trait := range bounds[name] {
			traits = append(traits, trait.String())
		}
		if len(traits) > 0 {
			parts = append(parts, name+": "+strings.Join(traits, " + "))
		}
	}
	return strings.Join(parts, "; ")
}

func TestParseTypeParameterRequirements(t *testing.T) {
	decls := parseDecls(t, `
		fn largest<T: Ordered>(items: [T]) -> T? { return null; }
		fn show<A, B: Show + Ordered, C: Source<A>>(a: A, b: B, c: C) { }
		struct Sorted<T: Ordered> { items: [T]; }
		enum Tree<T: Ordered + shapes.Show> { Leaf, Node(T) }
		impl<T: Ordered> Sorted<T> { fn first() -> T? { return null; } fn pick<U: Show>(u: U) { } }
		fn plain<T, U>(a: T, b: U) { }
	`)

	fn := decls[0].(*ast.FnDecl)
	if got := boundsOf(fn.TypeParams, fn.Bounds); got != "T: Ordered" {
		t.Errorf("largest: got %q", got)
	}
	fn = decls[1].(*ast.FnDecl)
	if got := boundsOf(fn.TypeParams, fn.Bounds); got != "B: Show + Ordered; C: Source<A>" || len(fn.TypeParams) != 3 {
		t.Errorf("show: got %q for %v", got, fn.TypeParams)
	}
	if s := decls[2].(*ast.StructDecl); boundsOf(s.TypeParams, s.Bounds) != "T: Ordered" {
		t.Errorf("Sorted: got %q", boundsOf(s.TypeParams, s.Bounds))
	}
	if e := decls[3].(*ast.EnumDecl); boundsOf(e.TypeParams, e.Bounds) != "T: Ordered + shapes.Show" {
		t.Errorf("Tree: got %q", boundsOf(e.TypeParams, e.Bounds))
	}
	impl := decls[4].(*ast.ImplDecl)
	if boundsOf(impl.TypeParams, impl.Bounds) != "T: Ordered" || impl.StructName != "Sorted" || impl.TraitName != "" {
		t.Errorf("impl: got %q on %s", boundsOf(impl.TypeParams, impl.Bounds), impl.StructName)
	}
	if pick := impl.Methods[1]; boundsOf(pick.TypeParams, pick.Bounds) != "U: Show" {
		t.Errorf("pick: got %q", boundsOf(pick.TypeParams, pick.Bounds))
	}
	if plain := decls[5].(*ast.FnDecl); plain.Bounds != nil {
		t.Errorf("expected no requirements on plain, got %v", plain.Bounds)
	}
}

func TestParseTraitsWithTypeParameters(t *testing.T) {
	decls := parseDecls(t, `
		trait Source<T> { fn next() -> T?; fn take(n: Int) -> [T] { return []; } }
		trait Pairs<K, V> { fn pairs() -> [(K, V)]; }
		impl Source<Int> for Counter { fn next() -> Int? { return 1; } }
		impl<T> Source<T> for List<T> { fn next() -> T? { return null; } }
		impl<K: Show, V> Pairs<K, [V]> for Table<K, V> { fn pairs() -> [(K, [V])] { return []; } }
		impl Show for Counter { fn show() -> String { return ""; } }
		impl<T> List<T> { fn size() -> Int { return 0; } }
		impl ops.Iterable<(Int, String)?> for Counter { fn iterate() -> [(Int, String)?] { return []; } }
	`)

	source := decls[0].(*ast.TraitDecl)
	if strings.Join(source.TypeParams, ",") != "T" || len(source.Methods) != 2 || source.Methods[0].Body != nil || source.Methods[1].Body == nil {
		t.Errorf("unexpected trait Source: %v, %d methods", source.TypeParams, len(source.Methods))
	}
	if pairs := decls[1].(*ast.TraitDecl); strings.Join(pairs.TypeParams, ",") != "K,V" {
		t.Errorf("unexpected trait Pairs: %v", pairs.TypeParams)
	}

	impls := []struct {
		trait, structName, params, args string
	}{
		{"Source", "Counter", "", "Int"},
		{"Source", "List", "T", "T"},
		{"Pairs", "Table", "K,V", "K [V]"},
		{"Show", "Counter", "", ""},
		{"", "List", "T", ""},
		{"ops.Iterable", "Counter", "", "__Tuple2<Int, String>?"},
	}
	for i, want := range impls {
		impl := decls[i+2].(*ast.ImplDecl)
		var args []string
		for _, arg := range impl.TraitArgs {
			args = append(args, arg.String())
		}
		if impl.TraitName != want.trait || impl.StructName != want.structName ||
			strings.Join(impl.TypeParams, ",") != want.params || strings.Join(args, " ") != want.args {
			t.Errorf("impl %d: got trait %q with <%s> for %q, parameters %v", i, impl.TraitName, strings.Join(args, " "), impl.StructName, impl.TypeParams)
		}
	}
}

func TestParseFieldDefaults(t *testing.T) {
	decls := parseDecls(t, `
		struct Config {
			host: String = "localhost";
			port: Int = 80 * 100 + 80;
			tags: [String] = [];
			retry: Retry = Retry { times: 3 };
			name: String;
			timeout: Int?,
		}
	`)
	fields := decls[0].(*ast.StructDecl).Fields
	want := []string{`"localhost"`, "((80 * 100) + 80)", "[]", "Retry { times: 3 }", "", ""}
	for i, field := range fields {
		got := ""
		if field.Default != nil {
			got = field.Default.String()
		}
		if got != want[i] {
			t.Errorf("field %s: expected the value %q, got %q", field.Name, want[i], got)
		}
	}
}

func TestParseTraitErrors(t *testing.T) {
	tests := []struct{ src, want string }{
		{`trait Source<T: Ordered> { }`, "a type parameter cannot be given a requirement here"},
		{`impl<T> Box<T: Show> { }`, "a type parameter cannot be given a requirement here"},
		{`fn f<T: >(a: T) { }`, "expected trait name"},
		{`fn f<T: Show +>(a: T) { }`, "expected trait name"},
		{`struct S { a: Int = ; }`, "expected expression"},
	}
	for _, tt := range tests {
		errs := errors.NewHandler()
		Parse(tt.src, "test.qi", errs)
		if !errs.HasErrors() || !strings.Contains(errs.Diagnostics[0].Message, tt.want) {
			t.Errorf("%s: expected an error containing %q, got %v", tt.src, tt.want, errs.Diagnostics)
		}
	}
}

func TestCloner(t *testing.T) {
	decls := parseDecls(t, `
		fn f<T: Show>(items: [T], n: Int) -> T? {
			let total: T? = null;
			for i, item in items {
				if i > n { break; } else { continue; }
			}
			while n > 0 { n -= 1; }
			match items[0] {
				case Shape.Rect(w, _): { return w as T; }
				case Int: { return null; }
				default: { let g = fn(x: T) -> [T] { return [x, Box<T> { item: x }.item]; }; }
			}
			return a?.b(c)? ?? (1, "x").0;
		}
	`)
	original := decls[0].(*ast.FnDecl)

	// A copy reads the same and shares nothing
	plain := (&ast.Cloner{}).Fn(original)
	if plain.String() != original.String() {
		t.Errorf("expected a copy to read the same:\n%s\n%s", original, plain)
	}
	if plain.Body == original.Body || plain.Params[0] == original.Params[0] || plain.Body.Stmts[0] == original.Body.Stmts[0] {
		t.Error("expected the copy to have nodes of its own")
	}
	plain.Body.Stmts[0].(*ast.LetStmt).Name = "changed"
	if original.Body.Stmts[0].(*ast.LetStmt).Name != "total" {
		t.Error("changing the copy changed the original")
	}

	// With types to replace, every T becomes what it stands for
	money := &ast.Type{Kind: ast.TypeBasic, Name: "Money"}
	replaced := (&ast.Cloner{Types: map[string]*ast.Type{"T": money}}).Fn(original).String()
	if strings.Contains(replaced, "T?") || strings.Contains(replaced, "[T]") || strings.Contains(replaced, "as T") || strings.Contains(replaced, "Box<T>") {
		t.Errorf("expected every T to be replaced, got:\n%s", replaced)
	}
	for _, want := range []string{"items: [Money]", "-> Money?", "let total: Money? = null", "as Money", "fn(x: Money) -> [Money]", "Box<Money>"} {
		if !strings.Contains(replaced, want) {
			t.Errorf("expected %q in the copy, got:\n%s", want, replaced)
		}
	}
	if !strings.Contains(original.String(), "let total: T? = null") {
		t.Error("replacing types in the copy changed the original")
	}
}
