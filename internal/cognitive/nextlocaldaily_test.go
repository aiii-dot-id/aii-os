package cognitive

import (
	"testing"
	"time"
)

// NextLocalDaily is now the ONE source for every daily deadline — the
// morning brief and the maintenance pass both re-arm through it — but
// nothing pinned its today's-if-still-ahead branch. Deleting that branch
// (so it always returns tomorrow's hour) left the whole package green:
// TestMorningBriefTimezone asserts only next > now, which cannot tell
// today's 07:00 from tomorrow's, and a maintenance alarm that silently
// skips to tomorrow is exactly the every-boot drift the shared helper
// was extracted to end.
//
// Fixed instants, no real clock: the boundary is the interesting part,
// and "still ahead" must mean strictly ahead — at exactly hour:min the
// deadline has passed, so the next one is tomorrow's, or an alarm
// settling ON its own deadline re-arms to the instant it just fired.
func TestNextLocalDailyPicksTodayOnlyWhileItIsStillAhead(t *testing.T) {
	loc := time.FixedZone("TEST", 5*3600)
	at := func(y int, mo time.Month, d, h, mi, s, ns int) time.Time {
		return time.Date(y, mo, d, h, mi, s, ns, loc)
	}
	for _, c := range []struct {
		name      string
		now       time.Time
		hour, min int
		want      time.Time
	}{
		{"before today's hour, today's", at(2026, 8, 27, 3, 0, 0, 0), 4, 0, at(2026, 8, 27, 4, 0, 0, 0)},
		{"a minute before, still today's", at(2026, 8, 27, 3, 59, 0, 0), 4, 0, at(2026, 8, 27, 4, 0, 0, 0)},
		{"exactly on it, tomorrow's", at(2026, 8, 27, 4, 0, 0, 0), 4, 0, at(2026, 8, 28, 4, 0, 0, 0)},
		{"a millisecond past, tomorrow's", at(2026, 8, 27, 4, 0, 0, int(time.Millisecond)), 4, 0, at(2026, 8, 28, 4, 0, 0, 0)},
		{"late evening, tomorrow's", at(2026, 8, 27, 23, 59, 0, 0), 4, 0, at(2026, 8, 28, 4, 0, 0, 0)},
		{"year roll", at(2026, 12, 31, 23, 0, 0, 0), 4, 0, at(2027, 1, 1, 4, 0, 0, 0)},
		{"minutes carried, not just the hour", at(2026, 8, 27, 7, 20, 0, 0), 7, 30, at(2026, 8, 27, 7, 30, 0, 0)},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := NextLocalDaily(c.now, c.hour, c.min)
			if got != c.want.UnixMilli() {
				t.Fatalf("from %s the next %02d:%02d is %s, want %s",
					c.now.Format(time.RFC3339Nano), c.hour, c.min,
					time.UnixMilli(got).In(loc).Format(time.RFC3339), c.want.Format(time.RFC3339))
			}
		})
	}
}

// The deadline must land in the caller's OWN location: a UTC-derived
// 04:00 fires at 09:00 local for an operator five hours east, which is
// the same "quiet hour" promise broken silently.
func TestNextLocalDailyStaysInItsOwnLocation(t *testing.T) {
	east := time.FixedZone("EAST", 9*3600)
	west := time.FixedZone("WEST", -8*3600)
	now := time.Date(2026, 8, 27, 3, 0, 0, 0, east)
	got := time.UnixMilli(NextLocalDaily(now, 4, 0)).In(east)
	if h, m := got.Hour(), got.Minute(); h != 4 || m != 0 {
		t.Fatalf("east: next daily landed at %02d:%02d local, not 04:00", h, m)
	}
	nowW := time.Date(2026, 8, 27, 3, 0, 0, 0, west)
	gotW := time.UnixMilli(NextLocalDaily(nowW, 4, 0)).In(west)
	if h, m := gotW.Hour(), gotW.Minute(); h != 4 || m != 0 {
		t.Fatalf("west: next daily landed at %02d:%02d local, not 04:00", h, m)
	}
}
