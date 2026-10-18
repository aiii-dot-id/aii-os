package ring

import (
	"sync"
	"testing"
	"time"
)

// The section accessors were all at 0%. They carry what facilities
// write into the prompt, so "last write per (level, name) wins" and
// "the caller cannot reach inside" are the two properties worth
// pinning — a facility that could mutate the manager's slice through a
// returned value would corrupt another facility's section.

func TestSectionsAreNamedAndLastWriteWins(t *testing.T) {
	m := NewManager()
	if got := m.Section(Ring2, "rhythm"); got != "" {
		t.Fatalf("an unset section returned %q — absent must be empty, not a guess", got)
	}

	m.SetSection(Ring2, "rhythm", "first")
	m.SetSection(Ring2, "focus", "the harbour")
	m.SetSection(Ring2, "rhythm", "second")

	if got := m.Section(Ring2, "rhythm"); got != "second" {
		t.Fatalf("rhythm = %q, want the later write", got)
	}
	if got := m.Section(Ring2, "focus"); got != "the harbour" {
		t.Fatalf("focus = %q — updating one section disturbed another", got)
	}

	secs := m.Sections(Ring2)
	if len(secs) != 2 {
		t.Fatalf("got %d sections, want 2 — a rewrite must replace, not append", len(secs))
	}
	// INSERTION ORDER, because the prompt is assembled in the order
	// facilities contributed and a reshuffle changes what the identity
	// reads first.
	if secs[0].Name != "rhythm" || secs[1].Name != "focus" {
		t.Fatalf("order = %s, %s — insertion order was not preserved", secs[0].Name, secs[1].Name)
	}
}

// Sections HANDS OUT A COPY. One facility must not be able to reach
// through a returned slice and rewrite another facility's section.
func TestSectionsCannotBeMutatedThroughTheReturnedSlice(t *testing.T) {
	m := NewManager()
	m.SetSection(Ring3, "reflection", "as written")

	got := m.Sections(Ring3)
	got[0].Content = "rewritten from outside"

	if inside := m.Section(Ring3, "reflection"); inside != "as written" {
		t.Fatalf("the manager's copy became %q — a caller mutated ring state through a read", inside)
	}
}

// Levels do not bleed into each other: the same name at two levels is
// two sections.
func TestTheSameSectionNameAtTwoLevelsIsTwoSections(t *testing.T) {
	m := NewManager()
	m.SetSection(Ring1, "note", "ring one")
	m.SetSection(Ring4, "note", "ring four")

	if got := m.Section(Ring1, "note"); got != "ring one" {
		t.Fatalf("Ring1 note = %q", got)
	}
	if got := m.Section(Ring4, "note"); got != "ring four" {
		t.Fatalf("Ring4 note = %q", got)
	}
}

// The brief is a bridge section rather than a ring, and round-trips.
func TestBriefRoundTrips(t *testing.T) {
	m := NewManager()
	if got := m.GetBrief(); got != "" {
		t.Fatalf("a fresh manager reported a brief: %q", got)
	}
	m.SetBrief("what held overnight")
	if got := m.GetBrief(); got != "what held overnight" {
		t.Fatalf("brief = %q", got)
	}
	m.SetBrief("replaced")
	if got := m.GetBrief(); got != "replaced" {
		t.Fatalf("brief did not replace: %q", got)
	}
}

// A brief keeps the time it was written: now for one a facility writes,
// the stored time for one a start restores. Restored as written now, a
// brief of any age read as this morning's after every restart.
func TestABriefKeepsWhenItWasWritten(t *testing.T) {
	m := NewManager()
	before := time.Now()
	m.SetBrief("written now")
	if content, written := m.Brief(); content != "written now" || written.Before(before) || written.After(time.Now()) {
		t.Fatalf("a brief set now is %q, written %s", content, written)
	}
	monday := time.Date(2026, 10, 12, 11, 0, 0, 0, time.UTC)
	m.RestoreBrief("written on Monday", monday)
	if content, written := m.Brief(); content != "written on Monday" || !written.Equal(monday) {
		t.Fatalf("A RESTORED BRIEF LOST ITS DATE: %q, written %s; want %s", content, written, monday)
	}
	if got := m.GetBrief(); got != "written on Monday" {
		t.Fatalf("brief = %q", got)
	}
}

// Facilities write concurrently; the mutexes are the reason this is
// safe, and the race detector is the reason this test is worth having.
func TestConcurrentSectionWritesAreSafe(t *testing.T) {
	m := NewManager()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			m.SetSection(Ring2, "shared", "written")
			m.SetSection(Ring2, string(rune('a'+i)), "own")
			_ = m.Sections(Ring2)
			_ = m.Section(Ring2, "shared")
			m.SetBrief("b")
			_ = m.GetBrief()
		}(i)
	}
	wg.Wait()
	if got := m.Section(Ring2, "shared"); got != "written" {
		t.Fatalf("shared section = %q after concurrent writes", got)
	}
}

// Sections is a read: it takes the read half of the lock, so it never
// waits on another reader — the prompt gate renders Ring 3 from it while
// other reads of the manager are under way.
func TestSectionsIsAReadAndNeverWaitsOnAnotherReader(t *testing.T) {
	m := NewManager()
	m.SetSection(Ring3, "surfacing", "a note")
	m.mu.RLock() // another reader, mid-read
	defer m.mu.RUnlock()
	got := make(chan []Section, 1)
	go func() { got <- m.Sections(Ring3) }()
	select {
	case secs := <-got:
		if len(secs) != 1 || secs[0].Content != "a note" {
			t.Fatalf("Sections = %+v", secs)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SECTIONS WAITED ON ANOTHER READER: it took the write lock for a read")
	}
}
