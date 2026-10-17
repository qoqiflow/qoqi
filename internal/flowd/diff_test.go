package flowd_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/qoqiflow/qoqi/internal/flowd"
)

// render writes hunks as diff -u does.
func render(hunks []flowd.Hunk) string {
	var out strings.Builder
	for _, h := range hunks {
		fmt.Fprintf(&out, "@@ -%d,%d +%d,%d @@\n", h.OldStart, h.OldLines, h.NewStart, h.NewLines)
		for _, l := range h.Lines {
			out.WriteString(l.Kind + l.Text + "\n")
		}
	}
	return out.String()
}

func numbered(n int) []string {
	var out []string
	for i := 1; i <= n; i++ {
		out = append(out, fmt.Sprint("line ", i))
	}
	return out
}

func TestLinesThatChanged(t *testing.T) {
	// One line changed in the middle of twenty: three lines around it
	a := numbered(20)
	b := append([]string(nil), a...)
	b[9] = "line ten"
	want := "@@ -7,7 +7,7 @@\n line 7\n line 8\n line 9\n-line 10\n+line ten\n line 11\n line 12\n line 13\n"
	if got := render(flowd.DiffLines(a, b)); got != want {
		t.Errorf("one line:\n%s\nwant\n%s", got, want)
	}

	// Two changes far apart are two hunks; near, one
	b = append([]string(nil), a...)
	b[1], b[18] = "two", "nineteen"
	if hunks := flowd.DiffLines(a, b); len(hunks) != 2 {
		t.Errorf("far apart: %d hunks\n%s", len(hunks), render(hunks))
	}
	b = append([]string(nil), a...)
	b[5], b[10] = "six", "eleven"
	if hunks := flowd.DiffLines(a, b); len(hunks) != 1 || hunks[0].OldStart != 3 || hunks[0].OldLines != 12 {
		t.Errorf("near: \n%s", render(hunks))
	}

	// Lines added at the end, and removed at the start
	b = append(append([]string(nil), a[2:]...), "line 21")
	got := render(flowd.DiffLines(a, b))
	if !strings.HasPrefix(got, "@@ -1,5 +1,3 @@\n-line 1\n-line 2\n line 3\n") || !strings.HasSuffix(got, " line 20\n+line 21\n") {
		t.Errorf("at the ends:\n%s", got)
	}

	// Nothing changed is no hunk
	if hunks := flowd.DiffLines(a, a); len(hunks) != 0 {
		t.Errorf("the same: %s", render(hunks))
	}
}

func TestFilesThatChanged(t *testing.T) {
	old := map[string]string{"a.qi": "one\ntwo\n", "b.qi": "kept\n", "c.qi": "gone\n"}
	newer := map[string]string{"a.qi": "one\n2\n", "b.qi": "kept\n", "d.qi": "new\n"}
	files := flowd.DiffSources(old, newer)
	var said []string
	for _, f := range files {
		said = append(said, fmt.Sprintf("%s %s +%d -%d", f.File, f.Status, f.Added, f.Gone))
	}
	if got := strings.Join(said, ", "); got != "a.qi changed +1 -1, b.qi same +0 -0, c.qi removed +0 -1, d.qi added +1 -0" {
		t.Errorf("got %s", got)
	}
}
