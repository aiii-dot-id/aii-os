package publicname

import "testing"

// The certificate service's address, and its port when one is exposed,
// are configuration; the defaults are the deployment at
// https://certificate.aiii.id (nginx on 443 in front of the service's
// 9185) and the production ACME directory, and a file without the
// block gets them.
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
