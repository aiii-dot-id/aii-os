package identity

import (
	"context"
	"strings"
	"testing"
)

// .
// .
// .
func TestVerbMeasureIsObservational(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)
	out, err := engine.ExecuteAction(context.Background(), "verb", "measure", map[string]interface{}{})
	if err != nil {
		t.Fatalf("measure: %v", err)
	}
	for _, want := range []string{"Outcome measurement", "process_only_turns", "not success", "source:", "unknown", "Record audit", "ungrounded beliefs", "open tensions", "Meaning layer"} {
		if !strings.Contains(out, want) {
			t.Fatalf("readout missing %q:\n%s", want, out)
		}
	}
	// .
	if _, err := engine.ExecuteAction(context.Background(), "verb", "measure", map[string]interface{}{"hours": 24}); err != nil {
		t.Fatalf("measure with hours: %v", err)
	}
}
