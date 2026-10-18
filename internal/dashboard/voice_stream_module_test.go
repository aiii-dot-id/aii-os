package dashboard

import (
	"regexp"
	"strconv"
	"testing"
)

// The page's half of the streaming frame lives in static/voice-stream.js:
// the codec, the lane and the pre-roll, carved out of voice.js as it was.
// Two sides write and read the same 24-byte header, so the page's version
// and header size are the host's (voice_stream.go). A page that sent a
// version the host does not route would have its microphone frames
// dropped at the socket with nothing said on either side.
//
// The module is also self-contained: it imports nothing, so a lane is
// whatever its constructor was given (sendFrame, sendJSON) and carries no
// state of voice.js's.
func TestVoiceStreamModuleSpeaksTheHostsFrame(t *testing.T) {
	src, err := staticFS.ReadFile("static/voice-stream.js")
	if err != nil {
		t.Fatal(err)
	}
	constant := func(name string) int {
		m := regexp.MustCompile(`(?m)^export const ` + name + ` = (\d+);`).FindSubmatch(src)
		if m == nil {
			t.Fatalf("voice-stream.js does not declare %s as a number", name)
		}
		n, _ := strconv.Atoi(string(m[1]))
		return n
	}
	if got := constant("STREAM_VERSION"); got != voiceStreamVersion {
		t.Errorf("the page streams version %d, the host routes version %d", got, voiceStreamVersion)
	}
	if got := constant("STREAM_HEADER"); got != voiceStreamHeaderBytes {
		t.Errorf("the page's stream header is %d bytes, the host's is %d", got, voiceStreamHeaderBytes)
	}
	if regexp.MustCompile(`(?m)^\s*import\b`).Match(src) {
		t.Error("voice-stream.js imports a module: the codec, the lane and the pre-roll take what they need from their callers")
	}
	if regexp.MustCompile(`(?m)^(let|var)\s`).Match(src) {
		t.Error("voice-stream.js holds module state: a lane carries only what its constructor was given")
	}
}
