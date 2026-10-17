package flowd_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/compiler"
	lerrors "github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/flowd"
	"github.com/qoqiflow/qoqi/internal/vm"
)

const billing = `
import std.flow.{Flow, Settled};
import std.time;

struct Line { sku: String; cents: Int; }
struct Invoice { id: Int; lines: [Line]; note: String?; rate: Float; }
enum Decline { Insufficient, Fraud(String), Limit(Int, Int), Held { by: String, until: Int } }
struct Charge { id: String; }
struct Hold { id: String; }

struct Order { invoice: Invoice; }

impl Flow<Result<Charge, Decline>> for Order {
    fn run() -> Result<Charge, Decline> {
        let hold = reserve(self.invoice.id)?;
        let _ = wire(hold.id);
        return charge(self.invoice);
    }
}

step fn charge(invoice: Invoice) -> Result<Charge, Decline> by billing;
step fn reserve(id: Int) -> Result<Hold, Decline> by billing undo release;
step fn release(hold: Hold) -> Result<Void, Decline> by billing;
step fn wire(to: String) -> Settled<Result<String, Decline>> within 2.hours() by billing;

fn main() {}
`

func billingSchema(t *testing.T) *flowd.Schema {
	t.Helper()
	errs := lerrors.NewHandler()
	result := compiler.CompileOptions(billing, "billing.qi", compiler.Options{Queues: []string{"billing"}}, errs)
	for _, d := range errs.Diagnostics {
		if d.Severity == lerrors.SeverityError {
			t.Fatalf("unexpected diagnostic:\n%s", d.String())
		}
	}
	return flowd.SchemaOf("billing.qi", result.AST, nil)
}

// A step's arguments go out without "$type", each as its type is written
// at the boundary.
func TestArgumentsAtTheBoundary(t *testing.T) {
	b := flowd.Boundary{Schema: billingSchema(t)}
	args := `{"invoice": {"$type": "Invoice", "id": 42, "lines": [{"$type": "Line", "sku": "a-1", "cents": 1200}], "note": null, "rate": {"$float": "NaN"}}}`
	got, err := b.Args("charge", json.RawMessage(args))
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"invoice":{"id":42,"lines":[{"sku":"a-1","cents":1200}],"note":null,"rate":"NaN"}}`; string(got) != want {
		t.Errorf("want %s\ngot  %s", want, got)
	}
	if s := b.Schema.Step("charge"); s == nil || s.Queue != "billing" {
		t.Errorf("the schema knows charge is by billing: %+v", s)
	}
	if s := b.Schema.Step("reserve"); s == nil || s.Undo != "release" {
		t.Errorf("the schema knows reserve is undone by release: %+v", s)
	}
}

// Each kind of value, written by the program and read back from the
// boundary, is the same.
func TestValuesThereAndBack(t *testing.T) {
	schema := billingSchema(t)
	b := flowd.Boundary{Schema: schema}
	charge := schema.Step("charge")
	for _, c := range []struct{ data, plain string }{
		{`{"$type": "std.result.Result.Ok", "$variant": true, "0": {"$type": "Charge", "id": "ch-1"}}`, `{"Ok":{"id":"ch-1"}}`},
		{`{"$type": "std.result.Result.Err", "$variant": true, "0": {"$type": "Decline.Insufficient", "$variant": true}}`, `{"Err":"Insufficient"}`},
		{`{"$type": "std.result.Result.Err", "$variant": true, "0": {"$type": "Decline.Fraud", "$variant": true, "0": "card stolen"}}`, `{"Err":{"Fraud":"card stolen"}}`},
		{`{"$type": "std.result.Result.Err", "$variant": true, "0": {"$type": "Decline.Limit", "$variant": true, "0": 100, "1": 50}}`, `{"Err":{"Limit":[100,50]}}`},
		{`{"$type": "std.result.Result.Err", "$variant": true, "0": {"$type": "Decline.Held", "$variant": true, "0": "risk", "1": 7}}`, `{"Err":{"Held":{"by":"risk","until":7}}}`},
	} {
		var value any
		json.Unmarshal([]byte(c.data), &value)
		plain, _ := json.Marshal(schema.Plain(value, charge.Returns))
		if string(plain) != c.plain {
			t.Errorf("written: want %s\ngot  %s", c.plain, plain)
			continue
		}
		back := b.Answer("charge", "", vm.Answer{Outcome: vm.Answered, Value: plain})
		var x, y any
		json.Unmarshal([]byte(c.data), &x)
		json.Unmarshal(back.Value, &y)
		if back.Outcome != vm.Answered || !same(x, y) {
			t.Errorf("read back %s: want %s\ngot  %s %s", plain, c.data, back.Value, back.Reason)
		}
	}
}

func same(x, y any) bool {
	a, _ := json.Marshal(x)
	b, _ := json.Marshal(y)
	return string(a) == string(b)
}

// An answer is read against the step's type, its undo against the
// parameter of the step that undoes it; what does not fit is a panic of
// the step, saying where.
func TestAnAnswerReadAgainstItsStep(t *testing.T) {
	b := flowd.Boundary{Schema: billingSchema(t)}
	got := b.Answer("reserve", "release", vm.Answer{Outcome: vm.Answered, Value: []byte(`{"Ok": {"id": "h-1"}}`), Undo: []byte(`{"id": "h-1"}`)})
	if got.Outcome != vm.Answered || !strings.Contains(string(got.Undo), `"$type":"Hold"`) || !strings.Contains(string(got.Value), `"$type":"std.result.Result.Ok"`) {
		t.Errorf("the value and its undo typed: %s %s %s", got.Value, got.Undo, got.Reason)
	}
	for _, c := range []struct{ step, value, undo, why string }{
		{"charge", `{"Ok": {"id": 3}}`, ``, "a String"},
		{"charge", `{"Maybe": {"id": "x"}}`, ``, "Ok"},
		{"reserve", `{"Ok": {"id": "h-1"}}`, `{"id": true}`, "the undo of reserve"},
		{"wire", `"Pending"`, ``, "Done"},
	} {
		a := vm.Answer{Outcome: vm.Answered, Value: []byte(c.value)}
		if c.undo != "" {
			a.Undo = []byte(c.undo)
		}
		got := b.Answer(c.step, "release", a)
		if got.Outcome != vm.Failed || !strings.Contains(got.Reason, c.why) {
			t.Errorf("%s %s: expected a misfit naming %q, got %v %q", c.step, c.value, c.why, got.Outcome, got.Reason)
		}
	}
	if got := b.Answer("wire", "", vm.Answer{Outcome: vm.Answered, Value: []byte(`"Unknown"`)}); got.Outcome != vm.Answered || !strings.Contains(string(got.Value), "std.flow.Settled.Unknown") {
		t.Errorf("Unknown as its name: %s %q", got.Value, got.Reason)
	}
}
