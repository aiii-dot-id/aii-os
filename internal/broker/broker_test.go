package broker

// The hostile battery for the three-ring lattice, the RING4 kv
// namespace, the receipt plane, secrets, and egress — every denial's
// reasonCode asserted against the adopted C vocabulary. The broker is
// driven straight through Dispatch here (the worker E2E rides in
// worker_e2e_test.go and the pluginhost suite).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

const testEnvelopeHost = "api.example.test"

func newStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func newHost(t *testing.T, st *store.Store, cfg Config) *Host {
	t.Helper()
	cfg.Store = st
	h, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// guardFor admits exactly the fixture server's URL prefix and defers
// everything else to the REAL guard — tests reach their loopback
// server without ever widening the egress policy for anything else.
func guardFor(ts *httptest.Server) func(string) error {
	return func(rawURL string) error {
		if strings.HasPrefix(rawURL, ts.URL) {
			return nil
		}
		return tools.FetchGuard(rawURL)
	}
}

func dispatch(t *testing.T, b *Binding, params string) map[string]json.RawMessage {
	t.Helper()
	reply, err := b.Dispatch(context.Background(), "invoke-call", []byte(params))
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(reply, &m); err != nil {
		t.Fatalf("reply is not a JSON object: %v\n%s", err, reply)
	}
	return m
}

// wantErrorReason asserts the audited error-object denial shape:
// {"code":-32000,"data":{"reasonCode":..., "denied_at":"capability_evaluation"}}.
func wantErrorReason(t *testing.T, m map[string]json.RawMessage, reason string) {
	t.Helper()
	var code int
	if err := json.Unmarshal(m["code"], &code); err != nil || code != -32000 {
		t.Fatalf("denial must be code -32000, got %s", m["code"])
	}
	var data struct {
		ReasonCode string `json:"reasonCode"`
		DeniedAt   string `json:"denied_at"`
	}
	if err := json.Unmarshal(m["data"], &data); err != nil {
		t.Fatalf("denial data missing: %v", err)
	}
	if data.ReasonCode != reason {
		t.Fatalf("reasonCode = %q, want %q", data.ReasonCode, reason)
	}
	if data.DeniedAt != deniedAtCapEval {
		t.Fatalf("denied_at = %q, want %q", data.DeniedAt, deniedAtCapEval)
	}
}

// wantResult asserts a result-path reply: status + (on failure) the
// same reason code in all three audited spellings, and a host-authored
// injected receipt.
func wantResult(t *testing.T, m map[string]json.RawMessage, status, reason string) map[string]json.RawMessage {
	t.Helper()
	var got struct {
		Status          string `json:"status"`
		Reason          string `json:"reason"`
		ReasonCode      string `json:"reasonCode"`
		ReasonCodeSnake string `json:"reason_code"`
	}
	raw, _ := json.Marshal(m)
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != status {
		t.Fatalf("status = %q, want %q (reply %s)", got.Status, status, raw)
	}
	if reason != "" && (got.Reason != reason || got.ReasonCode != reason || got.ReasonCodeSnake != reason) {
		t.Fatalf("reason spellings = %q/%q/%q, want all %q", got.Reason, got.ReasonCode, got.ReasonCodeSnake, reason)
	}
	var rec map[string]json.RawMessage
	if err := json.Unmarshal(m["external_receipt"], &rec); err != nil {
		t.Fatalf("every result must carry the injected host-authored receipt: %v", err)
	}
	if string(rec["host_authored"]) != "true" {
		t.Fatalf("receipt must be host_authored, got %s", m["external_receipt"])
	}
	return rec
}

func netParams(rawURL string, extraArgs string) string {
	args := "{}"
	if extraArgs != "" {
		args = extraArgs
	}
	return fmt.Sprintf(`{"operation":"http.get","target":{"url":%q},"arguments":%s}`, rawURL, args)
}

// --- the lattice ---

// The tier ceiling, from contract data: an unsigned T0 plugin holds no
// network — even with an operator grant AND an envelope naming the
// host. The outer ring cannot be widened from below.
func TestTierCeilingDeniesT0NetDespiteGrant(t *testing.T) {
	st := newStore(t)
	h := newHost(t, st, Config{Grants: map[string]Grant{"p": {Hosts: []string{testEnvelopeHost}}}})
	b := h.Bind("p", packagefmt.TierT0, []string{"net.outbound:" + testEnvelopeHost})
	m := dispatch(t, b, netParams("https://"+testEnvelopeHost+"/x", ""))
	wantErrorReason(t, m, reasonTierDenied)
	assertNoReceipts(t, st, "p")
}

// The envelope ring: a T1 plugin with an operator grant for a host the
// SIGNED manifest never requested is denied — the publisher's own
// declaration is a ceiling too.
func TestEnvelopeDeniesHostOutsideSignedSurface(t *testing.T) {
	st := newStore(t)
	h := newHost(t, st, Config{Grants: map[string]Grant{"p": {Hosts: []string{"other.example.test"}}}})
	b := h.Bind("p", packagefmt.TierT1, []string{"net.outbound:" + testEnvelopeHost})
	m := dispatch(t, b, netParams("https://other.example.test/x", ""))
	wantErrorReason(t, m, reasonNotInEnvelope)
	assertNoReceipts(t, st, "p")
}

// The operator ring: envelope-declared but ungranted is denied with
// the audited POLICY_DENY — the operator's silence is the policy.
func TestGrantRingDeniesUngrantedHost(t *testing.T) {
	st := newStore(t)
	h := newHost(t, st, Config{Grants: map[string]Grant{"p": {KV: true}}}) // an entry exists; hosts do not
	b := h.Bind("p", packagefmt.TierT1, []string{"net.outbound:" + testEnvelopeHost})
	m := dispatch(t, b, netParams("https://"+testEnvelopeHost+"/x", ""))
	wantErrorReason(t, m, reasonPolicyDeny)
	assertNoReceipts(t, st, "p")
}

// All three rings open → the broker performs the request, writes
// exactly one host-authored receipt, injects it, and feeds the fetch
// observation seam.
func TestAllRingsOpenPerformsWithReceiptAndObservation(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"ok":true}`)
	}))
	defer ts.Close()
	host, port := tsHostPort(t, ts)

	var observed []string
	st := newStore(t)
	h := newHost(t, st, Config{
		Grants:       map[string]Grant{"p": {Hosts: []string{fmt.Sprintf("%s:%d", host, port)}}},
		Guard:        guardFor(ts),
		ObserveFetch: func(u string) { observed = append(observed, u) },
	})
	b := h.Bind("p", packagefmt.TierT1, []string{fmt.Sprintf("net.outbound:%s:%d", host, port)})

	target := ts.URL + "/data"
	m := dispatch(t, b, netParams(target, ""))
	rec := wantResult(t, m, statusSucceeded, "")
	var or struct {
		HTTPStatus  int             `json:"http_status"`
		ContentType string          `json:"content_type"`
		Body        json.RawMessage `json:"body"`
	}
	if err := json.Unmarshal(m["operation_result"], &or); err != nil {
		t.Fatal(err)
	}
	if or.HTTPStatus != 200 || !strings.Contains(or.ContentType, "application/json") || string(or.Body) != `{"ok":true}` {
		t.Fatalf("operation_result drifted from the audited shape: %s", m["operation_result"])
	}
	if string(rec["protocol_status"]) != "200" || string(rec["transport_outcome"]) != "true" {
		t.Fatalf("receipt must record the real transport: %s", m["external_receipt"])
	}

	recs, err := st.PluginReceipts("p")
	if err != nil || len(recs) != 1 {
		t.Fatalf("exactly one receipt row, got %d (%v)", len(recs), err)
	}
	if !recs[0].Success || recs[0].Operation != "http.get" || recs[0].Target != target {
		t.Fatalf("receipt row must match the effect: %+v", recs[0])
	}
	if len(observed) != 1 || observed[0] != target {
		t.Fatalf("the fetch must feed the observation seam (H3), got %v", observed)
	}
}

// The guard outranks the grant: a loopback target stays blocked under
// the REAL egress policy even with every ring open, and the denial is
// a result carrying a non-success receipt.
func TestGuardWinsOverGrant(t *testing.T) {
	st := newStore(t)
	h := newHost(t, st, Config{Grants: map[string]Grant{"p": {Hosts: []string{"127.0.0.1:80", "localhost:80"}}}})
	b := h.Bind("p", packagefmt.TierT1, []string{"net.outbound:127.0.0.1:80", "net.outbound:localhost:80"})
	for _, target := range []string{"http://127.0.0.1/latest/meta-data", "http://localhost/admin"} {
		m := dispatch(t, b, netParams(target, ""))
		rec := wantResult(t, m, statusDenied, reasonPolicyDeny)
		var detail string
		_ = json.Unmarshal(rec["detail"], &detail)
		if !strings.Contains(detail, "egress guard") {
			t.Fatalf("the receipt must name the guard, got %q", detail)
		}
	}
	recs, _ := st.PluginReceipts("p")
	for _, r := range recs {
		if r.Success {
			t.Fatalf("a guard denial must never mint a success receipt: %+v", r)
		}
	}
}

// A redirect to a private range is blocked MID-FLIGHT: the first hop is
// admitted, the second is the guard's.
func TestRedirectToPrivateRangeBlockedMidflight(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data", http.StatusFound)
	}))
	defer ts.Close()
	host, port := tsHostPort(t, ts)

	st := newStore(t)
	h := newHost(t, st, Config{
		Grants: map[string]Grant{"p": {Hosts: []string{fmt.Sprintf("%s:%d", host, port)}}},
		Guard:  guardFor(ts),
	})
	b := h.Bind("p", packagefmt.TierT1, []string{fmt.Sprintf("net.outbound:%s:%d", host, port)})
	m := dispatch(t, b, netParams(ts.URL+"/hop", ""))
	rec := wantResult(t, m, statusDenied, reasonPolicyDeny)
	var detail string
	_ = json.Unmarshal(rec["detail"], &detail)
	if !strings.Contains(detail, "redirect blocked") {
		t.Fatalf("the mid-flight guard block must be named, got %q", detail)
	}
}

// Response and argument ceilings: over-budget bodies trap as the
// audited NET_RESPONSE_TOO_LARGE; unknown arguments and over-max
// timeouts fail closed with their C codes.
func TestNetCeilingsAndArguments(t *testing.T) {
	big := strings.Repeat("x", 4096)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, big)
	}))
	defer ts.Close()
	host, port := tsHostPort(t, ts)

	st := newStore(t)
	h := newHost(t, st, Config{
		Grants:           map[string]Grant{"p": {Hosts: []string{fmt.Sprintf("%s:%d", host, port)}}},
		Guard:            guardFor(ts),
		MaxResponseBytes: 1024,
	})
	b := h.Bind("p", packagefmt.TierT1, []string{fmt.Sprintf("net.outbound:%s:%d", host, port)})

	m := dispatch(t, b, netParams(ts.URL, ""))
	wantResult(t, m, statusFailed, reasonNetResponseTooBig)

	m = dispatch(t, b, netParams(ts.URL, `{"follow_redirects":true}`))
	wantResult(t, m, statusDenied, reasonNetUnknownArgument)

	m = dispatch(t, b, netParams(ts.URL, `{"timeout_ms":600000}`))
	wantResult(t, m, statusDenied, reasonNetTimeoutLimit)

	m = dispatch(t, b, `{"operation":"http.get","target":{"url":"not a url"}}`)
	wantResult(t, m, statusDenied, reasonTargetInvalid)
}

// --- params contract (verbatim daemon messages) ---

func TestParamsContract(t *testing.T) {
	st := newStore(t)
	h := newHost(t, st, Config{Grants: map[string]Grant{"p": {KV: true}}})
	b := h.Bind("p", packagefmt.TierT0, []string{"ring4.kv"})

	cases := []struct {
		params  string
		message string
	}{
		{`{}`, "operation (string) required"},
		{`{"operation":"kv.get","grant":{"anything":1}}`, "grant is retired"},
		{`{"operation":"kv.get","work_done_token":"tok"}`, "work_done_token requires negotiated rpc.cancel"},
	}
	for _, c := range cases {
		m := dispatch(t, b, c.params)
		var code int
		var msg string
		json.Unmarshal(m["code"], &code)
		json.Unmarshal(m["message"], &msg)
		if code != -32602 || !strings.Contains(msg, c.message) {
			t.Fatalf("params %s: got code %d message %q, want -32602 %q", c.params, code, msg, c.message)
		}
	}

	// Outside the operation registry → the audited capability denial.
	m := dispatch(t, b, `{"operation":"process.run","target":{},"arguments":{}}`)
	wantErrorReason(t, m, reasonOpNotAllowed)

	// Non-invoke methods have no step-4 surface.
	reply, err := b.Dispatch(context.Background(), "observe-subscribe", []byte(`{}`))
	if err != nil || !bytes.Contains(reply, []byte(reasonPolicyDeny)) {
		t.Fatalf("non-invoke methods must deny POLICY_DENY, got %s (%v)", reply, err)
	}
}

// --- binding admission ---

// DENY-ALL IS A GRANT, NOT A MISSING BINDING. Bind used to return nil
// for a plugin with no grants entry, which made "granted nothing" a
// different SHAPE rather than a different answer — a nil at every call
// site, and a plugin that could never be granted anything later without
// being rebound. It binds, and every ring denies.
func TestAnUngrantedPluginBindsAndIsDeniedEverything(t *testing.T) {
	st := newStore(t)
	h := newHost(t, st, Config{Grants: map[string]Grant{"granted": {}}})

	b := h.Bind("ungranted", packagefmt.TierT1, []string{"net.outbound:" + testEnvelopeHost})
	if b == nil {
		t.Fatal("a verified activation got no binding; there is nothing to grant later")
	}
	m := dispatch(t, b, netParams("https://"+testEnvelopeHost+"/x", ""))
	wantErrorReason(t, m, reasonPolicyDeny)

	if b := h.Bind("granted", packagefmt.TierT1, nil); b == nil {
		t.Fatal("an explicitly listed plugin must bind")
	}
	var none *Host
	if b := none.Bind("granted", packagefmt.TierT1, nil); b != nil {
		t.Fatal("a nil host binds nothing")
	}
}

// --- RING4 kv ---

func kvParams(op, key, value string) string {
	if value == "" {
		return fmt.Sprintf(`{"operation":%q,"target":{"key":%q},"arguments":{}}`, op, key)
	}
	return fmt.Sprintf(`{"operation":%q,"target":{"key":%q},"arguments":{"value":%q}}`, op, key, value)
}

func TestKVDeniedWithoutOperatorGrant(t *testing.T) {
	st := newStore(t)
	h := newHost(t, st, Config{Grants: map[string]Grant{"p": {Hosts: []string{"x"}}}}) // entry, but no kv
	b := h.Bind("p", packagefmt.TierT1, []string{"ring4.kv"})
	m := dispatch(t, b, kvParams("kv.put", "k", "v"))
	wantErrorReason(t, m, reasonPolicyDeny)
	assertNoReceipts(t, st, "p")
}

// Structural scoping: the namespace comes from the BINDING, so plugin B
// can neither read nor delete plugin A's keys — same store, same host.
func TestKVNamespaceIsStructurallyScoped(t *testing.T) {
	st := newStore(t)
	h := newHost(t, st, Config{Grants: map[string]Grant{"a": {KV: true}, "b": {KV: true}}})
	a := h.Bind("a", packagefmt.TierT1, []string{"ring4.kv"})
	b := h.Bind("b", packagefmt.TierT1, []string{"ring4.kv"})

	wantResult(t, dispatch(t, a, kvParams("kv.put", "shared-name", "a-secret-state")), statusSucceeded, "")
	wantResult(t, dispatch(t, b, kvParams("kv.get", "shared-name", "")), statusFailed, reasonKVNotFound)

	m := dispatch(t, b, kvParams("kv.delete", "shared-name", ""))
	wantResult(t, m, statusSucceeded, "")
	var del struct {
		Deleted bool `json:"deleted"`
	}
	json.Unmarshal(m["operation_result"], &del)
	if del.Deleted {
		t.Fatal("plugin b must not be able to delete a's key")
	}

	m = dispatch(t, a, kvParams("kv.get", "shared-name", ""))
	var got struct {
		Value string `json:"value"`
	}
	json.Unmarshal(m["operation_result"], &got)
	if got.Value != "a-secret-state" {
		t.Fatalf("a's key must survive b's attempts, got %q", got.Value)
	}
}

// The tier decides the scope's lifetime: T0 rows die with the binding
// (Close), T1 rows persist across activations.
func TestKVTempClearedAtCloseWhilePersistentSurvives(t *testing.T) {
	st := newStore(t)
	h := newHost(t, st, Config{Grants: map[string]Grant{"t0": {KV: true}, "t1": {KV: true}}})

	b0 := h.Bind("t0", packagefmt.TierT0, []string{"ring4.kv"})
	b1 := h.Bind("t1", packagefmt.TierT1, []string{"ring4.kv"})
	var scope struct {
		Scope string `json:"scope"`
	}
	m := dispatch(t, b0, kvParams("kv.put", "k", "ephemeral"))
	json.Unmarshal(m["operation_result"], &scope)
	if scope.Scope != "temp" {
		t.Fatalf("T0 storage must be temp-scoped, got %q", scope.Scope)
	}
	m = dispatch(t, b1, kvParams("kv.put", "k", "durable"))
	json.Unmarshal(m["operation_result"], &scope)
	if scope.Scope != "persistent" {
		t.Fatalf("T1 storage must persist, got %q", scope.Scope)
	}

	if err := b0.Close(); err != nil {
		t.Fatal(err)
	}
	if err := b1.Close(); err != nil {
		t.Fatal(err)
	}

	// Re-activation: T0's row is gone, T1's survives.
	b0 = h.Bind("t0", packagefmt.TierT0, []string{"ring4.kv"})
	b1 = h.Bind("t1", packagefmt.TierT1, []string{"ring4.kv"})
	wantResult(t, dispatch(t, b0, kvParams("kv.get", "k", "")), statusFailed, reasonKVNotFound)
	m = dispatch(t, b1, kvParams("kv.get", "k", ""))
	var got struct {
		Value string `json:"value"`
	}
	json.Unmarshal(m["operation_result"], &got)
	if got.Value != "durable" {
		t.Fatalf("T1 storage must survive deactivation, got %q", got.Value)
	}
}

func TestKVQuotasTrapAtCeiling(t *testing.T) {
	st := newStore(t)
	h := newHost(t, st, Config{
		Grants:          map[string]Grant{"p": {KV: true}},
		MaxKVValueBytes: 8, MaxKVKeys: 2, MaxKVTotalBytes: 64, MaxKVKeyBytes: 16,
	})
	b := h.Bind("p", packagefmt.TierT1, []string{"ring4.kv"})

	wantResult(t, dispatch(t, b, kvParams("kv.put", "k1", "12345678")), statusSucceeded, "")
	wantResult(t, dispatch(t, b, kvParams("kv.put", "k2", "123456789")), statusFailed, reasonKVValueTooLarge)
	wantResult(t, dispatch(t, b, kvParams("kv.put", "k2", "ok")), statusSucceeded, "")
	wantResult(t, dispatch(t, b, kvParams("kv.put", "k3", "over")), statusFailed, reasonKVQuotaExceeded)
	// Replacing in place stays legal at the ceiling.
	wantResult(t, dispatch(t, b, kvParams("kv.put", "k1", "fresh")), statusSucceeded, "")
	// Key grammar.
	wantResult(t, dispatch(t, b, kvParams("kv.put", strings.Repeat("K", 17), "v")), statusDenied, reasonTargetInvalid)
	wantResult(t, dispatch(t, b, `{"operation":"kv.put","target":{},"arguments":{"value":"v"}}`), statusDenied, reasonTargetInvalid)
	wantResult(t, dispatch(t, b, `{"operation":"kv.put","target":{"key":"k"},"arguments":{}}`), statusDenied, reasonArgumentInvalid)
}

// --- receipts ---

func assertNoReceipts(t *testing.T, st *store.Store, pluginID string) {
	t.Helper()
	recs, err := st.PluginReceipts(pluginID)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 0 {
		t.Fatalf("a lattice denial performs nothing and mints nothing, got %d receipts", len(recs))
	}
}

// One result = one receipt row, success mirroring the outcome; lattice
// denials (error path) mint none.
func TestReceiptLedgerMatchesOutcomes(t *testing.T) {
	st := newStore(t)
	h := newHost(t, st, Config{Grants: map[string]Grant{"p": {KV: true}}})
	b := h.Bind("p", packagefmt.TierT1, []string{"ring4.kv"})

	dispatch(t, b, kvParams("kv.put", "k", "v"))                    // succeeded
	dispatch(t, b, kvParams("kv.get", "k", ""))                     // succeeded
	dispatch(t, b, kvParams("kv.get", "miss", ""))                  // failed KV_NOT_FOUND
	dispatch(t, b, netParams("https://"+testEnvelopeHost+"/x", "")) // error path: no row

	recs, err := st.PluginReceipts("p")
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 3 {
		t.Fatalf("three results → three receipts, got %d", len(recs))
	}
	wantSuccess := []bool{true, true, false}
	for i, r := range recs {
		if r.Success != wantSuccess[i] {
			t.Fatalf("receipt %d success = %v, want %v (%+v)", i, r.Success, wantSuccess[i], r)
		}
		var rec map[string]interface{}
		if err := json.Unmarshal(r.ReceiptJSON, &rec); err != nil || rec["host_authored"] != true {
			t.Fatalf("stored receipt must be host-authored JSON: %s", r.ReceiptJSON)
		}
	}
}

// --- secrets ---

// The full credential path: a review-proven plugin with the handle
// granted reaches its pinned host carrying Authorization — and the
// secret value appears in NO plugin-visible byte stream, NO receipt,
// NO log line, NO store row.
func TestSecretRidesRequestButNeverPluginVisibleBytes(t *testing.T) {
	const secret = "sk-fake-test-credential-000"
	var sawAuth string
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		fmt.Fprint(w, `{"authorized":true}`)
	}))
	defer ts.Close()
	host, port := tsHostPort(t, ts)

	t.Setenv("AII_TEST_BROKER_SECRET", secret)

	var logBuf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&logBuf)
	defer log.SetOutput(prev)

	st := newStore(t)
	h := newHost(t, st, Config{
		Grants: map[string]Grant{"p": {
			Hosts:             []string{fmt.Sprintf("%s:%d", host, port)},
			CredentialHandles: []string{"api"},
		}},
		AuthProfiles: map[string]AuthProfile{
			"api": {SecretEnv: "AII_TEST_BROKER_SECRET", Host: host, Port: port},
		},
		Guard:     guardFor(ts),
		Transport: ts.Client().Transport,
	})
	b := h.Bind("p", packagefmt.TierT2, []string{fmt.Sprintf("net.outbound:%s:%d", host, port)})

	reply, err := b.Dispatch(context.Background(), "invoke-call",
		[]byte(netParams(ts.URL+"/v1", `{"auth_profile":"api"}`)))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(reply, &m); err != nil {
		t.Fatal(err)
	}
	wantResult(t, m, statusSucceeded, "")

	if sawAuth != "Bearer "+secret {
		t.Fatalf("the granted host must receive the Bearer credential, got %q", sawAuth)
	}
	// The value must be nowhere the plugin (or the audit trail) can see.
	if bytes.Contains(reply, []byte(secret)) {
		t.Fatal("SECRET LEAK: credential in the plugin-visible reply")
	}
	recs, _ := st.PluginReceipts("p")
	for _, r := range recs {
		if bytes.Contains(r.ReceiptJSON, []byte(secret)) {
			t.Fatal("SECRET LEAK: credential in a stored receipt")
		}
	}
	if strings.Contains(logBuf.String(), secret) {
		t.Fatal("SECRET LEAK: credential in the process log")
	}
}

// Every rung of the auth-profile ladder, each with its adopted code.
func TestAuthProfileLadderDenials(t *testing.T) {
	const secret = "sk-fake-ladder-secret"
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "{}")
	}))
	defer ts.Close()
	host, port := tsHostPort(t, ts)
	hostPort := fmt.Sprintf("%s:%d", host, port)
	t.Setenv("AII_TEST_LADDER_SECRET", secret)

	st := newStore(t)
	cfg := Config{
		Grants: map[string]Grant{"p": {
			Hosts:             []string{hostPort, "plain.example.test:80"},
			CredentialHandles: []string{"api", "wrongscope", "missing"},
		}},
		AuthProfiles: map[string]AuthProfile{
			"api":        {SecretEnv: "AII_TEST_LADDER_SECRET", Host: host, Port: port},
			"wrongscope": {SecretEnv: "AII_TEST_LADDER_SECRET", Host: "elsewhere.example.test", Port: 443},
			"missing":    {SecretEnv: "AII_TEST_LADDER_UNSET", Host: host, Port: port},
		},
		Guard:     guardFor(ts),
		Transport: ts.Client().Transport,
	}
	h := newHost(t, st, cfg)
	envelope := []string{"net.outbound:" + hostPort, "net.outbound:plain.example.test:80"}

	// The C tier floor (operations.c:851-855): T1 is publisher-proven
	// but not review-proven — auth profiles refuse.
	b1 := h.Bind("p", packagefmt.TierT1, envelope)
	m := dispatch(t, b1, netParams(ts.URL, `{"auth_profile":"api"}`))
	wantResult(t, m, statusDenied, reasonAuthNotAdmitted)

	b2 := h.Bind("p", packagefmt.TierT2, envelope)
	// Un-granted handle.
	m = dispatch(t, b2, netParams(ts.URL, `{"auth_profile":"other"}`))
	wantResult(t, m, statusDenied, reasonAuthNotAdmitted)
	// Cleartext target.
	m = dispatch(t, b2, netParams("http://plain.example.test/x", `{"auth_profile":"api"}`))
	wantResult(t, m, statusDenied, reasonAuthRequiresHTTPS)
	// Profile pinned elsewhere.
	m = dispatch(t, b2, netParams(ts.URL, `{"auth_profile":"wrongscope"}`))
	wantResult(t, m, statusDenied, reasonAuthScopeMismatch)
	// Secret source empty.
	m = dispatch(t, b2, netParams(ts.URL, `{"auth_profile":"missing"}`))
	wantResult(t, m, statusDenied, reasonAuthSecretMissing)

	// No denial performed a request, no receipt claims success.
	recs, _ := st.PluginReceipts("p")
	for _, r := range recs {
		if r.Success {
			t.Fatalf("auth-ladder denials must not mint success receipts: %+v", r)
		}
	}
}

func tsHostPort(t *testing.T, ts *httptest.Server) (string, int) {
	t.Helper()
	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	var port int
	fmt.Sscanf(u.Port(), "%d", &port)
	return u.Hostname(), port
}

// TestKVDeniedWhenUndeclared pins the envelope ring that joined once
// ring4.kv existed (operator-approved 2026-08-19): an operator grant
// without the signed manifest declaration is denied at the ENVELOPE
// ring with its own reason code — the manifest asks, the grant allows,
// and BOTH are required, exactly as net.outbound has always worked.
func TestKVDeniedWhenUndeclared(t *testing.T) {
	st := newStore(t)
	h := newHost(t, st, Config{Grants: map[string]Grant{"p": {KV: true}}})
	b := h.Bind("p", packagefmt.TierT1, nil) // granted, but nothing declared
	reply, err := b.Dispatch(context.Background(), "invoke-call",
		[]byte(`{"operation":"kv.put","target":{"key":"k"},"arguments":{"value":"v"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(reply, []byte(`"CAPABILITY_NOT_IN_STATIC_ENVELOPE"`)) {
		t.Fatalf("undeclared kv must deny at the envelope ring, got %s", reply)
	}
}
