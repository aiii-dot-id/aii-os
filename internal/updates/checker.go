package updates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"net/http"
	neturl "net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/quiesce"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
	"github.com/aiii-dot-id/aii-os/internal/version"
)

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

	runner func(work func()) bool

	lists func(context.Context) (string, error)

	trustSet func() *packagefmt.RevocationStatusSet

	stranded func(ctx context.Context, offered string) ([]StrandedPlugin, string)

	interval func() time.Duration
	rearm    chan struct{}

	ownSigned json.RawMessage
	repo      func() string

	apiURL  func() string
	gate    *quiesce.Gate
	dataDir string

	pkgListPath, pkgExe string
	canStage            func() (bool, string)
	httpClient          *http.Client
	badRepoOnce         sync.Once
	noReleaseOnce       sync.Once
	saidFailedHere      string
	saidBelow           string
	saidSecond          string

	ownRelease    func(context.Context) (*githubRelease, error)
	mu            sync.Mutex
	cachedRelease *githubRelease

	afterVerified func()

	hostAllowlist func(source *neturl.URL, raw string) error
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
		rearm:         make(chan struct{}, 1),
		hostAllowlist: releaseHost,
		httpClient:    &http.Client{},
	}

	c.httpClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("too many redirects")
		}
		return c.hostAllowlist(sourceOf(req.Context()), req.URL.String())
	}
	return c
}

type sourceKey struct{}

func withSource(ctx context.Context, source *neturl.URL) context.Context {
	return context.WithValue(ctx, sourceKey{}, source)
}

func sourceOf(ctx context.Context) *neturl.URL {
	source, _ := ctx.Value(sourceKey{}).(*neturl.URL)
	return source
}

func (c *Checker) sameSource(release *githubRelease) error {
	now, err := c.apiBase()
	if err != nil {
		return err
	}
	if release.source == nil || release.source.String() != now.String() {
		then := "no recorded source"
		if release.source != nil {
			then = release.source.String()
		}
		return fmt.Errorf("%w (read from %s, %s in force now)", ErrReleaseSourceChanged, then, now)
	}
	return nil
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

func (c *Checker) Run(ctx context.Context) {

	if cur := c.currentVer(); !version.Valid(cur) {
		logsink.Info("updates.refusal", "this build carries no release version (%q) — update checking is off for the life of this process; a released build carries one and checks normally", cur)
		return
	}

	c.armed.Store(true)

	every := c.checkInterval()
	tk := quiesce.NewTicker(c.gate, every)
	defer func() { tk.Stop() }()
	follow := func() {
		if d := c.checkInterval(); d != every {
			tk.Stop()
			every = d
			tk = quiesce.NewTicker(c.gate, every)
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.rearm:
			follow()
			continue
		case <-tk.C:
		}
		follow()

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

	listsErr := c.BringTrustLists(ctx)

	c.lookAtOwnRelease(ctx)

	c.lookAtStranded(ctx, available)

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

	if errors.Is(listsErr, ErrReleaseListBehind) {
		logsink.Warn("updates.refusal", "%s not installed automatically: %v", available, listsErr)
		return
	}

	var failedHere *FailedHereError
	var below *BelowHighestVerifiedError
	var second *SecondArchiveError
	if err := c.Apply(ctx); errors.Is(err, ErrUpdatePending) || errors.Is(err, ErrAlreadyApplying) {

		logsink.Warn("updates.refusal", "%s not installed by this attempt — %v", available, err)
	} else if errors.As(err, &failedHere) {

		c.sayRefusal(err, failedHere.Version, &c.saidFailedHere)
	} else if errors.As(err, &below) {

		c.sayRefusal(err, below.Offered, &c.saidBelow)
	} else if errors.As(err, &second) {

		c.sayRefusal(err, second.Version, &c.saidSecond)
	} else if err != nil {
		logsink.Warn("updates.error", "apply failed: %v", err)
		c.state.setLastError(err.Error())
	}
}

func (c *Checker) sayRefusal(err error, release string, said *string) {
	c.state.setLastError(err.Error())
	c.mu.Lock()
	again := *said == release
	*said = release
	c.mu.Unlock()
	if !again {
		logsink.Warn("updates.refusal", "%v", err)
	}
}

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

func (c *Checker) SetRunner(run func(work func()) bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.runner = run
}

func (c *Checker) SetInterval(every func() time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.interval = every
}

func (c *Checker) SetAPIURL(get func() string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.apiURL = get
}

func (c *Checker) apiBase() (*neturl.URL, error) {
	c.mu.Lock()
	get := c.apiURL
	c.mu.Unlock()
	raw := ""
	if get != nil {
		raw = get()
	}
	base, err := APIBase(raw)
	if err != nil {
		return nil, fmt.Errorf("updates.api_url: %w", err)
	}
	return base, nil
}

func (c *Checker) Rearm() {
	select {
	case c.rearm <- struct{}{}:
	default:
	}
}

func (c *Checker) Interval() time.Duration { return c.checkInterval() }

func (c *Checker) checkInterval() time.Duration {
	c.mu.Lock()
	every := c.interval
	c.mu.Unlock()
	if every != nil {
		if d := every(); d > 0 {
			return d
		}
	}
	return DefaultCheckIntervalMinutes * time.Minute
}

func (c *Checker) SetTrustLists(bring func(context.Context) (note string, err error)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lists = bring
}

func (c *Checker) SetStranded(look func(ctx context.Context, offered string) ([]StrandedPlugin, string)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stranded = look
}

func (c *Checker) lookAtStranded(ctx context.Context, offered string) {
	c.mu.Lock()
	look := c.stranded
	c.mu.Unlock()
	if look == nil || offered == "" {
		c.state.setStranded(nil, "")
		return
	}
	c.state.setStranded(look(ctx, offered))
}

func (c *Checker) BringTrustLists(ctx context.Context) error {
	c.mu.Lock()
	bring := c.lists
	c.mu.Unlock()
	if bring == nil {
		return nil
	}
	note, err := bring(ctx)
	why := ""
	if err != nil {
		logsink.Warn("updates.error", "withdrawal lists: %v", err)
		why = err.Error()
	}

	if c.state.setLists(why, note) && note != "" {
		logsink.Info("updates.decision", "withdrawal lists: %s", note)
	}
	return err
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
