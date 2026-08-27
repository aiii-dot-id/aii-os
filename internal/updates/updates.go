// Package updates implements the platform self-update system.
//
// The checker runs on a governed ticker (mirroring the plugin sweep
// pattern): 1-hour cadence, parked while backgrounded (battery law),
// parked during SAFE (R55 — no outside-world operations while
// integrity is unverified). It queries the GitHub Releases API for
// the latest release, compares versions via semver, and — on desktop
// platforms with updates.automatic enabled — downloads, verifies, and
// atomically swaps the binary. On mobile or when automatic is off, it
// informs only (notification surface), never downloads.
//
// Trust reuses the existing platform_release root pinned in
// plugins.platform_root — no new signing infrastructure. The release
// archive's platform.sig is verified via sigenvelope.VerifyPayload
// with a release-specific payload type (archive_hash only — no
// manifest, which was dropped per Occam's Razor). Same root, same
// crypto profile, same revocation check. New payload shape, not new
// trust infrastructure.
//
// Recovery: a boot-health marker (.boot_completed in the data
// directory) is written after successful boot and deleted before a
// binary swap. On next boot, if aii.previous exists and the marker is
// absent, the previous binary is restored — automatic rollback from a
// bad update. The marker being absent WITHOUT aii.previous is
// harmless (first boot, or marker deleted but no update happened).
package updates

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/quiesce"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
	"github.com/aiii-dot-id/aii-os/internal/version"
)

// GitHubRepo is the release source — infrastructure, not operator
// preference (hardcoded, same as witness/sweep cadences).
const GitHubRepo = "aiii-dot-id/aii-os"

// CheckInterval is the update check cadence. Hardcoded — no genuine
// operator need for a tunable interval (same pattern as the plugin
// sweep's 4s and the witness probe's 30s).
const CheckInterval = 1 * time.Hour

// downloadTimeout bounds the archive download. A large release on a
// slow link gets generous time, but the operation must terminate.
const downloadTimeout = 10 * time.Minute

// maxDownloadSize caps the size of any single download (archive or
// signature). The binary is tens of MB at most; a response larger than
// this is either a misconfigured CDN or a deliberate memory-exhaustion
// attack. 256 MB is generous headroom.
const maxDownloadSize = 256 * 1024 * 1024

// releaseArchivePayload is the closed payload signed by the
// platform_release root for a release archive. It binds EVERY claim
// the verifier acts on — not only the bytes. With archive_hash alone,
// version/platform/arch rode unsigned GitHub release metadata, so a
// compromised release account could republish an old validly-signed
// archive under a higher version tag or another platform's asset name
// (relabel/replay — external review P1, 2026-08-26). source_rev is
// the signer's provenance claim: not yet independently checkable here
// (that needs a post-extract buildinfo cross-check), bound now so the
// payload format is final before the first public release exists.
type releaseArchivePayload struct {
	ArchiveHash string `json:"archive_hash"`
	Version     string `json:"version"`
	Platform    string `json:"platform"`
	Arch        string `json:"arch"`
	SourceRev   string `json:"source_rev"`
}

// Artifact kind for release signatures — parallel to
// plugin.platform_release in packagefmt, but distinct: this signs a
// release archive, not a plugin package.
const artifactKindReleaseSig = "release.platform_release"

// State is the checker's runtime state, surfaced to the dashboard.
// Mutex-guarded; written by the checker goroutine, read by the
// dashboard handler. Same class as witnessAttempt / pluginSkips.
type State struct {
	mu           sync.RWMutex
	availableVer string // non-empty = an update is available
	lastCheck    time.Time
	lastError    string
	installedVer string // version that was installed (after a swap)
	needsRestart bool   // swap completed; restart to apply
}

// Snapshot returns a point-in-time copy for the dashboard.
func (s *State) Snapshot(currentVersion string) StateSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return StateSnapshot{
		CurrentVersion:   currentVersion,
		AvailableVersion: s.availableVer,
		LastCheck:        s.lastCheck,
		LastError:        s.lastError,
		InstalledVersion: s.installedVer,
		NeedsRestart:     s.needsRestart,
	}
}

// StateSnapshot is the dashboard-facing copy of the update state.
type StateSnapshot struct {
	CurrentVersion   string    `json:"current_version,omitempty"`
	AvailableVersion string    `json:"available_version,omitempty"`
	LastCheck        time.Time `json:"last_check,omitempty"`
	LastError        string    `json:"last_error,omitempty"`
	InstalledVersion string    `json:"installed_version,omitempty"`
	NeedsRestart     bool      `json:"needs_restart,omitempty"`
}

// Available returns the version string if an update is available.
func (s *State) Available() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.availableVer
}

// SetAvailable records that an update is available (or clears it).
func (s *State) SetAvailable(ver string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.availableVer = ver
}

// SetLastError records the most recent check error (or clears it).
func (s *State) SetLastError(err string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastError = err
}

// SetInstalled records a successful swap and marks needs-restart.
func (s *State) SetInstalled(ver string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.installedVer = ver
	s.needsRestart = true
	s.availableVer = ""
}

// MarkChecked records a successful check (no error).
func (s *State) MarkChecked() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastError = ""
	s.lastCheck = time.Now()
}

// Checker queries the GitHub Releases API and orchestrates updates.
type Checker struct {
	state         *State
	platformRoot  func() *sigenvelope.PublicKeyEnvelope
	currentVer    func() string
	automatic     func() bool
	gate          *quiesce.Gate // parks the ticker when backgrounded (battery law)
	dataDir       string        // directory for .boot_completed, aii.previous
	httpClient    *http.Client
	mu            sync.Mutex     // guards cachedRelease
	cachedRelease *githubRelease // cached by Check, consumed by Apply
}

// NewChecker creates an update checker.
//
//   - platformRoot: returns the pinned platform_release root (nil =
//     no root pinned → fail-closed, no updates)
//   - currentVer: returns the running version (from the VERSION file
//     via ldflags)
//   - automatic: returns the operator's updates.automatic setting
//   - gate: the quiesce gate — parks the checker ticker when the app
//     is backgrounded on mobile (battery law). Mirrors the plugin
//     sweep's quiesce.NewTicker. Nil = always running (acceptable for
//     tests and desktop-only).
//   - dataDir: the data directory (filepath.Dir of ledger_path) for
//     the boot-health marker and previous-binary backup
func NewChecker(platformRoot func() *sigenvelope.PublicKeyEnvelope, currentVer func() string, automatic func() bool, gate *quiesce.Gate, dataDir string) *Checker {
	return &Checker{
		state:        &State{},
		platformRoot: platformRoot,
		currentVer:   currentVer,
		automatic:    automatic,
		gate:         gate,
		dataDir:      dataDir,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// State returns the checker's state for dashboard reads.
func (c *Checker) State() *State { return c.state }

// Check queries the GitHub Releases API for the latest release and
// compares against the running version. Returns the latest version
// string if an update is available, or empty if current. This is the
// shared path for both automatic and inform-only modes — it never
// downloads. The caller (Run) decides whether to proceed to Apply.
//
// All errors are logged and recorded in state; the function returns
// empty on failure (fail-safe: a failed check is not an update).
func (c *Checker) Check(ctx context.Context) string {
	release, err := c.fetchLatestRelease(ctx)
	if err != nil {
		log.Printf("updates: check failed: %v", err)
		c.state.SetLastError(err.Error())
		return ""
	}

	// Cache the release for Apply to consume — avoids a second API
	// call and eliminates the TOCTOU window between check and apply.
	c.mu.Lock()
	c.cachedRelease = release
	c.mu.Unlock()

	// Strip the leading "v" from the tag (GitHub convention: v0.2.0).
	latestVer := strings.TrimPrefix(release.TagName, "v")
	currentVer := c.currentVer()

	// A malformed version is not a comparison — it is an unanswerable
	// question about whether to swap a running binary. Refuse it loudly
	// rather than let a hand-rolled parse produce a confident wrong
	// answer in either direction (a missed security update, or a
	// downgrade presented as an upgrade).
	if !version.Valid(latestVer) || !version.Valid(currentVer) {
		err := fmt.Errorf("invalid semantic version: running %q, latest %q", currentVer, latestVer)
		log.Printf("updates: check failed: %v", err)
		c.state.SetLastError(err.Error())
		return ""
	}

	cmp := version.Compare(latestVer, currentVer)
	if cmp <= 0 {
		// We're current or ahead — clear any stale "available" state.
		c.state.SetAvailable("")
		c.state.MarkChecked()
		log.Printf("updates: running %s, latest %s — current", currentVer, latestVer)
		return ""
	}

	c.state.SetAvailable(latestVer)
	c.state.MarkChecked()
	log.Printf("updates: running %s, latest %s — update available", currentVer, latestVer)
	return latestVer
}

// ErrUpdatePending refuses a swap while one is already installed and
// waiting for the restart that retires it. A swap's backup is the only
// copy of the last image known to boot; a SECOND swap backs up the
// UPDATE over it and records BackupSHA256 == NewSHA256, so the rollback
// a failed boot triggers would reinstall the binary that just failed.
// Check re-arms "available" every hour until that restart happens —
// c.currentVer() is the RUNNING version, unchanged by a swap — so the
// hourly tick was on course to do exactly that.
var ErrUpdatePending = errors.New("update already installed — restart to complete it before another update can be applied")

// Apply downloads, verifies, and atomically swaps the binary for the
// available update. Desktop-only; mobile callers never reach this
// path. Returns nil on success (the swap is done; the operator
// restarts at their convenience). On ANY failure, the running binary
// is untouched — the swap is atomic or it doesn't happen.
func (c *Checker) Apply(ctx context.Context) error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate running binary: %w", err)
	}
	return c.applyTo(ctx, exePath)
}

// applyTo is Apply with the executable path injectable, so tests can
// run the REAL sequence a repeated apply runs — swap, then apply again
// — against a temp file instead of the test binary (the same seam as
// checkRollbackAt).
func (c *Checker) applyTo(ctx context.Context, exePath string) error {
	// A swap that no healthy boot has retired refuses the next one. The
	// evidence is DURABLE — the in-memory needs-restart flag dies with
	// the process and this guard has to hold for any re-entry. Refuse
	// before any download: the hourly re-download was wasted work on top
	// of a destroyed backup.
	if unretiredSwap(c.dataDir) {
		return ErrUpdatePending
	}

	available := c.state.Available()
	if available == "" {
		return fmt.Errorf("no update available")
	}

	root := c.platformRoot()
	if root == nil {
		return fmt.Errorf("no platform_release root pinned — cannot verify updates")
	}

	// Build the expected asset name from the platform coordinates.
	assetName := assetName(available)
	sigAssetName := assetName + ".platform.sig"

	// Use the release cached by Check — no second API call, no
	// TOCTOU window between check and apply.
	c.mu.Lock()
	release := c.cachedRelease
	c.mu.Unlock()
	if release == nil {
		return fmt.Errorf("no cached release — run Check first")
	}

	archiveURL, sigURL, err := findAssets(release, assetName, sigAssetName)
	if err != nil {
		return fmt.Errorf("find assets: %w", err)
	}

	// Download the archive.
	archiveBytes, err := c.download(ctx, archiveURL)
	if err != nil {
		return fmt.Errorf("download archive: %w", err)
	}

	// Download the signature.
	sigBytes, err := c.download(ctx, sigURL)
	if err != nil {
		return fmt.Errorf("download signature: %w", err)
	}

	// Verify the archive hash.
	archiveHash := sha256hex(archiveBytes)

	// Verify the signature against the pinned platform root.
	if err := verifyReleaseSig(sigBytes, root, archiveHash, available, packagefmt.HostPlatform(), packagefmt.HostArch()); err != nil {
		return fmt.Errorf("signature verification REFUSED: %w", err)
	}

	log.Printf("updates: signature verified for %s (hash %s)", assetName, archiveHash[:12])

	// Extract the new binary from the archive.
	newBinary, err := extractBinary(archiveBytes)
	if err != nil {
		return fmt.Errorf("extract binary from archive: %w", err)
	}

	// Atomic swap with rollback backup.
	if err := swapBinary(exePath, newBinary, c.dataDir); err != nil {
		return fmt.Errorf("binary swap: %w", err)
	}

	c.state.SetInstalled(available)
	log.Printf("updates: installed %s — restart to apply", available)
	return nil
}

// Run is the governed-ticker loop. Mirrors startPluginSweep: select
// on context + ticker, SAFE check before each iteration, bounded work.
// The safeCheck function returns true if SAFE is active (the caller
// provides this — the updates package doesn't import app).
func (c *Checker) Run(ctx context.Context, isSafe func() bool, isMobile func() bool) {
	tk := quiesce.NewTicker(c.gate, CheckInterval)
	defer tk.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
		}

		if isSafe() {
			continue // R55: no outside-world operations in SAFE
		}

		available := c.Check(ctx)

		// Inform-only on mobile, or when automatic is off.
		if available == "" {
			continue
		}

		mobile := isMobile()
		automatic := c.automatic()

		if mobile || !automatic {
			log.Printf("updates: %s available — inform only (mobile=%v, automatic=%v)", available, mobile, automatic)
			continue
		}

		// Desktop + automatic: download, verify, swap. A swap already
		// staged refuses until a restart retires it — that is the
		// dashboard's NeedsRestart, not a check failure, and recording it
		// as lastError every hour would render a healthy wait as a fault.
		// It is still LOGGED: the tombstone outlives the in-memory flag
		// whenever WriteBootMarker keeps both because the marker write
		// failed, and only a boot retires them — so a process can refuse
		// here forever while the dashboard shows nothing, and this line
		// is the only witness.
		if err := c.Apply(ctx); errors.Is(err, ErrUpdatePending) {
			log.Printf("updates: %s staged but not applied — %v", available, err)
		} else if err != nil {
			log.Printf("updates: apply failed: %v", err)
			c.state.SetLastError(err.Error())
		}
	}
}

// --- GitHub Releases API ---

// githubRelease is the subset of the GitHub API response we need.
type githubRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func (c *Checker) fetchLatestRelease(ctx context.Context) (*githubRelease, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", GitHubRepo)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "aii-os/"+c.currentVer())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("github API returned %d: %s", resp.StatusCode, string(body))
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("decode release: %w", err)
	}
	return &release, nil
}

func (c *Checker) download(ctx context.Context, url string) ([]byte, error) {
	dlCtx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(dlCtx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "aii-os/"+c.currentVer())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download returned %d", resp.StatusCode)
	}

	return io.ReadAll(io.LimitReader(resp.Body, maxDownloadSize))
}

// findAssets locates the archive and signature download URLs by name.
func findAssets(release *githubRelease, archiveName, sigName string) (string, string, error) {
	var archiveURL, sigURL string
	for _, a := range release.Assets {
		if a.Name == archiveName {
			archiveURL = a.BrowserDownloadURL
		}
		if a.Name == sigName {
			sigURL = a.BrowserDownloadURL
		}
	}
	if archiveURL == "" {
		return "", "", fmt.Errorf("archive asset %q not found in release %s", archiveName, release.TagName)
	}
	if sigURL == "" {
		return "", "", fmt.Errorf("signature asset %q not found in release %s", sigName, release.TagName)
	}
	return archiveURL, sigURL, nil
}

// assetName constructs the expected archive filename from the version
// and the host's platform/arch coordinates.
func assetName(version string) string {
	platform := packagefmt.HostPlatform()
	arch := packagefmt.HostArch()
	ext := "tar.gz"
	if runtime.GOOS == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf("aii-os_%s_%s_%s.%s", version, platform, arch, ext)
}

// --- Signature verification ---

// verifyReleaseSig verifies the release archive signature against the
// pinned platform_release root. The payload is the bound release
// manifest {archive_hash, version, platform, arch, source_rev}; every
// field the verifier acted on to SELECT this archive is checked
// against what the signer actually signed, so unsigned GitHub
// metadata can relabel nothing.
func verifyReleaseSig(sigBytes []byte, root *sigenvelope.PublicKeyEnvelope, archiveHash, version, platform, arch string) error {
	// Verify the signature envelope.
	raw, err := sigenvelope.VerifyPayload(sigBytes, root, artifactKindReleaseSig, crypto.ProfileRoot)
	if err != nil {
		return fmt.Errorf("signature does not verify against platform root: %w", err)
	}

	// Decode the payload with the closed-payload rule (unknown fields
	// reject, same as packagefmt.strictDecode).
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	var payload releaseArchivePayload
	if err := dec.Decode(&payload); err != nil {
		return fmt.Errorf("signature payload is not the closed {archive_hash} object: %w", err)
	}
	if dec.More() {
		return fmt.Errorf("trailing data after payload object")
	}

	if payload.Version == "" {
		return fmt.Errorf("signature payload carries no bound version — pre-binding format (before 2026-08-26); re-sign the release with the full manifest payload")
	}
	if payload.ArchiveHash != archiveHash {
		return fmt.Errorf("signature binds archive_hash %s, recomputed %s — mismatch", payload.ArchiveHash, archiveHash)
	}
	if payload.Version != version {
		return fmt.Errorf("signature binds version %q, this release claims %q — relabel refused", payload.Version, version)
	}
	if payload.Platform != platform || payload.Arch != arch {
		return fmt.Errorf("signature binds platform/arch %s/%s, this host selected %s/%s — cross-platform replay refused", payload.Platform, payload.Arch, platform, arch)
	}
	if payload.SourceRev == "" {
		return fmt.Errorf("signature payload carries no source_rev — the signer must state build provenance")
	}

	return nil
}

// --- Binary swap ---

// updatePending is the swap's tombstone: it records that a swap
// happened and which bytes are which, so later boots can tell apart
// three states that all look like "marker absent" — the updated binary
// that has not booted YET (attempts 0), the updated binary that tried
// and DIED (attempts >= 1), and debris from builds before this file
// existed (no tombstone at all).
//
// WITHOUT IT, EVERY UPDATE ROLLED ITSELF BACK. The swap deletes the
// boot marker; the marker is rewritten only at the END of a healthy
// boot; and CheckRollback runs at the START of one. So the updated
// binary's first boot always saw backup-present + marker-absent — the
// exact signature of a failed update — and restored the old binary
// over itself. The mechanism shipped green because every test built
// its state by hand and none ran the sequence a real update runs.
type updatePending struct {
	// Attempts counts boots of the updated binary that BEGAN. 0 means
	// the swap happened and nothing has booted since; 1+ means a boot
	// started and — if the marker is still absent — did not finish.
	Attempts int `json:"attempts"`
	// BackupSHA256 is the hash of aii.previous as written. A restore
	// re-verifies it: a backup that no longer hashes to this is not a
	// backup, and installing it would replace a runnable binary with
	// corruption — the safety net bricking the identity it protects.
	BackupSHA256 string `json:"backup_sha256"`
	// NewSHA256 is the hash of the binary the swap installed. If the
	// bytes at the executable path stop matching it, an operator has
	// replaced the binary by hand, and a rollback would clobber their
	// repair with the thing that already failed.
	NewSHA256 string `json:"new_sha256"`
}

const (
	markerFile   = ".boot_completed"
	previousFile = "aii.previous"
	pendingFile  = ".update_pending"
)

// writeFileAtomic is WriteFile behind a rename: the target either
// keeps its old bytes or carries all the new ones. The update
// machinery must never leave a half-written binary under a real name —
// a crash mid-backup used to leave a truncated aii.previous, and the
// NEXT boot would "restore" that truncation over a healthy binary.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func writePending(dataDir string, p updatePending) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dataDir, pendingFile), raw, 0o644)
}

// unretiredSwap reports whether a swap is installed and not yet retired
// by a healthy boot — the state in which taking another one destroys the
// only image known to boot.
//
// IT ASKS ABOUT THE ARTIFACT, not only about the tombstone, because
// readPending maps a CORRUPT tombstone to "absent" — and an empty file
// is what a truncated write leaves. Keyed on readPending alone, an
// update applied, then a power loss that zeroed the tombstone, then the
// next hourly tick, walked straight through this guard and wrote the
// update over aii.previous. The two readers of that file would also have
// disagreed: checkRollbackAt treats a corrupt tombstone beside a live
// backup as "adopt and protect the backup", so the boot path defended
// what this path was about to overwrite.
//
// It cannot over-refuse past the un-retired window: aii.previous is
// removed at every retire point — the healthy-boot marker write, both of
// checkRollbackAt's retire branches, and the operator-repair branch.
func unretiredSwap(dataDir string) bool {
	if _, err := os.Stat(filepath.Join(dataDir, previousFile)); err == nil {
		return true
	}
	_, ok := readPending(dataDir)
	return ok
}

// readPending loads the tombstone. Corrupt reads as absent: the legacy
// path below re-derives a usable one from the backup itself.
func readPending(dataDir string) (updatePending, bool) {
	raw, err := os.ReadFile(filepath.Join(dataDir, pendingFile))
	if err != nil {
		return updatePending{}, false
	}
	var p updatePending
	if err := json.Unmarshal(raw, &p); err != nil {
		return updatePending{}, false
	}
	return p, true
}

// swapBinary performs the binary replacement, leaving behind everything
// a later boot needs to judge the update: the backup (atomic, hashed)
// and the tombstone. The order is chosen so that a crash between any
// two steps leaves a state some boot can walk out of:
//
//	after the backup, before the tombstone: marker still present, so
//	  the next boot retires the stray backup;
//	after the tombstone, before the marker delete: same;
//	after the marker delete, before the rename: the OLD binary is
//	  still in place — it boots, counts as attempt 1, finishes, and
//	  the marker write retires the whole state.
//
// Step 1 overwrites aii.previous unconditionally, so this function must
// never run over a live backup: applyTo is the sole guard, refusing with
// ErrUpdatePending while unretiredSwap sees either the backup or a
// readable tombstone.
func swapBinary(exePath string, newBinary []byte, dataDir string) error {
	markerPath := filepath.Join(dataDir, markerFile)
	prevPath := filepath.Join(dataDir, previousFile)
	pendPath := filepath.Join(dataDir, pendingFile)

	// 1. Back up the running binary.
	currentBytes, err := os.ReadFile(exePath)
	if err != nil {
		return fmt.Errorf("read current binary for backup: %w", err)
	}
	if err := writeFileAtomic(prevPath, currentBytes, 0o755); err != nil {
		return fmt.Errorf("write backup binary: %w", err)
	}

	// 2. Record the swap: installed, not yet booted.
	if err := writePending(dataDir, updatePending{
		Attempts:     0,
		BackupSHA256: sha256hex(currentBytes),
		NewSHA256:    sha256hex(newBinary),
	}); err != nil {
		os.Remove(prevPath)
		return fmt.Errorf("write update tombstone: %w", err)
	}

	// 3. Delete the boot-health marker; the next healthy boot rearms it.
	if err := os.Remove(markerPath); err != nil && !os.IsNotExist(err) {
		os.Remove(prevPath)
		os.Remove(pendPath)
		return fmt.Errorf("delete boot marker: %w", err)
	}

	// 4. Write the new binary to a temp file beside the target.
	tmpPath := exePath + ".new"
	if err := os.WriteFile(tmpPath, newBinary, 0o755); err != nil {
		// The swap didn't happen; retire its state.
		os.Remove(prevPath)
		os.Remove(pendPath)
		return fmt.Errorf("write new binary: %w", err)
	}

	// 5. Publish the new image at the running binary's path. The
	// platform difference — Windows renames the running image aside as
	// .old first — lives in atomicfile.ReplaceExecutable, so this file
	// stopped branching on GOOS (F5, 2026-08-26). The .old Windows
	// leaves is removed by checkRollbackAt on a later boot, when that
	// process is gone (the old comment here promised a cleanup that
	// did not exist).
	if err := atomicfile.ReplaceExecutable(tmpPath, exePath); err != nil {
		os.Remove(tmpPath)
		os.Remove(prevPath)
		os.Remove(pendPath)
		return fmt.Errorf("rename new binary into place: %w", err)
	}

	return nil
}

// extractBinary extracts the `aii` binary from a release archive
// (.tar.gz on Unix, .zip on Windows). Returns the raw binary bytes.
func extractBinary(archiveBytes []byte) ([]byte, error) {
	if runtime.GOOS == "windows" {
		return extractFromZip(archiveBytes)
	}
	return extractFromTarGz(archiveBytes)
}

// --- Boot-health marker ---

// WriteBootMarker records a healthy boot AND retires the update state:
// a completed boot is the success the backup existed for, so the
// backup and the tombstone are removed here rather than left for the
// next restart to tidy. Called at the end of startLive and
// startSafeBoot; on boots with no update in flight the removals are
// no-ops.
func WriteBootMarker(dataDir string) {
	if err := os.WriteFile(filepath.Join(dataDir, markerFile), []byte("ok"), 0o644); err != nil {
		// The marker could not be written, so the NEXT boot will look
		// like a failed one. Keep the backup and tombstone: rollback
		// insurance stays armed, which on a broken disk is the less
		// wrong of the two options.
		log.Printf("updates: could not write boot marker: %v", err)
		return
	}
	os.Remove(filepath.Join(dataDir, pendingFile))
	os.Remove(filepath.Join(dataDir, previousFile))
}

// CheckRollback examines the update state at startup and decides
// whether this boot should restore the previous binary. Returns the
// restored path if a rollback occurred, else "".
func CheckRollback(dataDir string) string {
	exePath, err := os.Executable()
	if err != nil {
		log.Printf("updates: rollback check skipped — cannot locate running binary: %v", err)
		return ""
	}
	return checkRollbackAt(dataDir, exePath)
}

// checkRollbackAt is CheckRollback with the executable path injectable,
// so tests can run the REAL sequence an update runs — swap, boot,
// boot again — against a temp file instead of the test binary.
func checkRollbackAt(dataDir, exePath string) string {
	markerPath := filepath.Join(dataDir, markerFile)
	prevPath := filepath.Join(dataDir, previousFile)
	pendPath := filepath.Join(dataDir, pendingFile)

	// A Windows swap or rollback leaves the displaced running image
	// beside the binary as .old — a running image can be renamed but
	// not removed. By this boot that process is gone; this is the one
	// early hook every platform's every boot passes through.
	_ = os.Remove(exePath + ".old")

	if _, err := os.Stat(prevPath); err != nil {
		os.Remove(pendPath) // a tombstone with no backup is debris
		return ""
	}
	if _, err := os.Stat(markerPath); err == nil {
		// Marker present — the previous boot succeeded. Whatever
		// update state remains is stale; retire it.
		os.Remove(prevPath)
		os.Remove(pendPath)
		return ""
	}

	pend, ok := readPending(dataDir)
	if !ok {
		// A swap from a build before the tombstone existed, or a
		// corrupt tombstone. The old logic rolled back HERE — on the
		// updated binary's first boot — which reverted every update
		// ever applied. Adopt the state instead: hash the backup now,
		// count this boot as the first attempt, and let the next boot
		// decide with real information.
		prevBytes, err := os.ReadFile(prevPath)
		if err != nil {
			log.Printf("updates: unreadable backup with no tombstone — retiring it: %v", err)
			os.Remove(prevPath)
			return ""
		}
		if err := writePending(dataDir, updatePending{
			Attempts:     1,
			BackupSHA256: sha256hex(prevBytes),
		}); err != nil {
			log.Printf("updates: could not adopt legacy update state: %v", err)
		}
		return ""
	}

	if pend.Attempts == 0 {
		// The swap happened and THIS is the updated binary's first
		// boot — not a failure. Count it and proceed; if this boot
		// dies before the marker, the next one sees attempts 1 and
		// rolls back.
		pend.Attempts = 1
		if err := writePending(dataDir, pend); err != nil {
			log.Printf("updates: could not record first boot attempt: %v", err)
		}
		return ""
	}

	// attempts >= 1 with no marker: the updated binary began a boot
	// and never finished one. Roll back — carefully.
	return rollbackToPrev(dataDir, prevPath, exePath, pend)
}

// rollbackToPrev restores the backup to exePath, verifying first that
// the backup is the backup and the target is still the update.
func rollbackToPrev(dataDir, prevPath, exePath string, pend updatePending) string {
	prevBytes, err := os.ReadFile(prevPath)
	if err != nil {
		log.Printf("updates: ROLLBACK FAILED — cannot read backup binary: %v", err)
		return ""
	}
	// THE BACKUP MUST HASH TO WHAT THE SWAP RECORDED. A truncated or
	// bit-rotted backup restored over a runnable binary is the safety
	// net bricking the identity: refuse, loudly, and leave the files
	// for the operator.
	if pend.BackupSHA256 != "" && sha256hex(prevBytes) != pend.BackupSHA256 {
		log.Printf("updates: ROLLBACK REFUSED — backup hashes to %.12s, swap recorded %.12s; the backup is not a backup, and the current binary stays",
			sha256hex(prevBytes), pend.BackupSHA256)
		return ""
	}
	// If the executable no longer matches what the swap installed, an
	// operator has already repaired it by hand. Their binary wins;
	// retire the update state rather than clobbering the repair with
	// the thing that already failed.
	if pend.NewSHA256 != "" {
		if cur, err := os.ReadFile(exePath); err == nil && sha256hex(cur) != pend.NewSHA256 {
			log.Printf("updates: rollback skipped — the binary changed since the update (operator repair); retiring update state")
			os.Remove(prevPath)
			os.Remove(filepath.Join(dataDir, pendingFile))
			return ""
		}
	}
	// The restore target is the RUNNING failed image. writeFileAtomic
	// renames over the target, which Windows refuses for a running
	// image — so the rollback the swap's own dance made possible was
	// failing at the finish line on Windows, every boot (review F1,
	// 2026-08-26). ReplaceExecutable carries the dance for both
	// callers now.
	tmpRestore := exePath + ".rollback"
	if err := os.WriteFile(tmpRestore, prevBytes, 0o755); err != nil {
		log.Printf("updates: ROLLBACK FAILED — cannot stage previous binary: %v", err)
		return ""
	}
	if err := atomicfile.ReplaceExecutable(tmpRestore, exePath); err != nil {
		os.Remove(tmpRestore)
		log.Printf("updates: ROLLBACK FAILED — cannot restore previous binary: %v", err)
		return ""
	}
	os.Remove(prevPath)
	os.Remove(filepath.Join(dataDir, pendingFile))
	log.Printf("updates: ROLLBACK — restored previous binary (update failed to boot)")
	return prevPath
}

// --- Helpers ---

func sha256hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
