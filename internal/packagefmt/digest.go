package packagefmt

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"hash"

	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

// .
// .
type packageDigest struct {
	agg   hash.Hash
	files int
	// .
	// .
	// .
	perFile map[string]string
}

func newPackageDigest() *packageDigest {
	return &packageDigest{agg: sha256.New(), perFile: make(map[string]string)}
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func (d *packageDigest) addFile(relPath string, fileSum [sha256.Size]byte) {
	hexDigest := hex.EncodeToString(fileSum[:])
	d.agg.Write([]byte(relPath))
	d.agg.Write([]byte{0})
	d.agg.Write([]byte(hexDigest))
	d.agg.Write([]byte{'\n'})
	d.perFile[relPath] = hexDigest
	d.files++
}

// .
func (d *packageDigest) sum() string {
	return "sha256:" + hex.EncodeToString(d.agg.Sum(nil))
}

// .
// .
// .
// .
// .
// .
func manifestHash(raw []byte) (string, *Error) {
	// .
	// .
	canonical, err := canonicaljson.CanonicalizeV1(raw)
	if err != nil {
		return "", fail(ReasonManifestInvalid, "manifest-hash", "manifest is not canonicalizable JSON: %v", err)
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(canonical, &members); err != nil {
		return "", fail(ReasonManifestInvalid, "manifest-hash", "manifest is not a JSON object: %v", err)
	}
	delete(members, "package_hash")
	stripped, err := json.Marshal(members)
	if err != nil {
		return "", fail(ReasonManifestInvalid, "manifest-hash", "manifest re-serialization failed: %v", err)
	}
	view, err := canonicaljson.CanonicalizeV1(stripped)
	if err != nil {
		return "", fail(ReasonManifestInvalid, "manifest-hash", "manifest-hash view not canonicalizable: %v", err)
	}
	return sigenvelope.SHA256Prefixed(view), nil
}
