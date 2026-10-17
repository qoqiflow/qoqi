package flowgen

import (
	"fmt"
	"strings"

	"github.com/qoqiflow/qoqi/internal/flowd"
)

// TS writes, for a TypeScript application, the types of the steps of
// the queues, an interface for each queue and the function that makes
// its handler, with the library @qoqi/flow. The JSON of the boundary is
// the value TypeScript holds: a struct is an interface, an enum a union
// of its variants as the boundary writes them, so nothing is converted.
func TS(s *flowd.Schema, o Options) ([]byte, error) {
	names, by, err := queues(s, o.Queue)
	if err != nil {
		return nil, err
	}
	from := o.From
	if from == "" {
		from = "@qoqi/flow"
	}
	g := &tsGen{s: s}
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

	var head strings.Builder
	fmt.Fprintf(&head, "// Written by qoqi gen from %s: change the flow, not this file.\n\n", o.Source)
	body := g.out.String()
	var values, types []string
	for _, name := range []string{"Queue", "Undone", "answer"} {
		if strings.Contains(body, name+"(") || strings.Contains(body, name+".") || strings.Contains(body, " "+name+" ") || strings.Contains(body, "instanceof "+name) {
			values = append(values, name)
		}
	}
	for _, name := range []string{"Awaitable", "Result", "Task"} {
		if strings.Contains(body, name+"<") || strings.Contains(body, ": "+name) {
			types = append(types, name)
		}
	}
	if len(values) > 0 {
		fmt.Fprintf(&head, "import { %s } from %q;\n", strings.Join(values, ", "), from)
	}
	if len(types) > 0 {
		fmt.Fprintf(&head, "import type { %s } from %q;\n", strings.Join(types, ", "), from)
	}
	return []byte(head.String() + body), nil
}

type tsGen struct {
	s   *flowd.Schema
	out strings.Builder
}

func (g *tsGen) p(format string, args ...any) {
	if format == "" {
		g.out.WriteString("\n")
		return
	}
	fmt.Fprintf(&g.out, format+"\n", args...)
}

// tsType is a type as TypeScript writes it.
func (g *tsGen) tsType(t *flowd.Type) string {
	switch t.Kind {
	case "int", "float":
		return "number"
	case "bool":
		return "boolean"
	case "string":
		return "string"
	case "void":
		return "null"
	case "option":
		return g.tsType(t.Elem) + " | null"
	case "array":
		inner := g.tsType(t.Elem)
		if strings.Contains(inner, " ") {
			return "(" + inner + ")[]"
		}
		return inner + "[]"
	case "dict":
		return "Record<string, " + g.tsType(t.Elem) + ">"
	case "struct", "enum":
		if isResult(t) {
			return "Result<" + g.tsType(t.Args[0]) + ", " + g.tsType(t.Args[1]) + ">"
		}
		if isSettled(t) {
			return g.tsType(t.Args[0])
		}
		return className(t)
	}
	return "unknown"
}

// jsdoc writes a comment of lines, at an indent.
func (g *tsGen) jsdoc(indent string, lines []string) {
	if len(lines) == 0 {
		return
	}
	if len(lines) == 1 {
		g.p("%s/** %s */", indent, lines[0])
		return
	}
	g.p("%s/**", indent)
	for _, line := range lines {
		if line == "" {
			g.p("%s *", indent)
		} else {
			g.p("%s * %s", indent, line)
		}
	}
	g.p("%s */", indent)
}

func (g *tsGen) declare(t *flowd.Type) {
	d := g.s.Types[t.Name]
	name := className(t)
	g.p("")
	g.jsdoc("", docLines(d.Doc))
	if d.Kind == "struct" {
		g.p("export interface %s {", name)
		for _, f := range d.Fields {
			g.p("  %s: %s;", f.Name, g.tsType(f.Type))
		}
		g.p("}")
		return
	}
	g.p("export type %s =", name)
	for i, v := range d.Variants {
		end := ""
		if i == len(d.Variants)-1 {
			end = ";"
		}
		switch {
		case len(v.Values) == 0:
			g.p("  | %q%s", v.Name, end)
		case v.Named:
			var fields []string
			for _, f := range v.Values {
				fields = append(fields, f.Name+": "+g.tsType(f.Type))
			}
			g.p("  | { %s: { %s } }%s", v.Name, strings.Join(fields, "; "), end)
		case len(v.Values) == 1:
			g.p("  | { %s: %s }%s", v.Name, g.tsType(v.Values[0].Type), end)
		default:
			var values []string
			for _, f := range v.Values {
				values = append(values, g.tsType(f.Type))
			}
			g.p("  | { %s: [%s] }%s", v.Name, strings.Join(values, ", "), end)
		}
	}
}

// lowerCamel is a queue's name as a value is named: billing as billing,
// card_payments as cardPayments.
func lowerCamel(name string) string {
	p := pascal(name)
	return strings.ToLower(p[:1]) + p[1:]
}

func (g *tsGen) queue(queue string, steps []*flowd.StepInfo, shapes map[string]string) {
	iface := pascal(queue)
	g.p("")
	g.jsdoc("", []string{
		"Carries out the steps of the queue " + queue + ". A step answers what it",
		"returns; an exception is a failure of its body, but for Retry, Later",
		"and Unknown of @qoqi/flow.",
	})
	g.p("export interface %s {", iface)
	for i, step := range steps {
		if i > 0 {
			g.p("")
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
		g.jsdoc("  ", lines)
		var params []string
		for _, p := range step.Params {
			params = append(params, p.Name+": "+g.tsType(p.Type))
		}
		params = append(params, "task: Task")
		returns := g.tsType(done(step.Returns))
		switch {
		case step.Returns.Kind == "void":
			returns = "void"
		case undo != nil && isResult(step.Returns):
			returns = "Undone<" + g.tsType(step.Returns.Args[0]) + ", " + g.tsType(undo) + "> | { Err: " + g.tsType(step.Returns.Args[1]) + " }"
		}
		g.p("  %s(%s): Awaitable<%s>;", stepName(step), strings.Join(params, ", "), returns)
	}
	g.p("}")

	name := lowerCamel(queue)
	g.p("")
	g.p("/** The shapes of the steps of %s: which handler answers which step. */", queue)
	g.p("export const %sShapes = {", name)
	for _, step := range steps {
		g.p("  %s: %q,", stepName(step), shapes[stepName(step)])
	}
	g.p("} as const;")
	g.p("")
	g.p("/** Makes the handler of the queue %s, at the address qoqi.json gives it. */", queue)
	g.p("export function %sQueue(steps: %s, ...secrets: string[]): Queue {", name, iface)
	g.p("  const queue = new Queue(%q, ...secrets);", queue)
	for _, step := range steps {
		g.p("")
		g.p("  queue.handle(%sShapes.%s, async (task) => {", name, stepName(step))
		var args []string
		for _, p := range step.Params {
			args = append(args, "task.args."+p.Name+" as "+g.tsType(p.Type))
		}
		args = append(args, "task")
		call := "await steps." + stepName(step) + "(" + strings.Join(args, ", ") + ")"
		undo := undoParam(g.s, step)
		switch {
		case step.Returns.Kind == "void":
			g.p("    %s;", call)
			g.p("    return answer.value(null);")
		case undo != nil && isResult(step.Returns):
			g.p("    const result = %s;", call)
			g.p("    if (result instanceof Undone) return answer.withUndo({ Ok: result.value }, result.undo);")
			g.p("    return answer.value(result);")
		case isSettled(step.Returns):
			g.p("    return answer.value({ Done: %s });", call)
		default:
			g.p("    return answer.value(%s);", call)
		}
		g.p("  });")
	}
	g.p("")
	g.p("  return queue;")
	g.p("}")
}
