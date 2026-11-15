package app

import (
	"errors"
	"fmt"
	"testing"
)

// THE RULE BETWEEN THE CADENCE AND THE SPACING IS ASKED ONCE, OF THE WHOLE
// CANDIDATE, AFTER EVERY KEY OF THE CHANGE HAS LANDED. Pinned ahead of
// breaking applyConfigChangeWith up, because a break-up has four ways to
// lose it that no other test names:
//
//	asking it of the configuration the change started from;
//	asking it with the cadence the change started from;
//	asking it after each key, so a change is refused for a candidate it
//	  only passes through;
//	not asking it.
//
// The refusal's own numbers (the cadence it names, the floor it advises
// and the default a zero would follow) are the candidate's too.

const (
	cadenceKey = "agency.rhythm_seconds"
	spacingKey = "cognition.consolidate_spacing_seconds"
)

// spacingRefusal is the sentence validCognition refuses a spacing with,
// written out here from reading it and not taken from the code under test.
func spacingRefusal(spacing, cadence, follows int) string {
	return fmt.Sprintf("%d is not longer than agency.rhythm_seconds (%d) — every tick with raw material would go to consolidation and none to DREAM; set more than %d, or 0 to follow the cadence (%d)",
		spacing, cadence, cadence, follows)
}

// cadenceRig is a host whose cadence and spacing were set through the door,
// with a save step that counts its runs.
type cadenceRig struct {
	t     *testing.T
	a     *App
	saves int
}

func newCadenceRig(t *testing.T, cadence, spacing int) *cadenceRig {
	t.Helper()
	r := &cadenceRig{t: t, a: newVoiceApp(t)}
	// The rule is asked of every number in the table, and a bare test
	// configuration holds zeros the loader never leaves (the outcome
	// window, the cadence): the host is given the loader's defaults, as
	// a configuration read from a file has them.
	r.a.cfgMu.Lock()
	applyDefaults(r.a.cfg)
	r.a.cfgMu.Unlock()
	// One key a change, the cadence first with no spacing stored: the rig
	// does not lean on how the door asks the rule of a pair.
	if _, err := r.a.applyConfigChangeWith(map[string]any{cadenceKey: float64(cadence)}, r.publish); err != nil {
		t.Fatalf("rig: cadence %d alone was refused: %v", cadence, err)
	}
	if _, err := r.a.applyConfigChangeWith(map[string]any{spacingKey: float64(spacing)}, r.publish); err != nil {
		t.Fatalf("rig: spacing %d alone, at cadence %d, was refused: %v", spacing, cadence, err)
	}
	r.expect("rig", cadence, spacing, 2)
	return r
}

func (r *cadenceRig) publish(*Config) (bool, error) { r.saves++; return true, nil }

func (r *cadenceRig) live() (int, int) {
	c := r.a.configSnapshot()
	return c.Agency.RhythmSeconds, c.Cognition.ConsolidateSpacingSeconds
}

func (r *cadenceRig) expect(when string, cadence, spacing, saves int) {
	r.t.Helper()
	if c, s := r.live(); c != cadence || s != spacing {
		r.t.Errorf("%s: live is cadence %d, spacing %d; want %d, %d", when, c, s, cadence, spacing)
	}
	if r.saves != saves {
		r.t.Errorf("%s: the save step has run %d time(s), want %d", when, r.saves, saves)
	}
}

// refused requires the change to be refused for the spacing, in exactly
// the given words, with nothing saved and nothing live changed.
func (r *cadenceRig) refused(change map[string]any, want string) {
	r.t.Helper()
	c0, s0 := r.live()
	saves0 := r.saves
	st, err := r.a.applyConfigChangeWith(change, r.publish)
	if err == nil {
		c, s := r.live()
		r.t.Fatalf("the change %v was accepted: live is cadence %d, spacing %d", change, c, s)
	}
	if st != nil {
		r.t.Errorf("the refusal returned a state")
	}
	var cr *CognitionRefusal
	if !errors.As(err, &cr) {
		r.t.Fatalf("the refusal is not the table's: %v", err)
	}
	if cr.Key != spacingKey {
		r.t.Errorf("the refusal names %q, want %q", cr.Key, spacingKey)
	}
	if cr.Requirement != want {
		r.t.Errorf("the refusal reads\n  %q\nwant\n  %q", cr.Requirement, want)
	}
	r.expect("after the refusal", c0, s0, saves0)
}

// accepted requires the change to be taken, saved once and live.
func (r *cadenceRig) accepted(change map[string]any, cadence, spacing int) {
	r.t.Helper()
	saves0 := r.saves
	st, err := r.a.applyConfigChangeWith(change, r.publish)
	if err != nil {
		r.t.Fatalf("the change %v was refused: %v", change, err)
	}
	if st == nil {
		r.t.Errorf("no state came back beside a nil error")
	}
	r.expect("after the accepted change", cadence, spacing, saves0+1)
}

func TestTheSpacingRuleIsAskedOnceOfTheWholeCandidate(t *testing.T) {
	// The cadence rises past the spacing the same change sends. Against
	// the cadence it started from (600) the spacing (900) is long enough;
	// against the one this change set it is not, and the sentence names
	// 1000 and the default that follows 1000.
	t.Run("a pair the same change makes invalid is refused in its own numbers", func(t *testing.T) {
		r := newCadenceRig(t, 600, 0)
		r.refused(map[string]any{cadenceKey: float64(1000), spacingKey: float64(900)}, spacingRefusal(900, 1000, 3000))
	})

	// The cadence falls under the spacing the same change sends. Against
	// the cadence it started from (600) the spacing (500) is too short.
	t.Run("a pair valid only with the cadence the same change set is accepted", func(t *testing.T) {
		r := newCadenceRig(t, 600, 0)
		r.accepted(map[string]any{cadenceKey: float64(100), spacingKey: float64(500)}, 100, 500)
	})

	// The cadence sorts ahead of the spacing, so the candidate is invalid
	// between the two keys (cadence 800, spacing still 500) and valid
	// once both have landed.
	t.Run("a pair that passes through an invalid candidate is accepted", func(t *testing.T) {
		r := newCadenceRig(t, 100, 500)
		r.accepted(map[string]any{cadenceKey: float64(800), spacingKey: float64(2500)}, 800, 2500)
	})

	// The rule holds the whole candidate, not only the keys a change
	// names: the refusal is the spacing's though only the cadence was sent.
	t.Run("a cadence alone is held against the stored spacing", func(t *testing.T) {
		r := newCadenceRig(t, 100, 500)
		r.refused(map[string]any{cadenceKey: float64(600)}, spacingRefusal(500, 600, 1800))
	})

	// What a zero follows has a floor of thirty minutes. Every refusal
	// above is at a cadence of 600 or more, where three ticks of the
	// cadence is the larger figure; at 200 three ticks is 600, and the
	// advice must still read 1800.
	t.Run("below a cadence of 600 the advice names the thirty-minute floor", func(t *testing.T) {
		r := newCadenceRig(t, 100, 500)
		r.refused(map[string]any{cadenceKey: float64(200), spacingKey: float64(150)}, spacingRefusal(150, 200, 1800))
	})

	// Zero is not a spacing a tick can meet: it follows the cadence.
	t.Run("a zero spacing follows whatever cadence the same change set", func(t *testing.T) {
		r := newCadenceRig(t, 100, 500)
		r.accepted(map[string]any{cadenceKey: float64(3000), spacingKey: float64(0)}, 3000, 0)
	})
}
