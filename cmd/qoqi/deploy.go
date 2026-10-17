package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/qoqiflow/qoqi/internal/compiler"
	"github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/flowd"
	"github.com/qoqiflow/qoqi/internal/flowrt"
	"github.com/qoqiflow/qoqi/internal/flows"
	"github.com/qoqiflow/qoqi/internal/project"
)

// What qoqi deploy sends, and qoqi serve gives itself: each
// program compiled here, as a package the daemon serves.

// deployable is a program to deploy: its file, and its package.
type deployable struct {
	file string
	pkg  *flowd.Package
}

// packagesOf compiles what a path names: a file is one program, a folder
// every program in it that has flows. What does not compile is said, and
// nothing is given.
func packagesOf(path string) ([]deployable, error) {
	var files []string
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		all, _ := filepath.Glob(filepath.Join(path, "*.qi"))
		sort.Strings(all)
		for _, file := range all {
			if !strings.HasSuffix(file, "_test.qi") {
				files = append(files, file)
			}
		}
	} else {
		files = []string{sourceFile(path, flowUsage)}
	}
	var out []deployable
	for _, file := range files {
		pkg, err := packageOf(file)
		if err != nil {
			return nil, err
		}
		if pkg != nil {
			out = append(out, deployable{file: file, pkg: pkg})
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s has no program with a flow: a flow is a struct that implements Flow<T> of std.flow", path)
	}
	return out, nil
}

// packageOf compiles one program into what is deployed of it; nil for a
// program with no flow.
func packageOf(file string) (*flowd.Package, error) {
	errs := errors.NewHandler()
	result, err := compiler.CompileFileOptions(file, projectOptions(file), errs)
	if err != nil {
		return nil, err
	}
	if errs.HasErrors() {
		var said []string
		for _, d := range errs.Diagnostics {
			said = append(said, d.String())
		}
		return nil, fmt.Errorf("%s", strings.Join(said, "\n"))
	}
	if len(flows.FlowsOf(result.AST)) == 0 {
		return nil, nil
	}
	sources := map[string]string{}
	for _, f := range programFiles(result.Chunk) {
		if data, err := os.ReadFile(f); err == nil {
			sources[f] = string(data)
		}
	}
	pkg, err := flowd.PackageOf(strings.TrimSuffix(filepath.Base(file), ".qi"), file, result.AST, result.Chunk, sources)
	if err != nil {
		return nil, err
	}
	cfg, err := project.Find(filepath.Dir(file))
	if err != nil {
		return nil, err
	}
	if cfg != nil && len(cfg.Queues) > 0 {
		pkg.Queues = cfg.Queues
	}
	return pkg, nil
}

// lockPath is where the lock of a program is kept with its source:
// shop.flow.lock next to shop.qi.
func lockPath(file string) string {
	return strings.TrimSuffix(file, filepath.Ext(file)) + ".flow.lock"
}

// said prints what a deploy did.
func said(report *flowd.Report, dry bool) {
	verb := "deployed"
	if dry {
		verb = "would be deployed"
	}
	version := shortVersion(report.Version)
	switch {
	case report.New:
		fmt.Printf("%s %s, version %s\n", report.Program, verb, version)
	case report.Same:
		fmt.Printf("%s is version %s already\n", report.Program, version)
	default:
		fmt.Printf("%s %s, version %s in place of %s: %d instances move to it at their next stage\n",
			report.Program, verb, version, shortVersion(report.Before), report.Moving)
	}
	for _, change := range report.Changes {
		fmt.Printf("  %s\n", change)
	}
	for _, line := range report.Said {
		fmt.Printf("  %s\n", line)
	}
}

// flowDeploy is qoqi deploy: the programs a path names, compiled
// here, sent to the daemon of the store, and their locks written next to
// them.
func flowDeploy(store, path string, dry bool) {
	programs, err := packagesOf(path)
	if err != nil {
		fatal(err)
	}
	var pkgs []*flowd.Package
	for _, p := range programs {
		pkgs = append(pkgs, p.pkg)
	}
	body, err := json.Marshal(map[string]any{"programs": pkgs, "dry": dry})
	if err != nil {
		fatal(err)
	}
	status, data := flowRequest(store, "POST", "/api/deploy", body)
	if status >= 400 {
		flowRefused(status, data)
	}
	var answer struct {
		Reports []*flowd.Report `json:"reports"`
	}
	if err := json.Unmarshal(data, &answer); err != nil {
		fatal(err)
	}
	for i, report := range answer.Reports {
		said(report, dry)
		if !dry && i < len(programs) && report.Lock != nil {
			if err := flowrt.WriteLock(lockPath(programs[i].file), report.Lock); err != nil {
				fatal(err)
			}
		}
	}
}

// deployHere deploys the programs a path names to a daemon in this
// process, their locks written next to them. A refusal is returned.
func deployHere(daemon *flowd.Daemon, path string) error {
	programs, err := packagesOf(path)
	if err != nil {
		return err
	}
	var pkgs []*flowd.Package
	for _, p := range programs {
		pkgs = append(pkgs, p.pkg)
	}
	reports, err := daemon.DeployAll(pkgs, false)
	if err != nil {
		return err
	}
	for i, report := range reports {
		for _, change := range report.Changes {
			fmt.Fprintf(os.Stderr, "qoqi: %s: %s\n", report.Program, change)
		}
		for _, line := range report.Said {
			fmt.Fprintf(os.Stderr, "qoqi: %s: %s\n", report.Program, line)
		}
		if err := flowrt.WriteLock(lockPath(programs[i].file), report.Lock); err != nil {
			return err
		}
	}
	return nil
}

// watch deploys a path again each time one of the files of its programs
// changes, until stop is closed: what does not compile, or is refused, is
// said, and the version before goes on.
func watch(daemon *flowd.Daemon, path string, stop <-chan struct{}) {
	stamps := func() map[string]time.Time {
		out := map[string]time.Time{}
		var files []string
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			files, _ = filepath.Glob(filepath.Join(path, "*.qi"))
		} else {
			files = []string{sourceFile(path, flowUsage)}
		}
		for _, f := range files {
			if info, err := os.Stat(f); err == nil {
				out[f] = info.ModTime()
			}
		}
		return out
	}
	last := stamps()
	tick := time.NewTicker(300 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
		}
		now := stamps()
		changed := len(now) != len(last)
		for f, t := range now {
			if !last[f].Equal(t) {
				changed = true
			}
		}
		if !changed {
			continue
		}
		last = now
		if err := deployHere(daemon, path); err != nil {
			fmt.Fprintf(os.Stderr, "qoqi: the version before goes on: %v\n", err)
		}
	}
}

// flowVersions is qoqi versions: the deploys kept in the store, the
// last first, with what still runs on each.
func flowVersions(store, program string) {
	path := "/api/deployments"
	if program != "" {
		path += "?program=" + program
	}
	status, data := flowRequest(store, "GET", path, nil)
	if status >= 400 {
		flowRefused(status, data)
	}
	var views []flowd.DeployView
	if err := json.Unmarshal(data, &views); err != nil {
		fatal(err)
	}
	if len(views) == 0 {
		fmt.Println("nothing deployed yet: qoqi deploy <program>")
		return
	}
	rows := [][]string{}
	for _, v := range views {
		at, _ := time.Parse(time.RFC3339Nano, v.At)
		if v.Removed {
			rows = append(rows, []string{fmt.Sprintf("#%d", v.Seq), v.Program, "removed", at.Local().Format("2006-01-02 15:04:05"), "", "", ""})
			continue
		}
		serving := ""
		if v.Serving {
			serving = "serving"
		}
		alive := ""
		if v.Alive > 0 {
			alive = fmt.Sprintf("%d alive", v.Alive)
		}
		changes := ""
		if len(v.Changes) > 0 {
			changes = fmt.Sprintf("%d stages changed", len(v.Changes))
			if len(v.Changes) == 1 {
				changes = "1 stage changed"
			}
		}
		rows = append(rows, []string{fmt.Sprintf("#%d", v.Seq), v.Program, shortVersion(v.Version), at.Local().Format("2006-01-02 15:04:05"), serving, alive, changes})
	}
	printColumns(rows)
}

// flowDiff is qoqi diff: what changed of the source from one deploy
// to another, as diff -u says it. A deploy is #seq, or a program, whose
// last deploy it is; from the deploy before it, unless --from says.
func flowDiff(store, which, from string) {
	seq := strings.TrimPrefix(which, "#")
	if _, err := fmt.Sscanf(seq, "%d", new(int)); err != nil {
		// A program: its last deploy
		status, data := flowRequest(store, "GET", "/api/deployments?program="+which, nil)
		if status >= 400 {
			flowRefused(status, data)
		}
		var views []flowd.DeployView
		json.Unmarshal(data, &views)
		if len(views) == 0 {
			fatal(fmt.Errorf("%s has not been deployed", which))
		}
		seq = fmt.Sprint(views[0].Seq)
	}
	path := "/api/deployments/" + seq + "/diff"
	if from != "" {
		path += "?from=" + strings.TrimPrefix(from, "#")
	}
	status, data := flowRequest(store, "GET", path, nil)
	if status >= 400 {
		flowRefused(status, data)
	}
	var diff flowd.Diff
	if err := json.Unmarshal(data, &diff); err != nil {
		fatal(err)
	}
	name := func(v *flowd.DeployView) string {
		return fmt.Sprintf("%s #%d, version %s", v.Program, v.Seq, shortVersion(v.Version))
	}
	if diff.From != nil {
		fmt.Printf("from %s\n", name(diff.From))
	} else {
		fmt.Println("from nothing: the first deploy")
	}
	fmt.Printf("to   %s\n", name(diff.To))
	for _, stage := range diff.Stages {
		fmt.Printf("  %s\n", stage)
	}
	for _, f := range diff.Files {
		if f.Status == "same" {
			continue
		}
		fmt.Printf("\n--- %s\n+++ %s  (%s, +%d -%d)\n", f.File, f.File, f.Status, f.Added, f.Gone)
		for _, h := range f.Hunks {
			fmt.Printf("@@ -%d,%d +%d,%d @@\n", h.OldStart, h.OldLines, h.NewStart, h.NewLines)
			for _, l := range h.Lines {
				fmt.Println(l.Kind + l.Text)
			}
		}
	}
}
