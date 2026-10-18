package llm

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// A parser swap must not reject a UUID URN because its scheme or namespace
// uses another case (RFC 8141 section 3.1). Validation preserves the supplied
// spelling; arbitrary characters around a UUID are still not braces.
func TestAClaudeProfileAdmitsAnAccountUUIDInTheseFormsOnly(t *testing.T) {
	const id = "f81d4fae-7dec-11d0-a765-00a0c91e6bf6"
	admitted := map[string]string{
		"absent":             "",
		"hyphenated":         id,
		"upper-case digits":  strings.ToUpper(id),
		"a urn":              "urn:uuid:" + id,
		"upper-case urn":     "URN:UUID:" + id,
		"mixed-case urn":     "UrN:UuId:" + strings.ToUpper(id),
		"braced":             "{" + id + "}",
		"thirty-two digits":  strings.ReplaceAll(id, "-", ""),
		"upper, thirty-two":  strings.ToUpper(strings.ReplaceAll(id, "-", "")),
		"the nil identifier": "00000000-0000-0000-0000-000000000000",
	}
	refused := map[string]string{
		"38 characters, no braces":       "X" + id + "X",
		"an opening brace only":          "{" + id,
		"one digit short":                id[:len(id)-1],
		"underscores for hyphens":        strings.ReplaceAll(id, "-", "_"),
		"a leading space":                " " + id,
		"a digit that is not hex":        "g" + id[1:],
		"a urn of another namespace":     "urn:oid:" + id,
		"an upper-case wrong namespace":  "URN:GUID:" + id,
		"an upper-case urn with bad hex": "URN:UUID:g" + id[1:],
		"a non-ASCII urn namespace":      "urn:uu\u0130d:" + id,
		"two identifiers run together":   id + id,
		"words":                          "the operator's account",
		"a hyphen out of place":          "f81d4fae7-dec-11d0-a765-00a0c91e6bf6",
		"a trailing newline":             id + "\n",
		"braces around thirty-two":       "{" + strings.ReplaceAll(id, "-", "") + "}",
		"a urn around thirty-two digits": "urn:uuid:" + strings.ReplaceAll(id, "-", ""),
		// Shorter than the prefix the validator looks for, and the prefix
		// with nothing after it: each is refused, and none is sliced past
		// its end.
		"one character":                  "u",
		"a urn prefix a byte short":      "urn:uuid",
		"a urn prefix alone":             "urn:uuid:",
		"an upper-case urn prefix alone": "URN:UUID:",
	}
	for name, value := range admitted {
		p := capturedClaudeProfile()
		p.AccountUUID = value
		if err := p.Validate(); err != nil {
			t.Errorf("%s (%q) is an account_uuid a profile admits, yet: %v", name, value, err)
		}
		if p.AccountUUID != value {
			t.Errorf("%s: validation rewrote account_uuid to %q", name, p.AccountUUID)
		}
	}
	// The profile is the captured one with this one field changed, and the
	// "absent" row above shows it valid without it: a refusal here is the
	// field's.
	for name, value := range refused {
		p := capturedClaudeProfile()
		p.AccountUUID = value
		if err := p.Validate(); err == nil {
			t.Errorf("%s (%q) was admitted as an account_uuid", name, value)
		}
	}
}

// Exercise admission and the actual request encoder, not just Parse. This
// is a local wire fixture, not proof of what the provider accepts remotely.
func TestAClaudeAccountUUIDKeepsItsConfiguredSpellingOnTheWire(t *testing.T) {
	const id = "f81d4fae-7dec-11d0-a765-00a0c91e6bf6"
	for _, tc := range []struct {
		value string
		valid bool
	}{
		{"urn:uuid:" + id, true},
		{"URN:UUID:" + id, true},
		{"UrN:UuId:" + strings.ToUpper(id), true},
		{"X" + id + "X", false},
		{"URN:UUID:g" + id[1:], false},
		{"urn:uu\u0130d:" + id, false},
	} {
		t.Run(tc.value, func(t *testing.T) {
			p := capturedClaudeProfile()
			raw, err := json.Marshal(map[string]string{"account_uuid": tc.value})
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(raw, p); err != nil {
				t.Fatal(err)
			}
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				var body struct {
					Metadata anthMetadata `json:"metadata"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				var metadata struct {
					AccountUUID string `json:"account_uuid"`
				}
				if err := json.Unmarshal([]byte(body.Metadata.UserID), &metadata); err != nil {
					t.Error(err)
				}
				if metadata.AccountUUID != tc.value {
					t.Errorf("wire account_uuid = %q, want configured %q", metadata.AccountUUID, tc.value)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, anthropicFixtureStream("end_turn", `null`))
			}))
			defer srv.Close()
			c := New(&ClientConfig{Endpoint: srv.URL, Provider: "anthropic", Model: "fixture", Credential: fixedCredential{token: "fixture"}, ClaudeCode: p})
			if _, err := c.Chat(t.Context(), []Message{{Role: "user", Content: "hello"}}, ChatOptions{}); (err == nil) != tc.valid {
				t.Fatalf("valid=%v: Chat error = %v", tc.valid, err)
			}
			wantRequests := int32(0)
			if tc.valid {
				wantRequests = 1
			}
			if requests.Load() != wantRequests {
				t.Errorf("requests = %d, want %d", requests.Load(), wantRequests)
			}
			if p.AccountUUID != tc.value || c.claudeCode.AccountUUID != tc.value {
				t.Fatal("admission rewrote the caller's or client's account_uuid")
			}
		})
	}
}
