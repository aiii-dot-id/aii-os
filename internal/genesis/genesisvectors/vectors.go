// Package genesisvectors owns the immutable public verifier fixtures shared by
// genesis's internal tests and the external genesistest helper. It contains no
// private key material and is not imported by production code.
package genesisvectors

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

// Set is the complete public input needed by the hermetic genesis verifier
// tests. Signed envelopes remain raw so production decoders see their exact
// wire representation.
type Set struct {
	SchemaVersion int `json:"schema_version"`

	Root  *sigenvelope.PublicKeyEnvelope `json:"root"`
	Ring0 map[string]json.RawMessage     `json:"ring0"`

	BootstrapDomain    *sigenvelope.PublicKeyEnvelope `json:"bootstrap_domain"`
	BootstrapKeyBundle json.RawMessage                `json:"bootstrap_key_bundle"`
	BootstrapPackets   map[string]json.RawMessage     `json:"bootstrap_packets"`

	ForeignRoot  *sigenvelope.PublicKeyEnvelope `json:"foreign_root"`
	ForeignRing0 map[string]json.RawMessage     `json:"foreign_ring0"`
	InvalidRing0 map[string]json.RawMessage     `json:"invalid_ring0"`

	Ring5Domain    *sigenvelope.PublicKeyEnvelope `json:"ring5_domain"`
	Ring5KeyBundle json.RawMessage                `json:"ring5_key_bundle"`
	Ring5Bundle    []byte                         `json:"ring5_bundle"`
	Ring5Manifest  json.RawMessage                `json:"ring5_manifest"`
}

//go:embed testdata/signed-genesis-vectors-v1.json
var rawVectors []byte

var (
	loadOnce sync.Once
	loaded   *Set
	loadErr  error
)

// Load decodes the embedded vector set once. Callers must treat the returned
// public envelopes and bytes as immutable.
func Load() (*Set, error) {
	loadOnce.Do(func() {
		var v Set
		dec := json.NewDecoder(bytes.NewReader(rawVectors))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&v); err != nil {
			loadErr = fmt.Errorf("decode genesis verifier vectors: %w", err)
			return
		}
		if err := dec.Decode(&struct{}{}); err != io.EOF {
			loadErr = fmt.Errorf("decode genesis verifier vectors: trailing JSON")
			return
		}
		if err := validate(&v); err != nil {
			loadErr = err
			return
		}
		loaded = &v
	})
	return loaded, loadErr
}

func validate(v *Set) error {
	if v.SchemaVersion != 1 {
		return fmt.Errorf("genesis verifier vector schema_version = %d, want 1", v.SchemaVersion)
	}
	if v.Root == nil || v.ForeignRoot == nil || v.BootstrapDomain == nil || v.Ring5Domain == nil {
		return fmt.Errorf("genesis verifier vectors omit a public key envelope")
	}
	if len(v.Ring0) == 0 || len(v.ForeignRing0) == 0 || len(v.InvalidRing0) == 0 || len(v.BootstrapPackets) == 0 {
		return fmt.Errorf("genesis verifier vectors omit a signed fixture family")
	}
	for name, raw := range map[string]json.RawMessage{
		"bootstrap_key_bundle": v.BootstrapKeyBundle,
		"ring5_key_bundle":     v.Ring5KeyBundle,
		"ring5_bundle":         json.RawMessage(v.Ring5Bundle),
		"ring5_manifest":       v.Ring5Manifest,
	} {
		if len(raw) == 0 || !json.Valid(raw) {
			return fmt.Errorf("genesis verifier vector %s is absent or invalid JSON", name)
		}
	}
	return nil
}
