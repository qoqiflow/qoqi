package flows

import (
	"strings"

	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/flowlock"
	"github.com/qoqiflow/qoqi/internal/token"
)

// WarnUnmigrated is a stage this version changed of the one its lock
// says was deployed last, which no migration answers: the daemon refuses
// the version while an instance is at the stage. Said where it is
// written, before anything is deployed.
const WarnUnmigrated = "F0011"

// checkLock compares the stages of the program's flows with those of
// the version deployed last, as its lock keeps them, and says each
// change no migrate fn answers, at the stage, or at the migration that
// does not fit it.
func (c *checker) checkLock(prog *ast.Program, roots []*fn, lock *flowlock.Lock) {
	if lock == nil {
		return
	}
	newer := flowlock.Shapes{}
	places := map[string]map[string]token.Token{} // flow, stage
	runs := map[string]token.Token{}
	served := map[string]bool{}
	for _, flow := range FlowsOf(prog) {
		served[flow] = true
	}
	for _, root := range roots {
		if !served[root.owner] {
			continue // a flow of a program imported is that program's
		}
		shapes, at := map[string]map[string]string{}, map[string]token.Token{}
		if root.decl.Body != nil {
			ast.Walk(root.decl.Body, func(ast.Expr) {}, func(stmt ast.Stmt) {
				if e, ok := stmt.(*ast.ExprStmt); ok && e.Stage != "" {
					shapes[e.Stage] = c.info.StageTypes[e]
					at[e.Stage] = e.Token()
				}
			})
		}
		newer[root.owner], places[root.owner], runs[root.owner] = shapes, at, root.decl.Token()
	}
	// A flow this version has not is no change of a stage: it is a flow
	// taken away, said by the daemon
	old := flowlock.Shapes{}
	for flow, stages := range lock.Flows {
		if _, ok := newer[flow]; ok {
			old[flow] = stages
		}
	}

	migrations := MigrationsOf(prog)
	written := map[string]token.Token{}
	for _, decl := range prog.Decls {
		if impl, ok := decl.(*ast.ImplDecl); ok {
			for _, method := range impl.Methods {
				if method.Migrate {
					written[impl.StructName+"."+method.Name] = method.Token()
				}
			}
		}
	}

	changes := flowlock.Changes(old, newer)
	for _, left := range flowlock.Unanswered(changes, old, newer, migrations) {
		at, ok := written[left.Flow+"."+left.Stage]
		if !ok || !strings.HasPrefix(left.What, "migrate fn") {
			if at, ok = places[left.Flow][left.Stage]; !ok {
				at = runs[left.Flow]
			}
		}
		suggestion := "An instance at the stage needs a migrate fn " + left.Stage + " to move to this version: the daemon refuses it while one is there"
		if strings.HasPrefix(left.What, "migrate fn") {
			suggestion = "A migration takes variables the stage had in the version deployed, of their types: see " + lockName(lock)
		}
		d := errors.NewDiagnosticFromToken(errors.SeverityWarning, WarnUnmigrated,
			"since the version deployed, "+left.String(), at, c.linesOf(at))
		d.Suggestion = suggestion
		c.errs.Add(d)
	}
}

func lockName(lock *flowlock.Lock) string {
	return "the version " + strings.TrimPrefix(lock.Version, "sha256:")[:min(8, len(strings.TrimPrefix(lock.Version, "sha256:")))] + " in the flow.lock"
}

// FlowsOf are the structs of a program that implement std.flow.Flow, its
// own: those of its entry file and of the modules it imports, each by its
// name in the program (easy.Easy for a flow Easy of the module easy). A
// module with a main of its own is a program, whose flows are served when
// it is deployed: one that imports it, to try its flows, serves none of
// them. The standard library's are none of the program's.
func FlowsOf(prog *ast.Program) []string {
	mains := map[string]bool{} // the modules that have a main
	for _, decl := range prog.Decls {
		if fn, ok := decl.(*ast.FnDecl); ok && strings.HasSuffix(fn.Name, ".main") {
			mains[strings.TrimSuffix(fn.Name, ".main")] = true
		}
	}
	var flows []string
	for _, decl := range prog.Decls {
		impl, ok := decl.(*ast.ImplDecl)
		if !ok || impl.TraitName != FlowTrait {
			continue
		}
		if dot := strings.LastIndex(impl.StructName, "."); dot >= 0 {
			module := impl.StructName[:dot]
			if mains[module] || module == "std" || strings.HasPrefix(module, "std.") {
				continue
			}
		}
		flows = append(flows, impl.StructName)
	}
	return flows
}

// FlowName is the name a flow is written with in its own module, and
// started by: Easy for easy.Easy.
func FlowName(flow string) string {
	return flow[strings.LastIndex(flow, ".")+1:]
}

// MigrationsOf are the migrate fns of a program, with the types of their
// parameters as the source writes them.
func MigrationsOf(prog *ast.Program) []flowlock.Migration {
	var migrations []flowlock.Migration
	for _, decl := range prog.Decls {
		impl, ok := decl.(*ast.ImplDecl)
		if !ok {
			continue
		}
		for _, method := range impl.Methods {
			if !method.Migrate {
				continue
			}
			m := flowlock.Migration{Flow: impl.StructName, Stage: method.Name, Params: map[string]string{}}
			for _, param := range method.Params {
				if param.Type != nil {
					m.Params[param.Name] = param.Type.String()
				}
			}
			migrations = append(migrations, m)
		}
	}
	return migrations
}
