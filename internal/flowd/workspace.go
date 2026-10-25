package flowd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/qoqiflow/qoqi/internal/compiler"
	qerrors "github.com/qoqiflow/qoqi/internal/errors"
	"github.com/qoqiflow/qoqi/internal/flowrt"
	"github.com/qoqiflow/qoqi/internal/flows"
	"github.com/qoqiflow/qoqi/internal/formatter"
	"github.com/qoqiflow/qoqi/internal/project"
	"github.com/qoqiflow/qoqi/internal/vm"
)

// The console's editor: a program is written there, file by file, kept
// as a draft until it is deployed. It is compiled by the daemon, from
// the draft alone: its entry is name.qi, the modules it imports are the
// other files of the draft, and the queues of its steps are in its
// qoqi.json, if it has one. Deployed, the draft is done with: the
// program is what was deployed, and editing it again starts from there.

var (
	programName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	fileName    = regexp.MustCompile(`^([a-z0-9_]+/)*[a-z0-9_]+\.qi$`)
)

const projectFile = project.FileName

func checkProgramName(name string) error {
	if !programName.MatchString(name) {
		return fmt.Errorf("a program's name is a lower case letter, then letters, digits and _: %q is not", name)
	}
	return nil
}

func checkFileName(name string) error {
	if name == projectFile || fileName.MatchString(name) {
		return nil
	}
	return fmt.Errorf("a file of a program is qoqi.json, or a .qi file whose name and folders are lower case letters, digits and _: %q is not", name)
}

// WorkspaceProgram is a program as the editor lists it.
type WorkspaceProgram struct {
	Program  string   `json:"program"`
	Files    []string `json:"files"`
	Draft    bool     `json:"draft"`              // changed in the console, not deployed
	Deployed string   `json:"deployed,omitempty"` // the version served
	Updated  int64    `json:"updated,omitempty"`  // when the draft last changed
	Author   string   `json:"author,omitempty"`   // who changed it last
}

// WorkspaceFile is a file of a program, with its text.
type WorkspaceFile struct {
	File    string `json:"file"`
	Text    string `json:"text"`
	Updated int64  `json:"updated,omitempty"`
	Author  string `json:"author,omitempty"`
}

// Opened is a program opened in the editor: its draft, or what was
// deployed of it when it has none.
type Opened struct {
	Program  string          `json:"program"`
	Entry    string          `json:"entry"`
	Draft    bool            `json:"draft"`
	Deployed string          `json:"deployed,omitempty"`
	Files    []WorkspaceFile `json:"files"`
}

// deployedFiles are the files of the last version of a program, as the
// editor names them: from the folder they share, and its qoqi.json of
// the queues it was deployed with.
func (d *Daemon) deployedFiles(name string) ([]WorkspaceFile, string, bool) {
	dep := d.lastDeployed(name)
	if dep == nil {
		return nil, "", false
	}
	var pkg Package
	if err := json.Unmarshal(dep.Package, &pkg); err != nil {
		return nil, "", false
	}
	var paths []string
	for p := range pkg.Sources {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	common := ""
	if len(paths) > 0 {
		common = path.Dir(strings.ReplaceAll(paths[0], "\\", "/"))
		for _, p := range paths[1:] {
			for common != "." && common != "/" && !strings.HasPrefix(strings.ReplaceAll(p, "\\", "/"), common+"/") {
				common = path.Dir(common)
			}
		}
	}
	var files []WorkspaceFile
	for _, p := range paths {
		rel := strings.TrimPrefix(strings.ReplaceAll(p, "\\", "/"), common+"/")
		files = append(files, WorkspaceFile{File: rel, Text: pkg.Sources[p], Updated: dep.At})
	}
	if len(pkg.Queues) > 0 {
		cfg, _ := json.MarshalIndent(map[string]any{"queues": pkg.Queues}, "", "  ")
		files = append(files, WorkspaceFile{File: projectFile, Text: string(cfg) + "\n", Updated: dep.At})
	}
	return files, dep.Version, true
}

// workspace lists the programs: those deployed, and those written in the
// console, deployed or not.
func (d *Daemon) workspace(w http.ResponseWriter, r *http.Request) {
	if d.noEditor(w) {
		return
	}
	drafts, err := d.Keep.Drafts("", false)
	if err != nil {
		fail(w, http.StatusInternalServerError, "keep", err.Error())
		return
	}
	byName := map[string]*WorkspaceProgram{}
	for _, dr := range drafts {
		p := byName[dr.Program]
		if p == nil {
			p = &WorkspaceProgram{Program: dr.Program, Draft: true}
			byName[dr.Program] = p
		}
		p.Files = append(p.Files, dr.File)
		if dr.Updated > p.Updated {
			p.Updated, p.Author = dr.Updated, dr.Author
		}
	}
	if d.Deployments != nil {
		deployed, _ := d.Deployments.Deployments()
		for _, dep := range deployed {
			p := byName[dep.Name]
			if p == nil {
				files, _, _ := d.deployedFiles(dep.Name)
				p = &WorkspaceProgram{Program: dep.Name, Updated: dep.At}
				for _, f := range files {
					p.Files = append(p.Files, f.File)
				}
				byName[dep.Name] = p
			}
			p.Deployed = dep.Version
		}
	}
	out := []WorkspaceProgram{}
	for _, p := range byName {
		if p.Files == nil {
			p.Files = []string{}
		}
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Program < out[j].Program })
	answer(w, http.StatusOK, out)
}

func (d *Daemon) noEditor(w http.ResponseWriter) bool {
	if d.Keep == nil {
		fail(w, http.StatusNotFound, "workspace", "this daemon has no editor: it was given no store for the console")
		return true
	}
	return false
}

// open is a program as the editor shows it: its draft, or the files of
// its last version.
func (d *Daemon) open(name string) (*Opened, error) {
	opened := &Opened{Program: name, Entry: name + ".qi", Files: []WorkspaceFile{}}
	_, version, _ := d.deployedFiles(name)
	opened.Deployed = version
	drafts, err := d.Keep.Drafts(name, true)
	if err != nil {
		return nil, err
	}
	if len(drafts) > 0 {
		opened.Draft = true
		for _, dr := range drafts {
			opened.Files = append(opened.Files, WorkspaceFile{File: dr.File, Text: dr.Text, Updated: dr.Updated, Author: dr.Author})
		}
		return opened, nil
	}
	files, _, ok := d.deployedFiles(name)
	if !ok {
		return nil, nil
	}
	opened.Files = files
	return opened, nil
}

func (d *Daemon) draft(w http.ResponseWriter, r *http.Request) {
	if d.noEditor(w) {
		return
	}
	name := r.PathValue("program")
	opened, err := d.open(name)
	if err != nil {
		fail(w, http.StatusInternalServerError, "keep", err.Error())
		return
	}
	if opened == nil {
		fail(w, http.StatusNotFound, "program", "there is no program "+name+", deployed or written here")
		return
	}
	if principalOf(r).Operator {
		// An operator reads the code, not the secrets written in it: an
		// admin, who edits and deploys it, does
		for i, f := range opened.Files {
			if f.File == projectFile {
				opened.Files[i].Text = withoutSecrets(f.Text)
			}
		}
	}
	answer(w, http.StatusOK, opened)
}

// hiddenSecret is what an operator is shown of a secret written in a
// qoqi.json, rather than read from the environment.
const hiddenSecret = "(hidden: an admin reads it)"

// withoutSecrets is a qoqi.json, the secrets of its queues written there
// hidden; those it reads from the environment, "env:NAME", as they are.
func withoutSecrets(text string) string {
	var config map[string]json.RawMessage
	if json.Unmarshal([]byte(text), &config) != nil {
		// Not JSON: nothing in it can be told a secret, and none is shown
		return "{}\n"
	}
	var queues map[string]map[string]json.RawMessage
	if json.Unmarshal(config["queues"], &queues) != nil {
		return text
	}
	for _, q := range queues {
		var secret string
		if json.Unmarshal(q["secret"], &secret) == nil && secret != "" && !strings.HasPrefix(secret, "env:") {
			q["secret"], _ = json.Marshal(hiddenSecret)
		}
	}
	config["queues"], _ = json.Marshal(queues)
	out, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return "{}\n"
	}
	return string(out) + "\n"
}

// discard takes a draft away: the program is what was deployed of it,
// or nothing.
func (d *Daemon) discard(w http.ResponseWriter, r *http.Request) {
	if d.noEditor(w) {
		return
	}
	if err := d.Keep.RemoveDraft(r.PathValue("program"), ""); err != nil {
		fail(w, http.StatusInternalServerError, "keep", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// startDraft makes a draft of a program from what was deployed of it,
// the first time one of its files changes in the console.
func (d *Daemon) startDraft(name, author string) error {
	drafts, err := d.Keep.Drafts(name, false)
	if err != nil || len(drafts) > 0 {
		return err
	}
	files, _, _ := d.deployedFiles(name)
	now := time.Now().UnixMilli()
	for _, f := range files {
		if err := d.Keep.PutDraft(Draft{Program: name, File: f.File, Text: f.Text, Updated: now, Author: author}); err != nil {
			return err
		}
	}
	return nil
}

func authorOf(r *http.Request) string {
	if p := principalOf(r); p.User != "" {
		return p.User
	}
	return "token"
}

const maxFile = 1 << 20

func (d *Daemon) saveFile(w http.ResponseWriter, r *http.Request) {
	if d.noEditor(w) {
		return
	}
	name, file := r.PathValue("program"), r.PathValue("file")
	if err := checkProgramName(name); err != nil {
		fail(w, http.StatusBadRequest, "program", err.Error())
		return
	}
	if err := checkFileName(file); err != nil {
		fail(w, http.StatusBadRequest, "file", err.Error())
		return
	}
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxFile+1024)).Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "body", "the body is JSON, {\"text\": ...}, of a megabyte at most")
		return
	}
	if file == projectFile && strings.TrimSpace(body.Text) != "" {
		var cfg project.Config
		if err := json.Unmarshal([]byte(body.Text), &cfg); err != nil {
			fail(w, http.StatusBadRequest, "file", "qoqi.json is not JSON: "+err.Error())
			return
		}
	}
	d.drafting.Lock()
	defer d.drafting.Unlock()
	author := authorOf(r)
	if err := d.startDraft(name, author); err != nil {
		fail(w, http.StatusInternalServerError, "keep", err.Error())
		return
	}
	saved := Draft{Program: name, File: file, Text: body.Text, Updated: time.Now().UnixMilli(), Author: author}
	if err := d.Keep.PutDraft(saved); err != nil {
		fail(w, http.StatusInternalServerError, "keep", err.Error())
		return
	}
	saved.Text = ""
	answer(w, http.StatusOK, saved)
}

func (d *Daemon) removeFile(w http.ResponseWriter, r *http.Request) {
	if d.noEditor(w) {
		return
	}
	name, file := r.PathValue("program"), r.PathValue("file")
	d.drafting.Lock()
	defer d.drafting.Unlock()
	if err := d.startDraft(name, authorOf(r)); err != nil {
		fail(w, http.StatusInternalServerError, "keep", err.Error())
		return
	}
	if err := d.Keep.RemoveDraft(name, file); err != nil {
		fail(w, http.StatusInternalServerError, "keep", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Problem is a diagnostic of the compiler, where the editor shows it.
type Problem struct {
	Severity   string `json:"severity"`
	Code       string `json:"code,omitempty"`
	Message    string `json:"message"`
	File       string `json:"file"`
	Line       int    `json:"line"`
	Column     int    `json:"column"`
	EndColumn  int    `json:"endColumn,omitempty"`
	Suggestion string `json:"suggestion,omitempty"`
}

// Checked is what the compiler said of a draft.
type Checked struct {
	OK       bool      `json:"ok"`
	Flows    []string  `json:"flows"`
	Problems []Problem `json:"problems"`
	// Schema is what the program is, when it compiles: its flows and the
	// types of their fields, for the forms of the editor
	Schema *Schema `json:"schema,omitempty"`
}

// compileDraft compiles the files of a program, as they are given: the
// entry is name.qi, among them.
func (d *Daemon) compileDraft(name string, files map[string]string) (*compiler.Result, *Package, Checked, error) {
	checked := Checked{Flows: []string{}, Problems: []Problem{}}
	entry := name + ".qi"
	src, ok := files[entry]
	if !ok {
		checked.Problems = append(checked.Problems, Problem{Severity: "error", Message: "a program has a file " + entry + ": its flows start there", File: entry, Line: 1, Column: 1})
		return nil, nil, checked, nil
	}
	opts, cfg, problem := d.draftOptions(name, files)
	if problem != nil {
		checked.Problems = append(checked.Problems, *problem)
		return nil, nil, checked, nil
	}
	root := name
	errs := qerrors.NewHandler()
	result := compiler.CompileOptions(src, root+"/"+entry, opts, errs)
	checked.Problems = append(checked.Problems, problemsOf(errs, root)...)
	if errs.HasErrors() || result == nil || result.Chunk == nil {
		return result, nil, checked, nil
	}
	checked.OK = true
	checked.Flows = flows.FlowsOf(result.AST)
	if len(checked.Flows) == 0 {
		checked.OK = false
		checked.Problems = append(checked.Problems, Problem{Severity: "error", Message: "the program has no flow: a flow is a struct that implements Flow<T> of std.flow", File: entry, Line: 1, Column: 1})
		return result, nil, checked, nil
	}
	sources := map[string]string{}
	for f, text := range files {
		if strings.HasSuffix(f, ".qi") {
			sources[root+"/"+f] = text
		}
	}
	pkg, err := PackageOf(name, root+"/"+entry, result.AST, result.Chunk, sources)
	if err != nil {
		return result, nil, checked, err
	}
	if len(cfg.Queues) > 0 {
		pkg.Queues = cfg.Queues
	}
	checked.Schema = pkg.Schema
	return result, pkg, checked, nil
}

// draftOptions are how the files of a draft are compiled: named in the
// program's own folder, name/entry.qi, each read from the draft and from
// nowhere else, with the queues of its qoqi.json.
func (d *Daemon) draftOptions(name string, files map[string]string) (compiler.Options, project.Config, *Problem) {
	var cfg project.Config
	if text := strings.TrimSpace(files[projectFile]); text != "" {
		if err := json.Unmarshal([]byte(text), &cfg); err != nil {
			return compiler.Options{}, cfg, &Problem{Severity: "error", Message: "qoqi.json is not JSON: " + err.Error(), File: projectFile, Line: 1, Column: 1}
		}
	}
	root := name
	read := func(p string) (string, error) {
		rel := strings.TrimPrefix(path.Clean(strings.ReplaceAll(p, "\\", "/")), root+"/")
		if text, ok := files[rel]; ok && strings.HasSuffix(rel, ".qi") {
			return text, nil
		}
		return "", fmt.Errorf("%s is not a file of %s", rel, name)
	}
	opts := compiler.Options{Read: read, Root: root, Unused: true, Queues: cfg.QueueNames(), Lock: d.lastLock(name)}
	if opts.Lock == nil {
		opts.Lock = &flowrt.Lock{} // never one beside the daemon
	}
	return opts, cfg, nil
}

// problemsOf are the diagnostics of a compilation, named in the program.
func problemsOf(errs *qerrors.Handler, root string) []Problem {
	var out []Problem
	for _, diag := range errs.Diagnostics {
		p := Problem{
			Severity: strings.ToLower(diag.Severity.String()), Code: diag.Code, Message: diag.Message,
			File: strings.TrimPrefix(diag.Pos.File, root+"/"), Line: diag.Pos.Line, Column: diag.Pos.Col, Suggestion: diag.Suggestion,
		}
		if len(diag.Highlights) > 0 {
			p.EndColumn = diag.Highlights[0].EndCol
		}
		out = append(out, p)
	}
	return out
}

// draftAsked is the body of a request on a draft: the files the editor
// has, over those kept, and what a run of its tests is to run.
type draftAsked struct {
	Files  map[string]string `json:"files"`
	File   string            `json:"file"`
	Filter string            `json:"filter"`
}

// filesOf are the files of a program to compile: those the request
// gives, as the editor has them, over those of the draft.
func (d *Daemon) filesOf(name string, r *http.Request) (map[string]string, error) {
	files, _, err := d.filesAndAsked(name, r)
	return files, err
}

func (d *Daemon) filesAndAsked(name string, r *http.Request) (map[string]string, draftAsked, error) {
	var body draftAsked
	data, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, 16*maxFile))
	if err != nil {
		return nil, body, err
	}
	if len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, &body); err != nil {
			return nil, body, errors.New("the body is JSON: {\"files\": {\"name.qi\": ...}}")
		}
	}
	files := map[string]string{}
	opened, err := d.open(name)
	if err != nil {
		return nil, body, err
	}
	if opened != nil {
		for _, f := range opened.Files {
			files[f.File] = f.Text
		}
	}
	for f, text := range body.Files {
		if err := checkFileName(f); err != nil {
			return nil, body, err
		}
		files[f] = text
	}
	return files, body, nil
}

// check compiles a draft, and says what the compiler said of it.
func (d *Daemon) check(w http.ResponseWriter, r *http.Request) {
	if d.noEditor(w) {
		return
	}
	name := r.PathValue("program")
	if err := checkProgramName(name); err != nil {
		fail(w, http.StatusBadRequest, "program", err.Error())
		return
	}
	files, err := d.filesOf(name, r)
	if err != nil {
		fail(w, http.StatusBadRequest, "body", err.Error())
		return
	}
	_, _, checked, err := d.compileDraft(name, files)
	if err != nil {
		fail(w, http.StatusInternalServerError, "compile", err.Error())
		return
	}
	answer(w, http.StatusOK, checked)
}

// DraftDeploy is what deploying a draft did, or would do.
type DraftDeploy struct {
	Checked
	Report *Report  `json:"report,omitempty"`
	Why    []string `json:"why,omitempty"` // why the daemon refused it
}

// deployDraft compiles a draft and deploys it, or says what a deploy
// would do: ?dry=1. Deployed, the draft is done with.
func (d *Daemon) deployDraft(w http.ResponseWriter, r *http.Request) {
	if d.noEditor(w) {
		return
	}
	name := r.PathValue("program")
	if err := checkProgramName(name); err != nil {
		fail(w, http.StatusBadRequest, "program", err.Error())
		return
	}
	dry := r.URL.Query().Get("dry") != ""
	d.drafting.Lock()
	defer d.drafting.Unlock()
	files, err := d.filesOf(name, r)
	if err != nil {
		fail(w, http.StatusBadRequest, "body", err.Error())
		return
	}
	_, pkg, checked, err := d.compileDraft(name, files)
	if err != nil {
		fail(w, http.StatusInternalServerError, "compile", err.Error())
		return
	}
	out := DraftDeploy{Checked: checked}
	if pkg == nil {
		answer(w, http.StatusUnprocessableEntity, out)
		return
	}
	report, err := d.Deploy(pkg, dry)
	var refusal *Refused
	if errors.As(err, &refusal) {
		out.OK, out.Why = false, refusal.Why
		answer(w, http.StatusConflict, out)
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "deploy", err.Error())
		return
	}
	out.Report = report
	if !dry {
		if err := d.Keep.RemoveDraft(name, ""); err != nil {
			fmt.Fprintf(d.Log, "qoqi: the draft of %s stays: %v\n", name, err)
		}
	}
	answer(w, http.StatusOK, out)
}

// The formatter is a Qoqi program, compiled once, run for each text.
var (
	formatOnce  sync.Once
	formatChunk *vm.Chunk
	formatErr   error
)

// Format writes a program the one way, as qoqi fmt does.
func Format(text string) (string, error) {
	formatOnce.Do(func() {
		errs := qerrors.NewHandler()
		result := compiler.CompileOptions(formatter.Source, formatter.File, compiler.Options{}, errs)
		if errs.HasErrors() || result.Chunk == nil {
			formatErr = errors.New("the formatter does not compile")
			return
		}
		formatChunk = result.Chunk
	})
	if formatErr != nil {
		return "", formatErr
	}
	var out bytes.Buffer
	machine := quietMachine(&out, formatter.File)
	machine.SetArgs([]string{"-"})
	machine.SetInput(strings.NewReader(text))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	value, err := machine.ExecContext(ctx, formatChunk)
	if err != nil {
		return "", err
	}
	if value.IsInt() && value.AsInt() != 0 {
		return "", errors.New("the program cannot be formatted as it is: check it first")
	}
	return out.String(), nil
}

func (d *Daemon) format(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxFile+1024)).Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "body", "the body is JSON, {\"text\": ...}, of a megabyte at most")
		return
	}
	tidy, err := Format(body.Text)
	if err != nil {
		fail(w, http.StatusUnprocessableEntity, "format", err.Error())
		return
	}
	answer(w, http.StatusOK, map[string]string{"text": tidy})
}
