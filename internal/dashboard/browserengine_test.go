//go:build !windows

package dashboard

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/browsertest"
)

// browserEngine is one engine plus how to drive it headlessly.
type browserEngine struct {
	// name is the engine's name for AII_BROWSER_ENGINES; where its
	// executable is installed is browsertest.Find's.
	name string
	// args builds the command line. profileDir is a fresh per-run directory.
	args func(profileDir, url string) []string
	// sizeArgs optionally adapts the launch to a window size, for tests
	// of width-scoped CSS (@media blocks). An engine that cannot be sized
	// at launch leaves it nil and is SKIPPED, named — never run at the
	// default width, where a phone test would either fail confusingly or
	// pass against rules that never applied.
	sizeArgs func(w, h int) []string
	// prepare optionally seeds the profile directory before launch.
	prepare func(profileDir string) error
	// home optionally gives the browser a home of its own: given a fresh
	// directory, it returns the environment that puts every per-user
	// directory the browser reads beneath it.
	home func(dir string) []string
}

var browserEngines = []browserEngine{
	{
		name: "blink-chrome",
		args: func(profileDir, url string) []string {
			return []string{
				"--headless=new", "--no-sandbox", "--disable-gpu",
				"--no-first-run", "--no-default-browser-check",
				"--disable-extensions", "--disable-background-networking",
				// MOTION IS REDUCED IN THE RIG. A smooth scroll is the
				// browser animating over wall-clock time, driven by frames;
				// under the gate's load the frames are starved and so is any
				// waiter watching for the animation to end, so a test of
				// WHERE the page ends up became a test of how fast this host
				// could paint (TestJumpToLatest..., 2177px short). This is a
				// real user setting the pages already honour, and reducing
				// motion in the test environment is the ordinary remedy.
				"--force-prefers-reduced-motion",
				"--user-data-dir=" + profileDir, url,
			}
		},
		sizeArgs: func(w, h int) []string {
			return []string{fmt.Sprintf("--window-size=%d,%d", w, h)}
		},
	},
	{
		name: "blink-edge",
		args: func(profileDir, url string) []string {
			return []string{
				"--headless=new", "--no-sandbox", "--disable-gpu",
				"--no-first-run", "--no-default-browser-check",
				"--force-prefers-reduced-motion", // see blink-chrome
				"--user-data-dir=" + profileDir, url,
			}
		},
		sizeArgs: func(w, h int) []string {
			return []string{fmt.Sprintf("--window-size=%d,%d", w, h)}
		},
	},
	{
		name: "gecko-firefox",
		args: func(profileDir, url string) []string {
			// -no-remote stops Firefox handing the URL to an already-running
			// instance and exiting, which would look like a silent pass.
			return []string{"--headless", "-no-remote", "-profile", profileDir, url}
		},
		sizeArgs: func(w, h int) []string {
			return []string{"-width", strconv.Itoa(w), "-height", strconv.Itoa(h)}
		},
		prepare: func(profileDir string) error {
			// A fresh profile otherwise spends its first run on migration,
			// default-browser and telemetry prompts, which can outlast the
			// test timeout on a cold machine.
			prefs := strings.Join([]string{
				`user_pref("browser.shell.checkDefaultBrowser", false);`,
				`user_pref("browser.startup.homepage_override.mstone", "ignore");`,
				`user_pref("toolkit.telemetry.reportingpolicy.firstRun", false);`,
				`user_pref("datareporting.policy.firstRunURL", "");`,
				`user_pref("browser.aboutwelcome.enabled", false);`,
				`user_pref("network.dns.offline-localhost", false);`,
				// Motion is reduced here as it is for the Blink engines: see
				// the note on blink-chrome's arguments.
				`user_pref("ui.prefersReducedMotion", 1);`,
			}, "\n")
			return os.WriteFile(filepath.Join(profileDir, "user.js"), []byte(prefs), 0o644)
		},

		home: func(dir string) []string {
			return []string{
				"HOME=" + dir,
				"CFFIXED_USER_HOME=" + dir,
				"XDG_CONFIG_HOME=" + filepath.Join(dir, ".config"),
				"XDG_CACHE_HOME=" + filepath.Join(dir, ".cache"),
				"XDG_DATA_HOME=" + filepath.Join(dir, ".local", "share"),
				"XDG_STATE_HOME=" + filepath.Join(dir, ".local", "state"),
				"TMPDIR=" + filepath.Join(dir, "tmp"),
			}
		},
	},
}

func resolveEngines() []struct {
	engine browserEngine
	path   string
} {
	var found []struct {
		engine browserEngine
		path   string
	}
	for _, candidate := range browserEngines {
		if !browsertest.Allowed(candidate.name) {
			continue
		}
		if resolved, _ := browsertest.Find(candidate.name); resolved != "" {
			found = append(found, struct {
				engine browserEngine
				path   string
			}{candidate, resolved})
		}
	}
	return found
}

// skipWithoutEngines is the skip of a host with no engine: it says where
// each engine was looked for.
func skipWithoutEngines(t testing.TB) {
	t.Helper()
	var where []string
	for _, e := range browserEngines {
		_, looked := browsertest.Find(e.name)
		where = append(where, e.name+": "+browsertest.Looked(e.name, looked))
	}
	t.Skip("no browser engine available; looked for " + strings.Join(where, "; "))
}

// harnessJS is served to the page under test. report() is the result
// channel; run() guarantees exactly one report even when the body throws.
const harnessJS = `
let reported = false;
const __rigFetch = fetch; // bound at load: no test clobber of window.fetch can break the report channel
export function report(text) {
  if (reported) return;
  reported = true;
  try {
    __rigFetch('/__result', { method: 'POST', body: text, keepalive: true }).catch(() => {
      if (navigator.sendBeacon) navigator.sendBeacon('/__result', text);
    });
  } catch (e) {
    try { if (navigator.sendBeacon) navigator.sendBeacon('/__result', text); } catch (e2) {}
  }
}
export function assert(ok, message) { if (!ok) throw new Error(message); }
const failure = (e) => 'FAIL: ' + ((e && e.message) || String(e));
// run reports OK only when the body has FINISHED without throwing. A body
// that returns a promise is awaited: the earlier shape called body(),
// reported OK at once, and a later rejection was dropped because a report
// had already been sent — so an async page could not fail, whatever it
// asserted (found 2026-09-02 by a label mutant the page's own assertion
// should have caught). assert is also handed to the body, so pages may
// take it as a parameter or import it; both are the same function.
export function run(body) {
  let r;
  try { r = body(assert); } catch (e) { report(failure(e)); return; }
  if (r && typeof r.then === 'function') { r.then(() => report('OK'), (e) => report(failure(e))); }
  else report('OK');
}
// until waits for a CONDITION, with a deadline — never for a duration.
//
// A fixed delay is a guess about a machine. It holds on a quiet desktop and
// breaks in the gate, where the race suite, two engines and the packaging
// build run at once: the same page then needs more time than the number
// somebody typed, and the assertion fires before the act it is about has
// happened. That is the whole of the browser flakiness this rig has
// produced (2026-08-27 history traversal, 2026-09-21 a pasted link), and it
// is what every browser-testing framework fixed by waiting on state instead
// of on the clock: Playwright's web-first assertions and expect.poll,
// Selenium's explicit waits. Their linters ban the sleep outright
// (playwright/no-wait-for-timeout, ui-testing/no-hard-wait); ours is
// TestBrowserPagesWaitForConditionsNotDurations.
//
// IT RESOLVES AT THE DEADLINE INSTEAD OF REJECTING, deliberately: the
// caller's own assertion then runs against what is really there and fails
// in the page's own words ("a pasted link did not perform the select...")
// rather than in the waiter's. A real defect reads exactly as it did
// before; only the false failure goes away.
export function until(cond, ms) {
  const deadline = Date.now() + (ms || 5000);
  return new Promise(resolve => {
    const tick = () => {
      if (cond() || Date.now() > deadline) return resolve();
      setTimeout(tick, 15);
    };
    tick();
  });
}
// when is until for a page written in callbacks rather than await.
export function when(cond, then, ms) { until(cond, ms).then(then); }
window.addEventListener('error', ev => report('FAIL: uncaught ' + ((ev.error && ev.error.message) || ev.message)));
window.addEventListener('unhandledrejection', ev => report('FAIL: rejected ' + String(ev.reason)));
`

// runPageInEngines serves page + modules and runs it in every installed
// engine as a subtest. The page must import /__harness.js and call run().
//
// A page that never reports is a FAILURE, not a pass — a browser that
// dies before executing must never look like success.
func runPageInEngines(t *testing.T, page string, modules map[string][]byte) {
	t.Helper()
	runPageInEnginesWithHeaders(t, page, modules, nil)
}

// runPageInEnginesWithHeaders is runPageInEngines plus response headers on
// the DOCUMENT response. It exists for Content-Security-Policy: a policy
// delivered by <meta> is not the policy that ships, and the differences
// are real (frame-ancestors is header-only). Testing the header means
// testing the artifact.
func runPageInEnginesWithHeaders(t *testing.T, page string, modules map[string][]byte, headers map[string]string) {
	t.Helper()

	engines := resolveEngines()
	if len(engines) == 0 {
		skipWithoutEngines(t)
	}

	for _, found := range engines {
		found := found
		t.Run(found.engine.name, func(t *testing.T) {
			runPageInOneEngine(t, found.engine, found.path, page, modules, headers, 0, 0, 0)
		})
	}
}

// runPageInOneEngine runs one engine against the page. w and h of 0,0 mean
// the engine default window; anything else is prepended as size flags, for
// width-scoped CSS that does not exist at the default width.
func runPageInOneEngine(t *testing.T, engine browserEngine, path string, page string, modules map[string][]byte, headers map[string]string, w, h int, inertFor time.Duration) {
	t.Helper()
	results := make(chan string, 4)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__result" {
			// The whole body, bounded: one Read returns whatever has arrived,
			// and a page that reports what it MEASURED sends more than "OK".
			body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			select {
			case results <- string(body):
			default:
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path == "/__harness.js" {
			w.Header().Set("Content-Type", "text/javascript")
			fmt.Fprint(w, harnessJS)
			return
		}
		if module, ok := modules[r.URL.Path]; ok {
			w.Header().Set("Content-Type", "text/javascript")
			if strings.HasSuffix(r.URL.Path, ".css") {
				w.Header().Set("Content-Type", "text/css")
			}
			w.Write(module)
			return
		}
		// A MODULE THE TEST DID NOT STUB IS SERVED FOR REAL. Every
		// unmapped path used to fall through to the HTML page below, so
		// an import the map had not heard of arrived as text/html and
		// died as a parse error — which is what a rig does the day a
		// module it serves gains a new import. A stub still wins (the
		// lookup above); this only decides what "not stubbed" means, and
		// the honest answer is the shipped file rather than a page.
		if strings.HasSuffix(r.URL.Path, ".js") {
			if real, err := staticFS.ReadFile("static" + r.URL.Path); err == nil {
				w.Header().Set("Content-Type", "text/javascript")
				w.Write(real)
				return
			}
		}
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		// The DEFAULT, not an override. A test that states its own
		// Content-Type is testing a document the browser will treat
		// differently — a navigated image/svg+xml is the case this
		// exists for — and forcing text/html here would have made that
		// test quietly not the case it claimed.
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
		}
		fmt.Fprint(w, page)
	}))
	defer server.Close()
	runServerInOneEngine(t, engine, path, server.URL, results, w, h, inertFor)
}

// runServerInOneEngine opens startURL in one engine and waits for the page's
// verdict on results, which the caller's server fills from POST /__result.
// It is the half of runPageInOneEngine that does not care what serves the
// page, so a test can put the dashboard's own server behind the rig.
func runServerInOneEngine(t testing.TB, engine browserEngine, path, startURL string, results <-chan string, w, h int, inertFor time.Duration) {
	t.Helper()
	// One directory per run holds the profile, the home of an engine that
	// keeps one, and what the browser writes to its output.
	root, err := os.MkdirTemp("", "aii-browser-*")
	if err != nil {
		t.Fatalf("browser dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(root) }()
	profileDir, home := filepath.Join(root, "profile"), filepath.Join(root, "home")
	for _, dir := range []string{profileDir, home} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatalf("browser dir: %v", err)
		}
	}
	if engine.home != nil {

		for _, dir := range []string{"Downloads", filepath.Join("Library", "Application Support"), "tmp"} {
			if err := os.MkdirAll(filepath.Join(home, dir), 0o700); err != nil {
				t.Fatalf("browser home: %v", err)
			}
		}
	}

	if engine.prepare != nil {
		if err := engine.prepare(profileDir); err != nil {
			t.Fatalf("profile prepare: %v", err)
		}
	}

	launchArgs := engine.args(profileDir, startURL)
	if w > 0 || h > 0 {
		if engine.sizeArgs == nil {
			t.Skipf("engine %s cannot be sized at launch; the %dx%d page cannot run here", engine.name, w, h)
		}
		launchArgs = append(engine.sizeArgs(w, h), launchArgs...)
	} else if engine.sizeArgs != nil {

		launchArgs = append(engine.sizeArgs(800, 600), launchArgs...)
	}

	budget := time.NewTimer(max(180*time.Second, inertFor+time.Second))
	defer budget.Stop()

	cmd := exec.Command(path, launchArgs...)
	cmd.Env = append(browserEnv(engine, home, os.Environ()), "MOZ_HEADLESS=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	cmd.Env = append(cmd.Env, "DBUS_SESSION_BUS_ADDRESS=disabled:")
	// WHAT THE BROWSER SAYS IS KEPT, so a page that never reports fails
	// with the browser's own account of why. Thrown away, a browser that
	// refused to start was indistinguishable from a page that hung.
	outputPath := filepath.Join(root, "output.log")
	output, err := os.Create(outputPath)
	if err != nil {
		t.Fatalf("browser output: %v", err)
	}
	cmd.Stdout, cmd.Stderr = output, output
	err = cmd.Start()
	_ = output.Close() // the browser holds its own copy
	if err != nil {
		t.Fatalf("%s failed to start: %v", engine.name, err)
	}
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()
	defer func() {
		if err := stopBrowserProcess(cmd, exited); err != nil {
			t.Errorf("%s cleanup: %v", engine.name, err)
		}
	}()

	// verdict reads a report. INERTNESS IS THE CLAIM when inertFor is set,
	// so silence is the pass and any report is the failure. A page that
	// cannot run script cannot report a verdict, which means the usual "no
	// report is a failure" rule would fail exactly the case being proven.
	// The witness is therefore OUTSIDE the browser — the caller watches
	// whether anything the page tried to do actually arrived — and the wait
	// below only gives the page real time to try.
	verdict := func(result string) {
		if inertFor > 0 {
			t.Fatalf("%s (%s): a page expected to be INERT reported %q — it ran", engine.name, path, result)
		}
		// A page that measures something says what it measured with its
		// verdict, "OK" then a newline then the measurement, so a passing
		// run still leaves the numbers in the log (a table of what a
		// screen reader can be told is no use to anyone who has to fail the
		// test to see it). Anything else is the failure it says.
		if measured, ok := strings.CutPrefix(result, "OK\n"); ok {
			t.Logf("%s measured:\n%s", engine.name, measured)
		} else if result != "OK" {
			t.Fatalf("%s (%s): %s", engine.name, path, result)
		}
	}
	var settled <-chan time.Time
	if inertFor > 0 {
		settled = time.After(inertFor)
	}
	gone := (<-chan struct{})(exited)
	for {
		select {
		case result := <-results:
			verdict(result)
			return
		case <-settled:
			return
		case <-gone:
			// The first process is not the whole browser: one that hands
			// the page to another of its processes may still report, and
			// only an empty group is a browser that cannot. A report that
			// came in as the browser went is still the verdict.
			if browserGroupAlive(cmd.Process.Pid) {
				gone = nil
				continue
			}
			select {
			case result := <-results:
				verdict(result)
				return
			default:
			}
			t.Fatalf("%s (%s) exited (%s) before the page reported; it said:\n%s", engine.name, path, cmd.ProcessState, browserSaid(outputPath))
		case <-budget.C:
			if inertFor > 0 {
				return
			}
			t.Fatalf("%s (%s): page never reported a result within the timeout; the browser said:\n%s", engine.name, path, browserSaid(outputPath))
		}
	}
}

// browserSaid is the end of what the browser wrote to its output, for a
// failure that would otherwise say only that nothing came.
func browserSaid(path string) string {
	out, err := os.ReadFile(path)
	if err != nil {
		return "(its output could not be read: " + err.Error() + ")"
	}
	const tail = 4 << 10
	if len(out) > tail {
		out = out[len(out)-tail:]
	}
	if said := strings.TrimSpace(string(out)); said != "" {
		return said
	}
	return "(nothing)"
}

// browserGroupAlive reports whether any process of the group the browser
// leads is still running. "Not mine to signal" counts as running, for the
// macOS reason stopBrowserProcess gives: the rig then waits out its budget
// as it always did, and only a group the kernel says is empty fails early.
func browserGroupAlive(pgid int) bool {
	err := syscall.Kill(-pgid, 0)
	return err == nil || err == syscall.EPERM
}

// browserEnv is the environment a test browser starts with: the parent's,
// without the session's display, and with the engine's own home in place of
// the parent's when the engine keeps one.
func browserEnv(engine browserEngine, home string, parent []string) []string {
	env := headlessBrowserEnv(parent)
	if engine.home == nil {
		return env
	}
	own := engine.home(home)
	replaced := map[string]bool{}
	for _, entry := range own {
		key, _, _ := strings.Cut(entry, "=")
		replaced[key] = true
	}
	out := make([]string, 0, len(env)+len(own))
	for _, entry := range env {
		if key, _, _ := strings.Cut(entry, "="); !replaced[key] {
			out = append(out, entry)
		}
	}
	return append(out, own...)
}

// A headless child must not borrow the session's Wayland display. On the Linux test host,
// Chrome 154 reported visible/focused but produced zero animation frames or
// paints with WAYLAND_DISPLAY inherited. Removing only that variable restored
// both, without changing browser flags, page code or the live desktop.
func headlessBrowserEnv(parent []string) []string {
	out := make([]string, 0, len(parent))
	for _, entry := range parent {
		if !strings.HasPrefix(entry, "WAYLAND_DISPLAY=") {
			out = append(out, entry)
		}
	}
	return out
}

func TestHeadlessBrowserEnvironmentDoesNotBorrowWayland(t *testing.T) {
	parent := []string{"PATH=/fixture", "WAYLAND_DISPLAY=wayland-0", "AII_RACE_SCOPE_CHAIN=owned", "FONTCONFIG_FILE=/fixture/fonts", "WAYLAND_DISPLAY_EXTRA=keep"}
	got := headlessBrowserEnv(parent)
	if strings.Join(got, "|") != "PATH=/fixture|AII_RACE_SCOPE_CHAIN=owned|FONTCONFIG_FILE=/fixture/fonts|WAYLAND_DISPLAY_EXTRA=keep" {
		t.Fatalf("headless environment leaked the display or lost unrelated settings: %v", got)
	}
	if parent[1] != "WAYLAND_DISPLAY=wayland-0" {
		t.Fatal("changed the parent's environment")
	}
}

// The Firefox under test keeps a home of its own: every per-user directory it
// reads is beneath the run's directory and none is the account's. The real
// gecko suite proves this only where the account's Firefox folder is refused
// (macOS 27); where it is open, Firefox would use it silently, and this is
// what says it does not. Chrome is given its directory by a flag and keeps
// the parent's environment.
func TestTheFirefoxUnderTestKeepsAHomeOfItsOwn(t *testing.T) {
	engine := func(name string) browserEngine {
		for _, e := range browserEngines {
			if e.name == name {
				return e
			}
		}
		t.Fatalf("no engine %s", name)
		return browserEngine{}
	}
	parent := []string{"PATH=/fixture", "HOME=/account", "XDG_CONFIG_HOME=/account/.config", "CFFIXED_USER_HOME=/account", "TMPDIR=/account/tmp", "LANG=C.UTF-8"}
	const home = "/run/home"

	got := browserEnv(engine("gecko-firefox"), home, parent)
	set := map[string]string{}
	for _, entry := range got {
		key, value, _ := strings.Cut(entry, "=")
		if _, twice := set[key]; twice {
			t.Fatalf("%s is set twice: %v", key, got)
		}
		set[key] = value
	}
	for _, key := range []string{"HOME", "CFFIXED_USER_HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "TMPDIR"} {
		if v := set[key]; v != home && !strings.HasPrefix(v, home+"/") {
			t.Errorf("Firefox's %s is %q, not beneath its own home %s", key, v, home)
		}
	}
	if set["PATH"] != "/fixture" || set["LANG"] != "C.UTF-8" {
		t.Errorf("the home took unrelated settings with it: %v", got)
	}
	if chrome := browserEnv(engine("blink-chrome"), home, parent); strings.Join(chrome, "|") != strings.Join(parent, "|") {
		t.Errorf("Chrome's environment changed: %v", chrome)
	}
}

func TestABrowserRunLeavesNothingBehind(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	page := `<script type="module">import { run } from './__harness.js'; run(() => {});</script>`
	ran := false
	for _, found := range resolveEngines() {
		if found.engine.home == nil {
			continue
		}
		ran = true
		t.Run(found.engine.name, func(t *testing.T) {
			runPageInOneEngine(t, found.engine, found.path, page, nil, nil, 0, 0, 0)
			var left []string
			err := filepath.WalkDir(tmp, func(path string, _ os.DirEntry, err error) error {
				if path != tmp {
					left = append(left, strings.TrimPrefix(path, tmp+"/"))
				}
				return err
			})
			if err != nil || len(left) != 0 {
				t.Fatalf("the run left %v behind in the temporary directory (%v)", left, err)
			}
		})
	}
	if !ran {
		t.Skip("no engine that keeps a home of its own is installed")
	}
}

// failureRecorder stands in for a test so that a test can watch the rig
// fail: it keeps the Fatalf and ends the goroutine, as Fatalf does.
type failureRecorder struct {
	testing.TB
	failed string
}

func (r *failureRecorder) Fatalf(format string, args ...any) {
	r.failed = fmt.Sprintf(format, args...)
	runtime.Goexit()
}

func TestABrowserGoneBeforeThePageReportsFailsInItsOwnWords(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh to stand in for a browser")
	}
	gone := browserEngine{
		name: "gone-at-start",
		args: func(profileDir, url string) []string {
			return []string{"-c", `echo "Could not find profile folder." >&2; exit 1`}
		},
	}
	for _, c := range []struct {
		name     string
		inertFor time.Duration
	}{
		{"a page that must report", 0},
		{"a page that must stay inert", time.Hour},
	} {
		t.Run(c.name, func(t *testing.T) {
			rec := &failureRecorder{TB: t}
			done := make(chan struct{})
			go func() {
				defer close(done)
				runServerInOneEngine(rec, gone, sh, "http://127.0.0.1:1/", make(chan string), 0, 0, c.inertFor)
			}()
			<-done
			if !strings.Contains(rec.failed, "exit status 1") || !strings.Contains(rec.failed, "Could not find profile folder.") {
				t.Fatalf("a browser that exited at its start was reported as %q", rec.failed)
			}
		})
	}
}

// stopBrowserProcess kills the isolated browser process group and returns only
// once the kernel says that group is gone. A fixed post-Wait sleep guessed at
// this property and charged every engine invocation whether cleanup was already
// complete or not. exited is closed by the one goroutine that waits for the
// browser's first process.
func stopBrowserProcess(cmd *exec.Cmd, exited <-chan struct{}) error {
	if cmd.Process == nil {
		return nil
	}
	pgid := cmd.Process.Pid
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
	_ = cmd.Process.Kill()
	<-exited

	// The reaping of a killed group is the kernel's work, and on a loaded
	// box it is not instant: Chrome's dozen helpers were still on their
	// way out two seconds after SIGKILL while kcompactd held a core, and
	// four passing pages were failed by their own cleanup. The claim here
	// is that the group GOES, not that it goes in two seconds; a group
	// that truly survives SIGKILL still fails.
	deadline := time.Now().Add(30 * time.Second)
	for {
		err := syscall.Kill(-pgid, 0)
		if err == syscall.ESRCH {
			return nil
		}
		if err == syscall.EPERM {

			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect process group %d: %w", pgid, err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("process group %d remained after SIGKILL", pgid)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// runPageInEnginesAtSize runs the page in every installed engine at an
// exact window size, for width-scoped CSS (@media blocks) whose rules do
// not exist at the default width. Engines that cannot be sized at launch
// are skipped with a named log line — a phone test silently run at desktop
// width would pass against rules that never applied.
func runPageInEnginesAtSize(t *testing.T, page string, modules map[string][]byte, w, h int) {
	t.Helper()

	engines := resolveEngines()
	if len(engines) == 0 {
		skipWithoutEngines(t)
	}

	for _, found := range engines {
		found := found
		t.Run(found.engine.name, func(t *testing.T) {
			runPageInOneEngine(t, found.engine, found.path, page, modules, nil, w, h, 0)
		})
	}
}

func stubModule(names ...string) []byte {
	var b strings.Builder
	for _, n := range names {
		fmt.Fprintf(&b, "export const %s = () => {};\n", n)
	}
	return []byte(b.String())
}

// utilStub is a hand stub of util.js — a page's own $, esc, hueOf or
// copyText — followed by the shared helpers util.js keeps below its
// "Shared helpers" line (bytes, composing, b64, loadingCard). They are
// pure, no page has reason to replace them, and each view that formats a
// size or guards an IME imports them; a stub without them would fail the
// page at link for a helper it never meant to override.
func utilStub(stub string) []byte {
	src, err := staticFS.ReadFile("static/util.js")
	if err != nil {
		panic(err)
	}
	const marker = "export function bytes("
	i := strings.Index(string(src), marker)
	if i < 0 {
		panic("util.js has no shared-helpers line for utilStub")
	}
	return []byte(stub + "\n" + string(src[i:]))
}

// wsRecordingStub is the recording socket: what the surface SENDS is
// the assertion. A stub that silently swallowed calls would let the
// regressions back in — the whole claim of the correlated-wait tests is
// that looking sends nothing. send() returns a unique id so request-id
// correlation paths behave as they do against the real wire.
const wsRecordingStub = `export const __sent = [];
export const __queried = [];
let __n = 0;
export function send(m) { __sent.push(m); return 'req-' + (++__n); }
export function query(t, a) { __queried.push([t, a]); return ''; }
export function connect() {}
export function wsReady() { return true; }
export function wake() {}
export function __reset() { __sent.length = 0; __queried.length = 0; __n = 0; }
`

// browserModuleStubs is the shared inert base for pages that boot the
// REAL app.js. app.js itself, state.js, util.js, views/projects.js and
// pending.js are deliberately absent — the rig's unmapped-path
// fall-through serves the real shipped files for those, so the module
// under test is always the artifact. The base stubs everything else
// app.js imports, with each module's full export list.
//
// WHY A SHARED BASE. Four browser tests hand-copied this same map, and
// the copies forked: one had a ws.js stub with no __sent export the
// moment another test needed it; app.js gaining an import meant the
// same edit in four places, and one missed copy was the silent module
// death that cost a diagnosis turn. The base makes the fix one edit in
// one place, by construction — the fork pattern removed, not policed.
//
// The ws.js entry here is inert (throwing on accidental use) so a test
// that forgets to override it with the recording stub fails loudly at
// the exact call site rather than passing against a socket that lies.
func browserModuleStubs() map[string][]byte {
	return map[string][]byte{
		"/ws.js": []byte(`export function send() { throw new Error('ws.js: override me with wsRecordingStub'); }
export function query() { throw new Error('ws.js: override me'); }
export function connect() {}
export function wake() {}
`),
		"/bridge.js":         stubModule("send", "query"),
		"/firstboot.js":      stubModule("renderProviderOptions", "setModelOptions", "fbHint", "fbResult", "acceptDiscoveryResponse", "firstbootConnectionLost", "discoveryAnswered", "discoveryFailed"),
		"/panel.js":          stubModule("renderPanel"),
		"/presence.js":       stubModule("renderPresence", "setThinking"),
		"/sections.js":       stubModule("initSections", "sectionTitle", "publish", "onSections", "onLayout", "onTokensChanged"),
		"/theme.js":          stubModule("onTheme"),
		"/overlay.js":        stubModule("restoreDraft", "onOverlayChanged", "holdReloadWhile"),
		"/voice.js":          stubModule("wireMic", "bindTransport", "speak", "render", "connectionLost"),
		"/views/chat.js":     stubModule("scrollThread", "addMsg", "attachSpeaker", "sysLine", "toolEventLive", "thinkingEvent", "renderInteractionPages", "renderHistory", "renderChatSubstrate", "renderComposer", "acceptSubstrateConfig", "rejectSubstrateConfig", "substrateConnectionLost", "renderSteering", "renderAsks"),
		"/views/home.js":     stubModule("renderHome"),
		"/views/work.js":     stubModule("renderWorkPill"),
		"/views/memory.js":   stubModule("renderMemory"),
		"/views/identity.js": stubModule("renderIdentity"),
		"/views/plugins.js":  stubModule("renderPlugins", "rejectPluginAction", "applyPluginStored"),
		"/views/record.js":   stubModule("renderRecord"),
		"/views/settings.js": stubModule("renderSettings", "acceptSettingsConfig", "rejectSettingsConfig", "acceptProviderSave", "rejectProviderSave", "acceptSpeechLists", "rejectSpeechLists", "acceptDashboardToken", "acceptAccessCode", "rejectAccessCode", "settingsConnectionLost"),
	}
}

// TestBrowserEngineCoverage records which engines this host can actually
// execute. It is deliberately not a pass/fail gate on engine count: it
// prints the coverage so a green suite cannot be mistaken for proof that
// every target engine was exercised.
func TestBrowserEngineCoverage(t *testing.T) {
	engines := resolveEngines()
	if len(engines) == 0 {
		skipWithoutEngines(t)
	}
	for _, found := range engines {
		t.Logf("engine available: %-14s %s", found.engine.name, found.path)
	}
	seenWebKit := false
	for _, found := range engines {
		if strings.HasPrefix(found.engine.name, "webkit") {
			seenWebKit = true
		}
	}
	if !seenWebKit {
		t.Logf("engine MISSING: webkit (Safari) — not installable on this host; " +
			"WebKit-specific defects are covered by static review only")
	}
}
