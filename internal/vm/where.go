package vm

import (
	"encoding/json"
	"fmt"
)

// Where a frozen program is: for each of its tasks, what it waits for and
// the calls it is in, read from what Freeze wrote and the debug
// information of its chunk. It is what the pages of the daemon show of an
// instance: the line it waits on, read as code.
//
//	task 0    waits for the clock, until 1792178323000
//	          Expense.run      approvals.qi:61
//	          main             approvals.qi:12

// Place is where one task of a frozen program is.
type Place struct {
	Task    string       `json:"task"`              // its name: "0", "0/task#2"
	Waiting string       `json:"waiting,omitempty"` // the native it waits in, or "step"
	Until   int64        `json:"until,omitempty"`   // when the clock wakes it, in milliseconds since 1970
	Ready   bool         `json:"ready,omitempty"`   // woken, waiting for its turn
	Started bool         `json:"started"`           // false: made, not run yet
	Failed  string       `json:"failed,omitempty"`  // ended with a failure nobody took
	Frames  []PlaceFrame `json:"frames,omitempty"`  // innermost first
}

// PlaceFrame is one call a task is in.
type PlaceFrame struct {
	Function string `json:"function"`
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
	Column   int    `json:"column,omitempty"`
}

// Where reads where the tasks of a frozen program are.
func Where(chunk *Chunk, state []byte) ([]Place, error) {
	var program frozenProgram
	if err := json.Unmarshal(state, &program); err != nil {
		return nil, fmt.Errorf("not a frozen program: %v", err)
	}
	function := func(ref int) string {
		if ref < 0 || ref >= len(program.Heap) {
			return ""
		}
		return program.Heap[ref].Proto
	}
	var places []Place
	for _, t := range program.Tasks {
		place := Place{Task: t.Name, Waiting: t.Waiting, Until: t.Until, Ready: t.Ready > 0, Started: t.Run == nil}
		if place.Task == "" {
			place.Task = fmt.Sprint(t.ID)
		}
		if t.Failure != nil {
			place.Failed = t.Failure.Message
		}
		if t.Run == nil && t.Failure == nil {
			// The instruction being run, then the calls that led to it,
			// outwards. Each ip is that of the call itself, as the machine
			// keeps it while the call is under way
			place.Frames = append(place.Frames, frameOf(chunk, function(t.Function), t.IP))
			for i := len(t.Frames) - 1; i >= 0; i-- {
				place.Frames = append(place.Frames, frameOf(chunk, function(t.Frames[i].Function), t.Frames[i].IP))
			}
		}
		places = append(places, place)
	}
	return places, nil
}

// frameOf is the line of the instruction at ip, in the function named.
func frameOf(chunk *Chunk, function string, ip int) PlaceFrame {
	frame := PlaceFrame{Function: function}
	start, end := 0, len(chunk.Code)
	if i := chunk.FindPrototype(function); i >= 0 {
		start, end = chunk.Prototypes[i].CodeStart, chunk.Prototypes[i].CodeStart+chunk.Prototypes[i].CodeSize
	}
	// The nearest instruction from ip back that says where it was
	// written, within the function
	for at := ip; at >= start && at < end; at-- {
		if info := chunk.GetDebugInfo(at); info != nil && info.Line > 0 {
			frame.File, frame.Line, frame.Column = info.FilePath, info.Line, info.Column
			break
		}
	}
	return frame
}

// Files are the source files a chunk was compiled from, as its debug
// information names them.
func (c *Chunk) Files() []string {
	seen := map[string]bool{}
	var files []string
	for _, info := range c.DebugInfo {
		if info != nil && info.FilePath != "" && !seen[info.FilePath] {
			seen[info.FilePath] = true
			files = append(files, info.FilePath)
		}
	}
	return files
}
