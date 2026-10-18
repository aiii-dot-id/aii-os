package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// storedBrief seeds a brief whose row says it was written at, in the words
// given: a time as the store writes one, or anything else.
func storedBrief(t *testing.T, content, at string) func(*store.Store) {
	return func(s *store.Store) {
		t.Helper()
		if err := s.SaveBrief(content); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB().Exec(`UPDATE ring_snapshots SET updated_at = ? WHERE section = '__brief__'`, at); err != nil {
			t.Fatal(err)
		}
	}
}

// A START RESTORES THE BRIEF WITH THE DAY IT WAS WRITTEN. The store kept
// the day and the start restored the brief as written now, so after every
// restart a brief of any age stood under "# This Morning ... since this
// time yesterday". The start hands the stored day to the prompt, which
// heads the brief by it.
func TestAStartRestoresTheBriefWithTheDayItWasWritten(t *testing.T) {
	const bridge = "Quinn landed the deliver gate."
	url, asked := briefModel(t)
	monday := time.Now().UTC().Add(-3 * 24 * time.Hour).Truncate(time.Second)
	at := time.Now().UTC().Add(12 * time.Hour) // the brief's hour: none is due at this start
	a := bootWithBrief(t, at.Format("15:04"), url, storedBrief(t, bridge, monday.Format(time.RFC3339Nano)))
	if content, written := a.rings.Brief(); content != bridge || !written.Equal(monday) {
		t.Fatalf("A START RESTORED THE BRIEF %q AS WRITTEN %s; want it as written %s", content, written.UTC().Format(time.RFC3339), monday.Format(time.RFC3339))
	}
	p, err := a.composer.Compose("", 0)
	if err != nil {
		t.Fatal(err)
	}
	want := "# Your Last Brief\n\nWritten on " + monday.Format("2006-01-02") +
		" (UTC); no brief has been written since. The bridge from where things stood then to what you were doing: what had changed since the day before.\n\n" + bridge
	if !strings.Contains(p.Text, want) || strings.Contains(p.Text, "# This Morning") {
		t.Fatalf("AFTER A START A BRIEF WRITTEN ON %s IS NOT SHOWN UNDER ITS DATE; want\n%s\nin\n%s", monday.Format("2006-01-02"), want, p.Text)
	}
	if asked() != 0 {
		t.Fatalf("rig: a brief was written at the start (%d)", asked())
	}
}

// A STORED BRIEF WHOSE DATE CANNOT BE READ IS NOT SHOWN, AND THE LOG SAYS
// SO. The prompt could neither call it this morning's nor date it. It
// stays in the store until the next brief written replaces it.
func TestAStoredBriefWithNoReadableDateIsNotRestoredAndTheLogSaysSo(t *testing.T) {
	const bridge = "Quinn landed the deliver gate."
	logs := logsink.CaptureForTest(t)
	url, _ := briefModel(t)
	at := time.Now().UTC().Add(12 * time.Hour)
	a := bootWithBrief(t, at.Format("15:04"), url, storedBrief(t, bridge, "last Monday"))
	if got := a.rings.GetBrief(); got != "" {
		t.Fatalf("A BRIEF THAT CANNOT BE DATED IS IN THE PROMPT: %q", got)
	}
	const said = "the stored morning brief's date cannot be read — the brief is not restored to the prompt; the next brief written replaces it"
	if !logs.Contains(said) {
		t.Fatalf("THE LOG DOES NOT SAY THE STORED BRIEF WAS LEFT OUT; want %q in\n%s", said, logs.String())
	}
	if kept, err := a.store.GetBrief(); err != nil || kept != bridge {
		t.Fatalf("the stored brief is %q (%v); want it kept as it was", kept, err)
	}
}

// THE WAIT A FAILED BRIEF IS GIVEN IS THE SPACING THE OPERATOR SET. The
// brief's asking-again waits cognition.consolidate_spacing_seconds, the
// one number for how long a pass that came to nothing waits; the facility
// is handed it with its hour and its bound, and a brief built without it
// would wait the default whatever the file said.
func TestAFailedBriefWaitsTheSpacingTheOperatorSet(t *testing.T) {
	var asked atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "morning brief for the identity") {
			asked.Add(1)
			http.Error(w, "fixture provider failure", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, textReply("ok"))
	}))
	t.Cleanup(srv.Close)
	const spacing = 2 * time.Hour
	at := time.Now().UTC().Add(12 * time.Hour)
	a := bootWithBriefSet(t, at.Format("15:04"), srv.URL, nil, func(c *Config) {
		c.Cognition.ConsolidateSpacingSeconds = int(spacing / time.Second)
	})
	var res cognitive.AlarmResult
	var before time.Time
	waitUntil(t, func() bool { // a firing deferred for a turn names no deadline: fire again
		before = time.Now()
		res = a.briefFacility.OnAlarm(t.Context(), "morning_brief", "wall", 0, "")
		return res.NextDeadline != nil
	}, "the brief's alarm answered with a deadline")
	if res.Accepted || asked.Load() == 0 {
		t.Fatalf("rig: the brief's call was to fail: accepted %v, asked %d", res.Accepted, asked.Load())
	}
	if again := time.UnixMilli(*res.NextDeadline); again.Before(before.Add(spacing-time.Second)) || again.After(time.Now().Add(spacing+time.Second)) {
		t.Fatalf("WITH THE SPACING SET TO %s A FAILED BRIEF IS ASKED FOR AGAIN IN %s", spacing, again.Sub(before).Round(time.Second))
	}
}
