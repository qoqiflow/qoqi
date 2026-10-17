// Package stdlib holds the standard library: Qoqi modules compiled into
// the qoqi binary, so that `import std.strings;` works in any program
// without installing anything.
//
// The modules are ordinary Qoqi source, in lib/. What they cannot do in
// Qoqi they declare with `extern fn`; those functions are the natives of
// the virtual machine (internal/vm/native_*.go).
package stdlib

import (
	"embed"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Package is the name the library is imported by: import std.math;
const Package = "std"

// Root is the folder the library's files appear to be in. It is not a
// folder on disk: paths under it are served from the binary by Read. It
// is what diagnostics and runtime errors show for a line of the library.
const Root = "<std>"

//go:embed lib/*.qi
var files embed.FS

// IsPath reports whether a path names a file of the library.
func IsPath(file string) bool {
	return strings.HasPrefix(file, Root+"/")
}

// Read returns the source of a file of the library, given as Root/name.qi.
func Read(file string) (string, error) {
	// The loader builds the path with the separator of the system
	name := strings.ReplaceAll(strings.TrimPrefix(file, Root+"/"), "\\", "/")
	data, err := files.ReadFile(path.Join("lib", name))
	if !IsPath(file) || strings.Contains(name, "/") {
		err = fs.ErrNotExist
	}
	if err != nil {
		return "", &fs.PathError{Op: "read", Path: file, Err: fs.ErrNotExist}
	}
	return string(data), nil
}

// Modules lists the modules of the library, sorted: "std.math", ...
func Modules() []string {
	entries, _ := files.ReadDir("lib")
	var names []string
	for _, entry := range entries {
		names = append(names, Package+"."+strings.TrimSuffix(entry.Name(), ".qi"))
	}
	sort.Strings(names)
	return names
}

// declaration is a name a module of the library offers at its top level:
// pub fn, pub extern fn, pub struct, pub enum, pub trait, pub const.
var declaration = regexp.MustCompile(`(?m)^pub (?:extern )?(?:fn|struct|enum|trait|const) +([A-Za-z_][A-Za-z0-9_]*)`)

var (
	declaredOnce sync.Once
	declared     map[string][]string // Name to the modules that offer it
)

// Declaring lists the modules of the library that offer a name at their
// top level, sorted: Declaring("Ordered") is ["std.ops"]. It is what tells
// a program that uses a name without importing it where the name is.
func Declaring(name string) []string {
	declaredOnce.Do(func() {
		declared = make(map[string][]string)
		for _, module := range Modules() {
			data, err := files.ReadFile(path.Join("lib", strings.TrimPrefix(module, Package+".")+".qi"))
			if err != nil {
				continue
			}
			seen := make(map[string]bool)
			for _, m := range declaration.FindAllStringSubmatch(string(data), -1) {
				if !seen[m[1]] {
					seen[m[1]] = true
					declared[m[1]] = append(declared[m[1]], module)
				}
			}
		}
	})
	return declared[name]
}
