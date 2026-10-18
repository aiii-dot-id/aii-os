package llm

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

	"github.com/aiii-dot-id/aii-os/internal/llm/wire"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// ring0Sentinel stands for Ring 0 in a system text: no byte run of it may
// reach a capture.
const ring0Sentinel = "RING-ZERO-SENTINEL-7f3a9c the constitution, fetched live and never stored"

// filesHolding is every file under dir whose bytes hold s.
func filesHolding(t *testing.T, dir, s string) []string {
	t.Helper()
	var found []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(b, []byte(s)) {
			found = append(found, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// A PROMPT TAP NEVER WRITES THE SYSTEM TEXT, IN ANY DIALECT. With every
// prompt tap on and captures written into a directory, a call in each
// dialect — the turn's Chat and a facility's ChatSimple with its cache seam,
// which splits the system text into blocks — writes a capture that holds the
// rest of the request and, where each piece of system text was, its length
// and SHA-256; no file under the directory holds a byte run of the sentinel
// that stands for Ring 0. A system-only turn the Responses dialect sends as a
// user item is left out the same way.
func TestAPromptTapNeverWritesTheSystemText(t *testing.T) {
	answers := map[string]string{
		"":          `{"id":"c","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`,
		"anthropic": `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`,
		"gemini":    `{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1}}`,
		"chatgpt":   streamOneItem + "\n" + streamTerminal,
	}
	system := ring0Sentinel + " / then the identity's charter and working truth"
	for provider, answer := range answers {
		t.Run("provider "+provider, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body)
				if provider != "chatgpt" {
					w.Header().Set("Content-Type", "application/json")
				}
				io.WriteString(w, answer)
			}))
			defer srv.Close()
			dir := t.TempDir()
			logsink.SetCaptureDir(dir)
			logsink.SetTaps(map[string]time.Time{"llm.prompt": {}, "llm.return": {}})
			t.Cleanup(func() { logsink.CloseCaptures(); logsink.SetCaptureDir(""); logsink.SetTaps(nil) })
			c := New(&ClientConfig{Endpoint: srv.URL, Model: "a-model", Provider: provider, APIKey: "k", NoStream: true})
			ctx := context.Background()
			if _, err := c.Chat(ctx, []Message{{Role: "system", Content: system, StableLen: len(ring0Sentinel)}, {Role: "user", Content: "THE-QUESTION"}}, ChatOptions{}); err != nil {
				t.Fatal(err)
			}
			if _, _, _, err := c.ChatSimple(wire.WithStablePrefix(ctx, len(ring0Sentinel)), system, "A-FACILITY-INPUT"); err != nil {
				t.Fatal(err)
			}
			if provider == "chatgpt" {
				// A system-only turn: the dialect sends the text as a user item.
				if _, err := c.Chat(ctx, []Message{{Role: "system", Content: system}}, ChatOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			logsink.CloseCaptures()
			for _, cut := range []string{ring0Sentinel, "RING-ZERO-SENTINEL", "fetched live and never stored", "charter and working truth"} {
				if found := filesHolding(t, dir, cut); len(found) > 0 {
					t.Fatalf("%q is on disk in %v", cut, found)
				}
			}
			if len(filesHolding(t, dir, "THE-QUESTION")) == 0 || len(filesHolding(t, dir, "A-FACILITY-INPUT")) == 0 {
				t.Fatal("the captures do not hold the rest of the requests")
			}
			if len(filesHolding(t, dir, "[system text: ")) == 0 {
				t.Fatal("no capture says where the system text was")
			}
		})
	}
}

// THE LINE IN PLACE OF SYSTEM TEXT SAYS ITS SIZE AND ITS SHA-256, in every
// shape a dialect sends, and nothing else of the body moves.
func TestTheSystemTextIsReplacedInEveryShape(t *testing.T) {
	line := func(s string) string {
		sum := sha256.Sum256([]byte(s))
		return `"[system text: ` + strconv.Itoa(len(s)) + ` bytes, sha256 ` + hex.EncodeToString(sum[:]) + `]"`
	}
	for body, want := range map[string]string{
		`{"system":[{"type":"text","text":"S1","cache_control":{"type":"ephemeral"}},{"type":"text","text":"S2"}],"messages":[{"role":"user","content":"Q 30.0"}]}`: `{"system":[{"type":"text","text":` + line("S1") + `,"cache_control":{"type":"ephemeral"}},{"type":"text","text":` + line("S2") + `}],"messages":[{"role":"user","content":"Q 30.0"}]}`,
		`{"system":"S"}`: `{"system":` + line("S") + `}`,
		`{"instructions":"I","input":[{"role":"user","content":[{"type":"input_text","text":"Q"}]}]}`:                                               `{"instructions":` + line("I") + `,"input":[{"role":"user","content":[{"type":"input_text","text":"Q"}]}]}`,
		`{"systemInstruction":{"parts":[{"text":"G"}]},"contents":[]}`:                                                                              `{"systemInstruction":{"parts":[{"text":` + line("G") + `}]},"contents":[]}`,
		`{"messages":[{"content":"S<","role":"system"},{"role":"developer","content":[{"type":"text","text":"D"}]},{"role":"user","content":"Q"}]}`: `{"messages":[{"content":` + line("S<") + `,"role":"system"},{"role":"developer","content":[{"type":"text","text":` + line("D") + `}]},{"role":"user","content":"Q"}]}`,
	} {
		if got := string(withoutSystemText(context.Background(), []byte(body))); got != want {
			t.Errorf("%s\n got %s\nwant %s", body, got, want)
		}
	}
	// What the caller sent as system text is left out wherever it went.
	ctx := withSystemTexts(context.Background(), []Message{{Role: "system", Content: "FOUNDING"}})
	body := `{"input":[{"role":"user","content":[{"type":"input_text","text":"FOUNDING"}]}],"model":"FOUNDING"}`
	want := `{"input":[{"role":"user","content":[{"type":"input_text","text":` + line("FOUNDING") + `}]}],"model":"FOUNDING"}`
	if got := string(withoutSystemText(ctx, []byte(body))); got != want {
		t.Errorf("the founding text sent as a user item:\n got %s\nwant %s", got, want)
	}
	if got := string(withoutSystemText(context.Background(), []byte("not json"))); !strings.HasPrefix(got, "[system text: 8 bytes") {
		t.Errorf("a body that is not JSON is written as %q", got)
	}
}
