package broker

import "testing"

// A GRANT'S PORT IS A WHOLE NUMBER OR NOTHING. A scope that names a port names
// it exactly: "443x" or " 443" is no port at all, and the scope is refused
// rather than read as 443. fmt.Sscanf("%d") stopped at the first non-digit and
// skipped leading space, so it accepted both.
func TestAHostScopePortIsAWholeNumber(t *testing.T) {
	for _, s := range []string{"example.com:443x", "example.com: 443", "example.com:4 43", "example.com:0", "example.com:65536"} {
		if sc, ok := parseHostScope(s); ok {
			t.Errorf("%q was accepted as %+v", s, sc)
		}
	}
	for s, port := range map[string]int{"example.com:443": 443, "Example.COM:8443": 8443} {
		sc, ok := parseHostScope(s)
		if !ok || sc.port != port || sc.anyPort {
			t.Errorf("%q: got %+v ok=%v", s, sc, ok)
		}
	}
	if sc, ok := parseHostScope("example.com"); !ok || !sc.anyPort {
		t.Errorf("a host without a port is any port: %+v ok=%v", sc, ok)
	}
}
