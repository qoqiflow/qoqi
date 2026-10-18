package flowd

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"
)

// What happened, day by day, for the console's pages of activity: each
// instance as it starts, ends or stops is kept once (keep.go), and the
// steps that leave are counted, by day, in batches.

// hourLayout is how the hours of the steps are written: in UTC, cut into
// days where the person reading is, when they are read.
const hourLayout = "2006-01-02T15"

type stepCounts struct {
	mu     sync.Mutex
	counts map[[3]string]int64 // by hour, flow and step
	kept   time.Time
}

// countStep counts a step that left, to be kept with the next batch.
func (d *Daemon) countStep(flow, step string) {
	if d.Keep == nil {
		return
	}
	c := &d.steps
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.counts == nil {
		c.counts = map[[3]string]int64{}
	}
	c.counts[[3]string{time.Now().UTC().Format(hourLayout), flow, step}]++
}

// keepSteps writes the counts of the steps, every few seconds, or now.
func (d *Daemon) keepSteps(now bool) {
	if d.Keep == nil {
		return
	}
	c := &d.steps
	c.mu.Lock()
	if len(c.counts) == 0 || (!now && time.Since(c.kept) < 5*time.Second) {
		c.mu.Unlock()
		return
	}
	counts := c.counts
	c.counts, c.kept = nil, time.Now()
	c.mu.Unlock()
	for k, n := range counts {
		if err := d.Keep.CountStep(k[0], k[1], k[2], n); err != nil {
			fmt.Fprintf(d.Log, "qoqi: the console cannot count the steps: %v\n", err)
			return
		}
	}
}

func (d *Daemon) keepWhatHappened(happened []Happened) {
	if d.Keep == nil {
		return
	}
	for _, h := range happened {
		if err := d.Keep.Record(h); err != nil {
			fmt.Fprintf(d.Log, "qoqi: the console cannot keep what happened: %v\n", err)
			return
		}
	}
}

// DayStats is what happened on a day.
type DayStats struct {
	Day     string `json:"day"`
	Started int    `json:"started"`
	Done    int    `json:"done"`
	Failed  int    `json:"failed"`
	Stopped int    `json:"stopped"`
	Steps   int64  `json:"steps"`
}

// FlowStats is what happened to the instances of a flow.
type FlowStats struct {
	Flow    string `json:"flow"`
	Program string `json:"program"`
	Started int    `json:"started"`
	Done    int    `json:"done"`
	Failed  int    `json:"failed"`
	Stopped int    `json:"stopped"`
	// How long those that ended took, from their start, in milliseconds
	P50 int64 `json:"p50"`
	P95 int64 `json:"p95"`
	Max int64 `json:"max"`
}

// Stats is what the pages of activity show.
type Stats struct {
	From  string      `json:"from"`
	To    string      `json:"to"`
	Days  []DayStats  `json:"days"`
	Flows []FlowStats `json:"flows"`
	Steps []StepRuns  `json:"steps"` // over the whole span, by flow and step, the most first
	// Hours are the starts by day of the week (0 is Sunday) and hour
	Hours [7][24]int `json:"hours"`
}

// stats is what happened over the last days, of a flow or of all: days
// are cut at midnight where the person reading is, tz minutes from UTC.
func (d *Daemon) stats(w http.ResponseWriter, r *http.Request) {
	if d.Keep == nil {
		fail(w, http.StatusNotFound, "stats", "this daemon keeps nothing of what happened: it was given no store for the console")
		return
	}
	query := r.URL.Query()
	days := 365
	if n, err := strconv.Atoi(query.Get("days")); err == nil && n > 0 && n <= 731 {
		days = n
	}
	tz := 0
	if n, err := strconv.Atoi(query.Get("tz")); err == nil && n >= -14*60 && n <= 14*60 {
		tz = n
	}
	flow := query.Get("flow")
	zone := time.FixedZone("", tz*60)
	now := time.Now().In(zone)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, zone)
	first := today.AddDate(0, 0, -(days - 1))

	stats := Stats{From: first.Format(time.DateOnly), To: today.Format(time.DateOnly), Flows: []FlowStats{}, Steps: []StepRuns{}}
	byDay := map[string]*DayStats{}
	for t := first; !t.After(today); t = t.AddDate(0, 0, 1) {
		day := t.Format(time.DateOnly)
		stats.Days = append(stats.Days, DayStats{Day: day})
	}
	for i := range stats.Days {
		byDay[stats.Days[i].Day] = &stats.Days[i]
	}

	// A day before the first, in UTC, has what its first hours were
	happened, err := d.Keep.Since(first.Add(-24 * time.Hour).UnixMilli())
	if err != nil {
		fail(w, http.StatusInternalServerError, "keep", err.Error())
		return
	}
	type life struct {
		flow, program string
		start, end    int64
	}
	lives := map[string]*life{}
	flows := map[string]*FlowStats{}
	flowOf := func(name, program string) *FlowStats {
		if flows[name] == nil {
			flows[name] = &FlowStats{Flow: name, Program: program}
		}
		return flows[name]
	}
	for _, h := range happened {
		if flow != "" && h.Flow != flow {
			continue
		}
		at := time.UnixMilli(h.At).In(zone)
		day := byDay[at.Format(time.DateOnly)]
		if day == nil {
			continue
		}
		key := h.Instance + "/" + h.Birth
		l := lives[key]
		if l == nil {
			l = &life{flow: h.Flow, program: h.Program}
			lives[key] = l
		}
		f := flowOf(h.Flow, h.Program)
		switch h.Status {
		case "waiting", "running":
			if l.start == 0 || h.At < l.start {
				if l.start == 0 {
					day.Started++
					f.Started++
					stats.Hours[at.Weekday()][at.Hour()]++
				}
				l.start = h.At
			}
		case "done":
			day.Done++
			f.Done++
			l.end = h.At
		case "failed":
			day.Failed++
			f.Failed++
			l.end = h.At
		case "stopped":
			day.Stopped++
			f.Stopped++
		}
	}
	took := map[string][]int64{}
	for _, l := range lives {
		if l.start > 0 && l.end >= l.start {
			took[l.flow] = append(took[l.flow], l.end-l.start)
		}
	}
	for name, f := range flows {
		if ms := took[name]; len(ms) > 0 {
			sort.Slice(ms, func(i, j int) bool { return ms[i] < ms[j] })
			f.P50, f.P95, f.Max = ms[len(ms)/2], ms[(len(ms)*95)/100], ms[len(ms)-1]
		}
		stats.Flows = append(stats.Flows, *f)
	}
	sort.Slice(stats.Flows, func(i, j int) bool { return stats.Flows[i].Flow < stats.Flows[j].Flow })

	steps, err := d.Keep.Steps(first.UTC().Format(hourLayout))
	if err != nil {
		fail(w, http.StatusInternalServerError, "keep", err.Error())
		return
	}
	total := map[[2]string]int64{}
	for _, s := range steps {
		if flow != "" && s.Flow != flow {
			continue
		}
		at, err := time.ParseInLocation(hourLayout, s.Hour, time.UTC)
		if err != nil {
			continue
		}
		if day := byDay[at.In(zone).Format(time.DateOnly)]; day != nil {
			day.Steps += s.Runs
		}
		total[[2]string{s.Flow, s.Step}] += s.Runs
	}
	for k, n := range total {
		stats.Steps = append(stats.Steps, StepRuns{Flow: k[0], Step: k[1], Runs: n})
	}
	sort.Slice(stats.Steps, func(i, j int) bool {
		if stats.Steps[i].Runs != stats.Steps[j].Runs {
			return stats.Steps[i].Runs > stats.Steps[j].Runs
		}
		return stats.Steps[i].Flow+stats.Steps[i].Step < stats.Steps[j].Flow+stats.Steps[j].Step
	})
	answer(w, http.StatusOK, stats)
}
