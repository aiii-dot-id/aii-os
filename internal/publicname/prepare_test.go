package publicname

import "testing"

func TestWhichIdentitiesClaimTheirNameByThemselves(t *testing.T) {
	if !prepareWhenUnset {
		t.Fatal("the product's default for certificate.prepare is off in this package: only a test binary that boots identities may turn it off, for itself")
	}
	on, off := true, false
	for _, c := range []struct {
		what    string
		host    string
		tls     bool
		prepare *bool
		url     string
		atStart bool
		atBirth bool
	}{
		{"the ordinary install: loopback, plain", "127.0.0.1", false, nil, "", false, true},
		{"loopback with HTTPS on", "127.0.0.1", true, nil, "", false, true},
		{"a network bind", "192.0.2.6", true, nil, "", true, true},
		{"preparation set on", "127.0.0.1", false, &on, "", false, true},
		{"preparation off: loopback claims nothing", "127.0.0.1", false, &off, "", false, false},
		{"preparation off: a network bind still claims at a start, as before", "192.0.2.6", true, &off, "", true, false},
		{"the service declined", "127.0.0.1", false, nil, "none", false, false},
		{"the service declined, on a network bind", "192.0.2.6", true, &on, "none", false, false},
	} {
		cfg := Settings{Host: c.host, TLS: c.tls}
		cfg.Certificate.Prepare, cfg.Certificate.ServerURL = c.prepare, c.url
		if got := autoClaimApplies(cfg); got != c.atStart {
			t.Errorf("%s: claims by itself at a start %v, want %v", c.what, got, c.atStart)
		}
		if got := prepareApplies(cfg); got != c.atBirth {
			t.Errorf("%s: claims by itself at its birth %v, want %v", c.what, got, c.atBirth)
		}
	}
}

func TestACertificateIsWantedWhenTheNameLeadsSomewhere(t *testing.T) {
	vpn := []string{"10.8.0.2"}
	none := []string{}
	for _, c := range []struct {
		what   string
		change func(*Settings)
		want   bool
	}{
		{"a prepared loopback identity", func(*Settings) {}, false},
		{"loopback with HTTPS on: its operator's bind answers for the name", func(s *Settings) { s.TLS = true }, true},
		{"a network bind serves the name", func(s *Settings) { s.Host, s.TLS = "192.0.2.6", true }, true},
		{"a relay carries it", func(s *Settings) {
			s.Certificate.RouteMode, s.Certificate.RelayEndpoint = RouteRelay, "relay.example.test:443"
		}, true},
		{"the direct route has an address to publish", func(s *Settings) { s.Certificate.DirectAddresses = &vpn }, true},
		{"the direct route is named empty", func(s *Settings) { s.Certificate.DirectAddresses = &none }, false},
		{"the route is disabled", func(s *Settings) { s.Certificate.RouteMode = RouteDisabled }, false},
		{"disabled, and the bind serves the name", func(s *Settings) {
			s.Host, s.TLS, s.Certificate.RouteMode = "192.0.2.6", true, RouteDisabled
		}, true},
		{"a route mode nobody knows", func(s *Settings) { s.Certificate.RouteMode = "sideways" }, false},
		{"the operator asked for it ahead of time", func(s *Settings) { s.Certificate.PrepareCertificate = true }, true},
	} {
		cfg := Settings{Host: "127.0.0.1"}
		c.change(&cfg)
		if got := certificateWanted(cfg); got != c.want {
			t.Errorf("%s: a certificate is wanted %v, want %v", c.what, got, c.want)
		}
	}
}
