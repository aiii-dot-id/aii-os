// Section activation: verify the package (the standard packagefmt
// path, T0 minimum), extract the install-root into a host-owned cache
// with per-file digest re-verification, parse the declaration. The
// extraction rule is pluginhost's loadVerifiedMember discipline made
// total: verification never materializes artifacts, so extraction is a
// second pass over the file — and the digest comparison on EVERY file
// is what turns a package swapped between the passes into a typed
// refusal instead of served bytes.

package sections

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

// declMember is the install-root file that makes an asset a section.
const declMember = "section.json"

// ActivateFromPackage verifies the .aiiospkg at pkgPath and, when it is
// a kind=asset package carrying a section.json, extracts its
// install-root into a fresh cache directory and returns the Section.
// Lane signals for the caller's activation loop:
//
//	ErrNotAsset        — verified fine, kind=plugin: use the plugin lane
//	ErrAssetNotSection — verified fine, asset without section.json: skip with a log
//	anything else      — a typed refusal; the package activates as nothing
//
// All-or-nothing like plugin activation: any refusal removes the cache.
func ActivateFromPackage(pkgPath string, roots packagefmt.TrustRoots) (*Section, error) {
	res, err := packagefmt.VerifyFile(pkgPath, roots)
	if err != nil {
		return nil, err
	}
	if res.Manifest.Kind != "asset" {
		return nil, ErrNotAsset
	}
	if _, present := res.FileDigests[declMember]; !present {
		return nil, ErrAssetNotSection
	}

	declRaw, err := loadVerifiedMember(pkgPath, res, declMember)
	if err != nil {
		return nil, err
	}
	decl, err := ParseDecl(declRaw)
	if err != nil {
		return nil, err
	}
	if _, present := res.FileDigests[decl.Entry]; !present {
		return nil, &DeclError{Field: "entry", Reason: fmt.Sprintf("%q is not a file in the package install-root", decl.Entry)}
	}

	dir, err := os.MkdirTemp("", "aii-section-"+sanitizeCacheToken(res.Manifest.ID)+"-")
	if err != nil {
		return nil, fmt.Errorf("sections: cache dir: %w", err)
	}
	if err := extractVerified(pkgPath, res, dir); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	return &Section{Decl: *decl, Dir: dir, PackageID: res.Manifest.ID}, nil
}

// ActivateDev builds a dev-serve section from an operator-named
// directory (config plugins.dev_section — the co-edit loop, UI_FRAME.md
// §3). NO verification BY DESIGN: the declaration still meets the full
// schema bar (a typo'd slot refuses in dev too), the id must match the
// operator's config statement (a dev directory must not quietly claim a
// different identity than the one configured), and the Section is
// marked Dev so the frame banners it, the server disables caching, and
// SAFE refuses it entirely.
func ActivateDev(id, dir string) (*Section, error) {
	raw, err := os.ReadFile(filepath.Join(dir, declMember))
	if err != nil {
		return nil, fmt.Errorf("sections: dev section %q: %w", id, err)
	}
	decl, err := ParseDecl(raw)
	if err != nil {
		return nil, err
	}
	if decl.ID != id {
		return nil, &DeclError{Field: "id", Reason: fmt.Sprintf("declares %q but config plugins.dev_section names %q — the operator's statement must match the directory", decl.ID, id)}
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(decl.Entry))); err != nil {
		return nil, &DeclError{Field: "entry", Reason: fmt.Sprintf("%q: %v", decl.Entry, err)}
	}
	return &Section{Decl: *decl, Dir: dir, Dev: true}, nil
}

// loadVerifiedMember extracts one install-root member and enforces the
// verified-bytes invariant against the Result's digest (the pluginhost
// discipline, restated here so this package carries its own wall).
func loadVerifiedMember(pkgPath string, res *packagefmt.Result, rel string) ([]byte, error) {
	want, ok := res.FileDigests[rel]
	if !ok {
		return nil, &TamperError{Member: rel}
	}
	raw, err := packagefmt.ReadMember(pkgPath, rel)
	if err != nil {
		return nil, &TamperError{Member: rel, Want: want}
	}
	sum := sha256.Sum256(raw)
	if got := "sha256:" + hex.EncodeToString(sum[:]); got != want {
		return nil, &TamperError{Member: rel, Want: want, Got: got}
	}
	return raw, nil
}

// extractVerified writes every install-root member into dir, digest-
// checking each against the verified Result on the way (tamper between
// the verify pass and this pass = typed refusal). Member paths are
// fenced with the same rule the declaration meets — the verified
// grammar already refuses hostile paths, but this extractor writes to
// disk, so it carries its own belt.
func extractVerified(pkgPath string, res *packagefmt.Result, dir string) error {
	for rel := range res.FileDigests {
		if !cleanEntryPath(rel) {
			return &TamperError{Member: rel, Want: res.FileDigests[rel]}
		}
		raw, err := loadVerifiedMember(pkgPath, res, rel)
		if err != nil {
			return err
		}
		dst := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return fmt.Errorf("sections: extract %s: %w", rel, err)
		}
		if err := os.WriteFile(dst, raw, 0o600); err != nil {
			return fmt.Errorf("sections: extract %s: %w", rel, err)
		}
	}
	return nil
}

// removeCache deletes an extraction cache (Section.Close).
func removeCache(dir string) error {
	return os.RemoveAll(dir)
}

// sanitizeCacheToken keeps cache-dir names filesystem-sane (the
// pluginhost sanitizer's rule).
func sanitizeCacheToken(s string) string {
	out := []byte(s)
	for i := 0; i < len(out); i++ {
		c := out[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-') {
			out[i] = '_'
		}
	}
	return string(out)
}
