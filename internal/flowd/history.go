package flowd

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/qoqiflow/qoqi/internal/flowrt"
	"github.com/qoqiflow/qoqi/internal/vm"
)

// The versions deployed, kept one after another: what each changed, what
// still runs on it, and what changed of the source from one to another.

// DeployView is a deploy as the API shows it.
type DeployView struct {
	Seq     int64    `json:"seq"`
	Program string   `json:"program"`
	Version string   `json:"version"`
	At      string   `json:"at"`
	Flows   []string `json:"flows"`
	Files   []string `json:"files,omitempty"`
	Changes []string `json:"changes,omitempty"` // what it changed of the stages of the version before
	Moving  int      `json:"moving"`            // instances that were to move to it when it was deployed

	// Alive is how many instances alive run it now; Serving, whether it is
	// the version a new instance starts on
	Alive   int  `json:"alive"`
	Serving bool `json:"serving"`
	Removed bool `json:"removed,omitempty"` // the program taken away, here

	Sources map[string]string `json:"sources,omitempty"`
}

// Diff is what changed from one deploy to another: of the source, file
// by file, and of the shapes of the stages.
type Diff struct {
	From   *DeployView `json:"from,omitempty"`
	To     *DeployView `json:"to"`
	Files  []FileDiff  `json:"files"`
	Stages []string    `json:"stages,omitempty"`
}

// view is a deploy for the API, with its sources if asked for.
func (d *Daemon) view(dep flowrt.Deployed, sources bool) *DeployView {
	v := &DeployView{Seq: dep.Seq, Program: dep.Name, Version: dep.Version, At: time.UnixMilli(dep.At).UTC().Format(time.RFC3339Nano)}
	var report Report
	if json.Unmarshal(dep.Report, &report) == nil {
		v.Changes, v.Moving = report.Changes, report.Moving
	}
	v.Removed = dep.Version == ""
	if v.Removed {
		return v
	}
	var pkg Package
	if len(dep.Package) > 0 && json.Unmarshal(dep.Package, &pkg) == nil {
		v.Flows = pkg.Flows
		for f := range pkg.Sources {
			v.Files = append(v.Files, f)
		}
		if sources {
			v.Sources = pkg.Sources
		}
	}
	for _, p := range d.programs() {
		if p.Name != dep.Name {
			continue
		}
		v.Serving = fingerprintOf(p) == dep.Version
		for _, inst := range p.Runtime.Store.List("", "") {
			if inst.Version == dep.Version && inst.Status != flowrt.Done && inst.Status != flowrt.Failed {
				v.Alive++
			}
		}
	}
	return v
}

// deployments is GET /api/deployments: every deploy, of one program with
// ?program=, the last first.
func (d *Daemon) deployments(w http.ResponseWriter, r *http.Request) {
	if d.Deployments == nil {
		answer(w, http.StatusOK, []DeployView{})
		return
	}
	history, err := d.Deployments.History(r.URL.Query().Get("program"), true)
	if err != nil {
		fail(w, http.StatusInternalServerError, "store", err.Error())
		return
	}
	views := []*DeployView{}
	for i := len(history) - 1; i >= 0; i-- {
		views = append(views, d.view(history[i], false))
	}
	answer(w, http.StatusOK, views)
}

// deployment is GET /api/deployments/{seq}: one deploy, with its sources.
func (d *Daemon) deployment(w http.ResponseWriter, r *http.Request) {
	dep, ok := d.deployOf(w, r.PathValue("seq"))
	if !ok {
		return
	}
	answer(w, http.StatusOK, d.view(dep, true))
}

// diff is GET /api/deployments/{seq}/diff: what changed from the deploy
// of the program before it, or from ?from=.
func (d *Daemon) diff(w http.ResponseWriter, r *http.Request) {
	to, ok := d.deployOf(w, r.PathValue("seq"))
	if !ok {
		return
	}
	var from *flowrt.Deployed
	if f := r.URL.Query().Get("from"); f != "" {
		dep, ok := d.deployOf(w, f)
		if !ok {
			return
		}
		from = &dep
	} else {
		history, _ := d.Deployments.History(to.Name, false)
		for _, dep := range history {
			if dep.Seq < to.Seq {
				before := dep
				from = &before
			}
		}
		if from != nil {
			if full, ok := d.Deployments.Deploy(from.Seq); ok {
				from = &full
			}
		}
	}
	out := Diff{To: d.view(to, false)}
	newer, older := sourcesOf(to), map[string]string{}
	var oldLock, newLock flowrt.Lock
	json.Unmarshal(to.Lock, &newLock)
	if from != nil {
		out.From = d.view(*from, false)
		older = sourcesOf(*from)
		json.Unmarshal(from.Lock, &oldLock)
		for _, change := range flowrt.Changes(oldLock.Flows, newLock.Flows) {
			out.Stages = append(out.Stages, change.String())
		}
	}
	out.Files = DiffSources(older, newer)
	answer(w, http.StatusOK, out)
}

func (d *Daemon) deployOf(w http.ResponseWriter, seq string) (flowrt.Deployed, bool) {
	n, err := strconv.ParseInt(seq, 10, 64)
	if err != nil || d.Deployments == nil {
		fail(w, http.StatusNotFound, "notFound", "there is no deploy "+seq)
		return flowrt.Deployed{}, false
	}
	dep, ok := d.Deployments.Deploy(n)
	if !ok {
		fail(w, http.StatusNotFound, "notFound", "there is no deploy "+seq)
	}
	return dep, ok
}

func sourcesOf(dep flowrt.Deployed) map[string]string {
	var pkg Package
	json.Unmarshal(dep.Package, &pkg)
	if pkg.Sources == nil {
		return map[string]string{}
	}
	return pkg.Sources
}

// fingerprintOf is the version a program serves now.
func fingerprintOf(p Program) string {
	return vm.Fingerprint(p.Runtime.Newest())
}
