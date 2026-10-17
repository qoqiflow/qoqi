package flowd

import (
	"sort"
	"strings"
)

// What changed of the source from one version to another, line by line,
// as `diff -u` says it: hunks of the lines that changed, with three lines
// around them.

// FileDiff is what changed of one file.
type FileDiff struct {
	File   string `json:"file"`
	Status string `json:"status"` // added, removed, changed or same
	Added  int    `json:"added"`
	Gone   int    `json:"removed"`
	Hunks  []Hunk `json:"hunks,omitempty"`
}

// Hunk is lines that changed, with the lines around them: where they are
// in the old file and in the new, from 1.
type Hunk struct {
	OldStart int        `json:"oldStart"`
	OldLines int        `json:"oldLines"`
	NewStart int        `json:"newStart"`
	NewLines int        `json:"newLines"`
	Lines    []DiffLine `json:"lines"`
}

// DiffLine is a line of a hunk: kept (" "), added ("+") or removed ("-").
type DiffLine struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

// around is how many lines a hunk keeps around what changed.
const around = 3

// most is the largest file pair compared line by line: beyond it, the
// whole file is said to have changed.
const most = 4_000_000

// DiffSources is what changed from one version's files to another's.
func DiffSources(old, newer map[string]string) []FileDiff {
	seen := map[string]bool{}
	var files []string
	for f := range old {
		seen[f] = true
		files = append(files, f)
	}
	for f := range newer {
		if !seen[f] {
			files = append(files, f)
		}
	}
	sort.Strings(files)
	var out []FileDiff
	for _, f := range files {
		before, had := old[f]
		after, has := newer[f]
		diff := FileDiff{File: f}
		switch {
		case !had:
			diff.Status = "added"
		case !has:
			diff.Status = "removed"
		case before == after:
			diff.Status = "same"
		default:
			diff.Status = "changed"
		}
		if diff.Status != "same" {
			diff.Hunks = DiffLines(splitLines(before), splitLines(after))
			for _, h := range diff.Hunks {
				for _, l := range h.Lines {
					switch l.Kind {
					case "+":
						diff.Added++
					case "-":
						diff.Gone++
					}
				}
			}
		}
		out = append(out, diff)
	}
	return out
}

func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}

// DiffLines is what changed from one list of lines to another, in hunks.
func DiffLines(a, b []string) []Hunk {
	ops := edits(a, b)
	var hunks []Hunk
	// The changes, and the kept lines near enough to them
	for i := 0; i < len(ops); {
		if ops[i].kind == " " {
			i++
			continue
		}
		start := max(i-around, 0)
		end := i
		for end < len(ops) {
			if ops[end].kind != " " {
				end++
				continue
			}
			// A run of kept lines: the hunk ends there if it is long
			run := end
			for run < len(ops) && ops[run].kind == " " {
				run++
			}
			if run == len(ops) || run-end > 2*around {
				end = min(end+around, len(ops))
				break
			}
			end = run
		}
		h := Hunk{OldStart: ops[start].old + 1, NewStart: ops[start].new + 1}
		for _, op := range ops[start:end] {
			h.Lines = append(h.Lines, DiffLine{Kind: op.kind, Text: op.text})
			if op.kind != "+" {
				h.OldLines++
			}
			if op.kind != "-" {
				h.NewLines++
			}
		}
		hunks = append(hunks, h)
		i = end
	}
	return hunks
}

// edit is a line kept, added or removed, and where it is in each file.
type edit struct {
	kind     string
	text     string
	old, new int
}

// edits is the shortest way from a to b, by the longest list of lines
// both have in order.
func edits(a, b []string) []edit {
	n, m := len(a), len(b)
	if n*m > most {
		// Too large to compare: all removed, then all added
		var out []edit
		for i, line := range a {
			out = append(out, edit{"-", line, i, 0})
		}
		for j, line := range b {
			out = append(out, edit{"+", line, n, j})
		}
		return out
	}
	// common[i][j] is the length of the longest list a[i:] and b[j:] share
	common := make([][]int32, n+1)
	for i := range common {
		common[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				common[i][j] = common[i+1][j+1] + 1
			} else {
				common[i][j] = max(common[i+1][j], common[i][j+1])
			}
		}
	}
	var out []edit
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && a[i] == b[j]:
			out = append(out, edit{" ", a[i], i, j})
			i++
			j++
		case i < n && (j == m || common[i+1][j] >= common[i][j+1]):
			// What goes before what comes, as diff -u says it
			out = append(out, edit{"-", a[i], i, j})
			i++
		default:
			out = append(out, edit{"+", b[j], i, j})
			j++
		}
	}
	return out
}
