// Package mobile is the gomobile bind surface — what the Android and
// iOS shells embed (MOBILE_PORT §1). It exists because gomobile cannot
// bind internal/ packages and restricts bound types; this package keeps
// the surface to strings, bools, and methods on one opaque handle.
//
// Shell contract:
//
//	rt, err := mobile.Start(configPath)   // non-blocking
//	rt.DashboardURL()                     // load in the WebView
//	rt.SetForeground(true|false)          // app lifecycle → presence
//	rt.SetWakeScheduler(s)                // OS scheduler learns next-due
//	rt.TimeWake()                         // OS wake → TIME catch-up
//	rt.Stop()                             // shell owns process death
//
// The host app passes the container dir; the runtime chdirs into it
// (entirely local — relative paths resolve inside the app sandbox).
package mobile

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/app"
)

// Runtime is the running AII OS instance, opaque to the shell.
type Runtime struct {
	a *app.App
}

// Start loads the config (creating a FIRSTBOOT default when absent) and
// starts the runtime without blocking. The shell owns lifecycle.
func Start(configPath, dataDir string) (*Runtime, error) {
	// ENTIRELY LOCAL, mobile edition: the app container IS the install
	// directory. Chdir into it and every relative default (config.json,
	// data/) lands inside the sandbox — the same one-directory law as
	// desktop. (The dir arrives as an ARGUMENT: gomobile snapshots the
	// process env at library load, so a shell setenv never reaches Go —
	// live stall, 2026-08-18. No env var is set; cwd is the mechanism.)
	if dataDir == "" {
		// FAIL CLOSED (Sev 2026-08-26, P1): an empty container dir means
		// the shell passed nothing, and every relative path — config,
		// data/, tool cwd — would resolve against whatever directory the
		// process happened to inherit. There is no correct guess.
		return nil, fmt.Errorf("mobile: empty container dir — the shell must pass its app-private directory")
	}
	if err := os.Chdir(dataDir); err != nil {
		// FAIL CLOSED (Sev batch 2, P2-5): the container dir IS the
		// one-directory law on mobile. Continuing after a failed
		// entry ran every relative path — tools cwd, logs, data —
		// against whatever directory the process happened to launch
		// in: an obsolete container on iOS, / on a misconfigured
		// shell. A shell that cannot enter its own container has
		// nothing safe to start.
		return nil, fmt.Errorf("mobile: cannot enter the app container %q: %w", dataDir, err)
	}
	cfg, err := app.LoadConfig(configPath)
	if err != nil {
		cfg, err = quarantineUnreadableConfig(configPath, err)
		if err != nil {
			return nil, err
		}
	}
	if err := rerootContainerPaths(cfg, dataDir); err != nil {
		return nil, err
	}
	a := app.New(cfg)
	if err := a.StartEmbedded(); err != nil {
		// A late failure (e.g. dashboard bind) leaves plugins and core
		// resources live; the caller only gets the error, never the
		// App, so the teardown must happen here (external review
		// 2026-08-20, M9). Stop is idempotent and nil-tolerant.
		a.Stop()
		return nil, err
	}
	return &Runtime{a: a}, nil
}

// DashboardURL is the address the shell's WebView loads.
func (r *Runtime) DashboardURL() string { return r.a.DashboardURL() }

// SetForeground reports app lifecycle: foreground = operator present.
func (r *Runtime) SetForeground(live bool) { r.a.SetForeground(live) }

// TimeWake is the platform wake entry: AlarmManager receiver (Android)
// or BGTask handler (iOS) calls this; TIME fires everything due.
func (r *Runtime) TimeWake() { r.a.TimeWake() }

// WakeScheduler is the OUTBOUND half of the OS-wake loop (MOBILE_PORT
// §2): the shell implements it in Kotlin/Swift, and TIME drives it with
// its next-due moment so the OS can wake the process back into TimeWake.
// ONE slot, by contract: each Schedule supersedes the previous request
// (AlarmManager's same-PendingIntent set(), BGTaskScheduler's
// same-identifier submit — both already mean "replace"); Cancel means
// nothing is worth waking for. atUnixMs is UTC wall milliseconds —
// int64 because gomobile cannot carry time.Time across the boundary.
//
// Exactness is the PLATFORM's, not the contract's: Android with the
// exact-alarm grant is near-exact, without it Doze-batched; iOS
// BGTaskScheduler is opportunistic by design. TIME tolerates every
// case — a late wake evaluates late, never lost (canon #13).
type WakeScheduler interface {
	Schedule(atUnixMs int64)
	Cancel()
}

// wakeAdapter turns the shell's WakeScheduler into TIME's PlatformWake.
// The shapes differ only in the timestamp type; the one-slot,
// supersede-on-write semantics pass through untouched.
type wakeAdapter struct {
	s WakeScheduler
}

func (w wakeAdapter) WakeAt(at time.Time) error {
	w.s.Schedule(at.UnixMilli())
	return nil
}

func (w wakeAdapter) WakeClear() { w.s.Cancel() }

// SetWakeScheduler registers the shell's OS wake scheduler. Call it
// right after Start; nil restores the desktop no-op. The registration
// survives FIRSTBOOT (TIME does not exist until birth completes — the
// app stores the implementation and installs it when the cognitive
// runtime comes up).
func (r *Runtime) SetWakeScheduler(s WakeScheduler) {
	if s == nil {
		r.a.SetPlatformWake(nil)
		return
	}
	r.a.SetPlatformWake(wakeAdapter{s: s})
}

// Stop shuts the runtime down. Safe to call once.
func (r *Runtime) Stop() { r.a.Stop() }

// Version returns the platform version string (injected via ldflags
// from the VERSION file). Empty = a dev build. The mobile shell
// displays this in its settings surface.
func Version() string { return app.Version }

// rerootContainerPaths repairs identity paths that point at a container
// the OS has since moved.
//
// FOUND BY RUNNING IT (iOS Simulator, 2026-08-24). The desktop law is
// that the install directory is the identity's whole world, and on
// mobile Start() honours it by chdir-ing into the app container so every
// RELATIVE default lands inside the sandbox. That works perfectly for a
// fresh config and not at all for a persisted one: iOS keeps Documents
// content across an app update but reassigns the Data container's UUID,
// so a config written yesterday carries absolute paths into a directory
// that no longer exists.
//
// The observed failure was total and silent. ledger.jsonl, aii.db and
// identity.sec — the record, the projection and the IDENTITY'S SIGNING
// KEY — all resolved into a dead container, Start returned an error the
// shell discarded, and the app showed a blank screen. Every iOS update
// would have stranded the resident.
//
// Only paths that fall OUTSIDE the live container are moved: a relative
// path is already correct (chdir handles it), and a path the operator
// deliberately pointed elsewhere on a desktop build never reaches here.
//
// HOW they move is the P0 (Sev 2026-08-26): the OS moved the container
// out from under the config, not the files out of the container — iOS
// preserves the CONTENT and layout while reassigning the UUID. The
// path's tail is therefore the truth and its head is the lie, so the
// preserved file is found by suffix, deepest first (data/ledger.jsonl
// before ledger.jsonl), with the standard location data/<name> always
// among the candidates. The first cut flattened to basename instead,
// which lost the data/ layout, pointed the config at files that do not
// exist, and turned every container move into FIRSTBOOT — a second
// identity minted over a preserved one. When nothing is found the path
// lands on the standard layout for FIRSTBOOT to build; when MORE THAN
// ONE candidate exists the function refuses — choosing between two
// identity records is minting or erasing one by side effect.
func rerootContainerPaths(cfg *app.Config, dataDir string) error {
	if cfg == nil || dataDir == "" {
		return nil
	}
	type resolution struct{ name, dir string }
	var moved []resolution
	fix := func(name string, p *string) error {
		if *p == "" || !filepath.IsAbs(*p) {
			return nil // relative paths already resolve under the container
		}
		if rel, err := filepath.Rel(dataDir, *p); err == nil && !strings.HasPrefix(rel, "..") {
			return nil // already inside this container
		}
		was := *p
		// Candidates: suffixes of the stale path, deepest first, plus the
		// standard location. Bounded at 4 components — identity files live
		// at most a few levels deep, and a longer tail only re-includes
		// dead-container segments that cannot exist here.
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
		switch len(found) {
		case 1:
			*p = found[0]
		case 0:
			// A blank container with a stale config: land on the standard
			// layout so a legitimate FIRSTBOOT builds where every other
			// path expects it.
			*p = filepath.Join(dataDir, "data", filepath.Base(was))
		default:
			return fmt.Errorf("mobile: %s is ambiguous — %s all exist in this container; refusing to choose between identity records (recovery required)", name, strings.Join(found, " and "))
		}
		log.Printf("mobile: %s pointed outside this container (%s) — re-rooted to %s", name, was, *p)
		moved = append(moved, resolution{name: name, dir: filepath.Dir(*p)})
		return nil
	}
	if err := fix("ledger_path", &cfg.Identity.LedgerPath); err != nil {
		return err
	}
	if err := fix("db_path", &cfg.Identity.DBPath); err != nil {
		return err
	}
	if err := fix("key_path", &cfg.Identity.KeyPath); err != nil {
		return err
	}
	// One identity lives in one place. Fields re-rooted this boot must
	// agree on the directory: a record from data/ paired with a key from
	// the container root is two identities interleaved — the flattening
	// bug's other wake (D02 residual, Sev 2026-08-26). Refuse the mix.
	for i := 1; i < len(moved); i++ {
		if moved[i].dir != moved[0].dir {
			return fmt.Errorf("mobile: re-rooted identity files resolved into different directories (%s in %s, %s in %s) — one identity lives in one place; refusing a mixed set (recovery required)", moved[0].name, moved[0].dir, moved[i].name, moved[i].dir)
		}
	}
	return nil
}

// quarantineUnreadableConfig moves a config this build cannot read aside
// and starts from a fresh default. MOBILE ONLY, deliberately.
//
// The desktop contract is right and unchanged: an unreadable config is a
// hard refusal, because the operator can open the file and fix it. On
// mobile they cannot. config.json lives inside the app container, and
// the only way to remove a bad one is to delete the app — which deletes
// the container, and with it ledger.jsonl, aii.db and identity.sec. A
// config the operator cannot reach must never be able to cost them their
// identity.
//
// Observed on iOS (2026-08-24): a config written in August carried
// llm.endpoint, a field since moved to providers.json, and
// DisallowUnknownFields made it fatal. The app showed "could not start"
// with no route back. Every schema change would have done the same to
// every existing mobile install.
//
// FAIL-CLOSED IS PRESERVED WHERE IT MATTERS. We still refuse to RUN on a
// config we do not understand — it is renamed, not repaired, not
// partially honoured, and never silently ignored. What survives is the
// identity, whose record and key are separate files. The operator loses
// stale settings; they do not lose their resident.
func quarantineUnreadableConfig(configPath string, cause error) (*app.Config, error) {
	raw, _ := os.ReadFile(configPath) // before the rename: the salvage source
	aside := configPath + ".unreadable-" + time.Now().UTC().Format("20060102T150405Z")
	if rerr := os.Rename(configPath, aside); rerr != nil {
		// Nothing was moved, so nothing is lost — report the real cause.
		log.Printf("mobile: config is unreadable (%v) and could not be set aside (%v)", cause, rerr)
		return nil, cause
	}
	log.Printf("mobile: config was unreadable (%v) — set aside as %s and started from a default; "+
		"the identity's record and key are untouched", cause, filepath.Base(aside))
	cfg, err := app.LoadConfig(configPath)
	if err != nil {
		return nil, err
	}
	// The unreadable file's identity block is the map to the resident.
	// Defaults without it can point at an empty world while the real
	// ledger sits at an operator path — the D02 residual fork (Sev
	// 2026-08-26).
	if adopted, perr := app.SalvageIdentityInto(raw, cfg); len(adopted) > 0 {
		if perr != nil {
			// Fail closed: locations that hold for one boot only are a
			// deferred fork — the next boot would forget them and
			// firstboot over the identity. Identity survival outranks
			// this boot's availability.
			return nil, fmt.Errorf("mobile: identity locations were salvaged from the unreadable config but could not be persisted: %w", perr)
		}
		log.Printf("mobile: salvaged from the unreadable config: %s", strings.Join(adopted, ", "))
	}
	return cfg, nil
}

// ForegroundNeedListener hears the process's need to stay alive flip
// (gomobile-safe: bool + string). true arrives with a reason the
// shell can show in its persistent notification ("turn",
// "work: alarm.rhythm"); false means nothing holds and the shell may
// let the OS suspend. The Go side never touches the OS —
// Service.startForeground and BGProcessingTask stay in Kotlin/Swift
// (sev_foreground's architecture, taken as inspiration per the
// 2026-08-26 ruling, not mirrored).
type ForegroundNeedListener interface {
	Need(active bool, reason string)
}

// SetForegroundNeedListener registers the shell's listener; nil
// clears. A late attach hears an immediate catch-up with the current
// state — the same law as SetWakeScheduler.
//
// Edges arrive IN ORDER (the Go side serializes them), so the last
// edge is always the truth. The shell SHOULD debounce the false edge
// by a few seconds before stopping its foreground service: turns
// chain quickly (a released turn often starts the next), and Android
// restricts restarting services from the background — flapping the
// notification is worse than holding it briefly idle.
func (r *Runtime) SetForegroundNeedListener(l ForegroundNeedListener) {
	if l == nil {
		r.a.SubscribeForegroundNeed(nil)
		return
	}
	r.a.SubscribeForegroundNeed(func(need bool, reason string) { l.Need(need, reason) })
}

// DashboardMintedToken returns the R74 dashboard access token IF this
// boot minted one and it has not been read yet, else "" — the shell's
// ONE read, enforced: the runtime clears the value as it hands it
// over (D77), so any second call gets "". Store it in the platform
// keystore (Keychain / Android Keystore) and supply it to the WebView
// as the aii_token cookie; the runtime keeps only the hash, so no
// later call can recover it. Wiring the WebView side belongs to the
// shell batch (device-proof required).
func (r *Runtime) DashboardMintedToken() string { return r.a.DashboardMintedToken() }
