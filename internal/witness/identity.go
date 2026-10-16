package witness

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

func readFileTrimmed(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return []byte(strings.TrimSpace(string(data))), nil
}

type EnvelopeStore interface {
	SaveWitnessEnvelope(canonicalJSON []byte) error
	ReplaceWitnessEnvelope(canonicalJSON []byte) error
	LoadWitnessEnvelope() ([]byte, error)

	WitnessEnrollment() (ids []string, born, firstWitnessed time.Time, err error)
}

var (
	ErrEnvelopeUnrecoverable = errors.New("the witness envelope the record names cannot be rebuilt from the record")

	ErrEnrollmentForked = errors.New("the record names more than one witness identity")

	ErrNotTheRecordedIdentity = errors.New("the witness envelope names a different identity than the record's receipts")
)

const enrollmentSlack = 24 * time.Hour

const maxRecoverySeconds = 400 * 24 * 60 * 60

type IdentityKey interface {
	Fingerprint() string
	PublicKeyB64() string
	Sign(message []byte) ([]byte, error)
}

type keyPairAdapter struct{ kp *crypto.KeyPair }

func (a keyPairAdapter) Fingerprint() string           { return a.kp.Fingerprint() }
func (a keyPairAdapter) PublicKeyB64() string          { return a.kp.PublicKeyB64() }
func (a keyPairAdapter) Sign(m []byte) ([]byte, error) { return crypto.Sign(a.kp, m) }

func AsIdentityKey(kp *crypto.KeyPair) IdentityKey { return keyPairAdapter{kp} }

const envelopeExpiry = 100 * 365 * 24 * time.Hour

func EnsureIdentityEnvelope(key IdentityKey, store EnvelopeStore, ledgerDir string) (canonical []byte, env *PublicKeyEnvelope, err error) {
	ids, born, firstWitnessed, err := store.WitnessEnrollment()
	if err != nil {
		return nil, nil, fmt.Errorf("read the record's witness enrollment: %w", err)
	}
	if len(ids) > 1 {
		return nil, nil, fmt.Errorf("%w: %s", ErrEnrollmentForked, strings.Join(ids, ", "))
	}
	evidence := "the record"
	pending, err := readPendingReceipt(ledgerDir)
	if err != nil {
		return nil, nil, fmt.Errorf("the receipt pending beside the ledger: %w", err)
	}
	if pending != nil {
		switch {
		case len(ids) == 0:
			ids, firstWitnessed = []string{pending.IdentityID}, pending.writtenAt
			evidence = "the receipt pending beside the ledger"
		case ids[0] != pending.IdentityID:
			return nil, nil, fmt.Errorf("%w: the record names %s, the receipt pending beside the ledger names %s", ErrEnrollmentForked, ids[0], pending.IdentityID)
		}
	}
	existing, err := store.LoadWitnessEnvelope()
	if err != nil {
		return nil, nil, fmt.Errorf("load witness envelope: %w", err)
	}
	if existing != nil {
		canonical, env, err = parseIdentityEnvelope(existing)
		if err != nil {
			if len(ids) == 0 {

				return nil, nil, fmt.Errorf("stored witness envelope invalid: %w", err)
			}
			logsink.Warn("witness.refusal", "the stored witness envelope is invalid (%v) — rebuilding the envelope %s names", err, evidence)
		} else if len(ids) == 0 {
			return canonical, env, nil
		} else if id, err := DeriveIdentityID(canonical, env); err != nil {
			return nil, nil, err
		} else if id == ids[0] {
			return canonical, env, nil
		} else {
			logsink.Warn("witness.refusal", "the stored witness envelope names %s, %s names %s — the row is not this identity's envelope; rebuilding that one", id, evidence, ids[0])
		}
	}
	if len(ids) == 0 {

		if canonical, _, err = buildIdentityEnvelope(key, time.Now()); err != nil {
			return nil, nil, err
		}
		if err := store.SaveWitnessEnvelope(canonical); err != nil {
			return nil, nil, fmt.Errorf("persist witness envelope: %w", err)
		}

		stored, err := store.LoadWitnessEnvelope()
		if err != nil {
			return nil, nil, fmt.Errorf("load witness envelope: %w", err)
		}
		if stored == nil {
			return nil, nil, errors.New("the witness envelope was saved and is not there")
		}
		return parseIdentityEnvelope(stored)
	}
	if born.IsZero() || firstWitnessed.IsZero() {
		return nil, nil, fmt.Errorf("%w: %s (named by %s) — the record gives no enrollment window", ErrEnvelopeUnrecoverable, ids[0], evidence)
	}
	canonical, env, err = recoverIdentityEnvelope(key, ids[0], born, firstWitnessed)
	if err != nil {
		return nil, nil, err
	}
	if existing != nil {
		err = store.ReplaceWitnessEnvelope(canonical)
	} else {
		err = store.SaveWitnessEnvelope(canonical)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("persist the recovered witness envelope: %w", err)
	}
	logsink.Info("witness.decision", "witness envelope rebuilt from %s: %s, enrolled %s", evidence, ids[0], env.CreatedAt)
	return canonical, env, nil
}

func buildIdentityEnvelope(key IdentityKey, created time.Time) ([]byte, *PublicKeyEnvelope, error) {
	created = created.UTC()
	keyID := "aiii_identity_" + key.Fingerprint()[:16]
	env := &PublicKeyEnvelope{
		V:         1,
		Kind:      PublicKeyEnvelopeKind,
		KeyID:     keyID,
		KeyType:   "identity",
		Profile:   ProfileRoot,
		CreatedAt: created.Format(time.RFC3339),
		NotBefore: created.Add(-time.Minute).Format(time.RFC3339),
		ExpiresAt: created.Add(envelopeExpiry).Format(time.RFC3339),
		Keys: []PublicKeyMaterial{{
			Alg:                  AlgMLDSA87,
			PublicKeyB64:         key.PublicKeyB64(),
			PublicKeyFingerprint: sigenvelope.PublicKeyFingerprint(AlgMLDSA87, keyID, key.PublicKeyB64()),
		}},
	}
	raw, err := jsonMarshal(env)
	if err != nil {
		return nil, nil, err
	}
	canonical, err := canonicaljson.CanonicalizeV1(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("canonicalize identity envelope: %w", err)
	}
	return canonical, env, nil
}

type envelopeCandidates struct {
	parts       [4][]byte
	fingerprint string
}

var errNoTemplate = errors.New("the canonical envelope does not hold its three times once each in canonical order")

func envelopeTimes(created time.Time) [3]string {
	created = created.UTC()
	return [3]string{created.Format(time.RFC3339), created.Add(envelopeExpiry).Format(time.RFC3339), created.Add(-time.Minute).Format(time.RFC3339)}
}

func newEnvelopeCandidates(key IdentityKey) (*envelopeCandidates, error) {
	ref := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)
	canonical, env, err := buildIdentityEnvelope(key, ref)
	if err != nil {
		return nil, err
	}
	ml, ok := env.FindPublicKey(AlgMLDSA87)
	if !ok {
		return nil, errNoTemplate
	}
	c := &envelopeCandidates{fingerprint: ml.PublicKeyFingerprint}
	rest := canonical
	for i, field := range [3]string{"created_at", "expires_at", "not_before"} {
		value := envelopeTimes(ref)[i]
		needle := []byte(`"` + field + `":"` + value + `"`)
		at := bytes.Index(rest, needle)
		if at < 0 || bytes.Count(canonical, needle) != 1 {
			return nil, errNoTemplate
		}
		at += len(field) + 4
		c.parts[i] = rest[:at:at]
		rest = rest[at+len(value):]
	}
	c.parts[3] = rest
	for _, probe := range []time.Time{ref.Add(time.Second), ref.Add(97*24*time.Hour + 13*time.Second)} {
		want, _, err := buildIdentityEnvelope(key, probe)
		if err != nil || !bytes.Equal(c.canonical(nil, probe), want) {
			return nil, errNoTemplate
		}
	}
	return c, nil
}

func (c *envelopeCandidates) canonical(buf []byte, created time.Time) []byte {
	created = created.UTC()
	buf = append(buf[:0], c.parts[0]...)
	buf = created.AppendFormat(buf, time.RFC3339)
	buf = append(buf, c.parts[1]...)
	buf = created.Add(envelopeExpiry).AppendFormat(buf, time.RFC3339)
	buf = append(buf, c.parts[2]...)
	buf = created.Add(-time.Minute).AppendFormat(buf, time.RFC3339)
	return append(buf, c.parts[3]...)
}

func recoverIdentityEnvelope(key IdentityKey, did string, born, firstWitnessed time.Time) ([]byte, *PublicKeyEnvelope, error) {
	candidates, err := newEnvelopeCandidates(key)
	if err != nil {
		logsink.Warn("witness.error", "the envelope template could not be proven (%v) — searching through the builder, more slowly", err)
		candidates = nil
	}
	return searchIdentityEnvelope(key, did, born, firstWitnessed, candidates)
}

func searchIdentityEnvelope(key IdentityKey, did string, born, firstWitnessed time.Time, candidates *envelopeCandidates) ([]byte, *PublicKeyEnvelope, error) {
	from := born.Add(-enrollmentSlack).UTC().Truncate(time.Second)
	pivot := firstWitnessed.UTC().Truncate(time.Second)
	to := firstWitnessed.Add(enrollmentSlack).UTC().Truncate(time.Second)
	below := int64(pivot.Sub(from)/time.Second) + 1
	above := int64(to.Sub(pivot) / time.Second)
	if below <= 0 {
		return nil, nil, fmt.Errorf("%w: %s — the first witness record (%s) is before the genesis record (%s)",
			ErrEnvelopeUnrecoverable, did, firstWitnessed.Format(time.RFC3339), born.Format(time.RFC3339))
	}
	span := below + above
	if span > maxRecoverySeconds {
		return nil, nil, fmt.Errorf("%w: %s — the enrollment window from %s to %s is longer than %d days, not searched; restore the witness_identity row from a copy of the database that holds it",
			ErrEnvelopeUnrecoverable, did, from.Format(time.RFC3339), to.Format(time.RFC3339), maxRecoverySeconds/86400)
	}
	second := func(i int64) time.Time {
		if i < below {
			return pivot.Add(-time.Duration(i) * time.Second)
		}
		return pivot.Add(time.Duration(i-below+1) * time.Second)
	}
	logsink.Info("witness.decision", "rebuilding the witness envelope %s from the record: searching up to %d seconds from %s down, then above", did, span, pivot.Format(time.RFC3339))
	matches := func(buf []byte, at time.Time) ([]byte, bool, error) {
		if candidates != nil {
			buf = candidates.canonical(buf, at)
			return buf, identityIDFor(candidates.fingerprint, buf) == did, nil
		}
		canonical, env, err := buildIdentityEnvelope(key, at)
		if err != nil {
			return buf, false, err
		}
		id, err := DeriveIdentityID(canonical, env)
		return buf, err == nil && id == did, err
	}
	var found atomic.Int64
	var failOnce sync.Once
	var failed error
	workers := runtime.GOMAXPROCS(0)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			var buf []byte
			for i := int64(w); i < span && found.Load() == 0; i += int64(workers) {
				var hit bool
				var err error
				buf, hit, err = matches(buf, second(i))
				if hit {
					found.CompareAndSwap(0, i+1)
					return
				}
				if err != nil {
					failOnce.Do(func() { failed = err })
					found.CompareAndSwap(0, -1)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	switch n := found.Load(); {
	case n > 0:

		canonical, env, err := buildIdentityEnvelope(key, second(n-1))
		if err != nil {
			return nil, nil, err
		}
		if id, err := DeriveIdentityID(canonical, env); err != nil || id != did {
			return nil, nil, fmt.Errorf("rebuild the witness envelope: the builder's envelope at %s does not name %s (%v)", env.CreatedAt, did, err)
		}
		return canonical, env, nil
	case n < 0:
		return nil, nil, fmt.Errorf("rebuild the witness envelope: %w", failed)
	default:
		return nil, nil, fmt.Errorf("%w: no envelope this key minted between %s and %s hashes to %s; restore the witness_identity row from a copy of the database that holds it",
			ErrEnvelopeUnrecoverable, from.Format(time.RFC3339), to.Format(time.RFC3339), did)
	}
}

func parseIdentityEnvelope(canonical []byte) ([]byte, *PublicKeyEnvelope, error) {
	env := &PublicKeyEnvelope{}
	if err := jsonUnmarshalStrict(canonical, env); err != nil {
		return nil, nil, err
	}
	if env.KeyType != "identity" || env.Kind != PublicKeyEnvelopeKind {
		return nil, nil, fmt.Errorf("envelope is not an identity key envelope")
	}
	if _, ok := env.FindPublicKey(AlgMLDSA87); !ok {
		return nil, nil, fmt.Errorf("envelope has no ML-DSA-87 key")
	}
	return canonical, env, nil
}

func DeriveIdentityID(canonicalEnvelope []byte, env *PublicKeyEnvelope) (string, error) {
	ml, ok := env.FindPublicKey(AlgMLDSA87)
	if !ok {
		return "", fmt.Errorf("identity envelope missing ML-DSA-87 key")
	}
	return identityIDFor(ml.PublicKeyFingerprint, canonicalEnvelope), nil
}

func identityIDFor(mlFingerprint string, canonicalEnvelope []byte) string {
	material := IdentityIDMaterial(mlFingerprint, sha256Prefixed(canonicalEnvelope))
	return "did:aiii:identity:sha256:" + strings.TrimPrefix(sha256Prefixed([]byte(material)), HashPrefixSHA256)
}

func SignRequest(key IdentityKey, env *PublicKeyEnvelope, req WitnessRequest, canonicalEnvelope []byte) (SignatureEntry, error) {
	return SignInput(key, env, RequestSignatureInput(req, canonicalEnvelope))
}

func SignInput(key IdentityKey, env *PublicKeyEnvelope, input []byte) (SignatureEntry, error) {
	sig, err := key.Sign(input)
	if err != nil {
		return SignatureEntry{}, fmt.Errorf("sign request: %w", err)
	}
	ml, _ := env.FindPublicKey(AlgMLDSA87)
	return SignatureEntry{
		SignatureProfile:     ProfileFast,
		Alg:                  AlgMLDSA87,
		KeyID:                env.KeyID,
		PublicKeyFingerprint: ml.PublicKeyFingerprint,
		SignatureInputSHA256: sha256Prefixed(input),
		SigB64:               base64Encode(sig),
	}, nil
}
