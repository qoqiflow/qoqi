package flowd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/qoqiflow/qoqi/internal/flowrt"
)

// Values as Qoqi writes them, read from the data the store keeps:
// Ok("van-3"), Err(NoVan("no van")), Cart { id: "1", amount: 4200 },
// {"a": 1} for a Dict. What the command line prints, as the pages do.

// Qoqi writes data as Qoqi does. Data that is not JSON is given back as
// it is.
func Qoqi(data []byte) string {
	if len(bytes.TrimSpace(data)) == 0 {
		return ""
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	value, err := readValue(decoder)
	if err != nil {
		return string(data)
	}
	var out strings.Builder
	writeQoqi(&out, value)
	return out.String()
}

// object is a JSON object whose keys keep the order they were written in:
// the fields of a struct, in the order of its declaration.
type object struct {
	keys   []string
	values map[string]any
}

func readValue(d *json.Decoder) (any, error) {
	token, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch t := token.(type) {
	case json.Delim:
		switch t {
		case '{':
			o := &object{values: map[string]any{}}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return nil, err
				}
				name, _ := key.(string)
				v, err := readValue(d)
				if err != nil {
					return nil, err
				}
				o.keys = append(o.keys, name)
				o.values[name] = v
			}
			_, err := d.Token()
			return o, err
		case '[':
			items := []any{}
			for d.More() {
				v, err := readValue(d)
				if err != nil {
					return nil, err
				}
				items = append(items, v)
			}
			_, err := d.Token()
			return items, err
		}
	}
	return token, nil
}

// typeName is the last part of a type's name: std.result.Result.Ok is Ok.
func typeName(name string) string {
	return name[strings.LastIndex(name, ".")+1:]
}

func writeQoqi(out *strings.Builder, value any) {
	switch v := value.(type) {
	case nil:
		out.WriteString("null")
	case string:
		quoted, _ := json.Marshal(v)
		out.Write(quoted)
	case json.Number:
		out.WriteString(v.String())
	case bool:
		out.WriteString(strconv.FormatBool(v))
	case []any:
		out.WriteString("[")
		for i, item := range v {
			if i > 0 {
				out.WriteString(", ")
			}
			writeQoqi(out, item)
		}
		out.WriteString("]")
	case *object:
		writeObject(out, v)
	default:
		fmt.Fprint(out, v)
	}
}

func writeObject(out *strings.Builder, o *object) {
	if f, ok := o.values["$float"]; ok {
		fmt.Fprint(out, f)
		return
	}
	if _, ok := o.values["$index"]; ok {
		out.WriteString("index")
		return
	}
	name, _ := o.values["$type"].(string)
	if name == "std.dict.Dict" {
		writeDict(out, o)
		return
	}
	if ms, ok := o.values["ms"].(json.Number); ok && name == "std.time.Duration" {
		n, _ := ms.Int64()
		out.WriteString(DurationLiteral(n))
		return
	}
	var fields []string
	for _, key := range o.keys {
		if !strings.HasPrefix(key, "$") {
			fields = append(fields, key)
		}
	}
	if variant, _ := o.values["$variant"].(bool); variant {
		out.WriteString(typeName(name))
		if len(fields) == 0 {
			return
		}
		// A variant holds its values by place
		out.WriteString("(")
		for i, key := range fields {
			if i > 0 {
				out.WriteString(", ")
			}
			writeQoqi(out, o.values[key])
		}
		out.WriteString(")")
		return
	}
	if name != "" {
		out.WriteString(typeName(name))
		out.WriteString(" ")
	}
	if len(fields) == 0 {
		out.WriteString("{}")
		return
	}
	out.WriteString("{ ")
	for i, key := range fields {
		if i > 0 {
			out.WriteString(", ")
		}
		out.WriteString(key)
		out.WriteString(": ")
		writeQoqi(out, o.values[key])
	}
	out.WriteString(" }")
}

// DurationLiteral writes a length of time as Qoqi does, in the largest
// unit it is a whole number of: 3.seconds(), 90.minutes(), 2.days().
func DurationLiteral(ms int64) string {
	if ms == 0 {
		return "0.seconds()"
	}
	for _, unit := range []struct {
		name string
		size int64
	}{{"days", 86400000}, {"hours", 3600000}, {"minutes", 60000}, {"seconds", 1000}, {"milliseconds", 1}} {
		if ms%unit.size == 0 {
			if ms < 0 {
				return fmt.Sprintf("(%d).%s()", ms/unit.size, unit.name)
			}
			return fmt.Sprintf("%d.%s()", ms/unit.size, unit.name)
		}
	}
	return ""
}

// writeDict writes a Dict of std.dict as its literal: its keys and its
// values, the removed ones left out.
func writeDict(out *strings.Builder, o *object) {
	keys, _ := o.values["keys"].([]any)
	values, _ := o.values["values"].([]any)
	dead, _ := o.values["dead"].([]any)
	out.WriteString("{")
	first := true
	for i, key := range keys {
		if i < len(dead) && dead[i] == true {
			continue
		}
		if !first {
			out.WriteString(", ")
		}
		first = false
		writeQoqi(out, key)
		out.WriteString(": ")
		if i < len(values) {
			writeQoqi(out, values[i])
		}
	}
	out.WriteString("}")
}

// Awaited is what an instance waits for, by name: other instances, and
// its inboxes ("inbox:notes"). Its own cancellation ("cancel:", a flow
// that hears it) goes without saying, and the answers of the application
// to its steps are Answers.
func Awaited(v View) (instances, inboxes []string) {
	for _, a := range v.Awaits {
		switch {
		case strings.HasPrefix(a, "cancel:"), strings.HasPrefix(a, "answer:"):
		case strings.HasPrefix(a, "inbox:"):
			inboxes = append(inboxes, strings.TrimPrefix(a, "inbox:"))
		default:
			instances = append(instances, a)
		}
	}
	return instances, inboxes
}

// Answers are the steps whose answer an instance waits for, from the
// application that carries them out ("answer:<key>"), by name.
func Answers(v View) []string {
	var steps []string
	for _, a := range v.Awaits {
		if key, ok := strings.CutPrefix(a, "answer:"); ok {
			step := key[strings.LastIndex(key, ":")+1:]
			step, _, _ = strings.Cut(step, "#")
			steps = append(steps, step)
		}
	}
	return steps
}

// Doing is what an instance is doing now, in a few words: what qoqi flow
// ls says beside each one, as the pages do.
func Doing(v View, now time.Time) string {
	switch flowrt.Status(v.Status) {
	case flowrt.Done:
		return "returned " + Qoqi(v.Result)
	case flowrt.Failed:
		if v.Ended != "" {
			return "ended as it was: " + v.Ended
		}
		if v.Cancel != "" {
			return "cancelled: " + v.Cancel
		}
		return "failed"
	case flowrt.Stopped:
		first, _, _ := strings.Cut(v.Stopped, "\n")
		return strings.TrimSpace(first)
	case flowrt.Running:
		if v.Phase == flowrt.Finish {
			return "undoing what it did"
		}
		return "running"
	}
	if v.Cancel != "" {
		return "cancelling: " + v.Cancel
	}
	if v.Phase == flowrt.Finish {
		return "undoing what it did"
	}
	instances, inboxes := Awaited(v)
	answers := Answers(v)
	if len(instances)+len(inboxes)+len(answers) > 0 {
		parts := append([]string(nil), instances...)
		if len(inboxes) > 0 {
			parts = append(parts, "a message on "+strings.Join(inboxes, " or "))
		}
		if len(answers) > 0 {
			parts = append(parts, "the application's answer to "+strings.Join(answers, " and "))
		}
		return "waits for " + strings.Join(parts, ", ")
	}
	if v.WakeAt != "" {
		at, err := time.Parse(time.RFC3339Nano, v.WakeAt)
		if err == nil {
			if !at.After(now.Add(500 * time.Millisecond)) {
				return "due now"
			}
			return "wakes " + Relative(at, now)
		}
	}
	return "waits"
}

// Relative is a moment to come as a person says it: in 30 s, in 2 min, in
// 3 days.
func Relative(at, now time.Time) string {
	d := at.Sub(now)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("in %d s", int(d.Round(time.Second)/time.Second))
	case d < time.Hour:
		return fmt.Sprintf("in %d min", int(d.Round(time.Minute)/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("in %d h", int(d.Round(time.Hour)/time.Hour))
	}
	return fmt.Sprintf("in %d days", int(d.Round(24*time.Hour)/(24*time.Hour)))
}
