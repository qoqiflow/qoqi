package main

import (
	. "github.com/alecthomas/chroma/v2" //nolint:revive
	"github.com/alecthomas/chroma/v2/lexers"
)

// Qoqi, coloured as the editor colours it (editors/vscode/syntaxes). The
// words that are keywords only where they are used so, as step before fn
// or stage before a name, are matched with what follows them.
var qoqi = lexers.Register(MustNewLexer(
	&Config{Name: "Qoqi", Aliases: []string{"qoqi", "qi"}, Filenames: []string{"*.qi"}},
	func() Rules {
		return Rules{
			"root": {
				{Pattern: `\s+`, Type: Text},
				{Pattern: `//[^\n]*`, Type: CommentSingle},
				{Pattern: `/\*`, Type: CommentMultiline, Mutator: Push("comment")},
				{Pattern: `"`, Type: LiteralStringDouble, Mutator: Push("string")},
				{Pattern: `(\.)([a-z_][A-Za-z0-9_]*)(?=\s*\()`, Type: ByGroups(Punctuation, NameFunction)},
				{Pattern: `(\.)([A-Za-z_][A-Za-z0-9_]*)`, Type: ByGroups(Punctuation, Name)},
				{Pattern: `\bimport\b`, Type: KeywordNamespace},
				{Pattern: `\b(fn|let|const|struct|enum|trait|impl|pub|extern|static|priv)\b`, Type: KeywordDeclaration},
				{Pattern: `\bstep(?=\s+fn\b)|\bmigrate(?=\s+fn\b)|\bstage(?=\s+[A-Za-z_][A-Za-z0-9_]*\s*;)`, Type: KeywordDeclaration},
				{Pattern: `\b(if|else|default|while|for|break|continue|return|match|case|in|defer|wait|within|by|undo|as|is)\b`, Type: Keyword},
				{Pattern: `\b(true|false|null)\b`, Type: KeywordConstant},
				{Pattern: `\b(Int|Float|String|Bool|Void|Any|Self)\b`, Type: KeywordType},
				{Pattern: `\b[A-Z][A-Za-z0-9_]*\b`, Type: NameClass},
				{Pattern: `\b[a-z_][A-Za-z0-9_]*(?=\s*\()`, Type: NameFunction},
				{Pattern: `\b\d+\.\d+\b`, Type: LiteralNumberFloat},
				{Pattern: `\b\d+\b`, Type: LiteralNumberInteger},
				{Pattern: `[A-Za-z_][A-Za-z0-9_]*`, Type: Name},
				{Pattern: `->|\?\?|\?\.|==|!=|<=|>=|&&|\|\||\|>|\+=|-=|\*=|/=|%=|\+\+|--|\.\.|[-+*/%<>=!?|&]`, Type: Operator},
				{Pattern: `[{}()\[\];,.:@]`, Type: Punctuation},
				{Pattern: `.`, Type: Text},
			},
			"string": {
				{Pattern: `\$\{`, Type: LiteralStringInterpol, Mutator: Push("interpolation")},
				{Pattern: `\\.`, Type: LiteralStringEscape},
				{Pattern: `"`, Type: LiteralStringDouble, Mutator: Pop(1)},
				{Pattern: `[^"\\$]+`, Type: LiteralStringDouble},
				{Pattern: `\$`, Type: LiteralStringDouble},
			},
			"interpolation": {
				{Pattern: `\}`, Type: LiteralStringInterpol, Mutator: Pop(1)},
				Include("root"),
			},
			"comment": {
				{Pattern: `\*/`, Type: CommentMultiline, Mutator: Pop(1)},
				{Pattern: `[^*]+`, Type: CommentMultiline},
				{Pattern: `\*`, Type: CommentMultiline},
			},
		}
	},
))
