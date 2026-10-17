package fuzz

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/sandbox"
	"github.com/qoqiflow/qoqi/internal/vm"
)

// Programs made at random, each run twice: by Qoqi, and by the few lines
// of Go below that say what such a program means. The two must print the
// same.
//
// The fuzzers above find inputs that break Qoqi. They cannot find a
// program that compiles, runs and prints the wrong number: nothing tells
// them it is wrong. Here something does.
//
// The programs are small in what they use and free in how they combine
// it: numbers, variables, arithmetic, tests, loops, arrays, a struct,
// functions calling functions, and closures over variables that change
// after the closure was made.

// modulus keeps every number small: an Int has 48 bits, and a product of
// two results must fit.
const modulus = 9973

// An expression: a number, a variable, or an operation on two.
type expr struct {
	kind  string // "lit", "var", "bin", "index", "field", "call", "closure"
	value int
	name  string
	op    string
	args  []*expr
}

// A statement.
type stmt struct {
	kind  string // "let", "set", "if", "for", "while", "push", "setindex", "setfield", "print", "letclosure"
	name  string
	field string
	cond  *cond
	value *expr
	index *expr
	count int
	body  []*stmt
	other []*stmt
}

// A condition: a comparison of two expressions, or conditions joined by
// "&&" or "||", or one negated ("!"). The right side of && and || is only
// worked out if the left has not decided, which shows when it calls a
// function that prints.
type cond struct {
	op   string
	l, r *expr
	a, b *cond
}

type function struct {
	name   string
	params []string
	body   []*stmt
	result *expr
}

// --- Making a program ---

type generator struct {
	random *rand.Rand
	names  int
	funcs  []*function

	// What can be named where the generator is: variables by kind
	ints, arrays, structs, closures []string
	readOnly                        map[string]bool
}

func (g *generator) fresh(prefix string) string {
	g.names++
	return fmt.Sprintf("%s%d", prefix, g.names)
}

func (g *generator) pick(names []string) string {
	return names[g.random.Intn(len(names))]
}

func (g *generator) expr(depth int) *expr {
	choice := g.random.Intn(10)
	switch {
	case depth <= 0 || choice < 2:
		if len(g.ints) > 0 && g.random.Intn(3) > 0 {
			return &expr{kind: "var", name: g.pick(g.ints)}
		}
		return &expr{kind: "lit", value: g.random.Intn(200) - 50}
	case choice < 6:
		ops := []string{"+", "-", "*", "%"}
		return &expr{kind: "bin", op: ops[g.random.Intn(len(ops))], args: []*expr{g.expr(depth - 1), g.expr(depth - 1)}}
	case choice == 6 && len(g.arrays) > 0:
		return &expr{kind: "index", name: g.pick(g.arrays), args: []*expr{g.expr(depth - 1)}}
	case choice == 7 && len(g.structs) > 0:
		return &expr{kind: "field", name: g.pick(g.structs), op: g.pick([]string{"a", "b"})}
	case choice == 8 && len(g.funcs) > 0:
		callee := g.funcs[g.random.Intn(len(g.funcs))]
		return &expr{kind: "call", name: callee.name, args: []*expr{g.expr(depth - 1), g.expr(depth - 1)}}
	case choice == 9 && len(g.closures) > 0:
		return &expr{kind: "closure", name: g.pick(g.closures), args: []*expr{g.expr(depth - 1)}}
	}
	return g.expr(depth - 1)
}

func (g *generator) cond() *cond {
	return g.condition(2)
}

func (g *generator) condition(depth int) *cond {
	if depth > 0 {
		switch g.random.Intn(6) {
		case 0:
			return &cond{op: "&&", a: g.condition(depth - 1), b: g.condition(depth - 1)}
		case 1:
			return &cond{op: "||", a: g.condition(depth - 1), b: g.condition(depth - 1)}
		case 2:
			return &cond{op: "!", a: g.condition(depth - 1)}
		}
	}
	ops := []string{"<", "<=", ">", ">=", "==", "!="}
	return &cond{op: ops[g.random.Intn(len(ops))], l: g.expr(2), r: g.expr(2)}
}

func (c *cond) source() string {
	switch c.op {
	case "&&", "||":
		return fmt.Sprintf("(%s %s %s)", c.a.source(), c.op, c.b.source())
	case "!":
		return fmt.Sprintf("!(%s)", c.a.source())
	}
	return fmt.Sprintf("%s %s %s", c.l.source(), c.op, c.r.source())
}

// block makes statements. What they declare is gone when the block ends.
func (g *generator) block(depth, length int) []*stmt {
	ints, arrays, structs, closures := len(g.ints), len(g.arrays), len(g.structs), len(g.closures)
	var stmts []*stmt
	for i := 0; i < length; i++ {
		stmts = append(stmts, g.stmt(depth))
	}
	g.ints, g.arrays, g.structs, g.closures = g.ints[:ints], g.arrays[:arrays], g.structs[:structs], g.closures[:closures]
	return stmts
}

func (g *generator) assignable() []string {
	var names []string
	for _, name := range g.ints {
		if !g.readOnly[name] {
			names = append(names, name)
		}
	}
	return names
}

func (g *generator) stmt(depth int) *stmt {
	for {
		switch choice := g.random.Intn(13); {
		case choice < 2:
			s := &stmt{kind: "let", name: g.fresh("v"), value: g.expr(3)}
			g.ints = append(g.ints, s.name)
			return s
		case choice < 4:
			if names := g.assignable(); len(names) > 0 {
				return &stmt{kind: "set", name: g.pick(names), value: g.expr(3)}
			}
		case choice == 4:
			if g.random.Intn(4) == 0 {
				// A condition as a value: worked out into a variable
				return &stmt{kind: "printcond", cond: g.cond()}
			}
			return &stmt{kind: "print", value: g.expr(3)}
		case choice == 5 && depth > 0:
			return &stmt{kind: "if", cond: g.cond(), body: g.block(depth-1, 1+g.random.Intn(3)), other: g.block(depth-1, g.random.Intn(3))}
		case choice == 6 && depth > 0:
			s := &stmt{kind: "for", name: g.fresh("i"), count: 1 + g.random.Intn(4)}
			g.ints = append(g.ints, s.name)
			g.readOnly[s.name] = true
			s.body = g.block(depth-1, 1+g.random.Intn(3))
			g.ints = g.ints[:len(g.ints)-1]
			return s
		case choice == 7 && depth > 0:
			// Counts down a variable of its own, so that it ends
			s := &stmt{kind: "while", name: g.fresh("w"), count: 1 + g.random.Intn(4)}
			g.ints = append(g.ints, s.name)
			g.readOnly[s.name] = true
			s.body = g.block(depth-1, 1+g.random.Intn(3))
			g.ints = g.ints[:len(g.ints)-1]
			return s
		case choice == 8:
			if len(g.arrays) == 0 || g.random.Intn(3) == 0 {
				s := &stmt{kind: "letarray", name: g.fresh("xs"), value: g.expr(2), index: g.expr(2)}
				g.arrays = append(g.arrays, s.name)
				return s
			}
			return &stmt{kind: "push", name: g.pick(g.arrays), value: g.expr(3)}
		case choice == 9 && len(g.arrays) > 0:
			return &stmt{kind: "setindex", name: g.pick(g.arrays), index: g.expr(2), value: g.expr(3)}
		case choice == 10:
			if len(g.structs) == 0 || g.random.Intn(3) == 0 {
				s := &stmt{kind: "letstruct", name: g.fresh("p"), value: g.expr(2), index: g.expr(2)}
				g.structs = append(g.structs, s.name)
				return s
			}
			return &stmt{kind: "setfield", name: g.pick(g.structs), field: g.pick([]string{"a", "b"}), value: g.expr(3)}
		case choice == 11 && len(g.ints) > 0:
			// A closure over a variable, which goes on changing after
			s := &stmt{kind: "letclosure", name: g.fresh("c"), field: g.fresh("n")}
			g.ints = append(g.ints, s.field)
			g.readOnly[s.field] = true
			s.value = g.expr(3)
			g.ints = g.ints[:len(g.ints)-1]
			g.closures = append(g.closures, s.name)
			return s
		}
	}
}

func generate(seed int64) (funcs []*function, main []*stmt) {
	g := &generator{random: rand.New(rand.NewSource(seed)), readOnly: map[string]bool{}}
	for i := g.random.Intn(4); i > 0; i-- {
		f := &function{name: g.fresh("f"), params: []string{g.fresh("a"), g.fresh("b")}}
		g.ints, g.arrays, g.structs, g.closures = append([]string(nil), f.params...), nil, nil, nil
		f.body = nil
		for n := 1 + g.random.Intn(4); n > 0; n-- {
			f.body = append(f.body, g.stmt(2))
		}
		f.result = g.expr(3)
		// Only the functions before it can be called: none calls itself
		g.funcs = append(g.funcs, f)
	}
	g.ints, g.arrays, g.structs, g.closures = nil, nil, nil, nil
	for n := 4 + g.random.Intn(8); n > 0; n-- {
		main = append(main, g.stmt(3))
	}
	return g.funcs, main
}

// --- Writing it in Qoqi ---

func (e *expr) source() string {
	switch e.kind {
	case "lit":
		if e.value < 0 {
			return fmt.Sprintf("(0 - %d)", -e.value)
		}
		return fmt.Sprint(e.value)
	case "var":
		return e.name
	case "bin":
		if e.op == "%" {
			// A function makes the divisor at least 1, having worked the
			// operand out once
			return fmt.Sprintf("rem(%s, %s)", e.args[0].source(), e.args[1].source())
		}
		return fmt.Sprintf("((%s %s %s) %% %d)", e.args[0].source(), e.op, e.args[1].source(), modulus)
	case "index":
		// Any number is made a position of the array
		return fmt.Sprintf("%s[at(%s, (%s).len())]", e.name, e.args[0].source(), e.name)
	case "field":
		return e.name + "." + e.op
	case "call":
		return fmt.Sprintf("%s(%s, %s)", e.name, e.args[0].source(), e.args[1].source())
	case "closure":
		return fmt.Sprintf("%s(%s)", e.name, e.args[0].source())
	}
	panic("unknown expression " + e.kind)
}

func writeBlock(out *strings.Builder, stmts []*stmt, indent string) {
	for _, s := range stmts {
		out.WriteString(indent)
		switch s.kind {
		case "let":
			fmt.Fprintf(out, "let %s = %s;\n", s.name, s.value.source())
		case "set":
			fmt.Fprintf(out, "%s = %s;\n", s.name, s.value.source())
		case "print":
			fmt.Fprintf(out, "println(%s);\n", s.value.source())
		case "printcond":
			fmt.Fprintf(out, "println(%s);\n", s.cond.source())
		case "if":
			fmt.Fprintf(out, "if %s {\n", s.cond.source())
			writeBlock(out, s.body, indent+"    ")
			out.WriteString(indent + "} else {\n")
			writeBlock(out, s.other, indent+"    ")
			out.WriteString(indent + "}\n")
		case "for":
			fmt.Fprintf(out, "for %s in 0..%d {\n", s.name, s.count)
			writeBlock(out, s.body, indent+"    ")
			out.WriteString(indent + "}\n")
		case "while":
			fmt.Fprintf(out, "let %s = %d;\n%swhile %s > 0 {\n", s.name, s.count, indent, s.name)
			writeBlock(out, s.body, indent+"    ")
			fmt.Fprintf(out, "%s    %s -= 1;\n%s}\n", indent, s.name, indent)
		case "letarray":
			fmt.Fprintf(out, "let %s: [Int] = [%s, %s];\n", s.name, s.value.source(), s.index.source())
		case "push":
			fmt.Fprintf(out, "%s.push(%s);\n", s.name, s.value.source())
		case "setindex":
			fmt.Fprintf(out, "%s[at(%s, (%s).len())] = %s;\n", s.name, s.index.source(), s.name, s.value.source())
		case "letstruct":
			fmt.Fprintf(out, "let %s = Pair { a: %s, b: %s };\n", s.name, s.value.source(), s.index.source())
		case "setfield":
			fmt.Fprintf(out, "%s.%s = %s;\n", s.name, s.field, s.value.source())
		case "letclosure":
			fmt.Fprintf(out, "let %s = fn(%s: Int) -> Int { return %s; };\n", s.name, s.field, s.value.source())
		default:
			panic("unknown statement " + s.kind)
		}
	}
}

func qoqiSource(funcs []*function, main []*stmt) string {
	var out strings.Builder
	out.WriteString("import std.arrays;\n\nstruct Pair { a: Int; b: Int; }\n\n")
	out.WriteString("fn at(n: Int, length: Int) -> Int { return (n % length + length) % length; }\n\n")
	out.WriteString("fn rem(n: Int, by: Int) -> Int { let d = by % 7; return n % (d * d + 1); }\n\n")
	for _, f := range funcs {
		fmt.Fprintf(&out, "fn %s(%s: Int, %s: Int) -> Int {\n", f.name, f.params[0], f.params[1])
		writeBlock(&out, f.body, "    ")
		fmt.Fprintf(&out, "    return %s;\n}\n\n", f.result.source())
	}
	out.WriteString("fn main() -> Int {\n")
	writeBlock(&out, main, "    ")
	out.WriteString("    return 0;\n}\n")
	return out.String()
}

// --- Saying what it means ---

// tooLong ends the evaluation of a program that does more than a test
// should wait for; another is made in its place.
type tooLong struct{}

type closure struct {
	param string
	body  *expr
}

// scope is the variables of one call. Names are never reused, so one map
// per call is enough.
type scope struct {
	ints     map[string]int
	arrays   map[string]*[]int
	structs  map[string]*[2]int
	closures map[string]closure
}

func newScope() *scope {
	return &scope{ints: map[string]int{}, arrays: map[string]*[]int{}, structs: map[string]*[2]int{}, closures: map[string]closure{}}
}

type evaluator struct {
	funcs map[string]*function
	out   strings.Builder
	work  int
}

func position(n, length int) int {
	return (n%length + length) % length
}

func (ev *evaluator) eval(e *expr, s *scope) int {
	if ev.work++; ev.work > 200_000 {
		panic(tooLong{})
	}
	switch e.kind {
	case "lit":
		return e.value
	case "var":
		return s.ints[e.name]
	case "bin":
		l, r := ev.eval(e.args[0], s), ev.eval(e.args[1], s)
		switch e.op {
		case "+":
			return (l + r) % modulus
		case "-":
			return (l - r) % modulus
		case "*":
			return (l * r) % modulus
		default:
			return l % (r%7*(r%7) + 1)
		}
	case "index":
		xs := *s.arrays[e.name]
		return xs[position(ev.eval(e.args[0], s), len(xs))]
	case "field":
		if e.op == "a" {
			return s.structs[e.name][0]
		}
		return s.structs[e.name][1]
	case "call":
		f := ev.funcs[e.name]
		inner := newScope()
		// Arguments are worked out left to right, in the caller
		a := ev.eval(e.args[0], s)
		b := ev.eval(e.args[1], s)
		inner.ints[f.params[0]], inner.ints[f.params[1]] = a, b
		ev.run(f.body, inner)
		return ev.eval(f.result, inner)
	case "closure":
		c := s.closures[e.name]
		// It sees the variables as they are now, not as they were
		argument := ev.eval(e.args[0], s)
		s.ints[c.param] = argument
		return ev.eval(c.body, s)
	}
	panic("unknown expression " + e.kind)
}

func (ev *evaluator) test(c *cond, s *scope) bool {
	switch c.op {
	case "&&":
		return ev.test(c.a, s) && ev.test(c.b, s)
	case "||":
		return ev.test(c.a, s) || ev.test(c.b, s)
	case "!":
		return !ev.test(c.a, s)
	}
	l, r := ev.eval(c.l, s), ev.eval(c.r, s)
	switch c.op {
	case "<":
		return l < r
	case "<=":
		return l <= r
	case ">":
		return l > r
	case ">=":
		return l >= r
	case "==":
		return l == r
	}
	return l != r
}

func (ev *evaluator) run(stmts []*stmt, s *scope) {
	for _, st := range stmts {
		switch st.kind {
		case "let", "set":
			s.ints[st.name] = ev.eval(st.value, s)
		case "print":
			fmt.Fprintln(&ev.out, ev.eval(st.value, s))
		case "printcond":
			fmt.Fprintln(&ev.out, ev.test(st.cond, s))
		case "if":
			if ev.test(st.cond, s) {
				ev.run(st.body, s)
			} else {
				ev.run(st.other, s)
			}
		case "for":
			for i := 0; i < st.count; i++ {
				s.ints[st.name] = i
				ev.run(st.body, s)
			}
		case "while":
			for s.ints[st.name] = st.count; s.ints[st.name] > 0; s.ints[st.name]-- {
				ev.run(st.body, s)
			}
		case "letarray":
			first := ev.eval(st.value, s)
			second := ev.eval(st.index, s)
			s.arrays[st.name] = &[]int{first, second}
		case "push":
			// A statement run again in a loop appends to the same array
			value := ev.eval(st.value, s)
			xs := s.arrays[st.name]
			*xs = append(*xs, value)
		case "setindex":
			xs := *s.arrays[st.name]
			at := position(ev.eval(st.index, s), len(xs))
			xs[at] = ev.eval(st.value, s)
		case "letstruct":
			a := ev.eval(st.value, s)
			b := ev.eval(st.index, s)
			s.structs[st.name] = &[2]int{a, b}
		case "setfield":
			value := ev.eval(st.value, s)
			if st.field == "a" {
				s.structs[st.name][0] = value
			} else {
				s.structs[st.name][1] = value
			}
		case "letclosure":
			s.closures[st.name] = closure{param: st.field, body: st.value}
		default:
			panic("unknown statement " + st.kind)
		}
	}
}

// expected is what a program prints, or false if working it out takes too
// long to be worth running.
func expected(funcs []*function, main []*stmt) (out string, ok bool) {
	ev := &evaluator{funcs: map[string]*function{}}
	for _, f := range funcs {
		ev.funcs[f.name] = f
	}
	defer func() {
		if r := recover(); r != nil {
			if _, long := r.(tooLong); !long {
				panic(r)
			}
			out, ok = "", false
		}
	}()
	ev.run(main, newScope())
	return ev.out.String(), true
}

// --- Comparing ---

// TestRandomProgramsMeanWhatTheySay runs programs made from a fixed list
// of seeds, so that a failure can be had again: the seed is in its name.
func TestRandomProgramsMeanWhatTheySay(t *testing.T) {
	programs := 400
	if testing.Short() {
		programs = 60
	}
	checked, printed := 0, 0
	for seed := int64(1); checked < programs; seed++ {
		funcs, main := generate(seed)
		want, ok := expected(funcs, main)
		if !ok {
			continue
		}
		checked++
		printed += strings.Count(want, "\n")
		src := qoqiSource(funcs, main)

		chunk := compile(src)
		if chunk == nil {
			t.Fatalf("seed %d: the program does not compile:\n%s", seed, src)
		}
		generous := sandbox.Limits{Steps: 5_000_000, Output: 1 << 20}
		got, err := sandbox.Run(chunk, generous)
		if err != nil {
			t.Fatalf("seed %d: the program failed: %v\n%s", seed, err, src)
		}
		if got != want {
			t.Fatalf("seed %d: the program printed\n%s\nand means\n%s\n%s", seed, got, want, src)
		}

		// Written to a file and read back, it is the same program
		data, err := vm.SerializeChunk(chunk)
		if err != nil {
			t.Fatalf("seed %d: cannot be written: %v", seed, err)
		}
		loaded, err := vm.DeserializeChunk(data)
		if err != nil {
			t.Fatalf("seed %d: cannot be read back: %v", seed, err)
		}
		if again, err := sandbox.Run(loaded, generous); err != nil || again != want {
			t.Fatalf("seed %d: read back from a file, the program printed\n%s(%v)\nand means\n%s\n%s", seed, again, err, want, src)
		}
	}
	// Programs that print nothing would agree whatever Qoqi did
	if printed < programs {
		t.Errorf("%d programs printed %d lines between them: too few to tell anything", programs, printed)
	}
}
