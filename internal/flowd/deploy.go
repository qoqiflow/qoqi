package flowd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/qoqiflow/qoqi/internal/ast"
	"github.com/qoqiflow/qoqi/internal/flowrt"
	"github.com/qoqiflow/qoqi/internal/flows"
	"github.com/qoqiflow/qoqi/internal/project"
	"github.com/qoqiflow/qoqi/internal/vm"
)

// Deploying: a daemon runs without stopping, and a program comes to it,
// or a new version of one, as a package. The version is checked against
// the one served before, from the lock the store keeps: a change of a
// stage no migration answers is refused while an instance is there. A
// version accepted is the newest at once: what runs ends its turn on the
// version before, and each instance moves at its next stage.

// Package is a program as it is deployed: compiled where it is written,
// served by a daemon that may run elsewhere.
type Package struct {
	Name       string             `json:"name"`
	Bytecode   []byte             `json:"bytecode"`
	Flows      []string           `json:"flows"`
	Migrations []flowrt.Migration `json:"migrations,omitempty"`
	Schema     *Schema            `json:"schema,omitempty"`

	// Sources are the files it was compiled from, for the pages
	Sources map[string]string `json:"sources,omitempty"`

	// Queues are the queues its project declares, as qoqi.json writes
	// them: "env:BILLING_URL" is read from the daemon's environment
	Queues map[string]project.QueueConfig `json:"queues,omitempty"`
}

// Report is what a deploy did, or would do.
type Report struct {
	Seq     int64  `json:"seq,omitempty"` // the deploy, in the history of the store
	Program string `json:"program"`
	Version string `json:"version"`
	Before  string `json:"before,omitempty"` // the version it replaces
	New     bool   `json:"new,omitempty"`    // a program the daemon did not serve
	Same    bool   `json:"same,omitempty"`   // the version served already

	// Changes are what this version changed of the stages of the one
	// before; Said, the steps versions still alive may send
	Changes []string `json:"changes,omitempty"`
	Said    []string `json:"said,omitempty"`

	// Moving is how many instances alive run another version: they move
	// to this one at their next stage
	Moving int `json:"moving"`

	// Lock is what the version leaves, to be kept with its source
	Lock *flowrt.Lock `json:"lock"`
}

// Refused is a version a daemon will not serve, and why.
type Refused struct {
	Program string
	Why     []string
}

func (r *Refused) Error() string {
	return fmt.Sprintf("this version of %s is refused:\n  %s", r.Program, strings.Join(r.Why, "\n  "))
}

// Deploy checks a version of a program against what the daemon serves,
// and, unless dry, serves it: in place of the version before, or beside
// the programs served.
func (d *Daemon) Deploy(pkg *Package, dry bool) (*Report, error) {
	d.deploying.Lock()
	defer d.deploying.Unlock()

	chunk, err := vm.DeserializeChunk(pkg.Bytecode)
	if err != nil {
		return nil, &Refused{Program: pkg.Name, Why: []string{"the bytecode cannot be read: " + err.Error()}}
	}
	if pkg.Name == "" || len(pkg.Flows) == 0 {
		return nil, &Refused{Program: pkg.Name, Why: []string{"a program deployed has a name and a flow"}}
	}
	version := vm.Fingerprint(chunk)
	report := &Report{Program: pkg.Name, Version: version}

	programs, _, _ := d.snapshot()
	var served *Program
	for i := range programs {
		if programs[i].Name == pkg.Name {
			served = &programs[i]
		}
	}
	// A flow belongs to one program
	for _, p := range programs {
		if p.Name == pkg.Name {
			continue
		}
		for _, flow := range p.Flows {
			for _, mine := range pkg.Flows {
				if flow == mine {
					return nil, &Refused{Program: pkg.Name, Why: []string{fmt.Sprintf("%s has a flow %s already: a flow belongs to one program", p.Name, flow)}}
				}
			}
		}
	}

	var store flowrt.Store
	if served != nil {
		store = served.Runtime.Store
		report.Before = vm.Fingerprint(served.Runtime.Newest())
		report.Same = report.Before == version
	} else {
		if d.Open == nil {
			return nil, &Refused{Program: pkg.Name, Why: []string{"this daemon serves the programs it was made with, and no other"}}
		}
		if store, err = d.Open(pkg.Name); err != nil {
			return nil, err
		}
		report.New = true
	}

	// The version before, as the store keeps its lock
	last := d.lastLock(pkg.Name)
	alive := func(match func(*flowrt.Instance) bool) int {
		n := 0
		for _, inst := range store.List("", "") {
			if inst.Status != flowrt.Done && inst.Status != flowrt.Failed && match(inst) {
				n++
			}
		}
		return n
	}
	shapes := flowrt.ShapesOf(chunk, pkg.Flows)
	if last != nil {
		changes := flowrt.Changes(last.Flows, shapes)
		for _, change := range changes {
			report.Changes = append(report.Changes, change.String())
		}
		// A change no migration answers leaves the instances there behind:
		// refused while any is alive
		var blocking []string
		for _, left := range flowrt.Unanswered(changes, last.Flows, shapes, pkg.Migrations) {
			n := alive(func(inst *flowrt.Instance) bool { return inst.Flow == left.Flow })
			if n > 0 {
				blocking = append(blocking, fmt.Sprintf("%s (%d instances of %s alive)", left, n, left.Flow))
			}
		}
		if len(blocking) > 0 {
			blocking = append(blocking, "write a migrate fn for each, or let the instances end first")
			return nil, &Refused{Program: pkg.Name, Why: blocking}
		}
	}
	steps := flowrt.StepsOf(chunk)
	before, said := flowrt.StillSent(last, version, steps, func(v string) int {
		return alive(func(inst *flowrt.Instance) bool { return inst.Version == v })
	})
	report.Said = said
	report.Lock = &flowrt.Lock{Format: flowrt.LockFormat, Version: version, Flows: shapes, Steps: steps, Before: before}
	report.Moving = alive(func(inst *flowrt.Instance) bool { return inst.Version != "" && inst.Version != version })

	queues, err := queuesOf(pkg)
	if queues != nil {
		queues.Log = func(format string, args ...any) { fmt.Fprintf(d.Log, "qoqi: "+pkg.Name+": "+format+"\n", args...) }
	}
	if err != nil {
		return nil, &Refused{Program: pkg.Name, Why: []string{err.Error()}}
	}
	if dry {
		return report, nil
	}

	var boundary flowrt.Boundary
	var sends vm.Queues
	if queues != nil {
		sends = queues
		// What crosses to the application is in the form of the boundary
		boundary = Boundary{Schema: pkg.Schema}
	}
	if served != nil {
		served.Runtime.Deploy(chunk, sends, boundary)
	} else {
		rt := flowrt.New(chunk, store, d.Out)
		rt.Workers = d.Workers
		rt.Queues, rt.Boundary = sends, boundary
		d.observe(rt)
		rt.Finished = d.poke
		served = &Program{Name: pkg.Name, Runtime: rt}
	}

	d.pmu.Lock()
	served.Flows = append([]string(nil), pkg.Flows...)
	kept := false
	for i := range d.Programs {
		if d.Programs[i].Name == pkg.Name {
			d.Programs[i] = *served
			kept = true
		}
	}
	if !kept {
		d.Programs = append(d.Programs, *served)
	}
	if d.schemas == nil {
		d.schemas, d.sources = map[string]*Schema{}, map[string]map[string]string{}
	}
	if pkg.Schema != nil {
		schema := *pkg.Schema
		schema.Files = nil
		for file := range pkg.Sources {
			schema.Files = append(schema.Files, file)
		}
		sort.Strings(schema.Files)
		d.schemas[pkg.Name] = &schema
	}
	d.sources[pkg.Name] = pkg.Sources
	d.Flows = nil
	var schemas []*Schema
	for _, p := range d.Programs {
		d.Flows = append(d.Flows, p.Flows...)
		if s := d.schemas[p.Name]; s != nil {
			schemas = append(schemas, s)
		}
	}
	if len(schemas) > 0 {
		d.Schema = MergeSchemas("", schemas)
	}
	d.pmu.Unlock()

	if d.Deployments != nil {
		// Kept after the deploys before, unless it is the version deployed
		// last: a daemon started again serves it, and deploys nothing
		if last := d.lastDeployed(pkg.Name); last != nil && last.Version == version {
			report.Seq = last.Seq
		} else {
			data, _ := json.Marshal(pkg)
			lock, _ := json.Marshal(report.Lock)
			said, _ := json.Marshal(report)
			seq, err := d.Deployments.PutDeployment(flowrt.Deployed{Name: pkg.Name, Version: version, At: time.Now().UnixMilli(), Package: data, Lock: lock, Report: said})
			if err != nil {
				return nil, err
			}
			report.Seq = seq
		}
	}
	// The pages ask again what the program is, and its versions
	d.hub.send(Event{Kind: "deploy", At: stamp(), Text: pkg.Name})
	switch {
	case report.New:
		fmt.Fprintf(d.Log, "qoqi: %s deployed, version %s: %s\n", pkg.Name, shortVersion(version), strings.Join(pkg.Flows, ", "))
	case report.Same:
		fmt.Fprintf(d.Log, "qoqi: %s is version %s already\n", pkg.Name, shortVersion(version))
	default:
		fmt.Fprintf(d.Log, "qoqi: %s deployed, version %s; %d instances move to it at their next stage\n", pkg.Name, shortVersion(version), report.Moving)
	}
	d.poke()
	return report, nil
}

// lastDeployed is the last deploy of a program the store keeps, or nil.
func (d *Daemon) lastDeployed(name string) *flowrt.Deployed {
	if d.Deployments == nil {
		return nil
	}
	deployed, err := d.Deployments.Deployments()
	if err != nil {
		return nil
	}
	for _, dep := range deployed {
		if dep.Name == name {
			return &dep
		}
	}
	return nil
}

// lastLock is the lock the last deploy of a program left, or nil.
func (d *Daemon) lastLock(name string) *flowrt.Lock {
	if dep := d.lastDeployed(name); dep != nil {
		var lock flowrt.Lock
		if json.Unmarshal(dep.Lock, &lock) == nil {
			return &lock
		}
	}
	return nil
}

// Restore serves again what was deployed to the store, when the daemon
// starts: a daemon started with no program serves what it served.
func (d *Daemon) Restore() error {
	if d.Deployments == nil {
		return nil
	}
	deployed, err := d.Deployments.Deployments()
	if err != nil {
		return err
	}
	for _, dep := range deployed {
		var pkg Package
		if err := json.Unmarshal(dep.Package, &pkg); err != nil {
			return fmt.Errorf("what was deployed of %s cannot be read: %v", dep.Name, err)
		}
		if _, err := d.Deploy(&pkg, false); err != nil {
			return err
		}
	}
	return nil
}

// queuesOf are where the queues of a package are served, read from the
// daemon's environment where the package says "env:NAME".
func queuesOf(pkg *Package) (*flowrt.HTTPQueues, error) {
	if len(pkg.Queues) == 0 {
		return nil, nil
	}
	cfg := &project.Config{Queues: pkg.Queues, Path: "the qoqi.json of " + pkg.Name}
	queues := map[string]flowrt.Queue{}
	for _, name := range cfg.QueueNames() {
		url, secret, err := cfg.Queue(name)
		if err != nil {
			return nil, err
		}
		queues[name] = flowrt.Queue{URL: url, Secret: secret, Concurrency: pkg.Queues[name].Concurrency}
	}
	return flowrt.NewHTTPQueues(queues), nil
}

// DeployAll deploys programs together: each checked first, the flows of
// one against the others' too, then all served; none is served when one
// is refused.
func (d *Daemon) DeployAll(pkgs []*Package, dry bool) ([]*Report, error) {
	owner := map[string]string{}
	for _, pkg := range pkgs {
		for _, flow := range pkg.Flows {
			if other, taken := owner[flow]; taken && other != pkg.Name {
				return nil, &Refused{Program: pkg.Name, Why: []string{fmt.Sprintf("%s and %s both have a flow %s: a flow belongs to one program", other, pkg.Name, flow)}}
			}
			owner[flow] = pkg.Name
		}
	}
	var reports []*Report
	for _, pkg := range pkgs {
		report, err := d.Deploy(pkg, true)
		if err != nil {
			return nil, err
		}
		reports = append(reports, report)
	}
	if dry {
		return reports, nil
	}
	reports = nil
	for _, pkg := range pkgs {
		report, err := d.Deploy(pkg, false)
		if err != nil {
			return nil, err
		}
		reports = append(reports, report)
	}
	return reports, nil
}

// deploy is POST /api/deploy: DeployAll.
func (d *Daemon) deploy(w http.ResponseWriter, r *http.Request) {
	var asked struct {
		Programs []*Package `json:"programs"`
		Dry      bool       `json:"dry"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<20)).Decode(&asked); err != nil || len(asked.Programs) == 0 {
		fail(w, http.StatusBadRequest, "request", "the request is JSON: {\"programs\": [...], \"dry\": false}, as qoqi deploy writes it")
		return
	}
	reports, err := d.DeployAll(asked.Programs, asked.Dry)
	if err != nil {
		refused(w, err)
		return
	}
	answer(w, http.StatusOK, map[string]any{"reports": reports, "dry": asked.Dry})
}

func refused(w http.ResponseWriter, err error) {
	if r, ok := err.(*Refused); ok {
		fail(w, http.StatusConflict, "refused", r.Error())
		return
	}
	fail(w, http.StatusInternalServerError, "deploy", err.Error())
}

// shortVersion is a version as people read it: its first letters.
func shortVersion(version string) string {
	version = strings.TrimPrefix(version, "sha256:")
	if len(version) > 8 {
		return version[:8]
	}
	return version
}

// PackageOf is what is deployed of a program compiled: its flows, its
// bytecode, its migrations, what the pages know of it, and its files.
func PackageOf(name, file string, prog *ast.Program, chunk *vm.Chunk, sources map[string]string) (*Package, error) {
	bytecode, err := vm.SerializeChunk(chunk)
	if err != nil {
		return nil, err
	}
	var files []string
	for f := range sources {
		files = append(files, f)
	}
	sort.Strings(files)
	return &Package{
		Name:       name,
		Bytecode:   bytecode,
		Flows:      flows.FlowsOf(prog),
		Migrations: flows.MigrationsOf(prog),
		Schema:     SchemaOf(file, prog, files),
		Sources:    sources,
	}, nil
}

// Undeploy takes a program away: the daemon serves it no more, nor does
// one started on the store. Refused while an instance of it is alive, or
// while one runs: they are ended first, by cancel or end. What its
// instances were is kept in the store, and its versions in the history.
func (d *Daemon) Undeploy(name string) error {
	d.deploying.Lock()
	defer d.deploying.Unlock()
	var served *Program
	for _, p := range d.programs() {
		if p.Name == name {
			p := p
			served = &p
		}
	}
	if served == nil {
		return &Refused{Program: name, Why: []string{"this daemon serves no program " + name}}
	}
	var alive []string
	for _, inst := range served.Runtime.Store.List("", "") {
		if inst.Status != flowrt.Done && inst.Status != flowrt.Failed {
			alive = append(alive, inst.ID)
		}
	}
	if len(alive) > 0 || served.Runtime.Busy() > 0 {
		sort.Strings(alive)
		if len(alive) > 5 {
			alive = append(alive[:5], fmt.Sprintf("and %d more", len(alive)-5))
		}
		return &Refused{Program: name, Why: []string{
			fmt.Sprintf("%d instances of it are alive: %s", len(alive), strings.Join(alive, ", ")),
			"end each first, qoqi cancel or qoqi end, then take it away",
		}}
	}
	if d.Deployments != nil {
		report, _ := json.Marshal(map[string]any{"removed": true})
		if _, err := d.Deployments.PutDeployment(flowrt.Deployed{Name: name, At: time.Now().UnixMilli(), Package: []byte("{}"), Lock: []byte("{}"), Report: report}); err != nil {
			return err
		}
	}
	d.forget(name)
	return nil
}

// forget serves a program no more.
func (d *Daemon) forget(name string) {
	d.pmu.Lock()
	kept := d.Programs[:0]
	d.Flows = nil
	for _, p := range d.Programs {
		if p.Name != name {
			kept = append(kept, p)
			d.Flows = append(d.Flows, p.Flows...)
		}
	}
	d.Programs = kept
	delete(d.schemas, name)
	delete(d.sources, name)
	var schemas []*Schema
	for _, p := range d.Programs {
		if s := d.schemas[p.Name]; s != nil {
			schemas = append(schemas, s)
		}
	}
	d.Schema = nil
	if len(schemas) > 0 {
		d.Schema = MergeSchemas("", schemas)
	}
	d.pmu.Unlock()
	d.hub.send(Event{Kind: "deploy", At: stamp(), Text: name})
	fmt.Fprintf(d.Log, "qoqi: %s taken away\n", name)
}

// Follow serves what another daemon sharing the store deployed since it
// last looked: a new version of a program, a program new, one taken
// away. Each daemon deploys to the store, and the others follow.
func (d *Daemon) Follow() error {
	if d.Deployments == nil || d.Open == nil {
		return nil
	}
	history, err := d.Deployments.History("", false)
	if err != nil || len(history) == 0 {
		return err
	}
	d.mu.Lock()
	seen := d.followed
	d.mu.Unlock()
	if history[len(history)-1].Seq <= seen {
		return nil
	}
	last := map[string]flowrt.Deployed{}
	var names []string
	for _, dep := range history {
		if _, ok := last[dep.Name]; !ok {
			names = append(names, dep.Name)
		}
		last[dep.Name] = dep
	}
	served := map[string]string{}
	for _, p := range d.programs() {
		served[p.Name] = vm.Fingerprint(p.Runtime.Newest())
	}
	for _, name := range names {
		dep := last[name]
		version, serving := served[name]
		switch {
		case dep.Version == "" && serving:
			d.deploying.Lock()
			d.forget(name)
			d.deploying.Unlock()
		case dep.Version != "" && dep.Version != version:
			full, ok := d.Deployments.Deploy(dep.Seq)
			if !ok {
				continue
			}
			var pkg Package
			if err := json.Unmarshal(full.Package, &pkg); err != nil {
				return fmt.Errorf("what was deployed of %s cannot be read: %v", name, err)
			}
			if _, err := d.Deploy(&pkg, false); err != nil {
				fmt.Fprintf(d.Log, "qoqi: %s, deployed by another daemon, is not served here: %v\n", name, err)
			}
		}
	}
	d.mu.Lock()
	d.followed = history[len(history)-1].Seq
	d.mu.Unlock()
	return nil
}

// undeploy is POST /api/undeploy: {"program": "shop"}.
func (d *Daemon) undeploy(w http.ResponseWriter, r *http.Request) {
	var asked struct {
		Program string `json:"program"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&asked); err != nil || asked.Program == "" {
		fail(w, http.StatusBadRequest, "request", "the request is JSON: {\"program\": \"shop\"}")
		return
	}
	if err := d.Undeploy(asked.Program); err != nil {
		refused(w, err)
		return
	}
	answer(w, http.StatusOK, map[string]string{"removed": asked.Program})
}
