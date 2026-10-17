package inspect

import (
	"strconv"

	"github.com/qoqiflow/qoqi/internal/vm"
)

// The natives of the module std.compiler. As every native they hand back
// simple things, texts in arrays; the module builds its structs from them.

func init() {
	text := func(args []vm.Value, i int) string {
		s, _ := vm.ArgString(args, i)
		return s
	}
	number := func(args []vm.Value, i int) int {
		n, _ := vm.ArgInt(args, i)
		return int(n)
	}
	// source checks the two arguments every question starts with: the
	// path of a file and its text
	source := func(args []vm.Value) error {
		if _, err := vm.ArgString(args, 0); err != nil {
			return err
		}
		_, err := vm.ArgString(args, 1)
		return err
	}

	// Per diagnostic: severity, code, message, file, line, column, the
	// column after its last character, suggestion
	vm.RegisterNative("std.compiler.diagnose", 2, func(machine *vm.VM, args []vm.Value) (vm.Value, error) {
		if err := source(args); err != nil {
			return vm.NewNull(), err
		}
		var parts []string
		for _, d := range Check(text(args, 0), text(args, 1)) {
			parts = append(parts, d.Severity, d.Code, d.Message, absolute(d.File),
				strconv.Itoa(d.Line), strconv.Itoa(d.Column), strconv.Itoa(d.EndColumn), d.Suggestion)
		}
		return machine.NewStringArray(parts)
	})

	// Per declaration: kind, name, detail, container, line, column
	vm.RegisterNative("std.compiler.outline", 2, func(machine *vm.VM, args []vm.Value) (vm.Value, error) {
		if err := source(args); err != nil {
			return vm.NewNull(), err
		}
		var parts []string
		for _, s := range Outline(text(args, 0), text(args, 1)) {
			parts = append(parts, s.Kind, s.Name, s.Detail, s.Container, strconv.Itoa(s.Line), strconv.Itoa(s.Column))
		}
		return machine.NewStringArray(parts)
	})

	// Nothing, or: signature, doc, file, line, column of the declaration,
	// then the first column of the name asked about and the one after it
	vm.RegisterNative("std.compiler.describeAt", 4, func(machine *vm.VM, args []vm.Value) (vm.Value, error) {
		if err := source(args); err != nil {
			return vm.NewNull(), err
		}
		d := Describe(text(args, 0), text(args, 1), number(args, 2), number(args, 3))
		if d == nil {
			return machine.NewStringArray(nil)
		}
		// A declaration inside the library is given a file to open
		return machine.NewStringArray([]string{
			d.Signature, d.Doc, OnDisk(d.File), strconv.Itoa(d.Line), strconv.Itoa(d.Column),
			strconv.Itoa(d.StartColumn), strconv.Itoa(d.EndColumn),
		})
	})

	// Per suggestion: label, kind, detail
	vm.RegisterNative("std.compiler.completeAt", 4, func(machine *vm.VM, args []vm.Value) (vm.Value, error) {
		if err := source(args); err != nil {
			return vm.NewNull(), err
		}
		var parts []string
		for _, c := range Complete(text(args, 0), text(args, 1), number(args, 2), number(args, 3)) {
			parts = append(parts, c.Label, c.Kind, c.Detail)
		}
		return machine.NewStringArray(parts)
	})

	vm.RegisterNative("std.compiler.overlay", 2, func(machine *vm.VM, args []vm.Value) (vm.Value, error) {
		if err := source(args); err != nil {
			return vm.NewNull(), err
		}
		Overlay(text(args, 0), text(args, 1))
		return vm.NewNull(), nil
	})
	vm.RegisterNative("std.compiler.forget", 1, func(machine *vm.VM, args []vm.Value) (vm.Value, error) {
		if _, err := vm.ArgString(args, 0); err != nil {
			return vm.NewNull(), err
		}
		Forget(text(args, 0))
		return vm.NewNull(), nil
	})
}
