package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// .
func sayPost(t *testing.T, addr, body, origin, cookie string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "https://"+addr+"/speech/say", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	resp, err := testClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// .
func sayGet(t *testing.T, addr, id string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "https://"+addr+"/speech/say/"+id, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := testClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func sayID(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	var said map[string]string
	body, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(body, &said)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("minting was refused: %d %s", resp.StatusCode, body)
	}
	return said["id"]
}

// .
// .
// .
// .
// .
func TestTheReplyAudioRouteSpeaksAndRefuses(t *testing.T) {
	var got atomic.Value
	got.Store("")
	var asked atomic.Value
	asked.Store(SpeakText{})
	h := &WSHandler{
		SpeakMint: func(say SpeakText) (string, error) {
			got.Store(say.Text)
			asked.Store(say)
			if say.Text == "the service refuses this" {
				return "", errors.New("this voice is not on your plan")
			}
			return "reply-1", nil
		},
		SpeakPlay: func(ctx context.Context, id string, w io.Writer) error {
			if id != "reply-1" {
				return errors.New("that reply is no longer here to be spoken")
			}
			_, err := w.Write([]byte("RIFF....WAVE"))
			return err
		},
		ReplyVoice: func() string { return "ElevenLabs" },
		GetConfig:  func() (*ConfigState, error) { return &ConfigState{}, nil },
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())

	id := sayID(t, sayPost(t, addr, `{"text":"the kettle is on"}`, "https://"+addr, ""))
	if id != "reply-1" || got.Load().(string) != "the kettle is on" {
		t.Fatalf("the words did not reach the service: %q, id %q", got.Load(), id)
	}
	resp := sayGet(t, addr, id)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "audio/wav" || !bytes.HasPrefix(body, []byte("RIFF")) {
		t.Fatalf("the reply was not spoken: %d %s %q", resp.StatusCode, resp.Header.Get("Content-Type"), body)
	}
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("an identity's words were left cacheable: %q", resp.Header.Get("Cache-Control"))
	}

	// .
	// .
	resp = sayGet(t, addr, "reply-gone")
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway || !strings.Contains(string(body), "no longer here") {
		t.Fatalf("an unknown reply answered %d %s", resp.StatusCode, body)
	}

	// .
	resp = sayPost(t, addr, `{"text":"the service refuses this"}`, "https://"+addr, "")
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	var said map[string]string
	_ = json.Unmarshal(body, &said)
	if resp.StatusCode != http.StatusBadGateway || !strings.Contains(said["error"], "not on your plan") {
		t.Fatalf("the service's own words did not reach the page: %d %s", resp.StatusCode, body)
	}

	// .
	_ = sayID(t, sayPost(t, addr, `{"sample":true,"provider":"Cartesia","model":"sonic-2","voice":"v-7","api_key":"only-typed"}`, "https://"+addr, ""))
	if c := asked.Load().(SpeakText); !c.Sample || c.Provider != "Cartesia" || c.Model != "sonic-2" || c.Voice != "v-7" || c.APIKey != "only-typed" {
		t.Fatalf("the card did not reach the service: %+v", c)
	}

	// .
	resp = sayPost(t, addr, `{"text":"the kettle is on"}`, "https://elsewhere.example", "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a cross-site request was answered %d", resp.StatusCode)
	}
}

// .
// .
// .
func TestTheReplyAudioArrivesAsItIsMade(t *testing.T) {
	second := make(chan struct{})
	h := &WSHandler{
		SpeakMint: func(say SpeakText) (string, error) { return "reply-1", nil },
		SpeakPlay: func(ctx context.Context, id string, w io.Writer) error {
			if _, err := w.Write([]byte("RIFF....WAVEfirst")); err != nil {
				return err
			}
			if f, ok := w.(interface{ Flush() }); ok {
				f.Flush()
			}
			select {
			case <-second:
			case <-ctx.Done():
				return ctx.Err()
			}
			_, err := w.Write([]byte("second"))
			return err
		},
		ReplyVoice: func() string { return "ElevenLabs" },
		GetConfig:  func() (*ConfigState, error) { return &ConfigState{}, nil },
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())

	resp := sayGet(t, addr, "reply-1")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the stream did not open: %d", resp.StatusCode)
	}
	opening := make([]byte, len("RIFF....WAVEfirst"))
	read := make(chan error, 1)
	go func() {
		_, err := io.ReadFull(resp.Body, opening)
		read <- err
	}()
	select {
	case err := <-read:
		if err != nil {
			t.Fatalf("the opening did not arrive: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the opening waited for the whole reply")
	}
	if !bytes.HasPrefix(opening, []byte("RIFF")) {
		t.Fatalf("the opening is not audio: %q", opening)
	}
	close(second)
	rest, err := io.ReadAll(resp.Body)
	if err != nil || string(rest) != "second" {
		t.Fatalf("the rest did not follow: %q %v", rest, err)
	}
}

// .
// .
// .
func TestAnIdentitysReplyCarriesTheVoiceItIsSpokenIn(t *testing.T) {
	var ahead atomic.Int32
	h := &WSHandler{
		SpeakAhead: func(text string) string {
			ahead.Add(1)
			return "reply-1"
		},
		SpeakMint:  func(say SpeakText) (string, error) { return "reply-1", nil },
		SpeakPlay:  func(ctx context.Context, id string, w io.Writer) error { return nil },
		ReplyVoice: func() string { return "ElevenLabs" },
		GetConfig:  func() (*ConfigState, error) { return &ConfigState{}, nil },
		GetStats:   func() (*StatsResponse, error) { return &StatsResponse{}, nil },
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())

	// .
	if msg := s.spokenAloud(ServerMessage{Type: "response", Message: "the kettle is on", Role: "identity", Done: true}); msg.VoiceReply != nil || ahead.Load() != 0 {
		t.Fatalf("a reply was spoken to an empty room: %+v", msg.VoiceReply)
	}

	conn := dialWS(t, addr)
	defer conn.CloseNow()
	// .
	for i := 0; i < 400 && s.screens() == 0; i++ {
		time.Sleep(5 * time.Millisecond)
	}
	s.BroadcastResponse("identity", "the kettle is on")
	m := drainUntil(t, conn, "response")
	if m.VoiceReply == nil || m.VoiceReply.SynthesisID != "reply-1" || m.VoiceReply.Route != "cloud" {
		t.Fatalf("the reply did not carry the voice it is spoken in: %+v", m.VoiceReply)
	}

	// .
	s.BroadcastVoiceReply(VoiceReplyRef{SessionID: "vs-1", SynthesisID: "syn-1", Route: "plugin"}, "the engine says this")
	m = drainUntil(t, conn, "response")
	if m.VoiceReply == nil || m.VoiceReply.Route != "plugin" {
		t.Fatalf("an engine-spoken reply was taken over: %+v", m.VoiceReply)
	}

	// .
	if msg := s.spokenAloud(ServerMessage{Type: "response", Message: "a system line", Role: "system", Done: true}); msg.VoiceReply != nil {
		t.Fatal("a system line was given a voice")
	}
}

// .
// .
func TestWithoutASpeakingServiceTheRouteSaysSo(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{GetConfig: func() (*ConfigState, error) { return &ConfigState{}, nil }})
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	resp := sayPost(t, addr, `{"text":"the kettle is on"}`, "https://"+addr, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("a host that cannot speak answered %d", resp.StatusCode)
	}
}

// .
// .
// .
// .
// .
func TestNothingConfiguredIsNotABadGateway(t *testing.T) {
	var asked atomic.Int32
	h := &WSHandler{
		SpeakMint: func(say SpeakText) (string, error) {
			asked.Add(1)
			return "reply-1", nil
		},
		SpeakPlay:  func(ctx context.Context, id string, w io.Writer) error { return nil },
		ReplyVoice: func() string { return "" },
		GetConfig:  func() (*ConfigState, error) { return &ConfigState{}, nil },
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	resp := sayPost(t, addr, `{"text":"the kettle is on"}`, "https://"+addr, "")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(body), "no speaking service") {
		t.Fatalf("a host with nothing configured answered %d %s", resp.StatusCode, body)
	}
	if asked.Load() != 0 {
		t.Fatalf("a service that was never chosen was asked to speak %d times", asked.Load())
	}
	resp = sayPost(t, addr, `{"sample":true,"provider":"Cartesia","voice":"c-2","api_key":"only-typed"}`, "https://"+addr, "")
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || asked.Load() != 1 {
		t.Fatalf("a sample was refused because nothing was saved yet: %d %s", resp.StatusCode, body)
	}
}

// .
// .
func TestTheReplyAudioRouteNeedsTheAccessToken(t *testing.T) {
	h := &WSHandler{
		SpeakMint:  func(say SpeakText) (string, error) { return "reply-1", nil },
		SpeakPlay:  func(ctx context.Context, id string, w io.Writer) error { _, err := w.Write([]byte("RIFF")); return err },
		ReplyVoice: func() string { return "ElevenLabs" },
		GetConfig:  func() (*ConfigState, error) { return &ConfigState{}, nil },
	}
	s := New("127.0.0.1", 0, h)
	s.SetAccessToken(true, "the-operators-token")
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	resp := sayPost(t, addr, `{"text":"the kettle is on"}`, "https://"+addr, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a tokenless request was answered %d", resp.StatusCode)
	}
	resp = sayGet(t, addr, "reply-1")
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a tokenless play was answered %d", resp.StatusCode)
	}
}

// .
// .
// .
func TestTheStatusNamesTheVoiceThatSpeaks(t *testing.T) {
	stats := func(h *WSHandler) StatsResponse {
		h.GetStats = func() (*StatsResponse, error) { return &StatsResponse{}, nil }
		s := New("127.0.0.1", 0, h)
		msg, ok := s.statusMessage(h)
		if !ok || msg.Stats == nil {
			t.Fatalf("no status: %+v", msg)
		}
		return *msg.Stats
	}
	speaking := stats(&WSHandler{
		SpeakMint:  func(say SpeakText) (string, error) { return "reply-1", nil },
		ReplyVoice: func() string { return "ElevenLabs" },
	})
	if speaking.ReplyVoice != "ElevenLabs" {
		t.Errorf("the voice that speaks replies was not named: %q", speaking.ReplyVoice)
	}
	mute := stats(&WSHandler{ReplyVoice: func() string { return "ElevenLabs" }})
	if mute.ReplyVoice != "" {
		t.Errorf("a host that cannot speak claimed a voice: %q", mute.ReplyVoice)
	}
	if browser := stats(&WSHandler{}); browser.ReplyVoice != "" {
		t.Errorf("a host with no speaking service claimed one: %q", browser.ReplyVoice)
	}
}

// .
// .
// .
func TestTheDashboardTokenIsAnsweredOnlyToTheScreenThatAsks(t *testing.T) {
	h := &WSHandler{
		DashboardToken: func() string { return "the-running-token" },
		GetConfig:      func() (*ConfigState, error) { return &ConfigState{Dashboard: DashboardState{RequireToken: true}}, nil },
	}
	s := New("127.0.0.1", 0, h)
	s.SetAccessToken(true, "the-running-token")
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	conn, err := dialWSToken(addr, s.accessHash())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	sendMsg(t, conn, ClientMessage{RequestID: "cfg-1", Type: "query", Query: "config"})
	m := drainUntil(t, conn, "config")
	if raw, _ := json.Marshal(m); strings.Contains(string(raw), "the-running-token") {
		t.Fatalf("the token rides in the configuration frame: %s", raw)
	}
	sendMsg(t, conn, ClientMessage{RequestID: "tok-1", Type: "query", Query: "dashboard_token"})
	m = drainUntil(t, conn, "dashboard_token")
	if m.RequestID != "tok-1" || m.DashboardToken != "the-running-token" {
		t.Fatalf("the token was not answered to the screen that asked: %+v", m)
	}

	open := New("127.0.0.1", 0, h)
	openAddr, err := open.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer open.Shutdown(context.Background())
	c2 := dialWS(t, openAddr)
	defer c2.CloseNow()
	sendMsg(t, c2, ClientMessage{RequestID: "tok-2", Type: "query", Query: "dashboard_token"})
	if m := drainUntil(t, c2, "error"); m.RequestID != "tok-2" {
		t.Fatalf("an open dashboard handed out a token: %+v", m)
	}
}

// .
// .
func TestARotatedTokenEndsTheOldCookies(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{GetConfig: func() (*ConfigState, error) { return &ConfigState{}, nil }})
	s.SetAccessToken(true, "the-first-token")
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	old := s.accessHash()
	if c, err := dialWSToken(addr, old); err != nil {
		t.Fatalf("the first token did not admit: %v", err)
	} else {
		c.CloseNow()
	}
	s.SetAccessToken(true, "the-second-token")
	if _, err := dialWSToken(addr, old); err == nil {
		t.Fatal("a cookie minted under the old token still admits after the rotation")
	}
	if c, err := dialWSToken(addr, s.accessHash()); err != nil {
		t.Fatalf("the new token does not admit: %v", err)
	} else {
		c.CloseNow()
	}
	if s.validAccessToken("the-first-token") || !s.validAccessToken("the-second-token") {
		t.Fatal("the login verifier did not follow the rotation")
	}
}
