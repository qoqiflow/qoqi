package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	cfg, err := Parse([]byte(`{
		"name": "game",
		"entry": "src/main.qi",
		"root": "src",
		"packages": { "std": "vendor/std", "ui_kit2": "/opt/qoqi/ui" },
		"heap_mb": 512,
		"max_depth": 20000
	}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Name != "game" || cfg.Entry != "src/main.qi" || cfg.Root != "src" || cfg.HeapMB != 512 || cfg.MaxDepth != 20000 {
		t.Errorf("unexpected config: %+v", cfg)
	}
	if len(cfg.Packages) != 2 || cfg.Packages["std"] != "vendor/std" {
		t.Errorf("unexpected packages: %v", cfg.Packages)
	}

	// Every key is optional
	empty, err := Parse([]byte(`{}`))
	if err != nil {
		t.Fatalf("expected {} to be a valid project file, got %v", err)
	}
	if empty.Entry != "" || empty.Root != "" || len(empty.Packages) != 0 || empty.HeapMB != 0 || empty.MaxDepth != 0 {
		t.Errorf("expected an empty config, got %+v", empty)
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"empty file", ``, "the file is empty or ends too early"},
		{"cut short", `{"name": "a"`, "ends too early"},
		{"not JSON", "{\n  \"name\": \"a\",\n  entry: \"main.qi\"\n}", "line 3, column 3: not valid JSON"},
		{"trailing comma", "{\n  \"name\": \"a\",\n}", "line 3, column 1: not valid JSON"},
		{"text after the object", `{} {}`, "unexpected text after the closing '}'"},
		{"unknown key", `{"main": "main.qi"}`, `unknown key "main"; the keys are name, entry, root, packages, heap_mb, max_depth, queues`},
		{"misspelled key", `{"heap-mb": 5}`, `unknown key "heap-mb"`},
		{"number as text", "{\n\"heap_mb\": \"512\"}", `line 2: "heap_mb" must be a whole number`},
		{"fraction", `{"max_depth": 1.5}`, `"max_depth" must be a whole number`},
		{"entry as a number", `{"entry": 3}`, `"entry" must be a string`},
		{"packages as a list", `{"packages": ["std"]}`, `"packages" must be an object of names and folders`},
		{"package folder as a number", `{"packages": {"std": 1}}`, `must be an object of names and folders`},
		{"entry that is not source", `{"entry": "main.txt"}`, `"entry" must be a .qi file, got "main.txt"`},
		{"negative heap", `{"heap_mb": -1}`, `"heap_mb" must be positive`},
		{"negative depth", `{"max_depth": -5}`, `"max_depth" must be positive`},
		{"package with a dot", `{"packages": {"std.io": "x"}}`, `package name "std.io" is not a name that can be imported`},
		{"package with a dash", `{"packages": {"my-lib": "x"}}`, `package name "my-lib" is not a name`},
		{"package starting with a digit", `{"packages": {"2d": "x"}}`, `package name "2d" is not a name`},
		{"package without a name", `{"packages": {"": "x"}}`, `package name "" is not a name`},
		{"package without a folder", `{"packages": {"std": ""}}`, `package "std" has no folder`},
		{"require as a list", `{"require": ["acme/log"]}`, `"require" must be an object of packages and versions`},
		{"require with a number", `{"require": {"acme/log": 1}}`, `"require" must be an object of packages and versions`},
		{"require of a name without a vendor", `{"require": {"log": "^1.0"}}`, `"log" in "require" is not a package name: write vendor/name, in lower case`},
		{"require of a name in capitals", `{"require-dev": {"Acme/Log": "^1.0"}}`, `"Acme/Log" in "require-dev" is not a package name`},
		{"require without a version", `{"require": {"acme/log": " "}}`, `acme/log in "require" has no version`},
		{"repositories as an object", `{"repositories": {"type": "git"}}`, `"repositories" must be an array of repositories`},
		{"repository of an unknown type", `{"repositories": [{"type": "svn", "url": "x"}]}`, `repository 1 has a "type" that is not "git" or "path"`},
		{"repository without an address", `{"repositories": [{"type": "git", "url": "a"}, {"type": "path"}]}`, `repository 2 has no "url"`},
		{"repository with a misspelled key", `{"repositories": [{"type": "git", "uri": "a"}]}`, `unknown key "uri"`},
		{"import that cannot be imported", `{"import": "text-kit"}`, `"import" must be a name that can be imported`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.src))
			if err == nil {
				t.Fatal("expected an error, got none")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("expected an error containing %q, got %q", tt.want, err.Error())
			}
		})
	}
}

// write creates a file under dir, with its folders.
func write(t *testing.T, dir, path, content string) string {
	t.Helper()
	full := filepath.Join(dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return full
}

func TestLoadResolvesPathsFromTheProjectFile(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, "game/qoqi.json", `{
		"entry": "src/main.qi",
		"root": "src",
		"packages": { "std": "../shared/std", "abs": "/opt/qoqi/abs" }
	}`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	game := filepath.Join(dir, "game")
	if cfg.Path != path || cfg.Dir != game {
		t.Errorf("expected the config to know its place, got %s in %s", cfg.Path, cfg.Dir)
	}
	if got := cfg.RootDir(); got != filepath.Join(game, "src") {
		t.Errorf("unexpected root %s", got)
	}
	if got, err := cfg.EntryFile(); err != nil || got != filepath.Join(game, "src", "main.qi") {
		t.Errorf("unexpected entry %s, %v", got, err)
	}
	dirs := cfg.PackageDirs()
	if dirs["std"] != filepath.Join(dir, "shared", "std") {
		t.Errorf("expected a relative package folder to start at the project file, got %s", dirs["std"])
	}
	if dirs["abs"] != "/opt/qoqi/abs" {
		t.Errorf("expected an absolute package folder to be kept, got %s", dirs["abs"])
	}
}

func TestLoadDefaults(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(write(t, dir, "qoqi.json", `{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Without "root", module paths start where the project file is
	if got := cfg.RootDir(); got != dir {
		t.Errorf("expected the root to be the project's folder, got %s", got)
	}
	if len(cfg.PackageDirs()) != 0 {
		t.Errorf("expected no packages, got %v", cfg.PackageDirs())
	}
	// Without "entry" there is nothing to run by default, and the message says what to add
	if _, err := cfg.EntryFile(); err == nil || !strings.Contains(err.Error(), `add "entry": "main.qi"`) {
		t.Errorf("expected a message about the missing entry, got %v", err)
	}
}

func TestLoadErrorsNameTheFile(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, "qoqi.json", `{"entri": "main.qi"}`)
	if _, err := Load(path); err == nil || !strings.HasPrefix(err.Error(), path+": unknown key") {
		t.Errorf("expected the error to start with the file, got %v", err)
	}
	if _, err := Load(filepath.Join(dir, "missing.json")); err == nil || !strings.Contains(err.Error(), "cannot read") {
		t.Errorf("expected a read error, got %v", err)
	}
}

func TestFind(t *testing.T) {
	dir := t.TempDir()
	outer := write(t, dir, "qoqi.json", `{"name": "outer"}`)
	inner := write(t, dir, "tools/gen/qoqi.json", `{"name": "inner"}`)
	write(t, dir, "src/app/deep/file.qi", "")
	write(t, dir, "tools/gen/src/file.qi", "")

	find := func(rel string) *Config {
		t.Helper()
		cfg, err := Find(filepath.Join(dir, rel))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return cfg
	}
	same := func(a, b string) bool {
		ra, _ := filepath.EvalSymlinks(a)
		rb, _ := filepath.EvalSymlinks(b)
		return ra == rb
	}

	// In the folder itself, and from any depth below it
	for _, rel := range []string{".", "src", "src/app/deep"} {
		if cfg := find(rel); cfg == nil || cfg.Name != "outer" || !same(cfg.Path, outer) {
			t.Errorf("from %s: expected the outer project, got %+v", rel, cfg)
		}
	}
	// The nearest one wins
	for _, rel := range []string{"tools/gen", "tools/gen/src"} {
		if cfg := find(rel); cfg == nil || cfg.Name != "inner" || !same(cfg.Path, inner) {
			t.Errorf("from %s: expected the inner project, got %+v", rel, cfg)
		}
	}
	// A folder between the two belongs to the outer one
	if cfg := find("tools"); cfg == nil || cfg.Name != "outer" {
		t.Errorf("expected the outer project from tools/, got %+v", cfg)
	}

	// None at all is not an error
	bare := t.TempDir()
	if cfg, err := Find(bare); err != nil || cfg != nil {
		t.Errorf("expected no project in an empty folder, got %+v, %v", cfg, err)
	}
	// A folder called qoqi.json is not a project file
	if err := os.Mkdir(filepath.Join(bare, FileName), 0755); err != nil {
		t.Fatal(err)
	}
	if cfg, err := Find(bare); err != nil || cfg != nil {
		t.Errorf("expected a folder named %s to be ignored, got %+v, %v", FileName, cfg, err)
	}

	// A broken project file is an error, not a missing project
	broken := t.TempDir()
	write(t, broken, "qoqi.json", `{"entry": }`)
	if _, err := Find(filepath.Join(broken)); err == nil || !strings.Contains(err.Error(), "not valid JSON") {
		t.Errorf("expected the broken file to be reported, got %v", err)
	}
}

func TestParsePackageKeys(t *testing.T) {
	cfg, err := Parse([]byte(`{
		"name": "acme/shop",
		"version": "1.2.0",
		"description": "A shop",
		"license": "MIT",
		"import": "shop",
		"require": { "acme/log": "^1.0", "acme/my--lib.x": "dev-main" },
		"require-dev": { "acme/test_kit": "*" },
		"repositories": [
			{ "type": "git", "url": "https://example.com/log.git" },
			{ "type": "path", "url": "../kit" }
		]
	}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Version != "1.2.0" || cfg.Import != "shop" || cfg.Require["acme/log"] != "^1.0" || cfg.RequireDev["acme/test_kit"] != "*" {
		t.Errorf("unexpected config: %+v", cfg)
	}
	if len(cfg.Repositories) != 2 || cfg.Repositories[1] != (Repository{Type: "path", URL: "../kit"}) {
		t.Errorf("unexpected repositories: %+v", cfg.Repositories)
	}
}

// installed.json, as qoqi install writes it, gives the folders of the
// packages; CheckInstalled says when one the project requires is missing.
func TestInstalledPackages(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, "shop/qoqi.json", `{
		"packages": { "local": "lib/local" },
		"require": { "acme/log": "^1.0", "acme/text-kit": "^2.0" },
		"require-dev": { "acme/test": "*" }
	}`)
	shop := filepath.Join(dir, "shop")

	// Nothing installed yet
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := cfg.CheckInstalled(); err == nil || err.Error() != path+" requires acme/log, which is not installed: run qoqi pkg install" {
		t.Errorf("expected the first package missing to be named, got %v", err)
	}
	if dirs := cfg.PackageDirs(); len(dirs) != 1 {
		t.Errorf("expected only the folder of \"packages\", got %v", dirs)
	}

	// Installed, without require-dev
	write(t, dir, "shop/vendor/installed.json", `{
		"dev": false,
		"packages": [
			{ "name": "acme/log", "version": "1.4.0", "import": "log", "dir": "vendor/acme/log/log" },
			{ "name": "acme/text-kit", "version": "2.0.1", "import": "text_kit", "dir": "/opt/kits/text_kit" }
		]
	}`)
	cfg, err = Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := cfg.CheckInstalled(); err != nil {
		t.Errorf("expected an install without require-dev to be enough, got %v", err)
	}
	dirs := cfg.PackageDirs()
	if dirs["log"] != filepath.Join(shop, "vendor", "acme", "log", "log") || dirs["text_kit"] != "/opt/kits/text_kit" || dirs["local"] != filepath.Join(shop, "lib", "local") {
		t.Errorf("unexpected folders: %v", dirs)
	}

	// Installed with require-dev, but without one of its packages
	write(t, dir, "shop/vendor/installed.json", `{"dev": true, "packages": [
		{ "name": "acme/log", "import": "log", "dir": "vendor/acme/log/log" },
		{ "name": "acme/text-kit", "import": "text_kit", "dir": "vendor/acme/text-kit/text_kit" }
	]}`)
	cfg, err = Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := cfg.CheckInstalled(); err == nil || !strings.Contains(err.Error(), "requires acme/test, which is not installed") {
		t.Errorf("expected the package of require-dev to be missed, got %v", err)
	}

	broken := []struct{ installed, want string }{
		{`{"packages": [`, "vendor/installed.json: unexpected end of JSON input; run qoqi pkg install to write it again"},
		{`{"packages": [{"name": "acme/log", "import": "a-b", "dir": "x"}]}`, `package "acme/log" has no import name or no folder`},
		{`{"packages": [{"name": "acme/log", "import": "log"}]}`, `package "acme/log" has no import name or no folder`},
		{`{"packages": [{"name": "acme/local", "import": "local", "dir": "x"}]}`, `the package acme/local is imported as local, which "packages" of qoqi.json gives to a folder already`},
	}
	for _, tt := range broken {
		write(t, dir, "shop/vendor/installed.json", tt.installed)
		if _, err := Load(path); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: expected an error containing %q, got %v", tt.installed, tt.want, err)
		}
	}
}

// A queue whose secret is nothing would have the daemon sign with a key
// anyone has: refused, written so or read so from the environment.
func TestAQueueHasASecret(t *testing.T) {
	t.Setenv("QOQI_EMPTY_SECRET", "")
	for _, secret := range []string{"", "   "} {
		cfg := &Config{Path: "qoqi.json", Queues: map[string]QueueConfig{"billing": {URL: "http://127.0.0.1:9/q", Secret: secret}}}
		if _, _, err := cfg.Queue("billing"); err == nil || !strings.Contains(err.Error(), "no secret") {
			t.Errorf("secret %q: %v", secret, err)
		}
	}
	cfg := &Config{Path: "qoqi.json", Queues: map[string]QueueConfig{"billing": {URL: "http://127.0.0.1:9/q", Secret: "env:QOQI_EMPTY_SECRET"}}}
	if _, _, err := cfg.Queue("billing"); err == nil {
		t.Error("a secret read from a variable set to nothing")
	}
	cfg.Queues["billing"] = QueueConfig{URL: "http://127.0.0.1:9/q", Secret: "a secret of its own"}
	if _, secret, err := cfg.Queue("billing"); err != nil || secret != "a secret of its own" {
		t.Errorf("a secret: %q %v", secret, err)
	}
}
