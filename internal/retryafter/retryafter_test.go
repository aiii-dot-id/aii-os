package retryafter

import (
	"net/http"
	"testing"
	"time"
)

func TestRetryAfterIsTheServicesWordOrNothing(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 400_000_000, time.UTC) // .4s past the second
	date := func(d time.Duration) string { return now.Add(d).UTC().Format(http.TimeFormat) }
	for _, c := range []struct {
		name string
		h    string
		want time.Duration
		ok   bool
	}{
		{"delta-seconds", "120", 120 * time.Second, true},
		{"zero is a value the service sent", "0", 0, true},
		{"leading zeros", "007", 7 * time.Second, true},
		{"surrounding space", " 30 ", 30 * time.Second, true},
		{"the longest wait a Duration holds", "9223372036", 9223372036 * time.Second, true},
		// The date's resolution is the second; now's fraction rounds the wait up.
		{"an HTTP-date", date(2 * time.Minute), 120 * time.Second, true},
		{"an HTTP-date one second ahead", date(time.Second), time.Second, true},
		{"an RFC 850 date", now.Add(time.Hour).UTC().Format(time.RFC850), time.Hour, true},
		{"an asctime date", now.Add(time.Hour).UTC().Format(time.ANSIC), time.Hour, true},

		{"no header", "", 0, false},
		{"only space", "   ", 0, false},
		{"a word", "soon", 0, false},
		{"a fraction", "1.5", 0, false},
		{"a sign", "+5", 0, false},
		{"a negative", "-5", 0, false},
		{"a unit", "30s", 0, false},
		{"a delta past what a Duration holds", "9223372037", 0, false},
		{"a delta past int64", "99999999999999999999", 0, false},
		{"a date in the past", date(-time.Minute), 0, false},
		{"a date this very second", now.Truncate(time.Second).Format(http.TimeFormat), 0, false},
		{"a date past what a Duration holds", "Fri, 31 Dec 9999 23:59:59 GMT", 0, false},
		{"a malformed date", "Mon, 32 Sep 2026 12:00:00 GMT", 0, false},
	} {
		got, ok := Parse(c.h, now)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: Parse(%q) = %v, %v; want %v, %v", c.name, c.h, got, ok, c.want, c.ok)
		}
	}
}

// The date is read against the clock it is given, not the process's.
func TestAnHTTPDateIsReadAgainstTheGivenClock(t *testing.T) {
	h := "Mon, 28 Sep 2026 12:10:00 GMT"
	early := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	if got, ok := Parse(h, early); !ok || got != 10*time.Minute {
		t.Fatalf("ten minutes before: %v, %v", got, ok)
	}
	if got, ok := Parse(h, early.Add(time.Hour)); ok || got != 0 {
		t.Fatalf("fifty minutes after: %v, %v — a past date is no hint", got, ok)
	}
}
