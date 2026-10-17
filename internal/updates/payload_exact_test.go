package updates

import (
	"strings"
	"testing"
)

func TestAReleasePayloadIsReadExactly(t *testing.T) {
	s := newTestReleaseSigner(t)
	hash := sha256hex([]byte("an archive"))
	verify := func(change func(payload map[string]any)) error {
		payload := map[string]any{"archive_hash": hash, "version": "v1.2.3", "platform": "linux", "arch": "amd64", "source_rev": "cafebabe1234"}
		change(payload)
		_, err := verifyReleaseSig(s.signReleasePayload(t, payload), s.env, hash, "v1.2.3", "linux", "amd64")
		return err
	}
	if err := verify(func(map[string]any) {}); err != nil {
		t.Fatalf("the payload as the contract spells it is refused, so no refusal below is the mutation's: %v", err)
	}
	for name, change := range map[string]func(payload map[string]any){
		"a member named in another case": func(p map[string]any) {
			delete(p, "version")
			p["Version"] = "v1.2.3"
		},
		"a member in both spellings": func(p map[string]any) {
			p["Version"] = "v1.2.3"
		},
		// The member named exactly binds another release. The folded one
		// comes later in the payload's bytes, and it was the one read.
		"a member matched by folding, after the exact one": func(p map[string]any) {
			p["version"] = "v9.9.9"
			p["ver\u017fion"] = "v1.2.3"
		},
	} {
		err := verify(change)
		if err == nil {
			t.Errorf("%s: the signature verified", name)
			continue
		}
		for _, want := range []string{"is not the closed {archive_hash} object", "must match its field exactly and appear once"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: refused, but not as %q: %v", name, want, err)
			}
		}
	}
}
