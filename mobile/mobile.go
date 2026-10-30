package mobile

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/app"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

var BuildCommit = "devel"

func BuildID() string { return BuildCommit }

type Runtime struct {
	a                   *app.App
	configPath, dataDir string
	restartMu           sync.Mutex
	restart             *restartAttempt
}

type restartAttempt struct {
	done chan struct{}
	next *Runtime
	err  error
}

type startupCleanupError struct{ cause error }

func (e *startupCleanupError) Error() string { return e.cause.Error() }
func (e *startupCleanupError) Unwrap() error { return e.cause }

func Start(configPath, dataDir string, deviceKey DeviceKey) (*Runtime, error) {
	if deviceKey == nil {
		return nil, errNoDeviceKey
	}
	crypto.SetDeviceKey(deviceKey)
	return start(configPath, dataDir, true)
}

func start(configPath, dataDir string, cold bool) (*Runtime, error) {

	if dataDir == "" {

		return nil, fmt.Errorf("mobile: empty container dir — the shell must pass its app-private directory")
	}
	if err := os.Chdir(dataDir); err != nil {

		return nil, fmt.Errorf("mobile: cannot enter the app container %q: %w", dataDir, err)
	}

	load := app.LoadConfig
	if !cold {
		load = app.ReadConfig
	}
	cfg, err := load(configPath)
	if err != nil {
		if !cold {
			return nil, err
		}
		cfg, err = quarantineUnreadableConfig(configPath, err)
		if err != nil {
			return nil, err
		}
	}
	begin := (*app.App).StartAuthenticatedEmbedded
	if err := rerootContainerPaths(cfg, dataDir); err != nil {
		var choose *identityChoiceError
		if !errors.As(err, &choose) {
			return nil, err
		}

		begin = func(a *app.App) error { return a.StartAuthenticatedEmbeddedAtChoice(choose.choice) }
	}
	a := app.New(cfg)
	if err := begin(a); err != nil {

		if closeErr := a.Shutdown(context.Background()); closeErr != nil {
			return nil, &startupCleanupError{cause: errors.Join(err, closeErr)}
		}
		return nil, err
	}
	return &Runtime{a: a, configPath: configPath, dataDir: dataDir}, nil
}

func (r *Runtime) WaitForRestart() bool { return r.a.WaitForRestart() }

func (r *Runtime) Restart() (*Runtime, error) {
	r.restartMu.Lock()
	attempt := r.restart
	owner := attempt == nil
	if owner {
		attempt = &restartAttempt{done: make(chan struct{})}
		r.restart = attempt
	}
	r.restartMu.Unlock()
	if !owner {
		<-attempt.done
		return attempt.next, attempt.err
	}
	retryable := false
	attempt.err = r.a.Shutdown(context.Background())
	if attempt.err == nil {
		attempt.next, attempt.err = start(r.configPath, r.dataDir, false)
		var cleanup *startupCleanupError
		retryable = attempt.err != nil && !errors.As(attempt.err, &cleanup)
	}
	r.restartMu.Lock()
	if retryable {
		r.restart = nil
	}
	close(attempt.done)
	r.restartMu.Unlock()
	return attempt.next, attempt.err
}

func (r *Runtime) CanRetryRestart() bool {
	r.restartMu.Lock()
	defer r.restartMu.Unlock()
	return r.restart == nil
}

func (r *Runtime) DashboardURL() string { return r.a.DashboardURL() }

func (r *Runtime) DashboardAccessTokenForOrigin(origin string) (string, error) {
	return r.a.DashboardAccessTokenForOrigin(origin)
}

func (r *Runtime) SetForeground(live bool) { r.a.SetForeground(live) }

func (r *Runtime) TimeWake() { r.a.TimeWake() }

type WakeScheduler interface {
	Schedule(atUnixMs int64)
	Cancel()
}

type wakeAdapter struct {
	s WakeScheduler
}

func (w wakeAdapter) WakeAt(at time.Time) error {
	w.s.Schedule(at.UnixMilli())
	return nil
}

func (w wakeAdapter) WakeClear() { w.s.Cancel() }

func (r *Runtime) SetWakeScheduler(s WakeScheduler) {
	if s == nil {
		r.a.SetPlatformWake(nil)
		return
	}
	r.a.SetPlatformWake(wakeAdapter{s: s})
}

func (r *Runtime) Shutdown() error { return r.a.Shutdown(context.Background()) }

func rerootContainerPaths(cfg *app.Config, dataDir string) error {
	if cfg == nil || dataDir == "" {
		return nil
	}
	type field struct {
		name string
		p    *string
	}
	type resolution struct {
		app.IdentityFiles
		p  *string
		to string
	}
	var moved []resolution
	for _, f := range []field{{"ledger_path", &cfg.Identity.LedgerPath}, {"db_path", &cfg.Identity.DBPath}, {"key_path", &cfg.Identity.KeyPath}} {
		was := *f.p
		if was == "" || !filepath.IsAbs(was) {
			continue
		}
		if rel, err := filepath.Rel(dataDir, was); err == nil && !strings.HasPrefix(rel, "..") {
			continue
		}

		parts := strings.Split(filepath.ToSlash(was), "/")
		cands := make([]string, 0, 5)
		for k := min(4, len(parts)-1); k >= 1; k-- {
			cands = append(cands, filepath.Join(append([]string{dataDir}, parts[len(parts)-k:]...)...))
		}
		cands = append(cands, filepath.Join(dataDir, "data", filepath.Base(was)))
		seen := map[string]bool{}
		var found []string
		for _, cand := range cands {
			if seen[cand] {
				continue
			}
			seen[cand] = true
			if st, err := os.Stat(cand); err == nil && st.Mode().IsRegular() {
				found = append(found, cand)
			}
		}

		to := filepath.Join(dataDir, "data", filepath.Base(was))
		if len(found) > 0 {
			to = found[0]
		}
		moved = append(moved, resolution{IdentityFiles: app.IdentityFiles{Key: f.name, Was: was, Found: found}, p: f.p, to: to})
	}
	var why []string
	for _, m := range moved {
		if len(m.Found) > 1 {
			why = append(why, fmt.Sprintf("%s is ambiguous — %s all exist in this container", m.Key, strings.Join(m.Found, " and ")))
		}
	}

	if len(why) == 0 {
		for i := 1; i < len(moved); i++ {
			if filepath.Dir(moved[i].to) != filepath.Dir(moved[0].to) {
				why = append(why, fmt.Sprintf("re-rooted identity files resolved into different directories (%s in %s, %s in %s) — one identity lives in one place", moved[0].Key, filepath.Dir(moved[0].to), moved[i].Key, filepath.Dir(moved[i].to)))
				break
			}
		}
	}
	if len(why) > 0 {
		choice := app.IdentityChoice{Home: dataDir, Why: "mobile: " + strings.Join(why, "; ") + "; this start does not choose between identity records"}
		for _, m := range moved {
			choice.Files = append(choice.Files, m.IdentityFiles)
		}
		return &identityChoiceError{choice}
	}
	for _, m := range moved {
		*m.p = m.to
		logsink.Info("config.decision", "mobile: %s pointed outside this container (%s) — re-rooted to %s", m.Key, m.Was, m.to)
	}
	return nil
}

type identityChoiceError struct{ choice app.IdentityChoice }

func (e *identityChoiceError) Error() string { return e.choice.Why }

func quarantineUnreadableConfig(configPath string, cause error) (*app.Config, error) {

	raw, err := os.ReadFile(configPath)
	if err != nil {
		return nil, cause
	}
	aside := app.SetAsideName(configPath, time.Now())
	if rerr := os.Rename(configPath, aside); rerr != nil {

		logsink.Warn("config.error", "mobile: config is unreadable (%v) and could not be set aside (%v)", cause, rerr)
		return nil, cause
	}
	cfg, lost, err := app.SalvageConfig(configPath, raw)
	if err != nil {

		if rerr := os.Rename(aside, configPath); rerr != nil {
			return nil, fmt.Errorf("mobile: config was unreadable (%w), what could be read of it was not written (%v), and it could not be put back from %s (%v)", cause, err, filepath.Base(aside), rerr)
		}
		return nil, fmt.Errorf("mobile: config was unreadable (%w) and what could be read of it was not written (%v); it is where it was", cause, err)
	}
	notKept := "every setting in it is kept"
	if len(lost) > 0 {
		notKept = "not kept: " + strings.Join(lost, ", ")
	}
	logsink.Warn("boot.refusal", "mobile: config was unreadable (%v) — set aside as %s and started from what could be read of it (%s); "+
		"the identity's record and key are untouched", cause, filepath.Base(aside), notKept)
	return cfg, nil
}

type ForegroundNeedListener interface {
	Need(active bool, reason string)
}

func (r *Runtime) SetForegroundNeedListener(l ForegroundNeedListener) {
	if l == nil {
		r.a.SubscribeForegroundNeed(nil)
		return
	}
	r.a.SubscribeForegroundNeed(func(need bool, reason string) { l.Need(need, reason) })
}
