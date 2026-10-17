package cognitive

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestStagnationOperatorThresholdWithinBriefBucket(t *testing.T) {
	src := &fakeStagnation{items: []store.StaleIntention{{ID: "ongoing", Statement: "ongoing work", UpdatedSeq: 10}}}
	door := &mockLedger{st: &mockStore{}}
	src.door = door
	notices := map[string]string{}
	outbox := func(id, content string) {
		if old, exists := notices[id]; exists && old != content {
			t.Fatalf("notice %s changed content", id)
		}
		notices[id] = content
	}
	for _, step := range []struct {
		gap             uint64
		briefs, notices int
	}{
		{99, 0, 0}, {100, 1, 0}, {200, 2, 0}, {249, 2, 0},
		{250, 2, 1}, {250, 2, 1}, {299, 2, 1}, {300, 3, 1}, {1000, 4, 1},
	} {
		src.items[0].Gap = step.gap
		// A fresh Rhythm must use the record/outbox, not in-memory flags.
		quietRhythm(src, door, outbox).checkStagnation()
		if got := len(attentionBriefs(door)); got != step.briefs {
			t.Fatalf("gap %d: briefs %d, want %d", step.gap, got, step.briefs)
		}
		if len(notices) != step.notices {
			t.Fatalf("gap %d: notices %d, want %d", step.gap, len(notices), step.notices)
		}
	}
}

// Use the host's real persistence and content-sensitive deduplication.
// A close/reopen is a storage check, not a whole-process restart or delivery.
func TestStagnationOperatorNoticePersistsForUnchangedWork(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notices.db")
	st, err := store.NewForTest(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	src := &fakeStagnation{items: []store.StaleIntention{{ID: "ongoing", Gap: 250, UpdatedSeq: 10}}}
	door := &mockLedger{st: &mockStore{}}
	src.door = door
	var noticeID, original string
	check := func(wantInsert bool) {
		t.Helper()
		calls := 0
		outbox := func(id, content string) {
			calls++
			inserted, err := st.AddOutboxNoticeOnce(id, "operator", "", content, nil)
			if err != nil || inserted != wantInsert {
				t.Fatalf("inserted=%v want=%v err=%v", inserted, wantInsert, err)
			}
			if noticeID == "" {
				noticeID, original = id, content
			} else if id != noticeID || content != original {
				t.Fatalf("unchanged work changed notice: %s %q", id, content)
			}
		}
		quietRhythm(src, door, outbox).checkStagnation()
		if calls != 1 {
			t.Fatalf("outbox calls=%d, want 1", calls)
		}
	}
	check(true)
	check(false)
	// Give the persisted notice an earlier UTC day. Age is not a re-notice key.
	if err := st.BumpOutboxCreatedMs(noticeID, -48*60*60*1000); err != nil {
		t.Fatal(err)
	}
	check(false)
	if err := pageTook(st, noticeID); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = store.NewForTest(path)
	if err != nil {
		t.Fatal(err)
	}
	src.items[0].Gap = 1000
	check(false)
	retained, err := st.OutboxContent(noticeID)
	if err != nil || retained != original {
		t.Fatalf("retained notice=%q err=%v", retained, err)
	}
	pending, err := st.PendingNotices()
	if err != nil || len(pending) != 0 {
		t.Fatalf("delivered notice requeued: count=%d err=%v", len(pending), err)
	}
	src.items = nil
	quietRhythm(src, door, func(string, string) { t.Fatal("resolved work notified") }).checkStagnation()
}

// Legacy daily notices contain an aggregate count, not intention versions.
// They cannot suppress per-version notices or be guessed away on upgrade.
func TestStagnationOperatorLegacyNoticesCoexist(t *testing.T) {
	const legacyID = "attention_20261005"
	const legacyContent = "[attention] 2 intention(s) unattended for 250+ events — the identity has been briefed; a conversation may help."
	for _, delivered := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy_delivered_%v", delivered), func(t *testing.T) {
			st, err := store.NewMemory()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { st.Close() })
			if inserted, err := st.AddOutboxNoticeOnce(legacyID, "operator", "", legacyContent, nil); err != nil || !inserted {
				t.Fatalf("seed legacy notice: inserted=%v err=%v", inserted, err)
			}
			if delivered {
				if err := pageTook(st, legacyID); err != nil {
					t.Fatal(err)
				}
			}
			src := &fakeStagnation{items: []store.StaleIntention{
				{ID: "ongoing", Gap: 250, UpdatedSeq: 10},
				{ID: "second", Gap: 250, UpdatedSeq: 11},
			}}
			door := &mockLedger{st: &mockStore{}}
			src.door = door
			for _, wantInsert := range []bool{true, false} {
				calls := 0
				quietRhythm(src, door, func(id, content string) {
					calls++
					if !strings.HasPrefix(id, "attention_intention_") {
						t.Fatalf("unexpected per-intention notice ID: %q", id)
					}
					inserted, err := st.AddOutboxNoticeOnce(id, "operator", "", content, nil)
					if err != nil || inserted != wantInsert {
						t.Fatalf("upgraded notice: inserted=%v want=%v err=%v", inserted, wantInsert, err)
					}
				}).checkStagnation()
				if calls != len(src.items) {
					t.Fatalf("outbox calls=%d, want %d", calls, len(src.items))
				}
			}
			if content, err := st.OutboxContent(legacyID); err != nil || content != legacyContent {
				t.Fatalf("legacy notice changed: content=%q err=%v", content, err)
			}
			pending, err := st.PendingNotices()
			if err != nil {
				t.Fatal(err)
			}
			wantPending := len(src.items)
			if !delivered {
				wantPending++
			}
			if len(pending) != wantPending {
				t.Fatalf("pending notices=%d, want %d", len(pending), wantPending)
			}
			legacyPending := false
			for _, row := range pending {
				if row.ID == legacyID {
					legacyPending = true
				}
			}
			if legacyPending != !delivered {
				t.Fatalf("legacy pending=%v, want %v", legacyPending, !delivered)
			}
		})
	}
}

func TestStagnationOperatorRealOutboxChangedWork(t *testing.T) {
	for _, initialPair := range []bool{false, true} {
		t.Run(fmt.Sprintf("initial_pair_%v", initialPair), func(t *testing.T) {
			st, err := store.NewMemory()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { st.Close() })
			a := store.StaleIntention{ID: "ongoing", Gap: 250, UpdatedSeq: 10}
			b := store.StaleIntention{ID: "second", Gap: 250, UpdatedSeq: 11}
			src := &fakeStagnation{items: []store.StaleIntention{a}}
			if initialPair {
				src.items = append(src.items, b)
			}
			door := &mockLedger{st: &mockStore{}}
			src.door = door
			seen := map[string]string{}
			check := func(wantNew int) {
				t.Helper()
				calls, newNotices := 0, 0
				quietRhythm(src, door, func(id, content string) {
					calls++
					inserted, err := st.AddOutboxNoticeOnce(id, "operator", "", content, nil)
					if err != nil {
						t.Fatal(err)
					}
					if inserted {
						newNotices++
					}
					if !strings.Contains(content, "250+ events") {
						t.Fatalf("notice omitted threshold: %q", content)
					}
					if old, exists := seen[id]; exists && old != content {
						t.Fatalf("same key changed content: %q / %q", old, content)
					}
					seen[id] = content
				}).checkStagnation()
				qualifying := 0
				for _, si := range src.items {
					if si.Gap >= stagnationOperatorGap {
						qualifying++
					}
				}
				if newNotices != wantNew || calls != qualifying {
					t.Fatalf("new notices=%d want=%d; calls=%d qualifying=%d", newNotices, wantNew, calls, qualifying)
				}
			}
			if initialPair {
				check(2)
				// Removal-only A+B -> A must not repeat unchanged A.
				src.items = []store.StaleIntention{a}
				check(0)
			} else {
				check(1)
				// A brief-only member must not trigger an operator notice.
				b.Gap = 100
				src.items = []store.StaleIntention{a, b}
				check(0)
				b.Gap = 250
				src.items[1] = b
				check(1) // Only newly qualifying B, never A again.
			}
			// Query order, gap and statement changes do not change last touch.
			a.Gap, b.Gap = 1000, 700
			a.Statement = "different display text at the same last-change sequence"
			src.items = []store.StaleIntention{b, a}
			check(0)
			src.items = []store.StaleIntention{a}
			check(0)
			src.items = nil
			check(0)
			src.items = []store.StaleIntention{a, b}
			check(0)
			// A real last touch followed by re-drift allows only A's new notice.
			a.UpdatedSeq = 300
			src.items = []store.StaleIntention{a, b}
			check(1)
			check(0)
			rows, err := st.PendingNotices()
			if err != nil || len(rows) != 3 {
				t.Fatalf("pending notices: count=%d err=%v", len(rows), err)
			}
		})
	}
}

// Outbox failures must not mark work as notified in a resident-only flag.
// The existing callback owns reporting the error; Rhythm retries the same key.
func TestStagnationOperatorOutboxFailureRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notices.db")
	st, err := store.NewForTest(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	src := &fakeStagnation{items: []store.StaleIntention{{ID: "ongoing", Gap: 250, UpdatedSeq: 10}}}
	door := &mockLedger{st: &mockStore{}}
	src.door = door
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	var failedID, failedContent string
	failures := 0
	quietRhythm(src, door, func(id, content string) {
		inserted, err := st.AddOutboxNoticeOnce(id, "operator", "", content, nil)
		if err == nil || inserted {
			t.Fatalf("closed outbox accepted notice: inserted=%v err=%v", inserted, err)
		}
		failedID, failedContent = id, content
		failures++
	}).checkStagnation()
	if failures != 1 {
		t.Fatalf("outbox failures=%d, want 1", failures)
	}
	st, err = store.NewForTest(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, wantInsert := range []bool{true, false} {
		calls := 0
		quietRhythm(src, door, func(id, content string) {
			calls++
			if id != failedID || content != failedContent {
				t.Fatal("retry changed notice identity/content")
			}
			inserted, err := st.AddOutboxNoticeOnce(id, "operator", "", content, nil)
			if err != nil || inserted != wantInsert {
				t.Fatalf("recovered outbox: inserted=%v want=%v err=%v", inserted, wantInsert, err)
			}
		}).checkStagnation()
		if calls != 1 {
			t.Fatalf("retry calls=%d, want 1", calls)
		}
	}
}

type failingAttentionLookup struct {
	*fakeStagnation
	err error
}

func (s failingAttentionLookup) EntityExists(string) (bool, error) { return false, s.err }

func TestStagnationOperatorNoticeRequiresBrief(t *testing.T) {
	for _, failure := range []string{"source", "lookup", "append"} {
		t.Run(failure, func(t *testing.T) {
			injected := errors.New("injected attention failure")
			src := &fakeStagnation{items: []store.StaleIntention{{ID: "ongoing", Gap: 250}}}
			door := &mockLedger{st: &mockStore{}}
			src.door = door
			var source stagnationSource = src
			switch failure {
			case "source":
				src.err = injected
			case "lookup":
				source = failingAttentionLookup{src, injected}
			case "append":
				door.refuse = func(ledger.EventType) error { return injected }
			}
			notices := 0
			outbox := func(string, string) { notices++ }
			quietRhythm(source, door, outbox).checkStagnation()
			if notices != 0 || len(attentionBriefs(door)) != 0 {
				t.Fatalf("%s failure produced briefs/notices: %d/%d", failure, len(attentionBriefs(door)), notices)
			}
			// Failure does not poison a retry once the owning boundary recovers.
			src.err, door.refuse = nil, nil
			quietRhythm(src, door, outbox).checkStagnation()
			if notices != 1 || len(attentionBriefs(door)) != 1 {
				t.Fatalf("recovery produced briefs/notices: %d/%d", len(attentionBriefs(door)), notices)
			}
		})
	}
}

// pageTook records a notice as a page's carriage records it: claimed for
// the page, then delivered under that claim.
func pageTook(st *store.Store, id string) error {
	d, err := st.ClaimForPage(id)
	if err != nil {
		return err
	}
	return st.MarkDispatchDelivered(d, "test")
}
