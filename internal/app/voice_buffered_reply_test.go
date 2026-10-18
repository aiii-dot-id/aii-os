package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// The SDK engine has finished generating, but the page has rendered only
// part of the reply. An empty-id interruption cannot fence that generation.
// Exercise the real driver, audio pump, reference engine and receipt path;
// the speech_start case injects the observer's input event, not microphone
// recognition (the reference engine has no recognizer).
func TestBufferedReplyStopsAndDrainsWithTheSDKEngine(t *testing.T) {
	engine := sdkVoiceEngine(t)
	for _, action := range []string{"stop", "speech-start", "spoken-replacement", "typed-replacement"} {
		t.Run(action, func(t *testing.T) {
			a, v := bufferedReplyApp(t, engine)
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			ended := make(chan uint32, 8)
			a.voiceEventSink = func(ev dashboard.VoiceEvent) {
				if ev.Type == "synthesis_end" {
					ended <- ev.Stream
				}
			}
			plane := a.AudioPlane()
			if err := plane.Register(&audio.Endpoint{ID: "speaker", Sink: audio.NewCaptureSink(mono16k)}); err != nil {
				t.Fatal(err)
			}
			opened, err := a.OpenVoiceSession(ctx, "", "speaker", voiceOutputMode, nil)
			if err != nil {
				t.Fatal(err)
			}
			h := opened.(*voiceHandle)
			_, pump := v.Audio()
			produce := func(text string) {
				t.Helper()
				if got := a.synthesizeReply(ctx, h.id, h.gen.Load(), text); got != replyAdmitted {
					t.Fatalf("reply admission = %v", got)
				}
			}
			productionEnded := func() dashboard.PlaybackReport {
				t.Helper()
				var stream uint32
				select {
				case stream = <-ended:
				case <-ctx.Done():
					t.Fatal("SDK engine did not finish producing the reply")
				}
				var st audio.OutputStream
				voiceWait(t, "the completed reply reaches the speaker", 10*time.Second, func() bool {
					st, _ = pump.OutputStream(stream)
					return st.Ended && st.Written > 1
				})
				return dashboard.PlaybackReport{SessionID: h.id, Stream: stream, Rendered: st.Written,
					Rate: mono16k.Rate, Channels: mono16k.Channels, Terminal: true, Outcome: "drained"}
			}
			produce("The page still holds the unplayed tail of this reply.")
			prior := productionEnded()
			prior.Rendered /= 2
			prior.Outcome = "stopped"
			if err := h.PlaybackReport(ctx, prior); !pluginhost.IsSessionRefused(err) {
				t.Fatalf("unfenced partial playback was not refused by the engine: %v", err)
			}
			switch action {
			case "stop":
				if err := h.Interrupt(ctx); err != nil {
					t.Fatal(err)
				}
			case "speech-start":
				a.voiceObserved(decodeVoiceFrame(pluginhost.Event{Type: "speech_start", SessionID: h.id}))
				a.bgWG.Wait() // the existing owner joins the fence, never a timing guess
			case "spoken-replacement":
				produce("A new spoken-turn reply replaces the buffered tail.")
			case "typed-replacement":
				a.settleVoice(ctx, "A new typed-turn reply replaces the buffered tail.")
			}
			if err := h.PlaybackReport(ctx, prior); err != nil {
				t.Fatalf("stopped receipt for a production-ended reply was refused: %v", err)
			}
			if action == "stop" || action == "speech-start" {
				produce("A new reply still works after the stop.")
			}
			if err := h.PlaybackReport(ctx, productionEnded()); err != nil {
				t.Fatalf("the following reply did not drain: %v", err)
			}
			if err := h.Close(ctx, "drain"); err != nil {
				t.Fatal(err)
			}
			select {
			case <-h.Done():
			case <-ctx.Done():
				t.Fatal("an unretired predecessor stranded the session's drain")
			}
			select {
			case <-h.Released():
			case <-ctx.Done():
				t.Fatal("the drained session kept its speaker")
			}
		})
	}
}

func bufferedReplyApp(t *testing.T, engine []byte) (*App, *pluginhost.VoiceSession) {
	t.Helper()
	dir := t.TempDir()
	root, status, err := packagetest.NewPlatformRelease("aiii_test_platform_release")
	if err != nil {
		t.Fatal(err)
	}
	roots := packagefmt.TrustRoots{PlatformRelease: root.Env}
	if err := os.WriteFile(filepath.Join(dir, packagetest.StatusFilePlatform), status, 0600); err != nil {
		t.Fatal(err)
	}
	roots.Revocation = packagefmt.LoadRevocationStatus(dir, roots, nil)
	pkg := voicePackage(t, dir, "com.example.buffered-voice", "0.1.0", engine, root)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry(dir, nil, tools.Timeouts{})
	ap, err := pluginhost.Activate(t.Context(), pkg, reg, &pluginhost.Options{Roots: roots, WorkerBinary: exe, PluginDataDir: filepath.Join(dir, "private")})
	if err != nil {
		t.Fatal(err)
	}
	a := New(&Config{})
	setPlugins(a, []*pluginhost.ActivePlugin{ap})
	t.Cleanup(func() {
		a.bgCancel()
		a.bgWG.Wait()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := ap.Deactivate(ctx); err != nil {
			t.Errorf("retire the test engine: %v", err)
		}
		voiceWait(t, "the application's observer exits", 5*time.Second, func() bool {
			a.voiceObsMu.Lock()
			defer a.voiceObsMu.Unlock()
			return len(a.voiceObs) == 0
		})
	})
	return a, ap.Voice.Load()
}
