package genesis

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/rootlist"
)

func TestTheRing0ListIsTheOneRootThisBuildPinned(t *testing.T) {
	if sum := sha256.Sum256(pinnedRootJSON); hex.EncodeToString(sum[:]) != "9ee375d3fc7f7f3962b254669d26a47f3e0f64b07c2f917d020e7cb68165d506" {
		t.Fatalf("the pinned Ring 0 root is not the published one: sha256 %x", sum)
	}
	var want publicKeyEnvelope
	if err := json.Unmarshal(pinnedRootJSON, &want); err != nil {
		t.Fatal(err)
	}
	roots := PinnedRoots().Roots()
	if len(roots) != 1 || !reflect.DeepEqual(roots[0].Env, &want) || !roots[0].RetiredAt.IsZero() {
		t.Fatalf("the Ring 0 list is not the one root this build pinned: %d roots", len(roots))
	}
	for name, got := range map[string]*publicKeyEnvelope{
		"PinnedRoot":                            PinnedRoot(),
		"the client's Root, handed to the mint": NewClient("", "", "").Root(),
	} {
		if !reflect.DeepEqual(got, &want) {
			t.Errorf("%s is not the one pinned root: %s", name, got.KeyID)
		}
	}
	trust, err := NewClient("https://genesis.example.test", "", "").trustRoot()
	if err != nil {
		t.Fatalf("the root Ring 0 is verified under: %v", err)
	}
	if !reflect.DeepEqual(trust, &want) {
		t.Errorf("Ring 0 is verified under %s, not the one pinned root", trust.KeyID)
	}
}

// A LIST THAT DOES NOT CHAIN IS A BROKEN BUILD. The embedded list is
// checked when it is first read: a Ring 0 root after the first that the
// root before it did not endorse panics as a broken embed does, and one it
// did endorse becomes the current root, the first retired when it begins.
func TestARing0ListThatDoesNotChainIsABrokenBuild(t *testing.T) {
	role := func(keyID string) *packagetest.Role {
		r, err := packagetest.NewRole(keyID, ring0RootKeyType)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	founding, successor := role("aiii_ring0_test_founding"), role("aiii_ring0_test_successor")
	envOf := func(r *packagetest.Role) []byte {
		raw, err := json.Marshal(r.Env)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	endorsed, err := founding.Sign(rootlist.ArtifactKindEndorsement, successor.Env)
	if err != nil {
		t.Fatal(err)
	}
	selfEndorsed, err := successor.Sign(rootlist.ArtifactKindEndorsement, successor.Env)
	if err != nil {
		t.Fatal(err)
	}

	l := mustParseRoots([]rootlist.Entry{{Envelope: envOf(founding)}, {Envelope: envOf(successor), Endorsement: endorsed}})
	if l.Current().KeyID != successor.KeyID || l.Roots()[0].RetiredAt.IsZero() {
		t.Fatalf("a cross-signed successor is not the current root: %s", l.Current().KeyID)
	}

	for name, entries := range map[string][]rootlist.Entry{
		"an unendorsed impostor":                          {{Envelope: envOf(founding)}, {Envelope: envOf(successor), Endorsement: selfEndorsed}},
		"a successor with no endorsement":                 {{Envelope: envOf(founding)}, {Envelope: envOf(successor)}},
		"the shipped root, then a root it never endorsed": {{Envelope: pinnedRootJSON}, {Envelope: envOf(successor), Endorsement: endorsed}},
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				r := recover()
				if r == nil || !strings.Contains(r.(string), "broken build") {
					t.Fatalf("%s did not panic as a broken build: %v", name, r)
				}
			}()
			mustParseRoots(entries)
		})
	}
}
