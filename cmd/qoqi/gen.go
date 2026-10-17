package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/qoqiflow/qoqi/internal/flowd"
	"github.com/qoqiflow/qoqi/internal/flowgen"
	"github.com/qoqiflow/qoqi/internal/vm"
)

// flowGen is qoqi gen: what an application in another language
// needs to carry out the steps of its queues, written from the program.
func flowGen(language, program string, options map[string]string) {
	file := sourceFile(program, flowUsage)
	result := compileSource(file)
	schema := flowd.SchemaOf(file, result.AST, nil)

	// The shape of each step, as the daemon sends it: its name and its
	// signature, read from the bytecode
	shapes := map[string]string{}
	for _, step := range schema.Steps {
		if step.Queue == "" {
			continue
		}
		at := result.Chunk.FindPrototype(step.Name)
		if at < 0 {
			continue
		}
		name := step.Name[strings.LastIndex(step.Name, ".")+1:]
		shapes[name] = vm.StepShape(name, result.Chunk.Prototypes[at].Signature)
	}
	o := flowgen.Options{Source: filepath.Base(file), Queue: options["queue"], Namespace: options["namespace"], From: options["from"], Shapes: shapes}

	var out []byte
	var err error
	switch language {
	case "go":
		out, err = flowgen.Go(schema, options["package"], o)
	case "php":
		// A file for each class, as PSR-4 loads them, in the folder given
		dir := options["out"]
		if dir == "" {
			fatal(fmt.Errorf("qoqi gen php writes a file for each class: --out src/Flow"))
		}
		files, err := flowgen.PHP(schema, o)
		if err != nil {
			fatal(err)
		}
		writeFiles(dir, files)
		return
	case "ts":
		out, err = flowgen.TS(schema, o)
	default:
		err = fmt.Errorf("qoqi gen writes for go, php or ts; not %s", language)
	}
	if err != nil {
		fatal(err)
	}
	os.Stdout.Write(out)
}

// writeFiles writes the files given under a folder, and takes away the
// files of an earlier run that are no longer written: a class gone from
// the program is gone from the folder.
func writeFiles(dir string, files map[string]string) {
	marker := "Written by qoqi gen"
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		if _, kept := files[filepath.ToSlash(rel)]; !kept {
			if data, err := os.ReadFile(path); err == nil && strings.Contains(string(data), marker) {
				os.Remove(path)
			}
		}
		return nil
	})
	for name, text := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			fatal(err)
		}
	}
}
