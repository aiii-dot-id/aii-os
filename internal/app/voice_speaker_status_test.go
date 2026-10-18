package app

import (
	"context"
	"testing"
)

func TestTheStatusNamesTheEngineStillSpeaking(t *testing.T) {
	ctx := context.Background()
	a := speechRouteApp(t, "", "id.engine.first")
	runningEngine(t, a, "id.engine.first")
	second, _ := runningEngine(t, a, "id.engine.second")
	opensReach(t)

	if got := a.voiceSpeakerCarried(); got != "" {
		t.Fatalf("with no session open the status names %q", got)
	}
	open, err := openSpeaking(a, "spk-2")
	if err != nil {
		t.Fatal(err)
	}
	if got := a.voiceSpeakerCarried(); got != "" {
		t.Fatalf("the open session is the choice's own, and the status names %q", got)
	}
	chooseSpeech(a, "", second.ID)
	if got := a.voiceSpeakerCarried(); got != "id.engine.first" {
		t.Fatalf("after another engine was chosen the status names %q, want the engine still speaking, id.engine.first", got)
	}
	chooseSpeech(a, "", "")
	if got := a.voiceSpeakerCarried(); got != "" {
		t.Fatalf("the default is the engine already speaking, and the status names %q", got)
	}
	chooseSpeech(a, "", "ElevenLabs")
	if got := a.voiceSpeakerCarried(); got != "" {
		t.Fatalf("a service speaks at once, and the status names %q", got)
	}
	chooseSpeech(a, "", second.ID)
	open.v = &fakeEngineSession{}
	if err := open.Close(ctx, "abort"); err != nil {
		t.Fatal(err)
	}
	if got := a.voiceSpeakerCarried(); got != "" {
		t.Fatalf("the session retired, and the status still names %q", got)
	}
}
