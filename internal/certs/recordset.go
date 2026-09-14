package certs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
	"github.com/aiii-dot-id/aii-os/internal/witness"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .

const (
	recordSetReplaceTag = "AIII-CERTD-RECORD-SET-V1"
	recordSetStatusTag  = "AIII-CERTD-RECORD-SET-STATUS-V1"
	recordSetPath       = "/certd/records"
	recordSetStatusPath = "/certd/records/status"
	// .
	// .
	maxRecordSetMembers = 128
	// .
	// .
	// .
	// .
	RecordA     = "A"
	RecordAAAA  = "AAAA"
	RecordCNAME = "CNAME"

	// .
	CapabilityRecordSets = "record-sets-v1"
	CapabilityRelayV2    = "relay-v2"

	// .
	recordSetApplied = "applied"
	recordSetStored  = "stored"
)

// .
type Record struct {
	Type  string `json:"rrtype"`
	Value string `json:"value"`
}

// .
// .
// .
// .
type LeasedRecord struct {
	Type      string `json:"rrtype"`
	Value     string `json:"value"`
	ExpiresAt string `json:"expires_at"`
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
type RecordSet struct {
	Generation int64
	Managed    bool
	Alias      string
	Records    []LeasedRecord
	Delivered  bool
	HTTPStatus int
}

// .
// .
// .
// .
// .
// .
// .
// .
func (r RecordSet) Adopted() bool { return r.Managed }

// .
// .
// .
func (r RecordSet) Withdrawn() bool { return r.Managed && len(r.Records) == 0 }

// .
func (r RecordSet) Addresses() []string {
	out := make([]string, 0, len(r.Records))
	for _, rec := range r.Records {
		if rec.Type == RecordA || rec.Type == RecordAAAA {
			out = append(out, rec.Value)
		}
	}
	return out
}

// .
func (r RecordSet) CNAME() (string, bool) {
	if len(r.Records) == 1 && r.Records[0].Type == RecordCNAME {
		return r.Records[0].Value, true
	}
	return "", false
}

// .
var (
	// .
	// .
	// .
	// .
	ErrRecordSetConflict = errors.New("certs: record set generation conflict")
	// .
	// .
	// .
	// .
	ErrRecordSetAmbiguous = errors.New("certs: record set commit state unknown")
)

// .
// .
type RecordSetError struct {
	Op         string
	Name       string
	Status     int
	Message    string
	RetryAfter time.Duration
	kind       error
}

func (e *RecordSetError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = "no reason given"
	}
	if e.Status == 0 {
		return fmt.Sprintf("certs: %s %s: %s", e.Op, e.Name, msg)
	}
	return fmt.Sprintf("certs: %s %s: HTTP %d: %s", e.Op, e.Name, e.Status, msg)
}

func (e *RecordSetError) Unwrap() error { return e.kind }

// .
// .
// .
// .
// .
// .
// .
// .
// .
func CanonicalRecords(in []Record) ([]Record, error) {
	out, err := canonicalRoute(in)
	if err != nil {
		return nil, err
	}
	for _, r := range out {
		if r.Type == RecordCNAME {
			continue
		}
		ip := net.ParseIP(r.Value)
		if ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() || ip.IsLinkLocalUnicast() {
			return nil, fmt.Errorf("certs: %s cannot be a route for anyone else to reach", r.Value)
		}
	}
	return out, nil
}

// .
// .
// .
// .
func canonicalRoute(in []Record) ([]Record, error) {
	out := make([]Record, 0, len(in))
	for _, r := range in {
		switch r.Type {
		case RecordA, RecordAAAA:
			ip := net.ParseIP(r.Value)
			if ip == nil {
				return nil, fmt.Errorf("certs: %s record %q is not an IP address", r.Type, r.Value)
			}
			// .
			// .
			// .
			// .
			kind := RecordAAAA
			if ip.To4() != nil {
				kind = RecordA
			}
			if kind != r.Type {
				return nil, fmt.Errorf("certs: %s is a %s address, not %s", r.Value, kind, r.Type)
			}
			out = append(out, Record{Type: kind, Value: ip.String()})
		case RecordCNAME:
			v := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(r.Value), "."))
			if v == "" || !strings.Contains(v, ".") || net.ParseIP(v) != nil {
				return nil, fmt.Errorf("certs: %q is not a relay name", r.Value)
			}
			// .
			// .
			// .
			// .
			// .
			// .
			if !hostnameShape(v) {
				return nil, fmt.Errorf("certs: %q is not a relay name", r.Value)
			}
			if _, _, err := net.SplitHostPort(v); err == nil {
				return nil, fmt.Errorf("certs: the relay CNAME carries no port (%q); DNS has none to carry", r.Value)
			}
			out = append(out, Record{Type: RecordCNAME, Value: v})
		default:
			return nil, fmt.Errorf("certs: an identity record set accepts A, AAAA or CNAME, not %q", r.Type)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		return out[i].Value < out[j].Value
	})
	deduped := out[:0]
	for i, r := range out {
		if i > 0 && out[i-1] == r {
			continue
		}
		deduped = append(deduped, r)
	}
	out = deduped
	for _, r := range out {
		if r.Type == RecordCNAME && len(out) != 1 {
			return nil, errors.New("certs: a relay CNAME is the whole route or none of it")
		}
	}
	if len(out) > maxRecordSetMembers {
		return nil, fmt.Errorf("certs: %d records is past the %d the service accepts", len(out), maxRecordSetMembers)
	}
	return out, nil
}

// .
// .
// .
// .
func hostnameShape(name string) bool {
	if name == "" || len(name) > 253 || !strings.Contains(name, ".") {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 {
			return false
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

// .
// .
// .
func canonicalRecordsJSON(records []Record) ([]byte, error) {
	if records == nil {
		records = []Record{}
	}
	raw, err := json.Marshal(records)
	if err != nil {
		return nil, err
	}
	return canonicaljson.CanonicalizeV1(raw)
}

// .
func RecordsDigest(records []Record) (string, error) {
	raw, err := canonicalRecordsJSON(records)
	if err != nil {
		return "", err
	}
	return canonicaljson.CanonicalizeV1SHA256(raw)
}

// .
// .
// .
// .
func RecordSetInput(tag, identityID, name string, generation int64, recordsDigest, nonce, expiresAt string) []byte {
	return []byte(tag + "\n" +
		"identity_id:" + identityID + "\n" +
		"name:" + name + "\n" +
		"expected_generation:" + strconv.FormatInt(generation, 10) + "\n" +
		"records_sha256:" + recordsDigest + "\n" +
		"nonce:" + nonce + "\n" +
		"expires_at:" + expiresAt + "\n")
}

// .
// .
// .
// .
// .
// .
// .
// .
func (c *PublisherClient) ReadRecords(ctx context.Context, name string, lease time.Duration) (RecordSet, error) {
	nonce, expires, err := c.recordSetEnvelope(lease)
	if err != nil {
		return RecordSet{}, err
	}
	digest, err := RecordsDigest(nil)
	if err != nil {
		return RecordSet{}, err
	}
	sig, err := witness.SignInput(c.key, c.env, RecordSetInput(recordSetStatusTag, c.identityID, name, 0, digest, nonce, expires))
	if err != nil {
		return RecordSet{}, err
	}
	// .
	// .
	body := map[string]interface{}{
		"identity_id":        c.identityID,
		"name":               name,
		"nonce":              nonce,
		"expires_at":         expires,
		"identity_signature": sig,
	}
	return c.recordSetCall(ctx, "read", recordSetStatusPath, name, body)
}

// .
// .
// .
// .
// .
// .
// .
func (c *PublisherClient) ReplaceRecords(ctx context.Context, name string, records []Record, expectedGeneration int64, lease time.Duration) (RecordSet, error) {
	if expectedGeneration < 0 {
		return RecordSet{}, fmt.Errorf("certs: expected generation %d is not a generation", expectedGeneration)
	}
	canonical, err := CanonicalRecords(records)
	if err != nil {
		return RecordSet{}, err
	}
	if canonical == nil {
		canonical = []Record{}
	}
	nonce, expires, err := c.recordSetEnvelope(lease)
	if err != nil {
		return RecordSet{}, err
	}
	digest, err := RecordsDigest(canonical)
	if err != nil {
		return RecordSet{}, err
	}
	sig, err := witness.SignInput(c.key, c.env, RecordSetInput(recordSetReplaceTag, c.identityID, name, expectedGeneration, digest, nonce, expires))
	if err != nil {
		return RecordSet{}, err
	}
	body := map[string]interface{}{
		"identity_id":         c.identityID,
		"name":                name,
		"records":             canonical,
		"expected_generation": expectedGeneration,
		"nonce":               nonce,
		"expires_at":          expires,
		"identity_signature":  sig,
	}
	return c.recordSetCall(ctx, "replace", recordSetPath, name, body)
}

// .
// .
// .
// .
// .
func (c *PublisherClient) recordSetEnvelope(lease time.Duration) (nonce, expires string, err error) {
	if lease <= 0 {
		lease = publishLifetime
	}
	nonce, err = c.nonce()
	if err != nil {
		return "", "", err
	}
	return nonce, c.now().Add(lease).UTC().Truncate(time.Second).Format(time.RFC3339), nil
}

// .
// .
// .
func (c *PublisherClient) recordSetCall(ctx context.Context, op, path, name string, body interface{}) (RecordSet, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return RecordSet{}, err
	}
	canonical, err := canonicaljson.CanonicalizeV1(raw)
	if err != nil {
		return RecordSet{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(canonical))
	if err != nil {
		return RecordSet{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		// .
		// .
		return RecordSet{}, &RecordSetError{Op: op, Name: name, Message: err.Error(), kind: ambiguousFor(op)}
	}
	defer res.Body.Close()
	// .
	// .
	data, readErr := io.ReadAll(io.LimitReader(res.Body, maxResponseBytes+1))
	truncated := len(data) > maxResponseBytes
	retry := retryAfter(res.Header.Get("Retry-After"))

	switch {
	case res.StatusCode == http.StatusAccepted, res.StatusCode == http.StatusOK, res.StatusCode == http.StatusServiceUnavailable:
		if readErr != nil || truncated {
			return RecordSet{}, &RecordSetError{Op: op, Name: name, Status: res.StatusCode,
				Message: "the answer could not be read whole", RetryAfter: retry, kind: ambiguousFor(op)}
		}
		set, ok := decodeRecordSet(data, res.StatusCode, op)
		if !ok {
			// .
			// .
			return RecordSet{}, &RecordSetError{Op: op, Name: name, Status: res.StatusCode,
				Message: "the answer carried no usable snapshot", RetryAfter: retry, kind: ambiguousFor(op)}
		}
		return set, nil
	case res.StatusCode == http.StatusConflict:
		return RecordSet{}, &RecordSetError{Op: op, Name: name, Status: res.StatusCode,
			Message: serviceMessage(data), RetryAfter: retry, kind: ErrRecordSetConflict}
	case res.StatusCode == http.StatusTooManyRequests:
		// .
		// .
		if retry <= 0 {
			retry = time.Minute
		}
		return RecordSet{}, &RecordSetError{Op: op, Name: name, Status: res.StatusCode,
			Message: serviceMessage(data), RetryAfter: retry}
	case res.StatusCode >= 400 && res.StatusCode < 500:
		// .
		// .
		return RecordSet{}, &RecordSetError{Op: op, Name: name, Status: res.StatusCode,
			Message: serviceMessage(data), RetryAfter: retry}
	default:
		// .
		// .
		// .
		// .
		// .
		// .
		return RecordSet{}, &RecordSetError{Op: op, Name: name, Status: res.StatusCode,
			Message: serviceMessage(data), RetryAfter: retry, kind: ambiguousFor(op)}
	}
}

// .
// .
// .
func ambiguousFor(op string) error {
	if op == "replace" {
		return ErrRecordSetAmbiguous
	}
	return nil
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
func decodeRecordSet(data []byte, status int, op string) (RecordSet, bool) {
	var out struct {
		Status     string          `json:"status"`
		Generation *int64          `json:"generation"`
		Managed    *bool           `json:"managed"`
		Alias      string          `json:"alias"`
		Records    *[]LeasedRecord `json:"records"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&out); err != nil {
		return RecordSet{}, false
	}
	// .
	if _, err := dec.Token(); err != io.EOF {
		return RecordSet{}, false
	}
	if out.Generation == nil || *out.Generation < 0 || out.Managed == nil {
		return RecordSet{}, false
	}
	// .
	// .
	// .
	if out.Records == nil {
		return RecordSet{}, false
	}
	switch out.Status {
	case recordSetApplied, recordSetStored:
	default:
		return RecordSet{}, false
	}
	// .
	// .
	// .
	if (out.Status == recordSetApplied) != (status == http.StatusAccepted) {
		return RecordSet{}, false
	}
	// .
	// .
	// .
	// .
	// .
	switch op {
	case "read":
		if status != http.StatusOK {
			return RecordSet{}, false
		}
	case "replace":
		if status != http.StatusAccepted && status != http.StatusServiceUnavailable {
			return RecordSet{}, false
		}
	default:
		return RecordSet{}, false
	}
	// .
	// .
	// .
	if *out.Generation == 0 && (*out.Managed || len(*out.Records) != 0) {
		return RecordSet{}, false
	}
	plain := make([]Record, 0, len(*out.Records))
	for _, r := range *out.Records {
		// .
		// .
		// .
		if !plausibleLeasedRecord(r) {
			return RecordSet{}, false
		}
		plain = append(plain, Record{Type: r.Type, Value: r.Value})
	}
	// .
	// .
	// .
	// .
	// .
	// .
	canonical, err := canonicalRoute(plain)
	if err != nil || len(canonical) != len(plain) {
		return RecordSet{}, false
	}
	for i := range canonical {
		if canonical[i] != plain[i] {
			return RecordSet{}, false
		}
	}
	if op == "replace" {
		// .
		// .
		// .
		// .
		if !*out.Managed || *out.Generation == 0 {
			return RecordSet{}, false
		}
	}
	return RecordSet{
		Generation: *out.Generation,
		Managed:    *out.Managed,
		Alias:      out.Alias,
		Records:    *out.Records,
		Delivered:  out.Status == recordSetApplied,
		HTTPStatus: status,
	}, true
}

// .
// .
// .
// .
// .
func plausibleLeasedRecord(r LeasedRecord) bool {
	if r.ExpiresAt == "" {
		return false
	}
	if _, err := time.Parse(time.RFC3339, r.ExpiresAt); err != nil {
		return false
	}
	switch r.Type {
	case RecordA:
		ip := net.ParseIP(r.Value)
		return ip != nil && ip.To4() != nil
	case RecordAAAA:
		ip := net.ParseIP(r.Value)
		return ip != nil && ip.To4() == nil
	case RecordCNAME:
		return r.Value != "" && strings.Contains(r.Value, ".") && net.ParseIP(r.Value) == nil
	}
	return false
}

func serviceMessage(data []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(data, &e) == nil && e.Error != "" {
		return e.Error
	}
	msg := strings.TrimSpace(string(data))
	if len(msg) > 300 {
		msg = msg[:300]
	}
	return msg
}

func retryAfter(h string) time.Duration {
	if h == "" {
		return 0
	}
	if secs, err := strconv.Atoi(strings.TrimSpace(h)); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	if when, err := http.ParseTime(h); err == nil {
		if d := time.Until(when); d > 0 {
			return d
		}
	}
	return 0
}
