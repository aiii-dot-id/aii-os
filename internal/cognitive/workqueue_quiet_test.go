package cognitive

import (
	"context"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/store"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// .
type quietHandler struct{}

func (quietHandler) WorkKinds() []string { return []string{"alarm.memory", "real.work"} }
func (quietHandler) RunWork(context.Context, *store.WorkItem) error {
	return nil
}

// .
// .
// .
// .
// .
func TestAnAlarmTickIsNotACompletionWorthALine(t *testing.T) {
	e := NewExecutor(&memQueue{})
	h := quietHandler{}
	e.handlers["alarm.memory"] = h
	e.handlers["real.work"] = h

	buf := logsink.CaptureForTest(t)

	e.runOne(context.Background(), &store.WorkItem{ID: "a1", Kind: "alarm.memory"})
	if strings.Contains(buf.String(), "a1") {
		t.Fatalf("an uneventful alarm reported its completion: %s", buf.String())
	}

	e.runOne(context.Background(), &store.WorkItem{ID: "w1", Kind: "real.work"})
	if !strings.Contains(buf.String(), "real.work w1 done") {
		t.Fatalf("real work must still report its completion, got: %q", buf.String())
	}
}
