package inspect

import (
	"testing"

	"github.com/qoqiflow/qoqi/internal/compiler"
	"github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/stdlib"
)

// A program only has checked what it uses of the standard library: what
// it does not reach is left out before the types are looked at. Here every
// module is compiled whole, as the editor does for a file of the library,
// so that a mistake in a function no test happens to call is found. (The
// test is here and not beside the compiler: the natives of std.compiler
// are the ones of this package.)
func TestTheWholeLibraryCompiles(t *testing.T) {
	for _, module := range stdlib.Modules() {
		t.Run(module, func(t *testing.T) {
			errs := errors.NewHandler()
			source := "import " + module + ";\nfn main() -> Int { return 0; }\n"
			result := compiler.CompileOptions(source, "test.qi", compiler.Options{WholeLibrary: true}, errs)
			for _, d := range errs.Diagnostics {
				t.Errorf("unexpected diagnostic:\n%s", d.String())
			}
			if result.Chunk == nil {
				t.Errorf("expected the module to compile")
			}
		})
	}
}
