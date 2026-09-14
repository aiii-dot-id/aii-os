package app

import "testing"

// .
// .
func TestCredentialSourceIsSharedPerStore(t *testing.T) {
	a := New(defaultConfig())
	one, err := a.credentialSource("file:/nonexistent-but-shaped.json", nil)
	if err == nil {
		// .
		// .
		two, _ := a.credentialSource("file:/nonexistent-but-shaped.json", nil)
		if one != two {
			t.Fatal("the same store must yield the same source")
		}
		return
	}
	// .
	// .
	a.credMu.Lock()
	n := len(a.credSrc)
	a.credMu.Unlock()
	if n != 0 {
		t.Fatalf("a failed construction must not be cached, %d entries", n)
	}
}
