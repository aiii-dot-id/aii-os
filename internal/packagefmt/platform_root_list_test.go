package packagefmt

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

func TestThePlatformReleaseListIsTheOneRootEveryReleaseShipped(t *testing.T) {
	raw, err := shippedFS.ReadFile("shipped/platform_release_root.json")
	if err != nil {
		t.Fatal(err)
	}
	if sum := sha256.Sum256(raw); hex.EncodeToString(sum[:]) != "d090504b021ca2e059b10d2e39c501fee601fda55a89058357bc84e2f4aad420" {
		t.Fatalf("the shipped platform_release root is not the one every release carried: sha256 %x", sum)
	}
	var want sigenvelope.PublicKeyEnvelope
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	roots := ShippedPlatformReleaseRoots().Roots()
	if len(roots) != 1 || !reflect.DeepEqual(roots[0].Env, &want) || !roots[0].RetiredAt.IsZero() {
		t.Fatalf("the platform_release list is not the one root this build ships: %d roots", len(roots))
	}
	pinned, err := PinnedOrShipped("", KeyTypePlatformRelease)
	if err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string]*sigenvelope.PublicKeyEnvelope{
		"ShippedRoots":     ShippedRoots().PlatformRelease,
		"PinnedOrShipped":  pinned,
		"the current root": ShippedPlatformReleaseRoots().Current(),
	} {
		if !reflect.DeepEqual(got, &want) {
			t.Errorf("%s is not the one shipped root: %s", name, got.KeyID)
		}
	}
}

// A LIST THAT DOES NOT CHAIN IS A BROKEN BUILD, read from embedded files as
// the shipped list is: with the root list vectors' platform roots
// (spec/rootlist/vectors), a successor its predecessor endorsed becomes
// the current root, and an impostor that endorsed itself, a successor
// whose endorsement file is missing, and a list whose roots are not
// platform_release roots panic.
func TestAShippedPlatformReleaseListThatDoesNotChainIsABrokenBuild(t *testing.T) {
	raw, err := os.ReadFile("../../spec/rootlist/vectors/root_list.json")
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Roots        map[string]json.RawMessage `json:"roots"`
		Endorsements map[string]json.RawMessage `json:"endorsements"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	files := fstest.MapFS{
		"founding.json":             {Data: v.Roots["founding"]},
		"successor.json":            {Data: v.Roots["successor"]},
		"impostor.json":             {Data: v.Roots["impostor"]},
		"successor.endorsement.sig": {Data: v.Endorsements["successor_by_founding"]},
		"impostor.endorsement.sig":  {Data: v.Endorsements["impostor_by_impostor"]},
		"ring0.json":                {Data: pinnedRing0ForTest(t)},
	}
	l := mustParseShippedList(files, []shippedListEntry{{envelope: "founding.json"}, {envelope: "successor.json", endorsement: "successor.endorsement.sig"}})
	var successor sigenvelope.PublicKeyEnvelope
	if err := json.Unmarshal(v.Roots["successor"], &successor); err != nil {
		t.Fatal(err)
	}
	if l.Current().KeyID != successor.KeyID {
		t.Fatalf("a cross-signed successor is not the current root: %s", l.Current().KeyID)
	}
	for name, list := range map[string][]shippedListEntry{
		"an unendorsed impostor":      {{envelope: "founding.json"}, {envelope: "impostor.json", endorsement: "impostor.endorsement.sig"}},
		"an endorsement file missing": {{envelope: "founding.json"}, {envelope: "successor.json", endorsement: "absent.sig"}},
		"a root of another key type":  {{envelope: "ring0.json"}},
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r, _ := recover().(string); !strings.Contains(r, "broken build") {
					t.Fatalf("%s did not panic as a broken build: %q", name, r)
				}
			}()
			mustParseShippedList(files, list)
		})
	}
}

// pinnedRing0ForTest is a root of another key type: the Ring 0 signing
// root's envelope, as genesis pins it.
func pinnedRing0ForTest(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("../genesis/ring0_root_pubkey.json")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
