package cognitive

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/llm/wire"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

var errSourceDown = errors.New("disk is on fire")

// failingBriefStore fails exactly one source and answers the rest.
type failingBriefStore struct{ fail string }

func (f failingBriefStore) ListIntentions() ([]store.Intention, error) {
	if f.fail == "intentions" {
		return nil, errSourceDown
	}
	return nil, nil
}
func (f failingBriefStore) ListSelfModelSyntheses(n int, beforeSeq uint64) ([]store.SelfModelSynthesis, error) {
	if f.fail == "syntheses" {
		return nil, errSourceDown
	}
	return nil, nil
}
func (f failingBriefStore) ListExperiencesSince(time.Time, int) ([]store.Experience, error) {
	if f.fail == "experiences" {
		return nil, errSourceDown
	}
	return nil, nil
}

type countingLLM struct{ calls int }

func (c *countingLLM) ChatSimple(ctx context.Context, sys, user string) (string, string, error) {
	c.calls++
	return "a confident good morning", "m", nil
}
func (c *countingLLM) ChatStructured(ctx context.Context, sys, user string, tool wire.ToolDefinition) (string, string, bool, error) {
	c.calls++
	return "", "m", false, nil
}

type recordingBriefWriter struct {
	brief string
	sets  int
}

func (w *recordingBriefWriter) SetBrief(content string) { w.sets++; w.brief = content }

func TestMorningBriefRefusesToSpeakFromEvidenceItCouldNotRead(t *testing.T) {
	const yesterday = "yesterday's brief, which must survive untouched"
	for _, source := range []string{"intentions", "syntheses", "experiences"} {
		t.Run(source, func(t *testing.T) {
			llm := &countingLLM{}
			w := &recordingBriefWriter{brief: yesterday}
			mb := NewMorningBrief(failingBriefStore{fail: source}, llm, w, MorningBriefConfig{})

			err := mb.Execute(context.Background())
			if err == nil {
				t.Fatal("a failed read produced no error — the brief was composed from an evidence set that could not be read")
			}
			if !errors.Is(err, errSourceDown) {
				t.Errorf("the underlying failure was not wrapped: %v", err)
			}
			if !strings.Contains(err.Error(), source[:5]) {
				t.Errorf("the error does not name its source (%s): %v", source, err)
			}
			if llm.calls != 0 {
				t.Errorf("the model was called %d time(s) on unreadable evidence", llm.calls)
			}
			if w.sets != 0 {
				t.Errorf("SetBrief was called %d time(s) — a failed read must not replace the brief", w.sets)
			}
			if w.brief != yesterday {
				t.Errorf("the previous brief was overwritten: %q", w.brief)
			}
		})
	}
}

func TestABriefWhoseModelCallFailedIsAskedForAgainThatDay(t *testing.T) {
	const yesterday = "yesterday's bridge"
	// The brief's hour is twelve hours off, so the next brief of its own is
	// between eleven hours fifty-nine minutes and twelve hours away.
	at := time.Now().UTC().Add(12 * time.Hour).Format("15:04")
	fire := func(model *mockLLM, spacing time.Duration) (AlarmResult, *mockBriefWriter) {
		bw := &mockBriefWriter{brief: yesterday}
		mb := NewMorningBrief(&mockStore{}, model, bw, MorningBriefConfig{LocalTime: at, Spacing: spacing})
		return mb.OnAlarm(context.Background(), "morning_brief", "wall", 0, ""), bw
	}

	logs := logsink.CaptureForTest(t)
	down := &mockLLM{err: errors.New("provider unreachable")}
	before := time.Now()
	res, bw := fire(down, time.Hour)
	if res.Accepted || res.NextDeadline == nil {
		t.Fatalf("A FAILED CALL WAS TAKEN FOR THE DAY'S BRIEF: accepted %v, a deadline named %v", res.Accepted, res.NextDeadline != nil)
	}
	if again := time.UnixMilli(*res.NextDeadline); again.Before(before.Add(time.Hour-time.Second)) || again.After(time.Now().Add(time.Hour+time.Second)) {
		t.Fatalf("the brief is asked for again at %s; want one spacing, an hour, from now", again.UTC().Format(time.RFC3339))
	}
	if down.calls != 1 || bw.brief != yesterday {
		t.Fatalf("the failed pass made %d call(s) and left the brief %q; want one call and yesterday's standing", down.calls, bw.brief)
	}
	// The operator is told when, and by which setting, in the log.
	if said := "the brief's model call failed — it is asked for again at " + time.UnixMilli(*res.NextDeadline).UTC().Format("15:04 UTC") +
		" (cognition.consolidate_spacing_seconds), and the brief that stood stands until then"; !logs.Contains(said) {
		t.Fatalf("THE LOG DOES NOT SAY WHEN THE BRIEF IS ASKED FOR AGAIN; want %q in\n%s", said, logs.String())
	}

	// Where no spacing is set the wait is the rhythm's own default, thirty
	// minutes, and not none: a facility built without one does not ask a
	// model that is away again at once.
	before = time.Now()
	res, _ = fire(&mockLLM{err: errors.New("provider unreachable")}, 0)
	if res.Accepted || res.NextDeadline == nil {
		t.Fatalf("with no spacing set a failed call was taken for the day's brief: accepted %v", res.Accepted)
	}
	if again := time.UnixMilli(*res.NextDeadline); again.Before(before.Add(DefaultSpacing-time.Second)) || again.After(time.Now().Add(DefaultSpacing+time.Second)) {
		t.Fatalf("WITH NO SPACING SET THE BRIEF IS ASKED FOR AGAIN AT %s; want the default spacing, %s, from now", again.UTC().Format(time.RFC3339), DefaultSpacing)
	}

	// A spacing that reaches past the next brief's own hour asks nothing
	// in between: the alarm is armed for that hour, as after a brief.
	res, _ = fire(&mockLLM{err: errors.New("provider unreachable")}, 13*time.Hour)
	if !res.Accepted || res.NextDeadline == nil || time.Until(time.UnixMilli(*res.NextDeadline)) > 12*time.Hour+time.Minute {
		t.Fatalf("with the next brief's hour nearer than the spacing the alarm was not armed for that hour: accepted %v", res.Accepted)
	}

	// A refusal for what was asked would be refused the same way again:
	// tomorrow's is asked for as this one was, and none before it.
	res, _ = fire(&mockLLM{err: &wire.IncompleteResponseError{Reason: "length", Got: 40}}, time.Hour)
	if !res.Accepted || res.NextDeadline == nil || time.Until(time.UnixMilli(*res.NextDeadline)) < 11*time.Hour {
		t.Fatalf("a brief refused for what it asked is asked for again the same day: accepted %v", res.Accepted)
	}

	// And the model back, the asking again writes the day's brief and arms
	// the alarm for the next one.
	res, bw = fire(&mockLLM{override: "A short bridge, written this morning."}, time.Hour)
	if !res.Accepted || bw.brief != "A short bridge, written this morning." {
		t.Fatalf("with the model back the brief was not written: accepted %v, the brief held is %q", res.Accepted, bw.brief)
	}
}
