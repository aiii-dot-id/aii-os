//go:build (linux && !android) || (darwin && !ios) || windows

package pluginhost

import (
	"os"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
)

func TestASessionCheckRunsAgainstTheKitsVoiceEngine(t *testing.T) {
	bin := os.Getenv("AII_SDK_VOICE")
	if bin == "" {
		t.Skip("set AII_SDK_VOICE to the kit's voice-skel built at the pinned commit (the sdkcompat stage does)")
	}
	skipWhereNativeIsRefused(t)
	skipWhereTheSandboxCannotBeEstablished(t)
	engine, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	roots, role := sharedPlatformRoots(t)
	controls := []string{"speech.session.open", "speech.session.synthesize", "speech.session.cancel_synthesis",
		"speech.session.stop_playback", "speech.session.finish_input", "speech.session.close", "speech.session.status"}
	run := func(t *testing.T, id, validation string) (*ActivePlugin, pluginfacility.Checks) {
		pkg := nativePackageAs(t, id, engine, role, map[string][]byte{
			packagefmt.ValidationFile: []byte(validation),
			"checks/tone.wav":         checkRecording(),
		}, map[string]any{"plugin_family": SessionLaneFamily},
			&packagetest.InterfaceSpec{ID: SessionInterfaceID, Version: 1, SchemaFile: "interfaces/speech.session.v1.schema.json", Methods: controls})
		ap, _, checks := standCandidate(t, pkg, supervisedOpts(Options{Roots: roots}))
		return ap, checks
	}
	t.Run("its words and its tone", func(t *testing.T) {
		ap, checks := run(t, "org.example.voice-skel", checksOf(
			hearsCheck("hears its tail", 10000, "the tail, finalized"),
			`{"name":"speaks a sentence","sets":["*"],"lane":"session","input":{"text":"The local service is ready."},`+
				`"expect":{"events":{"order":["session_ready","synthesis_start","synthesis_end"],"audio_at_least":{"ms":100,"rms_dbfs":-30}}},"within_ms":10000}`))
		if checks.Ran != 2 || checks.Failed != nil {
			t.Fatalf("the kit's engine passes both: %+v %+v", checks, checks.Failed)
		}
		nothingObservedBy(t, ap)
		if ap.Pinned() || ap.Voice.Load().IsOpen() {
			t.Fatal("no check's session is left open or pinning")
		}
	})
	t.Run("words it never says", func(t *testing.T) {
		_, checks := run(t, "org.example.voice-skel-wrong", checksOf(hearsCheck("hears cobalt", 10000, "cobalt lantern")))
		if f := checks.Failed; f == nil || f.Outcome != pluginfacility.CheckWrong || !strings.Contains(f.Words, `its transcript lacks "cobalt lantern": "the tail finalized"`) {
			t.Fatalf("a phrase the engine never says is wrong, saying what it said: %+v", f)
		}
	})
}
