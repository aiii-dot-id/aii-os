package rootlist

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

const ArtifactKindEndorsement = "root.endorsement"

type Entry struct {
	Envelope    []byte
	Endorsement []byte
}

type Root struct {
	Env       *sigenvelope.PublicKeyEnvelope
	RetiredAt time.Time
}

func (r Root) Covers(t time.Time) bool {
	if sigenvelope.ValidWindow(r.Env, t) != nil {
		return false
	}
	return r.RetiredAt.IsZero() || t.Before(r.RetiredAt)
}

type List struct {
	roots []Root
}

func Parse(keyType string, entries []Entry) (*List, error) {
	if len(entries) == 0 {
		return nil, errors.New("the list names no root")
	}
	l := &List{roots: make([]Root, 0, len(entries))}
	seen := map[string]bool{}
	for i, e := range entries {

		var env sigenvelope.PublicKeyEnvelope
		if err := sigenvelope.Decode(e.Envelope, &env); err != nil {
			return nil, fmt.Errorf("root %d does not parse: %w", i, err)
		}
		if err := sigenvelope.ValidatePublicKeyEnvelopeShape(&env, crypto.ProfileRoot); err != nil {
			return nil, fmt.Errorf("root %d (%s): %w", i, env.KeyID, err)
		}
		if env.KeyType != keyType {
			return nil, fmt.Errorf("root %d (%s) declares key_type %q, want %q", i, env.KeyID, env.KeyType, keyType)
		}
		if seen[env.KeyID] {
			return nil, fmt.Errorf("root %d: key_id %s is listed twice", i, env.KeyID)
		}
		seen[env.KeyID] = true
		if i == 0 {
			if len(e.Endorsement) != 0 {
				return nil, fmt.Errorf("root 0 (%s) carries an endorsement: the first root is pinned as code, endorsed by nothing in the list", env.KeyID)
			}
			l.roots = append(l.roots, Root{Env: &env})
			continue
		}
		prev := &l.roots[i-1]
		if len(e.Endorsement) == 0 {
			return nil, fmt.Errorf("root %d (%s) is not endorsed by %s, the root before it", i, env.KeyID, prev.Env.KeyID)
		}
		if err := endorses(prev.Env, e.Endorsement, e.Envelope); err != nil {
			return nil, fmt.Errorf("root %d (%s) is not endorsed by %s, the root before it: %w", i, env.KeyID, prev.Env.KeyID, err)
		}
		begins, err := time.Parse(time.RFC3339, env.NotBefore)
		if err != nil {
			return nil, fmt.Errorf("root %d (%s) names no not_before, so it cannot say when %s was retired", i, env.KeyID, prev.Env.KeyID)
		}
		if !prev.Covers(begins) {
			return nil, fmt.Errorf("root %d (%s) begins at %s, outside the validity of %s, the root before it", i, env.KeyID, env.NotBefore, prev.Env.KeyID)
		}
		prev.RetiredAt = begins
		l.roots = append(l.roots, Root{Env: &env})
	}
	return l, nil
}

func endorses(by *sigenvelope.PublicKeyEnvelope, endorsement, envelope []byte) error {
	payload, err := sigenvelope.VerifyPayload(endorsement, by, ArtifactKindEndorsement, crypto.ProfileRoot)
	if err != nil {
		return err
	}
	got, err := canonicaljson.CanonicalizeV1(payload)
	if err != nil {
		return err
	}
	want, err := canonicaljson.CanonicalizeV1(envelope)
	if err != nil {
		return err
	}
	if !bytes.Equal(got, want) {
		return errors.New("the endorsement signs another envelope")
	}
	return nil
}

func (l *List) Current() *sigenvelope.PublicKeyEnvelope {
	return clone(l.roots[len(l.roots)-1].Env)
}

func (l *List) Roots() []Root {
	out := make([]Root, len(l.roots))
	for i, r := range l.roots {
		out[i] = Root{Env: clone(r.Env), RetiredAt: r.RetiredAt}
	}
	return out
}

func (l *List) IsCurrent(env *sigenvelope.PublicKeyEnvelope) bool {
	return env != nil && sameKeys(env, l.roots[len(l.roots)-1].Env)
}

func (l *List) Signer(bundle []byte) (Root, bool) {
	var env struct {
		Signatures []struct {
			KeyID string `json:"key_id"`
		} `json:"signatures"`
	}
	if json.Unmarshal(bundle, &env) != nil || len(env.Signatures) == 0 {
		return Root{}, false
	}
	for _, r := range l.roots {
		if r.Env.KeyID == env.Signatures[0].KeyID {
			return Root{Env: clone(r.Env), RetiredAt: r.RetiredAt}, true
		}
	}
	return Root{}, false
}

func sameKeys(a, b *sigenvelope.PublicKeyEnvelope) bool {
	if a.KeyID != b.KeyID || len(a.Keys) != len(b.Keys) {
		return false
	}
	for _, k := range a.Keys {
		if m, ok := b.FindPublicKey(k.Alg); !ok || m != k {
			return false
		}
	}
	return true
}

func clone(env *sigenvelope.PublicKeyEnvelope) *sigenvelope.PublicKeyEnvelope {
	c := *env
	c.Keys = slices.Clone(env.Keys)
	return &c
}
