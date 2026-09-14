package version

import "testing"

func TestVersion(t *testing.T) {
	for _, v := range []string{"0.1.0", "1.0.0-rc.1", "1.0.0+build"} {
		if !Valid(v) {
			t.Errorf("Valid(%q) = false", v)
		}
	}
	for _, v := range []string{"", "dev", "v1.0.0", "1.0", "01.0.0"} {
		if Valid(v) {
			t.Errorf("Valid(%q) = true", v)
		}
	}
	if Compare("1.0.0-rc.1", "1.0.0") >= 0 || Compare("1.0.1", "1.0.0") <= 0 || Compare("1.0.0", "1.0.0") != 0 {
		t.Fatal("Compare does not preserve semantic-version order")
	}
}
