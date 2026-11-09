package app

import "testing"

// THE TURN'S COST READS AS ENGLISH: its thousands apart and its nouns one or
// many, in the log and on Status ("18,400 tokens over 3 calls", "1 call").
func TestTheTurnsCostReadsAsEnglish(t *testing.T) {
	for n, want := range map[int]string{0: "0", 7: "7", 999: "999", 1000: "1,000", 18400: "18,400", 1945593: "1,945,593", -1200: "-1,200"} {
		if got := thousands(n); got != want {
			t.Errorf("thousands(%d) = %q, want %q", n, got, want)
		}
	}
	for _, c := range []struct {
		n    int
		want string
	}{{1, "1 call"}, {0, "0 calls"}, {3, "3 calls"}, {12000, "12,000 calls"}} {
		if got := counted(c.n, "call", "calls"); got != c.want {
			t.Errorf("counted(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}
