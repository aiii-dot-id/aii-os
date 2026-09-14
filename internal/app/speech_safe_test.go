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

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .

// .
func countingEngine(t *testing.T, text string, hits *int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(hits, 1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"text": text})
	}))
}

// .
// .
// .
// .
// .
// .
// .
func TestSAFEStopsAudioLeavingTheHost(t *testing.T) {
	var hits int32
	srv := countingEngine(t, "the ledger and the outbox disagree", &hits)
	defer srv.Close()

	a := speechApp(t, srv.URL)
	a.enterSafe("chain verification failed")

	err := a.HearUtterance(context.Background(), make([]byte, 3200), 16000, 1, false)
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

// .
// .
// .
// .
// .
// .
// .
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
