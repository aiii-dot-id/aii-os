package witness

import (
	"crypto/rand"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/rootlist"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

func TestAPersistedManifestVerifiesUnderTheRetiredRootThatSignedIt(t *testing.T) {
	now := time.Now().UTC()
	founding, successor, impostor := synthPlatform(t), synthPlatform(t), synthPlatform(t)
	founding.env.NotBefore = now.Add(-48 * time.Hour).Format(time.RFC3339)
	successor.env.NotBefore = now.Add(-24 * time.Hour).Format(time.RFC3339) // the founding root's retirement
	list := listOf(t, founding, successor)
	was := shippedRoots
	t.Cleanup(func() { shippedRoots = was })
	shippedRoots = func() *rootlist.List { return list }
	current := list.Current()

	fw := newFakeWitness(t)
	load := func(t *testing.T, manifest []byte, platform *PublicKeyEnvelope) error {
		t.Helper()
		dir := t.TempDir()
		if err := persistWitnessKey(dir, fw.witnessEnv.KeyID, manifest, mustCanonical(t, fw.witnessEnv), nil); err != nil {
			t.Fatal(err)
		}
		keys, err := LoadWitnessKeys(dir, platform)
		if err == nil && keys[fw.witnessEnv.KeyID] == nil {
			t.Fatal("the load answered no error and no key")
		}
		return err
	}
	beforeRetirement := now.Add(-36 * time.Hour)

	t.Run("the current root's manifest", func(t *testing.T) {
		if err := load(t, buildManifest(t, successor, fw.witnessEnv, false), current); err != nil {
			t.Fatalf("a manifest the current root signed was refused: %v", err)
		}
	})
	t.Run("the retired root's, created while it was current", func(t *testing.T) {
		if err := load(t, buildManifestCreated(t, founding, fw.witnessEnv, false, beforeRetirement), current); err != nil {
			t.Fatalf("A MANIFEST THE RETIRED ROOT SIGNED WHILE CURRENT WAS REFUSED: a sealed history would go to SAFE: %v", err)
		}
	})
	t.Run("the retired root's, created after its retirement", func(t *testing.T) {
		err := load(t, buildManifestCreated(t, founding, fw.witnessEnv, false, now.Add(-time.Hour)), current)
		if err == nil || !strings.Contains(err.Error(), "did not cover") {
			t.Fatalf("a manifest the retired root signed after its retirement was not refused for it: %v", err)
		}
	})
	t.Run("an impostor's", func(t *testing.T) {
		if err := load(t, buildManifestCreated(t, impostor, fw.witnessEnv, false, beforeRetirement), current); err == nil {
			t.Fatal("A MANIFEST NO LISTED ROOT SIGNED VOUCHED FOR A WITNESS KEY")
		}
	})
	t.Run("the retired root's, tampered", func(t *testing.T) {
		if err := load(t, buildManifestCreated(t, founding, fw.witnessEnv, true, beforeRetirement), current); err == nil {
			t.Fatal("a tampered manifest loaded under the retired root")
		}
	})
	// An operator's own platform file is not the root this build ships:
	// it is asked alone, and the list lends it nothing, though the list
	// holds the retired root that signed the manifest.
	t.Run("under an operator's own root", func(t *testing.T) {
		if err := load(t, buildManifestCreated(t, founding, fw.witnessEnv, false, beforeRetirement), impostor.env); err == nil {
			t.Fatal("a root the build does not ship took history from a list it is not the current root of")
		}
	})
	t.Run("under the shipped list of another build", func(t *testing.T) {
		shippedRoots = genesis.PinnedRoots
		t.Cleanup(func() { shippedRoots = func() *rootlist.List { return list } })
		if err := load(t, buildManifestCreated(t, founding, fw.witnessEnv, false, beforeRetirement), current); err == nil {
			t.Fatal("a manifest verified under a retired root no shipped list holds")
		}
	})
}

// WITH TODAY'S ONE ROOT NOTHING CHANGES: the list this build ships is the
// one pinned root, current and never retired, so a manifest verifies under
// it alone, as before; one any other root signed is refused.
func TestWithTheShippedListOfOneRootAManifestVerifiesAsBefore(t *testing.T) {
	roots := shippedRoots().Roots()
	if len(roots) != 1 || !roots[0].RetiredAt.IsZero() || !shippedRoots().IsCurrent(genesis.PinnedRoot()) {
		t.Fatalf("the shipped Ring 0 list is not today's one root: %d roots", len(roots))
	}
	f := persistedFixture(t)
	if _, err := LoadWitnessKeys(f.dir, genesis.PinnedRoot()); err == nil {
		t.Fatal("a manifest a test root signed verified under the shipped root")
	}
	if _, err := LoadWitnessKeys(f.dir, f.p.env); err != nil {
		t.Fatalf("a manifest verified under the root that signed it, named as the platform: %v", err)
	}
}

// listOf is the list of the given roots, oldest first, each after the first
// endorsed by the one before it.
func listOf(t *testing.T, roots ...*platformKeys) *rootlist.List {
	t.Helper()
	var entries []rootlist.Entry
	for i, r := range roots {
		env, err := json.Marshal(r.env)
		if err != nil {
			t.Fatal(err)
		}
		e := rootlist.Entry{Envelope: env}
		if i > 0 {
			e.Endorsement = signAsPlatform(t, roots[i-1], rootlist.ArtifactKindEndorsement, r.env)
		}
		entries = append(entries, e)
	}
	l, err := rootlist.Parse(roots[0].env.KeyType, entries)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// signAsPlatform signs payload as kind with both of a platform's keys.
func signAsPlatform(t *testing.T, p *platformKeys, kind string, payload any) []byte {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := canonicaljson.CanonicalizeV1(raw)
	if err != nil {
		t.Fatal(err)
	}
	sum := sigenvelope.SHA256Prefixed(canonical)
	var sigs []sigenvelope.SignatureEntry
	for _, alg := range []string{AlgMLDSA87, AlgSLHDSASHA2256} {
		mat, _ := p.env.FindPublicKey(alg)
		in := sigenvelope.SignatureInput(kind, ProfileRoot, alg, p.env.KeyID, mat.PublicKeyFingerprint, sum)
		var sig []byte
		if alg == AlgMLDSA87 {
			sig, err = crypto.Sign(p.mlDsaKp, []byte(in))
		} else {
			sig, err = p.slhSk.Sign(rand.Reader, []byte(in), nil)
		}
		if err != nil {
			t.Fatal(err)
		}
		sigs = append(sigs, sigenvelope.SignatureEntry{Alg: alg, KeyID: p.env.KeyID, PublicKeyFingerprint: mat.PublicKeyFingerprint,
			SignatureInputSHA256: sigenvelope.SHA256Prefixed([]byte(in)), SigB64: base64Encode(sig)})
	}
	out, err := json.Marshal(sigenvelope.Envelope{ArtifactKind: kind, Payload: canonical, PayloadSHA256: sum,
		Canonicalization: sigenvelope.CanonicalizationV1, SignatureProfile: ProfileRoot, Signatures: sigs})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
