package vm

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// exitError is what std.os.exit returns to the VM: not a failure, but the
// program asking to end with a status. Nothing catches it on the way out.
type exitError struct{ code int }

func (e *exitError) Error() string { return fmt.Sprintf("exit %d", e.code) }

// ExitCode returns the status a program asked to end with through
// std.os.exit, and whether err is such a request.
func ExitCode(err error) (int, bool) {
	var exit *exitError
	if errors.As(err, &exit) {
		return exit.code, true
	}
	return 0, false
}

// SetArgs gives the program the arguments it was started with (std.os.args).
func (vm *VM) SetArgs(args []string) {
	vm.args = args
}

// SetProgram tells the program which file it was run from (std.os.program).
func (vm *VM) SetProgram(path string) {
	vm.program = path
}

// failedFile records why a file operation did not work, for the Qoqi side
// to turn into an io.Error (std.io.problem), and returns the "it did not
// work" value of the native.
func (vm *VM) failedFile(err error, result Value) (Value, error) {
	vm.problemKind, vm.problem = kindOfFileError(err), reasonOf(err)
	return result, nil
}

// newArrayOf builds an array of values that are already pinned or
// otherwise alive.
func (vm *VM) newArrayOf(values []Value) (Value, error) {
	arr, _, err := vm.heap.AllocArray(len(values))
	if err != nil {
		return NewNull(), err
	}
	result := vm.pin(NewHeapPtr(uintptr(ptrOf(arr))))
	for _, v := range values {
		if err := vm.appendToArray(arr, v); err != nil {
			return NewNull(), err
		}
	}
	return result, nil
}

// newStringTable builds an array of arrays of strings: the rows of a table.
func (vm *VM) newStringTable(rows [][]string) (Value, error) {
	values := make([]Value, len(rows))
	for i, row := range rows {
		v, err := vm.newStringArray(row)
		if err != nil {
			return NewNull(), err
		}
		values[i] = v
	}
	return vm.newArrayOf(values)
}

func init() {
	// A function of one path that gives a text
	pathText := func(name string, f func(string) string) native {
		return native{name, 1, func(vm *VM, args []Value) (Value, error) {
			path, err := argString(args, 0)
			if err != nil {
				return NewNull(), err
			}
			return vm.newString(f(path))
		}}
	}
	// A function of one path that says yes or no
	pathBool := func(name string, f func(string) bool) native {
		return native{name, 1, func(vm *VM, args []Value) (Value, error) {
			path, err := argString(args, 0)
			if err != nil {
				return NewNull(), err
			}
			return NewBool(f(path)), nil
		}}
	}
	// An operation on one path that works or not; the reason is kept
	pathDo := func(name string, f func(string) error) native {
		return native{name, 1, func(vm *VM, args []Value) (Value, error) {
			path, err := argString(args, 0)
			if err != nil {
				return NewNull(), err
			}
			if err := f(path); err != nil {
				return vm.failedFile(err, NewBool(false))
			}
			return NewBool(true), nil
		}}
	}
	// An operation from one path to another
	pathPair := func(name string, f func(from, to string) error) native {
		return native{name, 2, func(vm *VM, args []Value) (Value, error) {
			from, err := argString(args, 0)
			if err != nil {
				return NewNull(), err
			}
			to, err := argString(args, 1)
			if err != nil {
				return NewNull(), err
			}
			if err := f(from, to); err != nil {
				return vm.failedFile(err, NewBool(false))
			}
			return NewBool(true), nil
		}}
	}

	registerNatives("std.path",
		native{"joined", 1, func(vm *VM, args []Value) (Value, error) {
			parts, err := argStrings(args, 0)
			if err != nil {
				return NewNull(), err
			}
			return vm.newString(filepath.Join(parts...))
		}},
		pathText("base", filepath.Base),
		pathText("dir", filepath.Dir),
		pathText("extension", extension),
		pathText("clean", filepath.Clean),
		pathBool("isAbsolute", filepath.IsAbs),
		native{"absolute", 1, func(vm *VM, args []Value) (Value, error) {
			path, err := argString(args, 0)
			if err != nil {
				return NewNull(), err
			}
			abs, err := filepath.Abs(path)
			if err != nil {
				return vm.newString(path)
			}
			return vm.newString(abs)
		}},
		// The path that leads from a folder to a path, or null when there is
		// none (another drive). A relative one starts at the current folder.
		native{"relative", 2, func(vm *VM, args []Value) (Value, error) {
			from, err := argString(args, 0)
			if err != nil {
				return NewNull(), err
			}
			to, err := argString(args, 1)
			if err != nil {
				return NewNull(), err
			}
			if filepath.IsAbs(from) != filepath.IsAbs(to) {
				from, _ = filepath.Abs(from)
				to, _ = filepath.Abs(to)
			}
			rel, err := filepath.Rel(from, to)
			if err != nil {
				return NewNull(), nil
			}
			return vm.newString(rel)
		}},
		native{"separator", 0, func(vm *VM, args []Value) (Value, error) {
			return vm.newString(string(filepath.Separator))
		}},
		// Whether a name fits a pattern with * and ?: match("*.txt", "a.txt")
		native{"matches", 2, func(vm *VM, args []Value) (Value, error) {
			pattern, err := argString(args, 0)
			if err != nil {
				return NewNull(), err
			}
			name, err := argString(args, 1)
			if err != nil {
				return NewNull(), err
			}
			ok, err := filepath.Match(pattern, name)
			if err != nil {
				return NewNull(), fmt.Errorf("'%s' is not a valid pattern", pattern)
			}
			return NewBool(ok), nil
		}},
	)

	registerNatives("std.fs",
		pathBool("isDir", func(path string) bool {
			info, err := os.Stat(path)
			return err == nil && info.IsDir()
		}),
		pathBool("isFile", func(path string) bool {
			info, err := os.Stat(path)
			return err == nil && info.Mode().IsRegular()
		}),
		// The names in a folder, sorted; null if it cannot be listed
		native{"names", 1, func(vm *VM, args []Value) (Value, error) {
			path, err := argString(args, 0)
			if err != nil {
				return NewNull(), err
			}
			entries, err := os.ReadDir(path)
			if err != nil {
				return vm.failedFile(err, NewNull())
			}
			names := make([]string, len(entries))
			for i, entry := range entries {
				names[i] = entry.Name()
			}
			sort.Strings(names)
			return vm.newStringArray(names)
		}},
		// Every file under a folder, as paths, sorted; null if it cannot
		// be walked
		native{"tree", 1, func(vm *VM, args []Value) (Value, error) {
			root, err := argString(args, 0)
			if err != nil {
				return NewNull(), err
			}
			var files []string
			walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if !entry.IsDir() {
					files = append(files, path)
				}
				return nil
			})
			if walkErr != nil {
				return vm.failedFile(walkErr, NewNull())
			}
			sort.Strings(files)
			return vm.newStringArray(files)
		}},
		// Size in bytes and time of the last change in milliseconds, or
		// null
		native{"stat", 1, func(vm *VM, args []Value) (Value, error) {
			path, err := argString(args, 0)
			if err != nil {
				return NewNull(), err
			}
			info, err := os.Stat(path)
			if err != nil {
				return vm.failedFile(err, NewNull())
			}
			return vm.newArrayOf([]Value{NewInt(info.Size()), NewInt(info.ModTime().UnixMilli())})
		}},
		pathDo("makeFolder", func(path string) error { return os.MkdirAll(path, 0755) }),
		pathDo("removeTree", os.RemoveAll),
		pathPair("move", os.Rename),
		pathPair("duplicate", copyFile),
		native{"currentDir", 0, func(vm *VM, args []Value) (Value, error) {
			dir, err := os.Getwd()
			if err != nil {
				return vm.newString(".")
			}
			return vm.newString(dir)
		}},
		native{"tempDir", 0, func(vm *VM, args []Value) (Value, error) {
			return vm.newString(os.TempDir())
		}},
		// A new, empty folder for the program's own use, or null
		native{"newTempDir", 1, func(vm *VM, args []Value) (Value, error) {
			prefix, err := argString(args, 0)
			if err != nil {
				return NewNull(), err
			}
			dir, err := os.MkdirTemp("", prefix)
			if err != nil {
				return vm.failedFile(err, NewNull())
			}
			return vm.newString(dir)
		}},
	)

	registerNatives("std.os",
		native{"args", 0, func(vm *VM, args []Value) (Value, error) {
			return vm.newStringArray(vm.args)
		}},
		// The file the program was run from, or "" if it has none
		native{"program", 0, func(vm *VM, args []Value) (Value, error) {
			return vm.newString(vm.program)
		}},
		native{"exit", 1, func(vm *VM, args []Value) (Value, error) {
			code, err := argInt(args, 0)
			if err != nil {
				return NewNull(), err
			}
			return NewNull(), &exitError{code: int(code)}
		}},
		// "linux", "darwin", "windows", ...
		native{"platform", 0, func(vm *VM, args []Value) (Value, error) {
			return vm.newString(runtime.GOOS)
		}},
		native{"hostname", 0, func(vm *VM, args []Value) (Value, error) {
			name, err := os.Hostname()
			if err != nil {
				return vm.newString("")
			}
			return vm.newString(name)
		}},
		// Every environment variable, as name=value
		native{"variables", 0, func(vm *VM, args []Value) (Value, error) {
			env := os.Environ()
			sort.Strings(env)
			return vm.newStringArray(env)
		}},
		// Runs a command and waits for it. Gives its exit status, what it
		// wrote and what it wrote as errors; or null if it could not be
		// started, the reason being kept.
		native{"execute", 3, func(vm *VM, args []Value) (Value, error) {
			return vm.runProgram(args, "", nil)
		}},
		// The same, in a folder ("" for the current one), with variables
		// added to the environment, each written name=value
		native{"executeIn", 5, func(vm *VM, args []Value) (Value, error) {
			folder, err := argString(args, 3)
			if err != nil {
				return NewNull(), err
			}
			variables, err := argStrings(args, 4)
			if err != nil {
				return NewNull(), err
			}
			return vm.runProgram(args, folder, variables)
		}},
	)
}

// runProgram runs the program of std.os.execute and executeIn: args are the
// program, its arguments and its input.
func (vm *VM) runProgram(args []Value, folder string, variables []string) (Value, error) {
	name, err := argString(args, 0)
	if err != nil {
		return NewNull(), err
	}
	arguments, err := argStrings(args, 1)
	if err != nil {
		return NewNull(), err
	}
	input, err := argString(args, 2)
	if err != nil {
		return NewNull(), err
	}

	cmd := exec.Command(name, arguments...)
	cmd.Dir = folder
	if len(variables) > 0 {
		cmd.Env = append(os.Environ(), variables...)
	}
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if input != "" {
		cmd.Stdin = strings.NewReader(input)
	}
	status := 0
	// Other tasks go on while the program runs
	var failed error
	if gone := vm.blocking(func() { failed = cmd.Run() }); gone != nil {
		return NewNull(), gone
	}
	if err := failed; err != nil {
		var exit *exec.ExitError
		if errors.Is(err, exec.ErrNotFound) {
			return vm.refused("not found", fmt.Sprintf("there is no program called '%s'", name))
		}
		if !errors.As(err, &exit) {
			return vm.failedFile(err, NewNull())
		}
		status = exit.ExitCode()
	}
	return vm.newStringArray([]string{fmt.Sprint(status), stdout.String(), stderr.String()})
}

// extension is the extension of the last part of a path, with its dot.
// The dots a name starts with do not start one: .bashrc has none, and
// .config.json has .json.
func extension(path string) string {
	ext := filepath.Ext(path)
	at := len(path) - len(ext)
	start := strings.LastIndexFunc(path[:at], func(r rune) bool { return os.IsPathSeparator(uint8(r)) }) + 1
	if strings.Trim(path[start:at], ".") == "" {
		return ""
	}
	return ext
}

func copyFile(from, to string) error {
	source, err := os.Open(from)
	if err != nil {
		return err
	}
	defer source.Close()
	if info, err := source.Stat(); err == nil && info.IsDir() {
		return &fs.PathError{Op: "copy", Path: from, Err: errIsDirectory}
	}

	target, err := os.Create(to)
	if err != nil {
		return err
	}
	if _, err := io.Copy(target, source); err != nil {
		target.Close()
		return err
	}
	return target.Close()
}
