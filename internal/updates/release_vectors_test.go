package updates

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

type releaseVectorSet struct {
	SchemaVersion int                            `json:"schema_version"`
	Root          *sigenvelope.PublicKeyEnvelope `json:"root"`
	OtherRoot     *sigenvelope.PublicKeyEnvelope `json:"other_root"`
	Signatures    map[string]json.RawMessage     `json:"signatures"`
}

//go:embed testdata/signed-release-vectors-v1.json
var rawReleaseVectors []byte

var releaseVectorsCache struct {
	once sync.Once
	set  *releaseVectorSet
	err  error
}

func releaseVectors(t *testing.T) *releaseVectorSet {
	t.Helper()
	releaseVectorsCache.once.Do(func() {
		var v releaseVectorSet
		dec := json.NewDecoder(bytes.NewReader(rawReleaseVectors))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&v); err != nil {
			releaseVectorsCache.err = fmt.Errorf("decode release verifier vectors: %w", err)
			return
		}
		if err := dec.Decode(&struct{}{}); err != io.EOF {
			releaseVectorsCache.err = fmt.Errorf("decode release verifier vectors: trailing JSON")
			return
		}
		if v.SchemaVersion != 1 || v.Root == nil || v.OtherRoot == nil {
			releaseVectorsCache.err = fmt.Errorf("release verifier vector header is incomplete")
			return
		}
		want := []string{
			"empty_source", "legacy", "linux_archive_v1", "old_archive_v1",
			"unknown_fields", "valid_archive_v123", "valid_fake_archive_v123",
		}
		if len(v.Signatures) != len(want) {
			releaseVectorsCache.err = fmt.Errorf("release verifier vectors contain %d signatures, want %d", len(v.Signatures), len(want))
			return
		}
		for _, name := range want {
			if raw := v.Signatures[name]; len(raw) == 0 || !json.Valid(raw) {
				releaseVectorsCache.err = fmt.Errorf("release verifier vector %q is absent or invalid JSON", name)
				return
			}
		}
		releaseVectorsCache.set = &v
	})
	if releaseVectorsCache.err != nil {
		t.Fatal(releaseVectorsCache.err)
	}
	return releaseVectorsCache.set
}

func releaseSignature(t *testing.T, name string) ([]byte, *sigenvelope.PublicKeyEnvelope) {
	t.Helper()
	v := releaseVectors(t)
	raw, ok := v.Signatures[name]
	if !ok {
		t.Fatalf("release verifier vector %q does not exist", name)
	}
	return raw, v.Root
}
