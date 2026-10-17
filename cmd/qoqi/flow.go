package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/qoqiflow/qoqi/internal/flowd"
	"github.com/qoqiflow/qoqi/internal/flowrt"
	"github.com/qoqiflow/qoqi/internal/project"
	"github.com/qoqiflow/qoqi/internal/sqldb"
	"github.com/qoqiflow/qoqi/internal/vm"
)

// flowCommands are the commands of flows: the daemon, and those that talk
// to it. They are the first level of qoqi: qoqi serve, qoqi deploy.
var flowCommands = []string{
	"serve", "deploy", "undeploy", "versions", "diff", "start", "ls", "show", "result",
	"send", "cancel", "resume", "answer", "end", "ui", "gen",
}

const flowUsage = `Usage:
  qoqi serve [program or folder] [--watch] [--store flows.db] [--listen 127.0.0.1:7070] [--workers N]
                              Run the flows deployed to the store, and those of a program
                              or of every program of a folder; --watch deploys again what
                              changes as it is saved. The store is a file of SQLite, or
                              a server: postgres://user:pass@host/db, mysql://user:pass@host/db
  qoqi deploy <program or folder> [--dry] [--store flows.db]
                              A new version to the daemon, served at once: what runs ends
                              its turn, and each instance moves at its next stage
  qoqi undeploy <program> [--store flows.db]
                              Take a program away, once none of its instances is alive
  qoqi versions [program] [--store flows.db]
                              The deploys kept, the last first, and what runs on each
  qoqi diff <seq or program> [--from seq] [--store flows.db]
                              What a deploy changed of the source, from the one before
  qoqi start <Flow> <name> [fields as JSON] [--store flows.db]
                              Start an instance: a name taken gives the one that has it
  qoqi ls [--flow Flow] [--status waiting|running|stopped|done|failed] [--json] [--store flows.db]
                              The instances, and what each is doing
  qoqi show <name> [--json] [--store flows.db]
                              An instance: where it is, what it was given, what its steps returned
  qoqi result <name> [--wait 30s] [--json] [--store flows.db]
                              What its run returned, waiting for it that long
  qoqi send <name> <inbox> <message as JSON> [--key evt-1] [--store flows.db]
                              A message for an inbox of an instance; a key sent before is one message
  qoqi cancel <name> [reason] [--store flows.db]
                              End it: what it gave flow.undo runs, the last first
  qoqi resume <name> [--go] [--store flows.db]
                              A stopped instance, taken up: what it would do, then --go to do it
  qoqi answer <name> <key> (<what it did, as JSON> | --nothing) [--store flows.db]
                              A step the application could not tell of, answered by a
                              person: then resume it
  qoqi end <name> [reason] [--store flows.db]
                              A stopped instance, ended as it is, with no more undos
  qoqi ui [--store flows.db]
                              Open the console of the daemon in a browser
  qoqi gen go <program> [--package steps] [--queue billing]
  qoqi gen php <program> --namespace 'App\Flow' --out src/Flow [--queue billing]
  qoqi gen ts <program> [--from @qoqi/flow] [--queue billing]
                              The types of the steps an application carries out, an
                              interface for each queue, and the handler that serves it`

// cmdFlow runs one of the commands of flows: args[0] is the command.
func cmdFlow(args []string) {
	for _, arg := range args[1:] {
		if arg == "--help" || arg == "-h" {
			fmt.Println(flowUsage)
			return
		}
	}
	options, rest := flowOptions(args[1:])
	store := options["store"]
	switch args[0] {
	case "serve":
		if len(rest) > 1 {
			fmt.Println(flowUsage)
			exit(1)
		}
		program := ""
		if len(rest) == 1 {
			program = rest[0]
		} else if cfg, err := project.Find("."); err == nil && cfg != nil && cfg.Entry != "" {
			// In a project, the project's flows
			program = "."
		}
		flowServe(program, store, options["listen"], options)
	case "undeploy":
		if len(rest) != 1 {
			fmt.Println(flowUsage)
			exit(1)
		}
		body, _ := json.Marshal(map[string]string{"program": rest[0]})
		status, data := flowRequest(store, "POST", "/api/undeploy", body)
		if status >= 400 {
			flowRefused(status, data)
		}
		fmt.Printf("%s is taken away: its versions and its instances are kept\n", rest[0])
	case "versions":
		if len(rest) > 1 {
			fmt.Println(flowUsage)
			exit(1)
		}
		program := ""
		if len(rest) == 1 {
			program = rest[0]
		}
		flowVersions(store, program)
	case "diff":
		if len(rest) != 1 {
			fmt.Println(flowUsage)
			exit(1)
		}
		flowDiff(store, rest[0], options["from"])
	case "deploy":
		if len(rest) != 1 {
			fmt.Println(flowUsage)
			exit(1)
		}
		flowDeploy(store, rest[0], options["dry"] != "")
	case "start":
		if len(rest) < 2 || len(rest) > 3 {
			fmt.Println(flowUsage)
			exit(1)
		}
		fields := "{}"
		if len(rest) == 3 {
			fields = rest[2]
		}
		if !json.Valid([]byte(fields)) {
			fatal(fmt.Errorf("the fields are JSON, as in '{\"id\": \"A1\", \"amount\": 3}'"))
		}
		body, _ := json.Marshal(map[string]any{"flow": rest[0], "id": rest[1], "fields": json.RawMessage(fields)})
		flowCall(store, "POST", "/api/instances", body)
	case "ls":
		query := url.Values{}
		if f := options["flow"]; f != "" {
			query.Set("flow", f)
		}
		if s := options["status"]; s != "" {
			query.Set("status", s)
		}
		flowList(store, "/api/instances?"+query.Encode(), options["json"] != "")
	case "show":
		if len(rest) != 1 {
			fmt.Println(flowUsage)
			exit(1)
		}
		if options["json"] != "" {
			flowCall(store, "GET", "/api/instances/"+url.PathEscape(rest[0]), nil)
			return
		}
		flowShow(store, rest[0])
	case "result":
		if len(rest) != 1 {
			fmt.Println(flowUsage)
			exit(1)
		}
		wait := options["wait"]
		if wait == "" {
			wait = "0s"
		}
		path := "/api/instances/" + url.PathEscape(rest[0]) + "/result?wait=" + url.QueryEscape(wait)
		if options["json"] != "" {
			flowCall(store, "GET", path, nil)
			return
		}
		status, data := flowRequest(store, "GET", path, nil)
		if status >= 400 {
			flowRefused(status, data)
		}
		if status == http.StatusAccepted {
			fmt.Printf("%s has not ended yet\n", rest[0])
			exit(2)
		}
		// As Qoqi writes it: "shipped", Ok("van-3")
		fmt.Println(flowd.Qoqi(data))
	case "send":
		if len(rest) != 3 {
			fmt.Println(flowUsage)
			exit(1)
		}
		if !json.Valid([]byte(rest[2])) {
			fatal(fmt.Errorf("the message is JSON, as in '{\"$type\": \"Decision.Approved\", \"$variant\": true, \"0\": \"ada\"}'"))
		}
		body, _ := json.Marshal(map[string]any{"message": json.RawMessage(rest[2]), "key": options["key"]})
		status, data := flowRequest(store, "POST", "/api/instances/"+url.PathEscape(rest[0])+"/inboxes/"+url.PathEscape(rest[1]), body)
		if status >= 400 {
			flowRefused(status, data)
		}
		var sent struct {
			Sent string `json:"sent"`
		}
		_ = json.Unmarshal(data, &sent)
		if sent.Sent == "already" {
			fmt.Printf("%s had it already: a message of that key is sent once\n", rest[0])
		} else {
			fmt.Printf("sent to %s, on %s\n", rest[0], rest[1])
		}
	case "resume":
		if len(rest) != 1 {
			fmt.Println(flowUsage)
			exit(1)
		}
		path := "/api/instances/" + url.PathEscape(rest[0]) + "/resume"
		if _, ok := options["go"]; ok {
			status, data := flowRequest(store, "POST", path, []byte(`{}`))
			if status >= 400 {
				flowRefused(status, data)
			}
			fmt.Printf("%s is resumed\n", rest[0])
			return
		}
		status, data := flowRequest(store, "POST", path, []byte(`{"dry": true}`))
		if status >= 400 {
			flowRefused(status, data)
		}
		var dry flowrt.DryRun
		if err := json.Unmarshal(data, &dry); err != nil {
			fatal(err)
		}
		fmt.Printf("Resumed, %s would\n", rest[0])
		for _, key := range dry.Forget {
			fmt.Printf("  forget the Err of %s, and run it again\n", key)
		}
		for _, key := range dry.Took {
			fmt.Printf("  take what %s gave, kept\n", key)
		}
		for _, next := range dry.Next {
			fmt.Printf("  then: %s\n", next)
		}
		if dry.Stopped != "" {
			fmt.Printf("  and stop again: %s\n", dry.Stopped)
		}
		fmt.Printf("qoqi resume %s --go does it\n", rest[0])
	case "end":
		if len(rest) < 1 || len(rest) > 2 {
			fmt.Println(flowUsage)
			exit(1)
		}
		reason := ""
		if len(rest) == 2 {
			reason = rest[1]
		}
		body, _ := json.Marshal(map[string]string{"reason": reason})
		status, data := flowRequest(store, "POST", "/api/instances/"+url.PathEscape(rest[0])+"/end", body)
		if status >= 400 {
			flowRefused(status, data)
		}
		fmt.Printf("%s is ended as it was\n", rest[0])
	case "answer":
		_, nothing := options["nothing"]
		if len(rest) != 3 && !(len(rest) == 2 && nothing) {
			fmt.Println(flowUsage)
			exit(1)
		}
		asked := map[string]any{"key": rest[1], "nothing": nothing}
		if len(rest) == 3 {
			if !json.Valid([]byte(rest[2])) {
				fatal(fmt.Errorf("what the step did is JSON, as the application would have answered it: '{\"Ok\": \"ch-1\"}'"))
			}
			asked["value"] = json.RawMessage(rest[2])
		}
		body, _ := json.Marshal(asked)
		status, data := flowRequest(store, "POST", "/api/instances/"+url.PathEscape(rest[0])+"/answer", body)
		if status >= 400 {
			flowRefused(status, data)
		}
		fmt.Printf("%s is answered: qoqi resume %s --go takes it up\n", rest[1], rest[0])
	case "gen":
		if len(rest) != 2 {
			fmt.Println(flowUsage)
			exit(1)
		}
		flowGen(rest[0], rest[1], options)
	case "ui":
		address, err := flowd.ReadAddress(store)
		if err != nil {
			fatal(err)
		}
		// The token goes after #: the browser keeps it, and never sends it
		page := address.URL + "/#token=" + address.Token
		fmt.Println(page)
		opener := "xdg-open"
		if runtime.GOOS == "darwin" {
			opener = "open"
		}
		_ = exec.Command(opener, page).Start()
	case "cancel":
		if len(rest) < 1 || len(rest) > 2 {
			fmt.Println(flowUsage)
			exit(1)
		}
		reason := ""
		if len(rest) == 2 {
			reason = rest[1]
		}
		body, _ := json.Marshal(map[string]string{"reason": reason})
		status, data := flowRequest(store, "POST", "/api/instances/"+url.PathEscape(rest[0])+"/cancel", body)
		if status >= 400 {
			flowRefused(status, data)
		}
		fmt.Printf("%s is cancelled: what it gave flow.undo runs, then it ends\n", rest[0])
	default:
		fmt.Printf("Unknown command: qoqi %s\n\n%s\n", args[0], flowUsage)
		exit(1)
	}
}

// flowOptions takes --name value out of the arguments.
func flowOptions(args []string) (map[string]string, []string) {
	options := map[string]string{"store": "flows.db", "listen": "127.0.0.1:7070"}
	var rest []string
	for i := 0; i < len(args); i++ {
		if name, ok := strings.CutPrefix(args[i], "--"); ok {
			if key, value, inline := strings.Cut(name, "="); inline {
				options[key] = value
				continue
			}
			if name == "go" || name == "json" || name == "nothing" || name == "watch" || name == "dry" {
				// A flag alone: resume for real, the API's JSON as it is,
				// deploy again what changes, or only say what a deploy does
				options[name] = "true"
				continue
			}
			if i+1 >= len(args) {
				fatal(fmt.Errorf("--%s needs a value", name))
			}
			options[name] = args[i+1]
			i++
			continue
		}
		rest = append(rest, args[i])
	}
	return options, rest
}

// flowServe is the daemon: it runs the flows of a program against a
// store, until it is told to stop.
func flowServe(program, storePath, listen string, options map[string]string) {
	shown := sqldb.Redacted(storePath)
	// One daemon on a store: two would run the same instances. One that
	// died leaves its address behind; it is no reason not to start
	if other, err := flowd.ReadAddress(storePath); err == nil && other.PID != os.Getpid() && alive(other.PID) {
		fatal(fmt.Errorf("a daemon (process %d) is serving %s already, at %s", other.PID, shown, other.URL))
	}
	store, err := flowrt.OpenStore(storePath)
	if err != nil {
		fatal(err)
	}
	defer store.Close()
	// Daemons on several machines share a store on a server: each runs
	// the instances whose leases it holds. Those a daemon of this machine
	// held when it died are let go at once
	machine, _ := os.Hostname()
	store.ShareAs(fmt.Sprintf("%s, process %d", machine, os.Getpid()))
	letGo := func(program *flowrt.SQL) error {
		return program.LetGoOf(func(holder string) bool { return deadHere(holder, machine) })
	}

	workers := runtime.NumCPU()
	if n, err := strconv.Atoi(options["workers"]); err == nil && n > 0 {
		workers = n
	}
	out := &lockedOut{w: os.Stdout}

	// The daemon serves what was deployed to the store, then what it is
	// given here: each program on tables named after it
	daemon, err := flowd.NewOf(nil)
	if err != nil {
		fatal(err)
	}
	daemon.Log = os.Stderr
	daemon.Open = func(name string) (flowrt.Store, error) {
		program, err := store.Program(name)
		if err != nil {
			return nil, err
		}
		return program, letGo(program)
	}
	daemon.Deployments = store
	daemon.Workers, daemon.Out = workers, out
	if err := daemon.Restore(); err != nil {
		fatal(err)
	}
	if program != "" {
		if err := deployHere(daemon, program); err != nil {
			fatal(err)
		}
	}
	if daemon.Token, err = flowd.StoreToken(storePath); err != nil {
		fatal(err)
	}

	listener, err := net.Listen("tcp", listen)
	if err != nil {
		fatal(fmt.Errorf("cannot listen on %s: %v", listen, err))
	}
	host, _, _ := net.SplitHostPort(listener.Addr().String())
	address := flowd.Address{URL: "http://" + listener.Addr().String(), Token: daemon.Token, PID: os.Getpid()}
	if err := flowd.WriteAddress(storePath, address); err != nil {
		fatal(err)
	}
	defer os.Remove(flowd.AddressFile(storePath))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	server := &http.Server{Handler: daemon.Handler(host, "localhost", "127.0.0.1", "::1"), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "qoqi: %v\n", err)
			stop()
		}
	}()
	if flows := daemon.Flows; len(flows) > 0 {
		fmt.Fprintf(os.Stderr, "qoqi: %s, on %s, at %s; qoqi ui opens the console\n", strings.Join(flows, ", "), shown, address.URL)
	} else {
		fmt.Fprintf(os.Stderr, "qoqi: no program yet, on %s, at %s; qoqi deploy gives it one\n", shown, address.URL)
	}
	if program != "" && options["watch"] != "" {
		stopWatching := make(chan struct{})
		defer close(stopWatching)
		go watch(daemon, program, stopWatching)
		fmt.Fprintf(os.Stderr, "qoqi: watching %s: a change saved is deployed\n", program)
	}
	if err := daemon.Run(ctx, 50*time.Millisecond); err != nil {
		exit(1)
	}
}

// deadHere says whether the daemon that holds a store, "host, process
// 123", is one of this machine whose process is gone.
func deadHere(holder, host string) bool {
	at, process, ok := strings.Cut(holder, ", process ")
	if !ok || at != host {
		return false
	}
	pid, err := strconv.Atoi(process)
	return err == nil && pid != os.Getpid() && !alive(pid)
}

// programFiles are the files of the program the pages may show: those it
// was compiled from that are on disk, not the library's.
func programFiles(chunk *vm.Chunk) []string {
	var files []string
	for _, file := range chunk.Files() {
		if info, err := os.Stat(file); err == nil && !info.IsDir() {
			files = append(files, file)
		}
	}
	sort.Strings(files)
	return files
}

// flowRequest asks the daemon serving a store.
func flowRequest(store, method, path string, body []byte) (int, []byte) {
	address, err := flowd.ReadAddress(store)
	if err != nil {
		fatal(err)
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, address.URL+path, reader)
	if err != nil {
		fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+address.Token)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		fatal(fmt.Errorf("the daemon serving %s does not answer at %s: %v", sqldb.Redacted(store), address.URL, err))
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}

// flowCall asks the daemon and prints what it answers; a refusal is the
// command's failure.
func flowCall(store, method, path string, body []byte) {
	status, data := flowRequest(store, method, path, body)
	if status >= 400 {
		flowRefused(status, data)
	}
	os.Stdout.Write(data)
	if len(data) > 0 && data[len(data)-1] != '\n' {
		fmt.Println()
	}
	if status == http.StatusAccepted {
		// Not ended yet
		exit(2)
	}
}

// flowRefused ends the command with what the daemon said was wrong.
func flowRefused(status int, data []byte) {
	var failure struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(data, &failure) == nil && failure.Message != "" {
		fatal(fmt.Errorf("%s", failure.Message))
	}
	fatal(fmt.Errorf("the daemon answered %d: %s", status, data))
}

// flowList prints the instances one per line, with what each is doing,
// as the console says it.
func flowList(store, path string, raw bool) {
	status, data := flowRequest(store, "GET", path, nil)
	if status >= 400 {
		flowRefused(status, data)
	}
	if raw {
		os.Stdout.Write(data)
		fmt.Println()
		return
	}
	var views []flowd.View
	if err := json.Unmarshal(data, &views); err != nil {
		fatal(err)
	}
	now := time.Now()
	rows := [][]string{}
	for _, v := range views {
		stage := v.Stage
		if v.Replaying {
			stage += " (replaying)"
		}
		rows = append(rows, []string{v.ID, v.Flow, v.Status, stage, flowd.Doing(v, now)})
	}
	printColumns(rows)
}

// printColumns prints rows with their columns lined up; the last is as
// long as it is.
func printColumns(rows [][]string) {
	if len(rows) == 0 {
		return
	}
	widths := make([]int, len(rows[0]))
	for _, row := range rows {
		for i, cell := range row[:len(row)-1] {
			widths[i] = max(widths[i], len([]rune(cell)))
		}
	}
	for _, row := range rows {
		var line strings.Builder
		for i, cell := range row {
			line.WriteString(cell)
			if i < len(row)-1 {
				line.WriteString(strings.Repeat(" ", widths[i]-len([]rune(cell))+3))
			}
		}
		fmt.Println(strings.TrimRight(line.String(), " "))
	}
}

// flowShow prints an instance for a person: where it is, what it was
// given, what its steps returned, what it would undo.
func flowShow(store, id string) {
	status, data := flowRequest(store, "GET", "/api/instances/"+url.PathEscape(id), nil)
	if status >= 400 {
		flowRefused(status, data)
	}
	var detail flowd.Detail
	if err := json.Unmarshal(data, &detail); err != nil {
		fatal(err)
	}
	head := []string{detail.Flow, detail.ID}
	if detail.Version != "" {
		head = append(head, "version "+shortVersion(detail.Version))
	}
	fmt.Println(strings.Join(head, "   "))

	var rows [][]string
	row := func(name, value string) { rows = append(rows, []string{"  " + name, value}) }
	row(detail.Status, flowd.Doing(detail.View, time.Now()))
	if detail.Stage != "" {
		stage := fmt.Sprintf("%s, pass %d", detail.Stage, detail.Pass)
		if detail.Replaying {
			stage += ", replaying to its next stage"
		}
		row("stage", stage)
	}
	for _, place := range detail.Tasks {
		for _, frame := range place.Frames {
			if frame.File == "" || strings.HasPrefix(frame.File, "<std>") {
				continue
			}
			at := fmt.Sprintf("%s:%d in %s", filepath.Base(frame.File), frame.Line, frame.Function)
			if len(detail.Tasks) > 1 {
				at = "task " + place.Task + "  " + at
			}
			row("at", at)
			break
		}
	}
	if fields := givenFields(detail.Fields); fields != nil {
		for _, field := range fields {
			row(field[0], field[1])
		}
	}
	for _, kept := range detail.Kept {
		// A key ends pass:task:step#n; the undos run in the task finish
		rest := kept.Key[strings.LastIndex(kept.Key, "/")+1:]
		step := rest[strings.LastIndex(rest, ":")+1:]
		if strings.Contains(rest, ":finish:") {
			row("undone", step+" → "+flowd.Qoqi(kept.Result))
			continue
		}
		row("step", step+" → "+flowd.Qoqi(kept.Result))
	}
	for i := len(detail.Undos) - 1; i >= 0; i-- {
		u := detail.Undos[i]
		argument, _ := json.Marshal(u.Argument)
		row("undo", fmt.Sprintf("%s(%s)", u.Step, flowd.Qoqi(argument)))
	}
	if detail.Parent != "" {
		row("parent", detail.Parent)
	}
	if len(detail.Children) > 0 {
		row("children", strings.Join(detail.Children, ", "))
	}
	printColumns(rows)
}

// givenFields are the fields an instance was started with, as Qoqi
// writes them, its inboxes left out: the daemon makes those.
func givenFields(fields json.RawMessage) [][2]string {
	decoder := json.NewDecoder(bytes.NewReader(fields))
	var order []string
	values := map[string]json.RawMessage{}
	if t, err := decoder.Token(); err != nil || t != json.Delim('{') {
		return nil
	}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return nil
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return nil
		}
		name, _ := key.(string)
		if strings.HasPrefix(name, "$") || strings.Contains(string(value), `"std.flow.Inbox"`) {
			continue
		}
		order = append(order, name)
		values[name] = value
	}
	var out [][2]string
	for _, name := range order {
		out = append(out, [2]string{name, flowd.Qoqi(values[name])})
	}
	return out
}

// shortVersion is a version as people read it: its first letters.
func shortVersion(version string) string {
	version = strings.TrimPrefix(version, "sha256:")
	if len(version) > 8 {
		return version[:8]
	}
	return version
}

// alive reports whether a process is running.
func alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return process.Signal(syscall.Signal(0)) == nil
}

// lockedOut is the output of a daemon's workers: one line at a time.
type lockedOut struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedOut) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
