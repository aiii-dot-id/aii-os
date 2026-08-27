package packagefmt

// Digest computation per PACKAGE_DIGEST.md (the digest owner).
//
//   package_hash — SHA-256 aggregate over every regular file under
//     install-root/, in lexicographic byte order of normalized relative
//     paths: update(relpath + "\0"); update(lowercase_hex(file_sha256) +
//     "\n"). The hex-not-raw choice is pinned by the C-stack reference
//     (aiiospkg.py package_digest — the doc's "file.digest" pseudocode
//     is ambiguous; the implementation is not).
//   manifest_hash — SHA-256 of the AIII-CANONICAL-JSON-V1 bytes of the
//     manifest with the top-level package_hash member omitted
//     (PACKAGE_DIGEST §3.5 — omitting it breaks the circular
//     exact-release definition).
//
// There is no //MANIFEST pseudo-entry and no digest exclusion
// mechanism: the curated install-root/ is the complete package-hash
// domain (§3.4).

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"hash"

	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

// packageDigest accumulates the install-root aggregate while members
// stream past in canonical (= digest) order.
type packageDigest struct {
	agg   hash.Hash
	files int
	// perFile keeps each install-root file's hex digest for the §7
	// step-8 variant artifact_hash check — 1,024 members maximum, so
	// this stays small by construction.
	perFile map[string]string
}

func newPackageDigest() *packageDigest {
	return &packageDigest{agg: sha256.New(), perFile: make(map[string]string)}
}

// addFile folds one install-root regular file into the aggregate.
// relPath is the normalized path relative to install-root/; members
// arrive in canonical archive order, which within the install-root
// subtree is exactly PACKAGE_DIGEST §2.4 sorted order. That is a
// theorem, not an assumption: §2.4 orders by lexicographic UTF-8 BYTE
// order of relative paths (the C reference sorts by
// canonical_relative_path_bytes, aiiospkg.py:866), the archive orders
// by the same bytewise comparison over full paths, and every
// install-root file shares the identical "<root>/install-root/"
// prefix — so full-path order reduces to relpath order. It would break
// only if either side ever stopped being a plain byte comparison;
// TestDigestSubtreeOrderTheorem pins that with separator-interleaving
// names ("a-c" < "a.d" < "a/b").
func (d *packageDigest) addFile(relPath string, fileSum [sha256.Size]byte) {
	hexDigest := hex.EncodeToString(fileSum[:])
	d.agg.Write([]byte(relPath))
	d.agg.Write([]byte{0})
	d.agg.Write([]byte(hexDigest))
	d.agg.Write([]byte{'\n'})
	d.perFile[relPath] = hexDigest
	d.files++
}

// sum returns sha256:<hex> over the aggregate.
func (d *packageDigest) sum() string {
	return "sha256:" + hex.EncodeToString(d.agg.Sum(nil))
}

// manifestHash computes the §3.5 manifest content hash from the raw
// manifest bytes. The canonicalize→strip→re-canonicalize shape is
// deliberate, not redundant: json.Marshal over map[string]RawMessage
// does NOT emit canonical bytes, so the second CanonicalizeV1 is what
// makes the hash a function of the CANONICAL form (§3.5) rather than
// of Go's map serialization. Do not "simplify" either pass away.
func manifestHash(raw []byte) (string, *Error) {
	// Canonicalize first: this validates the JSON grammar (including
	// duplicate-key rejection) before any member is dropped.
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
