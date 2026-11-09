//go:build !windows

package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/coder/websocket"
)

// A REPLY IS SHOWN AS IT IS WRITTEN, THROUGH THE WHOLE CHAIN, IN A REAL
// BROWSER. A scripted OpenAI-compatible provider streams its
// first words and is then held; the identity's turn runs in the real app
// behind the real TLS dashboard, and a real Chrome page is open on the
// conversation. Three turns, sent from the operator's other window:
//   - complete: the first words are on the page before the provider is
//     released; released, the reply is recorded and drawn once, and the
//     draft is gone;
//   - the provider fails after its first words: they stay struck, with the
//     provider named as the cause;
//   - stopped after its first words: they stay struck, with the stop named.
//
// Coverage limit, named: the chain is driven in Blink only (the CDP helper
// the journey tests use); the page's own draft cases run in three engines
// (chat_draft_browser_test.go). The provider is scripted: no model is run.
func TestAReplyIsShownAsItIsWrittenInARealBrowser(t *testing.T) {
	chrome := needChrome(t)
	t.Log("coverage limit, named: the whole chain in Blink only; the page's draft cases run in three engines")
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Streaming")
	cfg := safebootConfig(t, dir, "Streaming", keyPath, ledgerPath, dbPath)
	cfg.Dashboard.Port = 0
	a := New(cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatalf("startLive: %v", err)
	}
	t.Cleanup(func() { stopApp(t, a) })

	type turn struct {
		probe, ending string // ending: complete | fail | stop
		first         chan struct{}
		release       chan struct{}
	}
	turns := make(chan *turn, 3)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var tn *turn
		select {
		case tn = <-turns:
			if !strings.Contains(string(body), tn.probe) {
				turns <- tn // another facility's request: answered simply below
				tn = nil
			}
		default:
		}
		if tn == nil {
			if strings.Contains(string(body), `"stream":true`) {
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, sseChunk("ok")+sseFinish)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, textReply("ok"))
			return
		}
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, sseChunk("The first words"))
		flusher.Flush()
		close(tn.first)
		select {
		case <-tn.release:
		case <-r.Context().Done():
			return
		}
		if tn.ending == "fail" {
			io.WriteString(w, `data: {"error":{"message":"the upstream closed"}}`+"\n\n")
			return
		}
		io.WriteString(w, sseChunk(" and the rest.")+sseFinish)
	}))
	defer provider.Close()
	a.llmSwap.Swap(llm.New(&llm.ClientConfig{Endpoint: provider.URL, Model: "fake", MaxOutputTokens: 4096, Retries: -1}))

	origin := a.dashboard.Origin()
	page := openInChrome(t, chrome, origin, "/#/chat")
	op := dialWSS(t, origin)
	defer op.Close(websocket.StatusNormalClosure, "")
	await := func(what, predicate string) string {
		t.Helper()
		v, err := page.evalRetry(awaitJS(predicate, 15000), true, 20*time.Second, 3)
		if err != nil || strings.HasPrefix(v, "TIMEOUT") {
			t.Fatalf("%s: %v %.600s", what, err, v)
		}
		return v
	}
	run := func(probe, ending string) *turn {
		t.Helper()
		// Each message opens its own turn. One sent while the last turn is still
		// finishing is steered, and a leftover's turn shows no draft: that is not
		// what this test states.
		for begun := time.Now(); a.TurnActive(); time.Sleep(10 * time.Millisecond) {
			if time.Since(begun) > 20*time.Second {
				t.Fatalf("%s: the turn before it did not end", probe)
			}
		}
		tn := &turn{probe: probe, ending: ending, first: make(chan struct{}), release: make(chan struct{})}
		turns <- tn
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := wsjsonWrite(ctx, op, map[string]any{"type": "chat", "message": probe}); err != nil {
			t.Fatal(err)
		}
		select {
		case <-tn.first:
		case <-time.After(30 * time.Second):
			t.Fatalf("%s: the provider was never asked for the turn's reply", probe)
		}
		return tn
	}

	tn := run("streaming probe one", "complete")
	await("the first words on the page before the provider is released",
		`const d = document.querySelector('[data-reply-draft] .body'); last = d ? d.textContent : 'no draft'; return d && d.textContent.includes('The first words') ? 'shown' : null;`)
	close(tn.release)
	await("the reply recorded and drawn once, the draft gone", `
    const bodies = [...document.querySelectorAll('.record-message .body')].filter(b => b.textContent.trim() === 'The first words and the rest.');
    last = 'drawn ' + bodies.length + ', draft ' + !!document.querySelector('[data-reply-draft]');
    return bodies.length === 1 && !document.querySelector('[data-reply-draft]') ? 'settled' : null;`)

	tn = run("streaming probe two", "fail")
	await("the second turn's first words shown", `const d = document.querySelector('[data-reply-draft] .body'); last = d ? d.textContent : 'no draft'; return d && d.textContent.includes('The first words') ? 'shown' : null;`)
	close(tn.release)
	await("the provider's failure withdraws the words, naming it", `
    const d = document.querySelector('[data-reply-draft]');
    const del = d && d.querySelector('del'), why = d && d.querySelector('.reply-draft__why');
    last = d ? d.outerHTML.slice(0, 400) : 'no draft';
    return del && del.textContent === 'The first words' && why && why.textContent === 'These words were not kept: the provider stopped.' ? 'withdrawn' : null;`)

	tn = run("streaming probe three", "stop")
	await("the third turn's first words shown", `const d = document.querySelector('[data-reply-draft] .body'); last = d ? d.textContent : 'no draft'; return d && d.textContent.includes('The first words') && !d.querySelector('del') ? 'shown' : null;`)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := wsjsonWrite(ctx, op, map[string]any{"type": "cancel"}); err != nil {
		t.Fatal(err)
	}
	await("the stop withdraws the words, naming it", `
    const d = document.querySelector('[data-reply-draft]');
    const why = d && d.querySelector('.reply-draft__why');
    last = d ? d.outerHTML.slice(0, 400) : 'no draft';
    return why && why.textContent === 'These words were not kept: you stopped the reply.' ? 'withdrawn' : null;`)
	close(tn.release)
}

func sseChunk(text string) string {
	return fmt.Sprintf(`data: {"id":"c1","object":"chat.completion.chunk","model":"fake","choices":[{"index":0,"delta":{"role":"assistant","content":%q}}]}`, text) + "\n\n"
}

const sseFinish = `data: {"id":"c1","object":"chat.completion.chunk","model":"fake","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":4,"total_tokens":9}}` + "\n\n" + "data: [DONE]\n\n"
