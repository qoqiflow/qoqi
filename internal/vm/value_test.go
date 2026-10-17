package vm

import (
	"math"
	"testing"
)

// TestValueHeapPtrRoundTrip checks that the full 48-bit address range survives
// NaN-boxing. Uses synthetic addresses so the result does not depend on where
// the Go runtime happens to place its heap.
func TestValueHeapPtrRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		ptr  uintptr
	}{
		{"low", 0x1000},
		{"40-bit", 0x000000C000010000},
		{"just below 2^44", 0x00000FFFFFFFFFF8},
		{"2^44", 0x0000100000000000},
		{"above 2^44", 0x00001702207381E0},
		{"bit 47 set", 0x0000800000000000},
		{"max 48-bit", 0x0000FFFFFFFFFFF8},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := NewHeapPtr(tt.ptr)

			if !v.IsHeapPtr() {
				t.Fatalf("expected heap ptr, got %s", v.String())
			}
			if v.IsNumber() || v.IsInt() || v.IsBool() || v.IsNull() {
				t.Errorf("heap ptr 0x%x matched another type", tt.ptr)
			}
			if got := v.AsHeapPtr(); got != tt.ptr {
				t.Errorf("expected 0x%x, got 0x%x", tt.ptr, got)
			}
		})
	}
}

// TestValueIntRoundTrip checks the 48-bit signed int range, including
// negative values that rely on sign extension.
func TestValueIntRoundTrip(t *testing.T) {
	const (
		maxInt48 = int64(1)<<47 - 1
		minInt48 = -(int64(1) << 47)
	)

	tests := []struct {
		name string
		val  int64
	}{
		{"zero", 0},
		{"one", 1},
		{"minus one", -1},
		{"small negative", -42},
		{"2^44 - 1", int64(1)<<44 - 1},
		{"2^44", int64(1) << 44},
		{"-(2^44)", -(int64(1) << 44)},
		{"max", maxInt48},
		{"min", minInt48},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := NewInt(tt.val)

			if !v.IsInt() {
				t.Fatalf("expected int, got %s", v.String())
			}
			if v.IsNumber() || v.IsHeapPtr() || v.IsBool() || v.IsNull() {
				t.Errorf("int %d matched another type", tt.val)
			}
			if got := v.AsInt(); got != tt.val {
				t.Errorf("expected %d, got %d", tt.val, got)
			}
		})
	}
}

// TestValueTagsDoNotOverlapPayload guards the bit layout: a full payload must
// never spill into the tag bits.
func TestValueTagsDoNotOverlapPayload(t *testing.T) {
	if tagMask&payloadMask != 0 {
		t.Errorf("tagMask 0x%x overlaps payloadMask 0x%x", tagMask, payloadMask)
	}
	if tagMask|payloadMask != fullPayload {
		t.Errorf("tag and payload do not cover the mantissa: 0x%x", tagMask|payloadMask)
	}
	for _, tag := range []uint64{tagFloat, tagNull, tagBool, tagInt, tagHeapPtr} {
		if tag&^tagMask != 0 {
			t.Errorf("tag 0x%x has bits outside tagMask 0x%x", tag, tagMask)
		}
	}
}

// TestValueKindsAreDisjoint checks that every value is exactly one kind.
// Infinity and NaN have the exponent that marks boxed values, and must
// still be numbers: before null had a tag of its own, +Inf was null.
func TestValueKindsAreDisjoint(t *testing.T) {
	nan := math.NaN()
	tests := []struct {
		name  string
		value Value
		kind  string
	}{
		{"zero", NewNumber(0), "number"},
		{"negative zero", NewNumber(math.Copysign(0, -1)), "number"},
		{"float", NewNumber(-2.5), "number"},
		{"largest float", NewNumber(math.MaxFloat64), "number"},
		{"smallest float", NewNumber(math.SmallestNonzeroFloat64), "number"},
		{"positive infinity", NewNumber(math.Inf(1)), "number"},
		{"negative infinity", NewNumber(math.Inf(-1)), "number"},
		{"NaN", NewNumber(nan), "number"},
		{"NaN from arithmetic", NewNumber(math.Inf(1) - math.Inf(1)), "number"},
		{"null", NewNull(), "null"},
		{"true", NewBool(true), "bool"},
		{"false", NewBool(false), "bool"},
		{"int zero", NewInt(0), "int"},
		{"int", NewInt(-7), "int"},
		{"largest int", NewInt(1<<47 - 1), "int"},
		{"smallest int", NewInt(-(1 << 47)), "int"},
		{"heap pointer", NewHeapPtr(0x1702207381E0), "heap"},
		{"largest heap pointer", NewHeapPtr(0x0000FFFFFFFFFFF8), "heap"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kinds := map[string]bool{
				"number": tt.value.IsNumber(),
				"null":   tt.value.IsNull(),
				"bool":   tt.value.IsBool(),
				"int":    tt.value.IsInt(),
				"heap":   tt.value.IsHeapPtr(),
			}
			for kind, is := range kinds {
				if is != (kind == tt.kind) {
					t.Errorf("expected only %s, but Is%s reported %v", tt.kind, kind, is)
				}
			}
			if tt.value.IsNaN() == (tt.kind == "number") {
				t.Errorf("IsNaN (boxed) should be %v", tt.kind != "number")
			}
		})
	}

	// Floats keep their value through boxing
	for _, f := range []float64{0, -2.5, math.MaxFloat64, math.Inf(1), math.Inf(-1)} {
		if got := NewNumber(f).AsNumber(); got != f {
			t.Errorf("expected %v to survive boxing, got %v", f, got)
		}
	}
	if got := NewNumber(nan).AsNumber(); got == got {
		t.Errorf("expected NaN to survive boxing, got %v", got)
	}
}
