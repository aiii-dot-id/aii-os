package supervisor

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// A native T3 VOICE plugin, built by the SDK, run by the real
// supervisor, reporting a real utterance upward. The whole path.
//
//	cd ../aii-plugin-sdk && go build -o /tmp/voice-skel ./examples/voice-skel/
//	AII_SDK_VOICE=/tmp/voice-skel go test -run TestSDKVoicePlugin ./internal/supervisor/
type voiceDispatcher struct {
	heard   []string
	speaker string
}

func (d *voiceDispatcher) Dispatch(ctx context.Context, method string, params []byte) ([]byte, error) {
	var p struct {
		Operation string `json:"operation"`
		Arguments struct {
			Text    string `json:"text"`
			Speaker string `json:"speaker"`
		} `json:"arguments"`
	}
	_ = json.Unmarshal(params, &p)
	if p.Operation == "voice.observe" {
		d.heard = append(d.heard, p.Arguments.Text)
		d.speaker = p.Arguments.Speaker
	}
	return []byte(`{"status":"succeeded","value":{"recorded":true}}`), nil
}

func TestSDKVoicePluginReportsUpward(t *testing.T) {
	bin := os.Getenv("AII_SDK_VOICE")
	if bin == "" {
		t.Skip("set AII_SDK_VOICE to an SDK-built voice plugin (see the comment above)")
	}
	_, lg := newCapture()
	d := &voiceDispatcher{}
	s, err := Start(Spec{
		PluginID: "com.example.voice-skel", Argv: []string{bin},
		Env: []string{"SEV_PLUGIN_SOCKET=stdio:"}, ReadyMark: "child-ready",
		Backoff: Backoff{Initial: 20 * time.Millisecond, Max: 100 * time.Millisecond, MaxRestarts: 1},
		Log:     lg,
	}, d)
	if err != nil {
		t.Fatalf("the supervisor could not start the voice plugin: %v", err)
	}
	defer s.Close()

	if _, err := s.Invoke(context.Background(),
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"invoke.call","params":{"operation":"describe","arguments":{}}}`)); err != nil {
		t.Fatalf("describe failed: %v", err)
	}
	if _, err := s.Invoke(context.Background(),
		[]byte(`{"jsonrpc":"2.0","id":2,"method":"invoke.call","params":{"operation":"listen","arguments":{"on":true}}}`)); err != nil {
		t.Fatalf("listen failed: %v", err)
	}

	if len(d.heard) != 1 {
		t.Fatalf("the host heard %d utterances, want 1", len(d.heard))
	}
	if !strings.Contains(d.heard[0], "ledger and the outbox") {
		t.Fatalf("the utterance did not arrive intact: %q", d.heard[0])
	}
	if d.speaker != "speaker-1" {
		t.Fatalf("the speaker label did not travel: %q", d.speaker)
	}
	t.Logf("VOICE E2E: heard %q from %q, upward through the broker seam", d.heard[0], d.speaker)
}
