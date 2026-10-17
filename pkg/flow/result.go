package flow

import (
	"encoding/json"
	"fmt"
)

// Result is Qoqi's Result<T, E>: Ok with a T, or Err with an E, written
// at the boundary {"Ok": ...} or {"Err": ...}. An Err is an answer, not a
// failure: the flow decides on it.
type Result[T, E any] struct {
	ok    bool
	value T
	err   E
}

// Ok is a Result that holds a value.
func Ok[T, E any](value T) Result[T, E] {
	return Result[T, E]{ok: true, value: value}
}

// Err is a Result that holds an error of the step's own kind.
func Err[T, E any](err E) Result[T, E] {
	return Result[T, E]{err: err}
}

// Get gives what the Result holds, and whether it is Ok.
func (r Result[T, E]) Get() (value T, err E, ok bool) {
	return r.value, r.err, r.ok
}

func (r Result[T, E]) IsOk() bool { return r.ok }

func (r Result[T, E]) MarshalJSON() ([]byte, error) {
	if r.ok {
		return json.Marshal(map[string]T{"Ok": r.value})
	}
	return json.Marshal(map[string]E{"Err": r.err})
}

// Void is what a Result<Void, E> holds when Ok: written null.
type Void struct{}

func (Void) MarshalJSON() ([]byte, error) { return []byte("null"), nil }

// Decoder reads a value of the boundary: what `qoqi gen go` writes
// for the arguments of steps whose types json cannot read alone, an enum
// among them.
type Decoder[T any] func(data json.RawMessage) (T, error)

// Decode reads a value json reads alone.
func Decode[T any](data json.RawMessage) (T, error) {
	var v T
	err := json.Unmarshal(data, &v)
	return v, err
}

// DecodeList reads an array, each item with the decoder given.
func DecodeList[T any](item Decoder[T]) Decoder[[]T] {
	return func(data json.RawMessage) ([]T, error) {
		var raw []json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, err
		}
		out := make([]T, len(raw))
		for i, r := range raw {
			v, err := item(r)
			if err != nil {
				return nil, fmt.Errorf("[%d]: %w", i, err)
			}
			out[i] = v
		}
		return out, nil
	}
}

// DecodeMap reads an object of texts to values.
func DecodeMap[T any](item Decoder[T]) Decoder[map[string]T] {
	return func(data json.RawMessage) (map[string]T, error) {
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, err
		}
		out := make(map[string]T, len(raw))
		for k, r := range raw {
			v, err := item(r)
			if err != nil {
				return nil, fmt.Errorf("[%q]: %w", k, err)
			}
			out[k] = v
		}
		return out, nil
	}
}

// DecodeOption reads a value or null, as a pointer.
func DecodeOption[T any](item Decoder[T]) Decoder[*T] {
	return func(data json.RawMessage) (*T, error) {
		if string(data) == "null" || len(data) == 0 {
			return nil, nil
		}
		v, err := item(data)
		if err != nil {
			return nil, err
		}
		return &v, nil
	}
}

// DecodeResult reads {"Ok": ...} or {"Err": ...}.
func DecodeResult[T, E any](ok Decoder[T], err Decoder[E]) Decoder[Result[T, E]] {
	return func(data json.RawMessage) (Result[T, E], error) {
		name, value, e := Variant(data)
		if e != nil {
			return Result[T, E]{}, e
		}
		switch name {
		case "Ok":
			v, e := ok(value)
			return Ok[T, E](v), e
		case "Err":
			v, e := err(value)
			return Err[T](v), e
		}
		return Result[T, E]{}, fmt.Errorf("a Result is {\"Ok\": ...} or {\"Err\": ...}, not %s", data)
	}
}

// Variant reads a variant of an enum as the boundary writes it: its name
// alone, "Unanswered", or an object of one key, its name, to its values,
// {"Paid": "p-77"}. values is nil for a variant without.
func Variant(data json.RawMessage) (name string, values json.RawMessage, err error) {
	if json.Unmarshal(data, &name) == nil {
		return name, nil, nil
	}
	var one map[string]json.RawMessage
	if err := json.Unmarshal(data, &one); err != nil || len(one) != 1 {
		return "", nil, fmt.Errorf("a variant is its name, or an object of one key, its name: not %s", data)
	}
	for k, v := range one {
		name, values = k, v
	}
	return name, values, nil
}

// Field reads a field of an object, with the decoder given: missing, it
// reads null.
func Field[T any](object map[string]json.RawMessage, name string, decode Decoder[T]) (T, error) {
	data, ok := object[name]
	if !ok {
		data = json.RawMessage("null")
	}
	v, err := decode(data)
	if err != nil {
		return v, fmt.Errorf("%s: %w", name, err)
	}
	return v, nil
}
