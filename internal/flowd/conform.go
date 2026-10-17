package flowd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Conform writes the fields of a flow as the program's data, from JSON
// as a person writes it:
//
//	{"cart": {"id": "1", "amount": 4200}}           a struct needs no "$type"
//	"Approved", {"Refused": "too much"}             a variant, by its name
//	{"Refused": ["ada", "too much"]}                one of several values
//	{"$type": "Decision.Approved", "$variant": true} data as the program writes it, kept
//
// A field missing, one the struct has not, a text where a number goes:
// each is refused, and the message says where.
func (p *Schema) Conform(flow string, fields json.RawMessage) (json.RawMessage, error) {
	var info *FlowInfo
	for _, f := range p.Flows {
		if f.Name == flow {
			info = f
		}
	}
	if info == nil {
		return nil, fmt.Errorf("the program has no flow %s", flow)
	}
	decoder := json.NewDecoder(bytes.NewReader(fields))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("the fields are not JSON: %v", err)
	}
	if value == nil {
		value = map[string]any{}
	}
	out, err := p.conformStruct(value, flow, info.Fields, "")
	if err != nil {
		return nil, err
	}
	return json.Marshal(out)
}

func (p *Schema) conformStruct(value any, name string, fields []*Field, path string) (any, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s is a %s, written as an object: {%s}", where(path, name), short(name), fieldList(fields))
	}
	// A name misspelt says so first: "town" is wrong before "city" is missing
	known := map[string]bool{"$type": true}
	for _, f := range fields {
		known[f.Name] = true
	}
	var unknown []string
	for key := range object {
		if !known[key] {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, fmt.Errorf("%s has no field %s: it has %s", where(path, name), strings.Join(unknown, ", "), fieldList(fields))
	}
	out := newFields("$type", name)
	for _, f := range fields {
		v, given := object[f.Name]
		if !given {
			if f.Type.Kind == "option" {
				out.set(f.Name, nil)
				continue
			}
			return nil, fmt.Errorf("%s is missing: a %s", join(path, f.Name), f.Type.Text)
		}
		c, err := p.conform(v, f.Type, join(path, f.Name))
		if err != nil {
			return nil, err
		}
		out.set(f.Name, c)
	}
	return out, nil
}

func (p *Schema) conform(value any, t *Type, path string) (any, error) {
	switch t.Kind {
	case "int":
		n, ok := value.(json.Number)
		i, err := n.Int64()
		if !ok || err != nil {
			return nil, fmt.Errorf("%s is an Int, not %s", path, show(value))
		}
		if i > 1<<47-1 || i < -(1<<47) {
			return nil, fmt.Errorf("%s is out of the range of an Int, 48 bits", path)
		}
		return n, nil
	case "float":
		n, ok := value.(json.Number)
		f, err := n.Float64()
		if !ok || err != nil || math.IsInf(f, 0) {
			return nil, fmt.Errorf("%s is a Float, not %s", path, show(value))
		}
		text := n.String()
		if !strings.ContainsAny(text, ".eE") {
			text += ".0"
		}
		return json.Number(text), nil
	case "bool":
		if _, ok := value.(bool); !ok {
			return nil, fmt.Errorf("%s is a Bool, true or false, not %s", path, show(value))
		}
		return value, nil
	case "string":
		if _, ok := value.(string); !ok {
			return nil, fmt.Errorf("%s is a String, not %s", path, show(value))
		}
		return value, nil
	case "option":
		if value == nil {
			return nil, nil
		}
		return p.conform(value, t.Elem, path)
	case "array":
		items, ok := value.([]any)
		if !ok {
			return nil, fmt.Errorf("%s is a %s, written as an array, not %s", path, t.Text, show(value))
		}
		out := make([]any, len(items))
		for i, item := range items {
			c, err := p.conform(item, t.Elem, fmt.Sprintf("%s[%d]", path, i))
			if err != nil {
				return nil, err
			}
			out[i] = c
		}
		return out, nil
	case "duration":
		if object, ok := value.(map[string]any); ok {
			if _, isData := object["$type"]; isData {
				// Data as the program writes it: kept
				return value, nil
			}
		}
		text, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("%s is a Duration, written as a text with its unit: \"90s\", \"1h30m\", \"2d\", not %s", path, show(value))
		}
		ms, err := ParseDuration(text)
		if err != nil {
			return nil, fmt.Errorf("%s: %v", path, err)
		}
		out := newFields("$type", "std.time.Duration")
		out.set("ms", ms)
		return out, nil
	case "dict":
		object, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s is a %s, written as an object, not %s", path, t.Text, show(value))
		}
		if _, isData := object["$type"]; isData {
			// Data as the program writes it: kept
			return value, nil
		}
		// A Dict of std.dict, as the program holds one: its keys and its
		// values side by side, with no index while it has none. The keys
		// of a JSON object have no order the decoder keeps: alphabetical
		keys := make([]string, 0, len(object))
		for key := range object {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		values := make([]any, len(keys))
		for i, key := range keys {
			c, err := p.conform(object[key], t.Elem, fmt.Sprintf("%s[%q]", path, key))
			if err != nil {
				return nil, err
			}
			values[i] = c
		}
		out := newFields("$type", "std.dict.Dict")
		for _, field := range []struct {
			name  string
			value any
		}{{"keys", keys}, {"values", values}, {"count", len(keys)}, {"index", nil}, {"dead", nil}, {"removed", 0}} {
			out.set(field.name, field.value)
		}
		return out, nil
	case "struct":
		d := p.Types[t.Name]
		if d == nil {
			return value, nil
		}
		if object, ok := value.(map[string]any); ok {
			if given, ok := object["$type"].(string); ok && given != d.Name {
				return nil, fmt.Errorf("%s is a %s, not a %s", path, d.Text, short(given))
			}
		}
		return p.conformStruct(value, d.Name, d.Fields, path)
	case "enum":
		d := p.Types[t.Name]
		if d == nil {
			return value, nil
		}
		return p.conformVariant(value, d, path)
	}
	// What the pages cannot know the shape of is kept as it is written
	return value, nil
}

// conformVariant reads a value of an enum, in any of the forms Conform
// takes.
func (p *Schema) conformVariant(value any, d *Decl, path string) (any, error) {
	var name string
	var given any
	hasValues := false
	switch v := value.(type) {
	case string:
		name = v
	case map[string]any:
		if typ, ok := v["$type"].(string); ok {
			// Data as the program writes it: Decision.Refused, values by place
			name = strings.TrimPrefix(typ, d.Name+".")
			rest := map[string]any{}
			for key, item := range v {
				if !strings.HasPrefix(key, "$") {
					rest[key] = item
				}
			}
			given, hasValues = rest, true
		} else if len(v) == 1 {
			for key, item := range v {
				name, given, hasValues = key, item, true
			}
		}
	}
	var variant *Variant
	for _, candidate := range d.Variants {
		if candidate.Name == name {
			variant = candidate
		}
	}
	if variant == nil {
		return nil, fmt.Errorf("%s is a %s: one of %s, not %s", path, d.Text, variantList(d), show(value))
	}
	out := newFields("$type", d.Name+"."+variant.Name)
	out.set("$variant", true)
	if len(variant.Values) == 0 {
		return out, nil
	}
	if !hasValues {
		return nil, fmt.Errorf("%s: %s holds %s", path, variant.Name, valueList(variant))
	}
	// The values: by name, by place, or the one value alone
	byName := map[string]any{}
	switch g := given.(type) {
	case map[string]any:
		if len(variant.Values) == 1 && !variant.Named && g["0"] == nil {
			byName["0"] = g
		} else {
			byName = g
		}
	case []any:
		if len(variant.Values) == 1 && !variant.Named {
			byName["0"] = g
			if len(g) == 1 && variant.Values[0].Type.Kind != "array" {
				byName["0"] = g[0]
			}
		} else {
			if len(g) != len(variant.Values) {
				return nil, fmt.Errorf("%s: %s holds %s, not %d values", path, variant.Name, valueList(variant), len(g))
			}
			for i, item := range g {
				byName[variant.Values[i].Name] = item
			}
		}
	default:
		if len(variant.Values) != 1 {
			return nil, fmt.Errorf("%s: %s holds %s", path, variant.Name, valueList(variant))
		}
		byName[variant.Values[0].Name] = g
	}
	// A variant holds its values by place, named or not: the compiler
	// writes Label { text: t, loud: l } as Label(t, l)
	for i, f := range variant.Values {
		place := fmt.Sprint(i)
		item, ok := byName[f.Name]
		if !ok {
			item, ok = byName[place]
		}
		if !ok && f.Type.Kind != "option" {
			return nil, fmt.Errorf("%s: %s holds %s", path, variant.Name, valueList(variant))
		}
		c, err := p.conform(item, f.Type, join(path, variant.Name+"."+f.Name))
		if err != nil {
			return nil, err
		}
		out.set(place, c)
	}
	return out, nil
}

func join(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

func where(path, name string) string {
	if path == "" {
		return "the fields of " + short(name)
	}
	return path
}

func fieldList(fields []*Field) string {
	var parts []string
	for _, f := range fields {
		parts = append(parts, f.Name+": "+f.Type.Text)
	}
	return strings.Join(parts, ", ")
}

func variantList(d *Decl) string {
	var parts []string
	for _, v := range d.Variants {
		parts = append(parts, v.Name)
	}
	return strings.Join(parts, ", ")
}

func valueList(v *Variant) string {
	var parts []string
	for _, f := range v.Values {
		if v.Named {
			parts = append(parts, f.Name+": "+f.Type.Text)
		} else {
			parts = append(parts, f.Type.Text)
		}
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

func show(value any) string {
	switch v := value.(type) {
	case nil:
		return "null"
	case string:
		return fmt.Sprintf("the text %q", v)
	case json.Number:
		return "the number " + v.String()
	case bool:
		return fmt.Sprint(v)
	case []any:
		return "an array"
	case map[string]any:
		return "an object"
	}
	return fmt.Sprint(value)
}

// ConformMessage writes a message for an inbox of a flow as the program's
// data, from what a person writes, as Conform does for a start.
func (p *Schema) ConformMessage(flow, inbox string, message json.RawMessage) (json.RawMessage, error) {
	var box *Field
	for _, f := range p.Flows {
		if f.Name != flow {
			continue
		}
		for _, b := range f.Inboxes {
			if b.Name == inbox {
				box = b
			}
		}
	}
	if box == nil || box.Type.Elem == nil {
		return nil, fmt.Errorf("%s has no inbox %s", flow, inbox)
	}
	decoder := json.NewDecoder(bytes.NewReader(message))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("the message is not JSON: %v", err)
	}
	out, err := p.conform(value, box.Type.Elem, "the message")
	if err != nil {
		return nil, err
	}
	return json.Marshal(out)
}

// fields is a JSON object written in the order its fields were set: a
// struct's in the order of its declaration, a variant's by place, as the
// program writes them.
type fields struct {
	keys   []string
	values map[string]any
}

func newFields(key string, value any) *fields {
	f := &fields{values: map[string]any{}}
	f.set(key, value)
	return f
}

func (f *fields) set(key string, value any) {
	if _, ok := f.values[key]; !ok {
		f.keys = append(f.keys, key)
	}
	f.values[key] = value
}

func (f *fields) MarshalJSON() ([]byte, error) {
	var out bytes.Buffer
	out.WriteByte('{')
	for i, key := range f.keys {
		if i > 0 {
			out.WriteByte(',')
		}
		name, _ := json.Marshal(key)
		value, err := json.Marshal(f.values[key])
		if err != nil {
			return nil, err
		}
		out.Write(name)
		out.WriteByte(':')
		out.Write(value)
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}

// durationUnits are the units a Duration is written with, in milliseconds.
var durationUnits = map[string]float64{
	"ms": 1, "s": 1000, "sec": 1000, "second": 1000, "seconds": 1000,
	"m": 60000, "min": 60000, "minute": 60000, "minutes": 60000,
	"h": 3600000, "hour": 3600000, "hours": 3600000,
	"d": 86400000, "day": 86400000, "days": 86400000,
	"w": 604800000, "week": 604800000, "weeks": 604800000,
}

var durationPart = regexp.MustCompile(`^\s*(\d+(?:\.\d+)?)\s*([a-z]+)`)

// ParseDuration reads a length of time as a person writes it, in
// milliseconds: "90s", "1h30m", "1.5h", "2 days", "250ms", and the way a
// Duration prints itself, "1 h 30 min".
func ParseDuration(text string) (int64, error) {
	rest := strings.ToLower(strings.TrimSpace(text))
	if rest == "" {
		return 0, fmt.Errorf("a Duration is written with its unit: \"90s\", \"1h30m\", \"2d\"")
	}
	total := 0.0
	for rest != "" {
		m := durationPart.FindStringSubmatch(rest)
		if m == nil {
			return 0, fmt.Errorf("%q is not a Duration: write a number and its unit, as in \"90s\", \"1h30m\", \"2d\"", text)
		}
		size, known := durationUnits[m[2]]
		if !known {
			return 0, fmt.Errorf("%q is not a unit of a Duration: ms, s, m, h, d or w", m[2])
		}
		n, _ := strconv.ParseFloat(m[1], 64)
		total += n * size
		rest = strings.TrimSpace(rest[len(m[0]):])
	}
	return int64(math.Round(total)), nil
}
