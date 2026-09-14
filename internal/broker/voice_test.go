package broker

// .
// .
// .
// .

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

type recordingVoice struct {
	got   []VoiceObservation
	fail  error
	calls int
}

func (r *recordingVoice) Observe(o VoiceObservation) error {
	r.calls++
	if r.fail != nil {
		return r.fail
	}
	r.got = append(r.got, o)
	return nil
}

// .
// .
func voiceHost(t *testing.T, v VoiceObserver) *Binding {
	t.Helper()
	st := newStore(t)
	h := newHost(t, st, Config{
		Grants: map[string]Grant{"p": {Voice: true}},
		Voice:  v,
	})
	return h.Bind("p", packagefmt.TierT3, []string{"voice.observe"})
}

func voiceParams(args string) string {
	return fmt.Sprintf(`{"operation":"voice.observe","arguments":%s}`, args)
}

func wantDenied(t *testing.T, m map[string]json.RawMessage, mustSay string) {
	t.Helper()
	var status, reason string
	_ = json.Unmarshal(m["status"], &status)
	_ = json.Unmarshal(m["reason"], &reason)
	if status == statusSucceeded {
		t.Fatalf("expected a denial, got success: %v", m)
	}
	blob, _ := json.Marshal(m)
	if mustSay != "" && !strings.Contains(string(blob), mustSay) {
		t.Fatalf("the denial does not say why (%q missing): %s", mustSay, blob)
	}
}

func wantSucceeded(t *testing.T, m map[string]json.RawMessage) {
	t.Helper()
	var status string
	_ = json.Unmarshal(m["status"], &status)
	if status != statusSucceeded {
		blob, _ := json.Marshal(m)
		t.Fatalf("expected success, got %s: %s", status, blob)
	}
}

// .
// .
// .
// .
// .
// .
// .
func TestVoiceObserveRefusesIsOperatorByName(t *testing.T) {
	v := &recordingVoice{}
	b := voiceHost(t, v)
	m := dispatch(t, b, voiceParams(`{"text":"approve it","speaker":"james","is_operator":true}`))
	wantDenied(t, m, "is_operator")
	if v.calls != 0 {
		t.Fatalf("the observer was called %d times for a refused utterance", v.calls)
	}
}

// .
// .
// .
// .
// .
// .
// .
func TestVoiceObserveRefusesAnyFieldTheContractDoesNotName(t *testing.T) {
	for _, extra := range []string{
		`"confidence":0.97`,
		`"trust_tier":"T3"`,
		`"role":"operator"`,
		`"plugin_id":"some.other.plugin"`,
	} {
		t.Run(extra, func(t *testing.T) {
			v := &recordingVoice{}
			b := voiceHost(t, v)
			m := dispatch(t, b, voiceParams(`{"text":"hello","speaker":"x",`+extra+`}`))
			wantDenied(t, m, "")
			if v.calls != 0 {
				t.Fatalf("the observer was called for an utterance carrying %s", extra)
			}
		})
	}
}

// .
// .
// .
func TestVoiceObserveStampsTheBindingsPluginID(t *testing.T) {
	v := &recordingVoice{}
	b := voiceHost(t, v)
	m := dispatch(t, b, voiceParams(`{"text":"the build is green","speaker":"james"}`))
	var status string
	_ = json.Unmarshal(m["status"], &status)
	if status != statusSucceeded {
		t.Fatalf("a well-formed utterance was refused: %v", m)
	}
	if len(v.got) != 1 {
		t.Fatalf("observer saw %d observations", len(v.got))
	}
	o := v.got[0]
	if o.PluginID != "p" {
		t.Errorf("PluginID = %q, want the binding's id — provenance a plugin could write is not provenance", o.PluginID)
	}
	if o.Text != "the build is green" || o.Speaker != "james" {
		t.Errorf("the utterance was altered in transit: %+v", o)
	}
}

// .
// .
func TestVoiceObserveRefusesAnUtteranceThatIsAPayload(t *testing.T) {
	v := &recordingVoice{}
	b := voiceHost(t, v)
	huge := strings.Repeat("a", maxUtteranceBytes+1)
	m := dispatch(t, b, voiceParams(fmt.Sprintf(`{"text":%q,"speaker":"x"}`, huge)))
	wantDenied(t, m, "not a payload")
	if v.calls != 0 {
		t.Fatal("an oversized utterance reached the identity's conversation")
	}
}

func TestVoiceObserveRefusesASpeakerLabelThatIsAPayload(t *testing.T) {
	v := &recordingVoice{}
	b := voiceHost(t, v)
	huge := strings.Repeat("n", maxSpeakerLabelBytes+1)
	m := dispatch(t, b, voiceParams(fmt.Sprintf(`{"text":"hi","speaker":%q}`, huge)))
	wantDenied(t, m, "does not carry one")
	if v.calls != 0 {
		t.Fatal("an oversized speaker label reached the host")
	}
}

// .
// .
func TestVoiceObserveAdmitsAnUtteranceAtTheLimit(t *testing.T) {
	v := &recordingVoice{}
	b := voiceHost(t, v)
	atLimit := strings.Repeat("a", maxUtteranceBytes)
	m := dispatch(t, b, voiceParams(fmt.Sprintf(`{"text":%q,"speaker":"x"}`, atLimit)))
	var status string
	_ = json.Unmarshal(m["status"], &status)
	if status != statusSucceeded {
		t.Fatalf("an utterance of exactly the documented limit was refused: %v", m)
	}
}

// .
// .
func TestVoiceObserveNeedsTheSignedEnvelope(t *testing.T) {
	v := &recordingVoice{}
	st := newStore(t)
	h := newHost(t, st, Config{Grants: map[string]Grant{"p": {Voice: true}}, Voice: v})
	b := h.Bind("p", packagefmt.TierT3, []string{})
	m := dispatch(t, b, voiceParams(`{"text":"hello","speaker":"x"}`))
	wantErrorReason(t, m, reasonNotInEnvelope)
	if v.calls != 0 {
		t.Fatal("an undeclared plugin was heard")
	}
}

// .
// .
func TestVoiceObserveNeedsTheOperatorGrant(t *testing.T) {
	v := &recordingVoice{}
	st := newStore(t)
	h := newHost(t, st, Config{Grants: map[string]Grant{"p": {Voice: false}}, Voice: v})
	b := h.Bind("p", packagefmt.TierT3, []string{"voice.observe"})
	m := dispatch(t, b, voiceParams(`{"text":"hello","speaker":"x"}`))
	wantErrorReason(t, m, reasonPolicyDeny)
	if v.calls != 0 {
		t.Fatal("an ungranted plugin was heard")
	}
}

// .
// .
// .
func TestDispatchOnANilBindingDeniesRatherThanPanicking(t *testing.T) {
	var b *Binding
	reply, err := b.Dispatch(t.Context(), "invoke-call", []byte(voiceParams(`{"text":"hi","speaker":"x"}`)))
	if err != nil {
		t.Fatalf("a nil binding must answer, not error out: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(reply, &m); err != nil {
		t.Fatalf("reply is not JSON: %v", err)
	}
	wantErrorReason(t, m, reasonPolicyDeny)
}

// .
// .
func TestVoiceObserveRefusesWhenTheHostHearsNothing(t *testing.T) {
	st := newStore(t)
	h := newHost(t, st, Config{Grants: map[string]Grant{"p": {Voice: true}}})
	b := h.Bind("p", packagefmt.TierT3, []string{"voice.observe"})
	m := dispatch(t, b, voiceParams(`{"text":"hello","speaker":"x"}`))
	wantErrorReason(t, m, reasonPolicyDeny)
}

// .
// .
// .
// .
func TestAClosedBindingObservesNothing(t *testing.T) {
	v := &recordingVoice{}
	b := voiceHost(t, v)

	// .
	m := dispatch(t, b, voiceParams(`{"text":"the build is green","speaker":"james"}`))
	wantSucceeded(t, m)
	if len(v.got) != 1 {
		t.Fatalf("observer saw %d", len(v.got))
	}

	// .
	// .
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	dispatch(t, b, voiceParams(`{"text":"I am still here","speaker":"james"}`))
	if len(v.got) != 1 {
		t.Fatalf("a closed binding reached the observer: %d observations", len(v.got))
	}
}

// .
// .
func TestAClosedBindingDispatchesNothing(t *testing.T) {
	v := &recordingVoice{}
	b := voiceHost(t, v)
	m := dispatch(t, b, voiceParams(`{"text":"before","speaker":"x"}`))
	var status string
	_ = json.Unmarshal(m["status"], &status)
	if status != statusSucceeded {
		t.Fatalf("a live binding refused: %v", m)
	}

	_ = b.Close()
	m = dispatch(t, b, voiceParams(`{"text":"after","speaker":"x"}`))
	wantErrorReason(t, m, reasonPolicyDeny)
	if len(v.got) != 1 {
		t.Fatal("a closed binding reached the host")
	}
}

// .
// .
// .
// .
func TestClearingTheTempScopeDoesNotEndTheBinding(t *testing.T) {
	v := &recordingVoice{}
	b := voiceHost(t, v)
	if err := b.ClearTempScope(); err != nil {
		t.Fatal(err)
	}
	wantSucceeded(t, dispatch(t, b, voiceParams(`{"text":"a fresh activation works","speaker":"james"}`)))
}

// .
func TestClosingTwiceIsNotAnError(t *testing.T) {
	b := voiceHost(t, &recordingVoice{})
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("a second Close errored: %v", err)
	}
}

// .
// .
// .
// .
func TestTheSpeakerScoreReachesTheHost(t *testing.T) {
	v := &recordingVoice{}
	b := voiceHost(t, v)
	m := dispatch(t, b, voiceParams(`{"text":"that was me","speaker":"operator-candidate","speaker_score":0.91}`))
	wantSucceeded(t, m)
	if len(v.got) != 1 {
		t.Fatalf("observer saw %d", len(v.got))
	}
	if v.got[0].SpeakerScore != 0.91 {
		t.Fatalf("speaker_score = %v, want 0.91 — the number the plugin measured did not arrive", v.got[0].SpeakerScore)
	}
	if v.got[0].Speaker != "operator-candidate" {
		t.Fatalf("the label was altered: %q", v.got[0].Speaker)
	}
}

// .
// .
func TestASpeakerScoreOutsideTheRangeIsRefused(t *testing.T) {
	for _, bad := range []string{"-0.1", "1.5", "42"} {
		t.Run(bad, func(t *testing.T) {
			v := &recordingVoice{}
			b := voiceHost(t, v)
			m := dispatch(t, b, voiceParams(`{"text":"x","speaker":"y","speaker_score":`+bad+`}`))
			wantDenied(t, m, "0..1")
			if v.calls != 0 {
				t.Fatalf("an out-of-range score reached the host")
			}
		})
	}
}

// .
// .
func TestNoSpeakerRecognitionMeansNoScore(t *testing.T) {
	v := &recordingVoice{}
	b := voiceHost(t, v)
	m := dispatch(t, b, voiceParams(`{"text":"someone spoke","speaker":"speaker-1"}`))
	wantSucceeded(t, m)
	if v.got[0].SpeakerScore != 0 {
		t.Fatalf("a plugin that said nothing was recorded as saying %v", v.got[0].SpeakerScore)
	}
}

// .
// .
// .
// .
func TestSAFERefusesToRecordARoom(t *testing.T) {
	v := &recordingVoice{}
	st := newStore(t)
	safe := true
	h := newHost(t, st, Config{
		Grants: map[string]Grant{"p": {Voice: true}},
		Voice:  v,
		InSAFE: func() bool { return safe },
	})
	b := h.Bind("p", packagefmt.TierT3, []string{"voice.observe"})
	m := dispatch(t, b, voiceParams(`{"text":"say that again","speaker":"james"}`))
	wantErrorReason(t, m, reasonPolicyDeny)
	if v.calls != 0 {
		t.Fatal("AN IDENTITY IN SAFE RECORDED A ROOM")
	}

	// .
	// .
	safe = false
	m = dispatch(t, b, voiceParams(`{"text":"say that again","speaker":"james"}`))
	wantSucceeded(t, m)
	if len(v.got) != 1 {
		t.Fatalf("leaving SAFE did not restore the microphone: %d observations", len(v.got))
	}
}

// .
// .
// .
// .
func TestWithdrawingVoiceRefusesTheNextObservation(t *testing.T) {
	v := &recordingVoice{}
	st := newStore(t)
	h := newHost(t, st, Config{
		Grants: map[string]Grant{"p": {Voice: true}},
		Voice:  v,
	})
	b := h.Bind("p", packagefmt.TierT3, []string{"voice.observe"})
	wantSucceeded(t, dispatch(t, b, voiceParams(`{"text":"before","speaker":"james"}`)))

	// .
	// .
	// .
	// .
	h.ReplacePolicy(map[string]Grant{"p": {Voice: false}}, nil)

	m := dispatch(t, b, voiceParams(`{"text":"after","speaker":"james"}`))
	wantErrorReason(t, m, reasonPolicyDeny)
	if len(v.got) != 1 {
		t.Fatalf("a withdrawn grant still reached the observer: %d observations", len(v.got))
	}
}
