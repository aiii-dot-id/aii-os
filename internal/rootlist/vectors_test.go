package rootlist_test

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/rootlist"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

const (
	vectorsPath   = "../../spec/rootlist/vectors/root_list.json"
	vectorKeyType = "platform_release"
	vectorKind    = "rootlist.vector"
)

type vectorEntry struct {
	Root        string `json:"root"`
	Endorsement string `json:"endorsement,omitempty"`
}

type vectorList struct {
	Name    string            `json:"name"`
	Entries []vectorEntry     `json:"entries"`
	Accept  bool              `json:"accept"`
	Current string            `json:"current,omitempty"`
	Retired map[string]string `json:"retired,omitempty"`
	Why     string            `json:"why"`
}

type vectorSet struct {
	SchemaVersion int                        `json:"schema_version"`
	KeyType       string                     `json:"key_type"`
	Kind          string                     `json:"kind"`
	Roots         map[string]json.RawMessage `json:"roots"`
	Lists         []vectorList               `json:"lists"`
	Endorsements  map[string]json.RawMessage `json:"endorsements"`
	Signatures    map[string]json.RawMessage `json:"signatures"`
}

// vectorLists are the lists the vectors hold, as the file says them.
var vectorLists = []vectorList{
	{Name: "founding-alone", Entries: []vectorEntry{{Root: "founding"}}, Accept: true, Current: "founding",
		Why: "one root, as every build pinned before the list: it is current and nothing is retired"},
	{Name: "cross-signed-successor", Entries: []vectorEntry{{Root: "founding"}, {Root: "successor", Endorsement: "successor_by_founding"}},
		Accept: true, Current: "successor", Retired: map[string]string{"founding": "2025-01-01T00:00:00Z"},
		Why: "the successor is endorsed by the root before it; the founding root is retired when the successor's validity begins"},
	{Name: "unendorsed-impostor", Entries: []vectorEntry{{Root: "founding"}, {Root: "impostor", Endorsement: "impostor_by_impostor"}},
		Why: "the impostor endorsed itself; nothing in the list endorsed it"},
	{Name: "impostor-without-endorsement", Entries: []vectorEntry{{Root: "founding"}, {Root: "impostor"}},
		Why: "every root after the first carries its predecessor's endorsement"},
	{Name: "impostor-with-anothers-endorsement", Entries: []vectorEntry{{Root: "founding"}, {Root: "impostor", Endorsement: "successor_by_founding"}},
		Why: "the founding root endorsed the successor's envelope, not the impostor's"},
	{Name: "reversed", Entries: []vectorEntry{{Root: "successor"}, {Root: "founding", Endorsement: "founding_by_successor"}},
		Why: "a root cannot begin before the root that endorsed it: the order is the order of succession"},
	{Name: "listed-twice", Entries: []vectorEntry{{Root: "founding"}, {Root: "founding"}},
		Why: "a key id is listed once"},
	{Name: "endorsed-first-root", Entries: []vectorEntry{{Root: "successor", Endorsement: "successor_by_founding"}},
		Why: "the first root is pinned as code; an endorsement there names a root the list does not hold"},
	{Name: "empty", Entries: []vectorEntry{},
		Why: "a list names at least one root"},
}

func readVectors(t *testing.T) *vectorSet {
	t.Helper()
	raw, err := os.ReadFile(vectorsPath)
	if err != nil {
		t.Fatal(err)
	}
	var v vectorSet
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("decode the root list vectors: %v", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		t.Fatal("the root list vectors carry trailing JSON")
	}
	if v.SchemaVersion != 1 || v.KeyType != vectorKeyType || v.Kind != vectorKind || len(v.Roots) != 3 || len(v.Endorsements) != 3 || len(v.Signatures) != 3 {
		t.Fatalf("the root list vector header is not the one these tests read: %d %q %q, %d roots, %d endorsements, %d signatures",
			v.SchemaVersion, v.KeyType, v.Kind, len(v.Roots), len(v.Endorsements), len(v.Signatures))
	}
	return &v
}

func (v *vectorSet) entries(t *testing.T, list vectorList) []rootlist.Entry {
	t.Helper()
	out := []rootlist.Entry{}
	for _, e := range list.Entries {
		env, ok := v.Roots[e.Root]
		if !ok {
			t.Fatalf("%s names a root the vectors do not hold: %s", list.Name, e.Root)
		}
		entry := rootlist.Entry{Envelope: env}
		if e.Endorsement != "" {
			if entry.Endorsement, ok = v.Endorsements[e.Endorsement]; !ok {
				t.Fatalf("%s names an endorsement the vectors do not hold: %s", list.Name, e.Endorsement)
			}
		}
		out = append(out, entry)
	}
	return out
}

func (v *vectorSet) keyID(t *testing.T, name string) string {
	t.Helper()
	var env sigenvelope.PublicKeyEnvelope
	if err := json.Unmarshal(v.Roots[name], &env); err != nil {
		t.Fatal(err)
	}
	return env.KeyID
}

// The file holds the lists this package's tests name, and no others: a
// list changed in one place and not the other is a refusal here.
func TestTheVectorsHoldTheListsTheTestsName(t *testing.T) {
	v := readVectors(t)
	got, _ := json.Marshal(v.Lists)
	want, _ := json.Marshal(vectorLists)
	if !bytes.Equal(got, want) {
		t.Fatalf("the vectors' lists are not the tests':\nfile  %s\ntests %s", got, want)
	}
}

// EACH LIST IS ACCEPTED OR REFUSED AS THE VECTORS SAY: a successor endorsed
// by the root before it is accepted, and the founding root is then retired
// when the successor begins; an impostor nobody endorsed, one with no
// endorsement, one carrying another's, a reversed list, a root listed
// twice, an endorsed first root and an empty list are refused.
func TestEachVectorListIsAcceptedOrRefusedAsTheVectorsSay(t *testing.T) {
	v := readVectors(t)
	for _, list := range v.Lists {
		t.Run(list.Name, func(t *testing.T) {
			l, err := rootlist.Parse(v.KeyType, v.entries(t, list))
			if !list.Accept {
				if err == nil {
					t.Fatalf("a list the vectors refuse was accepted (%s)", list.Why)
				}
				return
			}
			if err != nil {
				t.Fatalf("a list the vectors accept was refused (%s): %v", list.Why, err)
			}
			if got := l.Current().KeyID; got != v.keyID(t, list.Current) {
				t.Errorf("the current root is %s, want %s", got, list.Current)
			}
			roots := l.Roots()
			if len(roots) != len(list.Entries) {
				t.Fatalf("%d roots listed, want %d", len(roots), len(list.Entries))
			}
			for i, r := range roots {
				name := list.Entries[i].Root
				want := list.Retired[name]
				switch {
				case want == "" && !r.RetiredAt.IsZero():
					t.Errorf("%s is retired at %s, and the vectors retire it never", name, r.RetiredAt)
				case want != "" && r.RetiredAt.Format(time.RFC3339) != want:
					t.Errorf("%s is retired at %v, want %s", name, r.RetiredAt, want)
				}
			}
		})
	}
}

// A NEW SIGNATURE VERIFIES UNDER THE CURRENT ROOT ONLY; HISTORY UNDER THE
// LISTED ROOT THAT SIGNED IT, FOR A TIME THAT ROOT COVERED. In the list of
// a founding root and its cross-signed successor, the successor's
// signature verifies under the current root and the retired founding
// root's does not; the founding root is still found as the signer of what
// it signed, and covers a time before its retirement and not after; the
// impostor signed nothing the list holds a root for.
func TestARetiredRootVerifiesHistoryAndNoNewSignature(t *testing.T) {
	v := readVectors(t)
	l, err := rootlist.Parse(v.KeyType, v.entries(t, vectorLists[1]))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sigenvelope.VerifyPayload(v.Signatures["by_successor"], l.Current(), v.Kind, crypto.ProfileRoot); err != nil {
		t.Fatalf("the current root's signature does not verify under the current root: %v", err)
	}
	if _, err := sigenvelope.VerifyPayload(v.Signatures["by_founding"], l.Current(), v.Kind, crypto.ProfileRoot); err == nil {
		t.Fatal("A RETIRED ROOT'S SIGNATURE VERIFIED AS A NEW ONE under the current root")
	}

	signer, ok := l.Signer(v.Signatures["by_founding"])
	if !ok || signer.Env.KeyID != v.keyID(t, "founding") || signer.RetiredAt.IsZero() {
		t.Fatalf("the retired founding root is not found as the signer of what it signed: %+v (%v)", signer, ok)
	}
	if _, err := sigenvelope.VerifyPayload(v.Signatures["by_founding"], signer.Env, v.Kind, crypto.ProfileRoot); err != nil {
		t.Fatalf("history signed by the retired root does not verify under it: %v", err)
	}
	before, after := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC), time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	if !signer.Covers(before) || signer.Covers(after) {
		t.Errorf("the retired root covers %v: %v, and %v after its retirement: %v; want true, false", before, signer.Covers(before), after, signer.Covers(after))
	}
	if cur, _ := l.Signer(v.Signatures["by_successor"]); !cur.Covers(after) || !cur.RetiredAt.IsZero() {
		t.Errorf("the current root does not cover %v: %+v", after, cur)
	}
	if r, ok := l.Signer(v.Signatures["by_impostor"]); ok {
		t.Errorf("the impostor's signature was given a listed root: %s", r.Env.KeyID)
	}
	if l.IsCurrent(signer.Env) || !l.IsCurrent(l.Current()) {
		t.Error("IsCurrent does not tell the current root from the retired one")
	}
}

// A substitute that claims the current root's key id with other keys is
// not the current root, and a copy handed out cannot change the list.
func TestTheCurrentRootIsItsKeysNotItsName(t *testing.T) {
	v := readVectors(t)
	l, err := rootlist.Parse(v.KeyType, v.entries(t, vectorLists[1]))
	if err != nil {
		t.Fatal(err)
	}
	var impostor sigenvelope.PublicKeyEnvelope
	if err := json.Unmarshal(v.Roots["impostor"], &impostor); err != nil {
		t.Fatal(err)
	}
	impostor.KeyID = l.Current().KeyID
	if l.IsCurrent(&impostor) {
		t.Fatal("a substitute that only claims the current root's key id was taken for it")
	}
	c := l.Current()
	c.Keys[0].PublicKeyB64 = "changed"
	c.KeyID = "changed"
	if l.Current().KeyID == "changed" || l.Current().Keys[0].PublicKeyB64 == "changed" {
		t.Fatal("a copy of the current root changed the list")
	}
}

// A list holds roots of its own key type only: the vectors' platform
// roots are no list of Ring 0 roots.
func TestTheListHoldsRootsOfItsOwnKeyType(t *testing.T) {
	v := readVectors(t)
	if _, err := rootlist.Parse("ring0", v.entries(t, vectorLists[0])); err == nil || !strings.Contains(err.Error(), "key_type") {
		t.Fatalf("a root of another key type was listed: %v", err)
	}
}

// A ROOT IS READ FROM THE BYTES THE ENDORSEMENT SIGNS: its canonical
// form. Read as written, a member spelled in another case after the real
// one ("KEY_TYPE" after "key_type") was the root's key type, and before it
// was not: one list read two ways by an order nothing signs. In the
// canonical form "KEY_TYPE" sorts before the real member, so the root is
// read as the founding root it is, in either order.
func TestARootIsReadAsItsCanonicalFormWhateverOrderItsMembersComeIn(t *testing.T) {
	v := readVectors(t)
	founding := bytes.TrimRight(v.Roots["founding"], " \n")
	other := `"KEY_TYPE":"ring0"`
	for _, before := range []bool{false, true} {
		doc := append([]byte("{"+other+","), founding[1:]...)
		if !before {
			doc = append(append([]byte(nil), founding[:len(founding)-1]...), []byte(","+other+"}")...)
		}
		l, err := rootlist.Parse(v.KeyType, []rootlist.Entry{{Envelope: doc}})
		if err != nil {
			t.Fatalf("before=%v: the founding root, read from its canonical form, is a %s root; the list was refused: %v", before, v.KeyType, err)
		}
		if got := l.Current().KeyType; got != v.KeyType {
			t.Fatalf("before=%v: the root is read as a %s root", before, got)
		}
	}
}
