package identity

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// activityStore stands in for the record's read of a session's last
// activity: what the store answers, the three ways it can.
type activityStore struct {
	*store.Store
	at    time.Time
	found bool
	err   error
}

func (s *activityStore) WorkSessionLastActivity(string) (time.Time, bool, error) {
	return s.at, s.found, s.err
}

// A SESSION'S LAST ACTIVITY: THE TIME OF ITS NEWEST RECORDED CALL OR
// NOTICE; "time unreadable" WHERE THE RECORD HOLDS A TIME THAT DOES NOT
// READ, WITH THE CAUSE IN THE LOG; ITS START WHERE IT HAS NONE; AND ITS
// SEQUENCE WHERE IT HAS NEITHER.
func TestASessionsLastActivitySaysWhenTheTimeCannotBeRead(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	ctx := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn_t"), "operator")
	if _, err := engine.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "start", "description": "a past task"}); err != nil {
		t.Fatal(err)
	}
	past, _ := st.ActiveWorkSession()
	if _, err := engine.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "deliver", "result": "served: done"}); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "start", "description": "the current task"}); err != nil {
		t.Fatal(err)
	}
	row := func() string {
		out, err := engine.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "status"})
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range strings.Split(out, "\n") {
			if strings.HasPrefix(l, past.ID+" ·") {
				return l
			}
		}
		t.Fatalf("the past session is not listed:\n%s", out)
		return ""
	}
	// The real record: the delivery is the newest recorded change.
	if r := row(); !regexp.MustCompile(` · last \d\d:\d\dZ · `).MatchString(r) {
		t.Fatalf("with a recorded time: %q", r)
	}
	stand := &activityStore{Store: st}
	engine.store = stand
	today := time.Now().UTC().Truncate(24 * time.Hour).Add(1*time.Hour + 23*time.Minute)
	stand.at, stand.found = today, true
	if r := row(); !strings.Contains(r, " · last 01:23Z · ") {
		t.Fatalf("the record's time today: %q", r)
	}
	// Not today: the date before the clock.
	stand.at = today.Add(-48 * time.Hour)
	if r := row(); !strings.Contains(r, " · last "+stand.at.Format("Jan 2")+" 01:23Z · ") {
		t.Fatalf("a time before today: %q", r)
	}
	stand.at = today
	// A time the record holds that does not read: said so, the cause logged.
	logs := logsink.CaptureForTest(t)
	stand.found, stand.err = false, errors.New(`session: the record's time "not a time": parsing time "not a time" as "2006-01-02T15:04:05.999999999Z07:00"`)
	if r := row(); !strings.Contains(r, " · last time unreadable · ") {
		t.Fatalf("an unreadable time is not said: %q", r)
	}
	if !strings.Contains(logs.String(), "could not be read") || !strings.Contains(logs.String(), "not a time") {
		t.Fatalf("the log does not carry the cause:\n%s", logs.String())
	}
	// No call or notice recorded: the session's start.
	stand.err = nil
	if r := row(); !regexp.MustCompile(` · last \d\d:\d\dZ · `).MatchString(r) || strings.Contains(r, "unreadable") {
		t.Fatalf("with no record, the start: %q", r)
	}
	// Neither: the sequence.
	if _, err := st.DB().Exec(`UPDATE work_sessions SET started_at = 0, updated_seq = 1 WHERE id = ?`, past.ID); err != nil {
		t.Fatal(err)
	}
	if r := row(); !strings.Contains(r, " · last seq 1 · ") {
		t.Fatalf("with neither, the sequence: %q", r)
	}
}
