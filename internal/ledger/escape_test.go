package ledger

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
)

// .
// .
// .
// .
// .

const hostilePayloadText = "a <b>bold</b> claim & more: café, 日本語, emoji 🙂, quotes \"q\" and a backslash \\ and a tab\t"

func TestARecordLineHoldsTheCanonicalBytesForEveryCharacter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	kp := testKeyPair(t)
	l, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	// .
	// .
	l.SetModelID("vendor/model-<1> & co")
	if _, err := l.Append(EventExperienceCreate, kp.Fingerprint(), 3, map[string]interface{}{
		"id": "exp_hostile", "content": hostilePayloadText, "nested": map[string]interface{}{"k<": "v&", "z": []interface{}{"<", ">", "&"}},
	}, kp); err != nil {
		t.Fatal(err)
	}
	l.Close()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, esc := range []string{`\u003c`, `\u003e`, `\u0026`} {
		if bytes.Contains(raw, []byte(esc)) {
			t.Fatalf("the line on disk carries the HTML escape %s — not canonical bytes:\n%s", esc, raw)
		}
	}
	if !bytes.Contains(raw, []byte("<b>bold</b> claim &")) {
		t.Fatalf("the payload text is not on disk as written:\n%s", raw)
	}
	// .
	// .
	n, err := VerifyChain(path, kp.PublicKeyBytes(), nil)
	if err != nil || n != 1 {
		t.Fatalf("verify: n=%d err=%v", n, err)
	}
	events, err := ReadAll(path)
	if err != nil {
		t.Fatal(err)
	}
	canon, err := canonicaljson.CanonicalizeV1(events[0].Payload)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(canon, events[0].Payload) {
		t.Fatal("the stored payload is not its own canonical form")
	}
	if !strings.Contains(string(events[0].Payload), "日本語") {
		t.Fatal("non-ASCII was not stored raw")
	}
	// .
	l2, err := New(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer l2.Close()
	if _, err := l2.Append(EventExperienceCreate, kp.Fingerprint(), 3, map[string]interface{}{"id": "e2", "content": "x > y"}, kp); err != nil {
		t.Fatalf("append after a hostile record: %v", err)
	}
}

func TestRewrapWritesTheCanonicalBytesForEveryCharacter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	kp := testKeyPair(t)
	l, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(EventRing0Genesis, kp.Fingerprint(), 0, map[string]interface{}{
		"fingerprint": kp.Fingerprint(), "public_key": kp.PublicKeyB64(), "note": "<genesis> & more",
	}, kp); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(EventExperienceCreate, kp.Fingerprint(), 3, map[string]interface{}{
		"id": "exp_hostile", "content": hostilePayloadText,
	}, kp); err != nil {
		t.Fatal(err)
	}
	l.Close()
	out := filepath.Join(t.TempDir(), "rewrapped.jsonl")
	n, err := Rewrap(path, kp, out, func(string) error { return nil })
	if err != nil || n != 2 {
		t.Fatalf("rewrap: n=%d err=%v", n, err)
	}
	raw, _ := os.ReadFile(out)
	for _, esc := range []string{`\u003c`, `\u003e`, `\u0026`} {
		if bytes.Contains(raw, []byte(esc)) {
			t.Fatalf("the re-wrapped line carries the HTML escape %s", esc)
		}
	}
	if m, err := VerifyChain(out, kp.PublicKeyBytes(), nil); err != nil || m != 2 {
		t.Fatalf("re-wrapped chain: n=%d err=%v", m, err)
	}
}
