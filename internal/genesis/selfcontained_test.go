package genesis

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
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

func write(t *testing.T, dir, name string, lines ...string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// .
// .
// .
// .
func genesisLine(t *testing.T, typ, pubKey, fingerprint string) string {
	t.Helper()
	payload := map[string]any{"name": "Forged"}
	if pubKey != "" {
		payload["public_key"] = pubKey
	}
	if fingerprint != "" {
		payload["fingerprint"] = fingerprint
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	ev := ledger.Event{Seq: 1, Timestamp: "2026-09-03T00:00:00Z", Type: ledger.EventType(typ), Ring: 0,
		Content: crypto.ContentHash(raw), Payload: json.RawMessage(raw), Sig: "forged"}
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// .
// .
func recordLine(payload string) string {
	ev := ledger.Event{Seq: 1, Timestamp: "2026-09-03T00:00:00Z", Type: ledger.EventRing0Genesis,
		Content: crypto.ContentHash([]byte(payload)), Payload: json.RawMessage(payload), Sig: "forged"}
	return `{"entry":` + string(ev.EntryBytes()) + `,"payload":` + payload + `,"sig":"forged"}`
}

func TestVerifySelfContainedRefusesEveryMalformedChain(t *testing.T) {
	dir := t.TempDir()

	for _, tc := range []struct {
		name string
		path string
		want string
	}{
		{
			name: "a ledger that is not there",
			path: filepath.Join(dir, "absent.jsonl"),
			want: "read ledger",
		},
		{
			name: "an empty chain",
			path: write(t, dir, "empty.jsonl"),
			want: "empty ledger",
		},
		{
			name: "a chain that does not begin with a birth",
			path: write(t, dir, "notbirth.jsonl", genesisLine(t, "belief.add", "AAAA", "ff")),
			want: "not a birth-headed chain",
		},
		{
			name: "a birth whose attestation will not parse",
			path: write(t, dir, "badpayload.jsonl", recordLine(`"not-an-object"`)),
			want: "payload must be a JSON object",
		},
		{
			name: "a birth carrying no public key",
			path: write(t, dir, "nokey.jsonl", genesisLine(t, "ring0.genesis", "", "ff")),
			want: "not self-contained",
		},
		{
			name: "a public key that is not decodable",
			path: write(t, dir, "badkey.jsonl", genesisLine(t, "ring0.genesis", "!!!not base64!!!", "ff")),
			want: "genesis public key",
		},
		{
			name: "a key that does not match its claimed fingerprint",
			path: write(t, dir, "wrongfp.jsonl", genesisLine(t, "ring0.genesis", "AAAAAAAA", "00")),
			want: "does not match its claimed fingerprint",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n, fp, err := VerifySelfContained(tc.path)
			if err == nil {
				t.Fatalf("VERIFICATION FAILED OPEN — %s was accepted as a verified identity (%d events, %s)", tc.name, n, fp)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("the refusal does not name the cause.\n  got:  %v\n  want: something containing %q", err, tc.want)
			}
			if n != 0 || fp != "" {
				t.Fatalf("a refused chain still reported %d events and identity %q", n, fp)
			}
			// .
			// .
			// .
			var failure *ledger.VerifyFailure
			if !errors.As(err, &failure) {
				t.Fatalf("the refusal is not a *ledger.VerifyFailure: %T", err)
			}
			if failure.Proved.Seq != 0 || failure.Traversed.Seq != 0 || !strings.Contains(err.Error(), "nothing was established") {
				t.Fatalf("a refusal before the walk claims something was established: %v", err)
			}
		})
	}
}
