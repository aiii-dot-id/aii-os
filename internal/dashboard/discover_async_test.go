package dashboard

import (
	"context"
	"testing"
)

// .
// .
// .
// .
// .
// .
// .
func TestASlowDiscoveryDoesNotHoldTheConnection(t *testing.T) {
	release := make(chan struct{})
	h := &WSHandler{
		GetProviders: func() ProviderDirectory { return ProviderDirectory{Providers: []ProviderInfo{{Name: "Claude"}}} },
		DiscoverModels: func(provider, _ string) ([]string, error) {
			<-release
			return []string{provider + "-model"}, nil
		},
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	conn := dialWS(t, addr)

	sendMsg(t, conn, ClientMessage{RequestID: "discover-1", Type: "query", Query: "discover", Provider: "Claude"})
	sendMsg(t, conn, ClientMessage{Type: "query", Query: "providers"})
	// .
	// .
	if got := drainUntil(t, conn, "providers"); len(got.Providers) != 1 || got.Providers[0].Name != "Claude" {
		t.Fatalf("providers answered %+v behind a blocked discovery", got.Providers)
	}
	close(release)
	models := drainUntil(t, conn, "models")
	if models.RequestID != "discover-1" || len(models.ModelList) != 1 || models.ModelList[0] != "Claude-model" {
		t.Fatalf("the discovery answered %+v", models)
	}
}
