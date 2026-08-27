package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// speech_safe_test.go — SAFE takes the microphone on the path a browser
// actually uses.
//
// THE REFUSAL EXISTED, ON THE WRONG DOOR. broker.dispatchVoiceObserve has
// refused voice.observe under SAFE since the stream carrier was
// discarded, and preserving that refusal through the discard was treated
// as the careful part of the work. What went unnoticed is that the path
// built beside it — the browser's, through HearUtterance — never asked.
// SAFE held shut the door nobody walks through.
//
// So these tests assert the thing that actually matters, which is not
// whether the words were recorded but whether the audio LEFT.

// countingEngine transcribes, and counts how many times it was asked.
func countingEngine(t *testing.T, text string, hits *int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(hits, 1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"text": text})
	}))
}

// SAFE MEANS THE AUDIO DOES NOT LEAVE.
//
// Not merely that the words are not recorded — that too, but it is the
// weaker claim and the later one. By the time a recording decision is
// reached, the room has already been sent to a third party on the
// strength of a configuration the identity has just declared it cannot
// trust, and no refusal downstream calls it back.
func TestSAFEStopsAudioLeavingTheHost(t *testing.T) {
	var hits int32
	srv := countingEngine(t, "the ledger and the outbox disagree", &hits)
	defer srv.Close()

	a := speechApp(t, srv.URL)
	a.voice = &voiceSession{}
	a.enterSafe("chain verification failed")

	err := a.HearUtterance(context.Background(), make([]byte, 3200), 16000, 1)
	if err == nil {
		t.Fatal("SAFE ACCEPTED AN UTTERANCE — the microphone is open while the record is untrusted")
	}
	if !strings.Contains(err.Error(), "SAFE") {
		t.Fatalf("the refusal does not tell the operator why: %v", err)
	}
	if n := atomic.LoadInt32(&hits); n != 0 {
		t.Fatalf("AUDIO LEFT THE HOST DURING SAFE: %d request(s) reached the endpoint", n)
	}
	turns, err := a.store.RecentTurns(10)
	if err != nil {
		t.Fatal(err)
	}
	for _, turn := range turns {
		if turn.Role == "participant" {
			t.Fatalf("SAFE recorded something it heard: %q", turn.Content)
		}
	}
}

// AND THE PAGE IS NOT INVITED TO SPEAK.
//
// The enforcement is above; this is the courtesy. It has to come from
// SAFE rather than from a fixture that was never offering a microphone
// in the first place, so the control is proven present before SAFE
// begins — otherwise the test passes for the wrong reason, which is the
// failure mode that produced the bug it is here to catch.
func TestSAFEOffersNoMicrophone(t *testing.T) {
	srv := fakeEngine(t, "unused")
	defer srv.Close()

	a := speechApp(t, srv.URL)
	if !a.VoiceConfigured() {
		t.Fatal("fixture: the microphone must be offered BEFORE SAFE, or this proves nothing")
	}
	a.enterSafe("chain verification failed")
	if a.VoiceConfigured() {
		t.Fatal("SAFE still offered a microphone")
	}
}
