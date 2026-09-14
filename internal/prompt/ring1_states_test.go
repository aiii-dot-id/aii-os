package prompt

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
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
func TestRing1RendersAllThreeStates(t *testing.T) {
	const charter = "Sam is my operator."
	for _, c := range []struct {
		name       string
		identity   store.PromptIdentity
		wantSubstr string
		absent     []string
	}{
		{
			name:       "no relationship at all",
			identity:   store.PromptIdentity{},
			wantSubstr: ring1Reminder,
			absent:     []string{ring1Incomplete, charter},
		},
		{
			name:       "approved but never chartered — the state that used to be silent",
			identity:   store.PromptIdentity{HasOperatorRelationship: true},
			wantSubstr: ring1Incomplete,
			absent:     []string{ring1Reminder},
		},
		{
			name:       "complete",
			identity:   store.PromptIdentity{HasOperatorRelationship: true, Charter: charter},
			wantSubstr: charter,
			absent:     []string{ring1Reminder, ring1Incomplete},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			composer := New(ring.NewManager(), 32000)
			composer.SetIdentitySource(staticIdentitySource{identity: c.identity})
			prompt, err := composer.Compose("", 0)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(prompt.Text, c.wantSubstr) {
				t.Errorf("this Ring 1 state renders nothing the identity can act on;\nwant to find: %q", c.wantSubstr)
			}
			for _, a := range c.absent {
				if a != "" && strings.Contains(prompt.Text, a) {
					t.Errorf("a different Ring 1 state leaked in: %q", a)
				}
			}
		})
	}
}
