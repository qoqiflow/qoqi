package stdlib_test

import "testing"

// std.layout: documents written out to fit a width.

const layoutImports = `import std.layout;
import std.layout.{Doc};
import std.arrays;
import std.strings;
`

// helpers are the documents the tests build: a list between brackets, as
// the formatter writes one.
const layoutHelpers = `
		let words = fn(texts: [String]) -> [Doc] { return texts.map(word -> layout.text(word)); };
		let list = fn(items: [Doc]) -> Doc {
			return layout.group(layout.concat([
				layout.text("["),
				layout.indent(layout.concat([Doc.Soft, layout.join(items, layout.concat([layout.text(","), Doc.Line]))])),
				Doc.IfBroken(layout.text(","), Doc.Nothing),
				Doc.Soft,
				layout.text("]"),
			]));
		};
`

// A group is on one line when it fits and takes every line end directly
// in it when it does not: the width decides, to the character.
func TestLayoutGroups(t *testing.T) {
	expect(t, layoutImports, layoutHelpers+`
		let short = list(words(["one", "two", "three"]));
		print(layout.render(short, 80, 4));
		print(layout.render(short, 17, 4));
		print(layout.render(short, 16, 4));
		print(layout.render(short, 16, 2));

		// An empty document is no text at all; a text alone ends its line
		println("[${layout.render(Doc.Nothing, 80, 4)}]", "[${layout.render(layout.concat([]), 80, 4)}]");
		print(layout.render(layout.text("alone"), 80, 4));

		// A text longer than the width is written all the same
		print(layout.render(list(words(["a-word-that-is-longer-than-the-line"])), 10, 4));
	`,
		"[one, two, three]",
		"[one, two, three]",
		"[",
		"    one,",
		"    two,",
		"    three,",
		"]",
		"[",
		"  one,",
		"  two,",
		"  three,",
		"]",
		"[] []",
		"alone",
		"[",
		"    a-word-that-is-longer-than-the-line,",
		"]",
	)
}

// Groups inside groups decide for themselves: the outer one breaks and
// the inner ones stay whole while they fit; what follows a group on its
// line counts in whether it fits.
func TestLayoutNesting(t *testing.T) {
	expect(t, layoutImports, layoutHelpers+`
		let inner = list(words(["1", "2", "3"]));
		let outer = list([inner, inner, list(words(["a-longer-item", "another-longer-item", "the-last-of-them"]))]);
		print(layout.render(outer, 80, 4));
		print(layout.render(outer, 60, 4));
		print(layout.render(outer, 30, 4));

		// The semicolon after the list has to fit with it
		let statement = layout.concat([layout.text("let xs = "), list(words(["one", "two"])), layout.text(";")]);
		print(layout.render(statement, 20, 4));
		print(layout.render(statement, 19, 4));

		// A line end that is always taken breaks every group around it,
		// and none beside it
		let forced = list([layout.concat([layout.text("first"), Doc.Hard, layout.text("second")]), inner]);
		print(layout.render(forced, 80, 4));
		println(layout.forces(forced), layout.forces(inner), layout.forces(layout.broken(inner)));
	`,
		"[[1, 2, 3], [1, 2, 3], [a-longer-item, another-longer-item, the-last-of-them]]",
		"[",
		"    [1, 2, 3],",
		"    [1, 2, 3],",
		"    [a-longer-item, another-longer-item, the-last-of-them],",
		"]",
		"[",
		"    [1, 2, 3],",
		"    [1, 2, 3],",
		"    [",
		"        a-longer-item,",
		"        another-longer-item,",
		"        the-last-of-them,",
		"    ],",
		"]",
		"let xs = [one, two];",
		"let xs = [",
		"    one,",
		"    two,",
		"];",
		"[",
		"    first",
		"    second,",
		"    [1, 2, 3],",
		"]",
		"true false true",
	)
}

// A list that fills its lines puts as many items on each as fit.
func TestLayoutFill(t *testing.T) {
	expect(t, layoutImports, `
		let numbers = arrays.range(1, 31).map(n -> layout.text(toString(n * 111)));
		let table = layout.group(layout.concat([
			layout.text("["),
			layout.indent(layout.concat([Doc.Soft, layout.fill(numbers)])),
			Doc.Soft,
			layout.text("]"),
		]));
		let text = layout.render(table, 40, 4);
		print(text);
		println(text.lines().all(line -> line.len() <= 40));
		print(layout.render(table, 200, 4));
	`,
		"[",
		"    111, 222, 333, 444, 555, 666, 777,",
		"    888, 999, 1110, 1221, 1332, 1443,",
		"    1554, 1665, 1776, 1887, 1998, 2109,",
		"    2220, 2331, 2442, 2553, 2664, 2775,",
		"    2886, 2997, 3108, 3219, 3330",
		"]",
		"true",
		"[111, 222, 333, 444, 555, 666, 777, 888, 999, 1110, 1221, 1332, 1443, 1554, 1665, 1776, 1887, 1998, 2109, 2220, 2331, 2442, 2553, 2664, 2775, 2886, 2997, 3108, 3219, 3330]",
	)
}

// Comments wait for the end of their line, and those of lines that
// follow each other start in one column. Empty lines never come in twos,
// nor at the top or the bottom.
func TestLayoutCommentsAndEmptyLines(t *testing.T) {
	expect(t, layoutImports, `
		let line = fn(code: String, comment: String) -> Doc {
			if comment == "" { return layout.text(code); }
			return layout.concat([layout.text(code), Doc.Trailing(comment)]);
		};
		let fields = layout.concat([
			Doc.Hard, Doc.Hard,
			line("id: Int;", "// its number"),
			Doc.Hard,
			line("name: String;", ""),
			Doc.Hard,
			line("weight: Float = 1.0;", "// in kilos"),
			Doc.Hard, Doc.Hard, Doc.Hard, Doc.Hard,
			line("note: String?;", "// alone: its own column"),
			Doc.Hard,
			layout.indent(layout.concat([Doc.Hard, line("deeper: Bool;", "// another depth, another column")])),
			Doc.Hard, Doc.Hard,
		]);
		print(layout.render(fields, 80, 4));

		// A comment that runs to the end of its line keeps its group from
		// being on one line: what follows it would come before it. One
		// that has an end of its own does not
		let call = fn(comment: String) -> Doc {
			return layout.group(layout.concat([layout.text("call(first,"), Doc.Trailing(comment), Doc.Line, layout.text("second);")]));
		};
		print(layout.render(call("// why"), 80, 4));
		print(layout.render(call("/* why */"), 80, 4));
		print(layout.render(layout.concat([Doc.Trailing("// one"), Doc.Trailing("/* two */"), layout.text("code();")]), 80, 4));

		// Fresh starts a line where one is under way, and only there
		print(layout.render(layout.concat([Doc.Fresh, layout.text("first"), Doc.Fresh, Doc.Fresh, layout.text("second"), Doc.Trailing("// said"), Doc.Fresh, layout.text("third"), Doc.Hard, Doc.Fresh, layout.text("fourth")]), 80, 4));
	`,
		"id: Int;              // its number",
		"name: String;",
		"weight: Float = 1.0;  // in kilos",
		"",
		"note: String?;  // alone: its own column",
		"",
		"    deeper: Bool;  // another depth, another column",
		"call(first,  // why",
		"second);",
		"call(first, second);  /* why */",
		"code();  // one /* two */",
		"first",
		"second  // said",
		"third",
		"fourth",
	)
}

// Items written under each other stand apart when they take several
// lines, and stay together when they do not.
func TestLayoutItems(t *testing.T) {
	expect(t, layoutImports, `
		let block = fn(head: String, body: [String]) -> Doc {
			let inside = body.map(line -> layout.concat([Doc.Hard, layout.text(line)]));
			return layout.concat([layout.text(head + " {"), layout.indent(layout.concat(inside)), Doc.Hard, layout.text("}")]);
		};
		let rows = fn(items: [Doc]) -> Doc {
			let parts: [Doc] = [];
			for i, item in items {
				if i > 0 { parts.push(Doc.Gap); }
				parts.push(Doc.Item(item, i > 0));
			}
			return layout.concat(parts);
		};
		print(layout.render(rows([
			layout.text("one();"),
			layout.text("two();"),
			block("if ready", ["go();"]),
			block("while busy", ["wait();"]),
			layout.text("three();"),
			layout.text("four();"),
			// Short enough for one line, so not apart; too long for it, so apart
			layout.group(layout.concat([layout.text("call("), layout.indent(layout.concat([Doc.Soft, layout.text("argument")])), Doc.Soft, layout.text(");")])),
			layout.group(layout.concat([layout.text("call("), layout.indent(layout.concat([Doc.Soft, layout.text("an-argument-that-is-too-long")])), Doc.Soft, layout.text(");")])),
			layout.text("five();"),
		]), 30, 4));

		// Inside one another: the inner items are apart from each other,
		// and never from the brace that opens or closes around them
		print(layout.render(rows([
			block("fn outer()", []),
			layout.concat([layout.text("fn main() {"), layout.indent(layout.concat([Doc.Hard, rows([
				layout.text("a();"), block("if x", ["b();"]), layout.text("c();"),
			])])), Doc.Hard, layout.text("}")]),
		]), 80, 4));
	`,
		"one();",
		"two();",
		"",
		"if ready {",
		"    go();",
		"}",
		"",
		"while busy {",
		"    wait();",
		"}",
		"",
		"three();",
		"four();",
		"call(argument);",
		"",
		"call(",
		"    an-argument-that-is-too-long",
		");",
		"",
		"five();",
		"fn outer() {",
		"}",
		"",
		"fn main() {",
		"    a();",
		"",
		"    if x {",
		"        b();",
		"    }",
		"",
		"    c();",
		"}",
	)
}

// Text over several lines is written as it is: its lines move with the
// first one when that changes nothing in what it says.
func TestLayoutVerbatim(t *testing.T) {
	expect(t, layoutImports, `
		let comment = Doc.Verbatim("/* one\n   two */", 0);
		print(layout.render(layout.concat([layout.text("{"), layout.indent(layout.concat([Doc.Hard, comment])), Doc.Hard, layout.text("}")]), 80, 4));

		// Moved back to where it has room; left alone where it has not
		print(layout.render(Doc.Verbatim("/* one\n          two\n        three */", 8), 80, 4));
		print(layout.render(Doc.Verbatim("/* one\n  two */", 8), 80, 4));

		// What follows on its last line counts from there
		let wide = layout.group(layout.concat([Doc.Verbatim("/* one\n   two */", 0), Doc.Line, layout.text("after")]));
		print(layout.render(wide, 80, 4));
	`,
		"{",
		"    /* one",
		"       two */",
		"}",
		"/* one",
		"  two",
		"three */",
		"/* one",
		"  two */",
		"/* one",
		"   two */ after",
	)
}
