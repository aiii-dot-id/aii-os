package store

import (
	"errors"
	"testing"
	"time"
)

// THE OPERATOR'S NOTICES HAVE TWO CARRIERS AND TAKE ONE (S-3). Each
// claims before it sends: the page before a write to the pages open, the
// route off the dashboard before it asks an adapter. These are the
// interlock's rules, held where they live: in the store's reads and
// writes, not in the dashboard paths and the walk that call them.

func noticeStore(t *testing.T, ids ...string) *Store {
	t.Helper()
	s := dispatchStore(t)
	for _, id := range ids {
		if err := s.AddOutboxNotice(id, "operator", "", "notice "+id, nil); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// row reads a row whatever its state.
func row(t *testing.T, s *Store, id string) OutboxMessage {
	t.Helper()
	rows, err := s.db.Query(`SELECT `+outboxColumns+` FROM outbox WHERE id = ?`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	all, err := scanOutbox(rows)
	if err != nil || len(all) != 1 {
		t.Fatalf("row %s: %v %v", id, all, err)
	}
	return all[0]
}

// pageTakes is the dashboard's carriage of one notice: the page's claim,
// a write that landed, and the claim settled as delivered.
func pageTakes(s *Store, id string) error {
	d, err := s.ClaimForPage(id)
	if err != nil {
		return err
	}
	return s.MarkDispatchDelivered(d, "dashboard")
}

func pageHas(t *testing.T, s *Store, id string) bool {
	t.Helper()
	notices, err := s.PageNotices()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range notices {
		if m.ID == id {
			return true
		}
	}
	return false
}

// A CLAIM SHUTS THE PAGE OUT, AND WHAT THE WALK LEAVES IS UNTOUCHED BY IT.
// In flight, delivered by an adapter, or of unknown effect, the notice is
// not the page's to push as new, and the page's claim is refused —
// above all it never turns an unknown effect into a performed one.
func TestThePageNeverTakesANoticeTheRouteHolds(t *testing.T) {
	s := noticeStore(t, "flying", "sent")
	flying := claimFor(t, s, "flying")
	if pageHas(t, s, "flying") {
		t.Fatal("the page may push a notice in flight under a walk's claim")
	}
	if _, err := s.ClaimForPage("flying"); !errors.Is(err, ErrClaimRefused) {
		t.Fatalf("the page claimed a notice in flight: %v", err)
	}
	if m := row(t, s, "flying"); m.Delivered != 0 || m.DispatchedMs != flying.At || m.DispatchedMs <= m.LastAttemptMs || m.Attempts != 0 {
		t.Fatalf("the page's claim touched the walk's: %+v", m)
	}
	if err := s.MarkEffectUnknown(flying, "org.example.telegram", "NET_EFFECT_UNKNOWN"); err != nil {
		t.Fatalf("the page's claim cost the walk its outcome: %v", err)
	}
	if pageHas(t, s, "flying") {
		t.Fatal("a notice of unknown effect is pushed to the page as new")
	}
	if _, err := s.ClaimForPage("flying"); !errors.Is(err, ErrClaimRefused) {
		t.Fatalf("the page claimed a notice of unknown effect: %v", err)
	}
	if m := row(t, s, "flying"); m.Delivered != 0 || m.Effect != "unknown" || m.DeliveredVia != "org.example.telegram" || m.Attempts != 1 || !m.Parked {
		t.Fatalf("the unknown effect was overwritten: %+v", m)
	}

	sent := claimFor(t, s, "sent")
	if err := s.MarkDispatchDelivered(sent, "org.example.telegram"); err != nil {
		t.Fatal(err)
	}
	if pageHas(t, s, "sent") {
		t.Fatal("a notice an adapter took is pushed to the page as new")
	}
	if _, err := s.ClaimForPage("sent"); !errors.Is(err, ErrClaimRefused) {
		t.Fatalf("the page claimed a notice an adapter took: %v", err)
	}
	if m := row(t, s, "sent"); m.DeliveredVia != "org.example.telegram" || m.Attempts != 1 || m.Effect != "performed" {
		t.Fatalf("the page re-stamped a delivery it did not make: %+v", m)
	}
}

func TestTheRouteNeverTakesANoticeThePageTook(t *testing.T) {
	s := noticeStore(t, "shown")
	if !pageHas(t, s, "shown") {
		t.Fatal("a fresh notice is not the page's to push")
	}
	held, err := s.ClaimForPage("shown")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimDispatch("shown"); !errors.Is(err, ErrClaimRefused) {
		t.Fatalf("a walk claimed a notice a page holds: %v", err)
	}
	if _, err := s.ClaimForPage("shown"); !errors.Is(err, ErrClaimRefused) {
		t.Fatalf("a second page pass claimed a notice a page holds: %v", err)
	}
	if pending, err := s.PendingNotices(); err != nil || len(pending) != 0 || pageHas(t, s, "shown") {
		t.Fatalf("a notice a page holds waits for a carrier: %d %v", len(pending), err)
	}
	if err := s.MarkDispatchDelivered(held, "dashboard"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimDispatch("shown"); !errors.Is(err, ErrClaimRefused) {
		t.Fatalf("a walk claimed a notice the page took: %v", err)
	}
	if pending, err := s.PendingNotices(); err != nil || len(pending) != 0 {
		t.Fatalf("a notice the page took waits for the route: %d %v", len(pending), err)
	}
	if err := pageTakes(s, "shown"); !errors.Is(err, ErrClaimRefused) {
		t.Fatalf("a second page took a notice the page took: %v", err)
	}
	if m := row(t, s, "shown"); m.Attempts != 1 || m.DeliveredVia != "dashboard" || m.Effect != "performed" {
		t.Fatalf("one page's carriage, one attempt: %+v", m)
	}
}

// A WRITE THAT DID NOT LAND GIVES THE PAGE'S CLAIM BACK UNTOUCHED: the row
// is as it was before the claim — nothing counted, no answer, a walk's
// earlier claim instant kept — and the next carrier, a walk or a page,
// may claim it. A claim given back is spent.
func TestAPageGivesItsClaimBackUntouched(t *testing.T) {
	s := noticeStore(t, "fresh", "refused")
	walk := claimFor(t, s, "refused")
	if _, _, err := s.RecordDeliveryAttempt(walk, "telegram(nothing accepted)", false, 8); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"fresh", "refused"} {
		before := row(t, s, id)
		d, err := s.ClaimForPage(id)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if err := s.ReleaseDispatch(d); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if after := row(t, s, id); after != before {
			t.Fatalf("%s: a claim given back left a trace:\n before %+v\n after  %+v", id, before, after)
		}
		if err := s.ReleaseDispatch(d); !errors.Is(err, ErrClaimSuperseded) {
			t.Fatalf("%s: a claim was given back twice: %v", id, err)
		}
		if !pageHas(t, s, id) {
			t.Fatalf("%s: a notice whose claim was given back is not the page's", id)
		}
	}
	if pending, err := s.PendingNotices(); err != nil || len(pending) != 2 {
		t.Fatalf("the notices given back do not wait for the route: %d %v", len(pending), err)
	}
	if _, err := s.ClaimDispatch("fresh"); err != nil {
		t.Fatalf("a walk may not claim a notice the page gave back: %v", err)
	}
}

// A WALK THAT SENT NOTHING LEAVES THE NOTICE TO THE PAGE — refused and
// waiting, or refused for good — and the page takes it once it opens.
// A person's message is never the page's.
func TestAWalkThatSentNothingLeavesTheNoticeToThePage(t *testing.T) {
	s := noticeStore(t, "refused", "never")
	for id, permanent := range map[string]bool{"refused": false, "never": true} {
		d := claimFor(t, s, id)
		if _, _, err := s.RecordDeliveryAttempt(d, "telegram(nothing accepted)", permanent, 8); err != nil {
			t.Fatal(err)
		}
		if !pageHas(t, s, id) {
			t.Fatalf("%s: a notice nothing carried is not the page's", id)
		}
		if err := pageTakes(s, id); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if m := row(t, s, id); m.Delivered != 1 || m.DeliveredVia != "dashboard" || m.Attempts != 2 || m.DispatchedMs > m.LastAttemptMs {
			t.Fatalf("%s: the page's carriage after a refused walk: %+v", id, m)
		}
	}
	if err := s.AddOutboxMessage("peer", "peer", "friend", "for a person", nil); err != nil {
		t.Fatal(err)
	}
	if pageHas(t, s, "peer") {
		t.Fatal("a person's message is on the page's list")
	}
	if _, err := s.ClaimForPage("peer"); !errors.Is(err, ErrClaimRefused) {
		t.Fatalf("the page claimed a person's message: %v", err)
	}
}

// THE BOOT SETTLES A NOTICE IN FLIGHT AS IT DOES A PERSON'S MESSAGE: the
// process stopped mid-send — a walk's, or a page's write — it may have
// gone, and the page does not push it as new or take it.
func TestTheBootSettlesANoticeInFlightAsUnknown(t *testing.T) {
	s := noticeStore(t, "flying", "writing", "waiting")
	claimFor(t, s, "flying")
	if _, err := s.ClaimForPage("writing"); err != nil {
		t.Fatal(err)
	}
	if n, err := s.SettleInterruptedDispatches("the process stopped while it was being sent — it may have arrived"); err != nil || n != 2 {
		t.Fatalf("settled %d, %v; want the two notices in flight", n, err)
	}
	for _, id := range []string{"flying", "writing"} {
		if m := row(t, s, id); !m.Parked || m.Effect != "unknown" || m.Attempts != 1 || m.DeliveredVia != "" {
			t.Fatalf("%s: the notice in flight is not settled as unknown: %+v", id, m)
		}
		if pageHas(t, s, id) {
			t.Fatalf("%s: the page may push a notice the boot settled as unknown", id)
		}
		if _, err := s.ClaimForPage(id); !errors.Is(err, ErrClaimRefused) {
			t.Fatalf("%s: the page claimed a notice the boot settled as unknown: %v", id, err)
		}
	}
	if !pageHas(t, s, "waiting") {
		t.Fatal("the page's list after the boot's settlement is wrong")
	}
}

func TestTheRouteTakesNoNoticeARestoreBroughtBack(t *testing.T) {
	s := noticeStore(t, "old", "older")
	// "older" is made a second older than "old", whenever each was written:
	// a bump of its own stamp by a second left it the newer one when the two
	// writes were more than a second apart, as on a loaded -race shard.
	gap := row(t, s, "old").CreatedMs - row(t, s, "older").CreatedMs
	if err := s.BumpOutboxCreatedMs("older", gap-1000); err != nil {
		t.Fatal(err)
	}
	if err := s.AddOutboxMessage("peer", "peer", "friend", "for a person", nil); err != nil {
		t.Fatal(err)
	}
	pending, err := s.PendingNotices()
	if err != nil || len(pending) != 2 || pending[0].ID != "older" || pending[1].ID != "old" {
		t.Fatalf("the route's notices, oldest first, no person's: %+v %v", pending, err)
	}

	r := Restore{At: time.Now().UTC(), Source: "snapshot"}
	if n, err := s.SettleRestore(r, "restored from a snapshot"); err != nil || n != 1 {
		t.Fatalf("the restore parked %d (%v); want only the person's message", n, err)
	}
	for _, id := range []string{"old", "older"} {
		if m := row(t, s, id); m.Parked || m.Attempts != 0 || m.Effect != "" {
			t.Fatalf("the restore's settlement parked the operator's notice %s: %+v", id, m)
		}
		if !pageHas(t, s, id) {
			t.Fatalf("a notice the restore brought back is not the page's: %s", id)
		}
	}
	time.Sleep(2 * time.Millisecond) // a notice written after the restore instant
	if err := s.AddOutboxNotice("since", "operator", "", "written after the restore", nil); err != nil {
		t.Fatal(err)
	}
	if pending, err := s.PendingNotices(); err != nil || len(pending) != 1 || pending[0].ID != "since" {
		t.Fatalf("the route takes only what was written since the restore: %+v %v", pending, err)
	}
	if !r.BroughtBack(row(t, s, "old").CreatedMs) || r.BroughtBack(row(t, s, "since").CreatedMs) {
		t.Fatal("BroughtBack disagrees with the route's own boundary")
	}

	// A source made on a clock that ran ahead keeps its rows back too.
	ahead := Restore{At: time.Now().UTC(), Source: "snapshot", SourceAt: time.Now().Add(time.Hour).UTC()}
	if !ahead.BroughtBack(time.Now().Add(30 * time.Minute).UnixMilli()) {
		t.Fatal("a row stamped before its source's own time came back with it")
	}

	// A record that does not read decides nothing in the route's favour.
	if _, err := s.DB().Exec(`UPDATE runtime_meta SET value = 'not json' WHERE key = ?`, RestoreKey); err != nil {
		t.Fatal(err)
	}
	if pending, err := s.PendingNotices(); err == nil || len(pending) != 0 {
		t.Fatalf("an unreadable restore record let notices walk: %+v %v", pending, err)
	}
}

// The page's list of notices: the newest first, bounded, with the total.
func TestNoticesListsTheNewestWithTheirTotal(t *testing.T) {
	s := noticeStore(t, "a", "b", "c")
	for i, id := range []string{"a", "b", "c"} {
		if err := s.BumpOutboxCreatedMs(id, int64(i*10)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AddOutboxMessage("peer", "peer", "friend", "for a person", nil); err != nil {
		t.Fatal(err)
	}
	p, err := s.Notices(t.Context(), 2)
	if err != nil || p.Total != 3 || len(p.Rows) != 2 || p.Rows[0].ID != "c" || p.Rows[1].ID != "b" {
		t.Fatalf("the newest two of three notices: %+v %v", p, err)
	}
}
