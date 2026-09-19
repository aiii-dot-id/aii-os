package pluginhost

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
)

// .
// .
// .
// .
func TestEngineFormatsRequiresBothConfirmed(t *testing.T) {
	j := func(s string) json.RawMessage { return json.RawMessage(s) }
	cases := []struct {
		name    string
		result  json.RawMessage
		wantErr bool
		in, out audio.Format
	}{
		{"empty admission", nil, true, audio.Format{}, audio.Format{}},
		{"no audio block", j(`{"accepted":true}`), true, audio.Format{}, audio.Format{}},
		{"unreadable", j(`{not json`), true, audio.Format{}, audio.Format{}},
		{"input only", j(`{"audio":{"input":{"rate":16000,"channels":1}}}`), true, audio.Format{}, audio.Format{}},
		{"output only", j(`{"audio":{"output":{"rate":24000,"channels":1}}}`), true, audio.Format{}, audio.Format{}},
		{"rate not audio", j(`{"audio":{"input":{"rate":100,"channels":1},"output":{"rate":24000,"channels":1}}}`), true, audio.Format{}, audio.Format{}},
		{"both confirmed", j(`{"audio":{"input":{"rate":16000,"channels":1},"output":{"rate":24000,"channels":1}}}`), false, audio.Format{Rate: 16000, Channels: 1}, audio.Format{Rate: 24000, Channels: 1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in, out, err := engineFormats(c.result, true)
			if (err != nil) != c.wantErr {
				t.Fatalf("engineFormats err=%v wantErr=%v", err, c.wantErr)
			}
			if !c.wantErr && (in != c.in || out != c.out) {
				t.Fatalf("confirmed formats in=%s out=%s, want in=%s out=%s", in, out, c.in, c.out)
			}
		})
	}
}

// .
// .
// .
// .
// .
// .
// .
func TestOpenWithAudioFaultsAndRetainsCustodyOnBadFormats(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  string
	}{
		{"admitted without naming formats", "FAKE_NO_FORMATS=1"},
		{"a format that is not audio", "FAKE_ENGINE_RATE=100"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ap := audioVoicePlugin(t, containedFakechild(t), tc.env, "FAKE_NO_TERMINAL=1")
			ctx := context.Background()
			plane := audio.NewPlane()
			src, err := audio.NewFileSource(bytes.NewReader(pcmRamp(100)), mono16k, 320)
			if err != nil {
				t.Fatal(err)
			}
			_ = plane.Register(&audio.Endpoint{ID: "mic", Source: src})
			_ = plane.Register(&audio.Endpoint{ID: "spk", Sink: audio.NewCaptureSink(mono16k)})
			b, err := plane.Bind("s1", "mic", "spk", true)
			if err != nil {
				t.Fatal(err)
			}
			if err := ap.Voice.OpenWithAudio(ctx, "s1", b, nil); err == nil {
				t.Fatal("unacceptable formats must be rejected, not pumped at a guessed rate")
			}
			if _, p := ap.Voice.Audio(); p != nil {
				t.Fatal("audio stays blocked: no pump on a rejected format")
			}
			// .
			// .
			if !ap.Voice.Faulted() {
				t.Fatal("a format rejection must record a host-side fault")
			}
			if !ap.Pinned() {
				t.Fatal("the activation pin is retained — the engine has not closed the session")
			}
			if fired(b.Released(), 500*time.Millisecond) {
				t.Fatal("the endpoints are not released on the rejection alone — only on a terminal word or a verified reap")
			}
		})
	}
}
