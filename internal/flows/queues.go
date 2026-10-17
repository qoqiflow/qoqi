package flows

import (
	"fmt"
	"strings"

	"github.com/qoqiflow/qoqi/internal/ast"
)

const (
	// ErrSettledWithin is a step that returns a Settled and does not say
	// how long it may take: a minute by default never
	// decides a transfer.
	ErrSettledWithin = "F0009"

	// ErrUnknownQueue is a step by a queue the project does not declare.
	ErrUnknownQueue = "F0012"

	settled = "std.flow.Settled"
)

// checkQueues checks the steps: a Settled one says how long it may take,
// and one the application carries out is by a queue qoqi.json declares.
func (c *checker) checkQueues(prog *ast.Program, queues []string) {
	declared := map[string]bool{}
	for _, name := range queues {
		declared[name] = true
	}
	for _, decl := range prog.Decls {
		step, ok := decl.(*ast.FnDecl)
		if !ok || !step.Step || isLibrary(step) {
			continue
		}
		name := lastPart(step.Name)
		if step.Within == nil && step.ReturnType != nil && step.ReturnType.Name == settled {
			c.stageError(step.Token(), ErrSettledWithin, fmt.Sprintf("%s returns a Settled, and does not say how long it may take", name),
				"Say it after its signature: -> "+step.ReturnType.String()+" within 2.hours(); a minute by default never decides what is settled")
		}
		if step.Queue == "" {
			continue
		}
		if !declared[step.Queue] {
			suggestion := `Declare it in the project's qoqi.json: "queues": {"` + step.Queue + `": {"url": "env:` + strings.ToUpper(step.Queue) + `_URL", "secret": "env:` + strings.ToUpper(step.Queue) + `_SECRET"}}`
			if len(queues) > 0 {
				suggestion = "The project declares " + strings.Join(queues, ", ") + "; or declare " + step.Queue + " in its qoqi.json"
			}
			c.stageError(step.QueueTok, ErrUnknownQueue, fmt.Sprintf("%s is carried out by the queue %s, which the project does not declare", name, step.Queue), suggestion)
		}
	}
}
