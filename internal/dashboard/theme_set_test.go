package dashboard

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// THE PAGE'S CHANGE TO theme.json IS ANSWERED UNDER ITS REQUEST. The
// tokens go to the app's writer as sent; the theme as it now is comes back
// to the page that asked, under its request, and to every other page; a
// refusal comes back under the request in the app's words, and no other
// page hears of it.
func TestAThemeChangeIsAnsweredUnderItsRequest(t *testing.T) {
	var got map[string]string
	theme := []byte(`{"v":1,"tokens":{"--mark-shape":"1"}}`)
	h := &WSHandler{
		GetStats: func() (*StatsResponse, error) { return &StatsResponse{}, nil },
		SetThemeTokens: func(tokens map[string]string) error {
			if tokens["--mark-shape"] == "bad" {
				return errors.New("the page sent a token theme.json does not take: --mark-shape")
			}
			got = tokens
			return nil
		},
	}
	s := New("127.0.0.1", 0, h)
	s.SetThemeSource(func() []byte { return theme })
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	asker, other := dialWS(t, addr), dialWS(t, addr)
	_ = drainUntil(t, asker, "status")
	_ = drainUntil(t, other, "status")

	sendMsg(t, asker, ClientMessage{RequestID: "mark-1", Type: "theme_set", Tokens: map[string]string{"--mark-shape": "1", "--mark-folds": ""}})
	if m := drainUntil(t, asker, "theme"); m.RequestID != "mark-1" || string(m.Theme) != string(theme) {
		t.Fatalf("the asking page was not answered with the theme under its request: %+v", m)
	}
	if got["--mark-shape"] != "1" || got["--mark-folds"] != "" || len(got) != 2 {
		t.Fatalf("the tokens did not reach the writer as sent: %v", got)
	}
	if m := drainUntil(t, other, "theme"); string(m.Theme) != string(theme) {
		t.Fatalf("another page did not hear the new theme: %+v", m)
	}

	sendMsg(t, asker, ClientMessage{RequestID: "mark-2", Type: "theme_set", Tokens: map[string]string{"--mark-shape": "bad"}})
	if m := drainUntil(t, asker, "error"); m.RequestID != "mark-2" || !strings.Contains(m.Message, "does not take") {
		t.Fatalf("a refusal was not answered under its request in the app's words: %+v", m)
	}
	sendMsg(t, asker, ClientMessage{RequestID: "mark-3", Type: "theme_set"})
	if m := drainUntil(t, asker, "error"); m.RequestID != "mark-3" || !strings.Contains(m.Message, "requires tokens") {
		t.Fatalf("an empty change was not refused: %+v", m)
	}
}
