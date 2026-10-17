package vm

import (
	cryptorand "crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/rand"
	"os"
	"strings"
	"syscall"
	"time"
)

// reasonOf says why the operating system refused something, without the
// name of the operation and the path, which the program already knows:
// "no such file or directory", "permission denied", "is a directory".
func reasonOf(err error) string {
	for {
		inner := errors.Unwrap(err)
		if inner == nil {
			return err.Error()
		}
		err = inner
	}
}

// kindOfFileError sorts a failure of the file system into the kinds the
// standard library tells apart (std.io.ErrorKind).
func kindOfFileError(err error) string {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "not found"
	case errors.Is(err, fs.ErrPermission):
		return "permission denied"
	case errors.Is(err, syscall.EISDIR):
		return "is a directory"
	case errors.Is(err, syscall.ENOTDIR):
		return "not a directory"
	case errors.Is(err, fs.ErrExist):
		return "already exists"
	}
	return "other"
}

var errIsDirectory = syscall.EISDIR

func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// start is when the process started; std.time.ticks counts from it.
var start = time.Now()

func init() {
	// write wraps writing a text to a file. It reports whether it worked;
	// when it did not, the reason is kept for std.io.problem.
	write := func(name string, flags int) native {
		return native{name, 2, func(vm *VM, args []Value) (Value, error) {
			path, err := argString(args, 0)
			if err != nil {
				return NewNull(), err
			}
			text, err := argString(args, 1)
			if err != nil {
				return NewNull(), err
			}
			file, err := os.OpenFile(path, flags, 0644)
			if err != nil {
				vm.problemKind, vm.problem = kindOfFileError(err), reasonOf(err)
				return NewBool(false), nil
			}
			_, writeErr := file.WriteString(text)
			closeErr := file.Close()
			if writeErr != nil || closeErr != nil {
				vm.problemKind, vm.problem = kindOfFileError(firstError(writeErr, closeErr)), reasonOf(firstError(writeErr, closeErr))
				return NewBool(false), nil
			}
			return NewBool(true), nil
		}}
	}

	registerNatives("std.io",
		// A line of input without its end of line, or null at the end
		native{"readLine", 0, func(vm *VM, args []Value) (Value, error) {
			// Other tasks go on while the line is typed
			input := vm.input()
			var line string
			var err error
			if gone := vm.blocking(func() {
				vm.inLock.Lock()
				line, err = input.ReadString('\n')
				vm.inLock.Unlock()
			}); gone != nil {
				return NewNull(), gone
			}
			if err != nil && (err != io.EOF || line == "") {
				return NewNull(), nil
			}
			return vm.newString(strings.TrimRight(line, "\r\n"))
		}},
		// That many bytes of input as a text, or null if the input ends
		// before: for what comes in pieces of a known size, not in lines
		native{"readBytes", 1, func(vm *VM, args []Value) (Value, error) {
			count, err := argInt(args, 0)
			if err != nil {
				return NewNull(), err
			}
			if count < 0 || count > maxBody {
				return NewNull(), fmt.Errorf("cannot read %d bytes at once", count)
			}
			input := vm.input()
			buffer := make([]byte, count)
			var failed error
			if gone := vm.blocking(func() {
				vm.inLock.Lock()
				_, failed = io.ReadFull(input, buffer)
				vm.inLock.Unlock()
			}); gone != nil {
				return NewNull(), gone
			}
			if failed != nil {
				return NewNull(), nil
			}
			return vm.newString(string(buffer))
		}},
		// The content of a file, or null if it cannot be read; the reason
		// is then kept for std.io.problem
		native{"readText", 1, func(vm *VM, args []Value) (Value, error) {
			path, err := argString(args, 0)
			if err != nil {
				return NewNull(), err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				vm.problemKind, vm.problem = kindOfFileError(err), reasonOf(err)
				return NewNull(), nil
			}
			return vm.newString(string(data))
		}},
		write("writeText", os.O_WRONLY|os.O_CREATE|os.O_TRUNC),
		write("appendText", os.O_WRONLY|os.O_CREATE|os.O_APPEND),
		// What is known about the last reading or writing of a file that
		// did not work: its kind, then the message of the operating system
		native{"problem", 0, func(vm *VM, args []Value) (Value, error) {
			return vm.newStringArray([]string{vm.problemKind, vm.problem})
		}},
		native{"exists", 1, func(vm *VM, args []Value) (Value, error) {
			path, err := argString(args, 0)
			if err != nil {
				return NewNull(), err
			}
			_, statErr := os.Stat(path)
			return NewBool(statErr == nil), nil
		}},
		// Deletes a file or an empty folder: whether it worked, the reason
		// kept for problem
		native{"removeFile", 1, func(vm *VM, args []Value) (Value, error) {
			path, err := argString(args, 0)
			if err != nil {
				return NewNull(), err
			}
			if err := os.Remove(path); err != nil {
				return vm.failedFile(err, NewBool(false))
			}
			return NewBool(true), nil
		}},
		// Writes to the error output, which print and println do not reach
		native{"printError", 1, func(vm *VM, args []Value) (Value, error) {
			text, err := argString(args, 0)
			if err != nil {
				return NewNull(), err
			}
			fmt.Fprint(os.Stderr, text)
			return NewNull(), nil
		}},
		// The value of an environment variable, or null if it is not set
		native{"env", 1, func(vm *VM, args []Value) (Value, error) {
			name, err := argString(args, 0)
			if err != nil {
				return NewNull(), err
			}
			value, ok := os.LookupEnv(name)
			if !ok {
				return NewNull(), nil
			}
			return vm.newString(value)
		}},
	)

	registerNatives("std.time",
		// Milliseconds since 1 January 1970
		native{"now", 0, func(vm *VM, args []Value) (Value, error) {
			return NewInt(vm.now()), nil
		}},
		// Microseconds since the program started, for measuring durations:
		// unlike now it never jumps when the clock is set
		native{"ticks", 0, func(vm *VM, args []Value) (Value, error) {
			return NewInt(time.Since(start).Microseconds()), nil
		}},
	)

	registerNatives("std.random",
		native{"seed", 1, func(vm *VM, args []Value) (Value, error) {
			n, err := argInt(args, 0)
			if err != nil {
				return NewNull(), err
			}
			vm.rng = rand.New(rand.NewSource(n))
			return NewNull(), nil
		}},
		// A whole number from 0 up to, not including, the limit
		native{"below", 1, func(vm *VM, args []Value) (Value, error) {
			limit, err := argInt(args, 0)
			if err != nil {
				return NewNull(), err
			}
			if limit <= 0 {
				return NewNull(), fmt.Errorf("the limit must be at least 1, got %d", limit)
			}
			return NewInt(vm.random().Int63n(limit)), nil
		}},
		// A whole number from low to high, both included: the numbers
		// below would give, moved up to low
		native{"between", 2, func(vm *VM, args []Value) (Value, error) {
			low, err := argInt(args, 0)
			if err != nil {
				return NewNull(), err
			}
			high, err := argInt(args, 1)
			if err != nil {
				return NewNull(), err
			}
			if low > high {
				return NewNull(), fmt.Errorf("the low end %d is above the high end %d", low, high)
			}
			return NewInt(low + vm.random().Int63n(high-low+1)), nil
		}},
		// A number from 0 up to, not including, 1
		native{"float", 0, func(vm *VM, args []Value) (Value, error) {
			return NewNumber(vm.random().Float64()), nil
		}},
		// A version 4 UUID from the system's secure generator: never the
		// same twice, whatever seed the program gave
		native{"uuid", 0, func(vm *VM, args []Value) (Value, error) {
			var b [16]byte
			if _, err := cryptorand.Read(b[:]); err != nil {
				return NewNull(), err
			}
			b[6] = b[6]&0x0f | 0x40
			b[8] = b[8]&0x3f | 0x80
			return vm.newString(fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]))
		}},
		// That many bytes from the secure generator, in hexadecimal
		native{"token", 1, func(vm *VM, args []Value) (Value, error) {
			n, err := argInt(args, 0)
			if err != nil {
				return NewNull(), err
			}
			if n < 1 || n > 1024 {
				return NewNull(), fmt.Errorf("a token is 1 to 1024 bytes, not %d", n)
			}
			b := make([]byte, n)
			if _, err := cryptorand.Read(b); err != nil {
				return NewNull(), err
			}
			return vm.newString(hex.EncodeToString(b))
		}},
	)
}
