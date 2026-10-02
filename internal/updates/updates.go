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
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/filelock"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/quiesce"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
	"github.com/aiii-dot-id/aii-os/internal/version"
	"github.com/aiii-dot-id/aii-os/internal/vulkancap"
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

const artifactKindReleaseSig = "release.platform_release"

const artifactKindEvidenceSig = "release.evidence_bundle"

type State struct {
	mu           sync.RWMutex
	availableVer string
	lastCheck    time.Time
	lastError    string
	installedVer string
	needsRestart bool
	checking     bool
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
		Checking:         s.checking,
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
}

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

func (s *State) SetAvailable(ver string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.availableVer = ver
}

func (s *State) SetLastError(err string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastError = err
}

func (s *State) SetInstalled(ver string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.installedVer = ver
	s.needsRestart = true
	s.availableVer = ""
}

func (s *State) MarkChecked() {
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
	inFlight bool
	applying atomic.Bool

	runner        func(work func()) bool
	repo          func() string
	gate          *quiesce.Gate
	dataDir       string
	httpClient    *http.Client
	badRepoOnce   sync.Once
	noReleaseOnce sync.Once
	mu            sync.Mutex
	cachedRelease *githubRelease

	hostAllowlist func(string) error
}

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
		c.state.SetAvailable("")
		c.state.MarkChecked()
		c.state.SetLastError(reason)
		return ""
	}
	if err != nil {
		logsink.Warn("updates.error", "check failed: %v", err)
		c.state.SetLastError(err.Error())
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
		c.state.SetLastError(err.Error())
		return ""
	}

	cmp := version.Compare(latestVer, currentVer)
	if cmp <= 0 {

		c.state.SetAvailable("")
		c.state.MarkChecked()
		logsink.Info("updates.end", "running %s, latest %s — current", currentVer, latestVer)
		return ""
	}

	c.state.SetAvailable(latestVer)
	c.state.MarkChecked()
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

	archiveBytes, err := c.download(ctx, archiveURL)
	if err != nil {
		return fmt.Errorf("download archive: %w", err)
	}

	sigBytes, err := c.download(ctx, sigURL)
	if err != nil {
		return fmt.Errorf("download signature: %w", err)
	}

	archiveHash := sha256hex(archiveBytes)

	hostPlatform, hostArch := hostTarget()
	signedPayload, err := verifyReleaseSig(sigBytes, root, archiveHash, available, hostPlatform, hostArch)
	if err != nil {
		return fmt.Errorf("signature verification REFUSED: %w", err)
	}

	revRoots := packagefmt.ReleaseTrustRoots(filepath.Join(c.dataDir, "trust"), root)
	if err := packagefmt.CheckReleaseRevocation(revRoots, artifactKindReleaseSig, signedPayload); err != nil {
		return fmt.Errorf("release REFUSED: %w", err)
	}

	logsink.Info("updates.decision", "signature verified for %s (hash %s)", assetName, archiveHash[:12])

	afterVerified()

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("update not installed: %w", err)
	}
	handled, already, err := applyIfBundle(tgt, archiveBytes, owner)
	if err != nil {
		return fmt.Errorf("bundle update: %w", err)
	}
	if !handled {
		if already, err = installBinary(tgt, archiveBytes, c.dataDir, owner); err != nil {
			return err
		}
	}

	c.state.SetInstalled(available)
	if already {
		logsink.Info("updates.decision", "%s is already the image on disk — nothing replaced, nothing backed up; restart to apply", available)
		return nil
	}
	logsink.Info("updates.end", "installed %s — restart to apply", available)
	return nil
}

func installBinary(tgt target, archiveBytes []byte, dataDir, owner string) (already bool, err error) {

	newBinary, helper, err := extractBinary(archiveBytes)
	if err != nil {
		return false, fmt.Errorf("extract binary from archive: %w", err)
	}
	return tgt.replace(sha256hex(newBinary), func(string) error {
		return swapWithHelper(tgt, newBinary, helper, dataDir, owner)
	})
}

func swapWithHelper(tgt target, newBinary, helper []byte, dataDir, owner string) error {
	if helper != nil {
		if err := writeFileAtomic(vulkancap.HelperPath(tgt.path), helper, 0o755); err != nil {
			return fmt.Errorf("install %s (%s not swapped): %w", vulkancap.Helper, filepath.Base(tgt.path), err)
		}
	}

	if err := swapBinary(tgt, newBinary, dataDir, owner); err != nil {
		return fmt.Errorf("binary swap: %w", err)
	}
	return nil
}

func (c *Checker) Run(ctx context.Context, isSafe func() bool, isMobile func() bool) {

	if cur := c.currentVer(); !version.Valid(cur) {
		logsink.Info("updates.refusal", "this build carries no release version (%q) — update checking is off for the life of this process; a released build carries one and checks normally", cur)
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

func (c *Checker) arm(isSafe, isMobile func() bool) {
	c.mu.Lock()
	c.isSafe, c.isMobile = isSafe, isMobile
	c.mu.Unlock()
	c.armed.Store(true)
}

func (c *Checker) Armed() bool { return c.armed.Load() }

func (c *Checker) tick(ctx context.Context) {
	available := c.Check(ctx)

	if available == "" {
		return
	}

	c.mu.Lock()
	isMobile := c.isMobile
	c.mu.Unlock()
	mobile := isMobile != nil && isMobile()
	automatic := c.automatic()

	staged, stageWhy := canStageBeside()

	if mobile || !automatic || !staged {
		if !staged {
			logsink.Info("updates.decision", "%s available — inform only (%s)", available, stageWhy)
		} else {
			logsink.Info("updates.decision", "%s available — inform only (mobile=%v, automatic=%v)", available, mobile, automatic)
		}
		return
	}

	if err := c.Apply(ctx); errors.Is(err, ErrUpdatePending) || errors.Is(err, ErrAlreadyApplying) {

		logsink.Warn("updates.refusal", "%s not installed by this attempt — %v", available, err)
	} else if err != nil {
		logsink.Warn("updates.error", "apply failed: %v", err)
		c.state.SetLastError(err.Error())
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
	work := func() {
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
	}

	if !c.run(work) {
		c.state.SetChecking(false)
		c.mu.Lock()
		c.inFlight = false
		c.mu.Unlock()
		return ErrStopping
	}
	return nil
}

var afterVerified = func() {}

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

type updatePending struct {
	Attempts int `json:"attempts"`

	BackupSHA256 string `json:"backup_sha256"`

	NewSHA256 string `json:"new_sha256"`

	Owner string `json:"owner,omitempty"`
}

type target struct {
	path   string
	exe    string
	bundle bool
	lock   string
	record string
	backup string
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
		lock:   filepath.Join(dir, "."+base+".update.lock"),
		record: filepath.Join(dir, "."+base+".update.json"),
		backup: filepath.Join(dir, "."+base+".previous")}
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
	raw, err := os.ReadFile(path)
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

func swapBinary(tgt target, newBinary []byte, dataDir, owner string) error {
	currentBytes, err := os.ReadFile(tgt.path)
	if err != nil {
		return fmt.Errorf("read current binary for backup: %w", err)
	}

	if err := writeRecord(tgt.record, updatePending{
		Attempts:     0,
		BackupSHA256: sha256hex(currentBytes),
		NewSHA256:    sha256hex(newBinary),
		Owner:        owner,
	}); err != nil {

		return errors.Join(fmt.Errorf("write update record: %w", err), withdraw(tgt.record))
	}
	replaceStep("recorded")

	if err := writeFileAtomic(tgt.backup, currentBytes, 0o755); err != nil {

		return errors.Join(fmt.Errorf("write backup binary: %w", err), withdraw(tgt.record))
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
		return errors.Join(fmt.Errorf("%s: %w", what, primary), withdraw(tgt.backup), withdraw(tgt.record))
	}

	if retireErr != nil {
		return retireSwapState(retireErr, "retire boot marker")
	}
	replaceStep("marker-retired")

	tmpPath, err := stageFileDurably(tgt.path, newBinary, 0o755)
	if err != nil {

		return retireSwapState(err, "write new binary")
	}

	published, err := atomicfile.ReplaceExecutable(tmpPath, tgt.path)
	if err != nil {
		if !published {

			os.Remove(tmpPath)
			return retireSwapState(err, "rename new binary into place")
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

func extractBinary(archiveBytes []byte) (binary, helper []byte, err error) {
	if runtime.GOOS == "windows" {
		binary, err = extractFromZip(archiveBytes)
		return binary, nil, err
	}
	return extractFromTarGz(archiveBytes)
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
	var errs []error
	if !tgt.bundle {
		errs = append(errs, withdraw(tgt.backup))
	}
	errs = append(errs, withdraw(tgt.record))
	if err := errors.Join(errs...); err != nil {
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

	r, ok, err := recoveryOf(dataDir, exePath)
	if ok {
		return settleLocked(dataDir, exePath, r.tgt)
	}
	if err != nil {
		logsink.Warn("updates.refusal", "rollback check: %v", err)
	}

	if !legacyStands(dataDir) {
		os.Remove(filepath.Join(dataDir, pendingFile))
	}
	return ""
}

func settleLocked(dataDir, exePath string, tgt target) string {
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
	return r.settle()
}

func (r recovery) settle() string {
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

		os.Remove(r.backup)
		os.Remove(r.record)
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

		prevBytes, err := os.ReadFile(r.backup)
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

	if pend.Attempts == 0 {

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

	prevBytes, err := os.ReadFile(r.backup)
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
			logsink.Info("updates.decision", "rollback skipped — the binary changed since the update (operator repair); retiring update state")
			os.Remove(r.backup)
			os.Remove(r.record)
			return ""
		}
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
	os.Remove(r.backup)
	os.Remove(r.record)
	logsink.Warn("updates.end", "ROLLBACK — restored previous binary (update failed to boot)")
	return r.backup
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
