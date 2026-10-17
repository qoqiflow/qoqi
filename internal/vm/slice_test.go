package vm

import (
	"fmt"
	"testing"
)

// The positions a range goes through inside a collection: steps outside
// it are left out, and a range to the largest Int is not counted to its
// end.
func TestSlicePositions(t *testing.T) {
	tests := []struct {
		from, end, by, length int64
		want                  string
	}{
		{1, 3, 1, 5, "[1 2]"},
		{0, MaxInt, 1, 4, "[0 1 2 3]"},
		{3, MaxInt, 1, 2, "[]"},
		{-4, 2, 1, 5, "[0 1]"},
		{-5, 10, 3, 10, "[1 4 7]"},
		{0, 10, 4, 10, "[0 4 8]"},
		{4, -1, -1, 5, "[4 3 2 1 0]"},
		{9, -1, -2, 5, "[3 1]"},
		{MaxInt, -1, -1, 3, "[2 1 0]"},
		{2, -100, -1, 5, "[2 1 0]"},
		{2, 2, 1, 5, "[]"},
		{0, 5, 1, 0, "[]"},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%d..%d by %d of %d", tt.from, tt.end, tt.by, tt.length), func(t *testing.T) {
			if got := fmt.Sprint(slicePositions(tt.from, tt.end, tt.by, tt.length)); got != tt.want && !(got == "[]" && tt.want == "[]") {
				t.Errorf("got %s, want %s", got, tt.want)
			}
		})
	}
}
