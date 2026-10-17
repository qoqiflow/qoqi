package flowd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/qoqiflow/qoqi/internal/flowrt"
)

// What happens, as it happens: GET /api/events is a stream of server-sent
// events, one JSON object each, that the pages read to stay current
// without asking again and again.
//
//	{"kind": "instance", "instance": {...}}           started, woken, asleep, stopped, ended
//	{"kind": "step", "id": "order:1", "step": "book", "key": "order:1/fecc/0:0:book#1"}
//	{"kind": "output", "id": "order:1", "text": "[vans] van-3 takes hold-1 to Lyon"}
//	{"kind": "deploy", "text": "shop"}                  a version of a program deployed
//
// The program's output is kept too, the last lines of it, each with the
// instance that printed it: GET /api/output.

// Event is one thing that happened.
type Event struct {
	Kind     string `json:"kind"`
	At       string `json:"at"`
	ID       string `json:"id,omitempty"`
	Instance *View  `json:"instance,omitempty"`
	Step     string `json:"step,omitempty"`
	Key      string `json:"key,omitempty"`
	Once     bool   `json:"once,omitempty"`
	Text     string `json:"text,omitempty"`
}

// Line is a line the program printed.
type Line struct {
	At   string `json:"at"`
	ID   string `json:"id,omitempty"` // the instance being run when it was printed
	Text string `json:"text"`
}

const keptLines = 2000

type hub struct {
	mu      sync.Mutex
	clients map[chan Event]bool
	seen    map[string]string // by instance, what it looked like last
	lines   []Line
	partial map[string][]byte // by instance, a line printed without its end yet
}

func newHub() *hub {
	return &hub{clients: map[chan Event]bool{}, seen: map[string]string{}, partial: map[string][]byte{}}
}

func stamp() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func (h *hub) send(e Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sendLocked(e)
}

func (h *hub) sendLocked(e Event) {
	for c := range h.clients {
		select {
		case c <- e:
		default:
			// A client that does not keep up misses events; it asks the
			// API again when it reconnects
		}
	}
}

func (h *hub) subscribe() chan Event {
	c := make(chan Event, 256)
	h.mu.Lock()
	h.clients[c] = true
	h.mu.Unlock()
	return c
}

func (h *hub) unsubscribe(c chan Event) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
}

// writer is where an instance prints: what it prints is kept line by
// line, with its name. Instances run side by side, each with its own.
func (h *hub) writer(id string) io.Writer { return printer{h, id} }

type printer struct {
	h  *hub
	id string
}

func (p printer) Write(b []byte) (int, error) {
	h := p.h
	h.mu.Lock()
	defer h.mu.Unlock()
	partial := append(h.partial[p.id], b...)
	for {
		i := bytes.IndexByte(partial, '\n')
		if i < 0 {
			break
		}
		h.line(p.id, string(partial[:i]))
		partial = partial[i+1:]
	}
	if len(partial) > 0 {
		h.partial[p.id] = partial
	} else {
		delete(h.partial, p.id)
	}
	return len(b), nil
}

// flush keeps a line an instance printed without its end, once it is no
// longer running: it is done printing, for now.
func (h *hub) flush(id string) {
	if partial, ok := h.partial[id]; ok {
		h.line(id, string(partial))
		delete(h.partial, id)
	}
}

func (h *hub) line(id, text string) {
	l := Line{At: stamp(), ID: id, Text: strings.TrimRight(text, "\r")}
	h.lines = append(h.lines, l)
	if len(h.lines) > keptLines {
		h.lines = append([]Line(nil), h.lines[len(h.lines)-keptLines:]...)
	}
	h.sendLocked(Event{Kind: "output", At: l.At, ID: l.ID, Text: l.Text})
}

func (h *hub) output(id string, limit int) []Line {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := []Line{}
	for _, l := range h.lines {
		if id == "" || l.ID == id {
			out = append(out, l)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

// observe sends an event for every instance that changed since it was
// last looked at.
func (h *hub) observe(store flowrt.Store) {
	instances := store.List("", "")
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, inst := range instances {
		if inst.Status != flowrt.Running {
			h.flush(inst.ID)
		}
		v := viewOf(inst)
		signature := fmt.Sprint(v.Status, v.WakeAt, v.Phase, v.Cancel, v.Stopped, len(v.Steps), len(v.Undos), len(v.Children), v.Awaits, len(inst.State))
		if h.seen[inst.ID] == signature {
			continue
		}
		h.seen[inst.ID] = signature
		h.sendLocked(Event{Kind: "instance", At: stamp(), ID: inst.ID, Instance: &v})
	}
}

// events streams what happens, until the client goes.
func (d *Daemon) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		fail(w, http.StatusInternalServerError, "stream", "this connection cannot stream")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	c := d.hub.subscribe()
	defer d.hub.unsubscribe(c)
	fmt.Fprint(w, ": the events of the daemon\n\n")
	flusher.Flush()
	alive := time.NewTicker(15 * time.Second)
	defer alive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-alive.C:
			fmt.Fprint(w, ": still here\n\n")
		case e := <-c:
			data, _ := json.Marshal(e)
			fmt.Fprintf(w, "data: %s\n\n", data)
		}
		flusher.Flush()
	}
}
