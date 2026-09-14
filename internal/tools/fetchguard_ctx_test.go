package tools

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

type blockingResolver struct{}

func (blockingResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// .
// .
// .
// .
func TestFetchGuardHonoursItsContext(t *testing.T) {
	old := ipResolver
	ipResolver = blockingResolver{}
	defer func() { ipResolver = old }()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- FetchGuard(ctx, "http://example.invalid/") }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "context") {
			t.Fatalf("expected the context's error, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("FetchGuard ignored its context — the pre-flight lookup is uncancellable")
	}
}
