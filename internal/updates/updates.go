package updates

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"io"
	"io/fs"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/filelock"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/quiesce"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
	"github.com/aiii-dot-id/aii-os/internal/version"
)

var errNoRelease = errors.New("no release published yet (or the repository does not exist)")

const DefaultRepo = "aiii-dot-id/aii-os"

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

const CheckInterval = 1 * time.Hour

const downloadTimeout = 10 * time.Minute

const metadataTimeout = 30 * time.Second

const maxDownloadSize = 256 * 1024 * 1024

type releaseArchivePayload = ReleasePayload

const artifactKindReleaseSig = packagefmt.ArtifactKindHostRelease

const artifactKindEvidenceSig = "release.evidence_bundle"

type State struct {
	mu           sync.RWMutex
	availableVer string
	lastCheck    time.Time
	lastError    string
	installedVer string
	needsRestart bool

	pkg PackageUpdate

	checking atomic.Bool
}

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
		Checking:         s.checking.Load(),
		Package:          s.pkg,
	}
}

type StateSnapshot struct {
	CurrentVersion   string    `json:"current_version,omitempty"`
	AvailableVersion string    `json:"available_version,omitempty"`
	LastCheck        time.Time `json:"last_check,omitempty"`
	LastError        string    `json:"last_error,omitempty"`
	InstalledVersion string    `json:"installed_version,omitempty"`
	NeedsRestart     bool      `json:"needs_restart,omitempty"`
	Checking         bool      `json:"checking,omitempty"`

	Package PackageUpdate `json:"package"`
}

func (s *State) beginCheck() bool { return s.checking.CompareAndSwap(false, true) }

func (s *State) endCheck() { s.checking.Store(false) }

func (s *State) Available() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.availableVer
}

func (s *State) setAvailable(ver string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.availableVer = ver
}

func (s *State) setPackage(p PackageUpdate) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pkg = p
}

func (s *State) setLastError(err string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastError = err
}

func (s *State) setInstalled(ver string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.installedVer = ver
	s.needsRestart = true
	s.availableVer = ""
}

func (s *State) markChecked() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastError = ""
	s.lastCheck = time.Now()
}

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
			logsink.Warn("updates.refusal", "config updates.repo %q is not owner/name — using %s", r, DefaultRepo)
		})
		return DefaultRepo
	}
	return r
}

type Checker struct {
	state        *State
	platformRoot func() *sigenvelope.PublicKeyEnvelope
	currentVer   func() string
	automatic    func() bool

	isSafe   func() bool
	isMobile func() bool

	armed    atomic.Bool
	applying atomic.Bool

	runner  func(work func()) bool
	repo    func() string
	gate    *quiesce.Gate
	dataDir string

	pkgListPath, pkgExe string
	canStage            func() (bool, string)
	httpClient          *http.Client
	badRepoOnce         sync.Once
	noReleaseOnce       sync.Once
	saidFailedHere      string

	ownRelease    func(context.Context) (*githubRelease, error)
	mu            sync.Mutex
	cachedRelease *githubRelease

	afterVerified func()

	hostAllowlist func(string) error
}

func NewChecker(platformRoot func() *sigenvelope.PublicKeyEnvelope, currentVer func() string, automatic func() bool, repo func() string, gate *quiesce.Gate, dataDir string, isSafe, isMobile func() bool) *Checker {
	c := &Checker{
		state:         &State{},
		platformRoot:  platformRoot,
		currentVer:    currentVer,
		automatic:     automatic,
		repo:          repo,
		gate:          gate,
		dataDir:       dataDir,
		isSafe:        isSafe,
		isMobile:      isMobile,
		afterVerified: func() {},
		hostAllowlist: gitHubReleaseHost,
		httpClient:    &http.Client{},
	}

	c.httpClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("too many redirects")
		}
		return c.hostAllowlist(req.URL.String())
	}
	return c
}

func (c *Checker) State() *State { return c.state }

func (c *Checker) Check(ctx context.Context) string {
	release, err := c.fetchLatestRelease(ctx)
	if errors.Is(err, errNoRelease) {

		reason := fmt.Sprintf("%s: %v (HTTP 404)", c.resolveRepo(), errNoRelease)
		c.noReleaseOnce.Do(func() {
			logsink.Warn("updates.error", "%s — checking continues; this is logged once, not every hour", reason)
		})
		c.state.setAvailable("")
		c.state.markChecked()
		c.state.setLastError(reason)
		return ""
	}
	if err != nil {
		logsink.Warn("updates.error", "check failed: %v", err)
		c.state.setLastError(err.Error())
		return ""
	}

	c.mu.Lock()
	c.cachedRelease = release
	c.mu.Unlock()

	latestVer := strings.TrimPrefix(release.TagName, "v")
	currentVer := c.currentVer()

	if !version.Valid(latestVer) || !version.Valid(currentVer) {
		err := fmt.Errorf("invalid semantic version: running %q, latest %q", currentVer, latestVer)
		logsink.Warn("updates.refusal", "check failed: %v", err)
		c.state.setLastError(err.Error())
		return ""
	}

	cmp := version.Compare(latestVer, currentVer)
	if cmp <= 0 {

		c.state.setAvailable("")
		c.forgetPackages()
		c.state.markChecked()
		logsink.Info("updates.end", "running %s, latest %s — current", currentVer, latestVer)
		return ""
	}

	c.state.setAvailable(latestVer)
	c.state.markChecked()
	logsink.Info("updates.end", "running %s, latest %s — update available", currentVer, latestVer)
	return latestVer
}

var ErrUpdatePending = errors.New("update already installed — restart to complete it before another update can be applied")

var ErrAlreadyApplying = errors.New("an update is already being applied — wait for that attempt to finish")

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

	archiveURL, sigURL, err := findAssets(release, assetName, sigAssetName)
	if err != nil {
		if tgt.bundle {

			return fmt.Errorf("%w (%v)", errBareBinaryIntoBundle(tgt.path), err)
		}
		return fmt.Errorf("find assets: %w", err)
	}

	archiveBytes, err := c.verifiedArchive(ctx, archiveURL, sigURL, assetName, available, root)
	if err != nil {
		return err
	}

	c.afterVerified()

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("update not installed: %w", err)
	}
	handled, already, err := applyIfBundle(tgt, archiveBytes, owner)
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

func (c *Checker) verifiedArchive(ctx context.Context, archiveURL, sigURL, assetName, available string, root *sigenvelope.PublicKeyEnvelope) ([]byte, error) {

	archiveBytes, err := c.download(ctx, archiveURL)
	if err != nil {
		return nil, fmt.Errorf("download archive: %w", err)
	}

	sigBytes, err := c.download(ctx, sigURL)
	if err != nil {
		return nil, fmt.Errorf("download signature: %w", err)
	}

	archiveHash := sha256hex(archiveBytes)

	hostPlatform, hostArch := hostTarget()
	signedPayload, err := verifyReleaseSig(sigBytes, root, archiveHash, available, hostPlatform, hostArch)
	if err != nil {
		return nil, fmt.Errorf("signature verification REFUSED: %w", err)
	}

	revRoots := packagefmt.ReleaseTrustRoots(filepath.Join(c.dataDir, "trust"), root)
	if err := packagefmt.CheckReleaseRevocation(revRoots, artifactKindReleaseSig, signedPayload); err != nil {
		return nil, fmt.Errorf("release REFUSED: %w", err)
	}

	logsink.Info("updates.decision", "signature verified for %s (hash %s)", assetName, archiveHash[:12])

	return archiveBytes, nil
}

func installBinary(tgt target, archiveBytes []byte, dataDir, owner, version string) (already bool, err error) {

	newBinary, beside, err := extractBinary(archiveBytes)
	if err != nil {
		return false, fmt.Errorf("extract binary from archive: %w", err)
	}
	return tgt.replace(sha256hex(newBinary), func(string) error {
		return swapWithBeside(tgt, newBinary, beside, dataDir, owner, version)
	})
}

func swapWithBeside(tgt target, newBinary []byte, beside []installedFile, dataDir, owner, version string) error {
	if err := swapBinary(tgt, newBinary, beside, dataDir, owner, version); err != nil {
		return fmt.Errorf("binary swap: %w", err)
	}
	return nil
}

func installBeside(path string, data []byte) error {
	mode, aside := os.FileMode(0o755), true
	if slices.Contains(ShippedDocuments(), filepath.Base(path)) {
		mode, aside = 0o644, false
	} else if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		aside = false
	}
	tmp, err := stageFileDurably(path, data, mode)
	if err != nil {
		return err
	}
	published, err := publishBeside(aside, tmp, path)
	switch {
	case err == nil:
		return nil
	case published:
		return &besideDisturbed{err: err}
	}
	os.Remove(tmp)
	return err
}

type besideDisturbed struct{ err error }

func (e *besideDisturbed) Error() string { return "published but not durable: " + e.err.Error() }
func (e *besideDisturbed) Unwrap() error { return e.err }

func undoThrough(i int, err error) int {
	var disturbed *besideDisturbed
	if errors.As(err, &disturbed) {
		return i + 1
	}
	return i
}

var publishBeside = func(aside bool, tmp, path string) (published bool, err error) {
	if aside {
		return atomicfile.ReplaceExecutable(tmp, path)
	}
	return atomicfile.Replace(tmp, path)
}

func (c *Checker) Run(ctx context.Context) {

	if cur := c.currentVer(); !version.Valid(cur) {
		logsink.Info("updates.refusal", "this build carries no release version (%q) — update checking is off for the life of this process; a released build carries one and checks normally", cur)
		return
	}

	c.armed.Store(true)

	tk := quiesce.NewTicker(c.gate, CheckInterval)
	defer tk.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
		}

		if c.isSafe() {
			continue
		}
		c.tick(ctx)
	}
}

func (c *Checker) Armed() bool { return c.armed.Load() }

func (c *Checker) tick(ctx context.Context) {
	available := c.Check(ctx)

	c.completeIfIncomplete(ctx)

	if available == "" {
		return
	}

	mobile := c.isMobile != nil && c.isMobile()
	automatic := c.automatic()

	canStage := c.canStage
	if canStage == nil {
		canStage = canStageBeside
	}
	staged, stageWhy := canStage()

	if mobile || !automatic || !staged {
		if !staged {

			c.preparePackage(ctx, available)
			logsink.Info("updates.decision", "%s available — inform only (%s)", available, stageWhy)
		} else {
			logsink.Info("updates.decision", "%s available — inform only (mobile=%v, automatic=%v)", available, mobile, automatic)
		}
		return
	}

	var failedHere *FailedHereError
	if err := c.Apply(ctx); errors.Is(err, ErrUpdatePending) || errors.Is(err, ErrAlreadyApplying) {

		logsink.Warn("updates.refusal", "%s not installed by this attempt — %v", available, err)
	} else if errors.As(err, &failedHere) {

		c.state.setLastError(err.Error())
		c.mu.Lock()
		said := c.saidFailedHere == failedHere.Version
		c.saidFailedHere = failedHere.Version
		c.mu.Unlock()
		if !said {
			logsink.Warn("updates.refusal", "%v", err)
		}
	} else if err != nil {
		logsink.Warn("updates.error", "apply failed: %v", err)
		c.state.setLastError(err.Error())
	}
}

var (
	ErrCheckingOff     = errors.New("update checking is off for this build (no release version)")
	ErrAlreadyChecking = errors.New("already checking")
	ErrSafeMode        = errors.New("in SAFE mode — no outside-world operations")
)

const checkNowTimeout = downloadTimeout + 5*time.Minute

func (c *Checker) CheckNow(ctx context.Context, done func()) error {
	if !c.armed.Load() {
		return ErrCheckingOff
	}
	if c.isSafe != nil && c.isSafe() {
		return ErrSafeMode
	}
	if !c.state.beginCheck() {
		return ErrAlreadyChecking
	}
	work := func() {
		defer func() {
			c.state.endCheck()
			if done != nil {
				done()
			}
		}()
		cctx, cancel := context.WithTimeout(ctx, checkNowTimeout)
		defer cancel()
		c.tick(cctx)
	}

	if !c.run(work) {
		c.state.endCheck()
		return ErrStopping
	}
	return nil
}

var ErrStopping = errors.New("this identity is stopping")

func (c *Checker) SetRunner(run func(work func()) bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.runner = run
}

func (c *Checker) run(work func()) bool {
	c.mu.Lock()
	runner := c.runner
	c.mu.Unlock()
	if runner == nil {
		go work()
		return true
	}
	return runner(work)
}

type githubRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func (c *Checker) fetchLatestRelease(ctx context.Context) (*githubRelease, error) {
	return c.fetchRelease(ctx, "latest")
}

func (c *Checker) fetchRelease(ctx context.Context, which string) (*githubRelease, error) {
	ctx, cancel := context.WithTimeout(ctx, metadataTimeout)
	defer cancel()
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/%s", c.resolveRepo(), which)
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

func assetName(version string) string {
	platform, arch := hostTarget()
	return AssetName(version, platform, arch)
}

func hostTarget() (platform, arch string) {
	return packagefmt.HostPlatform(), runtime.GOARCH
}

func verifyReleaseSig(sigBytes []byte, root *sigenvelope.PublicKeyEnvelope, archiveHash, version, platform, arch string) (json.RawMessage, error) {
	return verifySigOfKind(artifactKindReleaseSig, sigBytes, root, archiveHash, version, platform, arch)
}

func verifySigOfKind(kind string, sigBytes []byte, root *sigenvelope.PublicKeyEnvelope, archiveHash, version, platform, arch string) (json.RawMessage, error) {

	if err := packagefmt.ValidatePlatformReleaseRoot(root); err != nil {
		return nil, fmt.Errorf("release signature root refused: %w", err)
	}

	raw, err := sigenvelope.VerifyPayload(sigBytes, root, kind, crypto.ProfileRoot)
	if err != nil {
		return nil, fmt.Errorf("signature does not verify against platform root: %w", err)
	}

	var payload releaseArchivePayload
	if err := canonicaljson.DecodeStrict(raw, &payload); errors.Is(err, canonicaljson.ErrTrailingContent) {
		return nil, fmt.Errorf("trailing data after payload object")
	} else if err != nil {
		return nil, fmt.Errorf("signature payload is not the closed {archive_hash} object: %w", err)
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

type updatePending struct {
	Attempts int `json:"attempts"`

	BackupSHA256 string `json:"backup_sha256"`

	NewSHA256 string `json:"new_sha256"`

	Version string `json:"version,omitempty"`

	Owner string `json:"owner,omitempty"`

	Beside []besideSwap `json:"beside,omitempty"`
}

type besideSwap struct {
	Name string `json:"name"`

	BackupSHA256 string `json:"backup_sha256,omitempty"`

	NewSHA256 string `json:"new_sha256"`
}

type standing struct {
	there bool
	data  []byte
}

func besideBackup(tgt target, name string) string {
	return filepath.Join(filepath.Dir(tgt.path), "."+name+".previous")
}

func besideBackups(tgt target, pend updatePending) []string {
	if tgt.path == "" || tgt.bundle {
		return nil
	}
	platform, _ := hostTarget()
	names := installedBeside(platform)
	for _, b := range pend.Beside {
		if !slices.Contains(names, b.Name) && b.Name == filepath.Base(b.Name) && b.Name != "" {
			names = append(names, b.Name)
		}
	}
	paths := make([]string, len(names))
	for i, name := range names {
		paths[i] = besideBackup(tgt, name)
	}
	return paths
}

func withdrawSwap(tgt target, pend updatePending) error {
	var errs []error
	for _, path := range besideBackups(tgt, pend) {
		errs = append(errs, withdraw(path))
	}
	if !tgt.bundle {
		errs = append(errs, withdraw(tgt.backup))
	}
	return errors.Join(append(errs, withdraw(tgt.record))...)
}

type target struct {
	path   string
	exe    string
	bundle bool
	lock   string
	record string
	backup string
	failed string

	incomplete string
}

func targetOf(exePath string) (target, error) {
	if exePath == "" {

		return target{}, errors.New("the running program's path is unknown")
	}
	exe, err := filepath.EvalSymlinks(exePath)
	if err != nil {
		return target{}, fmt.Errorf("resolve the running program %s: %w", exePath, err)
	}
	return targetAt(exe), nil
}

func Program() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate the running program: %w", err)
	}
	tgt, err := targetOf(exe)
	if err != nil {
		return "", err
	}
	return tgt.path, nil
}

func Leftovers(exe string) []string {
	t := targetAt(exe)
	names := []string{t.lock, t.record, t.backup}
	if t.failed != "" {
		names = append(names, t.failed, t.incomplete)
	}
	return append(names, besideBackups(t, updatePending{})...)
}

func stageStem(target string) string { return "." + filepath.Base(target) + ".stage-" }

func Beside() (files, stems []string, err error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, nil, fmt.Errorf("locate the running program: %w", err)
	}
	tgt, err := targetOf(exe)
	if err != nil {
		return nil, nil, err
	}
	files = Leftovers(tgt.exe)
	dir := filepath.Dir(tgt.path)
	if tgt.bundle {
		return files, []string{filepath.Join(dir, bundleStagePrefix)}, nil
	}
	stems = []string{filepath.Join(dir, stageStem(tgt.path))}
	platform, _ := hostTarget()
	for _, name := range installedBeside(platform) {
		files = append(files, filepath.Join(dir, name))
		stems = append(stems, filepath.Join(dir, stageStem(name)))
	}
	return files, stems, nil
}

func targetAt(exe string) target {
	if app, ok := bundleRoot(exe); ok {
		dir := filepath.Dir(app)
		return target{path: app, exe: exe, bundle: true,
			lock:   filepath.Join(dir, ".aii-os-update.lock"),
			record: filepath.Join(dir, ".aii-os-update.json"),
			backup: filepath.Join(dir, previousBundleName)}
	}
	dir, base := filepath.Split(exe)
	return target{path: exe, exe: exe,
		lock:       filepath.Join(dir, "."+base+".update.lock"),
		record:     filepath.Join(dir, "."+base+".update.json"),
		backup:     filepath.Join(dir, "."+base+".previous"),
		failed:     filepath.Join(dir, "."+base+".update.failed.json"),
		incomplete: filepath.Join(dir, "."+base+".update.incomplete.json")}
}

type failedUpdate struct {
	Version   string    `json:"version"`
	NewSHA256 string    `json:"new_sha256"`
	At        time.Time `json:"at"`
}

type FailedHereError struct {
	Version, NewSHA256 string
	At                 time.Time
}

func (e *FailedHereError) Error() string {
	return fmt.Sprintf("%s did not finish a boot on this machine on %s and was rolled back; it is not installed again here. A later release is installed as usual",
		e.Version, e.At.UTC().Format("2006-01-02"))
}

func (t target) refusedHere(available string) error {
	if t.failed == "" {
		return nil
	}
	raw, err := atomicfile.ReadFile(t.failed)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	var f failedUpdate
	if err == nil {
		err = json.Unmarshal(raw, &f)
	}
	switch {
	case err != nil:
		logsink.Warn("updates.refusal", "the record of a release rolled back here (%s) does not read: %v — it is removed, and updates go on as if none had failed", t.failed, err)
	case f.Version != "" && f.Version == available:
		return &FailedHereError{Version: f.Version, NewSHA256: f.NewSHA256, At: f.At}
	}
	os.Remove(t.failed)
	return nil
}

func ownerOf(dataDir string) (string, error) {
	abs, err := filepath.Abs(dataDir)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

func lockTarget(tgt target) (release func(), err error) {
	f, err := os.OpenFile(tgt.lock, os.O_CREATE|os.O_RDONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open the update lock: %w", err)
	}
	if err := filelock.Lock(f); err != nil {
		f.Close()
		if errors.Is(err, filelock.ErrHeld) {
			return nil, fmt.Errorf("%w — another process is replacing %s (%s): %w", ErrAlreadyApplying, tgt.path, tgt.lock, err)
		}
		return nil, fmt.Errorf("lock %s: %w", tgt.lock, err)
	}
	return func() { f.Close() }, nil
}

func (tgt target) replace(newHash string, swap func(oldHash string) error) (already bool, err error) {
	release, err := lockTarget(tgt)
	if err != nil {
		return false, err
	}
	defer release()
	replaceStep("locked")
	have, err := imageHash(tgt.path)
	if err != nil {
		return false, fmt.Errorf("read the installed image to compare: %w", err)
	}
	if have == newHash {
		return true, nil
	}
	if err := tgt.unclaimed(); err != nil {
		return false, err
	}
	return false, swap(have)
}

func (tgt target) unclaimed() error {
	p, present, err := readClaim(tgt.record)
	switch {
	case !present:
		return nil
	case err != nil:
		return fmt.Errorf("%w: the update record %s cannot be read (%v) — it is kept, since no identity can be shown to own it; once no update awaits a restart, remove it by hand", ErrUpdatePending, tgt.record, err)
	default:
		return fmt.Errorf("%w: it was installed by the identity whose data is at %s, and that identity's next healthy boot retires it", ErrUpdatePending, p.Owner)
	}
}

func (tgt target) claimedBy(owner string) bool {
	p, present, err := readClaim(tgt.record)
	return present && err == nil && p.Owner == owner
}

var replaceStep = func(string) {}

const (
	markerFile = ".boot_completed"

	previousFile = "aii.previous"
	pendingFile  = ".update_pending"

	retiredMarkerFile = ".boot_completed.retired"
)

func stageFileDurably(target string, data []byte, mode os.FileMode) (string, error) {
	f, err := os.CreateTemp(filepath.Dir(target), stageStem(target)+"*")
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

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {

	published, err := atomicfile.WriteReplace(path, data, mode)
	if err != nil {
		if published {
			return fmt.Errorf("replace %s: published but not durable: %w", filepath.Base(path), err)
		}
		return fmt.Errorf("replace %s: %w", filepath.Base(path), err)
	}
	return nil
}

func writeRecord(path string, p updatePending) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, raw, 0o644)
}

func readRecord(path string) (p updatePending, present bool, err error) {
	raw, err := atomicfile.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) && statRefusal(path, err) == nil {
		return updatePending{}, false, nil
	}
	if err != nil {
		return updatePending{}, true, err
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return updatePending{}, true, err
	}
	return p, true, nil
}

func readClaim(path string) (updatePending, bool, error) {
	p, present, err := readRecord(path)
	if present && err == nil && p.Owner == "" {
		err = errors.New("it names no owner")
	}
	return p, present, err
}

func canStageBeside() (bool, string) {
	exe, err := os.Executable()
	if err != nil {
		return false, "cannot locate the running binary: " + err.Error()
	}
	tgt, err := targetOf(exe)
	if err != nil {
		return false, err.Error()
	}
	return canStageBesideAt(tgt.path)
}

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

func readPending(dataDir string) (updatePending, bool) {
	p, present, err := readRecord(filepath.Join(dataDir, pendingFile))
	return p, present && err == nil
}

func swapBinary(tgt target, newBinary []byte, beside []installedFile, dataDir, owner, version string) error {
	currentBytes, err := os.ReadFile(tgt.path)
	if err != nil {
		return fmt.Errorf("read current binary for backup: %w", err)
	}

	dir := filepath.Dir(tgt.path)
	stood := make([]standing, len(beside))
	pend := updatePending{
		Attempts:     0,
		BackupSHA256: sha256hex(currentBytes),
		NewSHA256:    sha256hex(newBinary),
		Version:      version,
		Owner:        owner,
	}
	for i, f := range beside {
		entry := besideSwap{Name: f.name, NewSHA256: sha256hex(f.data)}
		old, err := os.ReadFile(filepath.Join(dir, f.name))
		switch {
		case err == nil:
			stood[i] = standing{there: true, data: old}
			entry.BackupSHA256 = sha256hex(old)
		case !errors.Is(err, os.ErrNotExist):
			return fmt.Errorf("read %s for backup: %w", f.name, err)
		}
		pend.Beside = append(pend.Beside, entry)
	}

	if err := writeRecord(tgt.record, pend); err != nil {

		return errors.Join(fmt.Errorf("write update record: %w", err), withdraw(tgt.record))
	}
	replaceStep("recorded")

	if err := writeFileAtomic(tgt.backup, currentBytes, 0o755); err != nil {

		return errors.Join(fmt.Errorf("write backup binary: %w", err), withdraw(tgt.record))
	}

	for i, f := range beside {
		if !stood[i].there {
			continue
		}
		if err := writeFileAtomic(besideBackup(tgt, f.name), stood[i].data, 0o644); err != nil {

			return errors.Join(fmt.Errorf("write backup of %s: %w", f.name, err), withdrawSwap(tgt, pend))
		}
	}
	replaceStep("backed-up")

	markerRetired, retireErr := retireBootMarker(dataDir)

	rearmMarker := func() (bool, error) {
		if !markerRetired {
			return true, nil
		}
		return rearmBootMarker(dataDir)
	}

	retireSwapState := func(primary error, what string) error {
		if back, rerr := rearmMarker(); rerr != nil {
			state := "NOT re-armed"
			if back {
				state = "re-armed but not durably"
			}
			return errors.Join(
				fmt.Errorf("%s: %w", what, primary),
				fmt.Errorf("boot marker %s — backup and record kept so the next boot can still recover: %w", state, rerr))
		}
		return errors.Join(fmt.Errorf("%s: %w", what, primary), withdrawSwap(tgt, pend))
	}

	if retireErr != nil {
		return retireSwapState(retireErr, "retire boot marker")
	}
	replaceStep("marker-retired")

	tmpPath, err := stageFileDurably(tgt.path, newBinary, 0o755)
	if err != nil {

		return retireSwapState(err, "write new binary")
	}

	putBack := func(n int) error {
		var errs []error
		for i := n - 1; i >= 0; i-- {
			path := filepath.Join(dir, beside[i].name)
			if stood[i].there {
				errs = append(errs, installBeside(path, stood[i].data))
			} else {
				errs = append(errs, withdraw(path))
			}
		}
		return errors.Join(errs...)
	}
	abandon := func(n int, primary error, what string) error {
		os.Remove(tmpPath)
		if perr := putBack(n); perr != nil {
			return errors.Join(
				fmt.Errorf("%s: %w", what, primary),
				fmt.Errorf("what the update had written beside %s could not all be put back — backups and record kept so the next boot can: %w", filepath.Base(tgt.path), perr))
		}
		return retireSwapState(primary, what)
	}
	for i, f := range beside {
		if err := installBeside(filepath.Join(dir, f.name), f.data); err != nil {

			return abandon(undoThrough(i, err), err, "install "+f.name+" ("+filepath.Base(tgt.path)+" not swapped)")
		}
	}
	replaceStep("beside-installed")

	published, err := atomicfile.ReplaceExecutable(tmpPath, tgt.path)
	if err != nil {
		if !published {

			return abandon(len(beside), err, "rename new binary into place")
		}

		return fmt.Errorf("new binary published but not durable — backup and record kept so the next boot can still roll back: %w", err)
	}
	replaceStep("published")
	return nil
}

func withdraw(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("could not withdraw %s: %w", path, err)
	}
	return nil
}

func extractBinary(archiveBytes []byte) (binary []byte, beside []installedFile, err error) {
	platform, _ := hostTarget()
	if platform == "windows" {
		return extractFromZip(platform, archiveBytes)
	}
	return extractFromTarGz(platform, archiveBytes)
}

func retireBootMarker(dataDir string) (bool, error) {

	return classifyRetire(atomicfile.Replace(
		filepath.Join(dataDir, markerFile),
		filepath.Join(dataDir, retiredMarkerFile)))
}

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

func rearmBootMarker(dataDir string) (back bool, err error) {

	return atomicfile.Replace(
		filepath.Join(dataDir, retiredMarkerFile),
		filepath.Join(dataDir, markerFile))
}

var ErrBootRecoveryPending = errors.New("boot recovery remains armed; wait for boot completion before upgrading data paths")

type recovery struct {
	dataDir        string
	backup, record string
	tgt            target
	legacy         bool
}

func recoveryOf(dataDir, exePath string) (r recovery, ok bool, err error) {
	tgt, terr := targetOf(exePath)
	if legacyStands(dataDir) {

		return recovery{dataDir: dataDir, backup: filepath.Join(dataDir, previousFile), record: filepath.Join(dataDir, pendingFile), tgt: tgt, legacy: true}, true, nil
	}
	if terr != nil {
		return recovery{}, false, terr
	}
	mine, _, err := claimOf(dataDir, tgt)
	if !mine || tgt.bundle {
		return recovery{}, false, err
	}
	return recovery{dataDir: dataDir, backup: tgt.backup, record: tgt.record, tgt: tgt}, true, nil
}

func legacyStands(dataDir string) bool {
	prev := filepath.Join(dataDir, previousFile)
	_, err := os.Stat(prev)
	return err == nil || statRefusal(prev, err) != nil
}

func claimOf(dataDir string, tgt target) (mine bool, other string, err error) {
	p, present, err := readClaim(tgt.record)
	if !present {
		return false, "", nil
	}
	if err != nil {
		return false, "", fmt.Errorf("the update record %s cannot be read (%v) — no identity can be shown to own it, so no boot rolls it back or retires it, and it refuses every update of %s until it is removed by hand", tgt.record, err, tgt.path)
	}
	me, err := ownerOf(dataDir)
	if err != nil {
		return false, "", fmt.Errorf("cannot name this identity's data directory (%v) — the update record %s is left untouched", err, tgt.record)
	}
	if p.Owner != me {
		return false, p.Owner, nil
	}
	return true, "", nil
}

func RequireSettledBootUpdate(dataDir string) error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate the running program to read its update state: %w", err)
	}
	return requireSettledAt(dataDir, exePath)
}

func requireSettledAt(dataDir, exePath string) error {
	r, ok, err := recoveryOf(dataDir, exePath)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if _, err := os.Stat(r.backup); err != nil {
		return statRefusal(r.backup, err)
	}
	marker := filepath.Join(dataDir, markerFile)
	if _, err := os.Stat(marker); err != nil {
		if refusal := statRefusal(marker, err); refusal != nil {
			return refusal
		}
		return ErrBootRecoveryPending
	}
	return nil
}

func WriteBootMarker(dataDir string) {
	exePath, err := os.Executable()
	if err != nil {
		logsink.Warn("updates.refusal", "cannot locate the running program (%v) — an update record beside it, if any, is left for a later boot", err)
		exePath = ""
	}
	writeBootMarkerAt(dataDir, exePath)
}

func writeBootMarkerAt(dataDir, exePath string) {

	if err := writeFileAtomic(filepath.Join(dataDir, markerFile), []byte("ok"), 0o644); err != nil {

		logsink.Warn("updates.error", "could not write boot marker: %v", err)
		return
	}

	os.Remove(filepath.Join(dataDir, pendingFile))
	os.Remove(filepath.Join(dataDir, previousFile))

	os.Remove(filepath.Join(dataDir, retiredMarkerFile))
	if exePath == "" {
		return
	}
	tgt, err := targetOf(exePath)
	if err != nil {
		logsink.Warn("updates.refusal", "%v — an update record beside it, if any, is left for a later boot", err)
		return
	}
	mine, other, _ := claimOf(dataDir, tgt)
	if !mine {
		if other != "" {
			logsink.Info("updates.decision", "the update of %s was applied by the identity whose data is at %s — this healthy boot leaves its backup and record alone; that identity's boot settles them", tgt.path, other)
		}
		return
	}

	pend, _, _ := readRecord(tgt.record)

	noteIncomplete(tgt, pend)
	r := recovery{dataDir: dataDir, backup: tgt.backup, record: tgt.record, tgt: tgt}
	if cut, err := r.settleCutOff(pend); err != nil {
		logsink.Warn("updates.error", "this healthy boot could not put back what a cut-off update had written beside %s: %v — its backups and record are kept, and the next healthy boot tries again", tgt.path, err)
		return
	} else if cut {
		logsink.Info("updates.decision", "an update of %s was cut off before its binary was published — what it had written beside it is put back", tgt.path)
	}
	if err := withdrawSwap(tgt, pend); err != nil {
		logsink.Warn("updates.error", "this healthy boot could not retire its update of %s: %v", tgt.path, err)
	}
}

func CheckRollback(dataDir string) string {
	exePath, err := os.Executable()
	if err != nil {
		logsink.Warn("updates.refusal", "rollback check skipped — cannot locate running binary: %v", err)
		return ""
	}
	return checkRollbackAt(dataDir, exePath)
}

func RollbackArmed(dataDir string) bool {
	exePath, err := os.Executable()
	if err != nil {
		exePath = ""
	}
	return rollbackArmedAt(dataDir, exePath)
}

func rollbackArmedAt(dataDir, exePath string) bool {
	r, ok, _ := recoveryOf(dataDir, exePath)
	if !ok {
		return false
	}
	if _, err := os.Stat(r.backup); err != nil {
		return false
	}
	markerPath := filepath.Join(dataDir, markerFile)
	if _, err := os.Stat(markerPath); err == nil || statRefusal(markerPath, err) != nil {
		return false
	}
	pend, present, err := readRecord(r.record)
	return present && err == nil && pend.Attempts >= 1
}

func checkRollbackAt(dataDir, exePath string) string {

	_ = os.Remove(exePath + ".old")

	platform, _ := hostTarget()
	for i, p := range ShippedPrograms(platform) {
		if i > 0 {
			_ = os.Remove(filepath.Join(filepath.Dir(exePath), p.Name) + ".old")
		}
	}

	r, ok, err := recoveryOf(dataDir, exePath)
	if ok {
		return settleLocked(dataDir, exePath, r.tgt, false)
	}
	if err != nil {
		logsink.Warn("updates.refusal", "rollback check: %v", err)
	}

	if !legacyStands(dataDir) {
		os.Remove(filepath.Join(dataDir, pendingFile))
	}
	return ""
}

func settleLocked(dataDir, exePath string, tgt target, uncounted bool) string {
	if tgt.path != "" {
		release, err := lockTarget(tgt)
		if errors.Is(err, ErrAlreadyApplying) {
			logsink.Info("updates.decision", "rollback check deferred — %v; this boot leaves the update state as it is, and the next boot settles it", err)
			return ""
		}
		if err != nil {
			logsink.Warn("updates.refusal", "rollback check deferred — %v; the update state is left as it is, and the next boot tries again", err)
			return ""
		}
		defer release()
	}
	r, ok, err := recoveryOf(dataDir, exePath)
	switch {
	case err != nil:
		logsink.Warn("updates.refusal", "rollback check: %v", err)
		return ""
	case !ok:
		return ""
	case r.tgt != tgt:
		logsink.Warn("updates.refusal", "rollback check deferred — the running program resolved to %s, then to %s; the update state is left as it is, and the next boot tries again", tgt.path, r.tgt.path)
		return ""
	}
	return r.settle(uncounted)
}

func RollBackUnsettled(dataDir string) string {
	exePath, err := os.Executable()
	if err != nil {
		logsink.Warn("updates.refusal", "rollback skipped — cannot locate running binary: %v", err)
		return ""
	}
	return rollBackUnsettledAt(dataDir, exePath)
}

func rollBackUnsettledAt(dataDir, exePath string) string {
	r, ok, err := recoveryOf(dataDir, exePath)
	if err != nil {
		logsink.Warn("updates.refusal", "rollback: %v", err)
		return ""
	}
	if !ok {
		return ""
	}
	return settleLocked(dataDir, exePath, r.tgt, true)
}

func (r recovery) settle(uncounted bool) string {
	markerPath := filepath.Join(r.dataDir, markerFile)

	if _, err := os.Stat(r.backup); err != nil {
		if refusal := statRefusal(r.backup, err); refusal != nil {

			logsink.Warn("updates.refusal", "rollback check cannot stat %s: %v — leaving update state untouched", r.backup, refusal)
			return ""
		}
		os.Remove(r.record)
		return ""
	}
	if _, err := os.Stat(markerPath); err == nil {

		pend, _, _ := readRecord(r.record)
		r.retire(pend)
		return ""
	} else if refusal := statRefusal(markerPath, err); refusal != nil {

		logsink.Warn("updates.refusal", "rollback check cannot stat %s: %v — leaving update state untouched", markerPath, refusal)
		return ""
	}

	pend, present, err := readRecord(r.record)
	if !present || err != nil {
		if !r.legacy {

			logsink.Warn("updates.refusal", "the update record %s became unreadable (%v) — leaving update state untouched", r.record, err)
			return ""
		}

		if uncounted {
			logsink.Warn("updates.refusal", "ROLLBACK DEFERRED \u2014 the update state beside %s is an earlier build's and has no record to restore by; the first start that reaches its boot adopts it", r.backup)
			return ""
		}
		prevBytes, err := atomicfile.ReadFile(r.backup)
		if err != nil {
			logsink.Warn("updates.decision", "unreadable backup with no tombstone — retiring it: %v", err)
			os.Remove(r.backup)
			return ""
		}
		if err := writeRecord(r.record, updatePending{
			Attempts:     1,
			BackupSHA256: sha256hex(prevBytes),
		}); err != nil {
			logsink.Warn("updates.error", "could not adopt legacy update state: %v", err)
		}
		return ""
	}

	if pend.Attempts == 0 && !uncounted {

		pend.Attempts = 1
		if err := writeRecord(r.record, pend); err != nil {
			logsink.Warn("updates.error", "could not record first boot attempt: %v", err)
		}
		return ""
	}

	return r.rollback(pend)
}

func (r recovery) rollback(pend updatePending) string {
	exePath := r.tgt.exe
	if exePath == "" {
		logsink.Warn("updates.refusal", "ROLLBACK DEFERRED \u2014 the running program could not be resolved; the backup %s and its record are kept, and the next boot tries again", r.backup)
		return ""
	}

	if ok, why := canStageBesideAt(exePath); !ok {
		logsink.Warn("updates.refusal", "ROLLBACK DEFERRED — %s; the previous binary is kept at %s (restore it with your package manager, or from data/backups/)", why, r.backup)
		return ""
	}

	prevBytes, err := atomicfile.ReadFile(r.backup)
	if err != nil {
		logsink.Error("updates.error", "ROLLBACK FAILED — cannot read backup binary: %v", err)
		return ""
	}

	if pend.BackupSHA256 != "" && sha256hex(prevBytes) != pend.BackupSHA256 {
		logsink.Error("updates.refusal", "ROLLBACK REFUSED — backup hashes to %.12s, swap recorded %.12s; the backup is not a backup, and the current binary stays",
			sha256hex(prevBytes), pend.BackupSHA256)
		return ""
	}

	if pend.NewSHA256 != "" {
		if cur, err := os.ReadFile(exePath); err == nil && sha256hex(cur) != pend.NewSHA256 {
			cut, err := r.settleCutOff(pend)
			if err != nil {
				logsink.Error("updates.error", "ROLLBACK FAILED — %v; the backups and the record are kept, and the next boot tries again", err)
				return ""
			}
			if cut {
				logsink.Info("updates.decision", "rollback: the update was cut off before its binary was published — what it had written beside it is put back; retiring update state")
			} else {
				logsink.Info("updates.decision", "rollback skipped — the binary changed since the update (operator repair); retiring update state")
			}
			r.retire(pend)
			return ""
		}
	}

	if err := r.restoreBeside(pend); err != nil {
		logsink.Error("updates.error", "ROLLBACK FAILED — %v; the backups and the record are kept, and the next boot tries again", err)
		return ""
	}

	tmpRestore, err := stageFileDurably(exePath, prevBytes, 0o755)
	if err != nil {
		logsink.Error("updates.error", "ROLLBACK FAILED — cannot stage previous binary: %v", err)
		return ""
	}
	restored, rerr := atomicfile.ReplaceExecutable(tmpRestore, exePath)
	if rerr != nil && !restored {
		os.Remove(tmpRestore)
		logsink.Error("updates.error", "ROLLBACK FAILED — cannot restore previous binary: %v", rerr)
		return ""
	}
	if rerr != nil {

		logsink.Error("updates.error", "ROLLBACK restored the previous binary but could not make the directory entry durable — keeping the backup: %v", rerr)
		return r.backup
	}

	if r.tgt.failed != "" && pend.Version != "" {
		raw, err := json.Marshal(failedUpdate{Version: pend.Version, NewSHA256: pend.NewSHA256, At: time.Now().UTC()})
		if err == nil {
			err = writeFileAtomic(r.tgt.failed, raw, 0o644)
		}
		if err != nil {
			logsink.Warn("updates.error", "the rollback could not record which release it undid (%v) — the restored program may install %s again", err, pend.Version)
		}
	}
	r.retire(pend)
	logsink.Warn("updates.end", "ROLLBACK — restored previous binary (update failed to boot)")
	return r.backup
}

func (r recovery) retire(pend updatePending) {
	if !r.legacy {
		for _, path := range besideBackups(r.tgt, pend) {
			os.Remove(path)
		}
	}
	os.Remove(r.backup)
	os.Remove(r.record)
}

func (r recovery) settleCutOff(pend updatePending) (cut bool, err error) {
	if r.legacy || r.tgt.bundle || r.tgt.exe == "" || len(pend.Beside) == 0 || pend.BackupSHA256 == "" {
		return false, nil
	}
	if have, herr := imageHash(r.tgt.exe); herr != nil || have != pend.BackupSHA256 {
		return false, nil
	}
	return true, r.restoreBeside(pend)
}

func (r recovery) restoreBeside(pend updatePending) error {
	for _, b := range pend.Beside {
		if b.Name == "" || b.Name != filepath.Base(b.Name) {
			logsink.Warn("updates.refusal", "rollback: the update record names %q beside the binary, which is not a file name — left alone", b.Name)
			continue
		}
		path := filepath.Join(filepath.Dir(r.tgt.exe), b.Name)
		cur, err := os.ReadFile(path)

		absent := errors.Is(err, os.ErrNotExist) && b.BackupSHA256 != ""
		if !absent && (err != nil || sha256hex(cur) != b.NewSHA256) {
			logsink.Info("updates.decision", "rollback: %s is not the file the update installed (changed or removed since) — left as it is", b.Name)
			continue
		}
		if b.BackupSHA256 == "" {
			if err := os.Remove(path); err != nil {
				logsink.Warn("updates.error", "rollback: %s, which the update added, could not be removed: %v", b.Name, err)
			}
			continue
		}
		prev, err := atomicfile.ReadFile(besideBackup(r.tgt, b.Name))
		if err != nil || sha256hex(prev) != b.BackupSHA256 {
			logsink.Error("updates.refusal", "rollback: what was kept of %s is not what the swap recorded (%v) — the update's %s stays", b.Name, err, b.Name)
			continue
		}
		if err := installBeside(path, prev); err != nil {
			return fmt.Errorf("cannot restore %s beside the binary: %w", b.Name, err)
		}
	}
	return nil
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

func StageRefusal() string {
	if ok, why := canStageBeside(); !ok {
		return why
	}
	return ""
}
