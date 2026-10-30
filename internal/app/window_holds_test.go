package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"sync/atomic"
	"testing"
)

func TestAModelThatCannotHoldTheIdentityIsRefusedWhenChosen(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	var asked atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()
	before := app.llmSwap.Current()

	// 12,000 tokens less a 1,024-token reply and the 2,048 margin leaves
	// 8,928 for a prompt: less than this identity's tools alone.
	err := app.changeProviders("test", func(r *providerRegistry) error {
		r.Providers[0].URL, r.Providers[0].ContextLength, r.Providers[0].MaxOutputTokens = srv.URL, 12_000, 1_024
		return nil
	})
	want := regexp.MustCompile(`^substrate refused: provider "test" model "m" cannot hold this identity — the request it sends before any history is about (\d+) tokens \((\d+) of them its tools' definitions\), and a window of 12000 tokens leaves 8928 for a prompt; a window of at least (\d+) tokens holds it; current substrate kept$`)
	m := want.FindStringSubmatch(errText(err))
	if m == nil {
		t.Fatalf("the small model was not refused with the window it would need: %v", err)
	}
	required, _ := strconv.Atoi(m[1])
	tools, _ := strconv.Atoi(m[2])
	needs, _ := strconv.Atoi(m[3])
	if required <= 8928 || tools <= 0 || tools >= required || needs != required+1_024+promptSafetyTokens {
		t.Fatalf("the refusal's numbers do not add up: request %d, tools %d, window needed %d", required, tools, needs)
	}
	if n := asked.Load(); n != 0 {
		t.Fatalf("the refused model was asked %d requests; a window that cannot hold the identity is refused before any", n)
	}
	if app.llmSwap.Current() != before {
		t.Fatal("the refused edit replaced the running substrate")
	}

	// The window that refusal names holds it.
	err = app.changeProviders("test", func(r *providerRegistry) error {
		r.Providers[0].URL, r.Providers[0].ContextLength, r.Providers[0].MaxOutputTokens = srv.URL, needs, 1_024
		return nil
	})
	if err != nil {
		t.Fatalf("a window of %d tokens, the one the refusal named, was refused: %v", needs, err)
	}
	if asked.Load() == 0 {
		t.Fatal("the model whose window holds the identity was taken without being proved")
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
