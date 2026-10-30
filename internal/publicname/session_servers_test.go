package publicname

import (
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/relay"
)

func TestTheRelaysServersReachTheDashboardWithTheirTime(t *testing.T) {
	r, _ := fixtureRuntime(Settings{})
	if servers, expire := r.sessionServers(); servers != nil || !expire.IsZero() {
		t.Fatalf("with no relay the dashboard was handed %d servers until %v", len(servers), expire)
	}
	c := &relay.Client{Name: "n.example.test", Relay: "relay.example.invalid:8181"}
	expire := time.Now().Add(12 * time.Hour).Truncate(time.Second)
	c.HoldServersForTest([]relay.SessionServer{{URLs: []string{"turn:turn.example.test:3478?transport=udp"}, Username: "1700000000:n", Credential: "fixture"}}, expire)
	r.relay = c
	servers, got := r.sessionServers()
	if len(servers) != 1 || servers[0].Username != "1700000000:n" || servers[0].Credential != "fixture" {
		t.Fatalf("the relay's one server reached the dashboard as %+v", servers)
	}
	if !got.Equal(expire) {
		t.Fatalf("the relay's servers reached the dashboard with time %v, want the %v the relay stated", got, expire)
	}
}
