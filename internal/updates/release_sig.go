package updates

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

func verifyReleaseSig(sigBytes []byte, root *sigenvelope.PublicKeyEnvelope, archiveHash, version, platform, arch string) (json.RawMessage, error) {
	return verifySigOfKind(artifactKindReleaseSig, sigBytes, root, archiveHash, version, platform, arch)
}

func verifySigOfKind(kind string, sigBytes []byte, root *sigenvelope.PublicKeyEnvelope, archiveHash, version, platform, arch string) (json.RawMessage, error) {
	raw, payload, err := decodeReleaseSig(kind, sigBytes, root)
	if err != nil {
		return nil, err
	}
	if payload.ArchiveHash != archiveHash {
		return nil, fmt.Errorf("signature binds archive_hash %s, recomputed %s — mismatch", payload.ArchiveHash, archiveHash)
	}
	if payload.Version != version {
		return nil, fmt.Errorf("signature binds version %q, this release claims %q — relabel refused", payload.Version, version)
	}
	if payload.Platform != platform || payload.Arch != arch {
		return nil, fmt.Errorf("signature binds platform/arch %s/%s, this host selected %s/%s — cross-platform replay refused", payload.Platform, payload.Arch, platform, arch)
	}

	return raw, nil
}

func decodeReleaseSig(kind string, sigBytes []byte, root *sigenvelope.PublicKeyEnvelope) (json.RawMessage, releaseArchivePayload, error) {

	if err := packagefmt.ValidatePlatformReleaseRoot(root); err != nil {
		return nil, releaseArchivePayload{}, fmt.Errorf("release signature root refused: %w", err)
	}

	raw, err := sigenvelope.VerifyPayload(sigBytes, root, kind, crypto.ProfileRoot)
	if err != nil {
		return nil, releaseArchivePayload{}, fmt.Errorf("signature does not verify against platform root: %w", err)
	}

	var payload releaseArchivePayload
	if err := canonicaljson.DecodeStrict(raw, &payload); errors.Is(err, canonicaljson.ErrTrailingContent) {
		return nil, releaseArchivePayload{}, fmt.Errorf("trailing data after payload object")
	} else if err != nil {
		return nil, releaseArchivePayload{}, fmt.Errorf("signature payload is not the closed {archive_hash} object: %w", err)
	}

	if payload.Version == "" {
		return nil, releaseArchivePayload{}, fmt.Errorf("signature payload carries no bound version — pre-binding format (before 2026-08-26); re-sign the release with the full manifest payload")
	}
	if payload.SourceRev == "" {
		return nil, releaseArchivePayload{}, fmt.Errorf("signature payload carries no source_rev — the signer must state build provenance")
	}
	return raw, payload, nil
}

func sha256hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func imageHash(path string) (string, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !fi.IsDir() {
		return fileHash(path)
	}
	h := sha256.New()
	err = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(path, p)
		if err != nil {
			return err
		}
		var content string
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			content, err = os.Readlink(p)
		case info.Mode().IsRegular():
			content, err = fileHash(p)
		}
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(h, "%q %s %q\n", filepath.ToSlash(rel), info.Mode(), content)
		return err
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func fileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
