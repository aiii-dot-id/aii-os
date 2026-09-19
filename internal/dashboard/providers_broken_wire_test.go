package dashboard

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// .
// .
// .
// .
func TestProvidersMessageCarriesBrokenEntriesAndTheSignInSetting(t *testing.T) {
	calls := make(chan string, 4)
	h := &WSHandler{
		GetProviders: func() ProviderDirectory {
			return ProviderDirectory{
				Providers:                []ProviderInfo{{Name: "A"}},
				Broken:                   []BrokenProviderInfo{{Position: 1, SHA256: "ab", Name: "B", Reason: `unknown field "api_kye"`}},
				SkipSignInWithValidToken: false,
			}
		},
		RepairProvider: func(position int, sha string) error {
			calls <- fmt.Sprintf("repair %d %s", position, sha)
			return nil
		},
		RemoveBrokenProvider: func(position int, sha string) error {
			calls <- fmt.Sprintf("remove %d %s", position, sha)
			return nil
		},
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	conn := dialWS(t, addr)

	sendMsg(t, conn, ClientMessage{Type: "query", Query: "providers"})
	m := drainUntil(t, conn, "providers")
	if len(m.Providers) != 1 || len(m.BrokenProviders) != 1 || m.BrokenProviders[0].Name != "B" || m.BrokenProviders[0].Position != 1 {
		t.Fatalf("the broken entries did not ride with the list: %+v", m)
	}
	if m.SkipSignInWithValidToken == nil || *m.SkipSignInWithValidToken {
		t.Fatalf("the sign-in setting, off, was not on the wire: %v", m.SkipSignInWithValidToken)
	}

	one, zero := 1, 0
	sendMsg(t, conn, ClientMessage{Type: "provider_repair", RequestID: "r1", Position: &one, EntrySHA256: "ab"})
	if got := drainUntil(t, conn, "providers"); got.RequestID != "r1" {
		t.Fatalf("the repair was not answered with the refreshed list: %+v", got)
	}
	if got := <-calls; got != "repair 1 ab" {
		t.Fatalf("repair reached the app as %q", got)
	}
	sendMsg(t, conn, ClientMessage{Type: "provider_remove_broken", RequestID: "r2", Position: &zero, EntrySHA256: "cd"})
	drainUntil(t, conn, "providers")
	if got := <-calls; got != "remove 0 cd" {
		t.Fatalf("removal at position zero reached the app as %q", got)
	}
	sendMsg(t, conn, ClientMessage{Type: "provider_repair", RequestID: "r3", EntrySHA256: "ab"})
	if got := drainUntil(t, conn, "error"); !strings.Contains(got.Message, "not available") {
		t.Fatalf("a repair naming no position was not refused: %+v", got)
	}
	select {
	case got := <-calls:
		t.Fatalf("a repair naming no position reached the app: %q", got)
	default:
	}
}
