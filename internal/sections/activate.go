package sections

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

const declMember = "section.json"

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

func loadVerifiedMember(pkgPath string, res *packagefmt.Result, rel string) ([]byte, error) {
	want, ok := res.FileDigests[rel]
	if !ok {
		return nil, &TamperError{Member: rel}
	}
	raw, err := packagefmt.ReadMember(pkgPath, rel)
	if err != nil {
		return nil, &TamperError{Member: rel, Want: want, Err: err}
	}
	if want, got, ok := res.MemberDigest(rel, raw); !ok {
		return nil, &TamperError{Member: rel, Want: want, Got: got}
	}
	return raw, nil
}

func extractVerified(pkgPath string, res *packagefmt.Result, dir string) error {
	rels := make([]string, 0, len(res.FileDigests))
	for rel := range res.FileDigests {
		if !cleanEntryPath(rel) {
			return &TamperError{Member: rel, Want: res.FileDigests[rel]}
		}
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	written := make(map[string]bool, len(rels))
	err := packagefmt.ReadMembers(pkgPath, rels, func(rel string, raw []byte) error {
		written[rel] = true
		if want, got, ok := res.MemberDigest(rel, raw); !ok {
			return &TamperError{Member: rel, Want: want, Got: got}
		}
		dst := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return fmt.Errorf("sections: extract %s: %w", rel, err)
		}
		if err := os.WriteFile(dst, raw, 0o600); err != nil {
			return fmt.Errorf("sections: extract %s: %w", rel, err)
		}
		return nil
	})

	var walk *packagefmt.Error
	if errors.As(err, &walk) {
		for _, rel := range rels {
			if !written[rel] {
				return &TamperError{Member: rel, Want: res.FileDigests[rel], Err: err}
			}
		}
	}
	return err
}

func removeCache(dir string) error {
	return os.RemoveAll(dir)
}

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
