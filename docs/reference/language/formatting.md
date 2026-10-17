# Formatting: `qoqi fmt`

`qoqi fmt` writes the files of a program the one way, so that nobody has to decide how a brace is placed, and no change to a file is a change of spacing.

```bash
qoqi fmt                  # every .qi file under the current folder
qoqi fmt main.qi src     # these files, and those under these folders
qoqi fmt --check          # changes nothing: names the files that would change
qoqi fmt -                # reads a program from the input, writes it formatted
```

It prints the name of each file it changed. With `--check` it prints the files that would change and ends with status 1 if there are any, which is what a script that guards a repository wants. Folders whose name starts with a dot are not gone into, unless they are named.

In an editor it is **Format Document**, and formatting on save for who turns that on: see [editor.md](editor.md).

## What it decides

The aim is code with room in it: one thing on a line, and space around what is long.

```rust
fn load(text:String)->Log{let log=Log{hits:[],bad:[]};
    for i,line in text.lines(){if line.isBlank(){continue;}
        match lines.read(i+1,line){case Ok(hit):{log.hits.push(hit);}case Err(bad):{log.bad.push(bad);}}}
    return log;}
```

becomes

```rust
fn load(text: String) -> Log {
    let log = Log { hits: [], bad: [] };

    for i, line in text.lines() {
        if line.isBlank() {
            continue;
        }

        match lines.read(i + 1, line) {
            case Ok(hit): {
                log.hits.push(hit);
            }

            case Err(bad): {
                log.bad.push(bad);
            }
        }
    }

    return log;
}
```

| | |
|---|---|
| Indentation | Four spaces a level; no tabs |
| Lines | A hundred characters at most, where that can be helped: a text is never cut |
| Blocks | Never on the line of their brace: `if x { return; }` takes three lines. An empty one stays closed: `{}` |
| Empty lines | A statement that takes several lines stands apart from its neighbours, and so does a case; the ones that take one line stay together. Declarations stand apart, except imports, constants and `extern fn`, which follow each other. The empty lines you leave are kept, one at a time, and none at the start or the end of a block |
| Lists | Arguments, parameters, items, fields: on one line if they fit, else one to a line with a comma after the last |
| Arrays of plain values | Numbers, texts and names fill their lines instead: a table of forty numbers is not forty lines |
| A last argument with a body | A function, a struct literal, an array or a `match` starts on the line of the call and ends with it: `items.each(fn(item: Item) {` |
| Struct literals | On one line if they fit: `Point { x: 1, y: 2 }`. One field to a line where you started it so, by a line end after the brace |
| Enums | The same: `enum Light { Red, Amber, Green }`, or a variant to a line where you started it so |
| Calls that follow each other | `a.b().c().d()` on one line, or broken before each dot, all or none. What a chain starts from stays together: `json.object()` |
| Operators | Spaces around them. What does not fit breaks before the operators of one level, each starting a line. In the condition of an `if` that takes several lines, the brace gets a line of its own |
| `match` | A case to a line. Where it gives a value, each case ends with a semicolon |
| Comments | Stay with what they are about: above the line that follows them, or at the end of their line, two spaces after the code. The comments of lines that follow each other start in one column |
| Texts between `"""` | Move with the line they start on, when that leaves their value as it is; they stay where they are otherwise |

What is inside a text is never touched, the expressions of `${...}` included.

## What it does not do

It moves; it does not rename, reorder, add or remove. The imports stay in your order, a parenthesis you wrote stays, a long line of text stays long. Besides spaces and line ends, the only characters it writes its own way are separators: the comma after the last item of a list, there when the list takes several lines and gone when it takes one; the semicolon that ends each field of a struct and each case of a `match` that gives a value, where a comma is allowed too; the comma between the variants of an enum.

There is nothing to configure.

## What it needs

A program that reads as one: brackets that close, statements that end. Names and types are not its business, so a program that does not compile yet is formatted all the same.

```text
$ qoqi fmt main.qi
main.qi:12:5: expected ';', found 'let'
```

A file it cannot read is left exactly as it is.

Before it gives a text back, the formatter reads its own result and compares it with what it was given: the same tokens in the same order, the same comments. If they differ, which would be a mistake of the formatter, it says so and changes nothing. A file is never written with a program in it that is not the one you wrote.

## How it is built

Like the [language server](editor.md), the formatter is written in Qoqi and kept inside the `qoqi` binary. It is two modules of the standard library and a small program:

*   [`std.layout`](stdlib.md#stdlayout) lays text out to fit a width. What is to be written is described once, as pieces of text and the places where a line may end; writing it out decides which of those places are used. It knows nothing of Qoqi, and serves for printing any tree.
*   [`std.formatter`](stdlib.md#stdformatter) reads a program by its shape, tokens and brackets, and describes it to `std.layout`. `formatter.format(source)` gives the formatted text or the place where the reading stopped.
*   [`internal/formatter/fmt.qi`](../../../internal/formatter/fmt.qi) is `qoqi fmt` itself: it finds the files and reports.

The three are formatted by themselves, and a test fails when they are not.
