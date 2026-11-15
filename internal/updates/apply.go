package updates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"os"
	"path/filepath"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

func (c *Checker) Apply(ctx context.Context) error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate running binary: %w", err)
	}
	return c.applyTo(ctx, exePath)
}

func (c *Checker) applyTo(ctx context.Context, exePath string) error {

	if !c.applying.CompareAndSwap(false, true) {
		return ErrAlreadyApplying
	}
	defer c.applying.Store(false)

	tgt, err := targetOf(exePath)
	if err != nil {
		return err
	}
	owner, err := ownerOf(c.dataDir)
	if err != nil {
		return fmt.Errorf("name this identity's data directory for the update record: %w", err)
	}

	if c.state.Snapshot("").NeedsRestart || unretiredSwap(c.dataDir) || tgt.claimedBy(owner) {
		return ErrUpdatePending
	}

	available := c.state.Available()
	if available == "" {
		return fmt.Errorf("no update available")
	}

	if err := tgt.refusedHere(available); err != nil {
		return err
	}

	if err := tgt.belowHighest(available); err != nil {
		return err
	}

	root := c.platformRoot()
	if root == nil {
		return fmt.Errorf("no platform_release root pinned — cannot verify updates")
	}

	assetName := assetName(available)
	if tgt.bundle {
		assetName = bundleAssetName(available)
	}
	sigAssetName := SigAssetName(assetName)

	c.mu.Lock()
	release := c.cachedRelease
	c.mu.Unlock()
	if release == nil {
		return fmt.Errorf("no cached release — run Check first")
	}
	if err := c.sameSource(release); err != nil {
		return err
	}

	archiveURL, sigURL, err := findAssets(release, assetName, sigAssetName)
	if err != nil {
		if tgt.bundle {

			return fmt.Errorf("%w (%v)", errBareBinaryIntoBundle(tgt.path), err)
		}
		return fmt.Errorf("find assets: %w", err)
	}

	archiveBytes, signedPayload, err := c.verifiedArchive(ctx, tgt, release, archiveURL, sigURL, assetName, available, root)
	if err != nil {
		return err
	}

	c.afterVerified()

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("update not installed: %w", err)
	}

	if err := packagefmt.CheckReleaseRevocation(c.revocationRoots(root), artifactKindReleaseSig, signedPayload); err != nil {
		return fmt.Errorf("update not installed: release REFUSED at the last look: %w", err)
	}
	handled, already, err := applyIfBundle(tgt, archiveBytes, c.dataDir, owner, available)
	if err != nil {
		return fmt.Errorf("bundle update: %w", err)
	}
	if !handled {
		if already, err = installBinary(tgt, archiveBytes, c.dataDir, owner, available); err != nil {
			return err
		}
	}

	c.state.setInstalled(available)
	if already {
		logsink.Info("updates.decision", "%s is already the image on disk — nothing replaced, nothing backed up; restart to apply", available)
		return nil
	}
	logsink.Info("updates.end", "installed %s — restart to apply", available)
	return nil
}

func (c *Checker) verifiedArchive(ctx context.Context, tgt target, release *githubRelease, archiveURL, sigURL, assetName, available string, root *sigenvelope.PublicKeyEnvelope) ([]byte, json.RawMessage, error) {

	archiveBytes, err := c.download(ctx, release.source, archiveURL)
	if err != nil {
		return nil, nil, fmt.Errorf("download archive: %w", err)
	}

	sigBytes, err := c.download(ctx, release.source, sigURL)
	if err != nil {
		return nil, nil, fmt.Errorf("download signature: %w", err)
	}

	archiveHash := sha256hex(archiveBytes)

	hostPlatform, hostArch := hostTarget()
	signedPayload, err := verifyReleaseSig(sigBytes, root, archiveHash, available, hostPlatform, hostArch)
	if err != nil {
		return nil, nil, fmt.Errorf("signature verification REFUSED: %w", err)
	}

	if err := packagefmt.CheckReleaseRevocation(c.revocationRoots(root), artifactKindReleaseSig, signedPayload); err != nil {
		return nil, nil, fmt.Errorf("release REFUSED: %w", err)
	}

	logsink.Info("updates.decision", "signature verified for %s (hash %s)", assetName, archiveHash[:12])
	verified := highestVerified{
		Version: available, Platform: hostPlatform, Arch: hostArch, ArchiveSHA256: archiveHash,
		VerifiedAt: time.Now().UTC(), ReleasePublishedAt: release.PublishedAt,
	}

	if err := tgt.secondArchive(verified); err != nil {
		return nil, nil, err
	}

	tgt.noteVerified(verified)

	return archiveBytes, signedPayload, nil
}

func (c *Checker) revocationRoots(root *sigenvelope.PublicKeyEnvelope) packagefmt.TrustRoots {
	c.mu.Lock()
	held := c.trustSet
	c.mu.Unlock()
	if held != nil {
		if set := held(); set != nil {
			return packagefmt.TrustRoots{PlatformRelease: root, Revocation: set}
		}
	}
	return packagefmt.ReleaseTrustRoots(filepath.Join(c.dataDir, "trust"), root)
}

func (c *Checker) lookAtOwnRelease(ctx context.Context) {
	root := c.platformRoot()
	if root == nil || (c.isMobile != nil && c.isMobile()) {
		c.state.setOwn(false, "")
		return
	}
	roots := c.revocationRoots(root)
	if roots.Revocation.Entries(packagefmt.KeyTypePlatformRelease) == 0 {
		c.state.setOwn(false, "")
		return
	}
	signed, err := c.ownReleasePayload(ctx, root)
	if err != nil {
		c.state.setOwn(false, err.Error())
		return
	}
	err = packagefmt.CheckReleaseRevocation(roots, artifactKindReleaseSig, signed)
	var refusal *packagefmt.Error
	switch {
	case err == nil:
		c.state.setOwn(false, "")
	case errors.As(err, &refusal) && refusal.Reason == packagefmt.ReasonTrustPayloadRevoked:
		if snap := c.state.Snapshot(""); !snap.RunningWithdrawn {
			logsink.Warn("updates.decision", "the release this program came from (%s) is withdrawn by its publisher: %v", c.currentVer(), err)
		}
		c.state.setOwn(true, "")
	default:
		c.state.setOwn(false, err.Error())
	}
}

func (c *Checker) ownReleasePayload(ctx context.Context, root *sigenvelope.PublicKeyEnvelope) (json.RawMessage, error) {
	c.mu.Lock()
	cached := c.ownSigned
	c.mu.Unlock()
	if cached != nil {
		return cached, nil
	}
	current := c.currentVer()
	own := c.ownRelease
	if own == nil {
		own = func(ctx context.Context) (*githubRelease, error) { return c.fetchRelease(ctx, "tags/v"+current) }
	}
	release, err := own(ctx)
	if err != nil {
		return nil, fmt.Errorf("the release %s could not be read: %w", current, err)
	}
	name := assetName(current)
	if exe, err := os.Executable(); err == nil {
		if tgt, err := targetOf(exe); err == nil && tgt.bundle {
			name = bundleAssetName(current)
		}
	}
	_, sigURL, err := findAssets(release, name, SigAssetName(name))
	if err != nil {
		return nil, fmt.Errorf("the release %s does not carry this install's signed asset: %w", current, err)
	}
	sig, err := c.download(ctx, release.source, sigURL)
	if err != nil {
		return nil, fmt.Errorf("the signature of %s could not be fetched: %w", name, err)
	}
	platform, arch := hostTarget()
	signed, payload, err := decodeReleaseSig(artifactKindReleaseSig, sig, root)
	if err == nil && (payload.Version != current || payload.Platform != platform || payload.Arch != arch) {
		err = fmt.Errorf("it binds %s %s/%s, this program is %s %s/%s", payload.Version, payload.Platform, payload.Arch, current, platform, arch)
	}
	if err != nil {
		return nil, fmt.Errorf("the signature of %s is refused: %w", name, err)
	}
	c.mu.Lock()
	c.ownSigned = signed
	c.mu.Unlock()
	return signed, nil
}

func (c *Checker) SetTrustSet(held func() *packagefmt.RevocationStatusSet) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.trustSet = held
}
