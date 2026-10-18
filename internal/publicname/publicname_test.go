package publicname

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

func TestBootAdviceNamesTheTrustedURLAndNeverTheRootWhenANameIsHeld(t *testing.T) {
	mat := &dashboard.TLSMaterial{CACertPath: "/x/dashboard-ca.crt", Regenerated: true}
	issued := dashboard.PublicNameState{Status: StatusIssued, Origin: "https://ui.abc.example.test:8180"}
	for _, c := range []struct {
		name      string
		tlsOn     bool
		host      string
		st        dashboard.PublicNameState
		mat       *dashboard.TLSMaterial
		declined  bool
		wantLines int
		want      string
		never     string
	}{
		{"plain loopback", false, "127.0.0.1", dashboard.PublicNameState{Status: StatusNone}, nil, false, 0, "", "certutil"},
		{"name held", true, "10.0.0.5", issued, mat, false, 1, "https://ui.abc.example.test:8180", "certutil"},
		{"name held and resolving", true, "10.0.0.5", dashboard.PublicNameState{Status: StatusIssued, Origin: "https://ui.abc.example.test:8180", Address: "10.0.0.5", ResolvesTo: "10.0.0.5"}, mat, false, 1, "resolves to 10.0.0.5", "certutil"},
		{"name claiming", true, "10.0.0.5", dashboard.PublicNameState{Status: StatusClaiming, Origin: "https://ui.abc.example.test:8180"}, mat, false, 1, "being issued", "certutil"},
		{"no name, root just minted", true, "10.0.0.5", dashboard.PublicNameState{Status: StatusNone}, mat, false, 5, "certutil", ""},
		{"no name, root reused", true, "10.0.0.5", dashboard.PublicNameState{Status: StatusNone}, &dashboard.TLSMaterial{CACertPath: "/x/dashboard-ca.crt"}, false, 1, "not claimed yet", "certutil"},
		{"declined", true, "10.0.0.5", dashboard.PublicNameState{Status: StatusNone}, nil, true, 1, "server_url = none", "certutil"},
		{"tls on loopback", true, "127.0.0.1", dashboard.PublicNameState{Status: StatusNone}, nil, false, 1, "secure context", "certutil"},
	} {
		got := dashboardAdvice(c.tlsOn, c.host, c.st, c.mat, c.declined)
		joined := strings.Join(got, "\n")
		if len(got) != c.wantLines {
			t.Errorf("%s: %d line(s), want %d: %q", c.name, len(got), c.wantLines, joined)
		}
		if c.want != "" && !strings.Contains(joined, c.want) {
			t.Errorf("%s: missing %q in %q", c.name, c.want, joined)
		}
		if c.never != "" && strings.Contains(joined, c.never) {
			t.Errorf("%s: %q must not appear in %q", c.name, c.never, joined)
		}
	}
}
