package certs

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"
	"time"
)

// .
// .
// .
// .
// .
// .
// .
type recordSetVector struct {
	name       string
	tag        string
	identityID string
	recordName string
	generation int64
	records    []Record
	nonce      string
	expiresAt  string
	canonical  string
	digest     string
	input      string
}

const vectorIdentity = "did:aiii:identity:sha256:0000000000000000000000000000000000000000000000000000000000000000"
const vectorName = "abcdefghijklmnopqrstuvwxyz.example.invalid"

// .
// .
const emptyRecordsDigest = "sha256:4f53cda18c2baa0c0354bb5f9a3ecbe5ed12ab4d8e11ba873c2f11161202b945"

func recordSetVectors() []recordSetVector {
	return []recordSetVector{
		{
			name: "replace_dual_stack", tag: recordSetReplaceTag,
			identityID: vectorIdentity, recordName: vectorName, generation: 7,
			records:   []Record{{Type: RecordA, Value: "192.0.2.2"}, {Type: RecordAAAA, Value: "fd00::2"}},
			nonce:     "guide-replace_dual_stack",
			expiresAt: "2026-09-13T12:00:00Z",
			canonical: `[{"rrtype":"A","value":"192.0.2.2"},{"rrtype":"AAAA","value":"fd00::2"}]`,
			digest:    "sha256:6470a72821a61e8abb37b80b3628ccf4963df1f13d59902419f2055b32a4bfff",
			input: "AIII-CERTD-RECORD-SET-V1\nidentity_id:" + vectorIdentity + "\nname:" + vectorName +
				"\nexpected_generation:7\nrecords_sha256:sha256:6470a72821a61e8abb37b80b3628ccf4963df1f13d59902419f2055b32a4bfff" +
				"\nnonce:guide-replace_dual_stack\nexpires_at:2026-09-13T12:00:00Z\n",
		},
		{
			name: "replace_relay", tag: recordSetReplaceTag,
			identityID: vectorIdentity, recordName: vectorName, generation: 8,
			records:   []Record{{Type: RecordCNAME, Value: "relay.example.invalid"}},
			nonce:     "guide-replace_relay",
			expiresAt: "2026-09-13T12:00:00Z",
			canonical: `[{"rrtype":"CNAME","value":"relay.example.invalid"}]`,
			digest:    "sha256:893dd444084d3dd919806ab60add08778897b2baa368a94773f1e3373febc391",
			input: "AIII-CERTD-RECORD-SET-V1\nidentity_id:" + vectorIdentity + "\nname:" + vectorName +
				"\nexpected_generation:8\nrecords_sha256:sha256:893dd444084d3dd919806ab60add08778897b2baa368a94773f1e3373febc391" +
				"\nnonce:guide-replace_relay\nexpires_at:2026-09-13T12:00:00Z\n",
		},
		{
			name: "replace_empty", tag: recordSetReplaceTag,
			identityID: vectorIdentity, recordName: vectorName, generation: 9,
			records:   []Record{},
			nonce:     "guide-replace_empty",
			expiresAt: "2026-09-13T12:00:00Z",
			canonical: `[]`,
			digest:    emptyRecordsDigest,
			input: "AIII-CERTD-RECORD-SET-V1\nidentity_id:" + vectorIdentity + "\nname:" + vectorName +
				"\nexpected_generation:9\nrecords_sha256:" + emptyRecordsDigest +
				"\nnonce:guide-replace_empty\nexpires_at:2026-09-13T12:00:00Z\n",
		},
		{
			// .
			// .
			// .
			name: "read_status", tag: recordSetStatusTag,
			identityID: vectorIdentity, recordName: vectorName, generation: 0,
			records:   nil,
			nonce:     "guide-read_status",
			expiresAt: "2026-09-13T12:00:00Z",
			canonical: `[]`,
			digest:    emptyRecordsDigest,
			input: "AIII-CERTD-RECORD-SET-STATUS-V1\nidentity_id:" + vectorIdentity + "\nname:" + vectorName +
				"\nexpected_generation:0\nrecords_sha256:" + emptyRecordsDigest +
				"\nnonce:guide-read_status\nexpires_at:2026-09-13T12:00:00Z\n",
		},
	}
}

func TestRecordSetSigningInputsMatchTheServiceVectors(t *testing.T) {
	for _, v := range recordSetVectors() {
		t.Run(v.name, func(t *testing.T) {
			raw, err := canonicalRecordsJSON(v.records)
			if err != nil {
				t.Fatal(err)
			}
			if string(raw) != v.canonical {
				t.Fatalf("canonical records\n got %s\nwant %s", raw, v.canonical)
			}
			digest, err := RecordsDigest(v.records)
			if err != nil {
				t.Fatal(err)
			}
			if digest != v.digest {
				t.Fatalf("records_sha256\n got %s\nwant %s", digest, v.digest)
			}
			got := string(RecordSetInput(v.tag, v.identityID, v.recordName, v.generation, digest, v.nonce, v.expiresAt))
			if got != v.input {
				t.Fatalf("signature input\n got %q\nwant %q", got, v.input)
			}
		})
	}
}

// .
// .
func TestAReadIsNotAReplacement(t *testing.T) {
	digest, err := RecordsDigest(nil)
	if err != nil {
		t.Fatal(err)
	}
	read := RecordSetInput(recordSetStatusTag, vectorIdentity, vectorName, 0, digest, "n", "2026-09-13T12:00:00Z")
	replace := RecordSetInput(recordSetReplaceTag, vectorIdentity, vectorName, 0, digest, "n", "2026-09-13T12:00:00Z")
	if string(read) == string(replace) {
		t.Fatal("a signed read and a signed replacement have the same input")
	}
}

func TestCanonicalRecordsIsTheOrderTheServiceVerifies(t *testing.T) {
	// .
	// .
	// .
	got, err := CanonicalRecords([]Record{
		{Type: RecordAAAA, Value: "fd00::2"},
		{Type: RecordA, Value: "192.0.2.3"},
		{Type: RecordA, Value: "192.0.2.2"},
		{Type: RecordA, Value: "192.0.2.2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []Record{
		{Type: RecordA, Value: "192.0.2.2"},
		{Type: RecordA, Value: "192.0.2.3"},
		{Type: RecordAAAA, Value: "fd00::2"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("member %d: got %v want %v", i, got[i], want[i])
		}
	}
	// .
	// .
	if _, err := CanonicalRecords([]Record{{Type: RecordAAAA, Value: "::ffff:10.0.0.1"}}); err == nil {
		t.Fatal("an IPv4-mapped address was accepted as AAAA")
	}
	if got, err := CanonicalRecords([]Record{{Type: RecordA, Value: "::ffff:10.0.0.1"}}); err != nil || len(got) != 1 || got[0].Value != "10.0.0.1" {
		t.Fatalf("IPv4-mapped as A: %v %v", got, err)
	}
	for _, bad := range []Record{
		{Type: RecordA, Value: "0.0.0.0"},
		{Type: RecordA, Value: "127.0.0.1"},
		{Type: RecordAAAA, Value: "::"},
		{Type: RecordAAAA, Value: "fe80::1"},
		{Type: "TXT", Value: "x"},
		{Type: RecordCNAME, Value: "relay.example.invalid:8180"},
		{Type: RecordCNAME, Value: "norelaydots"},
	} {
		if _, err := CanonicalRecords([]Record{bad}); err == nil {
			t.Fatalf("%v was accepted as a route", bad)
		}
	}
	// .
	if _, err := CanonicalRecords([]Record{
		{Type: RecordA, Value: "10.0.0.1"},
		{Type: RecordCNAME, Value: "relay.example.invalid"},
	}); err == nil {
		t.Fatal("a CNAME was accepted beside an address")
	}
	// .
	got, err = CanonicalRecords([]Record{{Type: RecordCNAME, Value: "Relay.Example.Invalid."}})
	if err != nil || len(got) != 1 || got[0].Value != "relay.example.invalid" {
		t.Fatalf("relay canonicalization: %v %v", got, err)
	}
}

// .
// .
type recordSetServer struct {
	srv      *httptest.Server
	lastBody map[string]json.RawMessage
	lastPath string
	status   int
	body     string
	header   http.Header
}

func newRecordSetServer(t *testing.T) *recordSetServer {
	t.Helper()
	rs := &recordSetServer{status: http.StatusAccepted, header: http.Header{}}
	rs.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rs.lastPath = r.URL.Path
		rs.lastBody = map[string]json.RawMessage{}
		_ = json.NewDecoder(r.Body).Decode(&rs.lastBody)
		for k, vals := range rs.header {
			for _, v := range vals {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(rs.status)
		_, _ = w.Write([]byte(rs.body))
	}))
	t.Cleanup(rs.srv.Close)
	return rs
}

func recordSetClient(t *testing.T, url string) *PublisherClient {
	t.Helper()
	key, canonical, env := identityForTest(t)
	c, err := NewPublisherClient(url, "", key, canonical, env)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestTheWireCarriesExactlyWhatEachOperationMayCarry(t *testing.T) {
	rs := newRecordSetServer(t)
	c := recordSetClient(t, rs.srv.URL)

	// .
	// .
	// .
	rs.status, rs.body = http.StatusOK, `{"status":"stored","generation":4,"managed":true,"records":[]}`
	if _, err := c.ReadRecords(context.Background(), vectorName, 0); err != nil {
		t.Fatal(err)
	}
	if rs.lastPath != recordSetStatusPath {
		t.Fatalf("read went to %s", rs.lastPath)
	}
	if _, ok := rs.lastBody["records"]; ok {
		t.Fatal("the signed read carried a records field")
	}
	if _, ok := rs.lastBody["expected_generation"]; ok {
		t.Fatal("the signed read carried an expected generation")
	}

	// .
	// .
	// .
	rs.status, rs.body = http.StatusAccepted, `{"status":"applied","generation":1,"managed":true,"records":[]}`
	if _, err := c.ReplaceRecords(context.Background(), vectorName, nil, 0, 0); err != nil {
		t.Fatal(err)
	}
	if rs.lastPath != recordSetPath {
		t.Fatalf("replace went to %s", rs.lastPath)
	}
	if string(rs.lastBody["records"]) != "[]" {
		t.Fatalf("withdrawal sent records=%s, not []", rs.lastBody["records"])
	}
	if string(rs.lastBody["expected_generation"]) != "0" {
		t.Fatalf("withdrawal sent expected_generation=%s", rs.lastBody["expected_generation"])
	}

	// .
	// .
	// .
	var expires string
	if err := json.Unmarshal(rs.lastBody["expires_at"], &expires); err != nil {
		t.Fatal(err)
	}
	parsed, err := time.Parse(time.RFC3339, expires)
	if err != nil || parsed.UTC().Format(time.RFC3339) != expires || !strings.HasSuffix(expires, "Z") {
		t.Fatalf("expires_at %q does not round-trip as canonical UTC", expires)
	}
}

func TestRecordSetOutcomesSayWhatIsCommitted(t *testing.T) {
	rs := newRecordSetServer(t)
	c := recordSetClient(t, rs.srv.URL)
	route := []Record{{Type: RecordA, Value: "192.0.2.2"}}

	// .
	rs.status, rs.body = http.StatusAccepted, `{"status":"applied","generation":5,"managed":true,"alias":"cedar.example.invalid","records":[{"rrtype":"A","value":"192.0.2.2","expires_at":"2026-09-13T12:00:00Z"}]}`
	set, err := c.ReplaceRecords(context.Background(), vectorName, route, 4, 0)
	if err != nil || !set.Delivered || set.Generation != 5 || set.Alias != "cedar.example.invalid" {
		t.Fatalf("applied: %+v %v", set, err)
	}

	// .
	// .
	// .
	// .
	rs.status, rs.body = http.StatusServiceUnavailable, `{"status":"stored","generation":6,"managed":true,"records":[]}`
	set, err = c.ReplaceRecords(context.Background(), vectorName, route, 5, 0)
	if err != nil {
		t.Fatalf("a committed-but-undelivered write was reported as failure: %v", err)
	}
	if set.Delivered || set.Generation != 6 {
		t.Fatalf("stored: %+v", set)
	}

	// .
	// .
	rs.status, rs.body = http.StatusServiceUnavailable, `upstream is down`
	if _, err := c.ReplaceRecords(context.Background(), vectorName, route, 6, 0); !errors.Is(err, ErrRecordSetAmbiguous) {
		t.Fatalf("a bare 503 was not ambiguous: %v", err)
	}

	// .
	// .
	rs.status, rs.body = http.StatusAccepted, `{"status":"applied","generation":`
	if _, err := c.ReplaceRecords(context.Background(), vectorName, route, 6, 0); !errors.Is(err, ErrRecordSetAmbiguous) {
		t.Fatalf("a truncated 202 was not ambiguous: %v", err)
	}
	// .
	rs.body = `{"status":"applied","generation":7,"managed":true,"records":[]}{"status":"applied"}`
	if _, err := c.ReplaceRecords(context.Background(), vectorName, route, 6, 0); !errors.Is(err, ErrRecordSetAmbiguous) {
		t.Fatalf("trailing JSON was accepted: %v", err)
	}
	// .
	rs.status, rs.body = http.StatusOK, `{"status":"applied","generation":7,"managed":true,"records":[]}`
	if _, err := c.ReplaceRecords(context.Background(), vectorName, route, 6, 0); !errors.Is(err, ErrRecordSetAmbiguous) {
		t.Fatalf("applied on a 200 was accepted: %v", err)
	}

	// .
	// .
	// .
	rs.status, rs.body = http.StatusConflict, `{"error":"generation is stale"}`
	_, err = c.ReplaceRecords(context.Background(), vectorName, route, 1, 0)
	if !errors.Is(err, ErrRecordSetConflict) {
		t.Fatalf("409 was not a conflict: %v", err)
	}
	if errors.Is(err, ErrRecordSetAmbiguous) {
		t.Fatal("a conflict is a refusal, not an unknown commit")
	}

	// .
	rs.status, rs.body = http.StatusTooManyRequests, `{"error":"rate limited"}`
	rs.header = http.Header{"Retry-After": []string{"90"}}
	_, err = c.ReplaceRecords(context.Background(), vectorName, route, 6, 0)
	var rse *RecordSetError
	if !errors.As(err, &rse) || rse.RetryAfter != 90*time.Second {
		t.Fatalf("429 retry-after: %v", err)
	}
	rs.header = http.Header{}

	// .
	for _, code := range []int{http.StatusBadRequest, http.StatusForbidden, http.StatusUnauthorized} {
		rs.status, rs.body = code, `{"error":"no"}`
		_, err := c.ReplaceRecords(context.Background(), vectorName, route, 6, 0)
		if err == nil || errors.Is(err, ErrRecordSetAmbiguous) || errors.Is(err, ErrRecordSetConflict) {
			t.Fatalf("HTTP %d: %v", code, err)
		}
	}
}

// .
// .
// .
func TestOnlyAWriteCanBeAmbiguous(t *testing.T) {
	rs := newRecordSetServer(t)
	c := recordSetClient(t, rs.srv.URL)
	rs.srv.Close()

	_, err := c.ReplaceRecords(context.Background(), vectorName, nil, 0, 0)
	if !errors.Is(err, ErrRecordSetAmbiguous) {
		t.Fatalf("a lost replacement was not ambiguous: %v", err)
	}
	_, err = c.ReadRecords(context.Background(), vectorName, 0)
	if err == nil {
		t.Fatal("a lost read reported success")
	}
	if errors.Is(err, ErrRecordSetAmbiguous) {
		t.Fatal("a lost read was called an unknown commit")
	}
}

func TestRecordSetReadsAdoptionAndRoute(t *testing.T) {
	// .
	if (RecordSet{}).Adopted() {
		t.Fatal("an unwritten name reported adoption")
	}
	// .
	// .
	tomb := RecordSet{Generation: 3, Managed: true}
	if !tomb.Adopted() || !tomb.Withdrawn() {
		t.Fatal("a tombstone did not report adoption")
	}
	if _, ok := tomb.CNAME(); ok {
		t.Fatal("a tombstone named a relay")
	}
	if len(tomb.Addresses()) != 0 {
		t.Fatal("a tombstone carried addresses")
	}
	live := RecordSet{Generation: 4, Managed: true, Records: []LeasedRecord{
		{Type: RecordA, Value: "10.0.0.1"}, {Type: RecordAAAA, Value: "fd00::1"},
	}}
	if got := live.Addresses(); len(got) != 2 || got[0] != "10.0.0.1" || got[1] != "fd00::1" {
		t.Fatalf("addresses: %v", got)
	}
	relay := RecordSet{Generation: 5, Managed: true, Records: []LeasedRecord{{Type: RecordCNAME, Value: "relay.example.invalid"}}}
	if name, ok := relay.CNAME(); !ok || name != "relay.example.invalid" {
		t.Fatalf("cname: %q %v", name, ok)
	}
}

// .
// .
// .
func TestRouteLeaseHonoursTheAdvertisedCeiling(t *testing.T) {
	if got := (ServiceStatus{}).RouteLease(); got != publishLifetime {
		t.Fatalf("no advertised ceiling: %v", got)
	}
	if got := (ServiceStatus{MaxPublishLifetime: 900 * time.Second}).RouteLease(); got != publishLifetime {
		t.Fatalf("a generous ceiling should leave the usual lease: %v", got)
	}
	short := ServiceStatus{MaxPublishLifetime: 300 * time.Second}
	got := short.RouteLease()
	if got >= 300*time.Second || got <= 0 {
		t.Fatalf("a short ceiling must shorten the lease: %v", got)
	}
	if got > 280*time.Second {
		t.Fatalf("a short ceiling left no margin for clock skew or a retry: %v", got)
	}
}

func TestServiceStatusGatesOnWhatTheServiceActuallySays(t *testing.T) {
	st := ServiceStatus{
		Capabilities:   []string{"address-v1", "record-sets-v1", "relay-v2"},
		RecordSetTypes: []string{"A", "AAAA", "CNAME"},
		RelayEndpoints: []RelayEndpoint{{Name: "relay.example.invalid", Port: 8180}},
	}
	if !st.Supports(CapabilityRecordSets) || !st.Supports(CapabilityRelayV2) {
		t.Fatal("advertised capabilities were not seen")
	}
	if st.Supports("record-sets-v2") {
		t.Fatal("an unadvertised capability was claimed")
	}
	// .
	// .
	// .
	if st.AcceptsRecordType("TXT") {
		t.Fatal("TXT was accepted into a record set")
	}
	if !st.AcceptsRecordType(RecordCNAME) {
		t.Fatal("CNAME was not accepted")
	}
	if _, ok := st.Relay("relay.example.invalid"); !ok {
		t.Fatal("an advertised relay was not found")
	}
	if _, ok := st.Relay("relay.elsewhere.invalid"); ok {
		t.Fatal("a relay the service never advertised was selectable")
	}
	// .
	// .
	if _, ok := (ServiceStatus{}).Relay("relay.example.invalid"); ok {
		t.Fatal("a relay was selectable with none advertised")
	}
}

// .
// .
// .
// .
func TestIncompleteSnapshotsAreNotEvidenceOfACommit(t *testing.T) {
	rs := newRecordSetServer(t)
	c := recordSetClient(t, rs.srv.URL)
	route := []Record{{Type: RecordA, Value: "192.0.2.2"}}

	for _, tc := range []struct {
		name string
		code int
		body string
	}{
		{"no managed and no records", http.StatusServiceUnavailable, `{"status":"stored","generation":7}`},
		{"negative generation", http.StatusServiceUnavailable, `{"status":"stored","generation":-1,"managed":true,"records":[]}`},
		{"unmanaged after a write", http.StatusServiceUnavailable, `{"status":"stored","generation":0,"managed":false,"records":[]}`},
		{"generation zero after a write", http.StatusAccepted, `{"status":"applied","generation":0,"managed":true,"records":[]}`},
		{"null records", http.StatusServiceUnavailable, `{"status":"stored","generation":7,"managed":true,"records":null}`},
		{"missing generation", http.StatusAccepted, `{"status":"applied","managed":true,"records":[]}`},
		{"a value that is not the type it claims", http.StatusAccepted,
			`{"status":"applied","generation":7,"managed":true,"records":[{"rrtype":"AAAA","value":"10.0.0.1","expires_at":"2026-09-13T12:00:00Z"}]}`},
		{"a lease that does not parse", http.StatusAccepted,
			`{"status":"applied","generation":7,"managed":true,"records":[{"rrtype":"A","value":"10.0.0.1","expires_at":"soon"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rs.status, rs.body = tc.code, tc.body
			set, err := c.ReplaceRecords(context.Background(), vectorName, route, 6, 0)
			if err == nil {
				t.Fatalf("accepted as committed: %+v", set)
			}
			if !errors.Is(err, ErrRecordSetAmbiguous) {
				t.Fatalf("a malformed reply to a write must leave the commit unknown, got %v", err)
			}
		})
	}

	// .
	// .
	rs.status, rs.body = http.StatusAccepted, `{"status":"applied","generation":7,"managed":true,"records":[{"rrtype":"A","value":"192.0.2.2","expires_at":"2026-09-13T12:00:00Z"}]}`
	if set, err := c.ReplaceRecords(context.Background(), vectorName, route, 6, 0); err != nil || !set.Delivered {
		t.Fatalf("a valid applied reply was refused: %+v %v", set, err)
	}
	rs.status, rs.body = http.StatusServiceUnavailable, `{"status":"stored","generation":8,"managed":true,"records":[]}`
	if set, err := c.ReplaceRecords(context.Background(), vectorName, route, 7, 0); err != nil || set.Delivered || set.Generation != 8 {
		t.Fatalf("a valid stored reply was refused: %+v %v", set, err)
	}

	// .
	// .
	// .
	// .
	rs.status, rs.body = http.StatusOK, `{"status":"stored","generation":0,"managed":false,"records":[]}`
	set, err := c.ReadRecords(context.Background(), vectorName, 0)
	if err != nil {
		t.Fatalf("a never-adopted name could not be read: %v", err)
	}
	if set.Adopted() || set.Generation != 0 {
		t.Fatalf("never-adopted read: %+v", set)
	}
	// .
	// .
	// .
	rs.body = `{"status":"stored","generation":4,"managed":true,"records":[{"rrtype":"A","value":"10.0.0.1","expires_at":"2001-01-01T00:00:00Z"}]}`
	set, err = c.ReadRecords(context.Background(), vectorName, 0)
	if err != nil || len(set.Records) != 1 {
		t.Fatalf("an expired member was dropped from a signed read: %+v %v", set, err)
	}
}

// .
// .
// .
// .
func TestAProxyFailureLeavesTheCommitUnknown(t *testing.T) {
	rs := newRecordSetServer(t)
	c := recordSetClient(t, rs.srv.URL)
	route := []Record{{Type: RecordA, Value: "192.0.2.2"}}

	for _, code := range []int{http.StatusInternalServerError, http.StatusBadGateway, http.StatusGatewayTimeout, 599} {
		rs.status, rs.body = code, `<html>gateway</html>`
		_, err := c.ReplaceRecords(context.Background(), vectorName, route, 6, 0)
		if !errors.Is(err, ErrRecordSetAmbiguous) {
			t.Fatalf("HTTP %d on a write was treated as a definite refusal: %v", code, err)
		}
		// .
		// .
		// .
		_, err = c.ReadRecords(context.Background(), vectorName, 0)
		if err == nil {
			t.Fatalf("HTTP %d on a read reported success", code)
		}
		if errors.Is(err, ErrRecordSetAmbiguous) {
			t.Fatalf("HTTP %d on a read was called an unknown commit", code)
		}
	}
	// .
	for _, code := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusRequestEntityTooLarge} {
		rs.status, rs.body = code, `{"error":"no"}`
		_, err := c.ReplaceRecords(context.Background(), vectorName, route, 6, 0)
		if err == nil || errors.Is(err, ErrRecordSetAmbiguous) {
			t.Fatalf("HTTP %d should be a decision, not a doubt: %v", code, err)
		}
	}
}

// .
// .
// .
func TestOnlyTheManagedMarkerProvesAdoption(t *testing.T) {
	legacy := RecordSet{Generation: 12, Managed: false, Records: []LeasedRecord{
		{Type: RecordA, Value: "10.0.0.1", ExpiresAt: "2026-09-13T12:00:00Z"},
	}}
	if legacy.Adopted() {
		t.Fatal("a legacy name with a positive generation was called adopted")
	}
	if (RecordSet{Generation: 1, Managed: true}).Adopted() != true {
		t.Fatal("a managed name was not called adopted")
	}
	// .
	// .
	tomb := RecordSet{Generation: 3, Managed: true}
	if !tomb.Withdrawn() || !tomb.Adopted() {
		t.Fatalf("tombstone: %+v", tomb)
	}
	if (RecordSet{}).Withdrawn() {
		t.Fatal("a name that never published was called withdrawn")
	}
	if (RecordSet{Generation: 4, Managed: true, Records: []LeasedRecord{{Type: RecordA, Value: "10.0.0.1"}}}).Withdrawn() {
		t.Fatal("a live route was called withdrawn")
	}
}

// .
// .
// .
// .
// .
// .
// .
const legacyWireSnapshot = `{"status":"stored","generation":2,"managed":false,` +
	`"alias":"cobalt-pocket-relay.example.invalid",` +
	`"records":[{"rrtype":"A","value":"192.0.2.5","expires_at":"2026-09-13T19:06:17Z"}]}`

func TestTheServersOwnLegacyWireIsNotAdoption(t *testing.T) {
	rs := newRecordSetServer(t)
	c := recordSetClient(t, rs.srv.URL)
	rs.status, rs.body = http.StatusOK, legacyWireSnapshot
	set, err := c.ReadRecords(context.Background(), vectorName, 0)
	if err != nil {
		t.Fatalf("a real legacy snapshot was refused: %v", err)
	}
	if set.Adopted() {
		t.Fatal("a legacy name at generation 2 was read as adopted")
	}
	if set.Generation != 2 || len(set.Records) != 1 {
		t.Fatalf("the snapshot was not read whole: %+v", set)
	}
	// .
	// .
	if set.Alias != "cobalt-pocket-relay.example.invalid" {
		t.Fatalf("alias: %q", set.Alias)
	}
}

// .
func TestAStatusMustAnswerItsOwnOperation(t *testing.T) {
	rs := newRecordSetServer(t)
	c := recordSetClient(t, rs.srv.URL)
	stored := `{"status":"stored","generation":6,"managed":true,"records":[]}`

	// .
	rs.status, rs.body = http.StatusOK, stored
	if _, err := c.ReplaceRecords(context.Background(), vectorName, nil, 5, 0); !errors.Is(err, ErrRecordSetAmbiguous) {
		t.Fatalf("a 200 answered a write: %v", err)
	}
	// .
	for _, code := range []int{http.StatusAccepted, http.StatusServiceUnavailable} {
		rs.status, rs.body = code, stored
		if _, err := c.ReadRecords(context.Background(), vectorName, 0); err == nil {
			t.Fatalf("HTTP %d answered a read", code)
		}
	}
	// .
	rs.status, rs.body = http.StatusOK, stored
	if _, err := c.ReadRecords(context.Background(), vectorName, 0); err != nil {
		t.Fatalf("a 200 read was refused: %v", err)
	}
	rs.status, rs.body = http.StatusAccepted, `{"status":"applied","generation":6,"managed":true,"records":[]}`
	if _, err := c.ReplaceRecords(context.Background(), vectorName, nil, 5, 0); err != nil {
		t.Fatalf("a 202 write was refused: %v", err)
	}
}

// .
// .
func TestGenerationZeroCannotCarryAdoptionOrRecords(t *testing.T) {
	rs := newRecordSetServer(t)
	c := recordSetClient(t, rs.srv.URL)
	for _, body := range []string{
		`{"status":"stored","generation":0,"managed":true,"records":[]}`,
		`{"status":"stored","generation":0,"managed":false,"records":[{"rrtype":"A","value":"192.0.2.5","expires_at":"2026-09-13T19:00:00Z"}]}`,
	} {
		rs.status, rs.body = http.StatusOK, body
		if set, err := c.ReadRecords(context.Background(), vectorName, 0); err == nil {
			t.Fatalf("an impossible zero-generation snapshot was accepted: %+v", set)
		}
	}
	// .
	rs.status, rs.body = http.StatusOK, `{"status":"stored","generation":0,"managed":false,"records":[]}`
	if _, err := c.ReadRecords(context.Background(), vectorName, 0); err != nil {
		t.Fatalf("the never-adopted read was refused: %v", err)
	}
}

// .
// .
func TestTheBoundedReaderKeepsItsCauseAndItsBound(t *testing.T) {
	_, err := readWholeBody(&http.Response{Body: io.NopCloser(iotest.TimeoutReader(strings.NewReader(strings.Repeat("x", 64))))})
	if err == nil {
		t.Fatal("a damaged body read as whole")
	}
	if !errors.Is(err, iotest.ErrTimeout) {
		t.Fatalf("the transport cause was lost: %v", err)
	}
	body, err := readWholeBody(&http.Response{Body: io.NopCloser(strings.NewReader(strings.Repeat("x", maxResponseBytes)))})
	if err != nil || len(body) != maxResponseBytes {
		t.Fatalf("a body exactly at the ceiling was refused: %v", err)
	}
	if _, err := readWholeBody(&http.Response{Body: io.NopCloser(strings.NewReader(strings.Repeat("x", maxResponseBytes+1)))}); err == nil {
		t.Fatal("a body past the ceiling was accepted")
	}
}

// .
func TestServiceStatusRefusesMoreThanOneAnswer(t *testing.T) {
	for _, body := range []string{
		`{"zone":"example.test"} {"zone":"different.test"}`,
		`{"zone":"example.test"} garbage`,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(body))
		}))
		c := recordSetClient(t, srv.URL)
		_, err := c.ServiceStatus(context.Background())
		srv.Close()
		if err == nil {
			t.Fatalf("service status accepted trailing content: %s", body)
		}
	}
}

// .
// .
// .
// .
func TestASnapshotMustBeAWholeRouteNotJustPlausibleMembers(t *testing.T) {
	rs := newRecordSetServer(t)
	c := recordSetClient(t, rs.srv.URL)
	route := []Record{{Type: RecordA, Value: "192.0.2.2"}}
	const exp = `","expires_at":"2026-09-13T12:00:00Z"}`
	for name, records := range map[string]string{
		"a CNAME beside an address": `[{"rrtype":"A","value":"10.0.0.1` + exp +
			`,{"rrtype":"CNAME","value":"relay.example.invalid` + exp + `]`,
		"the same address twice": `[{"rrtype":"A","value":"10.0.0.1` + exp +
			`,{"rrtype":"A","value":"10.0.0.1` + exp + `]`,
		"a hostname with an empty label":        `[{"rrtype":"CNAME","value":"relay..example` + exp + `]`,
		"a spelling the service would not emit": `[{"rrtype":"AAAA","value":"FD00::1` + exp + `]`,
		"members out of order": `[{"rrtype":"AAAA","value":"fd00::1` + exp +
			`,{"rrtype":"A","value":"10.0.0.1` + exp + `]`,
	} {
		t.Run(name, func(t *testing.T) {
			rs.status = http.StatusServiceUnavailable
			rs.body = `{"status":"stored","generation":7,"managed":true,"records":` + records + `}`
			if set, err := c.ReplaceRecords(context.Background(), vectorName, route, 6, 0); err == nil {
				t.Fatalf("an impossible route was recorded as committed: %+v", set)
			} else if !errors.Is(err, ErrRecordSetAmbiguous) {
				t.Fatalf("want an unknown commit, got %v", err)
			}
		})
	}
	// .
	// .
	for _, good := range []string{
		`[{"rrtype":"A","value":"10.0.0.1` + exp + `,{"rrtype":"AAAA","value":"fd00::1` + exp + `]`,
		`[{"rrtype":"CNAME","value":"relay.example.invalid` + exp + `]`,
	} {
		rs.status = http.StatusServiceUnavailable
		rs.body = `{"status":"stored","generation":7,"managed":true,"records":` + good + `}`
		if _, err := c.ReplaceRecords(context.Background(), vectorName, route, 6, 0); err != nil {
			t.Fatalf("a real route was refused: %v (%s)", err, good)
		}
	}
}

// .
// .
// .
func TestARelayNameIsHeldToTheServicesOwnRules(t *testing.T) {
	long63 := strings.Repeat("a", 64)
	// .
	longName := strings.Repeat("a", 60) + "." + strings.Repeat("b", 60) + "." +
		strings.Repeat("c", 60) + "." + strings.Repeat("d", 60) + "." +
		strings.Repeat("e", 60) + ".example"
	for _, bad := range []string{
		"relay..example", "relay_bad.example", "relay/x.example",
		long63 + ".example", longName, "-relay.example", "relay-.example",
		"relay.example!", "RELAY.EXAMPLE.WITH SPACE",
	} {
		if _, err := CanonicalRecords([]Record{{Type: RecordCNAME, Value: bad}}); err == nil {
			t.Fatalf("%q was accepted as a relay name", bad)
		}
	}
	// .
	// .
	for _, good := range []string{
		"relay.example.invalid", "Relay.Example.Invalid.", "r1.relay-a.example.invalid",
	} {
		if _, err := CanonicalRecords([]Record{{Type: RecordCNAME, Value: good}}); err != nil {
			t.Fatalf("%q was refused: %v", good, err)
		}
	}
}

// .
// .
// .
// .
func TestDiscoveryKeepsTheStatusAndTheDelay(t *testing.T) {
	rs := newRecordSetServer(t)
	c := recordSetClient(t, rs.srv.URL)
	rs.status, rs.body = http.StatusTooManyRequests, `{"error":"rate limited"}`
	rs.header = http.Header{"Retry-After": []string{"60"}}
	_, err := c.ServiceStatus(context.Background())
	if err == nil {
		t.Fatal("a rate-limited discovery reported success")
	}
	var rse *RecordSetError
	if !errors.As(err, &rse) {
		t.Fatalf("discovery lost its typed status: %v", err)
	}
	if rse.Status != http.StatusTooManyRequests || rse.RetryAfter != time.Minute {
		t.Fatalf("discovery lost the service's delay: status=%d retry=%v", rse.Status, rse.RetryAfter)
	}
}
