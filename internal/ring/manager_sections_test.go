package ring

import (
	"sync"
	"testing"
)

// .
// .
// .
// .
// .

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
	// .
	// .
	// .
	if secs[0].Name != "rhythm" || secs[1].Name != "focus" {
		t.Fatalf("order = %s, %s — insertion order was not preserved", secs[0].Name, secs[1].Name)
	}
}

// .
// .
func TestSectionsCannotBeMutatedThroughTheReturnedSlice(t *testing.T) {
	m := NewManager()
	m.SetSection(Ring3, "reflection", "as written")

	got := m.Sections(Ring3)
	got[0].Content = "rewritten from outside"

	if inside := m.Section(Ring3, "reflection"); inside != "as written" {
		t.Fatalf("the manager's copy became %q — a caller mutated ring state through a read", inside)
	}
}

// .
// .
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

// .
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

// .
// .
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
