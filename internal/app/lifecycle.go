package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

func (a *App) lifetime() context.Context {
	if a.bgCtx != nil {
		return a.bgCtx
	}
	return context.Background()
}

var errStopping = errors.New("the identity is stopping")

func (a *App) runBackground(run func()) bool {
	a.bgMu.Lock()
	defer a.bgMu.Unlock()
	if a.stopping {
		return false
	}
	a.bgWG.Add(1)
	go func() {
		defer a.bgWG.Done()
		run()
	}()
	return true
}

func (a *App) untilStopped(ctx context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(ctx)
	if a.bgCtx == nil {
		return ctx, cancel
	}
	stop := context.AfterFunc(a.bgCtx, cancel)
	return ctx, func() { stop(); cancel() }
}

func (a *App) StartEmbedded() (err error) {

	release, err := a.prepareStart()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			release()
		}
	}()
	choice, err := a.chooseBoot()
	if err != nil {
		return err
	}
	if choice == bootFirstboot {
		return a.startFirstboot()
	}
	return a.startLive()
}

func (a *App) claimBeforeStart() (release func(), err error) {
	return a.claimIdentity(filepath.Dir(a.configSnapshot().Identity.LedgerPath))
}

func (a *App) prepareStart() (release func(), err error) {

	took := a.waitForHandover()

	a.startAttempt = takeStartAttempt()
	release, err = took.claim(a.claimBeforeStart)
	if err != nil {
		return nil, took.refusedAfter(err)
	}

	home, herr := filepath.Abs(filepath.Dir(a.cfg.Identity.LedgerPath))
	if herr != nil {
		home = filepath.Dir(a.cfg.Identity.LedgerPath)
	}
	cfgPath, cerr := filepath.Abs(a.cfg.SourcePath)
	if cerr != nil {
		cfgPath = a.cfg.SourcePath
	}

	if err := a.installLogSink(); err != nil {
		release()
		return nil, err
	}
	logsink.Info("boot.start", "Identity home: %s (config %s)", home, cfgPath)

	logsink.Info("boot.start", "Boot identity: AII OS v%s (build %s)", VersionString(), BuildIdentity())

	return release, nil
}

func (a *App) DashboardURL() string {
	if a.dashboard == nil {
		return ""
	}
	local := a.dashboard.LocalURL()
	if local == "" {
		return ""
	}
	return local + "/"
}

func (a *App) SubscribeForegroundNeed(fn func(needed bool, reason string)) {
	a.fg.Subscribe(fn)
}

func (a *App) TimeWake() {
	a.wakeMu.Lock()
	tf := a.timeFac
	a.wakeMu.Unlock()
	if tf != nil {
		tf.TimeWake()
	}
}

func (a *App) SetPlatformWake(w cognitive.PlatformWake) {
	a.wakeMu.Lock()
	a.platformWake = w

	tf := a.timeFac
	a.wakeMu.Unlock()
	if tf != nil {
		tf.SetPlatformWake(w)
		tf.TimeWake()
	}
}

func (a *App) installPlatformWake() {
	a.wakeMu.Lock()
	w := a.platformWake
	a.wakeMu.Unlock()
	if w == nil {
		a.timeFac.SetPlatformWake(cognitive.NoopWake{})
		return
	}
	a.timeFac.SetPlatformWake(w)
}

func (a *App) SetForeground(live bool) {
	if a.pulseSource != nil {
		a.pulseSource.setOverride(live)
	}
	if live {
		a.gate.Resume()

		a.pn.Returned()
	} else {
		a.gate.Pause()
	}
}

func (a *App) seedHomeDocs() {
	if a.configSnapshot().SourcePath == "" {

		logsink.Info("seed.decision", "no configuration file names a home: the docs are not seeded")
		return
	}
	a.seedSkillsDoc()
	a.seedMethodDoc()
}

func (a *App) Run() {

	if _, err := a.prepareStart(); err != nil {
		log.Fatalf("Startup failed: %v", err)
	}

	choice, err := a.chooseBoot()
	if err != nil {
		log.Fatalf("Startup refused: %v", err)
	}

	stopCh := watchStopRequest()

	if choice == bootFirstboot {
		if err := a.startFirstboot(); err != nil {
			log.Fatalf("Startup failed: %v", err)
		}
	} else {
		if err := a.startLive(); err != nil {
			log.Fatalf("Startup failed: %v", err)
		}
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-sigChan:
	case <-stopCh:
	case <-a.restartCh:
	}

	leaveAfterShutdown(a.Shutdown(context.Background()), a.restarting.Load(), relaunch, os.Exit)
}

func leaveAfterShutdown(teardown error, restart bool, relaunch func(teardownFailed bool), exit func(int)) {
	if teardown != nil {
		logsink.Error("boot.error", "shutdown did not complete cleanly: %v", teardown)
	}
	switch {
	case restart:
		if teardown != nil {
			logsink.Warn("boot.decision", "a restart was asked, so the next start is handed the identity all the same: it verifies the record before it extends it")
		}
		relaunch(teardown != nil)
	case teardown != nil:
		exit(1)
	}
}

func (a *App) Restart() error {
	a.restartOnce.Do(func() {
		a.restarting.Store(true)
		if a.restartCh != nil {
			close(a.restartCh)
		}
	})
	return nil
}

type ShutdownPending struct{ Cause error }

func (e *ShutdownPending) Error() string { return "shutdown still in progress: " + e.Cause.Error() }
func (e *ShutdownPending) Unwrap() error { return e.Cause }

func (a *App) Shutdown(ctx context.Context) error {
	a.stopOnce.Do(func() {
		a.stopDone = make(chan struct{})
		a.bgMu.Lock()
		a.stopping = true

		if a.bgStop != nil {
			a.bgStop(a.stopCause())
		}
		if a.bgCancel != nil {
			a.bgCancel()
		}
		a.bgMu.Unlock()
		go func() {
			a.stopErr = a.stop()
			close(a.stopDone)
		}()
	})

	select {
	case <-a.stopDone:
		return a.stopErr
	default:
	}
	select {
	case <-a.stopDone:
		return a.stopErr
	case <-ctx.Done():
		return &ShutdownPending{Cause: ctx.Err()}
	}
}

const stopDrainDefault = 5 * time.Second

func (a *App) stopDrain() time.Duration {
	if n := a.configSnapshot().Dashboard.StopDrainSeconds; n > 0 {
		return time.Duration(n) * time.Second
	}
	return stopDrainDefault
}

func (a *App) WaitForRestart() bool {
	select {
	case <-a.bgCtx.Done():
		return false
	case <-a.restartCh:
		return a.bgCtx.Err() == nil
	}
}

func (a *App) stopCause() conversation.TurnStop {
	if a.restarting.Load() {
		return conversation.RuntimeRestarting
	}
	return conversation.RuntimeShuttingDown
}

func (a *App) stopRunningTurn() {
	cause := a.stopCause()
	a.turnMu.Lock()
	if a.turnCancel != nil {
		a.turnCancel(cause)
	}
	a.turnMu.Unlock()
}

func (a *App) stop() error {
	logsink.Info("boot.end", "shutting down")

	a.stopSafeBeacon()
	a.stopStartRetry()

	a.stopRunningTurn()
	a.bgWG.Wait()

	shutCtx := context.Background()

	drainCut := false
	if a.dashboard != nil {
		drain, cancel := context.WithTimeout(shutCtx, a.stopDrain())
		err := a.dashboard.Shutdown(drain)
		cancel()
		var cut *dashboard.DrainCut
		switch {
		case err == nil:
		case errors.As(err, &cut):
			drainCut = true
			logsink.Warn("boot.end", "the dashboard's requests and jobs had not all ended after %s (dashboard.stop_drain_seconds); their connections are closed and the shutdown goes on", a.stopDrain())
		default:
			return fmt.Errorf("dashboard shutdown: %w", err)
		}
	}
	a.birthMu.Lock()
	defer a.birthMu.Unlock()
	if err := a.acquireTurn(shutCtx); err != nil {
		return fmt.Errorf("resident turn did not quiesce: %w", err)
	}
	err := a.closeLiveResources()

	if drainCut {
		settle, cancel := context.WithTimeout(shutCtx, a.stopDrain())
		if serr := a.dashboard.Settle(settle); serr != nil {
			err = errors.Join(err, fmt.Errorf("dashboard shutdown: after its connections and the identity's resources were closed, %w", serr))
		}
		cancel()
	}

	a.releaseIdentityClaim()
	a.releaseTurn()

	a.fg.Subscribe(nil)
	if a.logSink != nil {
		a.logSink.Close()
		a.logSink = nil
	}
	return err
}

func (a *App) closeLiveResources() error {
	a.databaseView.Store(nil)
	a.wakeMu.Lock()
	timeFac := a.timeFac
	a.timeFac = nil
	a.wakeMu.Unlock()
	if timeFac != nil {
		timeFac.Stop()
	}
	if a.executor != nil {
		a.executor.Stop()
		a.executor = nil
	}
	if a.timerOwner != nil {
		a.timerOwner.Stop()
		a.timerOwner = nil
	}

	a.pluginMu.Lock()
	f := a.facility
	a.pluginMu.Unlock()
	var unstopped []string
	if f != nil {
		f.Close()

		unstopped = f.Unretired()
	}

	a.pluginMu.Lock()
	plugins := a.plugins
	sectionActs := a.sectionActs
	a.plugins = nil
	a.sectionActs = nil
	a.activeMeta = nil
	retiring := a.retiring
	a.retiring = nil
	a.pluginMu.Unlock()

	var errs []error
	if len(unstopped) > 0 {
		errs = append(errs, fmt.Errorf("plugins that would not stop: %s", strings.Join(unstopped, " | ")))
	}

	for _, ap := range retiring {
		ctx, cancel := context.WithTimeout(context.Background(), pluginhost.StopBudget)
		if err := ap.CloseQuiet(ctx); err != nil {
			errs = append(errs, fmt.Errorf("stop pinned predecessor %s: %w", ap.ID, err))
		}
		cancel()
	}
	for _, ap := range plugins {
		ctx, cancel := context.WithTimeout(context.Background(), pluginhost.StopBudget)
		if err := ap.Deactivate(ctx); err != nil {
			errs = append(errs, fmt.Errorf("deactivate plugin %s: %w", ap.ID, err))
		}
		cancel()
	}

	if err := a.joinEngineWork(); err != nil {
		errs = append(errs, err)
	}
	for _, sec := range sectionActs {
		if a.sections != nil {
			a.sections.RemoveOwned(sec)
		}
		if err := sec.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close section %s: %w", sec.Decl.ID, err))
		}
	}
	if a.store != nil {
		if err := a.store.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close projection store: %w", err))
		}
		a.store = nil
	}
	if a.ledger != nil {
		if err := a.ledger.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close ledger: %w", err))
		}
		a.ledger = nil
	}
	a.live.Store(false)
	return errors.Join(errs...)
}
