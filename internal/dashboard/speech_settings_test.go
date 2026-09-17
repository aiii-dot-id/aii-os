package dashboard

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// .
// .
// .
func TestASpeechServiceAnswersUnderItsRequest(t *testing.T) {
	var gotName, gotKey, gotBase string
	h := &WSHandler{
		SetSpeechService: func(name, key, base string) error {
			if name == "Nowhere" {
				return fmt.Errorf("not a speech service this release ships")
			}
			gotName, gotKey, gotBase = name, key, base
			return nil
		},
		GetProviders: func() []ProviderInfo { return []ProviderInfo{{Name: "Deepgram"}} },
		GetConfig:    func() (*ConfigState, error) { return &ConfigState{}, nil },
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	conn := dialWS(t, addr)

	sendMsg(t, conn, ClientMessage{RequestID: "add-1", Type: "speech_service", Provider: "Deepgram", APIKey: "dg", BaseURL: "http://localhost:8000/v1"})
	if m := drainUntil(t, conn, "providers"); m.RequestID != "add-1" || len(m.Providers) != 1 {
		t.Fatalf("providers reply %+v", m)
	}
	if gotName != "Deepgram" || gotKey != "dg" || gotBase != "http://localhost:8000/v1" {
		t.Fatalf("handler got %q %q %q", gotName, gotKey, gotBase)
	}
	sendMsg(t, conn, ClientMessage{RequestID: "add-2", Type: "speech_service", Provider: "Nowhere"})
	if m := drainUntil(t, conn, "error"); m.RequestID != "add-2" || !strings.Contains(m.Message, "not a speech service") {
		t.Fatalf("refusal %+v", m)
	}
}

// .
// .
// .
func TestSpeechListsAnswerUnderTheirRequest(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	var carried atomic.Value
	carried.Store("")
	h := &WSHandler{
		SpeechLists: func(provider, direction, search, language, apiKey string) (SpeechLists, error) {
			if provider == "Nowhere" {
				return SpeechLists{}, fmt.Errorf("Nowhere is not a tts service")
			}
			carried.Store(apiKey)
			<-release
			return SpeechLists{Provider: provider, Direction: direction, Search: search, Language: language,
				Voices: []SpeechItem{{ID: "v-1", Name: "Rachel", Detail: "american"}}, VoicesListed: true, VoicesComplete: true}, nil
		},
		GetConfig: func() (*ConfigState, error) { return &ConfigState{}, nil },
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	conn := dialWS(t, addr)

	sendMsg(t, conn, ClientMessage{RequestID: "lists-1", Type: "query", Query: "speech_lists", Provider: "ElevenLabs", Direction: "tts", Search: "barber", Language: "en", APIKey: "typed"})
	sendMsg(t, conn, ClientMessage{RequestID: "cfg-1", Type: "query", Query: "config"})
	if m := drainUntil(t, conn, "config"); m.Config == nil {
		t.Fatalf("the connection waited on a vendor's list: %+v", m)
	}
	once.Do(func() { close(release) })
	m := drainUntil(t, conn, "speech_lists")
	if m.RequestID != "lists-1" || m.SpeechLists == nil || m.SpeechLists.Provider != "ElevenLabs" || m.SpeechLists.Direction != "tts" ||
		len(m.SpeechLists.Voices) != 1 || !m.SpeechLists.VoicesListed || m.SpeechLists.Search != "barber" || m.SpeechLists.Language != "en" {
		t.Fatalf("lists reply %+v", m)
	}
	// .
	// .
	// .
	if got := carried.Load().(string); got != "typed" {
		t.Fatalf("the door did not carry the typed key: %q", got)
	}
	sendMsg(t, conn, ClientMessage{RequestID: "lists-2", Type: "query", Query: "speech_lists", Provider: "Nowhere", Direction: "tts"})
	if m := drainUntil(t, conn, "error"); m.RequestID != "lists-2" || !strings.Contains(m.Message, "not a tts service") {
		t.Fatalf("refusal %+v", m)
	}
}
