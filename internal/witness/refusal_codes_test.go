package witness

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// refusalVector is one refusal a witness can answer a bookmark with, and
// what this client decides of it (testdata/witness-refusals).
type refusalVector struct {
	Name     string `json:"name"`
	Status   int    `json:"status"`
	Body     string `json:"body"`
	Decision string `json:"decision"`
}

func refusalVectors(t *testing.T) []refusalVector {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "witness-refusals", "client-decisions.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Vectors []refusalVector `json:"vectors"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Vectors) == 0 {
		t.Fatal("rig: no vectors")
	}
	return file.Vectors
}

// decided is what this client decides of a bookmark's answer: the one
// class of a conflict, or not_a_conflict for an answer that is not one.
func decided(t *testing.T, err error) string {
	t.Helper()
	var ce *ConflictError
	if !errors.As(err, &ce) {
		return "not_a_conflict"
	}
	var classes []string
	if ce.IsIntegrity() {
		classes = append(classes, "integrity")
	}
	if ce.IsCadence() {
		classes = append(classes, "cadence")
	}
	if ce.IsKeyAlreadyEnrolled() {
		classes = append(classes, "key_already_enrolled")
	}
	if ce.IsContextRequired() {
		classes = append(classes, "context_required")
	}
	if ce.Unrecognised() {
		classes = append(classes, "unrecognised")
	}
	if len(classes) != 1 {
		t.Fatalf("a conflict is of exactly one class, and %v is of %v", ce, classes)
	}
	return classes[0]
}

func TestEachRefusalIsReadByItsCodeAndByItsWordsOnlyWhereItCarriesNone(t *testing.T) {
	vectors := refusalVectors(t)
	for _, v := range vectors {
		t.Run(v.Name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(v.Status)
				_, _ = w.Write([]byte(v.Body))
			}))
			t.Cleanup(srv.Close)
			result, err := New(srv.URL, "").Bookmark(WitnessRequest{IdentityID: "did:aiii:identity:sha256:vector", LedgerOrdinal: 1, LedgerHash: "vector"})
			if err == nil {
				t.Fatalf("a %d was taken for a receipt: %+v", v.Status, result)
			}
			if got := decided(t, err); got != v.Decision {
				t.Fatalf("A %d ANSWERING %s IS DECIDED %s, WANT %s: %v", v.Status, v.Body, got, v.Decision, err)
			}
			// A refusal that is not a conflict still says what the witness
			// answered, its code with it.
			if v.Decision == "not_a_conflict" {
				var body struct {
					Code string `json:"code"`
				}
				if json.Unmarshal([]byte(v.Body), &body) == nil && body.Code != "" && !strings.Contains(err.Error(), body.Code) {
					t.Fatalf("the refusal's code %q is not in what is said of it: %v", body.Code, err)
				}
			}
		})
	}

	// Every code the deployed server names (0.4.0's table) is met by a
	// vector, and one nobody defined is too.
	met := map[string]bool{}
	for _, v := range vectors {
		var body struct {
			Code string `json:"code"`
		}
		if json.Unmarshal([]byte(v.Body), &body) == nil {
			met[body.Code] = true
		}
	}
	for _, code := range []string{
		"bad_request", "bad_signature", "too_large", "method_not_allowed", "misdirected_request",
		"identity_key_mismatch", "rollback", "fork", "cadence", "rate_limited", "unavailable",
		// 0.5.0's codes (the witness design, revision 8, its refusal table)
		"key_already_enrolled", "context_required", "context_mismatch", "expected_head_mismatch",
		"target_key_mismatch", "handover_unrecognized", "handover_unverified", "handover_mismatch",
		"rollback_behind_a_restore", // nobody's: a code this client does not know
	} {
		if !met[code] {
			t.Errorf("no vector carries the code %q", code)
		}
	}
}
