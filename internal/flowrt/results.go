package flowrt

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/qoqiflow/qoqi/internal/vm"
)

// Listed is what a step returned, as the store keeps it under its key.
type Listed struct {
	Key    string          `json:"key"`
	Result json.RawMessage `json:"result"`
	Undos  []vm.Undo       `json:"undos,omitempty"`
}

// Lister is a store that can list what it kept under the keys that start
// with a prefix: the steps of one instance are those of
// "name/birth/". The pages of the daemon show them.
type Lister interface {
	Results(prefix string) []Listed
}

// Prefix is what every key of an instance starts with.
func Prefix(inst *Instance) string {
	return inst.ID + "/" + inst.Birth + "/"
}

func (m *Memory) Results(prefix string) []Listed {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Listed
	for key, k := range m.results {
		if strings.HasPrefix(key, prefix) {
			out = append(out, Listed{Key: key, Result: append(json.RawMessage(nil), k.Data...), Undos: append([]vm.Undo(nil), k.Undos...)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func (s *SQL) Results(prefix string) []Listed {
	// The keys that start with prefix are those between it and the
	// greatest text that does
	rows, err := s.db.Query(s.q(`SELECT key, result, undo FROM steps WHERE key >= ? AND key < ? ORDER BY key`), prefix, prefix+greatest)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Listed
	for rows.Next() {
		var key, data, undo string
		if rows.Scan(&key, &data, &undo) != nil {
			continue
		}
		k := Listed{Key: key, Result: json.RawMessage(data)}
		if undo != "" {
			_ = json.Unmarshal([]byte(undo), &k.Undos)
		}
		out = append(out, k)
	}
	return out
}
