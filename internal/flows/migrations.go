package flows

import (
	"fmt"
	"sort"
	"strings"

	"github.com/qoqiflow/qoqi/internal/ast"
)

// ErrMigration is a migrate fn that does not fit the flow it is written
// for: written for a struct that is no flow, or giving a struct that is
// not the shape of one of its stages.
const ErrMigration = "F0007"

// checkMigrations checks each migrate fn against this version of its
// flow: it gives the struct of one of the flow's stages, `Polling` for
// `stage polling`, whose fields are the variables alive there, of the
// same types. Its parameters are the variables of a stage of the version
// before, which the daemon checks against flow.lock.
func (c *checker) checkMigrations(prog *ast.Program, roots []*fn) []*fn {
	stages := map[string]map[string]map[string]string{} // flow, stage, shape
	for _, root := range roots {
		shapes := map[string]map[string]string{}
		if root.decl.Body != nil {
			ast.Walk(root.decl.Body, func(ast.Expr) {}, func(stmt ast.Stmt) {
				if e, ok := stmt.(*ast.ExprStmt); ok && e.Stage != "" {
					shapes[e.Stage] = c.info.StageTypes[e]
				}
			})
		}
		stages[root.owner] = shapes
	}

	var migrations []*fn
	for _, decl := range prog.Decls {
		impl, ok := decl.(*ast.ImplDecl)
		if !ok {
			continue
		}
		for _, method := range impl.Methods {
			if !method.Migrate {
				continue
			}
			migrations = append(migrations, c.fns[method])
			shapes, isFlow := stages[impl.StructName]
			if !isFlow {
				c.stageError(method.Token(), ErrMigration,
					fmt.Sprintf("migrate fn %s is written for %s, which is no flow", method.Name, impl.StructName),
					"A migration is written in an impl of a struct that implements Flow")
				continue
			}
			c.checkMigration(impl.StructName, method, shapes)
		}
	}
	return migrations
}

func (c *checker) checkMigration(flow string, method *ast.FnDecl, shapes map[string]map[string]string) {
	var target string
	if method.ReturnType != nil {
		target = method.ReturnType.Name
	}
	if method.Name == "fields" {
		// The fields of the flow: it gives the flow, as this version has it
		if target != flow {
			c.stageError(method.Token(), ErrMigration,
				fmt.Sprintf("migrate fn fields gives %s, not %s: it makes the fields of the flow of what they were", describeName(target), flow),
				"Write it migrate fn fields(old fields, by name) -> "+flow)
		}
		return
	}
	stage := lowerFirst(target)
	shape, ok := shapes[stage]
	if target == "" || !ok {
		c.stageError(method.Token(), ErrMigration,
			fmt.Sprintf("migrate fn %s gives %s, which is the struct of no stage of %s", method.Name, describeName(target), flow),
			"A migration gives the struct of the stage an instance goes on from: Polling for `stage polling;`, its fields the variables alive there")
		return
	}

	fields := c.info.StructFields[target]
	var wrong []string
	for name, t := range shape {
		if name == "self" || strings.HasPrefix(name, "self.") || hidden(name) {
			continue
		}
		switch got, ok := fields[name]; {
		case !ok:
			wrong = append(wrong, fmt.Sprintf("%s has no field %s: %s", target, name, t))
		case got != t:
			wrong = append(wrong, fmt.Sprintf("%s.%s is a %s; %s is a %s at the stage", target, name, got, name, t))
		}
	}
	for name, t := range fields {
		if _, ok := shape[name]; !ok {
			wrong = append(wrong, fmt.Sprintf("%s.%s: %s is no variable of stage %s", target, name, t, stage))
		}
	}
	if len(wrong) > 0 {
		sort.Strings(wrong)
		c.stageError(method.Token(), ErrMigration,
			fmt.Sprintf("migrate fn %s gives %s, which is not the shape of stage %s: %s", method.Name, target, stage, strings.Join(wrong, "; ")),
			"Give "+target+" a field for each variable alive at the stage, of its type, and no other")
	}
}

// hidden says whether a variable of a shape is one a loop keeps for
// itself: the runtime carries it over, no migration gives it.
func hidden(name string) bool {
	return strings.HasPrefix(name, "__") || strings.HasPrefix(name, "loop.") || strings.HasPrefix(name, "match#")
}

func lowerFirst(name string) string {
	if name == "" {
		return ""
	}
	return strings.ToLower(name[:1]) + name[1:]
}

func describeName(name string) string {
	if name == "" {
		return "nothing"
	}
	return name
}
