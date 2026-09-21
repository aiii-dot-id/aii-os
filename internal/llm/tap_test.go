package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// .
// .

func capturedLines(t *testing.T, dir string) []map[string]any {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			if line == "" {
				continue
			}
			var m map[string]any
			if err := json.Unmarshal([]byte(line), &m); err != nil {
				t.Fatalf("a capture line is not JSON: %v\n%s", err, line)
			}
			out = append(out, m)
		}
	}
	return out
}

// .
// .
// .
func TestTheTapCapturesTheRawPromptAndTheRawReturn(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"cmpl-1","choices":[{"message":{"role":"assistant","content":"THE-ANSWER"},"finish_reason":"stop"}],"usage":{"total_tokens":7}}`)
	}))
	defer srv.Close()

	dir := t.TempDir()
	logsink.SetCaptureDir(dir)
	t.Cleanup(func() {
		logsink.CloseCaptures()
		logsink.SetCaptureDir("")
		logsink.SetTaps(nil)
	})

	c := New(&ClientConfig{Endpoint: srv.URL, Model: "a-model"})
	msgs := []Message{{Role: "user", Content: "THE-QUESTION"}}

	// .
	resp, err := c.Chat(context.Background(), msgs, ChatOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := capturedLines(t, dir); len(got) != 0 {
		t.Fatalf("an inactive tap captured %d line(s)", len(got))
	}
	if resp.CallID == "" {
		t.Error("the answer carries no correlating id — a handle that exists only while recording is no use for asking what happened")
	}

	// .
	logsink.SetTaps(map[string]time.Time{"llm.prompt": {}, "llm.return": {}})
	resp, err = c.Chat(context.Background(), msgs, ChatOptions{})
	if err != nil {
		t.Fatal(err)
	}
	lines := capturedLines(t, dir)
	if len(lines) != 2 {
		t.Fatalf("captured %d line(s), want the prompt and the return", len(lines))
	}
	byDir := map[string]map[string]any{}
	for _, l := range lines {
		byDir[l["direction"].(string)] = l
	}
	prompt, ok := byDir["prompt"]
	if !ok {
		t.Fatal("no prompt was captured")
	}
	ret, ok := byDir["return"]
	if !ok {
		t.Fatal("no return was captured")
	}
	if p := prompt["payload"].(string); !strings.Contains(p, "THE-QUESTION") || !strings.Contains(p, `"model":"a-model"`) {
		t.Errorf("the prompt capture is not the raw request body:\n%s", p)
	}
	if p := ret["payload"].(string); !strings.Contains(p, "THE-ANSWER") || !strings.Contains(p, `"finish_reason"`) {
		t.Errorf("the return capture is not the raw response body:\n%s", p)
	}
	if prompt["id"] != ret["id"] || prompt["id"] != resp.CallID {
		t.Errorf("the two halves of one exchange do not share an id: prompt=%v return=%v answer=%v",
			prompt["id"], ret["id"], resp.CallID)
	}

	// .
	// .
	for _, l := range lines {
		if strings.Contains(strings.ToLower(l["payload"].(string)), "authorization") {
			t.Errorf("a capture carries a header:\n%s", l["payload"])
		}
	}
}

// .
// .
// .
// .
// .
// .
func TestAnExchangeThatBeganRecordingFinishesEvenIfTheTapExpires(t *testing.T) {
	releaseBody := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.(http.Flusher).Flush()
		<-releaseBody
		io.WriteString(w, `{"id":"c1","choices":[{"message":{"role":"assistant","content":"LATE-ANSWER"},"finish_reason":"stop"}],"usage":{"total_tokens":3}}`)
	}))
	defer srv.Close()

	dir := t.TempDir()
	logsink.SetCaptureDir(dir)
	t.Cleanup(func() {
		logsink.CloseCaptures()
		logsink.SetCaptureDir("")
		logsink.SetTaps(nil)
	})

	logsink.SetTaps(map[string]time.Time{
		"llm.prompt": time.Now().Add(60 * time.Millisecond),
		"llm.return": time.Now().Add(60 * time.Millisecond),
	})
	c := New(&ClientConfig{Endpoint: srv.URL, Model: "a-model"})

	done := make(chan error, 1)
	go func() {
		_, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "Q"}}, ChatOptions{})
		done <- err
	}()

	// .
	time.Sleep(150 * time.Millisecond)
	if logsink.TapEnabled("llm.return") {
		t.Fatal("the fixture needs the tap to have expired before the answer lands")
	}
	close(releaseBody)
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	lines := capturedLines(t, dir)
	var prompt, ret bool
	for _, l := range lines {
		switch l["direction"] {
		case "prompt":
			prompt = true
		case "return":
			ret = true
			if p := l["payload"].(string); !strings.Contains(p, "LATE-ANSWER") {
				t.Errorf("the answer was captured without its content:\n%s", p)
			}
		}
	}
	if !prompt {
		t.Fatal("the fixture captured no prompt, so it proves nothing")
	}
	if !ret {
		t.Error("THE EXCHANGE WAS HALF CAPTURED: the tap expired mid-call and the answer was dropped")
	}
}

// .
func tapDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	logsink.SetCaptureDir(dir)
	t.Cleanup(func() {
		logsink.CloseCaptures()
		logsink.SetCaptureDir("")
		logsink.SetTaps(nil)
	})
	logsink.SetTaps(map[string]time.Time{"llm.prompt": {}, "llm.return": {}})
	return dir
}

// .
// .
// .
// .
func TestTheAnthropicDialectIsCapturedToo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"content":[{"type":"text","text":"ANTHROPIC-ANSWER"}],"stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":2}}`)
	}))
	defer srv.Close()
	dir := tapDir(t)

	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "a-model", Provider: "anthropic", MaxOutputTokens: 512})
	resp, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "ANTHROPIC-QUESTION"}}, ChatOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.CallID == "" {
		t.Error("an anthropic answer carries no correlating id")
	}
	var prompt, ret map[string]any
	for _, l := range capturedLines(t, dir) {
		switch l["direction"] {
		case "prompt":
			prompt = l
		case "return":
			ret = l
		}
	}
	if prompt == nil || !strings.Contains(prompt["payload"].(string), "ANTHROPIC-QUESTION") {
		t.Error("THE ANTHROPIC REQUEST WAS NOT CAPTURED")
	}
	if ret == nil || !strings.Contains(ret["payload"].(string), "ANTHROPIC-ANSWER") {
		t.Error("THE ANTHROPIC ANSWER WAS NOT CAPTURED")
	}
}

// .
// .
// .
// .
// .
func TestTheCapturedStreamRequestIsTheOneTransmitted(t *testing.T) {
	var wire string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		wire = string(b)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()
	dir := tapDir(t)

	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m"})
	if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(wire, `"stream":true`) {
		t.Skip("this build did not take the streamed path; nothing to compare")
	}
	for _, l := range capturedLines(t, dir) {
		if l["direction"] != "prompt" {
			continue
		}
		if p := l["payload"].(string); !strings.Contains(p, `"stream":true`) {
			t.Errorf("the capture is not the transmitted request — the wire carried stream:true and the capture does not:\n%s", p)
		}
		return
	}
	t.Error("no prompt was captured on the streamed path")
}

// .
// .
// .
// .
// .
func TestAnExpiryBeforeTheHeadersStillCapturesTheAnswer(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		<-release
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"AFTER-EXPIRY"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()

	dir := t.TempDir()
	logsink.SetCaptureDir(dir)
	t.Cleanup(func() {
		logsink.CloseCaptures()
		logsink.SetCaptureDir("")
		logsink.SetTaps(nil)
	})
	logsink.SetTaps(map[string]time.Time{
		"llm.prompt": time.Now().Add(60 * time.Millisecond),
		"llm.return": time.Now().Add(60 * time.Millisecond),
	})

	c := New(&ClientConfig{Endpoint: srv.URL, Model: "m"})
	done := make(chan error, 1)
	go func() {
		_, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "Q"}}, ChatOptions{})
		done <- err
	}()
	time.Sleep(150 * time.Millisecond)
	if logsink.TapEnabled("llm.return") {
		t.Fatal("the fixture needs the tap to lapse while the request is in flight")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	for _, l := range capturedLines(t, dir) {
		if l["direction"] == "return" && strings.Contains(l["payload"].(string), "AFTER-EXPIRY") {
			return
		}
	}
	t.Error("THE ANSWER WAS LOST: the tap lapsed before the headers and the exchange was left half captured")
}

// .
// .
// .
func TestALongAnswerIsBoundedWhileItIsCollected(t *testing.T) {
	const over = logsink.CaptureMax + (1 << 20)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"`)
		chunk := strings.Repeat("x", 1<<16)
		for n := 0; n < over; n += len(chunk) {
			io.WriteString(w, chunk)
		}
		io.WriteString(w, `"}, "finish_reason":"stop"}]}`)
	}))
	defer srv.Close()
	dir := tapDir(t)

	c := New(&ClientConfig{Endpoint: srv.URL, Model: "m"})
	if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "Q"}}, ChatOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, l := range capturedLines(t, dir) {
		if l["direction"] != "return" {
			continue
		}
		p := l["payload"].(string)
		if len(p) > logsink.CaptureMax+4096 {
			t.Errorf("the capture holds %d bytes, past the bound of %d", len(p), logsink.CaptureMax)
		}
		if !strings.Contains(p, "continued past the capture bound") {
			t.Errorf("a bounded capture does not say it was bounded, so a reader cannot tell a short answer from a cut one")
		}
		return
	}
	t.Error("no answer was captured")
}
