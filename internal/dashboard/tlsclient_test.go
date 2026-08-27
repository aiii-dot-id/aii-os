package dashboard

import (
	"crypto/tls"
	"net/http"
	"time"
)

// tlsclient_test.go — how the tests reach a dashboard that serves HTTPS
// only.
//
// The dashboard mints its own certificate from a per-machine local root,
// so a client that has not installed that root will not trust it. These
// tests are not testing trust — they are testing dashboard behaviour —
// so they skip verification and get on with it.
//
// tls_test.go is the exception and does the opposite: it pools the CA
// the server actually minted and requires a real chain to verify. That
// is where "the certificate is correct" is proven, once, properly.
// Everywhere else, permissive is the honest choice: a test asserting
// content types should fail for a wrong content type and not for a
// certificate.
var testClient = &http.Client{
	Timeout: 10 * time.Second,
	Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	},
}
