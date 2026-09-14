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
	neturl "net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/quiesce"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
	"github.com/aiii-dot-id/aii-os/internal/version"
)

// .
// .
// .
// .
// .
// .
var errNoRelease = errors.New("no release published yet (or the repository does not exist)")

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
const DefaultRepo = "aiii-dot-id/aii-os"

// .
// .
// .
func validRepo(s string) bool {
	slash := strings.IndexByte(s, '/')
	if slash <= 0 || slash == len(s)-1 {
		return false
	}
	if strings.IndexByte(s[slash+1:], '/') >= 0 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := c == '/' || c == '-' || c == '_' || c == '.' ||
			(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
		if !ok {
			return false
		}
	}
	return true
}

// .
// .
// .
const CheckInterval = 1 * time.Hour

// .
// .
const downloadTimeout = 10 * time.Minute

// .
const metadataTimeout = 30 * time.Second

// .
// .
// .
// .
const maxDownloadSize = 256 * 1024 * 1024

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
type releaseArchivePayload = ReleasePayload

// .
// .
// .
const artifactKindReleaseSig = "release.platform_release"

// .
// .
// .
// .
// .
// .
const artifactKindEvidenceSig = "release.evidence_bundle"

// .
// .
// .
type State struct {
	mu           sync.RWMutex
	availableVer string
	lastCheck    time.Time
	lastError    string
	installedVer string
	needsRestart bool
	checking     bool
}

// .
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
		Checking:         s.checking,
	}
}

// .
type StateSnapshot struct {
	CurrentVersion   string    `json:"current_version,omitempty"`
	AvailableVersion string    `json:"available_version,omitempty"`
	LastCheck        time.Time `json:"last_check,omitempty"`
	LastError        string    `json:"last_error,omitempty"`
	InstalledVersion string    `json:"installed_version,omitempty"`
	NeedsRestart     bool      `json:"needs_restart,omitempty"`
	Checking         bool      `json:"checking,omitempty"`
}

// .
// .
func (s *State) SetChecking(on bool) {
	s.mu.Lock()
	s.checking = on
	s.mu.Unlock()
}

func (s *State) Available() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.availableVer
}

// .
func (s *State) SetAvailable(ver string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.availableVer = ver
}

// .
func (s *State) SetLastError(err string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastError = err
}

// .
func (s *State) SetInstalled(ver string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.installedVer = ver
	s.needsRestart = true
	s.availableVer = ""
}

// .
func (s *State) MarkChecked() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastError = ""
	s.lastCheck = time.Now()
}

// .
// .
// .
// .
// .
// .
func (c *Checker) resolveRepo() string {
	if c.repo == nil {
		return DefaultRepo
	}
	r := c.repo()
	if r == "" {
		return DefaultRepo
	}
	if !validRepo(r) {
		c.badRepoOnce.Do(func() {
			log.Printf("updates: config updates.repo %q is not owner/name — using %s", r, DefaultRepo)
		})
		return DefaultRepo
	}
	return r
}

// .
type Checker struct {
	state        *State
	platformRoot func() *sigenvelope.PublicKeyEnvelope
	currentVer   func() string
	automatic    func() bool
	// .
	// .
	isSafe        func() bool
	isMobile      func() bool
	armed         atomic.Bool
	inFlight      bool
	repo          func() string
	gate          *quiesce.Gate
	dataDir       string
	httpClient    *http.Client
	badRepoOnce   sync.Once
	noReleaseOnce sync.Once
	mu            sync.Mutex
	cachedRelease *githubRelease
	// .
	// .
	// .
	// .
	// .
	hostAllowlist func(string) error
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
// .
// .
// .
func NewChecker(platformRoot func() *sigenvelope.PublicKeyEnvelope, currentVer func() string, automatic func() bool, repo func() string, gate *quiesce.Gate, dataDir string) *Checker {
	c := &Checker{
		state:         &State{},
		platformRoot:  platformRoot,
		currentVer:    currentVer,
		automatic:     automatic,
		repo:          repo,
		gate:          gate,
		dataDir:       dataDir,
		hostAllowlist: gitHubReleaseHost,
		httpClient:    &http.Client{},
	}
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	c.httpClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("too many redirects")
		}
		return c.hostAllowlist(req.URL.String())
	}
	return c
}

// .
func (c *Checker) State() *State { return c.state }

// .
// .
// .
// .
// .
// .
// .
// .
func (c *Checker) Check(ctx context.Context) string {
	release, err := c.fetchLatestRelease(ctx)
	if errors.Is(err, errNoRelease) {
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		reason := fmt.Sprintf("%s: %v (HTTP 404)", c.resolveRepo(), errNoRelease)
		c.noReleaseOnce.Do(func() {
			log.Printf("updates: %s — checking continues; this is logged once, not every hour", reason)
		})
		c.state.SetAvailable("")
		c.state.MarkChecked()
		c.state.SetLastError(reason)
		return ""
	}
	if err != nil {
		log.Printf("updates: check failed: %v", err)
		c.state.SetLastError(err.Error())
		return ""
	}

	// .
	// .
	c.mu.Lock()
	c.cachedRelease = release
	c.mu.Unlock()

	// .
	latestVer := strings.TrimPrefix(release.TagName, "v")
	currentVer := c.currentVer()

	// .
	// .
	// .
	// .
	// .
	if !version.Valid(latestVer) || !version.Valid(currentVer) {
		err := fmt.Errorf("invalid semantic version: running %q, latest %q", currentVer, latestVer)
		log.Printf("updates: check failed: %v", err)
		c.state.SetLastError(err.Error())
		return ""
	}

	cmp := version.Compare(latestVer, currentVer)
	if cmp <= 0 {
		// .
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

// .
// .
// .
// .
// .
// .
// .
// .
var ErrUpdatePending = errors.New("update already installed — restart to complete it before another update can be applied")

// .
// .
// .
// .
// .
func (c *Checker) Apply(ctx context.Context) error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate running binary: %w", err)
	}
	return c.applyTo(ctx, exePath)
}

// .
// .
// .
// .
func (c *Checker) applyTo(ctx context.Context, exePath string) error {
	// .
	// .
	// .
	// .
	// .
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

	// .
	// .
	// .
	// .
	// .
	// .
	bundlePath, inBundle := bundleRoot(exePath)
	assetName := assetName(available)
	if inBundle {
		assetName = bundleAssetName(available)
	}
	sigAssetName := assetName + ".platform.sig"

	// .
	// .
	c.mu.Lock()
	release := c.cachedRelease
	c.mu.Unlock()
	if release == nil {
		return fmt.Errorf("no cached release — run Check first")
	}

	archiveURL, sigURL, err := findAssets(release, assetName, sigAssetName)
	if err != nil {
		if inBundle {
			// .
			// .
			// .
			// .
			return fmt.Errorf("%w (%v)", errBareBinaryIntoBundle(bundlePath), err)
		}
		return fmt.Errorf("find assets: %w", err)
	}

	// .
	archiveBytes, err := c.download(ctx, archiveURL)
	if err != nil {
		return fmt.Errorf("download archive: %w", err)
	}

	// .
	sigBytes, err := c.download(ctx, sigURL)
	if err != nil {
		return fmt.Errorf("download signature: %w", err)
	}

	// .
	archiveHash := sha256hex(archiveBytes)

	// .
	hostPlatform, hostArch := hostTarget()
	signedPayload, err := verifyReleaseSig(sigBytes, root, archiveHash, available, hostPlatform, hostArch)
	if err != nil {
		return fmt.Errorf("signature verification REFUSED: %w", err)
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
	revRoots := packagefmt.ReleaseTrustRoots(filepath.Join(c.dataDir, "trust"), root)
	if err := packagefmt.CheckReleaseRevocation(revRoots, artifactKindReleaseSig, signedPayload); err != nil {
		return fmt.Errorf("release REFUSED: %w", err)
	}

	log.Printf("updates: signature verified for %s (hash %s)", assetName, archiveHash[:12])

	// .
	// .
	// .
	handled, err := applyIfBundle(exePath, archiveBytes)
	if err != nil {
		return fmt.Errorf("bundle update: %w", err)
	}
	if !handled {
		// .
		newBinary, err := extractBinary(archiveBytes)
		if err != nil {
			return fmt.Errorf("extract binary from archive: %w", err)
		}

		// .
		if err := swapBinary(exePath, newBinary, c.dataDir); err != nil {
			return fmt.Errorf("binary swap: %w", err)
		}
	}

	c.state.SetInstalled(available)
	log.Printf("updates: installed %s — restart to apply", available)
	return nil
}

// .
// .
// .
// .
func (c *Checker) Run(ctx context.Context, isSafe func() bool, isMobile func() bool) {
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
	if cur := c.currentVer(); !version.Valid(cur) {
		log.Printf("updates: this build carries no release version (%q) — update checking is off for the life of this process; a released build carries one and checks normally", cur)
		return
	}
	c.arm(isSafe, isMobile)

	tk := quiesce.NewTicker(c.gate, CheckInterval)
	defer tk.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
		}

		if isSafe() {
			continue
		}
		c.tick(ctx)
	}
}

// .
// .
// .
// .
func (c *Checker) arm(isSafe, isMobile func() bool) {
	c.mu.Lock()
	c.isSafe, c.isMobile = isSafe, isMobile
	c.mu.Unlock()
	c.armed.Store(true)
}

// .
func (c *Checker) Armed() bool { return c.armed.Load() }

// .
// .
// .
// .
func (c *Checker) tick(ctx context.Context) {
	available := c.Check(ctx)

	// .
	if available == "" {
		return
	}

	c.mu.Lock()
	isMobile := c.isMobile
	c.mu.Unlock()
	mobile := isMobile != nil && isMobile()
	automatic := c.automatic()
	// .
	// .
	// .
	// .
	// .
	staged, stageWhy := canStageBeside()

	if mobile || !automatic || !staged {
		if !staged {
			log.Printf("updates: %s available — inform only (%s)", available, stageWhy)
		} else {
			log.Printf("updates: %s available — inform only (mobile=%v, automatic=%v)", available, mobile, automatic)
		}
		return
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
	if err := c.Apply(ctx); errors.Is(err, ErrUpdatePending) {
		log.Printf("updates: %s staged but not applied — %v", available, err)
	} else if err != nil {
		log.Printf("updates: apply failed: %v", err)
		c.state.SetLastError(err.Error())
	}
}

// .
// .
var (
	ErrCheckingOff     = errors.New("update checking is off for this build (no release version)")
	ErrAlreadyChecking = errors.New("already checking")
	ErrSafeMode        = errors.New("in SAFE mode — no outside-world operations")
)

// .
// .
const checkNowTimeout = downloadTimeout + 5*time.Minute

// .
// .
// .
// .
// .
// .
// .
func (c *Checker) CheckNow(ctx context.Context, done func()) error {
	if !c.armed.Load() {
		return ErrCheckingOff
	}
	c.mu.Lock()
	isSafe := c.isSafe
	c.mu.Unlock()
	if isSafe != nil && isSafe() {
		return ErrSafeMode
	}
	c.mu.Lock()
	if c.inFlight {
		c.mu.Unlock()
		return ErrAlreadyChecking
	}
	c.inFlight = true
	c.mu.Unlock()
	c.state.SetChecking(true)
	go func() {
		defer func() {
			c.state.SetChecking(false)
			c.mu.Lock()
			c.inFlight = false
			c.mu.Unlock()
			if done != nil {
				done()
			}
		}()
		cctx, cancel := context.WithTimeout(ctx, checkNowTimeout)
		defer cancel()
		c.tick(cctx)
	}()
	return nil
}

// .

// .
type githubRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func (c *Checker) fetchLatestRelease(ctx context.Context) (*githubRelease, error) {
	ctx, cancel := context.WithTimeout(ctx, metadataTimeout)
	defer cancel()
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", c.resolveRepo())
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

	if resp.StatusCode == http.StatusNotFound {
		return nil, errNoRelease
	}
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
func gitHubReleaseHost(raw string) error {
	u, err := neturl.Parse(raw)
	if err != nil {
		return fmt.Errorf("unparseable URL: %w", err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("refusing a non-https release URL (%s)", u.Scheme)
	}
	h := u.Hostname()
	ok := h == "github.com" || h == "api.github.com" || h == "codeload.github.com" ||
		h == "githubusercontent.com" || strings.HasSuffix(h, ".githubusercontent.com")
	if !ok {
		return fmt.Errorf("refusing a release URL off GitHub (%s)", h)
	}
	return nil
}

func (c *Checker) download(ctx context.Context, url string) ([]byte, error) {
	if err := c.hostAllowlist(url); err != nil {
		return nil, fmt.Errorf("download egress: %w", err)
	}
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

	return readBounded(resp.Body, maxDownloadSize, "release download "+url)
}

// .
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

// .
// .
func assetName(version string) string {
	platform, arch := hostTarget()
	return AssetName(version, platform, arch)
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
func hostTarget() (platform, arch string) {
	return packagefmt.HostPlatform(), runtime.GOARCH
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
// .
func verifyReleaseSig(sigBytes []byte, root *sigenvelope.PublicKeyEnvelope, archiveHash, version, platform, arch string) (json.RawMessage, error) {
	return verifySigOfKind(artifactKindReleaseSig, sigBytes, root, archiveHash, version, platform, arch)
}

// .
// .
func verifySigOfKind(kind string, sigBytes []byte, root *sigenvelope.PublicKeyEnvelope, archiveHash, version, platform, arch string) (json.RawMessage, error) {
	// .
	// .
	// .
	// .
	// .
	// .
	if err := packagefmt.ValidatePlatformReleaseRoot(root); err != nil {
		return nil, fmt.Errorf("release signature root refused: %w", err)
	}
	// .
	raw, err := sigenvelope.VerifyPayload(sigBytes, root, kind, crypto.ProfileRoot)
	if err != nil {
		return nil, fmt.Errorf("signature does not verify against platform root: %w", err)
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
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	var payload releaseArchivePayload
	if err := dec.Decode(&payload); err != nil {
		return nil, fmt.Errorf("signature payload is not the closed {archive_hash} object: %w", err)
	}
	if dec.More() {
		return nil, fmt.Errorf("trailing data after payload object")
	}

	if payload.Version == "" {
		return nil, fmt.Errorf("signature payload carries no bound version — pre-binding format (before 2026-08-26); re-sign the release with the full manifest payload")
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
	if payload.SourceRev == "" {
		return nil, fmt.Errorf("signature payload carries no source_rev — the signer must state build provenance")
	}

	return raw, nil
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
// .
// .
type updatePending struct {
	// .
	// .
	// .
	Attempts int `json:"attempts"`
	// .
	// .
	// .
	// .
	BackupSHA256 string `json:"backup_sha256"`
	// .
	// .
	// .
	// .
	NewSHA256 string `json:"new_sha256"`
}

const (
	markerFile   = ".boot_completed"
	previousFile = "aii.previous"
	pendingFile  = ".update_pending"
	// .
	// .
	// .
	// .
	// .
	retiredMarkerFile = ".boot_completed.retired"
)

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
func stageFileDurably(target string, data []byte, mode os.FileMode) (string, error) {
	f, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+".stage-*")
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	fail := func(format string, err error) (string, error) {
		f.Close()
		os.Remove(tmp)
		return "", fmt.Errorf(format+" %s: %w", filepath.Base(target), err)
	}
	if err := f.Chmod(mode); err != nil {
		return fail("chmod staged", err)
	}
	if _, err := f.Write(data); err != nil {
		return fail("write staged", err)
	}
	if err := f.Sync(); err != nil {
		return fail("sync staged", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("close staged %s: %w", filepath.Base(target), err)
	}
	return tmp, nil
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) (retErr error) {
	tmp, err := stageFileDurably(path, data, mode)
	if err != nil {
		return err
	}
	defer func() {
		// .
		// .
		if err := os.Remove(tmp); err != nil && !os.IsNotExist(err) {
			retErr = errors.Join(retErr, err)
		}
	}()

	// .
	// .
	// .
	published, err := atomicfile.Replace(tmp, path)
	if err != nil {
		if published {
			return fmt.Errorf("replace %s: published but not durable: %w", filepath.Base(path), err)
		}
		return fmt.Errorf("replace %s: %w", filepath.Base(path), err)
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
// .
// .
// .
// .
// .
// .
// .
// .
func canStageBeside() (bool, string) {
	exe, err := os.Executable()
	if err != nil {
		return false, "cannot locate the running binary: " + err.Error()
	}
	return canStageBesideAt(exe)
}

// .
// .
// .
func canStageBesideAt(exe string) (bool, string) {
	dir := filepath.Dir(exe)
	probe, err := os.CreateTemp(dir, ".aii-swap-probe-")
	if err != nil {
		return false, exe + " cannot be replaced by this user — update it with the package manager that installed it"
	}
	name := probe.Name()
	probe.Close()
	os.Remove(name)
	return true, ""
}

func unretiredSwap(dataDir string) bool {
	if _, err := os.Stat(filepath.Join(dataDir, previousFile)); err == nil {
		return true
	}
	_, ok := readPending(dataDir)
	return ok
}

// .
// .
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
func swapBinary(exePath string, newBinary []byte, dataDir string) error {
	prevPath := filepath.Join(dataDir, previousFile)
	pendPath := filepath.Join(dataDir, pendingFile)

	// .
	currentBytes, err := os.ReadFile(exePath)
	if err != nil {
		return fmt.Errorf("read current binary for backup: %w", err)
	}
	if err := writeFileAtomic(prevPath, currentBytes, 0o755); err != nil {
		return fmt.Errorf("write backup binary: %w", err)
	}

	// .
	if err := writePending(dataDir, updatePending{
		Attempts:     0,
		BackupSHA256: sha256hex(currentBytes),
		NewSHA256:    sha256hex(newBinary),
	}); err != nil {
		os.Remove(prevPath)
		return fmt.Errorf("write update tombstone: %w", err)
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
	// .
	// .
	// .
	markerRetired, retireErr := retireBootMarker(dataDir)
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
	rearmMarker := func() (bool, error) {
		if !markerRetired {
			return true, nil
		}
		return rearmBootMarker(dataDir)
	}
	// .
	// .
	retireSwapState := func(primary error, what string) error {
		if back, rerr := rearmMarker(); rerr != nil {
			state := "NOT re-armed"
			if back {
				state = "re-armed but not durably"
			}
			return errors.Join(
				fmt.Errorf("%s: %w", what, primary),
				fmt.Errorf("boot marker %s — backup and tombstone kept so the next boot can still recover: %w", state, rerr))
		}
		os.Remove(prevPath)
		os.Remove(pendPath)
		return fmt.Errorf("%s: %w", what, primary)
	}

	// .
	// .
	// .
	// .
	// .
	// .
	if retireErr != nil {
		return retireSwapState(retireErr, "retire boot marker")
	}

	// .
	tmpPath, err := stageFileDurably(exePath, newBinary, 0o755)
	if err != nil {
		// .
		return retireSwapState(err, "write new binary")
	}

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	published, err := atomicfile.ReplaceExecutable(tmpPath, exePath)
	if err != nil {
		if !published {
			// .
			// .
			// .
			os.Remove(tmpPath)
			return retireSwapState(err, "rename new binary into place")
		}
		// .
		// .
		// .
		// .
		return fmt.Errorf("new binary published but not durable — backup and tombstone kept so the next boot can still roll back: %w", err)
	}

	return nil
}

// .
// .
func extractBinary(archiveBytes []byte) ([]byte, error) {
	if runtime.GOOS == "windows" {
		return extractFromZip(archiveBytes)
	}
	return extractFromTarGz(archiveBytes)
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
// .
// .
// .
// .
func retireBootMarker(dataDir string) (bool, error) {
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
	return classifyRetire(atomicfile.Replace(
		filepath.Join(dataDir, markerFile),
		filepath.Join(dataDir, retiredMarkerFile)))
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
// .
// .
// .
// .
// .
func classifyRetire(published bool, err error) (bool, error) {
	switch {
	case err == nil:
		return true, nil
	case published:
		return true, fmt.Errorf("boot marker retired but not durably: %w", err)
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	default:
		return false, err
	}
}

// .
// .
// .
// .
// .
// .
// .
// .
func statRefusal(path string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, os.ErrNotExist) {
		if fi, perr := os.Lstat(filepath.Dir(path)); perr == nil && !fi.IsDir() {
			return fmt.Errorf("stat %s: parent %s is not a directory", path, filepath.Dir(path))
		}
		return nil
	}
	return err
}

// .
// .
// .
// .
func rearmBootMarker(dataDir string) (back bool, err error) {
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	return atomicfile.Replace(
		filepath.Join(dataDir, retiredMarkerFile),
		filepath.Join(dataDir, markerFile))
}

// .
// .
// .
// .
// .
// .
func WriteBootMarker(dataDir string) {
	// .
	// .
	// .
	// .
	if err := writeFileAtomic(filepath.Join(dataDir, markerFile), []byte("ok"), 0o644); err != nil {
		// .
		// .
		// .
		// .
		log.Printf("updates: could not write boot marker: %v", err)
		return
	}
	os.Remove(filepath.Join(dataDir, pendingFile))
	os.Remove(filepath.Join(dataDir, previousFile))
	// .
	os.Remove(filepath.Join(dataDir, retiredMarkerFile))
}

// .
// .
// .
func CheckRollback(dataDir string) string {
	exePath, err := os.Executable()
	if err != nil {
		log.Printf("updates: rollback check skipped — cannot locate running binary: %v", err)
		return ""
	}
	return checkRollbackAt(dataDir, exePath)
}

// .
// .
// .
func checkRollbackAt(dataDir, exePath string) string {
	markerPath := filepath.Join(dataDir, markerFile)
	prevPath := filepath.Join(dataDir, previousFile)
	pendPath := filepath.Join(dataDir, pendingFile)

	// .
	// .
	// .
	// .
	_ = os.Remove(exePath + ".old")

	if _, err := os.Stat(prevPath); err != nil {
		if refusal := statRefusal(prevPath, err); refusal != nil {
			// .
			// .
			// .
			// .
			// .
			log.Printf("updates: rollback check cannot stat %s: %v — leaving update state untouched", prevPath, refusal)
			return ""
		}
		os.Remove(pendPath)
		return ""
	}
	if _, err := os.Stat(markerPath); err == nil {
		// .
		// .
		os.Remove(prevPath)
		os.Remove(pendPath)
		return ""
	} else if refusal := statRefusal(markerPath, err); refusal != nil {
		// .
		// .
		// .
		// .
		// .
		// .
		log.Printf("updates: rollback check cannot stat %s: %v — leaving update state untouched", markerPath, refusal)
		return ""
	}

	pend, ok := readPending(dataDir)
	if !ok {
		// .
		// .
		// .
		// .
		// .
		// .
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
		// .
		// .
		// .
		// .
		pend.Attempts = 1
		if err := writePending(dataDir, pend); err != nil {
			log.Printf("updates: could not record first boot attempt: %v", err)
		}
		return ""
	}

	// .
	// .
	return rollbackToPrev(dataDir, prevPath, exePath, pend)
}

// .
// .
func rollbackToPrev(dataDir, prevPath, exePath string, pend updatePending) string {
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if ok, why := canStageBesideAt(exePath); !ok {
		log.Printf("updates: ROLLBACK DEFERRED \u2014 %s; the previous binary is kept at %s (restore it with your package manager, or from data/backups/)", why, prevPath)
		return ""
	}
	prevBytes, err := os.ReadFile(prevPath)
	if err != nil {
		log.Printf("updates: ROLLBACK FAILED — cannot read backup binary: %v", err)
		return ""
	}
	// .
	// .
	// .
	// .
	if pend.BackupSHA256 != "" && sha256hex(prevBytes) != pend.BackupSHA256 {
		log.Printf("updates: ROLLBACK REFUSED — backup hashes to %.12s, swap recorded %.12s; the backup is not a backup, and the current binary stays",
			sha256hex(prevBytes), pend.BackupSHA256)
		return ""
	}
	// .
	// .
	// .
	// .
	if pend.NewSHA256 != "" {
		if cur, err := os.ReadFile(exePath); err == nil && sha256hex(cur) != pend.NewSHA256 {
			log.Printf("updates: rollback skipped — the binary changed since the update (operator repair); retiring update state")
			os.Remove(prevPath)
			os.Remove(filepath.Join(dataDir, pendingFile))
			return ""
		}
	}
	// .
	// .
	// .
	// .
	// .
	// .
	tmpRestore, err := stageFileDurably(exePath, prevBytes, 0o755)
	if err != nil {
		log.Printf("updates: ROLLBACK FAILED — cannot stage previous binary: %v", err)
		return ""
	}
	restored, rerr := atomicfile.ReplaceExecutable(tmpRestore, exePath)
	if rerr != nil && !restored {
		os.Remove(tmpRestore)
		log.Printf("updates: ROLLBACK FAILED — cannot restore previous binary: %v", rerr)
		return ""
	}
	if rerr != nil {
		// .
		// .
		// .
		// .
		// .
		log.Printf("updates: ROLLBACK restored the previous binary but could not make the directory entry durable — keeping the backup: %v", rerr)
		return prevPath
	}
	os.Remove(prevPath)
	os.Remove(filepath.Join(dataDir, pendingFile))
	log.Printf("updates: ROLLBACK — restored previous binary (update failed to boot)")
	return prevPath
}

// .

func sha256hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// .
// .
// .
// .
func StageRefusal() string {
	if ok, why := canStageBeside(); !ok {
		return why
	}
	return ""
}
