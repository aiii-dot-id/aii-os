package cognitive

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
// .
// .
// .
// .
// .
// .
// .

var errSourceDown = errors.New("disk is on fire")

// .
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
func (c *countingLLM) ChatStructured(ctx context.Context, sys, user string, tool llm.ToolDefinition) (string, string, bool, error) {
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
