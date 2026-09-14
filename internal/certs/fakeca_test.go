package certs

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// .
// .
// .
// .
// .
// .
// .
// .

type fakeCA struct {
	t      *testing.T
	srv    *httptest.Server
	caCert *x509.Certificate
	caKey  *ecdsa.PrivateKey
	pub    *stubPublisher

	mu          sync.Mutex
	thumbprints map[string]string
	orders      map[string]*fakeOrder
	authzs      map[string]*fakeAuthz
	certs       map[string][]byte
	issued      int
	window      func(leaf *x509.Certificate) (start, end time.Time)
	lifetime    time.Duration
}

type fakeOrder struct {
	status   string
	name     string
	authz    string
	certURL  string
	account  string
	finalize string
}

type fakeAuthz struct {
	status  string
	name    string
	token   string
	account string
	chalURL string
}

func newFakeCA(t *testing.T, pub *stubPublisher) *fakeCA {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fake root"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
		SubjectKeyId: []byte{1, 2, 3, 4, 5, 6, 7, 8},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, _ := x509.ParseCertificate(der)
	ca := &fakeCA{t: t, caCert: caCert, caKey: caKey, pub: pub,
		thumbprints: map[string]string{}, orders: map[string]*fakeOrder{}, authzs: map[string]*fakeAuthz{}, certs: map[string][]byte{},
		lifetime: 90 * 24 * time.Hour}
	mux := http.NewServeMux()
	mux.HandleFunc("/dir", ca.directory)
	mux.HandleFunc("/new-nonce", ca.nonce)
	mux.HandleFunc("/new-acct", ca.newAccount)
	mux.HandleFunc("/new-order", ca.newOrder)
	mux.HandleFunc("/order/", ca.order)
	mux.HandleFunc("/authz/", ca.authz)
	mux.HandleFunc("/chal/", ca.challenge)
	mux.HandleFunc("/finalize/", ca.finalize)
	mux.HandleFunc("/cert/", ca.cert)
	mux.HandleFunc("/renewal-info/", ca.renewalInfo)
	ca.srv = httptest.NewServer(mux)
	t.Cleanup(ca.srv.Close)
	return ca
}

func (ca *fakeCA) url(p string) string { return ca.srv.URL + p }

func (ca *fakeCA) directory(w http.ResponseWriter, r *http.Request) {
	d := map[string]string{
		"newNonce": ca.url("/new-nonce"), "newAccount": ca.url("/new-acct"), "newOrder": ca.url("/new-order"),
		"revokeCert": ca.url("/revoke"), "keyChange": ca.url("/key-change"),
	}
	if ca.window != nil {
		d["renewalInfo"] = ca.url("/renewal-info")
	}
	writeJSON(w, http.StatusOK, d)
}

func (ca *fakeCA) nonce(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Replay-Nonce", caNonce())
	w.WriteHeader(http.StatusNoContent)
}

func caNonce() string {
	b := make([]byte, 16)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// .
type jws struct {
	Protected struct {
		Alg   string          `json:"alg"`
		Nonce string          `json:"nonce"`
		URL   string          `json:"url"`
		KID   string          `json:"kid"`
		JWK   json.RawMessage `json:"jwk"`
	}
	Payload []byte
}

func (ca *fakeCA) parse(r *http.Request) (*jws, error) {
	var env struct {
		Protected string `json:"protected"`
		Payload   string `json:"payload"`
		Signature string `json:"signature"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&env); err != nil {
		return nil, err
	}
	out := &jws{}
	ph, err := base64.RawURLEncoding.DecodeString(env.Protected)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(ph, &out.Protected); err != nil {
		return nil, err
	}
	if env.Payload != "" {
		out.Payload, err = base64.RawURLEncoding.DecodeString(env.Payload)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// .
// .
func thumbprint(jwk json.RawMessage) (string, error) {
	var k struct {
		Crv string `json:"crv"`
		Kty string `json:"kty"`
		X   string `json:"x"`
		Y   string `json:"y"`
	}
	if err := json.Unmarshal(jwk, &k); err != nil {
		return "", err
	}
	canon := fmt.Sprintf(`{"crv":%q,"kty":%q,"x":%q,"y":%q}`, k.Crv, k.Kty, k.X, k.Y)
	sum := sha256.Sum256([]byte(canon))
	return base64.RawURLEncoding.EncodeToString(sum[:]), nil
}

func (ca *fakeCA) newAccount(w http.ResponseWriter, r *http.Request) {
	j, err := ca.parse(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	tp, err := thumbprint(j.Protected.JWK)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var body struct {
		OnlyReturnExisting bool `json:"onlyReturnExisting"`
	}
	_ = json.Unmarshal(j.Payload, &body)
	acct := ca.url("/acct/" + tp[:8])
	ca.mu.Lock()
	_, known := ca.thumbprints[acct]
	if !body.OnlyReturnExisting {
		ca.thumbprints[acct] = tp
	}
	ca.mu.Unlock()
	w.Header().Set("Replay-Nonce", caNonce())
	if body.OnlyReturnExisting && !known {
		writeJSON(w, http.StatusBadRequest, map[string]string{"type": "urn:ietf:params:acme:error:accountDoesNotExist", "detail": "no such account"})
		return
	}
	w.Header().Set("Location", acct)
	code := http.StatusCreated
	if known {
		code = http.StatusOK
	}
	writeJSON(w, code, map[string]interface{}{"status": "valid", "orders": ca.url("/orders")})
}

func (ca *fakeCA) newOrder(w http.ResponseWriter, r *http.Request) {
	j, err := ca.parse(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var body struct {
		Identifiers []struct{ Type, Value string } `json:"identifiers"`
	}
	if err := json.Unmarshal(j.Payload, &body); err != nil || len(body.Identifiers) != 1 {
		http.Error(w, "one identifier", http.StatusBadRequest)
		return
	}
	name := body.Identifiers[0].Value
	ca.mu.Lock()
	id := fmt.Sprintf("%d", len(ca.orders)+1)
	az := &fakeAuthz{status: "pending", name: name, token: caNonce(), account: j.Protected.KID}
	az.chalURL = ca.url("/chal/" + id)
	ca.authzs[id] = az
	o := &fakeOrder{status: "pending", name: name, authz: ca.url("/authz/" + id), account: j.Protected.KID, finalize: ca.url("/finalize/" + id)}
	ca.orders[id] = o
	ca.mu.Unlock()
	w.Header().Set("Replay-Nonce", caNonce())
	w.Header().Set("Location", ca.url("/order/"+id))
	writeJSON(w, http.StatusCreated, ca.orderJSON(o))
}

func (ca *fakeCA) orderJSON(o *fakeOrder) map[string]interface{} {
	m := map[string]interface{}{
		"status": o.status, "expires": time.Now().Add(time.Hour).Format(time.RFC3339),
		"identifiers":    []map[string]string{{"type": "dns", "value": o.name}},
		"authorizations": []string{o.authz}, "finalize": o.finalize,
	}
	if o.certURL != "" {
		m["certificate"] = o.certURL
	}
	return m
}

func (ca *fakeCA) order(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/order/")
	ca.mu.Lock()
	o := ca.orders[id]
	// .
	if o != nil && o.status == "pending" {
		if az := ca.authzs[id]; az != nil && az.status == "valid" {
			o.status = "ready"
		}
	}
	ca.mu.Unlock()
	if o == nil {
		http.Error(w, "no such order", http.StatusNotFound)
		return
	}
	w.Header().Set("Replay-Nonce", caNonce())
	writeJSON(w, http.StatusOK, ca.orderJSON(o))
}

func (ca *fakeCA) authzJSON(az *fakeAuthz) map[string]interface{} {
	return map[string]interface{}{
		"status":     az.status,
		"identifier": map[string]string{"type": "dns", "value": az.name},
		"challenges": []map[string]string{{"type": "dns-01", "url": az.chalURL, "token": az.token, "status": az.status}},
	}
}

func (ca *fakeCA) authz(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/authz/")
	ca.mu.Lock()
	az := ca.authzs[id]
	ca.mu.Unlock()
	if az == nil {
		http.Error(w, "no such authorization", http.StatusNotFound)
		return
	}
	w.Header().Set("Replay-Nonce", caNonce())
	writeJSON(w, http.StatusOK, ca.authzJSON(az))
}

// .
// .
// .
func (ca *fakeCA) challenge(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/chal/")
	j, err := ca.parse(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ca.mu.Lock()
	az := ca.authzs[id]
	tp := ca.thumbprints[j.Protected.KID]
	ca.mu.Unlock()
	if az == nil || tp == "" {
		http.Error(w, "unknown", http.StatusNotFound)
		return
	}
	keyAuth := az.token + "." + tp
	sum := sha256.Sum256([]byte(keyAuth))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	status := "invalid"
	if ca.pub.has("_acme-challenge."+az.name, want) {
		status = "valid"
	}
	ca.mu.Lock()
	az.status = status
	ca.mu.Unlock()
	w.Header().Set("Replay-Nonce", caNonce())
	writeJSON(w, http.StatusOK, map[string]string{"type": "dns-01", "url": az.chalURL, "token": az.token, "status": status})
}

func (ca *fakeCA) finalize(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/finalize/")
	j, err := ca.parse(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var body struct {
		CSR string `json:"csr"`
	}
	if err := json.Unmarshal(j.Payload, &body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	der, err := base64.RawURLEncoding.DecodeString(body.CSR)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	csr, err := x509.ParseCertificateRequest(der)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ca.mu.Lock()
	o := ca.orders[id]
	az := ca.authzs[id]
	if o == nil || az == nil || az.status != "valid" || len(csr.DNSNames) != 1 || csr.DNSNames[0] != o.name {
		ca.mu.Unlock()
		http.Error(w, "order not ready for this CSR", http.StatusForbidden)
		return
	}
	ca.issued++
	serial := big.NewInt(int64(1000 + ca.issued))
	tmpl := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: o.name}, DNSNames: []string{o.name},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(ca.lifetime),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		AuthorityKeyId: ca.caCert.SubjectKeyId,
	}
	leaf, err := x509.CreateCertificate(rand.Reader, tmpl, ca.caCert, csr.PublicKey, ca.caKey)
	if err != nil {
		ca.mu.Unlock()
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	o.certURL = ca.url("/cert/" + id)
	o.status = "valid"
	ca.certs[o.certURL] = leaf
	ca.mu.Unlock()
	w.Header().Set("Replay-Nonce", caNonce())
	writeJSON(w, http.StatusOK, ca.orderJSON(o))
}

func (ca *fakeCA) cert(w http.ResponseWriter, r *http.Request) {
	ca.mu.Lock()
	leaf := ca.certs[ca.url(r.URL.Path)]
	ca.mu.Unlock()
	if leaf == nil {
		http.Error(w, "no such certificate", http.StatusNotFound)
		return
	}
	w.Header().Set("Replay-Nonce", caNonce())
	w.Header().Set("Content-Type", "application/pem-certificate-chain")
	pem.Encode(w, &pem.Block{Type: "CERTIFICATE", Bytes: leaf})
	pem.Encode(w, &pem.Block{Type: "CERTIFICATE", Bytes: ca.caCert.Raw})
}

func (ca *fakeCA) renewalInfo(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/renewal-info/")
	ca.mu.Lock()
	var leaf *x509.Certificate
	for _, der := range ca.certs {
		c, _ := x509.ParseCertificate(der)
		if cid, _ := CertID(c); cid == id {
			leaf = c
		}
	}
	win := ca.window
	ca.mu.Unlock()
	if leaf == nil || win == nil {
		http.Error(w, "unknown certificate", http.StatusNotFound)
		return
	}
	start, end := win(leaf)
	writeJSON(w, http.StatusOK, map[string]interface{}{"suggestedWindow": map[string]string{"start": start.Format(time.RFC3339), "end": end.Format(time.RFC3339)}})
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// .
// .
// .
type stubPublisher struct {
	mu        sync.Mutex
	records   map[string]map[string]bool
	published int
	removed   int
}

func newStubPublisher() *stubPublisher { return &stubPublisher{records: map[string]map[string]bool{}} }

func (p *stubPublisher) Publish(_ context.Context, name, txt string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.records[name] == nil {
		p.records[name] = map[string]bool{}
	}
	p.records[name][txt] = true
	p.published++
	return nil
}

func (p *stubPublisher) Unpublish(_ context.Context, name, txt string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.records[name], txt)
	p.removed++
	return nil
}

func (p *stubPublisher) has(name, txt string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.records[name][txt]
}

func (p *stubPublisher) standing() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, vals := range p.records {
		n += len(vals)
	}
	return n
}
