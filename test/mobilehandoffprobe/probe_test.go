package mobilehandoffprobe

import "testing"

func TestCompanionCaseRequiresAnObservedPrivateAddress(t *testing.T) {
	for _, address := range []string{"", "localhost", "127.0.0.1", "::1", "0.0.0.0", "example.test", "192.0.2.1"} {
		p := Create()
		p.CompanionHost = address
		if _, err := p.StartCase(t.TempDir(), "companion"); err == nil {
			if serr := p.Stop(); serr != nil {
				t.Errorf("teardown: %v", serr)
			}
			t.Fatalf("unusable companion fixture address accepted: %q", address)
		}
	}
}
