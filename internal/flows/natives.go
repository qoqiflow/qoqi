package flows

import (
	"strings"

	"github.com/qoqiflow/qoqi/internal/ast"
)

// What each native function of the library does, as far as a flow is
// concerned. The list is by function, not by module: std.time.now
// computes, as far as a flow goes (it is given back when a flow is taken
// up), while every other native of std.io but printError reaches files.

// computing are the modules whose natives only compute, or only wait:
// std.task is how tasks take turns.
var computing = []string{
	"std.arrays.", "std.csv.", "std.dict.", "std.encoding.", "std.format.",
	"std.hash.", "std.json.", "std.math.", "std.regex.", "std.result.",
	"std.strings.", "std.url.", "std.task.", "std.formatter.", "std.layout.", "std.flow.",
}

// computingNatives are the natives of other modules that a flow may call:
// the clock and random numbers, which a flow is given back when it is
// taken up, and the program's own output, which costs nothing to write
// twice and is how a flow says what it does.
var computingNatives = map[string]bool{
	"std.time.now":       true,
	"std.time.ticks":     true,
	"std.time.fields":    true,
	"std.time.moment":    true,
	"std.time.read":      true,
	"std.random.seed":    true,
	"std.random.below":   true,
	"std.random.float":   true,
	"std.random.uuid":    true,
	"std.random.token":   true,
	"std.random.between": true,
	"std.io.printError":  true,
}

// reachesWorld reports whether a native function reaches outside the
// program: a file, the network, another program, the process. A function
// the host provides is taken to.
func reachesWorld(decl *ast.FnDecl) bool {
	name := decl.Name
	if computingNatives[name] {
		return false
	}
	for _, prefix := range computing {
		if strings.HasPrefix(name, prefix) {
			return false
		}
	}
	return true
}

// worldOf says what a native reaches, for the message.
func worldOf(native string) string {
	switch {
	case strings.HasPrefix(native, "std.http."):
		return "the network"
	case strings.HasPrefix(native, "std.io."), strings.HasPrefix(native, "std.fs."), strings.HasPrefix(native, "std.path."):
		return "files"
	case strings.HasPrefix(native, "std.os."):
		return "the process and other programs"
	case strings.HasPrefix(native, "std.sql."):
		return "a database"
	case strings.HasPrefix(native, "std."):
		return "the world"
	}
	return "the world, through a function of the host or of a C library,"
}
