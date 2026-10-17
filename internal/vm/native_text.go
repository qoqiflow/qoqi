package vm

import (
	"bytes"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"html"
	"io"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// fixedPoint writes x with that many digits after the point. It rounds
// the number as it is written, in its shortest form, a half going away
// from zero as Float.round() has it: 2.5 is "3", 0.125 to two digits
// "0.13", and 1.005 "1.01", though the Float nearest 1.005 is a little
// below it. A number that rounds to zero has no minus sign.
func fixedPoint(x float64, digits int) string {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return strconv.FormatFloat(x, 'f', digits, 64)
	}
	written := strconv.FormatFloat(math.Abs(x), 'f', -1, 64)
	whole, fraction, _ := strings.Cut(written, ".")
	if len(fraction) <= digits {
		fraction += strings.Repeat("0", digits-len(fraction))
	} else {
		up := fraction[digits] >= '5'
		kept := []byte(whole + fraction[:digits])
		for i := len(kept) - 1; up && i >= 0; i-- {
			if kept[i] == '9' {
				kept[i] = '0'
				continue
			}
			kept[i]++
			up = false
		}
		if up {
			kept = append([]byte{'1'}, kept...)
		}
		whole, fraction = string(kept[:len(kept)-digits]), string(kept[len(kept)-digits:])
	}
	text := whole
	if digits > 0 {
		text += "." + fraction
	}
	if x < 0 && strings.Trim(text, "0.") != "" {
		text = "-" + text
	}
	return text
}

// patterns keeps the regular expressions already compiled: a program uses
// few of them, many times.
var patterns sync.Map

func pattern(source string) (*regexp.Regexp, error) {
	if known, ok := patterns.Load(source); ok {
		return known.(*regexp.Regexp), nil
	}
	compiled, err := regexp.Compile(source)
	if err != nil {
		return nil, err
	}
	patterns.Store(source, compiled)
	return compiled, nil
}

// argPattern reads an argument that is a regular expression. std.regex
// only hands over the ones it has checked, so a bad one is a mistake of
// the caller.
func argPattern(args []Value, i int) (*regexp.Regexp, error) {
	source, err := argString(args, i)
	if err != nil {
		return nil, err
	}
	compiled, err := pattern(source)
	if err != nil {
		return nil, fmt.Errorf("'%s' is not a regular expression: %s", source, patternProblem(err))
	}
	return compiled, nil
}

func patternProblem(err error) string {
	return strings.TrimPrefix(err.Error(), "error parsing regexp: ")
}

func argInts(args []Value, i int) ([]int64, error) {
	arr, err := argArray(args, i)
	if err != nil {
		return nil, err
	}
	out := make([]int64, arr.Len())
	for j := range out {
		if !arr.Get(j).IsInt() {
			return nil, fmt.Errorf("argument %d must be an array of Ints, element %d is %s", i+1, j, typeName(arr.Get(j)))
		}
		out[j] = arr.Get(j).AsInt()
	}
	return out, nil
}

func argBool(args []Value, i int) (bool, error) {
	if args[i].IsBool() {
		return args[i].AsBool(), nil
	}
	return false, fmt.Errorf("argument %d must be a Bool, got %s", i+1, typeName(args[i]))
}

// argTable reads an argument that is an array of arrays of strings.
func argTable(args []Value, i int) ([][]string, error) {
	arr, err := argArray(args, i)
	if err != nil {
		return nil, err
	}
	rows := make([][]string, arr.Len())
	for j := range rows {
		row, err := argStrings([]Value{arr.Get(j)}, 0)
		if err != nil {
			return nil, fmt.Errorf("argument %d must be an array of arrays of Strings", i+1)
		}
		rows[j] = row
	}
	return rows, nil
}

func (vm *VM) newIntArray(items []int64) (Value, error) {
	values := make([]Value, len(items))
	for i, item := range items {
		values[i] = NewInt(item)
	}
	return vm.newArrayOf(values)
}

// refused records why a text could not be understood, for the Qoqi side
// to turn into an error value: where (a line and a column, or nothing) and
// the message.
func (vm *VM) refused(where, message string) (Value, error) {
	vm.problemKind, vm.problem = where, message
	return NewNull(), nil
}

// problemNative gives a module the reason of its last refusal.
var problemNative = native{"problem", 0, func(vm *VM, args []Value) (Value, error) {
	return vm.newStringArray([]string{vm.problemKind, vm.problem})
}}

// position turns an offset in bytes into "line:column", both from 1 and
// the column in characters.
func position(text string, offset int) string {
	if offset > len(text) {
		offset = len(text)
	}
	before := text[:offset]
	line := strings.Count(before, "\n") + 1
	column := utf8.RuneCountInString(before[strings.LastIndex(before, "\n")+1:]) + 1
	return fmt.Sprintf("%d:%d", line, column)
}

// csvReader reads the rows of a text, values separated by a character.
// A byte-order mark in front, which some programs write, is not part of
// the first value.
func csvReader(text, separator string) (*csv.Reader, error) {
	if utf8.RuneCountInString(separator) != 1 {
		return nil, fmt.Errorf("the separator must be one character, got '%s'", separator)
	}
	reader := csv.NewReader(strings.NewReader(strings.TrimPrefix(text, "\uFEFF")))
	reader.Comma, _ = utf8.DecodeRuneInString(separator)
	reader.FieldsPerRecord = -1 // rows may differ; std.csv checks
	return reader, nil
}

// jsonTokens lists what a JSON text is made of, one entry per token: the
// punctuation as itself, "s" and a text, "n" and a number, "t", "f", "z"
// (null). The tree is built from them in Qoqi (std/json.qi).
//
// For a text that is not JSON it gives where, as an offset in bytes, and
// why.
func jsonTokens(text string) (tokens []string, offset int, err error) {
	// The whole text is checked first: the decoder below reports what it
	// meets a token at a time, with positions that are not always from the
	// start
	var whole json.RawMessage
	if err := json.Unmarshal([]byte(text), &whole); err != nil {
		var syntax *json.SyntaxError
		if !errors.As(err, &syntax) {
			return nil, 0, err
		}
		at := max(int(syntax.Offset)-1, 0)
		if at >= len(text) || strings.Contains(syntax.Error(), "unexpected end") {
			return nil, len(text), errors.New("the text ends before the value does")
		}
		message := syntax.Error()
		// At the end of a text cut short the scanner is fed a space, and
		// complains about that: a character the text does not have there
		if strings.HasPrefix(message, "invalid character ' '") && text[at] != ' ' {
			return nil, len(text), errors.New("the text ends before the value does")
		}
		// The message quotes one byte; name the character it is part of
		if text[at] >= utf8.RuneSelf && strings.HasPrefix(message, "invalid character '") {
			r, _ := utf8.DecodeRuneInString(text[at:])
			rest := message[len("invalid character '"):]
			message = "invalid character '" + string(r) + rest[strings.Index(rest, "'"):]
		}
		if strings.HasSuffix(message, "after top-level value") {
			message = "there is something after the value"
		}
		return nil, at, errors.New(message)
	}

	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return tokens, 0, nil
		}
		if err != nil {
			return nil, int(decoder.InputOffset()), err
		}
		switch token := token.(type) {
		case json.Delim:
			tokens = append(tokens, string(token))
		case string:
			tokens = append(tokens, "s"+token)
		case json.Number:
			// 1e400 is a number to JSON, and none a Float can hold
			if _, err := strconv.ParseFloat(token.String(), 64); err != nil {
				start := int(decoder.InputOffset()) - len(token.String())
				return nil, start, fmt.Errorf("the number %s is out of range", token.String())
			}
			tokens = append(tokens, "n"+token.String())
		case bool:
			if token {
				tokens = append(tokens, "t")
			} else {
				tokens = append(tokens, "f")
			}
		case nil:
			tokens = append(tokens, "z")
		}
	}
}

// dateLayouts are the ways of writing a date that std.time.parse reads.
var dateLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02T15:04:05-0700",
	"2006-01-02 15:04:05Z07:00",
	"2006-01-02 15:04:05-0700",
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006-01-02T15:04",
	"2006-01-02 15:04",
	"2006-01-02",
	time.RFC1123Z,
	time.RFC1123,
	"02/Jan/2006:15:04:05 -0700",
}

func init() {
	// text wraps a function from a text to a text
	text := func(name string, f func(string) string) native {
		return native{name, 1, func(vm *VM, args []Value) (Value, error) {
			s, err := argString(args, 0)
			if err != nil {
				return NewNull(), err
			}
			return vm.newString(f(s))
		}}
	}
	// decode wraps a function that reads an encoding: null if the text is
	// not in it
	decode := func(name string, f func(string) ([]byte, error)) native {
		return native{name, 1, func(vm *VM, args []Value) (Value, error) {
			s, err := argString(args, 0)
			if err != nil {
				return NewNull(), err
			}
			data, err := f(s)
			if err != nil {
				return NewNull(), nil
			}
			return vm.newString(string(data))
		}}
	}

	registerNatives("std.regex",
		// Why a pattern is not a regular expression, or null if it is one
		native{"mistake", 1, func(vm *VM, args []Value) (Value, error) {
			source, err := argString(args, 0)
			if err != nil {
				return NewNull(), err
			}
			if _, err := pattern(source); err != nil {
				return vm.newString(patternProblem(err))
			}
			return NewNull(), nil
		}},
		native{"test", 2, func(vm *VM, args []Value) (Value, error) {
			re, err := argPattern(args, 0)
			if err != nil {
				return NewNull(), err
			}
			s, err := argString(args, 1)
			if err != nil {
				return NewNull(), err
			}
			return NewBool(re.MatchString(s)), nil
		}},
		// Up to `limit` matches (all of them if negative). One row each:
		// where it starts and ends, in characters, then the text matched
		// and the text of each group.
		native{"search", 3, func(vm *VM, args []Value) (Value, error) {
			re, err := argPattern(args, 0)
			if err != nil {
				return NewNull(), err
			}
			s, err := argString(args, 1)
			if err != nil {
				return NewNull(), err
			}
			limit, err := argInt(args, 2)
			if err != nil {
				return NewNull(), err
			}
			found := re.FindAllStringSubmatchIndex(s, int(limit))
			rows := make([][]string, len(found))
			for i, at := range found {
				row := []string{
					strconv.Itoa(utf8.RuneCountInString(s[:at[0]])),
					strconv.Itoa(utf8.RuneCountInString(s[:at[1]])),
				}
				for g := 0; g < len(at); g += 2 {
					if at[g] < 0 {
						row = append(row, "") // a group that took no part
						continue
					}
					row = append(row, s[at[g]:at[g+1]])
				}
				rows[i] = row
			}
			return vm.newStringTable(rows)
		}},
		// The names of the groups, "" for those without one; the first
		// stands for the whole match
		native{"groupNames", 1, func(vm *VM, args []Value) (Value, error) {
			re, err := argPattern(args, 0)
			if err != nil {
				return NewNull(), err
			}
			return vm.newStringArray(re.SubexpNames())
		}},
		native{"substitute", 3, func(vm *VM, args []Value) (Value, error) {
			re, err := argPattern(args, 0)
			if err != nil {
				return NewNull(), err
			}
			s, err := argString(args, 1)
			if err != nil {
				return NewNull(), err
			}
			with, err := argString(args, 2)
			if err != nil {
				return NewNull(), err
			}
			return vm.newString(re.ReplaceAllString(s, with))
		}},
		native{"pieces", 2, func(vm *VM, args []Value) (Value, error) {
			re, err := argPattern(args, 0)
			if err != nil {
				return NewNull(), err
			}
			s, err := argString(args, 1)
			if err != nil {
				return NewNull(), err
			}
			return vm.newStringArray(re.Split(s, -1))
		}},
		text("escape", regexp.QuoteMeta),
	)

	registerNatives("std.json",
		native{"tokens", 1, func(vm *VM, args []Value) (Value, error) {
			s, err := argString(args, 0)
			if err != nil {
				return NewNull(), err
			}
			tokens, offset, err := jsonTokens(s)
			if err != nil {
				return vm.refused(position(s, offset), strings.TrimPrefix(err.Error(), "json: "))
			}
			return vm.newStringArray(tokens)
		}},
		// A text as JSON writes it: in quotes, with what needs it escaped
		text("quote", func(s string) string {
			var out bytes.Buffer
			encoder := json.NewEncoder(&out)
			encoder.SetEscapeHTML(false)
			encoder.Encode(s)
			return strings.TrimSuffix(out.String(), "\n")
		}),
		// A number as JSON writes it
		native{"numeral", 1, func(vm *VM, args []Value) (Value, error) {
			x, err := argFloat(args, 0)
			if err != nil {
				return NewNull(), err
			}
			data, err := json.Marshal(x)
			if err != nil {
				return vm.newString("null") // infinity and NaN have no JSON
			}
			return vm.newString(string(data))
		}},
		problemNative,
	)

	registerNatives("std.csv",
		native{"rows", 2, func(vm *VM, args []Value) (Value, error) {
			s, err := argString(args, 0)
			if err != nil {
				return NewNull(), err
			}
			separator, err := argString(args, 1)
			if err != nil {
				return NewNull(), err
			}
			reader, err := csvReader(s, separator)
			if err != nil {
				return NewNull(), err
			}
			rows, err := reader.ReadAll()
			if err != nil {
				var parse *csv.ParseError
				if errors.As(err, &parse) {
					return vm.refused(fmt.Sprintf("%d:%d", parse.Line, parse.Column), parse.Err.Error())
				}
				return vm.refused("", err.Error())
			}
			return vm.newStringTable(rows)
		}},
		// The line each row of a text that rows read starts on: a value in
		// quotes may hold line ends, and blank lines are passed over
		native{"starts", 2, func(vm *VM, args []Value) (Value, error) {
			s, err := argString(args, 0)
			if err != nil {
				return NewNull(), err
			}
			separator, err := argString(args, 1)
			if err != nil {
				return NewNull(), err
			}
			reader, err := csvReader(s, separator)
			if err != nil {
				return NewNull(), err
			}
			var lines []int64
			for {
				if _, err := reader.Read(); err != nil {
					break
				}
				line, _ := reader.FieldPos(0)
				lines = append(lines, int64(line))
			}
			return vm.newIntArray(lines)
		}},
		native{"text", 2, func(vm *VM, args []Value) (Value, error) {
			rows, err := argTable(args, 0)
			if err != nil {
				return NewNull(), err
			}
			separator, err := argString(args, 1)
			if err != nil {
				return NewNull(), err
			}
			if utf8.RuneCountInString(separator) != 1 {
				return NewNull(), fmt.Errorf("the separator must be one character, got '%s'", separator)
			}
			var out strings.Builder
			writer := csv.NewWriter(&out)
			writer.Comma, _ = utf8.DecodeRuneInString(separator)
			if err := writer.WriteAll(rows); err != nil {
				return NewNull(), err
			}
			return vm.newString(out.String())
		}},
		problemNative,
	)

	registerNatives("std.encoding",
		text("toBase64", func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }),
		// With its padding or without
		decode("fromBase64", func(s string) ([]byte, error) {
			return base64.RawStdEncoding.DecodeString(strings.TrimRight(s, "="))
		}),
		text("toBase64Url", func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }),
		decode("fromBase64Url", func(s string) ([]byte, error) {
			return base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
		}),
		text("toHex", func(s string) string { return hex.EncodeToString([]byte(s)) }),
		decode("fromHex", hex.DecodeString),
		text("toUrl", url.QueryEscape),
		text("toHtml", html.EscapeString),
		text("fromHtml", html.UnescapeString),
		decode("fromUrl", func(s string) ([]byte, error) {
			decoded, err := url.QueryUnescape(s)
			return []byte(decoded), err
		}),
		// The bytes of a text in UTF-8
		native{"bytes", 1, func(vm *VM, args []Value) (Value, error) {
			s, err := argString(args, 0)
			if err != nil {
				return NewNull(), err
			}
			out := make([]int64, len(s))
			for i := 0; i < len(s); i++ {
				out[i] = int64(s[i])
			}
			return vm.newIntArray(out)
		}},
		// The text those bytes spell, or null if they are not UTF-8
		native{"fromBytes", 1, func(vm *VM, args []Value) (Value, error) {
			values, err := argInts(args, 0)
			if err != nil {
				return NewNull(), err
			}
			data := make([]byte, len(values))
			for i, v := range values {
				if v < 0 || v > 255 {
					return NewNull(), fmt.Errorf("a byte is between 0 and 255, element %d is %d", i, v)
				}
				data[i] = byte(v)
			}
			// A text holds any bytes, as one fromHex gives does
			return vm.newString(string(data))
		}},
	)

	registerNatives("std.hash",
		text("sha256", func(s string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(s))) }),
		text("sha1", func(s string) string { return fmt.Sprintf("%x", sha1.Sum([]byte(s))) }),
		text("md5", func(s string) string { return fmt.Sprintf("%x", md5.Sum([]byte(s))) }),
		native{"crc32", 1, func(vm *VM, args []Value) (Value, error) {
			s, err := argString(args, 0)
			if err != nil {
				return NewNull(), err
			}
			return NewInt(int64(crc32.ChecksumIEEE([]byte(s)))), nil
		}},
		native{"hmacSha256", 2, func(vm *VM, args []Value) (Value, error) {
			key, err := argString(args, 0)
			if err != nil {
				return NewNull(), err
			}
			s, err := argString(args, 1)
			if err != nil {
				return NewNull(), err
			}
			mac := hmac.New(sha256.New, []byte(key))
			mac.Write([]byte(s))
			return vm.newString(hex.EncodeToString(mac.Sum(nil)))
		}},
	)

	registerNatives("std.format",
		// A number with that many digits after the point
		native{"fixed", 2, func(vm *VM, args []Value) (Value, error) {
			x, err := argFloat(args, 0)
			if err != nil {
				return NewNull(), err
			}
			digits, err := argInt(args, 1)
			if err != nil {
				return NewNull(), err
			}
			if digits < 0 || digits > 100 {
				return NewNull(), fmt.Errorf("the number of digits must be between 0 and 100, got %d", digits)
			}
			return vm.newString(fixedPoint(x, int(digits)))
		}},
		// A whole number in another base, 2 to 36
		native{"inBase", 2, func(vm *VM, args []Value) (Value, error) {
			n, err := argInt(args, 0)
			if err != nil {
				return NewNull(), err
			}
			base, err := argInt(args, 1)
			if err != nil {
				return NewNull(), err
			}
			if base < 2 || base > 36 {
				return NewNull(), fmt.Errorf("the base must be between 2 and 36, got %d", base)
			}
			return vm.newString(strconv.FormatInt(n, int(base)))
		}},
		native{"fromBase", 2, func(vm *VM, args []Value) (Value, error) {
			s, err := argString(args, 0)
			if err != nil {
				return NewNull(), err
			}
			base, err := argInt(args, 1)
			if err != nil {
				return NewNull(), err
			}
			if base < 2 || base > 36 {
				return NewNull(), fmt.Errorf("the base must be between 2 and 36, got %d", base)
			}
			n, err := strconv.ParseInt(s, int(base), 64)
			if err != nil || n > MaxInt || n < MinInt {
				return NewNull(), nil
			}
			return NewInt(n), nil
		}},
	)

	// zone is the time zone a date is told in: the machine's, or UTC
	zone := func(local bool) *time.Location {
		if local {
			return time.Local
		}
		return time.UTC
	}
	registerNatives("std.time",
		// A moment taken apart: year, month, day, hour, minute, second,
		// millisecond, day of the week (0 is Sunday), day of the year, and
		// how many minutes that zone is ahead of UTC
		native{"fields", 2, func(vm *VM, args []Value) (Value, error) {
			ms, err := argInt(args, 0)
			if err != nil {
				return NewNull(), err
			}
			local, err := argBool(args, 1)
			if err != nil {
				return NewNull(), err
			}
			t := time.UnixMilli(ms).In(zone(local))
			_, offset := t.Zone()
			return vm.newIntArray([]int64{
				int64(t.Year()), int64(t.Month()), int64(t.Day()),
				int64(t.Hour()), int64(t.Minute()), int64(t.Second()), int64(t.Nanosecond() / 1e6),
				int64(t.Weekday()), int64(t.YearDay()), int64(offset / 60),
			})
		}},
		// The moment of a year, month, day, hour, minute, second and
		// millisecond. Values out of range carry over: day 32 of January
		// is the first of February.
		native{"moment", 2, func(vm *VM, args []Value) (Value, error) {
			f, err := argInts(args, 0)
			if err != nil {
				return NewNull(), err
			}
			local, err := argBool(args, 1)
			if err != nil {
				return NewNull(), err
			}
			if len(f) != 7 {
				return NewNull(), fmt.Errorf("a moment takes 7 numbers, got %d", len(f))
			}
			t := time.Date(int(f[0]), time.Month(f[1]), int(f[2]), int(f[3]), int(f[4]), int(f[5]), int(f[6])*1e6, zone(local))
			return NewInt(t.UnixMilli()), nil
		}},
		// The moment a text names, or null. Without a zone in the text,
		// it is taken as UTC.
		native{"read", 1, func(vm *VM, args []Value) (Value, error) {
			s, err := argString(args, 0)
			if err != nil {
				return NewNull(), err
			}
			for _, layout := range dateLayouts {
				if t, err := time.Parse(layout, strings.TrimSpace(s)); err == nil {
					return NewInt(t.UnixMilli()), nil
				}
			}
			return NewNull(), nil
		}},
	)
}
