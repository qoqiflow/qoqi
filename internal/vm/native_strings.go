package vm

import (
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
	"unsafe"
)

// Positions in a string count characters, not bytes, as `text[i]` and
// strings.len do.

func init() {
	// transform wraps a function from a string to a string
	transform := func(name string, f func(string) string) native {
		return native{name, 1, func(vm *VM, args []Value) (Value, error) {
			s, err := argString(args, 0)
			if err != nil {
				return NewNull(), err
			}
			return vm.newString(f(s))
		}}
	}
	// find wraps a search: the position in characters, or null
	find := func(name string, f func(s, part string) int) native {
		return native{name, 2, func(vm *VM, args []Value) (Value, error) {
			s, err := argString(args, 0)
			if err != nil {
				return NewNull(), err
			}
			part, err := argString(args, 1)
			if err != nil {
				return NewNull(), err
			}
			at := f(s, part)
			if at < 0 {
				return NewNull(), nil
			}
			return NewInt(int64(utf8.RuneCountInString(s[:at]))), nil
		}}
	}

	// every wraps a question asked of each character: true when every
	// character answers yes, and there is at least one
	every := func(name string, yes func(rune) bool) native {
		return native{name, 1, func(vm *VM, args []Value) (Value, error) {
			s, err := argString(args, 0)
			if err != nil {
				return NewNull(), err
			}
			if s == "" {
				return NewBool(false), nil
			}
			for _, r := range s {
				if !yes(r) {
					return NewBool(false), nil
				}
			}
			return NewBool(true), nil
		}}
	}
	// cased reports whether a text has letters, all of one case; what is
	// not a letter (a digit, a space) does not count
	cased := func(name string, ofCase func(rune) bool) native {
		return native{name, 1, func(vm *VM, args []Value) (Value, error) {
			s, err := argString(args, 0)
			if err != nil {
				return NewNull(), err
			}
			letters := false
			for _, r := range s {
				if unicode.IsLetter(r) {
					if !ofCase(r) {
						return NewBool(false), nil
					}
					letters = true
				}
			}
			return NewBool(letters), nil
		}}
	}

	registerNatives("std.strings",
		// Letters of any alphabet, and digits of any script
		every("isLetters", unicode.IsLetter),
		every("isAlphanumeric", func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }),
		every("isSpaces", unicode.IsSpace),
		cased("isUpper", unicode.IsUpper),
		cased("isLower", unicode.IsLower),
		// How many bytes a text takes written in UTF-8, which is not how
		// many characters it has
		native{"byteLength", 1, func(vm *VM, args []Value) (Value, error) {
			s, err := argString(args, 0)
			if err != nil {
				return NewNull(), err
			}
			return NewInt(int64(len(s))), nil
		}},
		native{"substring", 3, func(vm *VM, args []Value) (Value, error) {
			if _, err := argString(args, 0); err != nil {
				return NewNull(), err
			}
			from, err := argInt(args, 1)
			if err != nil {
				return NewNull(), err
			}
			to, err := argInt(args, 2)
			if err != nil {
				return NewNull(), err
			}
			// Positions are found through what the text keeps of where
			// its characters are, not by going through it from the start
			text := (*StringObj)(unsafe.Pointer(header(args[0])))
			if length := int64(text.RuneLen()); from < 0 || to > length || from > to {
				return NewNull(), fmt.Errorf("range %d..%d is outside a text of %s", from, to, characters(length))
			}
			if from == 0 && to == int64(text.RuneLen()) {
				return args[0], nil
			}
			return vm.newString(text.Slice(int(from), int(to)))
		}},
		find("indexOf", strings.Index),
		find("lastIndexOf", strings.LastIndex),
		transform("upper", strings.ToUpper),
		transform("lower", strings.ToLower),
		transform("trim", strings.TrimSpace),
		transform("trimStart", func(s string) string { return strings.TrimLeftFunc(s, unicode.IsSpace) }),
		transform("trimEnd", func(s string) string { return strings.TrimRightFunc(s, unicode.IsSpace) }),
		native{"split", 2, func(vm *VM, args []Value) (Value, error) {
			s, err := argString(args, 0)
			if err != nil {
				return NewNull(), err
			}
			sep, err := argString(args, 1)
			if err != nil {
				return NewNull(), err
			}
			// An empty separator splits into characters, as strings.Split does
			return vm.newStringArray(strings.Split(s, sep))
		}},
		native{"join", 2, func(vm *VM, args []Value) (Value, error) {
			parts, err := argStrings(args, 0)
			if err != nil {
				return NewNull(), err
			}
			sep, err := argString(args, 1)
			if err != nil {
				return NewNull(), err
			}
			size := uint64(0)
			for i, part := range parts {
				size += uint64(len(part))
				if i > 0 {
					size += uint64(len(sep))
				}
			}
			if err := vm.roomForText(size); err != nil {
				return NewNull(), err
			}
			return vm.newString(strings.Join(parts, sep))
		}},
		native{"replace", 3, func(vm *VM, args []Value) (Value, error) {
			s, err := argString(args, 0)
			if err != nil {
				return NewNull(), err
			}
			old, err := argString(args, 1)
			if err != nil {
				return NewNull(), err
			}
			with, err := argString(args, 2)
			if err != nil {
				return NewNull(), err
			}
			if old == "" {
				return NewNull(), fmt.Errorf("the text to replace is empty")
			}
			if len(with) > len(old) {
				grows := uint64(strings.Count(s, old)) * uint64(len(with)-len(old))
				if err := vm.roomForText(uint64(len(s)) + grows); err != nil {
					return NewNull(), err
				}
			}
			return vm.newString(strings.ReplaceAll(s, old, with))
		}},
		native{"repeat", 2, func(vm *VM, args []Value) (Value, error) {
			s, err := argString(args, 0)
			if err != nil {
				return NewNull(), err
			}
			n, err := argInt(args, 1)
			if err != nil {
				return NewNull(), err
			}
			if n < 0 {
				return NewNull(), fmt.Errorf("cannot repeat a text %d times", n)
			}
			// Checked before building it: the heap would only refuse the
			// result once Go had already allocated it
			if n > 0 && uint64(len(s)) > maxTextBytes/uint64(n) {
				return NewNull(), fmt.Errorf("out of memory: a text of %d bytes, %d times, cannot be made", len(s), n)
			}
			if err := vm.roomForText(uint64(len(s)) * uint64(n)); err != nil {
				return NewNull(), err
			}
			return vm.newString(strings.Repeat(s, int(n)))
		}},
		native{"parseInt", 1, func(vm *VM, args []Value) (Value, error) {
			s, err := argString(args, 0)
			if err != nil {
				return NewNull(), err
			}
			n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
			if err != nil || n < MinInt || n > MaxInt {
				return NewNull(), nil
			}
			return NewInt(n), nil
		}},
		native{"parseFloat", 1, func(vm *VM, args []Value) (Value, error) {
			s, err := argString(args, 0)
			if err != nil {
				return NewNull(), err
			}
			s = strings.TrimSpace(s)
			if !isDecimal(s) {
				return NewNull(), nil
			}
			f, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return NewNull(), nil
			}
			return NewNumber(f), nil
		}},
		native{"code", 1, func(vm *VM, args []Value) (Value, error) {
			s, err := argString(args, 0)
			if err != nil {
				return NewNull(), err
			}
			if s == "" {
				return NewNull(), fmt.Errorf("an empty text has no first character")
			}
			r, _ := utf8.DecodeRuneInString(s)
			return NewInt(int64(r)), nil
		}},
		native{"fromCode", 1, func(vm *VM, args []Value) (Value, error) {
			n, err := argInt(args, 0)
			if err != nil {
				return NewNull(), err
			}
			if n < 0 || n > utf8.MaxRune || !utf8.ValidRune(rune(n)) {
				return NewNull(), fmt.Errorf("%d is not the code of a character", n)
			}
			return vm.newString(string(rune(n)))
		}},
		native{"hash", 1, func(vm *VM, args []Value) (Value, error) {
			s, err := argString(args, 0)
			if err != nil {
				return NewNull(), err
			}
			h := fnv.New64a()
			h.Write([]byte(s))
			// Kept non-negative and within an Int
			return NewInt(int64(h.Sum64() & uint64(MaxInt))), nil
		}},
	)
}

// characters says how many characters there are: "1 character", "3 characters".
func characters(n int64) string {
	if n == 1 {
		return "1 character"
	}
	return fmt.Sprintf("%d characters", n)
}

// isDecimal says whether a text is a number written the plain way, which
// is all parseFloat takes: a sign or none, digits with a point among them
// or not, at least one digit, then an exponent or none: "2.5", "-1e3",
// ".5", "5.", "+7". Not "nan", "inf", "1_000" or "0x1p4", which Go would
// read too.
func isDecimal(s string) bool {
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	digits := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
		digits++
	}
	if i < len(s) && s[i] == '.' {
		i++
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
			digits++
		}
	}
	if digits == 0 {
		return false
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		exponent := 0
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
			exponent++
		}
		if exponent == 0 {
			return false
		}
	}
	return i == len(s)
}
