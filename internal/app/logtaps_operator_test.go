package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// capturesHolding counts the files under dir whose bytes hold s.
func capturesHolding(t *testing.T, dir, s string) int {
	t.Helper()
	n := 0
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		if err == nil && bytes.Contains(b, []byte(s)) {
			n++
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// THE IDENTITY STARTS A CAPTURE OF WHAT ITS MODEL IS SENT, FROM ITS OWN FILE.
// Written into the log
// control file as the identity's own file tools write it, a prompt tap starts,
// the broad one and a facility's, with an expiry or without, and nothing
// refuses it. A model call made then is written down, its system text as its
// size and SHA-256 and never as itself. The same file stops it, and a call
// made after is not written. The command writes one too, and the operator's
// config.json still starts one, which the file still stops.
func TestTheIdentityStartsAndStopsAPromptCaptureFromItsOwnFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"c","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	logsink.SetCaptureDir(dir)
	t.Cleanup(func() { logsink.CloseCaptures(); logsink.SetCaptureDir(""); logsink.SetTaps(nil) })
	logsink.SetTaps(nil)
	buf := logsink.CaptureForTest(t)
	control := LogControlPathIn(t.TempDir())
	cfg := Config{Logs: LogsConfig{Dir: "log"}}

	const system = "SYSTEM-SENTINEL-R266 the identity's charter, which is never on disk"
	sum := sha256.Sum256([]byte(system))
	inPlace := "[system text: " + strconv.Itoa(len(system)) + " bytes, sha256 " + hex.EncodeToString(sum[:]) + "]"
	client := llm.New(&llm.ClientConfig{Endpoint: srv.URL, Model: "a-model", APIKey: "k", NoStream: true})
	call := func(words string) {
		t.Helper()
		if _, err := client.Chat(context.Background(), []llm.Message{{Role: "system", Content: system}, {Role: "user", Content: words}}, llm.ChatOptions{}); err != nil {
			t.Fatal(err)
		}
		logsink.CloseCaptures()
	}

	call("CALL-BEFORE-ANY-TAP")
	if n := capturesHolding(t, dir, "CALL-BEFORE-ANY-TAP"); n != 0 {
		t.Fatalf("a call was written down with no tap asked for (%d files)", n)
	}

	later := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	if err := WriteLogControl(control, LogControl{Tap: map[string]string{"llm.prompt": "", "dream.prompt": later}}); err != nil {
		t.Fatal(err)
	}
	applyLogTaps(cfg, control)
	if !logsink.TapEnabled("llm.prompt") || !logsink.TapEnabled("dream.prompt") {
		t.Fatalf("the identity's own file did not start its prompt taps: llm.prompt %v, dream.prompt %v", logsink.TapEnabled("llm.prompt"), logsink.TapEnabled("dream.prompt"))
	}
	call("CALL-WHILE-RECORDING")
	if n := capturesHolding(t, dir, "CALL-WHILE-RECORDING"); n == 0 {
		t.Fatal("a call made while the identity's tap was on was not written down")
	}
	for _, cut := range []string{system, "SYSTEM-SENTINEL-R266", "which is never on disk"} {
		if n := capturesHolding(t, dir, cut); n != 0 {
			t.Fatalf("the system text %q is on disk in %d file(s)", cut, n)
		}
	}
	if n := capturesHolding(t, dir, inPlace); n == 0 {
		t.Fatalf("no capture holds the system text's size and hash, %q", inPlace)
	}
	if strings.Contains(buf.String(), "logs.refusal") || strings.Contains(buf.String(), "refused") {
		t.Errorf("starting a prompt tap from the identity's file was refused:\n%s", buf.String())
	}
	ctl, err := ReadLogControl(control)
	if err != nil {
		t.Fatal(err)
	}
	if got := DescribeLogging(cfg, ctl, control); !strings.Contains(got, "tap: llm.prompt — RECORDING, no expiry") || strings.Contains(got, "REFUSED") {
		t.Errorf("the readback does not say the identity's prompt tap is recording:\n%s", got)
	}
	if err := SetLogTap(control, "llm.prompt", later); err != nil {
		t.Errorf("the command does not write a prompt tap: %v", err)
	}

	if err := SetLogTap(control, "llm.prompt", TapOff); err != nil {
		t.Fatal(err)
	}
	applyLogTaps(cfg, control)
	if logsink.TapEnabled("llm.prompt") {
		t.Fatal("the identity's own file did not stop its prompt tap")
	}
	call("CALL-AFTER-THE-STOP")
	if n := capturesHolding(t, dir, "CALL-AFTER-THE-STOP"); n != 0 {
		t.Fatalf("a call made after the identity stopped its tap was written down (%d files)", n)
	}

	// The operator's durable list still starts one, and the file still stops it.
	cfg.Logs.Taps = map[string]TapSetting{"llm.prompt": {}}
	applyLogTaps(cfg, control)
	if logsink.TapEnabled("llm.prompt") {
		t.Error("the identity's stop no longer stands over the operator's config")
	}
	if err := SetLogTap(control, "llm.prompt", TapClear); err != nil {
		t.Fatal(err)
	}
	applyLogTaps(cfg, control)
	if !logsink.TapEnabled("llm.prompt") {
		t.Error("the operator's config.json no longer starts a prompt tap")
	}
}

// A FOUNDING PROMPT SENT AGAIN AS THE USER'S IS STILL CAPTURED ONLY AS ITS SIZE
// AND HASH. An OpenAI-shaped
// provider refuses the founding prompt as the only system message (HTTP 400);
// birth sends the same bytes once more as the only user message. The
// wire carries the words; with a prompt tap on, no capture holds them, and the
// capture of the retry holds their size and SHA-256. The founding string here is
// invented, the provider an httptest server.
func TestAFoundingRetryIsCapturedOnlyAsItsSizeAndHash(t *testing.T) {
	p := newFoundingProvider(t, "openai", refusesSystemOnly(http.StatusBadRequest))
	const founding = "FOUNDING-SENTINEL-7d1 an invented founding prompt, which is never on disk"
	p.app.bootstrapText = founding
	sum := sha256.Sum256([]byte(founding))
	inPlace := "[system text: " + strconv.Itoa(len(founding)) + " bytes, sha256 " + hex.EncodeToString(sum[:]) + "]"
	dir := t.TempDir()
	logsink.SetCaptureDir(dir)
	logsink.SetTaps(map[string]time.Time{"llm.prompt": {}})
	t.Cleanup(func() { logsink.CloseCaptures(); logsink.SetCaptureDir(""); logsink.SetTaps(nil) })
	greeting, err := p.app.handleGenesis(t.Context(), p.birth)
	if err != nil || greeting != "Hello." {
		t.Fatalf("birth on a provider that refuses the system message alone: %q %v", greeting, err)
	}
	logsink.CloseCaptures()
	last := p.requests[len(p.requests)-1]
	if len(last) != 1 || last[0].Role != "user" || last[0].Content != founding {
		t.Fatalf("the retry did not carry the founding words as the only user message: %+v", last)
	}
	for _, cut := range []string{founding, "FOUNDING-SENTINEL", "which is never on disk"} {
		if n := capturesHolding(t, dir, cut); n != 0 {
			t.Fatalf("the founding prompt %q is on disk in %d capture(s)", cut, n)
		}
	}
	if n := capturesHolding(t, dir, inPlace); n == 0 {
		t.Fatalf("no capture holds the founding prompt's size and hash, %q", inPlace)
	}
}
