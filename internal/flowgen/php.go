package flowgen

import (
	"fmt"
	"sort"
	"strings"

	"github.com/qoqiflow/qoqi/internal/flowd"
)

// PHP writes, for a PHP application, a file for each class, as PSR-4
// loads them under the namespace given: a class for each struct, an
// abstract class for each enum and one for each of its variants, in a
// namespace named after the enum; for each queue, an interface its
// application implements and the class that makes its handler, with the
// library qoqi/flow. What PHP's types cannot say, PHPStan's annotations
// do. The code is written as PSR-12 has it.
func PHP(s *flowd.Schema, o Options) (map[string]string, error) {
	names, by, err := queues(s, o.Queue)
	if err != nil {
		return nil, err
	}
	ns := strings.Trim(o.Namespace, `\`)
	if ns == "" {
		return nil, fmt.Errorf(`the classes are written in a namespace: --namespace 'App\Flow'`)
	}
	g := &php{s: s, ns: ns, source: o.Source, files: map[string]string{}}
	var all []*flowd.StepInfo
	for _, name := range names {
		all = append(all, by[name]...)
	}
	for _, t := range types(s, all) {
		g.declare(t)
	}
	for _, name := range names {
		g.queue(name, by[name], o.Shapes)
	}
	return g.files, nil
}

type php struct {
	s      *flowd.Schema
	ns     string
	source string
	files  map[string]string
}

// file is a PHP file being written: its namespace, the classes it uses,
// and its body.
type phpFile struct {
	ns   string
	uses map[string]bool
	body strings.Builder
}

func (f *phpFile) p(format string, args ...any) {
	if format == "" {
		f.body.WriteString("\n")
		return
	}
	fmt.Fprintf(&f.body, format+"\n", args...)
}

// use imports a class of the library, and gives its short name.
func (f *phpFile) use(class string) string {
	f.uses[`Qoqi\Flow\`+class] = true
	return class
}

func (g *php) write(path, ns string, f *phpFile) {
	var out strings.Builder
	out.WriteString("<?php\n\n")
	fmt.Fprintf(&out, "/**\n * Written by qoqi gen from %s: change the flow, not this file.\n */\n\n", g.source)
	out.WriteString("declare(strict_types=1);\n\n")
	fmt.Fprintf(&out, "namespace %s;\n\n", ns)
	var uses []string
	for u := range f.uses {
		uses = append(uses, u)
	}
	sort.Strings(uses)
	for _, u := range uses {
		fmt.Fprintf(&out, "use %s;\n", u)
	}
	if len(uses) > 0 {
		out.WriteString("\n")
	}
	out.WriteString(f.body.String())
	g.files[path] = out.String()
}

func newPHPFile() *phpFile {
	return &phpFile{uses: map[string]bool{}}
}

// native is a type as PHP declares it.
func (g *php) native(f *phpFile, t *flowd.Type) string {
	switch t.Kind {
	case "int":
		return "int"
	case "float":
		return "float"
	case "bool":
		return "bool"
	case "string":
		return "string"
	case "void":
		return "null"
	case "option":
		inner := g.native(f, t.Elem)
		if strings.Contains(inner, "|") || inner == "mixed" || inner == "null" {
			return inner
		}
		return "?" + inner
	case "array", "dict":
		return "array"
	case "struct", "enum":
		if isResult(t) {
			return f.use("Result")
		}
		if isSettled(t) {
			return g.native(f, t.Args[0])
		}
		return g.className(f, t)
	}
	return "mixed"
}

// doc is a type as PHPStan reads it.
func (g *php) doc(f *phpFile, t *flowd.Type) string {
	switch t.Kind {
	case "void":
		return "null"
	case "option":
		return g.doc(f, t.Elem) + "|null"
	case "array":
		return "list<" + g.doc(f, t.Elem) + ">"
	case "dict":
		return "array<string, " + g.doc(f, t.Elem) + ">"
	case "struct", "enum":
		if isResult(t) {
			return f.use("Result") + "<" + g.doc(f, t.Args[0]) + ", " + g.doc(f, t.Args[1]) + ">"
		}
		if isSettled(t) {
			return g.doc(f, t.Args[0])
		}
	}
	return g.native(f, t)
}

// needsDoc says whether PHPStan learns more from a type's annotation
// than from its declaration.
func needsDoc(t *flowd.Type) bool {
	switch t.Kind {
	case "array", "dict":
		return true
	case "option":
		return needsDoc(t.Elem)
	case "enum":
		return isResult(t) || isSettled(t)
	}
	return false
}

// className is the name of a class of the program, from where f is: its
// namespace is the one given.
func (g *php) className(f *phpFile, t *flowd.Type) string {
	name := className(t)
	if f.ns != g.ns {
		f.uses[g.ns+`\`+name] = true
	}
	return name
}

// read is the PHP that reads x, a value of the boundary, as a t.
func (g *php) read(f *phpFile, t *flowd.Type, x string) string {
	plain := f.use("Plain")
	switch t.Kind {
	case "int", "float", "bool", "string":
		return plain + "::" + t.Kind + "(" + x + ")"
	case "void":
		return "null"
	case "option":
		// ?? goes after ===: what it reads is in brackets
		held := x
		if strings.Contains(x, "??") {
			held = "(" + x + ")"
		}
		return "(" + held + " === null ? null : " + g.read(f, t.Elem, held) + ")"
	case "array":
		return "array_map(static fn (mixed $v): " + g.native(f, t.Elem) + " => " + g.read(f, t.Elem, "$v") + ", " + plain + "::list(" + x + "))"
	case "dict":
		return "array_map(static fn (mixed $v): " + g.native(f, t.Elem) + " => " + g.read(f, t.Elem, "$v") + ", " + plain + "::fields(" + x + "))"
	case "struct", "enum":
		if isResult(t) {
			return plain + "::result(" + x + ", static fn (mixed $v): " + g.native(f, t.Args[0]) + " => " + g.read(f, t.Args[0], "$v") +
				", static fn (mixed $v): " + g.native(f, t.Args[1]) + " => " + g.read(f, t.Args[1], "$v") + ")"
		}
		return g.className(f, t) + "::fromPlain(" + x + ")"
	}
	return x
}

// plain is the PHP that writes x, a t, in the form of the boundary.
func (g *php) plain(f *phpFile, t *flowd.Type, x string) string {
	switch t.Kind {
	case "float":
		return f.use("Plain") + "::writeFloat(" + x + ")"
	case "void":
		return "null"
	case "option":
		inner := g.plain(f, t.Elem, x)
		if inner == x {
			return x
		}
		return "(" + x + " === null ? null : " + inner + ")"
	case "array":
		inner := g.plain(f, t.Elem, "$v")
		if inner == "$v" {
			return x
		}
		return "array_map(static fn (" + g.native(f, t.Elem) + " $v): mixed => " + inner + ", " + x + ")"
	case "dict":
		inner := g.plain(f, t.Elem, "$v")
		if inner == "$v" {
			return "(object) " + x
		}
		return "(object) array_map(static fn (" + g.native(f, t.Elem) + " $v): mixed => " + inner + ", " + x + ")"
	case "struct", "enum":
		if isResult(t) {
			return x + "->toPlain(static fn (" + g.native(f, t.Args[0]) + " $v): mixed => " + g.plain(f, t.Args[0], "$v") +
				", static fn (" + g.native(f, t.Args[1]) + " $v): mixed => " + g.plain(f, t.Args[1], "$v") + ")"
		}
		if isSettled(t) {
			return "['Done' => " + g.plain(f, t.Args[0], x) + "]"
		}
		return x + "->toPlain()"
	}
	return x
}

// docBlock writes a docblock of lines, at an indent; none when empty.
func (f *phpFile) docBlock(indent string, lines []string) {
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return
	}
	f.p("%s/**", indent)
	for _, line := range lines {
		if line == "" {
			f.p("%s *", indent)
		} else {
			f.p("%s * %s", indent, line)
		}
	}
	f.p("%s */", indent)
}

// docLines are the lines of a doc comment of the program.
func docLines(doc string) []string {
	doc = strings.TrimSpace(doc)
	if doc == "" {
		return nil
	}
	var lines []string
	for _, line := range strings.Split(doc, "\n") {
		lines = append(lines, strings.TrimSpace(line))
	}
	return lines
}

// promoted writes a constructor whose parameters are readonly properties.
func (g *php) promoted(f *phpFile, fields []*flowd.Field, name func(*flowd.Field) string) {
	var docs []string
	for _, field := range fields {
		if needsDoc(field.Type) {
			docs = append(docs, "@param "+g.doc(f, field.Type)+" $"+name(field))
		}
	}
	f.docBlock("    ", docs)
	f.p("    public function __construct(")
	for _, field := range fields {
		f.p("        public readonly %s $%s,", g.native(f, field.Type), name(field))
	}
	f.p("    ) {")
	f.p("    }")
}

func (g *php) declare(t *flowd.Type) {
	d := g.s.Types[t.Name]
	f := newPHPFile()
	f.ns = g.ns
	name := g.className(f, t)
	if d.Kind == "struct" {
		f.docBlock("", docLines(d.Doc))
		f.p("final class %s", name)
		f.p("{")
		if len(d.Fields) > 0 {
			g.promoted(f, d.Fields, func(field *flowd.Field) string { return field.Name })
			f.p("")
		}
		f.p("    public static function fromPlain(mixed $plain): self")
		f.p("    {")
		if len(d.Fields) == 0 {
			f.p("        return new self();")
		} else {
			f.p("        $fields = %s::fields($plain);", f.use("Plain"))
			f.p("")
			f.p("        return new self(")
			for _, field := range d.Fields {
				f.p("            %s: %s,", field.Name, g.read(f, field.Type, "$fields['"+field.Name+"'] ?? null"))
			}
			f.p("        );")
		}
		f.p("    }")
		f.p("")
		f.p("    public function toPlain(): object")
		f.p("    {")
		f.p("        return (object) [")
		for _, field := range d.Fields {
			f.p("            '%s' => %s,", field.Name, g.plain(f, field.Type, "$this->"+field.Name))
		}
		f.p("        ];")
		f.p("    }")
		f.p("}")
		g.write(name+".php", g.ns, f)
		return
	}

	// An enum: an abstract class, and a class for each variant, in a
	// namespace named after it
	var variants []string
	for _, v := range d.Variants {
		variants = append(variants, v.Name)
	}
	lines := docLines(d.Doc)
	if len(lines) > 0 {
		lines = append(lines, "")
	}
	lines = append(lines, "One of "+strings.Join(variants, ", ")+".")
	f.docBlock("", lines)
	f.p("abstract class %s", name)
	f.p("{")
	f.p("    public static function fromPlain(mixed $plain): self")
	f.p("    {")
	f.p("        [$variant, $values] = %s::variant($plain);", f.use("Plain"))
	f.p("")
	f.p("        return match ($variant) {")
	for _, v := range d.Variants {
		class := name + `\` + v.Name
		switch {
		case len(v.Values) == 0:
			f.p("            '%s' => new %s(),", v.Name, class)
		case len(v.Values) == 1 && !v.Named:
			f.p("            '%s' => new %s(%s),", v.Name, class, g.read(f, v.Values[0].Type, "$values"))
		case v.Named:
			f.p("            '%s' => new %s(", v.Name, class)
			for _, value := range v.Values {
				f.p("                %s: %s,", value.Name, g.read(f, value.Type, f.use("Plain")+"::fields($values)['"+value.Name+"'] ?? null"))
			}
			f.p("            ),")
		default:
			f.p("            '%s' => new %s(", v.Name, class)
			for i, value := range v.Values {
				f.p("                %s,", g.read(f, value.Type, fmt.Sprintf("%s::list($values)[%d] ?? null", f.use("Plain"), i)))
			}
			f.p("            ),")
		}
	}
	f.p("            default => throw new \\UnexpectedValueException(")
	f.p("                'a %s is one of %s, not ' . $variant,", name, strings.Join(variants, ", "))
	f.p("            ),")
	f.p("        };")
	f.p("    }")
	f.p("")
	f.p("    abstract public function toPlain(): mixed;")
	f.p("}")
	g.write(name+".php", g.ns, f)

	for _, v := range d.Variants {
		vf := newPHPFile()
		vf.ns = g.ns + `\` + name
		vf.uses[g.ns+`\`+name] = true
		vf.p("final class %s extends %s", v.Name, name)
		vf.p("{")
		var fields []*flowd.Field
		fieldName := func(field *flowd.Field) string { return field.Name }
		switch {
		case len(v.Values) == 0:
		case v.Named:
			fields = v.Values
		case len(v.Values) == 1:
			fields = v.Values
			fieldName = func(*flowd.Field) string { return "value" }
		default:
			fields = v.Values
			fieldName = func(field *flowd.Field) string { return "value" + field.Name }
		}
		if len(fields) > 0 {
			g.promoted(vf, fields, fieldName)
			vf.p("")
		}
		vf.p("    public function toPlain(): mixed")
		vf.p("    {")
		switch {
		case len(v.Values) == 0:
			vf.p("        return '%s';", v.Name)
		case v.Named:
			vf.p("        return ['%s' => (object) [", v.Name)
			for _, field := range fields {
				vf.p("            '%s' => %s,", field.Name, g.plain(vf, field.Type, "$this->"+fieldName(field)))
			}
			vf.p("        ]];")
		case len(v.Values) == 1:
			vf.p("        return ['%s' => %s];", v.Name, g.plain(vf, fields[0].Type, "$this->value"))
		default:
			var values []string
			for _, field := range fields {
				values = append(values, g.plain(vf, field.Type, "$this->"+fieldName(field)))
			}
			vf.p("        return ['%s' => [%s]];", v.Name, strings.Join(values, ", "))
		}
		vf.p("    }")
		vf.p("}")
		g.write(name+"/"+v.Name+".php", vf.ns, vf)
	}
}

// camel is a step's name as a PHP method: it is one already.
func camel(name string) string { return name }

func (g *php) queue(queue string, steps []*flowd.StepInfo, shapes map[string]string) {
	iface := pascal(queue)
	f := newPHPFile()
	f.ns = g.ns
	f.docBlock("", []string{
		"Carries out the steps of the queue " + queue + ". A step answers what it",
		"returns; an exception is a failure of its body, but for Retry, Later",
		"and Unknown of qoqi/flow.",
	})
	f.p("interface %s", iface)
	f.p("{")
	for i, step := range steps {
		if i > 0 {
			f.p("")
		}
		lines := docLines(step.Doc)
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, signature(step))
		if isSettled(step.Returns) {
			lines = append(lines, "", "It returns what is Done: the daemon gives Unknown when nothing says", "what came of it.")
		}
		undo := undoParam(g.s, step)
		if undo != nil {
			lines = append(lines, "", "It returns, when it did its work, its value and what "+step.Undo+" is given;", "an Err otherwise.")
		}
		var docs []string
		for _, p := range step.Params {
			if needsDoc(p.Type) {
				docs = append(docs, "@param "+g.doc(f, p.Type)+" $"+p.Name)
			}
		}
		returns := g.native(f, done(step.Returns))
		switch {
		case step.Returns.Kind == "void":
			returns = "void"
		case undo != nil && isResult(step.Returns):
			docs = append(docs, "@return "+f.use("Undone")+"<"+g.doc(f, step.Returns.Args[0])+", "+g.doc(f, undo)+">|"+f.use("Err")+"<"+g.doc(f, step.Returns.Args[1])+">")
			returns = "Undone|Err"
		case needsDoc(done(step.Returns)):
			docs = append(docs, "@return "+g.doc(f, done(step.Returns)))
		}
		if len(docs) > 0 {
			lines = append(lines, "")
			lines = append(lines, docs...)
		}
		f.docBlock("    ", lines)
		params := []string{}
		for _, p := range step.Params {
			params = append(params, g.native(f, p.Type)+" $"+p.Name)
		}
		params = append(params, f.use("Task")+" $task")
		f.p("    public function %s(%s): %s;", camel(stepName(step)), strings.Join(params, ", "), returns)
	}
	f.p("}")
	g.write(iface+".php", g.ns, f)

	q := newPHPFile()
	q.ns = g.ns
	q.docBlock("", []string{"Makes the handler of the queue " + queue + ", at the address qoqi.json gives it."})
	q.p("final class %sQueue", iface)
	q.p("{")
	q.p("    /**")
	q.p("     * The shapes of the steps of %s: which handler answers which step.", queue)
	q.p("     */")
	q.p("    public const SHAPES = [")
	for _, step := range steps {
		q.p("        '%s' => '%s',", stepName(step), shapes[stepName(step)])
	}
	q.p("    ];")
	q.p("")
	q.p("    public static function make(%s $steps, string ...$secrets): %s", iface, q.use("Queue"))
	q.p("    {")
	q.p("        $queue = new Queue('%s', ...$secrets);", queue)
	for _, step := range steps {
		g.handler(q, step)
	}
	q.p("")
	q.p("        return $queue;")
	q.p("    }")
	q.p("}")
	g.write(iface+"Queue.php", g.ns, q)
}

func (g *php) handler(f *phpFile, step *flowd.StepInfo) {
	task, answer := f.use("Task"), f.use("Answer")
	f.p("")
	f.p("        $queue->handle(self::SHAPES['%s'], static function (%s $task) use ($steps): %s {", stepName(step), task, answer)
	if len(step.Params) > 0 {
		f.p("            $args = $task->args;")
	}
	var args []string
	for _, p := range step.Params {
		args = append(args, g.read(f, p.Type, "$args['"+p.Name+"'] ?? null"))
	}
	args = append(args, "$task")
	call := "$steps->" + camel(stepName(step)) + "("
	if len(args) == 1 {
		call += "$task)"
	} else {
		call += "\n                " + strings.Join(args, ",\n                ") + ",\n            )"
	}
	undo := undoParam(g.s, step)
	switch {
	case step.Returns.Kind == "void":
		f.p("            %s;", call)
		f.p("")
		f.p("            return %s::value(null);", answer)
	case undo != nil && isResult(step.Returns):
		f.p("            $result = %s;", call)
		f.p("            if ($result instanceof %s) {", f.use("Undone"))
		f.p("                return %s::withUndo(", answer)
		f.p("                    ['Ok' => %s],", g.plain(f, step.Returns.Args[0], "$result->value"))
		f.p("                    %s,", g.plain(f, undo, "$result->undo"))
		f.p("                );")
		f.p("            }")
		f.p("")
		f.p("            return %s::value(['Err' => %s]);", answer, g.plain(f, step.Returns.Args[1], "$result->error"))
	case isResult(step.Returns):
		// A Result written by what writes its value and its error, a line each
		f.p("            $result = %s;", call)
		f.p("")
		f.p("            return %s::value($result->toPlain(", answer)
		f.p("                static fn (%s $v): mixed => %s,", g.native(f, step.Returns.Args[0]), g.plain(f, step.Returns.Args[0], "$v"))
		f.p("                static fn (%s $v): mixed => %s,", g.native(f, step.Returns.Args[1]), g.plain(f, step.Returns.Args[1], "$v"))
		f.p("            ));")
	default:
		f.p("            $result = %s;", call)
		f.p("")
		f.p("            return %s::value(%s);", answer, g.plain(f, step.Returns, "$result"))
	}
	f.p("        });")
}
