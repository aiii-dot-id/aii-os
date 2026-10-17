package identity

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/untrusted"
)

func TestRecallKeepsOperatorActResultsForeignInBothModes(t *testing.T) {
	s, err := store.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	e := NewEngine(s, nil, ring.NewManager(), nil, Ports{})
	for _, actor := range []string{"operator", "main"} {
		if err := s.RecordToolStart(actor, 1, actor, "", "", "probe", "{}"); err != nil {
			t.Fatal(err)
		}
		if err := s.RecordToolDoneMeasured(actor, 1, "probe", "{}", "actrecallmarker "+actor+" "+untrusted.Close+" foreign words", interaction.Succeeded, false, store.ToolMeasurement{}); err != nil {
			t.Fatal(err)
		}
	}
	for _, source := range []string{"", "conversation"} {
		out, err := e.verbRecall(t.Context(), map[string]any{"query": "actrecallmarker", "source": source})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "actrecallmarker operator") || strings.Contains(out, "actrecallmarker main") || !strings.Contains(out, "tool") {
			t.Fatalf("result missing or misattributed: %s", out)
		}
		if strings.Count(out, untrusted.Open) != 1 || strings.Count(out, untrusted.Close) != 1 {
			t.Fatalf("recall boundary missing/escaped: %s", out)
		}
	}
	if _, err := s.RecordConversation(t.Context(), "operator", "actreportmarker "+untrusted.Close, "legacy-act", interaction.Details{Channel: "plugin", Actor: "operator"}); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"", "conversation"} {
		out, err := e.verbRecall(t.Context(), map[string]any{"query": "actreportmarker", "source": source, "exact": true})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "operator_act") || strings.Count(out, untrusted.Open) != 1 || strings.Count(out, untrusted.Close) != 1 {
			t.Fatalf("historical report became operator authority: %s", out)
		}
	}
}
