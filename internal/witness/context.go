package witness

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
)

const contextDomain = "AIII-WITNESS-CONTEXT-V1\n"

func ContextDigest(context []byte) (string, error) {
	canonical, err := canonicaljson.CanonicalizeV1(context)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(append([]byte(contextDomain), canonical...))
	return HashPrefixSHA256 + hex.EncodeToString(sum[:]), nil
}

type headTuple struct {
	Ordinal int64
	Hash    string
}

type witnessContext struct {
	digest        string
	targetKeyHash string

	expected      string
	expectedTuple *headTuple

	predecessor *contextPredecessor
}

type contextPredecessor struct {
	Receipt        json.RawMessage `json:"receipt"`
	PublicKey      json.RawMessage `json:"public_key"`
	SignedManifest json.RawMessage `json:"signed_manifest"`
}

func readContext(raw []byte) (*witnessContext, error) {
	canonical, err := canonicaljson.CanonicalizeV1(raw)
	if err != nil {
		return nil, fmt.Errorf("not canonical JSON: %w", err)
	}
	var c struct {
		V             *int            `json:"v"`
		TargetKeyHash string          `json:"target_key_hash"`
		ExpectedHead  json.RawMessage `json:"expected_head"`
		Predecessor   json.RawMessage `json:"predecessor"`
	}
	if err := canonicaljson.DecodeStrict(canonical, &c); err != nil {
		return nil, err
	}
	if c.V == nil || *c.V != 1 {
		return nil, errors.New("not version 1")
	}
	if !sha256Form.MatchString(c.TargetKeyHash) {
		return nil, fmt.Errorf("target_key_hash %q is not a sha256: digest", c.TargetKeyHash)
	}
	expected, tuple, err := readExpectedHead(c.ExpectedHead)
	if err != nil {
		return nil, fmt.Errorf("expected_head: %w", err)
	}
	ctx := &witnessContext{targetKeyHash: c.TargetKeyHash, expected: expected, expectedTuple: tuple}
	if c.Predecessor != nil {

		var p contextPredecessor
		if err := canonicaljson.DecodeStrict(c.Predecessor, &p); err != nil {
			return nil, fmt.Errorf("predecessor: %w", err)
		}
		for name, member := range map[string]json.RawMessage{"receipt": p.Receipt, "public_key": p.PublicKey, "signed_manifest": p.SignedManifest} {
			if len(member) == 0 || isNull(member) {
				return nil, fmt.Errorf("predecessor: no %s", name)
			}
		}
		ctx.predecessor = &p
	}
	sum := sha256.Sum256(append([]byte(contextDomain), canonical...))
	ctx.digest = HashPrefixSHA256 + hex.EncodeToString(sum[:])
	return ctx, nil
}

func readExpectedHead(raw json.RawMessage) (string, *headTuple, error) {
	r := bytes.TrimSpace(raw)
	if len(r) == 0 {
		return "", nil, errors.New("absent")
	}
	if isNull(r) {
		return "null", nil, nil
	}
	var h struct {
		LedgerOrdinal *int64 `json:"ledger_ordinal"`
		LedgerHash    string `json:"ledger_hash"`
	}
	if err := decodeExact(r, &h); err != nil {
		return "", nil, err
	}
	if h.LedgerOrdinal == nil || *h.LedgerOrdinal < 0 || !ledgerHashForm.MatchString(h.LedgerHash) {
		return "", nil, errors.New("not a ledger ordinal and hash")
	}
	canonical, err := canonicaljson.CanonicalizeV1(r)
	if err != nil {
		return "", nil, err
	}
	return string(canonical), &headTuple{Ordinal: *h.LedgerOrdinal, Hash: h.LedgerHash}, nil
}

func isNull(raw json.RawMessage) bool { return bytes.Equal(bytes.TrimSpace(raw), []byte("null")) }

func DecodeReceipt(raw []byte) (WitnessReceipt, error) {
	canonical, err := canonicaljson.CanonicalizeV1(raw)
	if err != nil {
		return WitnessReceipt{}, fmt.Errorf("not canonical JSON: %w", err)
	}
	var r WitnessReceipt
	if err := json.Unmarshal(canonical, &r); err != nil {
		return WitnessReceipt{}, err
	}
	if err := checkContextDigestMember(canonical, r.ContextSHA256); err != nil {
		return WitnessReceipt{}, err
	}
	return r, nil
}

func checkContextDigestMember(raw []byte, digest string) error {
	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil {
		return err
	}
	if v, present := members["context_sha256"]; present && !sha256Form.MatchString(digest) {
		return fmt.Errorf("context_sha256 is present and not a sha256: digest (%s)", v)
	}
	return nil
}
