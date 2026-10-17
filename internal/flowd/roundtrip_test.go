package flowd_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qoqiflow/qoqi/internal/compiler"
	lerrors "github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/flowd"
	"github.com/qoqiflow/qoqi/internal/flowrt"
)

// What the console writes, the program reads as it reads its own values:
// for every shape a value can take, a start and messages written as a
// person writes them are read by the flow, and what it reads of them is
// compared with what it reads of the same values made in Qoqi
// (testdata/shapes.qi). A value written in a form the program does not
// lay out that way reads as null or fails, and the two differ.

// The fields of sample() in shapes.qi, as a person writes them, in each
// form the daemon takes.
var shapeStarts = map[string]string{
	"by name": `{"all": {
		"n": -42, "f": 2.5, "yes": true, "s": "été \"quoted\"",
		"numbers": [3, 1, 2], "maybe": "here",
		"scores": {"b": 2, "a": 1},
		"point": {"x": 1, "y": 0.5},
		"points": [{"x": 2, "y": 1}, {"x": 3, "y": -1.5}],
		"shapes": ["Dot", {"Circle": 1.5}, {"Rect": [2, 3]}, {"Label": {"text": "hi", "size": 12}}, {"At": {"x": 7, "y": 7.5}}],
		"boxed": {"item": {"Label": {"text": "boxed", "size": 3}}, "tag": "t"},
		"ok": {"Ok": 7}, "err": {"Err": "no"},
		"wide": {"Many": [0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11]},
		"patience": "1h30m"
	}}`,
	"by place": `{"all": {
		"n": -42, "f": 2.5, "yes": true, "s": "été \"quoted\"",
		"numbers": [3, 1, 2], "maybe": "here", "nothing": null,
		"scores": {"b": 2, "a": 1},
		"point": {"x": 1, "y": 0.5},
		"points": [{"x": 2, "y": 1.0}, {"x": 3, "y": -1.5}],
		"shapes": [{"Dot": []}, {"Circle": [1.5]}, {"Rect": [2.0, 3.0]}, {"Label": ["hi", 12]}, {"At": [{"x": 7, "y": 7.5}]}],
		"boxed": {"item": {"Label": ["boxed", 3]}, "tag": "t"},
		"ok": {"Ok": [7]}, "err": {"Err": ["no"]},
		"wide": {"Many": [0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11]},
		"patience": "90 min"
	}}`,
	"as the program writes it": `{"all": {
		"$type": "All",
		"n": -42, "f": 2.5, "yes": true, "s": "été \"quoted\"",
		"numbers": [3, 1, 2], "maybe": "here", "nothing": null,
		"scores": {"b": 2, "a": 1},
		"point": {"$type": "Point", "x": 1, "y": 0.5},
		"points": [{"$type": "Point", "x": 2, "y": 1.0}, {"$type": "Point", "x": 3, "y": -1.5}],
		"shapes": [
			{"$type": "Shape.Dot", "$variant": true},
			{"$type": "Shape.Circle", "$variant": true, "0": 1.5},
			{"$type": "Shape.Rect", "$variant": true, "0": 2.0, "1": 3.0},
			{"$type": "Shape.Label", "$variant": true, "0": "hi", "1": 12},
			{"$type": "Shape.At", "$variant": true, "0": {"$type": "Point", "x": 7, "y": 7.5}}
		],
		"boxed": {"item": {"$type": "Shape.Label", "$variant": true, "0": "boxed", "1": 3}, "tag": "t"},
		"ok": {"$type": "std.result.Result.Ok", "$variant": true, "0": 7},
		"err": {"$type": "std.result.Result.Err", "$variant": true, "0": "no"},
		"wide": {"$type": "Wide.Many", "$variant": true, "0": 0, "1": 1, "2": 2, "3": 3, "4": 4, "5": 5, "6": 6, "7": 7, "8": 8, "9": 9, "10": 10, "11": 11},
		"patience": {"$type": "std.time.Duration", "ms": 5400000}
	}}`,
}

// Messages for the inbox, one of each form, and what the flow reads of them.
var shapeMessages = []struct{ message, read string }{
	{`"Dot"`, "dot"},
	{`{"Circle": 2}`, "circle:0.5"},
	{`{"Rect": [1.5, 4]}`, "rect:0.375x1"},
	{`{"Label": {"size": 9, "text": "named"}}`, "label:named@9"},
	{`{"At": {"x": -1, "y": 0.25}}`, "at:(-1,0.0625)"},
}

func TestWhatTheConsoleWritesTheProgramReads(t *testing.T) {
	file, _ := filepath.Abs("testdata/shapes.qi")
	errs := lerrors.NewHandler()
	result, err := compiler.CompileFileOptions(file, compiler.Options{}, errs)
	if err != nil || errs.HasErrors() {
		t.Fatalf("%v %v", err, errs.Diagnostics)
	}
	rt := flowrt.New(result.Chunk, flowrt.NewMemory(), io.Discard)
	rt.Workers = 4
	d := flowd.New(rt, []string{"Echo"})
	d.Schema = flowd.SchemaOf(file, result.AST, []string{file})
	ctx, cancel := context.WithCancel(context.Background())
	go d.Run(ctx, 5*time.Millisecond)
	server := httptest.NewServer(d.Handler())
	defer func() {
		server.Close()
		cancel()
	}()

	for form, fields := range shapeStarts {
		t.Run(form, func(t *testing.T) {
			id := "echo:" + strings.ReplaceAll(form, " ", "-")
			if status, body := call(t, d, server, "POST", "/api/instances", `{"flow": "Echo", "id": "`+id+`", "fields": `+fields+`}`); status != http.StatusCreated {
				t.Fatalf("start: %d %s", status, body)
			}
			for i, m := range shapeMessages {
				if status, body := call(t, d, server, "POST", "/api/instances/"+id+"/inboxes/shapes", fmt.Sprintf(`{"message": %s, "key": "m%d"}`, m.message, i)); status != http.StatusOK {
					t.Fatalf("message %s: %d %s", m.message, status, body)
				}
			}
			status, body := call(t, d, server, "GET", "/api/instances/"+id+"/result?wait=20s", "")
			if status != http.StatusOK {
				t.Fatalf("result: %d %s", status, body)
			}
			var text string
			if err := jsonText(body, &text); err != nil {
				t.Fatalf("%v: %s", err, body)
			}
			lines := strings.Split(text, "\n")
			if len(lines) != 3 {
				t.Fatalf("expected three lines, got %q", text)
			}
			given, made, messages := lines[0], lines[1], lines[2]
			if given != made {
				t.Errorf("what the console wrote reads otherwise than the program's own values:\n given %s\n made  %s", given, made)
			}
			var want []string
			for _, m := range shapeMessages {
				want = append(want, m.read)
			}
			if messages != strings.Join(want, " ") {
				t.Errorf("the messages read as %q, expected %q", messages, strings.Join(want, " "))
			}
		})
	}
}

// jsonText reads a JSON text.
func jsonText(body string, out *string) error {
	return json.Unmarshal([]byte(strings.TrimSpace(body)), out)
}
