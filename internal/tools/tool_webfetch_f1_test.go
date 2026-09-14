package tools

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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
type rebindResolver struct{ calls int }

func (r *rebindResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	r.calls++
	if r.calls == 1 {
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	}
	return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
}

// .
// .
// .
// .
func TestGuardedDialBlocksRebinding(t *testing.T) {
	// .
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "metadata-service-reached")
	}))
	defer target.Close()

	// .
	prev := ipResolver
	ipResolver = &rebindResolver{}
	defer func() { ipResolver = prev }()

	// .
	if err := FetchGuard(context.Background(), "http://rebind.example.com/"); err != nil {
		t.Fatalf("guard should pass on the first (public) answer: %v", err)
	}

	// .
	// .
	client := GuardedClient(5*time.Second, nil, nil)
	req, _ := http.NewRequestWithContext(context.Background(), "GET", "http://rebind.example.com/", nil)
	resp, err := client.Do(req)
	if err == nil {
		resp.Body.Close()
		t.Fatal("guarded client reached a rebinding target: dial-time floor failed")
	}
	if !strings.Contains(err.Error(), "refusing non-public") {
		t.Fatalf("expected the dial floor's refusal, got: %v", err)
	}
}

// .
// .
func TestGuardedDialContextVetsLiterals(t *testing.T) {
	ctx := context.Background()
	for _, addr := range []string{
		"127.0.0.1:80",
		"169.254.169.254:80",
		"10.0.0.5:443",
		"192.168.1.1:80",
		"100.64.0.1:80",
		"[::1]:80",
	} {
		if _, err := guardedDialContext(ctx, "tcp", addr, nil); err == nil {
			t.Errorf("dial of %s should be refused", addr)
		} else if !strings.Contains(err.Error(), "refusing non-public") {
			t.Errorf("dial of %s refused for the wrong reason: %v", addr, err)
		}
	}
}
