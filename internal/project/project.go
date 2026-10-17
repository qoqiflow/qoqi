// Package project reads qoqi.json, the file that describes a Qoqi
// program made of more than a single file: where it starts, where its
// modules are, which packages it uses and the limits it runs with.
//
//	{
//	    "name": "game",
//	    "entry": "src/main.qi",
//	    "root": "src",
//	    "packages": { "std": "vendor/std" },
//	    "heap_mb": 512,
//	    "max_depth": 20000,
//	    "require": { "acme/textkit": "^1.2" },
//	    "queues": { "billing": { "url": "env:BILLING_URL", "secret": "env:BILLING_SECRET" } }
//	}
//
// Every key is optional. Paths are relative to the folder of the file.
//
// The packages of "require" are installed by `qoqi pkg install` into vendor/,
// which then has a file, vendor/installed.json, that says where each one's
// modules are. Load reads it, and the packages it lists are found like
// those of "packages".
package project

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// FileName is the name of the project file.
const FileName = "qoqi.json"

// Config is the content of a project file.
type Config struct {
	// Name of the program. Informative.
	Name string `json:"name"`

	// Entry is the file the program starts in, run when no file is given
	// on the command line.
	Entry string `json:"entry"`

	// Root is the folder module paths start at. Default: the folder of
	// the project file.
	Root string `json:"root"`

	// Packages maps the first part of a module path to the folder that
	// holds its modules: with "std": "vendor/std", `import std.list;`
	// reads vendor/std/list.qi.
	Packages map[string]string `json:"packages"`

	// Limits of the virtual machine; 0 leaves the default.
	HeapMB   int `json:"heap_mb"`
	MaxDepth int `json:"max_depth"`

	// What a package says of itself, for the package manager: its
	// version when it is not taken from tags, what it is, the name its
	// modules are imported by.
	Version     string `json:"version"`
	Description string `json:"description"`
	License     string `json:"license"`
	Import      string `json:"import"`

	// The packages the program requires, by name and version constraint,
	// and where to find those that are not at the default place.
	Require      map[string]string `json:"require"`
	RequireDev   map[string]string `json:"require-dev"`
	Repositories []Repository      `json:"repositories"`

	// Queues are who carries out the steps of the application a flow
	// declares, `step fn charge(...) -> Charge by billing;`: by name,
	// where each is served and the secret its requests are signed with,
	// given by the environment as "env:NAME" (see docs/flow-app-steps.md)
	Queues map[string]QueueConfig `json:"queues"`

	// Installed is the folder of each installed package, by the name it
	// is imported by, from vendor/installed.json.
	Installed map[string]string `json:"-"`

	// installed holds the names of the packages installed, and installedDev
	// whether those of require-dev were.
	installed    map[string]bool
	installedDev bool

	// Path is the project file, Dir its folder.
	Path string `json:"-"`
	Dir  string `json:"-"`
}

// QueueConfig is a queue of qoqi.json.
type QueueConfig struct {
	URL    string `json:"url"`
	Secret string `json:"secret"`
	// Concurrency is how many of its steps may be under way at once
	Concurrency int `json:"concurrency"`
}

// QueueNames are the names of the queues the project declares, sorted.
func (c *Config) QueueNames() []string {
	var names []string
	for name := range c.Queues {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Queue is where a queue is served and its secret, read from the
// environment when the project says "env:NAME".
func (c *Config) Queue(name string) (url, secret string, err error) {
	q, ok := c.Queues[name]
	if !ok {
		return "", "", fmt.Errorf("%s declares no queue %s", c.Path, name)
	}
	if url, err = fromEnvironment(q.URL, name, "url"); err != nil {
		return "", "", err
	}
	if secret, err = fromEnvironment(q.Secret, name, "secret"); err != nil {
		return "", "", err
	}
	return url, secret, nil
}

// fromEnvironment is a value of qoqi.json, or the variable of the
// environment it names, "env:BILLING_URL".
func fromEnvironment(value, queue, key string) (string, error) {
	variable, ok := strings.CutPrefix(value, "env:")
	if !ok {
		return value, nil
	}
	got := os.Getenv(variable)
	if got == "" {
		return "", fmt.Errorf("the queue %s takes its %s from %s, which the environment does not set", queue, key, variable)
	}
	return got, nil
}

// Repository is a place packages come from: {"type": "git", "url": ...}.
type Repository struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

// Vendor is the folder packages are installed in, next to the project
// file; InstalledFile the file in it that says where each one is.
const (
	Vendor        = "vendor"
	InstalledFile = "installed.json"
)

// Find looks for a project file in dir and then in each folder above it.
// It returns nil when there is none.
func Find(dir string) (*Config, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	for {
		path := filepath.Join(abs, FileName)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return Load(display(path))
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return nil, nil
		}
		abs = parent
	}
}

// display shortens an absolute path to one relative to the current
// directory when it is inside it, so that messages show the path the way
// the user would write it.
func display(abs string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return abs
	}
	rel, err := filepath.Rel(cwd, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return abs
	}
	return rel
}

// Load reads and checks a project file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", path, err)
	}
	cfg, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	cfg.Path = path
	cfg.Dir = filepath.Dir(path)
	if err := cfg.readInstalled(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// readInstalled reads vendor/installed.json, if there is one.
func (c *Config) readInstalled() error {
	file := filepath.Join(c.Dir, Vendor, InstalledFile)
	data, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("cannot read %s: %w", file, err)
	}

	var installed struct {
		Dev      bool `json:"dev"`
		Packages []struct {
			Name   string `json:"name"`
			Import string `json:"import"`
			Dir    string `json:"dir"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(data, &installed); err != nil {
		return fmt.Errorf("%s: %v; run qoqi pkg install to write it again", file, err)
	}

	c.Installed = map[string]string{}
	c.installed = map[string]bool{}
	c.installedDev = installed.Dev
	for _, pkg := range installed.Packages {
		if !isIdentifier(pkg.Import) || pkg.Dir == "" {
			return fmt.Errorf("%s: package %q has no import name or no folder; run qoqi pkg install to write it again", file, pkg.Name)
		}
		if _, taken := c.Packages[pkg.Import]; taken {
			return fmt.Errorf("%s: the package %s is imported as %s, which \"packages\" of %s gives to a folder already", c.Path, pkg.Name, pkg.Import, FileName)
		}
		c.Installed[pkg.Import] = c.resolve(pkg.Dir)
		c.installed[pkg.Name] = true
	}
	return nil
}

// CheckInstalled reports the first package the project requires that is
// not installed: the project has to be installed before it is compiled.
func (c *Config) CheckInstalled() error {
	names := sortedKeys(c.Require)
	if c.installed == nil || c.installedDev {
		names = append(names, sortedKeys(c.RequireDev)...)
	}
	for _, name := range names {
		if !c.installed[name] {
			return fmt.Errorf("%s requires %s, which is not installed: run qoqi pkg install", c.Path, name)
		}
	}
	return nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// Parse reads the content of a project file.
func Parse(data []byte) (*Config, error) {
	cfg := &Config{}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(cfg); err != nil {
		return nil, describe(err, data)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("unexpected text after the closing '}'")
	}
	return cfg, cfg.check()
}

// describe turns a decoding error into a message about the file.
func describe(err error, data []byte) error {
	var syntax *json.SyntaxError
	var mismatch *json.UnmarshalTypeError
	switch {
	case errors.As(err, &syntax):
		// The offset counts the character that was not expected
		line, col := position(data, syntax.Offset-1)
		return fmt.Errorf("line %d, column %d: not valid JSON: %s", line, col, syntax.Error())
	case errors.As(err, &mismatch):
		line, _ := position(data, mismatch.Offset)
		return fmt.Errorf("line %d: %q must be %s", line, mismatch.Field, expected(mismatch.Field))
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return errors.New("the file is empty or ends too early; the smallest project file is {}")
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		key := strings.Trim(strings.TrimPrefix(err.Error(), "json: unknown field "), `"`)
		return fmt.Errorf("unknown key %q; the keys are %s", key, strings.Join(keys, ", "))
	}
	return err
}

var keys = []string{"name", "entry", "root", "packages", "heap_mb", "max_depth", "queues", "version", "description", "license", "import", "require", "require-dev", "repositories"}

func expected(field string) string {
	switch strings.SplitN(field, ".", 2)[0] {
	case "heap_mb", "max_depth":
		return "a whole number"
	case "packages":
		return `an object of names and folders, as in {"std": "vendor/std"}`
	case "require", "require-dev":
		return `an object of packages and versions, as in {"acme/textkit": "^1.2"}`
	case "repositories":
		return `an array of repositories, as in [{"type": "git", "url": "https://example.com/textkit.git"}]`
	case "queues":
		return `an object of queues, as in {"payments": {"url": "env:PAYMENTS_URL", "secret": "env:PAYMENTS_SECRET"}}`
	}
	return "a string"
}

// position converts a byte offset into a line and a column, both 1-based.
func position(data []byte, offset int64) (line, col int) {
	if offset > int64(len(data)) {
		offset = int64(len(data))
	}
	if offset < 0 {
		offset = 0
	}
	before := data[:offset]
	line = bytes.Count(before, []byte("\n")) + 1
	col = len(before) - bytes.LastIndexByte(before, '\n')
	return line, col
}

func (c *Config) check() error {
	if c.Entry != "" && !strings.HasSuffix(c.Entry, ".qi") {
		return fmt.Errorf(`"entry" must be a .qi file, got %q`, c.Entry)
	}
	if c.HeapMB < 0 {
		return fmt.Errorf(`"heap_mb" must be positive, got %d`, c.HeapMB)
	}
	if c.MaxDepth < 0 {
		return fmt.Errorf(`"max_depth" must be positive, got %d`, c.MaxDepth)
	}
	if c.Import != "" && !isIdentifier(c.Import) {
		return fmt.Errorf(`"import" must be a name that can be imported: use letters, digits and '_', not starting with a digit, got %q`, c.Import)
	}
	for _, section := range []struct {
		key      string
		packages map[string]string
	}{{"require", c.Require}, {"require-dev", c.RequireDev}} {
		for _, name := range sortedKeys(section.packages) {
			if !packageName.MatchString(name) {
				return fmt.Errorf(`%q in %q is not a package name: write vendor/name, in lower case`, name, section.key)
			}
			if strings.TrimSpace(section.packages[name]) == "" {
				return fmt.Errorf(`%s in %q has no version: write a constraint, as in "^1.2", or "*" for any`, name, section.key)
			}
		}
	}
	for name, q := range c.Queues {
		if !isIdentifier(name) {
			return fmt.Errorf(`queue %q is not a name a step can be declared by: use letters, digits and '_', not starting with a digit`, name)
		}
		if q.Concurrency < 0 {
			return fmt.Errorf(`queue %s has a "concurrency" below nothing: how many of its steps may be under way at once`, name)
		}
		if q.URL == "" || q.Secret == "" {
			return fmt.Errorf(`queue %s needs a "url" and a "secret", or "env:NAME" for each`, name)
		}
	}
	for i, repository := range c.Repositories {
		if repository.Type != "git" && repository.Type != "path" {
			return fmt.Errorf(`repository %d has a "type" that is not "git" or "path"`, i+1)
		}
		if repository.URL == "" {
			return fmt.Errorf(`repository %d has no "url"`, i+1)
		}
	}
	for _, name := range c.packageNames() {
		if !isIdentifier(name) {
			return fmt.Errorf(`package name %q is not a name that can be imported: use letters, digits and '_', not starting with a digit`, name)
		}
		if c.Packages[name] == "" {
			return fmt.Errorf(`package %q has no folder`, name)
		}
	}
	return nil
}

// packageName is what a package is called: vendor/name, as Composer has it.
var packageName = regexp.MustCompile(`^[a-z0-9]([_.-]?[a-z0-9]+)*/[a-z0-9](([_.]|-{1,2})?[a-z0-9]+)*$`)

func (c *Config) packageNames() []string {
	names := make([]string, 0, len(c.Packages))
	for name := range c.Packages {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func isIdentifier(s string) bool {
	for i, r := range s {
		letter := r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		if !letter && (i == 0 || r < '0' || r > '9') {
			return false
		}
	}
	return s != ""
}

// resolve returns a path of the project file relative to its folder.
func (c *Config) resolve(path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(c.Dir, path)
}

// RootDir is the folder module paths start at.
func (c *Config) RootDir() string {
	return c.resolve(c.Root)
}

// EntryFile is the file the program starts in.
func (c *Config) EntryFile() (string, error) {
	if c.Entry == "" {
		return "", fmt.Errorf(`%s does not say where the program starts; add "entry": "main.qi"`, c.Path)
	}
	return c.resolve(c.Entry), nil
}

// PackageDirs returns the folder of each package: those "packages" names,
// and those installed.
func (c *Config) PackageDirs() map[string]string {
	dirs := make(map[string]string, len(c.Packages)+len(c.Installed))
	for name, dir := range c.Installed {
		dirs[name] = dir
	}
	for name, dir := range c.Packages {
		dirs[name] = c.resolve(dir)
	}
	return dirs
}
