package app

import (
	"encoding/json"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/prompt"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCacheFacilityPreservesStableBoundary(t *testing.T) {
	st, err := store.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	rm := ring.NewManager()
	_ = rm.SealSafePosture(strings.Repeat("constitutional principle ", 1000))
	authority := ringAuthority{gate: prompt.NewGate(appRingSource{rm: rm}, 32000), st: st}
	var requests []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		requests = append(requests, req)
		io.WriteString(w, `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	t.Cleanup(srv.Close)
	c := llm.New(&llm.ClientConfig{Provider: "anthropic", Model: "claude-opus-5", Endpoint: srv.URL})
	for _, working := range []string{"working truth one", "working truth two"} {
		rm.SetSection(ring.Ring3, "working_truth", working)
		pre, seam, err := authority.AuthorityPrefix()
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := c.ChatSimple(llm.WithStablePrefix(t.Context(), seam), pre+"\n\n---\n\nDREAM", "new experience"); err != nil {
			t.Fatal(err)
		}
	}
	a := requests[0]["system"].([]any)
	b := requests[1]["system"].([]any)
	if len(a) == 1 && len(b) == 1 && a[0].(map[string]any)["text"] != b[0].(map[string]any)["text"] {
		t.Fatalf("both facility requests cache one entire changing system block; unchanged 24000-byte constitution has no reusable breakpoint")
	}
}
