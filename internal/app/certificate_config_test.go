package app

import "testing"

// .
// .
// .
// .
// .
func TestCertificateConfigDefaults(t *testing.T) {
	var c CertificateConfig
	if c.serverURL() != "https://certificate.aiii.id" {
		t.Fatalf("default server URL %q", c.serverURL())
	}
	if c.acmeDirectory() != "https://acme-v02.api.letsencrypt.org/directory" {
		t.Fatalf("default ACME directory %q", c.acmeDirectory())
	}
	c = CertificateConfig{ServerURL: "https://certificate.example.test:9999", ACMEDirectory: "https://pebble.test/dir"}
	if c.serverURL() != "https://certificate.example.test:9999" || c.acmeDirectory() != "https://pebble.test/dir" {
		t.Fatal("a configured server URL or directory must win over the defaults")
	}
}
