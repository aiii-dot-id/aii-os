//go:build !windows

package dashboard

import (
	"bufio"
	"crypto/rand"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/browsertest"
)

/*
Cross-engine browser harness.

WHY THIS EXISTS. The first browser test drove Chrome with --dump-dom and
scraped the printed DOM for a marker. That works, and it can only ever
test one engine: --dump-dom is a Chrome flag. Firefox has no equivalent,
so "also run it on Firefox" is not a matter of adding a binary name — the
RESULT CHANNEL itself was Chrome-specific.

So the result channel moves into the page. The page runs its assertions
and POSTs "OK" or "FAIL: ..." back to the test server; the harness waits
on that. The only browser capability required is fetch(), which every
engine in scope has had for years. That makes the harness engine-agnostic
by construction rather than by a per-browser special case, and it is why
adding Safari later is a table entry and not a rewrite.

WHAT IS AND IS NOT COVERED HERE. Engines, not brands:

  Blink   Chrome, Edge, Opera, Brave, and every Electron shell
  Gecko   Firefox, Firefox ESR
  WebKit  Safari (macOS, and EVERY iOS browser including iOS Chrome)

Edge IS Blink. Running Chrome covers Edge's engine; what it does not cover
is Edge's chrome-level behaviour, which is not what these tests assert.
The harness still probes for an Edge binary and will use it when present.

WEBKIT RUNS WHERE PLAYWRIGHT'S WEBKIT BUILD IS INSTALLED. Safari cannot be
started headless with a URL, and does not exist for Linux, so WebKit is
driven through Playwright's build of it: the engine webkit-playwright,
found by browsertest when AII_PLAYWRIGHT_DIR names a folder holding
node_modules/playwright-core and its WebKit build (about 300 MB, for the
iPhone shell's engine). A host without it skips the engine, named, as it
does any missing browser, and TestBrowserEngineCoverage says WebKit was
not run.

To install it, with node 26 or later, in a folder of its own on a disk with
the room (on the Mac, the external drive):

    npm install playwright-core@1.64.1
    PLAYWRIGHT_BROWSERS_PATH=./browsers npx playwright-core install webkit

then run the tests with AII_PLAYWRIGHT_DIR set to that folder, and
AII_BROWSER_ENGINES=webkit-playwright for WebKit alone.
*/

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
	// channel is the engine's own way to the page, which holds the page at
	// the size asked and reads it back (browserengine_size_test.go). Given
	// the command before it starts and where its output goes, it returns
	// what connects once the browser runs (told if it exits meanwhile), and
	// what closes all it opened.
	channel func(cmd *exec.Cmd, output string) (connect func(exited <-chan struct{}) (sizeHolder, error), release func(), err error)
	// opens says the channel opens the run's page: the browser starts at
	// about:blank.
	opens bool
	// netLog is how the engine keeps a log of its requests, given the path for it
	// (browserengine_netlog_test.go). An engine that keeps none leaves it nil.
	netLog func(path string) []string
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
		channel: devtoolsPipe,
		opens:   true,
		netLog:  chromiumNetLog,
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
		channel: devtoolsPipe,
		opens:   true,
		netLog:  chromiumNetLog,
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
		channel: webDriverBiDi,
		opens:   true,
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
	{
		// WebKit: Safari's engine, and the iPhone shell's (shells/ios,
		// WKWebView). The executable browsertest.Find returns is its
		// webkit-playwright.mjs, which opens the page in Playwright's
		// WebKit build; the rig starts it, sizes it and kills its group as
		// it does any browser.
		name: "webkit-playwright",
		args: func(profileDir, url string) []string {
			return []string{"--profile", profileDir, url}
		},
		sizeArgs: func(w, h int) []string {
			return []string{"--size", fmt.Sprintf("%dx%d", w, h)}
		},
		channel: playwrightSays,
		// Playwright writes into TMPDIR as it runs, and a run the rig kills
		// leaves what it wrote; so its home and temporary files are the
		// run's, as Firefox's are (see gecko-firefox's note).
		home: func(dir string) []string {
			return []string{
				"HOME=" + dir,
				"CFFIXED_USER_HOME=" + dir,
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
//
// The body runs once the page's stylesheets have loaded. A module can run
// before a stylesheet the page links has arrived (measured on Linux's WebKit,
// 2026-11-01: "sheets 0, link loaded false"), and a page whose styles have
// not applied answers every layout question wrongly. With none pending the
// body runs at once, as it always did; a sheet that never loads is waited
// for only until the deadline, and then the body runs and says what it sees.
export function run(body) {
  const go = () => {
    let r;
    try { r = body(assert); } catch (e) { report(failure(e)); return; }
    if (r && typeof r.then === 'function') { r.then(() => report('OK'), (e) => report(failure(e))); }
    else report('OK');
  };
  const loading = () => [...document.querySelectorAll('link[rel="stylesheet"]')].some(l => !l.sheet);
  if (!loading()) return go();
  until(() => !loading()).then(go);
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
//
// A PAGE LOST TO A NETWORK CHANGE RUNS ONCE MORE. When an address changes on the
// host, Chrome cancels what is queued for a socket, on localhost too, and a page
// missing one import never runs (proved 2026-11-04). That says nothing about the
// page, so it runs again in a fresh browser, with a server and a report of its own so
// nothing of the first can pass for the second, and both attempts are in the test's
// log. Any other silence is the failure it always was.
func runPageInOneEngine(t testing.TB, engine browserEngine, path string, page string, modules map[string][]byte, headers map[string]string, w, h int, inertFor time.Duration) {
	t.Helper()
	first := runPageOnce(t, engine, path, page, modules, headers, w, h, inertFor)
	if first == nil {
		return
	}
	if len(first.required) == 0 {
		t.Fatalf("%s (%s): %s", engine.name, path, first)
	}
	t.Logf("%s (%s): the first attempt was lost to a network change: %s\nIt runs once more in a fresh browser.", engine.name, path, first)
	if second := runPageOnce(t, engine, path, page, modules, headers, w, h, inertFor); second != nil {
		t.Fatalf("%s (%s): the second attempt, in a fresh browser, was lost too: %s", engine.name, path, second)
	}
	t.Logf("%s (%s): the second attempt, in a fresh browser, passed.", engine.name, path)
}

// runPageOnce is one attempt at a page, with its own server. It returns what was
// lost when the page never reported and Chrome refused some of its requests for a
// network change, and nil when the page reached its verdict; every other outcome is
// the test's failure, said here.
func runPageOnce(t testing.TB, engine browserEngine, path string, page string, modules map[string][]byte, headers map[string]string, w, h int, inertFor time.Duration) *lostPage {
	t.Helper()
	results := make(chan string, 4)
	// THE REPORT IS THE ATTEMPT'S. Only the browser this attempt launches is sent to
	// begin, the one address that gives the attempt's token, in a cookie no script sees.
	// A report without that token is refused and queues nothing, so neither a request
	// from no page nor a page of another attempt can speak for this one, whichever port
	// this attempt was given.
	token := rand.Text()
	begin := "/__attempt/" + token

	var served servedLog
	defer func() {
		if t.Failed() {
			t.Logf("%s (%s): the page's server answered:\n%s", engine.name, path, served.String())
		}
	}()
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		w := &statusWriter{ResponseWriter: rw, status: http.StatusOK}
		defer served.add(r.Method, r.URL.Path, w)
		if r.URL.Path == begin {
			http.SetCookie(w, &http.Cookie{Name: attemptCookie, Value: token, Path: "/", HttpOnly: true})
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		if r.URL.Path == "/__result" {
			if c, err := r.Cookie(attemptCookie); err != nil || c.Value != token {
				w.WriteHeader(http.StatusForbidden)
				return
			}
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
		// A path served with its query ("/record?before=2") answers that
		// query alone, ahead of the path's own answer.
		module, ok := modules[r.URL.Path+"?"+r.URL.RawQuery]
		if !ok || r.URL.RawQuery == "" {
			module, ok = modules[r.URL.Path]
		}
		if ok {
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
		// THE ROLES theme.css IMPORTS, AND THE COMPONENTS' AND REBUILT VIEWS'
		// OWN STYLESHEETS, ARE SERVED FOR REAL TOO, so a page that inlines or
		// maps theme.css gets the colours its aliases name, and the shell's
		// components and rebuilt views (first run's, which hides itself once
		// they are born) are drawn as they ship; so is Search's, which every
		// page asks for once it has drawn its first view. Only those: a page
		// that links another sheet and does not map it has always been given
		// the page in its place, and some tests were written against what
		// that leaves unstyled.
		if r.URL.Path == "/tokens/tokens.css" || ((strings.HasPrefix(r.URL.Path, "/components/") || strings.HasPrefix(r.URL.Path, "/views/") || strings.HasPrefix(r.URL.Path, "/shell/")) && strings.HasSuffix(r.URL.Path, ".css")) {
			if real, err := staticFS.ReadFile("static" + r.URL.Path); err == nil {
				w.Header().Set("Content-Type", "text/css")
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
	lost := runServerOnce(t, engine, path, server.URL+begin, results, w, h, inertFor)
	if lost != nil {
		lost.served = served.String()
	}
	return lost
}

// attemptCookie tells one attempt's report from another's.
const attemptCookie = "aii_rig_attempt"

// browserBudget is how long a run waits for its page to report (see
// runServerInOneEngine); a test of the rig's own timing lowers it.
var browserBudget = 180 * time.Second

// runServerInOneEngine opens startURL in one engine and waits for the page's
// verdict on results, which the caller's server fills from POST /__result.
// It is the half of runPageInOneEngine that does not care what serves the
// page, so a test can put the dashboard's own server behind the rig.
//
// A server the caller keeps can hold what the first attempt changed, and a first
// report could still reach it, so a page lost to a network change is not run again
// here: it fails, naming what was lost.
func runServerInOneEngine(t testing.TB, engine browserEngine, path, startURL string, results <-chan string, w, h int, inertFor time.Duration) {
	t.Helper()
	if lost := runServerOnce(t, engine, path, startURL, results, w, h, inertFor); lost != nil {
		t.Fatalf("%s (%s): %s", engine.name, path, lost)
	}
}

// runServerOnce is one attempt, in a browser of its own: see runPageOnce.
func runServerOnce(t testing.TB, engine browserEngine, path, startURL string, results <-chan string, w, h int, inertFor time.Duration) *lostPage {
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

	// THE SIZE IS HELD WHERE A TEST ASKS ONE. A run that names none keeps the
	// browser's own window and claims nothing about it: its channel opened on
	// every launch cost the gate's longest shard a quarter of its time.
	sized := (w > 0 || h > 0) && engine.channel != nil
	launchURL := startURL
	if sized && engine.opens {
		launchURL = "about:blank"
	}
	var netLogPath string
	var netLogArgs []string
	if engine.netLog != nil {
		netLogPath = filepath.Join(root, "net.json")
		netLogArgs = engine.netLog(netLogPath)
	}
	launchArgs := append(netLogArgs, engine.args(profileDir, launchURL)...)
	if w > 0 || h > 0 {
		if engine.sizeArgs == nil {
			t.Skipf("engine %s cannot be sized at launch; the %dx%d page cannot run here", engine.name, w, h)
		}
		launchArgs = append(engine.sizeArgs(w, h), launchArgs...)
	} else if engine.sizeArgs != nil {

		launchArgs = append(engine.sizeArgs(800, 600), launchArgs...)
	}

	cmd := exec.Command(path, launchArgs...)
	cmd.Env = append(browserEnv(engine, home, os.Environ()), "MOZ_HEADLESS=1")
	// The test browser never sees the operator's session bus. Chrome asks
	// org.freedesktop.Notifications for its capabilities at startup; on a
	// desktop whose shell is gone, D-Bus activates KDE's plasma_waitforname,
	// which waits for a shell that never comes, and Chrome stalls for the
	// 25 s activation timeout — every page test then overruns its wait and
	// the package overruns its 10-minute budget (the Linux test host, 2026-09-10, after the
	// operator's session was killed). A hermetic browser has no business
	// with notifications, keyrings or portals; a runner without a bus never
	// had this problem, and now neither does a workstation with a dead one.
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
	var connect func(<-chan struct{}) (sizeHolder, error)
	if sized {
		var release func()
		if connect, release, err = engine.channel(cmd, outputPath); err != nil {
			t.Fatalf("%s: its channel: %v", engine.name, err)
		}
		// What the channel opened goes when the run ends, a run whose
		// browser never started among them.
		defer release()
	}
	browser, err := startBrowserGroup(cmd)
	_ = output.Close() // the browser holds its own copy
	if err != nil {
		t.Fatalf("%s failed to start: %v", engine.name, err)
	}
	exited := browser.exited
	defer func() {
		if err := browser.stop(); err != nil {
			t.Errorf("%s cleanup: %v", engine.name, err)
		}
	}()
	// THE PAGE IS THE SIZE ASKED, through the engine's own channel, and it
	// is read back when the run ends (browserengine_size_test.go).
	var holder sizeHolder
	if connect != nil {
		if holder, err = connect(exited); err != nil {
			t.Fatalf("%s (%s): its channel: %v\nthe browser said:\n%s", engine.name, path, err, browserSaid(outputPath))
		}
		defer holder.close()
		if engine.opens {
			if err := holder.open(startURL, w, h); err != nil {
				t.Fatalf("%s (%s): the page could not be opened at %dx%d: %v\nthe browser said:\n%s", engine.name, path, w, h, err, browserSaid(outputPath))
			}
		}
	}
	// held fails a run whose page is not the size asked, with what the page
	// reported, if it did.
	held := func(reported string) {
		if holder == nil {
			return
		}
		if err := heldSize(holder, engine.name, w, h); err != nil {
			t.Fatalf("%s (%s)\nthe page reported: %s", err, path, reported)
		}
	}

	// verdict reads a report. INERTNESS IS THE CLAIM when inertFor is set,
	// so silence is the pass and any report is the failure. A page that
	// cannot run script cannot report a verdict, which means the usual "no
	// report is a failure" rule would fail exactly the case being proven.
	// The witness is therefore OUTSIDE the browser — the caller watches
	// whether anything the page tried to do actually arrived — and the wait
	// below only gives the page real time to try.
	verdict := func(result string) {
		held(result)
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
			t.Fatalf("%s (%s): %s\nthe browser said:\n%s", engine.name, path, result, browserSaid(outputPath))
		}
	}
	// THE BUDGET IS FOR A LOADED SHARD, NOT A PRODUCT BOUND. The claim is
	// that the page REPORTS; the gate's dashboard shard starts several
	// headless engines at once on a box other agents load too, and a cold
	// Chrome missed sixty seconds there while the same page reported in
	// three alone. Three minutes still fails a page that never reports.
	//
	// AN INERT RUN IS BOUNDED BY ITS OWN WINDOW: its silence for inertFor is
	// the claim, so the budget never ends it first. It did, at 180 s, while
	// the caller reported the whole window as the silence it had seen
	// (review of 0e24a4f1, 2026-10-14). Both start here, once the page is
	// open, so a channel's setup spends neither.
	budget := time.NewTimer(max(browserBudget, inertFor+time.Second))
	defer budget.Stop()
	var settled <-chan time.Time
	if inertFor > 0 {
		settled = time.After(inertFor)
	}
	gone := (<-chan struct{})(exited)
	for {
		select {
		case result := <-results:
			verdict(result)
			return nil
		case <-settled:
			held("nothing, as an inert page should")
			return nil
		case <-gone:
			// The first process is not the whole browser: one that hands
			// the page to another of its processes may still report, and
			// only a group with none of its processes left is a browser
			// that cannot. A report that came in as the browser went is
			// still the verdict.
			if browser.running() {
				gone = nil
				continue
			}
			select {
			case result := <-results:
				verdict(result)
				return nil
			default:
			}
			t.Fatalf("%s (%s) exited (%s) before the page reported; it said:\n%s", engine.name, path, cmd.ProcessState, browserSaid(outputPath))
		case <-budget.C:
			if inertFor > 0 {
				t.Fatalf("%s (%s): the run's budget ended before its inert window did", engine.name, path)
			}
			// SAY WHETHER THE BROWSER WAS STILL THERE. A page that never
			// reports and a browser that has died are different defects,
			// and this verdict said only the first: blink-chrome spent the
			// whole budget while gecko-firefox passed the same test in
			// 1.07 s and the page's server had answered every asset 200
			// (host gate of 2026-10-30, step 8 r1). The browser's own
			// output showed its renderer losing the zygote, and nothing in
			// the message could say so.
			procState := "still running"
			select {
			case <-exited:
				procState = "had exited"
			default:
			}
			groupState := "empty"
			if browser.running() {
				groupState = "not empty"
			}
			if required, other := lostToNetworkChange(netLogPath, startURL); len(required)+len(other) > 0 {
				return &lostPage{required: required, other: other, process: procState, group: groupState, files: raiseBrowserFiles(), said: browserSaid(outputPath)}
			}
			t.Fatalf("%s (%s): page never reported a result within the timeout; the browser process %s and its process group was %s; %sthe browser said:\n%s", engine.name, path, procState, groupState, raiseBrowserFiles(), browserSaid(outputPath))
		}
	}
}

// servedLog is what a rig's page server was asked for and answered, in
// order, bounded so a page that loops cannot grow it without end.
type servedLog struct {
	mu    sync.Mutex
	lines []string
	more  int
}

const servedLogMax = 200

func (l *servedLog) add(method, path string, w *statusWriter) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.lines) >= servedLogMax {
		l.more++
		return
	}
	l.lines = append(l.lines, fmt.Sprintf("%s %s %d (%d bytes)", method, path, w.status, w.n))
}

func (l *servedLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.lines) == 0 {
		return "(nothing: no request reached it)"
	}
	out := strings.Join(l.lines, "\n")
	if l.more > 0 {
		out += fmt.Sprintf("\n(and %d more)", l.more)
	}
	return out
}

// statusWriter keeps the status and the size of what a handler wrote.
type statusWriter struct {
	http.ResponseWriter
	status, n int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	w.n += n
	return n, err
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

// browserGroup is a browser in a process group the rig holds: its anchor leads
// the group, and the group's id is the anchor's until stop reaps it, so every
// signal goes to a group that is still this rig's.
type browserGroup struct {
	cmd    *exec.Cmd
	exited chan struct{} // closed by the one goroutine that waits for cmd
	anchor *exec.Cmd
	hold   io.WriteCloser // the anchor's input; only this binary holds it
}

// browserFiles is the fewest open files a browser may hold. A shell's limit can be
// 1024, and Go hands a child the limit it found. A page reloaded a dozen times in a
// second then runs Chrome out of descriptors: its socket and imports fail with no net
// error, and its main thread stops, which reads as a page that never reports.
const browserFiles = 65536

// raiseBrowserFiles sets the limit once, at no less than it stands: setting it is
// what makes a child inherit it. It says why the floor could not be met, for a
// failure to name, and says nothing when it was.
var raiseBrowserFiles = sync.OnceValue(func() string {
	var limit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &limit); err != nil {
		return fmt.Sprintf("the limit on a browser's open files could not be read: %v; ", err)
	}
	limit.Cur = max(limit.Cur, min(browserFiles, limit.Max))
	if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &limit); err != nil {
		return fmt.Sprintf("the limit on a browser's open files could not be set: %v; ", err)
	}
	if limit.Cur < browserFiles {
		return fmt.Sprintf("a browser may hold only %d open files; ", limit.Cur)
	}
	return ""
})

// startBrowserGroup starts an anchor and then cmd in the anchor's group. The
// anchor is a shell that kills its group when its input ends. The rig's cleanup
// runs in a defer, and a binary that dies without its defers (a -timeout panic,
// a stopped run) left its browsers running: eight on one Mac, the oldest 40 hours
// old (2026-11-01). The kernel closes the input however the binary ends. The
// anchor is armed before the browser starts, so no browser runs unwatched.
func startBrowserGroup(cmd *exec.Cmd) (*browserGroup, error) {
	raiseBrowserFiles()
	b := &browserGroup{cmd: cmd, exited: make(chan struct{})}
	b.anchor = exec.Command("/bin/sh", "-c", `read -r line; kill -KILL 0`)
	b.anchor.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	hold, err := b.anchor.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("its anchor: %w", err)
	}
	b.hold = hold
	if err := b.anchor.Start(); err != nil {
		_ = hold.Close()
		return nil, fmt.Errorf("its anchor: %w", err)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: b.anchor.Process.Pid}
	if err := cmd.Start(); err != nil {
		_ = b.anchor.Process.Kill()
		_ = b.anchor.Wait() // closes hold
		return nil, err
	}
	go func() {
		_ = cmd.Wait()
		close(b.exited)
	}()
	return b, nil
}

// running reports whether any process of the browser's group is still running,
// the anchor apart. If the kernel will not say, it counts as running: the rig
// then waits out its budget as it always did, and only a group known to have
// none of the browser's processes left fails early.
func (b *browserGroup) running() bool {
	others, err := b.members()
	return err != nil || len(others) > 0
}

// members lists the group's running processes, the anchor apart.
func (b *browserGroup) members() ([]int, error) {
	all, err := processGroupMembers(b.anchor.Process.Pid)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(all, func(pid int) bool { return pid == b.anchor.Process.Pid }), nil
}

// stop kills the browser's group and returns once the kernel says none of its
// processes runs. A fixed post-Wait sleep guessed at this property and charged
// every engine invocation whether cleanup was already complete or not. The anchor
// is reaped last, and only then may the group's id become another's; nothing is
// signalled after that.
func (b *browserGroup) stop() error {
	pgid := b.anchor.Process.Pid
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
	<-b.exited
	defer func() {
		_ = b.hold.Close() // its signal, should the rig's have missed it
		_ = b.anchor.Wait()
	}()

	// The reaping of a killed group is the kernel's work, and on a loaded
	// box it is not instant: Chrome's dozen helpers were still on their
	// way out two seconds after SIGKILL while kcompactd held a core, and
	// four passing pages were failed by their own cleanup. The claim here
	// is that the group GOES, not that it goes in two seconds; a group
	// that truly survives SIGKILL still fails.
	//
	// AND ONE SIGNAL CAN MISS. A process being forked as the group is signalled
	// can come through it: on the Mac a stand-in's "sleep 60" ran on with its parent
	// gone, and a second signal ended it (measured). So each look signals again;
	// the anchor is not reaped yet, so the group's id is still this group's.
	deadline := time.Now().Add(30 * time.Second)
	for {
		others, err := b.members()
		if err != nil {
			return fmt.Errorf("inspect process group %d: %w", pgid, err)
		}
		if len(others) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("process group %d remained after SIGKILL: %v", pgid, others)
		}
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		time.Sleep(20 * time.Millisecond)
	}
}

// A browser ends with the binary that started it, however that binary ends, and
// the group's id is the rig's until the rig lets it go. This binary runs itself
// again as a helper, which starts a stand-in browser the rig's way and says its
// group.
//   - killed outright: the helper is killed, as a -timeout panic or a stopped run
//     ends a binary, and the stand-in's group goes with it;
//   - dying at once: the helper ends as soon as it has started the stand-in, so
//     there is no moment a browser runs before something is watching it;
//   - ending by itself: the stand-in exits while the helper lives; the rig sees it
//     gone, the anchor still holds the id, and the helper's end takes the anchor;
//   - a browser's helpers: the first process ends and another of the group runs,
//     which is a browser still running, and stop's signal reaches it;
//   - ended, not reaped: a process waiting to be reaped runs nothing.
func TestABrowserEndsWithTheBinaryThatStartedIt(t *testing.T) {
	if mode := os.Getenv("AII_BROWSER_GROUP_HELPER"); mode != "" {
		stand := map[string]*exec.Cmd{
			"killed":  exec.Command("/bin/sleep", "60"),
			"at once": exec.Command("/bin/sleep", "60"),
			"ends":    exec.Command("/bin/sleep", "0.3"),
		}[mode]
		b, err := startBrowserGroup(stand)
		if err != nil {
			fmt.Println("start:", err)
			return
		}
		fmt.Println(b.anchor.Process.Pid, stand.Process.Pid)
		if mode == "at once" {
			os.Exit(0)
		}
		for deadline := time.Now().Add(60 * time.Second); mode == "ends" && b.running() && time.Now().Before(deadline); {
			time.Sleep(20 * time.Millisecond)
		}
		if mode == "ends" && !b.running() {
			fmt.Println("gone")
		}
		time.Sleep(60 * time.Second)
		runtime.KeepAlive(b)
		return
	}
	alive := func(id int) bool {
		err := syscall.Kill(id, 0)
		return err == nil || err == syscall.EPERM
	}
	goneWithin := func(id int, within time.Duration) bool {
		for deadline := time.Now().Add(within); alive(id); time.Sleep(20 * time.Millisecond) {
			if time.Now().After(deadline) {
				return false
			}
		}
		return true
	}
	helper := func(t *testing.T, mode string) (*exec.Cmd, int, int, <-chan string) {
		h := exec.Command(os.Args[0], "-test.run=^TestABrowserEndsWithTheBinaryThatStartedIt$")
		h.Env = append(os.Environ(), "AII_BROWSER_GROUP_HELPER="+mode)
		out, err := h.StdoutPipe()
		if err == nil {
			err = h.Start()
		}
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = h.Process.Kill(); _ = h.Wait() })
		said := make(chan string, 2)
		go func() {
			lines := bufio.NewReader(out)
			for {
				line, err := lines.ReadString('\n')
				if err != nil {
					return
				}
				said <- strings.TrimSpace(line)
			}
		}()
		var line string
		select {
		case line = <-said:
		case <-time.After(30 * time.Second):
		}
		var pgid, browser int
		if n, _ := fmt.Sscan(line, &pgid, &browser); n != 2 {
			t.Fatalf("the helper did not say its browser's group and process: %q", line)
		}
		return h, pgid, browser, said
	}
	endsWithItsBinary := func(t *testing.T, pgid, browser int) {
		t.Helper()
		if !goneWithin(browser, 10*time.Second) {
			_ = syscall.Kill(browser, syscall.SIGKILL)
			t.Errorf("the browser %d outlived the binary that started it", browser)
		}
		if !goneWithin(-pgid, 10*time.Second) {
			// Still the stand-in's: the anchor holds the id for as long as it runs.
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
			t.Fatalf("the browser's group %d outlived the binary that started it", pgid)
		}
	}
	t.Run("killed outright", func(t *testing.T) {
		h, pgid, browser, _ := helper(t, "killed")
		if !alive(-pgid) || !alive(browser) {
			t.Fatalf("the stand-in browser %d in group %d was not running", browser, pgid)
		}
		_ = h.Process.Kill()
		_ = h.Wait()
		endsWithItsBinary(t, pgid, browser)
	})
	t.Run("dying at once", func(t *testing.T) {
		h, pgid, browser, _ := helper(t, "at once")
		_ = h.Wait()
		endsWithItsBinary(t, pgid, browser)
	})
	t.Run("ending by itself", func(t *testing.T) {
		h, pgid, browser, said := helper(t, "ends")
		select {
		case line := <-said:
			if line != "gone" {
				t.Fatalf("the helper said %q", line)
			}
		case <-time.After(10 * time.Second):
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
			t.Fatalf("the rig did not see the stand-in in group %d go", pgid)
		}
		if !alive(-pgid) {
			t.Fatalf("the group's id %d was let go while the rig still held the group", pgid)
		}
		_ = h.Process.Kill()
		_ = h.Wait()
		endsWithItsBinary(t, pgid, browser)
	})
	t.Run("a browser's helpers", func(t *testing.T) {
		pid := filepath.Join(t.TempDir(), "helper.pid")
		b, err := startBrowserGroup(exec.Command("/bin/sh", "-c", `sleep 60 & echo $! > "$0"; exit 0`, pid))
		if err != nil {
			t.Fatal(err)
		}
		select {
		case <-b.exited:
		case <-time.After(10 * time.Second):
			t.Fatal("the stand-in's first process did not end")
		}
		if !b.running() {
			t.Error("a browser whose first process ended while another runs was taken for gone")
		}
		if err := b.stop(); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(pid)
		if err != nil {
			t.Fatal(err)
		}
		var helperPid int
		if _, err := fmt.Sscan(string(raw), &helperPid); err != nil {
			t.Fatal(err)
		}
		// Reaped by whoever adopted it, in its own time: gone is what stop's signal did.
		if !goneWithin(helperPid, 10*time.Second) {
			t.Fatalf("stop left the browser's helper %d running", helperPid)
		}
	})
	t.Run("ended, not reaped", func(t *testing.T) {
		b, err := startBrowserGroup(exec.Command("/bin/sleep", "0"))
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := b.stop(); err != nil {
				t.Error(err)
			}
		}()
		unreaped := exec.Command("/bin/sleep", "0")
		unreaped.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: b.anchor.Process.Pid}
		if err := unreaped.Start(); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = unreaped.Wait() }()
		for deadline := time.Now().Add(10 * time.Second); b.running(); time.Sleep(20 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatal("a process that ended and waits to be reaped was taken for one running")
			}
		}
	})
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
		t.Logf("engine MISSING: webkit (Safari, the iPhone shell) — set AII_PLAYWRIGHT_DIR to a folder " +
			"with playwright-core and its WebKit build; until then WebKit-specific defects are covered by static review only")
	}
}
