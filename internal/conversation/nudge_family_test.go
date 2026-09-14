package conversation

import "testing"

// .
// .
// .
func TestTheFamilySwitchOutranksTheMemberKey(t *testing.T) {
	yes, no := true, false
	for _, c := range []struct {
		name   string
		family bool
		member *bool
		want   bool
	}{
		{"family off, member unset", false, nil, false},
		{"family off, member explicitly on", false, &yes, false},
		{"family on, member unset (absent = on)", true, nil, true},
		{"family on, member explicitly off", true, &no, false},
		{"family on, member explicitly on", true, &yes, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := breadthNudgeOn(Config{HeuristicNudges: c.family, BreadthNudge: c.member})
			if got != c.want {
				t.Errorf("breadthNudgeOn = %v, want %v", got, c.want)
			}
		})
	}
}
