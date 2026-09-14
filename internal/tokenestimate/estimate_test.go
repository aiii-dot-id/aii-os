package tokenestimate

import "testing"

func TestEstimateRoundsUpUTF8Bytes(t *testing.T) {
	cases := map[string]int{
		"":     0,
		"a":    1,
		"abc":  1,
		"abcd": 2,
		"世界":   2,
	}
	for text, want := range cases {
		if got := Estimate(text); got != want {
			t.Errorf("Estimate(%q) = %d, want %d", text, got, want)
		}
	}
}
