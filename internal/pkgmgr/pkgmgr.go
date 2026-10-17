// Package pkgmgr holds the package manager of Qoqi: `qoqi pkg install`,
// `update`, `require`, `remove`, `show`, `why` and `outdated`. It is a
// Qoqi program of several modules, in lib/, compiled into the qoqi
// binary; see docs/packages.md.
package pkgmgr

import (
	"embed"
	"io/fs"
	"path"
	"strings"

	"github.com/qoqiflow/qoqi/internal/compiler"
)

// Root is the folder the program's files appear to be in. It is not a
// folder on disk: paths under it are served from the binary.
const Root = "<pkg>"

// File is the name the entry file goes by in messages.
const File = Root + "/main.qi"

// Commands are the commands of qoqi that the package manager answers.
var Commands = []string{"install", "update", "require", "remove", "show", "why", "outdated"}

//go:embed lib/*.qi
var files embed.FS

// Source is the text of the entry file.
var Source = read("main.qi")

func read(name string) string {
	data, err := files.ReadFile(path.Join("lib", name))
	if err != nil {
		panic(err)
	}
	return string(data)
}

// Options has the modules of the program read from the binary.
func Options() compiler.Options {
	return compiler.Options{
		Root: Root,
		Read: func(file string) (string, error) {
			name := strings.ReplaceAll(strings.TrimPrefix(file, Root+"/"), "\\", "/")
			data, err := files.ReadFile(path.Join("lib", name))
			if !strings.HasPrefix(file, Root+"/") || strings.Contains(name, "/") || err != nil {
				return "", &fs.PathError{Op: "read", Path: file, Err: fs.ErrNotExist}
			}
			return string(data), nil
		},
	}
}
