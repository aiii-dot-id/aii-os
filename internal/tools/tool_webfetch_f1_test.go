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

// F1 (external review 2026-08-17): DNS rebinding closed at the dial. The
// pre-flight guard resolves and vets the hostname, but the default
// transport re-resolves at connect — attacker DNS answers public for
// the guard, loopback for the dial. guardedDialContext resolves ONCE,
// vets every address, dials the vetted literals: guard and connection
// structurally cannot see different resolutions.

// rebindResolver answers exactly like attacker-controlled DNS during a
// rebinding attack: the first lookup (the guard's) gets a public IP,
// every later lookup (the transport's re-resolve) gets loopback.
type rebindResolver struct{ calls int }

func (r *rebindResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	r.calls++
	if r.calls == 1 {
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil // public — passes the guard
	}
	return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil // loopback — the attack
}

// TestGuardedDialBlocksRebinding: even when the pre-flight guard is
// fooled (public answer at check time), the dial-time floor refuses
// the loopback answer at connect time. The fixture loopback server
// must remain unreachable through the guarded transport.
func TestGuardedDialBlocksRebinding(t *testing.T) {
	// SSRF target stand-in on loopback.
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "metadata-service-reached")
	}))
	defer target.Close()

	// Swap in the rebinding resolver for BOTH guard and dial.
	prev := ipResolver
	ipResolver = &rebindResolver{}
	defer func() { ipResolver = prev }()

	// The pre-flight guard passes: first lookup answers public.
	if err := FetchGuard("http://rebind.example.com/"); err != nil {
		t.Fatalf("guard should pass on the first (public) answer: %v", err)
	}

	// The guarded client dials: the dial-time check sees the second
	// answer (loopback) and must refuse the connection.
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

// TestGuardedDialContextVetsLiterals: IP-literal URLs never resolve —
// the floor vets them directly, same classes as FetchGuard.
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
