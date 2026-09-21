package logsink

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .

type tickKey struct{ category, what string }

type tickStat struct {
	n       int
	slowest time.Duration
}

var ticks = struct {
	sync.Mutex
	m     map[tickKey]*tickStat
	since time.Time
}{m: map[tickKey]*tickStat{}}

// .
func Tick(category, what string, took time.Duration) {
	ticks.Lock()
	defer ticks.Unlock()
	if ticks.since.IsZero() {
		ticks.since = time.Now()
	}
	k := tickKey{category: category, what: what}
	st := ticks.m[k]
	if st == nil {
		st = &tickStat{}
		ticks.m[k] = st
	}
	st.n++
	if took > st.slowest {
		st.slowest = took
	}
}

// .
// .
// .
func FlushDigest() {
	ticks.Lock()
	if len(ticks.m) == 0 {
		ticks.Unlock()
		return
	}
	m, since := ticks.m, ticks.since
	ticks.m, ticks.since = map[tickKey]*tickStat{}, time.Time{}
	ticks.Unlock()

	keys := make([]tickKey, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].category != keys[j].category {
			return keys[i].category < keys[j].category
		}
		return keys[i].what < keys[j].what
	})
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		st := m[k]
		p := fmt.Sprintf("%s %d %s", k.category, st.n, k.what)
		if st.slowest >= 100*time.Millisecond {
			p += fmt.Sprintf(" (slowest %s)", st.slowest.Round(10*time.Millisecond))
		}
		parts = append(parts, p)
	}
	span := ""
	if !since.IsZero() {
		span = fmt.Sprintf("since %s — ", since.Format("15:04"))
	}
	Info("quiet", "%s%s", span, strings.Join(parts, " · "))
}

// .
// .
// .
// .
func resetTicks() {
	ticks.Lock()
	ticks.m, ticks.since = map[tickKey]*tickStat{}, time.Time{}
	ticks.Unlock()
}

// .
// .
func digestEvery(d time.Duration, stop <-chan struct{}) {
	t := time.NewTicker(d)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			FlushDigest()
		case <-stop:
			return
		}
	}
}
