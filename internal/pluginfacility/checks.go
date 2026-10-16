package pluginfacility

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

const (
	TimeoutsFixed = 3
	TimeoutWindow = 24 * time.Hour
)

const (
	ChecksNone    = "none"
	ChecksPassed  = "passed"
	ChecksPending = "pending"
	ChecksFailed  = "failed"
)

type ChecksVerdict struct {
	State    string
	Variant  string
	At       time.Time
	Failed   *CheckFailure
	Timeouts int
}

type VerdictStore interface {
	LoadVerdicts() ([]byte, error)
	SaveVerdicts([]byte) error
}

type ChecksFailedError struct {
	Variant string
	Failure CheckFailure
	At      time.Time
}

func (e *ChecksFailedError) RetryAfter() time.Time { return e.At }

const checksFailedRemedy = "This set is not used on this machine until its package, the set or this host's build changes, or Try again; the next set this machine can run is started in its place."

func (e *ChecksFailedError) Error() string {
	return fmt.Sprintf("its publisher's checks failed on this machine for component set %s: %s", e.Variant, e.Failure.said())
}

func (f CheckFailure) said() string { return fmt.Sprintf("%q: %s", f.Check, f.Words) }

type remembered struct {
	ID          string        `json:"id"`
	PackageHash string        `json:"package_hash"`
	Variant     string        `json:"variant"`
	Build       string        `json:"build"`
	PassedAt    time.Time     `json:"passed_at,omitzero"`
	Failed      *CheckFailure `json:"failed,omitempty"`
	FailedAt    time.Time     `json:"failed_at,omitzero"`
	TimedOut    []time.Time   `json:"timed_out,omitempty"`
}

type verdictBook struct {
	mu     sync.Mutex
	store  VerdictStore
	build  string
	log    func(format string, args ...any)
	loaded bool
	rows   []remembered
}

func (b *verdictBook) load() {
	if b.loaded {
		return
	}
	b.loaded = true
	if b.store == nil {
		return
	}
	raw, err := b.store.LoadVerdicts()
	if err == nil && len(raw) > 0 {
		var rows []remembered
		if err = json.Unmarshal(raw, &rows); err == nil {
			for _, r := range rows {

				if r.Build == b.build {
					b.rows = append(b.rows, r)
				}
			}
		}
	}
	if err != nil {
		b.say("the remembered verdicts of the publisher's checks could not be read, and every set is checked afresh: %v", err)
	}
}

func (b *verdictBook) save() {
	if b.store == nil {
		return
	}
	raw, err := json.Marshal(b.rows)
	if err == nil {
		err = b.store.SaveVerdicts(raw)
	}
	if err != nil {
		b.say("the verdicts of the publisher's checks could not be kept: this host remembers them until it stops, and checks every set afresh after: %v", err)
	}
}

func (b *verdictBook) say(format string, args ...any) {
	if b.log != nil {
		b.log(format, args...)
	}
}

func (b *verdictBook) failedSets(id, packageHash string) map[string]string {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.load()
	var out map[string]string
	for _, r := range b.rows {
		if r.ID == id && r.PackageHash == packageHash && r.Failed != nil {
			if out == nil {
				out = map[string]string{}
			}
			out[r.Variant] = r.Failed.said()
		}
	}
	return out
}

func (b *verdictBook) record(now time.Time, id, packageHash string, c Checks) (ChecksVerdict, error) {
	v := ChecksVerdict{State: ChecksNone, Variant: c.Variant}
	if c.Ran == 0 {
		return v, nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.load()

	kept := b.rows[:0]
	for _, r := range b.rows {
		if r.ID != id || r.PackageHash == packageHash {
			kept = append(kept, r)
		}
	}
	b.rows = kept
	i := -1
	for j, r := range b.rows {
		if r.ID == id && r.PackageHash == packageHash && r.Variant == c.Variant {
			i = j
		}
	}
	if i < 0 {
		b.rows = append(b.rows, remembered{ID: id, PackageHash: packageHash, Variant: c.Variant, Build: b.build})
		i = len(b.rows) - 1
	}
	row := &b.rows[i]
	f := c.Failed
	switch {
	case f == nil:
		row.PassedAt, row.TimedOut, row.Failed, row.FailedAt = now, nil, nil, time.Time{}
		v.State, v.At = ChecksPassed, now
	case f.Outcome == CheckTimedOut:
		var recent []time.Time
		for _, at := range row.TimedOut {
			if now.Sub(at) < TimeoutWindow {
				recent = append(recent, at)
			}
		}
		row.TimedOut = append(recent, now)
		v.State, v.Failed, v.Timeouts = ChecksPending, f, len(row.TimedOut)
		if len(row.TimedOut) >= TimeoutsFixed {
			fixed := *f
			fixed.Words = fmt.Sprintf("timed out at %d separate starts within %s: %s", len(row.TimedOut), durationWords(TimeoutWindow), f.Words)
			f = &fixed
			row.Failed, row.FailedAt = f, now
			v.State, v.At, v.Failed = ChecksFailed, now, f
		}
	case f.Outcome.Fixed():
		row.Failed, row.FailedAt = f, now
		v.State, v.At, v.Failed = ChecksFailed, now, f
	default:

		v.State, v.Failed = ChecksPending, f
		return v, nil
	}
	b.save()
	if v.State == ChecksFailed {
		return v, &ChecksFailedError{Variant: c.Variant, Failure: *f, At: now}
	}
	return v, nil
}

func (b *verdictBook) forget(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.load()
	kept := b.rows[:0]
	for _, r := range b.rows {
		if r.ID != id {
			kept = append(kept, r)
		}
	}
	if len(kept) == len(b.rows) {
		return
	}
	b.rows = kept
	b.save()
}

func durationWords(d time.Duration) string {
	if d%time.Hour == 0 {
		return fmt.Sprintf("%d hours", int(d/time.Hour))
	}
	return d.String()
}

func copyChecks(v *ChecksVerdict) *ChecksVerdict {
	if v == nil {
		return nil
	}
	c := *v
	if v.Failed != nil {
		f := *v.Failed
		c.Failed = &f
	}
	return &c
}
