package ledger

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestALineThatIsNotTheWritersOwnFormIsRefused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.jsonl")
	kp := testKeyPair(t)
	l, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	for i, text := range []string{"first", "second"} {
		if _, err := l.Append(EventExperienceCreate, 3, map[string]any{"id": "exp_" + text, "content": text, "n": i}, kp); err != nil {
			t.Fatal(err)
		}
	}
	l.Close()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := VerifyChain(path, kp.PublicKeyBytes(), nil); err != nil || n != 2 {
		t.Fatalf("the written ledger: n=%d err=%v", n, err)
	}
	lines := strings.SplitAfter(strings.TrimSuffix(string(raw), "\n"), "\n")
	second := strings.TrimSuffix(lines[1], "\n")
	ev, err := decodeEvent([]byte(second))
	if err != nil {
		t.Fatal(err)
	}
	entry, payload, sig := string(ev.EntryBytes()), string(ev.Payload), `"`+ev.Sig+`"`
	for name, respelled := range map[string]string{
		"members reordered":          `{"payload":` + payload + `,"entry":` + entry + `,"sig":` + sig + `}`,
		"a space after a colon":      `{"entry": ` + entry + `,"payload":` + payload + `,"sig":` + sig + `}`,
		"a space between":            `{"entry":` + entry + ` ,"payload":` + payload + `,"sig":` + sig + `}`,
		"a member twice":             `{"entry":` + entry + `,"payload":` + payload + `,"sig":"AAAA","sig":` + sig + `}`,
		"an escaped character":       `{"entry":` + entry + `,"payload":` + payload + `,"\u0073ig":` + sig + `}`,
		"an empty proof written out": `{"entry":` + entry + `,"payload":` + payload + `,"sig":""}`,
	} {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "ledger.jsonl")
			if err := os.WriteFile(p, []byte(lines[0]+respelled+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := VerifyChain(p, kp.PublicKeyBytes(), nil)
			if !errors.Is(err, ErrNotCanonical) || !strings.Contains(err.Error(), "the line is not the canonical form") {
				t.Fatalf("a respelled line was not refused as not canonical: %v", err)
			}
			if _, err := ReadAll(p); !errors.Is(err, ErrNotCanonical) {
				t.Fatalf("the reader took a respelled line: %v", err)
			}
		})
	}
	// The record as written, beside it, still reads.
	if _, err := decodeEvent(bytes.TrimSpace([]byte(second))); err != nil {
		t.Fatalf("the written line: %v", err)
	}
}
