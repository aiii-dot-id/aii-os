//go:build (linux && !android) || (darwin && !ios) || windows

package hostcap

import "testing"

// .
// .
// .
func TestStatusesAreHonest(t *testing.T) {
	for _, c := range []Capability{Subprocess, Shell, NativeChild, SelfReplace} {
		st := Can(c)
		if st.Available && st.Reason != "" {
			t.Fatalf("capability %d: available with a reason attached — pick one", c)
		}
		if !st.Available && st.Reason == "" {
			t.Fatalf("capability %d: unavailable with no reason — a bare refusal", c)
		}
	}
}
