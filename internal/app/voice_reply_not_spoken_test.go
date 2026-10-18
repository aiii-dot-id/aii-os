package app

import (
	"context"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

func TestATypedReplyIsSpokenByTheOpenConversationWhenRepliesAreAskedForAloud(t *testing.T) {
	ctx := context.Background()
	mode := func(speak string) VoiceModeConfig {
		return VoiceModeConfig{Listen: listenInteractive, Speak: speak, Revision: 1}
	}
	t.Run("speak on: the conversation's engine speaks it", func(t *testing.T) {
		r := newHeardRoute(t, "", false)
		r.a.publishVoiceMode(mode(speakOn))
		r.a.settleVoice(ctx, "typed during the conversation")
		awaitAppWork(t, r.a)
		if got := r.spokeBy(); got != "id.engine.a×1" {
			t.Fatalf("a typed reply in an open conversation was spoken by %s", got)
		}
		refs := r.replyRefs()
		if len(refs) != 1 || refs[0].Route != "plugin" || refs[0].SessionID != r.hear.id || refs[0].SynthesisID == "" || refs[0].TextOnly {
			t.Fatalf("the typed reply did not reach the page as the conversation's own: %+v", refs)
		}
	})
	t.Run("speak on: silence is not a reply", func(t *testing.T) {
		r := newHeardRoute(t, "", false)
		r.a.publishVoiceMode(mode(speakOn))
		r.a.settleVoice(ctx, "  \n ")
		awaitAppWork(t, r.a)
		if got, refs := r.spokeBy(), r.replyRefs(); got != "nobody" || len(refs) != 0 {
			t.Fatalf("a turn that answered with silence was spoken (%s) or shown (%+v)", got, refs)
		}
	})
	t.Run("auto: a typed conversation is not read aloud, as before", func(t *testing.T) {
		r := newHeardRoute(t, "", false)
		r.a.settleVoice(ctx, "typed under auto")
		awaitAppWork(t, r.a)
		if got, refs := r.spokeBy(), r.replyRefs(); got != "nobody" || len(refs) != 0 {
			t.Fatalf("under auto a typed reply was spoken (%s) or bound (%+v)", got, refs)
		}
	})
	t.Run("speak off: nothing is spoken", func(t *testing.T) {
		r := newHeardRoute(t, "", false)
		r.a.publishVoiceMode(mode(speakOff))
		r.a.settleVoice(ctx, "typed with speaking off")
		awaitAppWork(t, r.a)
		if got := r.spokeBy(); got != "nobody" {
			t.Fatalf("with speak off a typed reply was spoken by %s", got)
		}
	})
	t.Run("a service chosen to speak takes typed replies by its own road", func(t *testing.T) {
		r := newHeardRoute(t, "ElevenLabs", false)
		r.a.publishVoiceMode(mode(speakOn))
		r.a.settleVoice(ctx, "typed beside a chosen service")
		awaitAppWork(t, r.a)
		if n := len(r.hearOn.synthed()); n != 0 {
			t.Fatalf("the hearing engine spoke a typed reply although a service is chosen to speak (%d)", n)
		}
		if refs := r.replyRefs(); len(refs) != 0 {
			t.Fatalf("a typed reply beside a chosen service was bound to the conversation: %+v", refs)
		}
	})
	t.Run("an output-only session still has it first", func(t *testing.T) {
		r := newHeardRoute(t, "id.engine.b", true)
		r.a.publishVoiceMode(mode(speakOn))
		r.a.settleVoice(ctx, "typed with a speaker lane open")
		awaitAppWork(t, r.a)
		if got := r.spokeBy(); got != "id.engine.b×1" {
			t.Fatalf("with an output session open the typed reply was spoken by %s", got)
		}
	})
}

func TestARefusedReplySaysWhetherItWasOnlySuperseded(t *testing.T) {
	refusals := func(r *heardRoute, session string) []dashboard.VoiceEvent {
		r.mu.Lock()
		defer r.mu.Unlock()
		var out []dashboard.VoiceEvent
		for _, ev := range r.events {
			if ev.Type == "reply_refused" && ev.SessionID == session {
				out = append(out, ev)
			}
		}
		return out
	}
	t.Run("the operator spoke over it", func(t *testing.T) {
		r := newHeardRoute(t, "", false)
		r.hears(func() (string, error) {
			r.a.voiceObserved(decodeVoiceFrame(pluginhost.Event{Type: "speech_start", SessionID: r.hear.id}))
			return "a superseded answer", nil
		})
		got := refusals(r, r.hear.id)
		if len(got) != 1 || !got[0].Superseded || got[0].Reason == "" {
			t.Fatalf("a reply the operator spoke over was reported as a failure, or not at all: %+v", got)
		}
	})
	t.Run("its session ended before it came", func(t *testing.T) {
		r := newHeardRoute(t, "", false)
		if v := r.a.synthesizeReply(context.Background(), "vs-gone", 1, "an answer nine minutes late"); v != replySuperseded {
			t.Fatalf("a reply for a session that is gone was admitted: %v", v)
		}
		got := refusals(r, "vs-gone")
		if len(got) != 1 || got[0].Superseded || got[0].Reason != "the session ended before its reply" {
			t.Fatalf("a reply whose session ended was not reported as one no voice spoke: %+v", got)
		}
	})
	t.Run("the voice chosen to speak cannot speak here", func(t *testing.T) {
		r := newHeardRoute(t, "id.engine.b", false)
		r.hears(answer)
		got := refusals(r, r.hear.id)
		if len(got) != 1 || got[0].Superseded {
			t.Fatalf("a reply the chosen voice could not speak was not reported as an error: %+v", got)
		}
	})
}
