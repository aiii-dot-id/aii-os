// .
// .
// .
// .
// .

package app

import (
	"context"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

func TestAuditSynthesisEndDoesNotForgetUnrenderedTypedReply(t *testing.T) {
	a, h, f := auditOutputApp(t)
	a.settleVoice(context.Background(), "old answer buffered at the page")
	first := f.producingNow()
	if first == "" {
		t.Fatal("fixture has no first synthesis")
	}
	f.mu.Lock()
	f.producing = ""
	f.mu.Unlock()
	a.voiceObserved(pluginhost.Event{Type: "synthesis_end", SessionID: h.id, Raw: []byte(`{"synthesis_id":"` + first + `","output_stream":1,"playback_verified":false}`)})
	a.settleVoice(context.Background(), "new answer must replace the buffered old one")
	for _, op := range f.opsSeen() {
		if strings.HasPrefix(op, "interrupt:"+first+":") {
			return
		}
	}
	t.Fatalf("production end with no rendered receipt discarded old playback custody; next reply sent without its fence: %v", f.opsSeen())
}
