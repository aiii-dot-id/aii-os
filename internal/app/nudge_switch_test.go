package app

import "testing"

// .
// .
// .
// .
// .
// .
func TestHeuristicNudgesAreOffUnlessTheOperatorSaysOtherwise(t *testing.T) {
	yes, no := true, false
	for _, c := range []struct {
		name string
		v    *bool
		want bool
	}{
		{"absent means OFF (the inversion)", nil, false},
		{"explicit false", &no, false},
		{"explicit true", &yes, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := heuristicNudgesOn(c.v); got != c.want {
				t.Errorf("heuristicNudgesOn = %v, want %v", got, c.want)
			}
		})
	}
	// .
	// .
	if !agencyOn(nil) {
		t.Error("agencyOn(nil) is false — the absent-key doctrine changed under the other flags too")
	}
	if !planNudgeEnabled(nil) {
		t.Error("planNudgeEnabled(nil) is false — plan_nudge lost its own doctrine")
	}
}

// .
func TestDefaultConfigDoesNotArmTheNudgeFamily(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)
	if heuristicNudgesOn(cfg.Agency.HeuristicNudges) {
		t.Error("a defaulted config arms behaviour-shaping prose — beta ships it off")
	}
}

// .
// .
// .
// .
// .
// .
// .
func TestTheProseSwitchNeverDisablesTheStructuralCap(t *testing.T) {
	off, on := false, true
	for _, c := range []struct {
		name     string
		v        *bool
		familyOn bool
	}{
		{"absent — the default, which is OFF", nil, false},
		{"explicit off", &off, false},
		{"on", &on, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := &App{}
			plan, fanout, predicted, calib := a.residentLoopHooks(Config{
				Agency: AgencyConfig{HeuristicNudges: c.v},
			})
			if predicted == nil {
				t.Fatal("the resident's loop always gets the prediction accessor — the declared-budget cap keys on it, and a prose switch must not remove a bound on the loop")
			}
			if c.familyOn {
				if plan == nil || fanout == nil || calib == nil {
					t.Fatal("family on: the planning-ask family must be wired together")
				}
				return
			}
			if plan != nil || fanout != nil || calib != nil {
				t.Fatalf("family off: no prose hooks may be wired (plan=%v fanout=%v calibration=%v)",
					plan != nil, fanout != nil, calib != nil)
			}
		})
	}
}
