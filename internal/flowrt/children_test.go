package flowrt_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/qoqiflow/qoqi/internal/flowrt"
)

// The order of the design: it holds the stock, has the payment made by a
// flow of its own, then books the van. The payment authorizes, sleeps,
// then captures; its authorization is voided if it is cancelled.
const family = `
import std.flow;
import std.flow.{Flow, Instance};
import std.time;

struct Cart { id: String; amount: Int; }
enum Outcome { Paid(String), Refused(String) }
enum Problem { Refused(String), Broken(String) }

struct Payment { cart: Cart; }

impl Flow<Outcome> for Payment {
    fn run() -> Outcome {
        let auth = authorize(self.cart);
        wait time.after(20.milliseconds());

        if self.cart.amount > 1000 {
            return Outcome.Refused("too much");
        }
        if self.cart.id == "broken" {
            let none: [Int] = [];
            println(none[1]);
        }
        return Outcome.Paid(capture(auth));
    }
}

struct Order { cart: Cart; }

impl Flow<Result<String, Problem>> for Order {
    fn run() -> Result<String, Problem> {
        let hold = reserve(self.cart);
        let payment = flow.start("payment:${self.cart.id}", Payment { cart: self.cart });

        let outcome = wait payment;
        match outcome {
            case Err(cancelled): {
                return Err(Problem.Broken(cancelled.reason));
            }
            case Ok(result): {
                match result {
                    case Paid(reference): {
                        flow.undo(refund, reference);
                        return book(self.cart, hold, reference);
                    }
                    case Refused(reason): {
                        return Err(Problem.Refused(reason));
                    }
                }
            }
        }
    }
}

step fn reserve(cart: Cart) -> String {
    println("WORLD reserve", cart.id);
    flow.undo(release, "h-" + cart.id);
    return "h-" + cart.id;
}

step fn authorize(cart: Cart) -> String {
    println("WORLD authorize", cart.id);
    flow.undo(void, "a-" + cart.id);
    return "a-" + cart.id;
}

step fn capture(auth: String) -> String {
    println("WORLD capture", auth);
    return "p-" + auth;
}

step fn book(cart: Cart, hold: String, reference: String) -> Result<String, Problem> {
    println("WORLD book", cart.id);
    if cart.id == "nobook" {
        return Err(Problem.Broken("no van"));
    }
    return Ok("${hold} ${reference}");
}

step fn release(hold: String) -> Result<Void, Problem> {
    println("WORLD release", hold);
    return Ok(null);
}

step fn refund(reference: String) -> Result<Void, Problem> {
    println("WORLD refund", reference);
    return Ok(null);
}

step fn void(auth: String) -> Result<Void, String> {
    println("WORLD void", auth);
    return Ok(null);
}

struct Campaign { count: Int; }

impl Flow<Int> for Campaign {
    fn run() -> Int {
        let started: [Instance<Outcome>] = [];
        for i in 0..self.count {
            started.push(flow.start("pay:${i}", Payment { cart: Cart { id: "c${i}", amount: i * 300 } }));
        }

        let paid = 0;
        for payment in started {
            match wait payment {
                case Ok(outcome): {
                    match outcome {
                        case Paid(_): { paid += 1; }
                        case Refused(_): {}
                    }
                }
                case Err(_): {}
            }
        }
        return paid;
    }
}

struct Twice { id: String; }

impl Flow<String> for Twice {
    fn run() -> String {
        let first = flow.start("shared", Payment { cart: Cart { id: self.id, amount: 1 } });
        let again = flow.start("shared", Payment { cart: Cart { id: "other", amount: 5000 } });
        let a = wait first;
        let b = wait again;
        return "${a} ${b}";
    }
}

struct Clash { n: Int; }

impl Flow<Int> for Clash {
    fn run() -> Int {
        flow.start("shared", Order { cart: Cart { id: "x", amount: 1 } });
        return self.n;
    }
}

struct Leaver { id: String; }

impl Flow<Result<Int, Problem>> for Leaver {
    fn run() -> Result<Int, Problem> {
        flow.detach("alone:${self.id}", Payment { cart: Cart { id: self.id, amount: 1 } });
        flow.start("child:${self.id}", Payment { cart: Cart { id: self.id, amount: 1 } });
        wait time.after(1.hours());
        return Ok(1);
    }
}

fn main() {}
`

func orderOf(id string, amount int) string {
	return `{"$type": "Order", "cart": {"$type": "Cart", "id": "` + id + `", "amount": ` + itoa(amount) + `}}`
}

func TestAnOrderAndItsPayment(t *testing.T) {
	chunk := compiled(t, family)
	for _, c := range []struct {
		name, id string
		amount   int
		result   string
		world    []string
	}{
		{"paid and booked", "fine", 5,
			`{"$type":"std.result.Result.Ok","$variant":true,"0":"h-fine p-a-fine"}`,
			[]string{"WORLD reserve fine", "WORLD authorize fine", "WORLD capture a-fine", "WORLD book fine"}},
		{"refused: the hold is released, the payment kept its own", "rich", 5000,
			`{"$type":"std.result.Result.Err","$variant":true,"0":{"$type":"Problem.Refused","$variant":true,"0":"too much"}}`,
			[]string{"WORLD reserve rich", "WORLD authorize rich", "WORLD release h-rich"}},
		{"paid, then no van: refunded, then released", "nobook", 5,
			`{"$type":"std.result.Result.Err","$variant":true,"0":{"$type":"Problem.Broken","$variant":true,"0":"no van"}}`,
			[]string{"WORLD reserve nobook", "WORLD authorize nobook", "WORLD capture a-nobook", "WORLD book nobook", "WORLD refund p-a-nobook", "WORLD release h-nobook"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			store := flowrt.NewMemory()
			inst := runFlow(t, flowrt.New(chunk, store, &out), store, "o", "Order", orderOf(c.id, c.amount))
			if inst.Status != flowrt.Done || string(inst.Result) != c.result {
				t.Fatalf("expected done with %s, got %s %s %q", c.result, inst.Status, inst.Result, inst.Stopped)
			}
			if got := worldLines(out.String()); strings.Join(got, "|") != strings.Join(c.world, "|") {
				t.Errorf("want %v\ngot  %v", c.world, got)
			}
			child, ok, _ := store.Get("payment:" + c.id)
			if !ok || child.Status != flowrt.Done || child.Parent != "o" || strings.Join(inst.Children, ",") != "payment:"+c.id {
				t.Errorf("expected the payment done, the order's child: %+v %v", child, inst.Children)
			}
		})
	}
}

// The parent asleep while its child sleeps is written once, and woken by
// the write that ends the child.
func TestAParentWaitsWithoutTheClock(t *testing.T) {
	chunk := compiled(t, family)
	store := flowrt.NewMemory()
	rt := flowrt.New(chunk, store, &bytes.Buffer{})
	if _, err := rt.Start("o", "Order", []byte(orderOf("fine", 5))); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.RunDue(); err != nil {
		t.Fatal(err)
	}
	inst, _, _ := store.Get("o")
	if inst.Status != flowrt.Waiting || inst.WakeAt != flowrt.Never || strings.Join(inst.Awaits, ",") != "payment:fine" {
		t.Fatalf("expected it asleep for its payment alone, got %s %d %v", inst.Status, inst.WakeAt, inst.Awaits)
	}
	if err := rt.RunUntilIdle(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	if inst, _, _ := store.Get("o"); inst.Status != flowrt.Done {
		t.Errorf("expected the order done once its payment was, got %s %q", inst.Status, inst.Stopped)
	}
}

// A cancelled order cancels its payment first: the payment's undo runs
// before the order's.
func TestCancellingAParent(t *testing.T) {
	chunk := compiled(t, family)
	var out bytes.Buffer
	store := flowrt.NewMemory()
	rt := flowrt.New(chunk, store, &out)
	if _, err := rt.Start("o", "Order", []byte(orderOf("late", 5))); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := rt.RunDue(); err != nil {
			t.Fatal(err)
		}
	}
	if err := rt.Cancel("o", "withdrawn"); err != nil {
		t.Fatal(err)
	}
	if err := rt.RunUntilIdle(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	inst, _, _ := store.Get("o")
	child, _, _ := store.Get("payment:late")
	if inst.Status != flowrt.Failed || child.Status != flowrt.Failed || !strings.Contains(child.Cancel, "its parent o is ending") {
		t.Fatalf("expected both failed, got %s %s %q", inst.Status, child.Status, child.Cancel)
	}
	want := []string{"WORLD reserve late", "WORLD authorize late", "WORLD void a-late", "WORLD release h-late"}
	if got := worldLines(out.String()); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("want %v\ngot  %v", want, got)
	}
}

// A child stopped by a mistake: its parent waits on. Cancelling the
// parent cancels the child, whose undos run, then the parent's.
func TestAStoppedChild(t *testing.T) {
	chunk := compiled(t, family)
	var out bytes.Buffer
	store := flowrt.NewMemory()
	rt := flowrt.New(chunk, store, &out)
	if _, err := rt.Start("o", "Order", []byte(orderOf("broken", 5))); err != nil {
		t.Fatal(err)
	}
	if err := rt.RunUntilIdle(time.Second); err == nil {
		t.Fatalf("expected the order still waiting for its stopped payment")
	}
	child, _, _ := store.Get("payment:broken")
	if inst, _, _ := store.Get("o"); child.Status != flowrt.Stopped || inst.Status != flowrt.Waiting {
		t.Fatalf("expected the payment stopped and the order waiting, got %s %s", child.Status, inst.Status)
	}
	if err := rt.Cancel("o", "a person looked"); err != nil {
		t.Fatal(err)
	}
	if err := rt.RunUntilIdle(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	inst, _, _ := store.Get("o")
	if inst.Status != flowrt.Failed || !strings.HasSuffix(strings.Join(worldLines(out.String()), "|"), "WORLD void a-broken|WORLD release h-broken") {
		t.Errorf("expected failed, the payment undone first, got %s\n%s", inst.Status, out.String())
	}
}

// A name is an instance: started twice it is one; started as another
// flow, the instance that does so stops. A detached child outlives a
// cancelled parent; the other is cancelled with it.
func TestNamesAndDetachedChildren(t *testing.T) {
	chunk := compiled(t, family)
	store := flowrt.NewMemory()
	rt := flowrt.New(chunk, store, &bytes.Buffer{})

	inst := runFlow(t, rt, store, "twice", "Twice", `{"id": "first"}`)
	if inst.Status != flowrt.Done || !strings.Contains(string(inst.Result), `Ok(Paid(\"p-a-first\")) Ok(Paid(\"p-a-first\"))`) {
		t.Errorf("expected the same instance twice, got %s %s", inst.Status, inst.Result)
	}
	inst = runFlow(t, rt, store, "clash", "Clash", `{"n": 1}`)
	if inst.Status != flowrt.Stopped || !strings.Contains(inst.Stopped, "shared is an instance of Payment, not of Order") {
		t.Errorf("expected stopped on the name, got %s %q", inst.Status, inst.Stopped)
	}

	if _, err := rt.Start("leaver", "Leaver", []byte(`{"id": "z"}`)); err != nil {
		t.Fatal(err)
	}
	rt.RunDue()
	if err := rt.Cancel("leaver", "enough"); err != nil {
		t.Fatal(err)
	}
	if err := rt.RunUntilIdle(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	alone, _, _ := store.Get("alone:z")
	child, _, _ := store.Get("child:z")
	if alone.Status != flowrt.Done || alone.Parent != "" || child.Status != flowrt.Failed {
		t.Errorf("expected the detached one done and the child cancelled, got %s %q %s", alone.Status, alone.Parent, child.Status)
	}
}

// Twelve payments, waited for in turn: each written alone, the campaign
// written when it starts them and when it wakes.
func TestACampaign(t *testing.T) {
	chunk := compiled(t, family)
	store := flowrt.NewMemory()
	rt := flowrt.New(chunk, store, &bytes.Buffer{})
	inst := runFlow(t, rt, store, "campaign", "Campaign", `{"count": 12}`)
	if inst.Status != flowrt.Done || string(inst.Result) != "4" {
		t.Fatalf("expected four paid (the others too much), got %s %s %q", inst.Status, inst.Result, inst.Stopped)
	}
	if len(inst.Children) != 12 {
		t.Errorf("expected twelve children, got %v", inst.Children)
	}
}

// Stopped at any write, the parent and its child end the same, and each
// step is done once, or twice when the stop fell between doing it and
// keeping what it gave.
func TestFamiliesSurviveAStopAtEveryWrite(t *testing.T) {
	everyStopOf(t, family, func(rt *flowrt.Runtime) error {
		_, err := rt.Start("o", "Order", []byte(orderOf("nobook", 5)))
		return err
	}, flowrt.Done, []string{"WORLD reserve nobook", "WORLD authorize nobook", "WORLD capture a-nobook", "WORLD book nobook", "WORLD refund p-a-nobook", "WORLD release h-nobook"})

	everyStopOf(t, family, func(rt *flowrt.Runtime) error {
		_, err := rt.Start("o", "Campaign", []byte(`{"count": 3}`))
		return err
	}, flowrt.Done, []string{"WORLD authorize c0", "WORLD authorize c1", "WORLD authorize c2", "WORLD capture a-c0", "WORLD capture a-c1", "WORLD capture a-c2"})
}

// On a file of SQLite, stopped at every write.
func TestSQLiteFamilies(t *testing.T) {
	chunk := compiled(t, family)
	stoppedEverywhereOnDisk(t, chunk, "Order", orderOf("nobook", 5), func(k int, out string, inst *flowrt.Instance) {
		// The release may be done again, stopped as it was kept
		lines := strings.ReplaceAll(strings.Join(worldLines(out), "|"), "WORLD release h-nobook|WORLD release h-nobook", "WORLD release h-nobook")
		if inst.Status != flowrt.Done || !strings.Contains(string(inst.Result), "no van") || !strings.HasSuffix(lines, "WORLD refund p-a-nobook|WORLD release h-nobook") {
			t.Errorf("stopped at write %d: expected done, refunded then released, got %s %s\n%s", k, inst.Status, inst.Result, lines)
		}
	})
}
