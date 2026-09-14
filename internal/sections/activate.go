// .
// .
// .
// .
// .
// .
// .
// .

package sections

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

// .
const declMember = "section.json"

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

// .
// .
// .
// .
// .
// .
// .
// .
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

// .
// .
// .
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

// .
// .
// .
// .
// .
// .
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

// .
func removeCache(dir string) error {
	return os.RemoveAll(dir)
}

// .
// .
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
