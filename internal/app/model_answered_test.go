package app

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// answeringProvider is a provider that answers, counting the inference requests it is sent.
func answeringProvider(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var asked atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			w.Write([]byte(`{"data":[{"id":"m-one"},{"id":"m-two"}]}`))
			return
		}
		asked.Add(1)
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}]}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &asked
}

// THE REPLY TO A SAVE SAYS WHETHER THE MODEL WAS ASKED. The host sets it where it decides to ask,
// so it holds for a save that sends a model already in force, from a screen that had not heard of
// it, as well as for one that changes nothing about the model. A plain read says nothing of it.
func TestTheReplyToASaveSaysWhetherTheModelWasAsked(t *testing.T) {
	srv, asked := answeringProvider(t)
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Answered")
	cfg := safebootConfig(t, dir, "Answered", keyPath, ledgerPath, dbPath)
	cfg.Dashboard.Port = 0
	cfg.SourcePath = filepath.Join(dir, "config.json")
	cfg.LLM = withTestProvider(t, dir, "prov", srv.URL, "m-one", "sk-test-key-1234")
	t.Chdir(dir)
	a := New(cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatalf("startLive: %v", err)
	}
	t.Cleanup(func() { stopApp(t, a) })

	save := func(what string, changes map[string]any, wantAnswered bool, wantAsked int32) {
		t.Helper()
		before := asked.Load()
		st, err := a.applyConfigChange(changes)
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		if st.LLM.Answered != wantAnswered {
			t.Errorf("%s: the reply says answered=%v, want %v", what, st.LLM.Answered, wantAnswered)
		}
		if got := asked.Load() - before; (got > 0) != (wantAsked > 0) {
			t.Errorf("%s: the provider was asked %d times", what, got)
		}
	}
	save("a new model", map[string]any{"llm.provider": "prov", "llm.model": "m-two"}, true, 1)
	save("the same model, from a screen that had not heard", map[string]any{"llm.provider": "prov", "llm.model": "m-two"}, false, 0)
	save("a wait alone", map[string]any{"llm.refused_retry_max_seconds": 600}, false, 0)
	if a.configState().LLM.Answered {
		t.Error("a plain read says the model answered")
	}
}
