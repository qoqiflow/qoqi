package flowd_test

import (
	"testing"
	"time"

	"github.com/qoqiflow/qoqi/internal/flowd"
)

// Data as the store keeps it, written as Qoqi writes the same value.
func TestValuesAreWrittenAsQoqi(t *testing.T) {
	for data, want := range map[string]string{
		`"shipped"`:         `"shipped"`,
		`"été \"quoted\""`:  `"été \"quoted\""`,
		`-42`:               `-42`,
		`2.0`:               `2.0`,
		`{"$float": "NaN"}`: `NaN`,
		`null`:              `null`,
		`true`:              `true`,
		`[3, 1, 2]`:         `[3, 1, 2]`,
		`{"$type": "std.result.Result.Ok", "$variant": true, "0": "van-3"}`:                                                      `Ok("van-3")`,
		`{"$type": "Decision.Approved", "$variant": true}`:                                                                       `Approved`,
		`{"$type": "std.result.Result.Err", "$variant": true, "0": {"$type": "Problem.NoVan", "$variant": true, "0": "no van"}}`: `Err(NoVan("no van"))`,
		// Fields in the order the data has them, not sorted again
		`{"$type": "Cart", "id": "1", "customer": "ada", "amount": 4200, "note": null}`: `Cart { id: "1", customer: "ada", amount: 4200, note: null }`,
		`{"$type": "Empty"}`: `Empty {}`,
		// A variant's values by place, eleven and more in their order
		`{"$type": "Wide.Many", "$variant": true, "0": 0, "1": 1, "2": 2, "3": 3, "4": 4, "5": 5, "6": 6, "7": 7, "8": 8, "9": 9, "10": 10, "11": 11}`: `Many(0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11)`,
		// A Dict as its literal, the removed keys left out
		`{"$type": "std.dict.Dict", "keys": ["b", "a"], "values": [2, 1], "count": 2, "index": null, "dead": null, "removed": 0}`:                         `{"b": 2, "a": 1}`,
		`{"$type": "std.dict.Dict", "keys": ["b", "a", "c"], "values": [2, 1, 3], "count": 2, "index": null, "dead": [false, true, false], "removed": 1}`: `{"b": 2, "c": 3}`,
		`{"$type": "Box", "item": {"$type": "Shape.Label", "$variant": true, "0": "hi", "1": 12}, "tag": "t"}`:                                            `Box { item: Label("hi", 12), tag: "t" }`,
		`not json`: `not json`,
		``:         ``,
	} {
		if got := flowd.Qoqi([]byte(data)); got != want {
			t.Errorf("%s\n got  %s\n want %s", data, got, want)
		}
	}
}

// What an instance is doing, in the words the pages use.
func TestWhatAnInstanceIsDoing(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		view flowd.View
		want string
	}{
		{flowd.View{Status: "waiting", Awaits: []string{"payment:1"}}, "waits for payment:1"},
		{flowd.View{Status: "waiting", Awaits: []string{"cancel:", "inbox:answers"}}, "waits for a message on answers"},
		{flowd.View{Status: "waiting", Awaits: []string{"payment:1", "inbox:news", "inbox:notes"}}, "waits for payment:1, a message on news or notes"},
		{flowd.View{Status: "waiting", Awaits: []string{"answer:o/k7f2/0:0:charge#1"}, WakeAt: "2026-10-16T12:00:30Z"}, "waits for the application's answer to charge"},
		{flowd.View{Status: "waiting", WakeAt: "2026-10-16T12:00:30Z"}, "wakes in 30 s"},
		{flowd.View{Status: "waiting", WakeAt: "2026-10-16T12:05:00Z"}, "wakes in 5 min"},
		{flowd.View{Status: "waiting", WakeAt: "2026-10-19T12:00:00Z"}, "wakes in 3 days"},
		{flowd.View{Status: "waiting", WakeAt: "2026-10-16T12:00:00Z"}, "due now"},
		{flowd.View{Status: "waiting", Cancel: "enough"}, "cancelling: enough"},
		{flowd.View{Status: "done", Result: []byte(`{"$type": "std.result.Result.Ok", "$variant": true, "0": "van-3"}`)}, `returned Ok("van-3")`},
		{flowd.View{Status: "failed", Cancel: "looked at", Ended: "looked at"}, "ended as it was: looked at"},
		{flowd.View{Status: "failed", Cancel: "plans changed"}, "cancelled: plans changed"},
		{flowd.View{Status: "stopped", Stopped: "[ERROR] R0002: index out of bounds\n  --> a.qi:3"}, "[ERROR] R0002: index out of bounds"},
		{flowd.View{Status: "running", Phase: "finish"}, "undoing what it did"},
	} {
		if got := flowd.Doing(c.view, now); got != c.want {
			t.Errorf("%+v: got %q, want %q", c.view, got, c.want)
		}
	}
}

// A length of time, read as a person writes it, and written as Qoqi does.
func TestDurations(t *testing.T) {
	for text, ms := range map[string]int64{
		"90s": 90000, "1h30m": 5400000, "1.5h": 5400000, "2d": 172800000, "2 days": 172800000,
		"250ms": 250, "1 h 30 min": 5400000, "1w": 604800000, "3 s": 3000, " 1M ": 60000,
	} {
		got, err := flowd.ParseDuration(text)
		if err != nil || got != ms {
			t.Errorf("%q: got %d %v, want %d", text, got, err, ms)
		}
	}
	for _, wrong := range []string{"", "90", "3 parsecs", "s", "1h banana"} {
		if _, err := flowd.ParseDuration(wrong); err == nil {
			t.Errorf("%q: expected it refused", wrong)
		}
	}
	for ms, want := range map[int64]string{
		0: "0.seconds()", 3000: "3.seconds()", 5400000: "90.minutes()", 172800000: "2.days()", 250: "250.milliseconds()", 1500: "1500.milliseconds()", -7200000: "(-2).hours()",
	} {
		if got := flowd.DurationLiteral(ms); got != want {
			t.Errorf("%d: got %s, want %s", ms, got, want)
		}
	}
	if got := flowd.Qoqi([]byte(`{"$type": "Expense", "patience": {"$type": "std.time.Duration", "ms": 60000}}`)); got != `Expense { patience: 1.minutes() }` {
		t.Errorf("a Duration in a struct: %s", got)
	}
}
