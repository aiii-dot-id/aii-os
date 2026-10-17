package app

import (
	"math/rand/v2"
	"net"
	"strconv"
	"sync"
	"testing"
)

var (
	fixedPortsMu sync.Mutex
	fixedPorts   = map[int]bool{}
)

func fixedTestPort(t *testing.T) int {
	t.Helper()
	fixedPortsMu.Lock()
	defer fixedPortsMu.Unlock()
	for range 200 {
		p := 20000 + rand.IntN(10000)
		if fixedPorts[p] {
			continue
		}
		ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(p))
		if err != nil {
			continue // taken by something else on this host: try another
		}
		ln.Close()
		fixedPorts[p] = true
		return p
	}
	t.Fatal("no free loopback port in 20000-29999")
	return 0
}

func TestAFixedTestPortIsBelowTheEphemeralRangeAndNeverReused(t *testing.T) {
	seen := map[int]bool{}
	for range 50 {
		p := fixedTestPort(t)
		if p < 20000 || p > 29999 {
			t.Fatalf("port %d is outside 20000-29999", p)
		}
		if seen[p] {
			t.Fatalf("port %d handed out twice", p)
		}
		seen[p] = true
	}
}
