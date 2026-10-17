package flowd_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qoqiflow/qoqi/internal/flowd"
	"github.com/qoqiflow/qoqi/internal/flowrt"
)

// sharing is a daemon on a store another shares.
type sharing struct {
	d      *flowd.Daemon
	server *httptest.Server
	out    *lines
}

func newSharing(t *testing.T, path, name string) *sharing {
	t.Helper()
	store, err := flowrt.OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	store.ShareAs(name)
	d, err := flowd.NewOf(nil)
	if err != nil {
		t.Fatal(err)
	}
	s := &sharing{d: d, out: &lines{}}
	d.Open = func(program string) (flowrt.Store, error) { return store.Program(program) }
	d.Deployments, d.Workers, d.Out = store, 2, s.out
	if err := d.Restore(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		d.Run(ctx, 5*time.Millisecond)
		close(done)
	}()
	s.server = httptest.NewServer(d.Handler())
	t.Cleanup(func() {
		s.server.Close()
		cancel()
		<-done
		store.Close()
	})
	return s
}

func (s *sharing) serves(t *testing.T, flow string) bool {
	_, said := call(t, s.d, s.server, "GET", "/api/flows", "")
	return strings.Contains(said, `"name": "`+flow+`"`)
}

// serving is the seq of the deploy of a program the daemon serves.
func (s *sharing) serving(t *testing.T, program string) int64 {
	_, said := call(t, s.d, s.server, "GET", "/api/deployments?program="+program, "")
	var deploys []flowd.DeployView
	json.Unmarshal([]byte(said), &deploys)
	for _, d := range deploys {
		if d.Serving {
			return d.Seq
		}
	}
	return 0
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("waited in vain for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Two daemons on one store: what one deploys the other serves, an
// instance started on one is sent to through the other and ends on
// either, a new version deployed to the second reaches the first, and a
// program taken away from one is taken away from both.
func TestDaemonsFollowEachOthersDeploys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flows.db")
	a, b := newSharing(t, path, "a"), newSharing(t, path, "b")

	body, _ := json.Marshal(map[string]any{"programs": []*flowd.Package{packageOf(t, "tally", tally(1))}})
	if status, said := call(t, a.d, a.server, "POST", "/api/deploy", string(body)); status != http.StatusOK {
		t.Fatalf("deploy to a: %d %s", status, said)
	}
	eventually(t, "b to serve what a deployed", func() bool { return b.serves(t, "Tally") })

	if status, said := call(t, b.d, b.server, "POST", "/api/instances", `{"flow": "Tally", "id": "t", "fields": {"owner": "ada"}}`); status != http.StatusCreated {
		t.Fatalf("start on b: %d %s", status, said)
	}
	for i, note := range []string{"x", "y", "z"} {
		target := a
		if i%2 == 1 {
			target = b
		}
		if status, said := call(t, target.d, target.server, "POST", "/api/instances/t/inboxes/notes", `{"message": "`+note+`", "key": "k`+note+`"}`); status != http.StatusOK {
			t.Fatalf("send %s: %d %s", note, status, said)
		}
	}
	eventually(t, "the three notes taken, on either daemon", func() bool {
		taken := 0
		for _, note := range []string{"x", "y", "z"} {
			if a.out.has("v1 "+note) || b.out.has("v1 "+note) {
				taken++
			}
		}
		return taken == 3
	})
	call(t, a.d, a.server, "POST", "/api/instances/t/inboxes/notes", `{"message": "stop", "key": "kstop"}`)
	eventually(t, "t to end", func() bool {
		_, said := call(t, b.d, b.server, "GET", "/api/instances/t", "")
		return strings.Contains(said, `"done"`) && strings.Contains(said, `ada 3`)
	})
	for _, note := range []string{"x", "y", "z"} {
		if a.out.has("v1 "+note) && b.out.has("v1 "+note) {
			t.Errorf("note %s taken by both daemons", note)
		}
	}

	body, _ = json.Marshal(map[string]any{"programs": []*flowd.Package{packageOf(t, "tally", tally(2))}})
	if status, said := call(t, b.d, b.server, "POST", "/api/deploy", string(body)); status != http.StatusOK {
		t.Fatalf("deploy to b: %d %s", status, said)
	}
	eventually(t, "a to serve the version b deployed", func() bool { return a.serving(t, "tally") == 2 && b.serving(t, "tally") == 2 })

	if status, said := call(t, a.d, a.server, "POST", "/api/undeploy", `{"program": "tally"}`); status != http.StatusOK {
		t.Fatalf("undeploy on a: %d %s", status, said)
	}
	eventually(t, "b to take tally away too", func() bool { return !b.serves(t, "Tally") })
}
