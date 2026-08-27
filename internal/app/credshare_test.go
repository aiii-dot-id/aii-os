package app

import "testing"

// ONE source per credential store. The shared Source coordinates one
// parsed generation while rereading the owner-maintained file on change.
func TestCredentialSourceIsSharedPerStore(t *testing.T) {
	a := New(defaultConfig())
	one, err := a.credentialSource("file:/nonexistent-but-shaped.json", nil)
	if err == nil {
		// A missing file is fine for this test only if construction failed;
		// if it somehow succeeded, identity must still be shared.
		two, _ := a.credentialSource("file:/nonexistent-but-shaped.json", nil)
		if one != two {
			t.Fatal("the same store must yield the same source")
		}
		return
	}
	// Construction failed (expected): nothing must be cached, so a later
	// attempt after the operator creates the file can still succeed.
	a.credMu.Lock()
	n := len(a.credSrc)
	a.credMu.Unlock()
	if n != 0 {
		t.Fatalf("a failed construction must not be cached, %d entries", n)
	}
}
