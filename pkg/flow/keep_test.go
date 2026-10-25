package flow_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qoqiflow/qoqi/pkg/flow"
	_ "modernc.org/sqlite"
)

// shop is an application's database: its orders, and the answers of the
// steps a Keeper keeps beside them.
func shop(t *testing.T) (*sql.DB, *flow.Keeper) {
	t.Helper()
	db, err := sql.Open("sqlite", t.TempDir()+"/shop.db?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE shipped (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	k := &flow.Keeper{DB: db}
	if err := k.Setup(context.Background()); err != nil {
		t.Fatal(err)
	}
	return db, k
}

func count(t *testing.T, db *sql.DB, query string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func taskOf(shape, key string) flow.Task {
	return flow.Task{Key: key, Attempt: 1, Token: "t", Until: time.Now().Add(time.Minute).UnixMilli(), Step: "ship", Shape: shape, Args: json.RawMessage(`{}`)}
}

// A step under once: its effect and its answer in one transaction. Sent
// again, it answers what it did, and does not act; one that fails leaves
// neither its effect nor an answer; two at once act once.
func TestOnce(t *testing.T) {
	db, k := shop(t)
	var acted atomic.Int32
	fail := false
	q := flow.NewQueue("billing", "s1")
	q.Handle("ship-1", func(ctx context.Context, task *flow.Task) flow.Answer {
		acted.Add(1)
		tx := flow.TxOf(ctx)
		if _, err := tx.ExecContext(ctx, `INSERT INTO shipped (id) VALUES (?)`, task.Key); err != nil {
			return flow.Failure(err)
		}
		if fail {
			return flow.Failure(errors.New("the carrier refused"))
		}
		time.Sleep(10 * time.Millisecond)
		return flow.Answered(flow.Value(flow.Ok[string, string]("parcel-" + task.Key)))
	})
	q.KeepOnce(k)

	if _, said := post(t, q, "POST", taskOf("ship-1", "a")); said != `{"value":{"Ok":"parcel-a"}}` {
		t.Fatalf("the first: %s", said)
	}
	if _, said := post(t, q, "POST", taskOf("ship-1", "a")); said != `{"value":{"Ok":"parcel-a"}}` || acted.Load() != 1 {
		t.Errorf("sent again, it answers what it did without acting: %s, acted %d", said, acted.Load())
	}

	fail = true
	if _, said := post(t, q, "POST", taskOf("ship-1", "b")); !strings.Contains(said, "the carrier refused") {
		t.Errorf("a failure: %s", said)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM shipped WHERE id = 'b'`); n != 0 {
		t.Errorf("a step that failed leaves no effect, got %d", n)
	}
	fail = false

	var wg sync.WaitGroup
	answers := make([]string, 4)
	for i := range answers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, answers[i] = post(t, q, "POST", taskOf("ship-1", "c"))
		}(i)
	}
	wg.Wait()
	for _, said := range answers {
		if said != `{"value":{"Ok":"parcel-c"}}` {
			t.Errorf("each of four at once answers the same: %v", answers)
			break
		}
	}
	if n := count(t, db, `SELECT COUNT(*) FROM shipped WHERE id = 'c'`); n != 1 {
		t.Errorf("four at once ship once, got %d", n)
	}

	if _, said := post(t, q, "GET", flow.Task{}); !strings.Contains(said, `"once":["ship-1"]`) {
		t.Errorf("the manifest says ship-1 is kept under once: %s", said)
	}
}

// A step under started: a claim before it acts, alive while it does. One
// sent again meanwhile is told it is under way; after, it answers what
// it did. A claim found dead is unknown, until a person says it did
// nothing: the next delivery reclaims it, and acts.
func TestStarted(t *testing.T) {
	db, k := shop(t)
	var acted atomic.Int32
	release := make(chan struct{})
	q := flow.NewQueue("billing", "s1")
	q.Handle("wire-1", func(ctx context.Context, task *flow.Task) flow.Answer {
		acted.Add(1)
		if task.Key == "slow" {
			<-release
		}
		return flow.Answered(flow.Value(flow.Ok[string, string]("w-" + task.Key)))
	})
	q.KeepStarted(k, "wire-1")

	done := make(chan string)
	go func() {
		_, said := post(t, q, "POST", taskOf("wire-1", "slow"))
		done <- said
	}()
	deadline := time.Now().Add(5 * time.Second)
	for count(t, db, `SELECT COUNT(*) FROM flow_steps WHERE key = 'slow'`) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if status, said := post(t, q, "POST", taskOf("wire-1", "slow")); status != 200 || said != `{"working":true}` {
		t.Errorf("sent again while it acts: %d %s", status, said)
	}
	probe := taskOf("wire-1", "slow")
	probe.Probe = true
	if _, said := post(t, q, "POST", probe); said != `{"working":true}` {
		t.Errorf("asked while it acts: %s", said)
	}
	close(release)
	if said := <-done; said != `{"value":{"Ok":"w-slow"}}` {
		t.Errorf("its answer: %s", said)
	}
	if _, said := post(t, q, "POST", probe); said != `{"value":{"Ok":"w-slow"}}` {
		t.Errorf("asked after, it gives what it did: %s", said)
	}

	nothing := taskOf("wire-1", "never")
	nothing.Probe = true
	if _, said := post(t, q, "POST", nothing); said != `{"unseen":true}` {
		t.Errorf("asked of what it never saw: %s", said)
	}

	// A process that died as it acted: its claim, not renewed
	if _, err := db.Exec(`INSERT INTO flow_steps (key, state, answer, beat) VALUES ('dead', 'started', '', ?)`, time.Now().Add(-time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, said := post(t, q, "POST", taskOf("wire-1", "dead")); said != `{"unknown":true}` {
		t.Errorf("a dead claim: %s", said)
	}
	reclaim := taskOf("wire-1", "dead")
	reclaim.Reclaim = 1
	if _, said := post(t, q, "POST", reclaim); said != `{"value":{"Ok":"w-dead"}}` {
		t.Errorf("reclaimed, it acts: %s", said)
	}
	if acted.Load() != 2 {
		t.Errorf("acted for slow and the reclaimed, got %d", acted.Load())
	}
	if _, said := post(t, q, "GET", flow.Task{}); !strings.Contains(said, `"started":["wire-1"]`) {
		t.Errorf("the manifest says wire-1 is kept under started: %s", said)
	}
}

// A step under started that answers later keeps its claim: sent again,
// or probed, before its answer, it is under way, and does not act a
// second time; its answer, kept by Daemon.Complete with the Keeper, is
// what it gives after.
func TestStartedThatAnswersLater(t *testing.T) {
	_, k := shop(t)
	var acted atomic.Int32
	q := flow.NewQueue("billing", "s1")
	q.Handle("wire-1", func(ctx context.Context, task *flow.Task) flow.Answer {
		acted.Add(1)
		return flow.AnswerOf(flow.ErrLater)
	})
	q.KeepStarted(k, "wire-1")

	if status, _ := post(t, q, "POST", taskOf("wire-1", "later")); status != 202 {
		t.Fatalf("it answers later: %d", status)
	}
	probe := taskOf("wire-1", "later")
	probe.Probe = true
	if _, said := post(t, q, "POST", probe); said != `{"working":true}` {
		t.Errorf("probed before its answer: %s", said)
	}
	if status, said := post(t, q, "POST", taskOf("wire-1", "later")); status != 200 || said != `{"working":true}` {
		t.Errorf("sent again before its answer: %d %s", status, said)
	}
	if acted.Load() != 1 {
		t.Errorf("a step that answers later acted %d times", acted.Load())
	}
	task := taskOf("wire-1", "later")
	if err := k.Answered(context.Background(), &task, flow.Answered(flow.Value(flow.Ok[string, string]("w-later")))); err != nil {
		t.Fatal(err)
	}
	if _, said := post(t, q, "POST", probe); said != `{"value":{"Ok":"w-later"}}` {
		t.Errorf("probed after its answer: %s", said)
	}
}
