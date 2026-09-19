// .
// .
// .
// .
// .
// .

package pluginhost

import (
	"encoding/json"
	"fmt"
	"testing"
)

// .
// .
func TestReviewStatusCompletionCannotBypassRegisteredFinish(t *testing.T) {
	for _, tc := range []struct {
		name, stream string
		end          int64
	}{
		{"past_cutoff", "in", 999},
		{"wrong_handle", "other-input", 320},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, source := range []string{"event", "status"} {
				t.Run(source, func(t *testing.T) {
					p := newEpochProbe(t)
					p.open("s1")
					done := make(chan error, 1)
					go func() { done <- p.v.FinishInputFor(p.ctx, "s1", "in", 320) }()
					p.reply(p.read("speech.session.finish_input"), `{"accepted":true}`)
					if err := <-done; err != nil {
						t.Fatal(err)
					}
					c := InputCompletion{StreamID: tc.stream, EndSample: tc.end, ProcessedEndSample: tc.end, Sequence: 1, Reason: "capture_limit"}
					raw, err := json.Marshal(c)
					if err != nil {
						t.Fatal(err)
					}
					if source == "event" {
						probeEvent(p, fmt.Sprintf(`{"type":"input_finished","session_id":"s1",%s`, raw[1:]))
						p.observed()
					} else {
						go func() {
							p.reply(p.read("speech.session.status"), fmt.Sprintf(`{"session_id":"s1","state_sequence":1,"lifecycle":"open","input_completion":%s}`, raw))
						}()
						if _, err := p.v.Status(p.ctx); err != nil {
							return
						}
					}
					if !p.v.Faulted() {
						t.Fatalf("%s accepted contradictory completion %+v against acknowledged input in@320", source, p.v.InputCompletionInfo())
					}
				})
			}
		})
	}
}
