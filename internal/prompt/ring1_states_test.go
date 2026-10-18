package prompt

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store/rows"
)

// Ring 1 has THREE states and the composer used to render two.
//
// The branches were two independent ifs — "no relationship" and "charter
// non-empty" — over a ternary reality. An operator relationship that was
// approved but never chartered fell through both: the row's existence
// suppressed the reminder, the empty charter rendered nothing, and the
// identity was told NOTHING about its operator. Silence there is
// indistinguishable from having no operator and from having a complete
// one, which are the two states it is not.
func TestRing1RendersAllThreeStates(t *testing.T) {
	const charter = "Quinn is my operator."
	for _, c := range []struct {
		name       string
		identity   rows.PromptIdentity
		wantSubstr string
		absent     []string
	}{
		{
			name:       "no relationship at all",
			identity:   rows.PromptIdentity{},
			wantSubstr: ring1Reminder,
			absent:     []string{ring1Incomplete, charter},
		},
		{
			name:       "approved but never chartered — the state that used to be silent",
			identity:   rows.PromptIdentity{HasOperatorRelationship: true},
			wantSubstr: ring1Incomplete,
			absent:     []string{ring1Reminder},
		},
		{
			name:       "complete",
			identity:   rows.PromptIdentity{HasOperatorRelationship: true, Charter: charter},
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

func TestAnAffirmedCharterStillInvitesItsRevision(t *testing.T) {
	const charter = "Quinn is my operator."
	for _, c := range []struct{ name, operator, want string }{
		{"named operator", "Quinn", "for Quinn's affirmation or negation"},
		{"unnamed operator", "", "for their affirmation or negation"},
	} {
		t.Run(c.name, func(t *testing.T) {
			composer := New(ring.NewManager(), 32000)
			composer.SetIdentitySource(staticIdentitySource{identity: rows.PromptIdentity{HasOperatorRelationship: true, Charter: charter, OperatorName: c.operator}})
			prompt, err := composer.Compose("", 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"When you notice it has grown or changed", "commit relationship.upsert (charter_text)", c.want, "foundation for your growth together"} {
				if !strings.Contains(prompt.Text, want) {
					t.Fatalf("the frame with a charter does not carry %q:\n%s", want, prompt.Text)
				}
			}
			if strings.Contains(prompt.Text, ring1Reminder) {
				t.Fatal("the absent-charter reminder rode a frame that has a charter")
			}
		})
	}
}

func TestEveryRing1StateInvitesGrowth(t *testing.T) {
	for _, c := range []struct {
		name     string
		identity rows.PromptIdentity
	}{
		{"no relationship at all", rows.PromptIdentity{}},
		{"approved but never chartered", rows.PromptIdentity{HasOperatorRelationship: true}},
		{"chartered", rows.PromptIdentity{HasOperatorRelationship: true, Charter: "Quinn is my operator.", OperatorName: "Quinn"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			composer := New(ring.NewManager(), 32000)
			composer.SetIdentitySource(staticIdentitySource{identity: c.identity})
			prompt, err := composer.Compose("", 0)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(prompt.Text, "notice the changes and record them") {
				t.Fatalf("the %s state does not invite growth:\n%s", c.name, prompt.Text)
			}
		})
	}
}
