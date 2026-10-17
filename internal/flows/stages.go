package flows

import (
	"fmt"
	"strings"

	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/token"
)

const (
	// ErrStageInDefer is a stage inside the block of a defer: what is
	// left to run there is code, which a new version could not take up.
	ErrStageInDefer = "F0002"

	// ErrStagePlace is a stage written elsewhere than in the run of a flow
	// itself, or under a name another stage of it has.
	ErrStagePlace = "F0005"

	// WarnLoopWithoutStage is a loop of run that waits, with no stage in
	// it or around it: the instance keeps all it did since it started,
	// and never moves to a new version.
	WarnLoopWithoutStage = "W0011"
)

// checkStages says where a stage may be: in the run of a flow, as a
// statement of it, not in a function written inside it nor in a defer,
// each under a name of its own. And it suggests one in a loop that waits
// without any.
func (c *checker) checkStages(prog *ast.Program, roots []*fn) {
	allowed := map[*ast.ExprStmt]bool{}
	for _, root := range roots {
		s := &stageWalk{c: c, run: root, names: map[string]token.Token{}, allowed: allowed}
		if root.decl.Body != nil {
			s.block(root.decl.Body, false, 0)
		}
	}

	// Every other stage of the program is in a wrong place
	for _, decl := range prog.Decls {
		var bodies []*ast.BlockStmt
		switch d := decl.(type) {
		case *ast.FnDecl:
			bodies = append(bodies, d.Body)
		case *ast.ImplDecl:
			for _, method := range d.Methods {
				bodies = append(bodies, method.Body)
			}
		}
		for _, body := range bodies {
			if body == nil {
				continue
			}
			ast.Walk(body, func(ast.Expr) {}, func(stmt ast.Stmt) {
				if e, ok := stmt.(*ast.ExprStmt); ok && e.Stage != "" && !allowed[e] {
					c.stageError(e.Token(), ErrStagePlace,
						fmt.Sprintf("stage %s is written in the run of a flow itself", e.Stage),
						"A stage is where an instance may be taken up from its variables alone: in run, not in a function it calls or one written inside it")
				}
			})
		}
	}
}

type stageWalk struct {
	c       *checker
	run     *fn
	names   map[string]token.Token
	allowed map[*ast.ExprStmt]bool
}

// block walks the statements of run. inDefer says that they are in the
// block of a defer; loops, how many loops with a stage they are in.
func (s *stageWalk) block(b *ast.BlockStmt, inDefer bool, loops int) {
	if b == nil {
		return
	}
	for _, stmt := range b.Stmts {
		s.stmt(stmt, inDefer, loops)
	}
}

func (s *stageWalk) stmt(stmt ast.Stmt, inDefer bool, loops int) {
	switch st := stmt.(type) {
	case *ast.ExprStmt:
		if st.Stage == "" {
			return
		}
		s.allowed[st] = true
		switch {
		case inDefer:
			s.c.stageError(st.Token(), ErrStageInDefer,
				fmt.Sprintf("stage %s is inside a defer", st.Stage),
				"What a flow must undo days later is a flow.undo; a defer still to run is code a new version could not take up")
		case st.Stage == "start":
			s.c.stageError(st.Token(), ErrStagePlace, "stage start is the beginning of run, which has that name already", "Give the stage another name")
		case st.Stage == "fields":
			s.c.stageError(st.Token(), ErrStagePlace, "stage fields would be told apart from the fields of the flow by nothing: migrate fn fields is theirs", "Give the stage another name")
		default:
			if before, taken := s.names[st.Stage]; taken {
				s.c.stageError(st.Token(), ErrStagePlace,
					fmt.Sprintf("stage %s is written twice in %s, first on line %d", st.Stage, name(s.run), before.Pos.Line),
					"A stage is known by its name, from one version to the next: give each its own")
			} else {
				s.names[st.Stage] = st.Token()
			}
		}
	case *ast.BlockStmt:
		s.block(st, inDefer, loops)
	case *ast.IfStmt:
		s.block(st.Then, inDefer, loops)
		if st.Else != nil {
			s.stmt(st.Else, inDefer, loops)
		}
	case *ast.MatchStmt:
		for _, c := range st.Cases {
			if body, ok := c.Body.(*ast.BlockStmt); ok {
				s.block(body, inDefer, loops)
			}
		}
	case *ast.DeferStmt:
		s.block(st.Body, true, loops)
	case *ast.WhileStmt:
		s.loop(st.Token(), st.Body, inDefer, loops)
	case *ast.ForStmt:
		s.loop(st.Token(), st.Body, inDefer, loops)
	}
}

// loop walks a loop's body, and warns of one that waits with no stage in
// it nor in a loop around it.
func (s *stageWalk) loop(at token.Token, body *ast.BlockStmt, inDefer bool, loops int) {
	staged := hasStage(body)
	if staged {
		loops++
	} else if loops == 0 && !inDefer && s.waits(body) {
		if at.Pos.Line == 0 {
			// A loop the desugarer made: where its body starts
			at = firstToken(body)
		}
		d := errors.NewDiagnosticFromToken(errors.SeverityWarning, WarnLoopWithoutStage,
			fmt.Sprintf("a loop of %s waits with no stage in it: the instance keeps everything it did since it started, and never moves to a new version", name(s.run)),
			at, s.c.linesOf(at))
		d.Function = name(s.run)
		d.Suggestion = "Write `stage <name>;` at the top of the loop's body, named after what the flow is doing there"
		s.c.errs.Add(d)
		// Said of this loop: not again of the loops inside it
		loops++
	}
	s.block(body, inDefer, loops)
}

// firstToken is where the first statement of a block that says where it
// is was written.
func firstToken(b *ast.BlockStmt) token.Token {
	var first token.Token
	ast.Walk(b, func(expr ast.Expr) {
		if first.Pos.Line == 0 && expr.Token().Pos.Line > 0 {
			first = expr.Token()
		}
	}, func(stmt ast.Stmt) {
		if first.Pos.Line == 0 && stmt.Token().Pos.Line > 0 {
			first = stmt.Token()
		}
	})
	return first
}

// hasStage says whether a stage is written in a block, as run's own.
func hasStage(b *ast.BlockStmt) bool {
	found := false
	ast.Walk(b, func(ast.Expr) {}, func(stmt ast.Stmt) {
		if e, ok := stmt.(*ast.ExprStmt); ok && e.Stage != "" {
			found = true
		}
	})
	return found
}

// waits says whether a block waits or calls a step: what a flow is
// written down for.
func (s *stageWalk) waits(b *ast.BlockStmt) bool {
	found := false
	ast.Walk(b, func(expr ast.Expr) {
		call, ok := expr.(*ast.CallExpr)
		if !ok {
			return
		}
		switch callee := call.Func.(type) {
		case *ast.MemberExpr:
			if module, ok := callee.Expr.(*ast.Ident); ok && module.Name == ast.TaskModule {
				found = true
			}
		case *ast.Ident:
			// wait and wait match, as the modules and the copies for each
			// type wrote them: std.task.waitFor_Void
			if strings.HasPrefix(callee.Name, "std.task.waitFor") || strings.HasPrefix(callee.Name, "std.task.first") {
				found = true
			}
			if target := s.c.function(callee); target != nil && target.decl.Step {
				found = true
			}
		}
	}, func(ast.Stmt) {})
	return found
}

func (c *checker) stageError(at token.Token, code, message, suggestion string) {
	d := errors.NewDiagnosticFromToken(errors.SeverityError, code, message, at, c.linesOf(at))
	d.Suggestion = suggestion
	c.errs.Add(d)
}
