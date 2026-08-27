//go:build !windows

// The engine rig drives a real browser through node/playwright and
// kills it by process group — unix mechanics (Setpgid, kill(-pgid)).
// Windows never runs this rig; without the tag its test file still
// had to COMPILE there, and the unix-only SysProcAttr field broke the
// windows row invisibly, because the gate cross-built packages but
// never type-checked tests (D71, Sev 2026-08-26; the §7 gate now
// vets the windows and darwin rows, which compiles _test.go files).

package dashboard

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
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

WebKit CANNOT RUN HERE. Safari does not exist for Linux, so on this host
WebKit coverage is structurally unavailable — not merely unconfigured. The
honest consequence: WebKit-specific defects are caught by static review
(vendor prefixes, feature guards) and NOT by execution. That is a real gap
and it is recorded as one. playwright's WebKit build is the way to close
it; that is an operator decision because it adds a Node toolchain and a
~300MB download to a Go repo.
*/

// browserEngine is one engine plus how to drive it headlessly.
type browserEngine struct {
	name string
	// bins are candidate binary names, tried in order.
	bins []string
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
}

var browserEngines = []browserEngine{
	{
		name: "blink-chrome",
		bins: []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser"},
		args: func(profileDir, url string) []string {
			return []string{
				"--headless=new", "--no-sandbox", "--disable-gpu",
				"--no-first-run", "--no-default-browser-check",
				"--disable-extensions", "--disable-background-networking",
				"--user-data-dir=" + profileDir, url,
			}
		},
		sizeArgs: func(w, h int) []string {
			return []string{fmt.Sprintf("--window-size=%d,%d", w, h)}
		},
	},
	{
		name: "blink-edge",
		bins: []string{"microsoft-edge", "microsoft-edge-stable"},
		args: func(profileDir, url string) []string {
			return []string{
				"--headless=new", "--no-sandbox", "--disable-gpu",
				"--no-first-run", "--no-default-browser-check",
				"--user-data-dir=" + profileDir, url,
			}
		},
		sizeArgs: func(w, h int) []string {
			return []string{fmt.Sprintf("--window-size=%d,%d", w, h)}
		},
	},
	{
		name: "gecko-firefox",
		bins: []string{"firefox", "firefox-esr"},
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
			}, "\n")
			return os.WriteFile(filepath.Join(profileDir, "user.js"), []byte(prefs), 0o644)
		},
	},
}

// resolveEngines returns the engines actually installed on this host,
// narrowed by AII_BROWSER_ENGINES when the operator set it (comma-
// separated engine names). The variable exists for CI hosts whose
// bundled engines misbehave in ways a real desktop does not — the
// runner image's Edge hangs sized headless windows (2026-08-27) —
// so the pipeline can name the engines it vouches for instead of
// inheriting whatever the image carries. Default: every engine found.
func resolveEngines() []struct {
	engine browserEngine
	path   string
} {
	allow := map[string]bool{}
	if sel := os.Getenv("AII_BROWSER_ENGINES"); sel != "" {
		for _, name := range strings.Split(sel, ",") {
			if n := strings.TrimSpace(name); n != "" {
				allow[n] = true
			}
		}
	}
	var found []struct {
		engine browserEngine
		path   string
	}
	for _, candidate := range browserEngines {
		if len(allow) > 0 && !allow[candidate.name] {
			continue
		}
		for _, bin := range candidate.bins {
			if path, err := exec.LookPath(bin); err == nil {
				found = append(found, struct {
					engine browserEngine
					path   string
				}{candidate, path})
				break
			}
		}
	}
	return found
}

// harnessJS is served to the page under test. report() is the result
// channel; run() guarantees exactly one report even when the body throws.
const harnessJS = `
let reported = false;
export function report(text) {
  if (reported) return;
  reported = true;
  try {
    fetch('/__result', { method: 'POST', body: text, keepalive: true }).catch(() => {
      if (navigator.sendBeacon) navigator.sendBeacon('/__result', text);
    });
  } catch (e) {
    try { if (navigator.sendBeacon) navigator.sendBeacon('/__result', text); } catch (e2) {}
  }
}
export function assert(ok, message) { if (!ok) throw new Error(message); }
export function run(body) {
  try { body(); report('OK'); }
  catch (e) { report('FAIL: ' + ((e && e.message) || String(e))); }
}
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
		t.Skip("no browser engine available")
	}

	for _, found := range engines {
		found := found
		t.Run(found.engine.name, func(t *testing.T) {
			runPageInOneEngine(t, found.engine, found.path, page, modules, headers, 0, 0)
		})
	}
}

// runPageInOneEngine runs one engine against the page. w and h of 0,0 mean
// the engine default window; anything else is prepended as size flags, for
// width-scoped CSS that does not exist at the default width.
func runPageInOneEngine(t *testing.T, engine browserEngine, path string, page string, modules map[string][]byte, headers map[string]string, w, h int) {
	t.Helper()
	results := make(chan string, 4)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__result" {
			body := make([]byte, 4096)
			n, _ := r.Body.Read(body)
			select {
			case results <- string(body[:n]):
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
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, page)
	}))
	defer server.Close()

	profileDir, err := os.MkdirTemp("", "aii-browser-*")
	if err != nil {
		t.Fatalf("profile dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(profileDir) }()

	if engine.prepare != nil {
		if err := engine.prepare(profileDir); err != nil {
			t.Fatalf("profile prepare: %v", err)
		}
	}

	launchArgs := engine.args(profileDir, server.URL)
	if w > 0 || h > 0 {
		if engine.sizeArgs == nil {
			t.Skipf("engine %s cannot be sized at launch; the %dx%d page cannot run here", engine.name, w, h)
		}
		launchArgs = append(engine.sizeArgs(w, h), launchArgs...)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, path, launchArgs...)
	cmd.Env = append(os.Environ(), "MOZ_HEADLESS=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("%s failed to start: %v", engine.name, err)
	}
	defer func() {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
		time.Sleep(150 * time.Millisecond)
	}()

	select {
	case result := <-results:
		if result != "OK" {
			t.Fatalf("%s (%s): %s", engine.name, path, result)
		}
	case <-ctx.Done():
		t.Fatalf("%s (%s): page never reported a result within the timeout", engine.name, path)
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
		t.Skip("no browser engine available")
	}

	for _, found := range engines {
		found := found
		t.Run(found.engine.name, func(t *testing.T) {
			runPageInOneEngine(t, found.engine, found.path, page, modules, nil, w, h)
		})
	}
}

// stubModule builds an inert ES module from export names. Each export
// is `const n = () => {}` — inert under every call, and crucially the
// NAME LIST is the contract: if the module under test gains an import,
// the map built from this list fails to instantiate rather than silently
// skipping — the missing-export lesson (§8, 2026-08-27) made structural.
func stubModule(names ...string) []byte {
	var b strings.Builder
	for _, n := range names {
		fmt.Fprintf(&b, "export const %s = () => {};\n", n)
	}
	return []byte(b.String())
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
		"/firstboot.js":      stubModule("renderProviderOptions", "setModelOptions", "fbHint", "fbResult", "acceptDiscoveryResponse", "firstbootConnectionLost"),
		"/panel.js":          stubModule("renderPanel"),
		"/presence.js":       stubModule("renderPresence", "setThinking"),
		"/sections.js":       stubModule("initSections", "sectionTitle", "publish", "onSections", "onLayout", "onTokensChanged"),
		"/theme.js":          stubModule("onTheme"),
		"/overlay.js":        stubModule("restoreDraft", "onOverlayChanged"),
		"/voice.js":          stubModule("wireMic", "bindTransport", "speak", "render", "connectionLost"),
		"/views/chat.js":     stubModule("scrollThread", "addMsg", "sysLine", "toolEventLive", "thinkingEvent", "renderHistory", "renderChatSubstrate", "acceptSubstrateConfig", "rejectSubstrateConfig", "substrateConnectionLost", "renderSteering"),
		"/views/home.js":     stubModule("renderHome"),
		"/views/work.js":     stubModule("renderWorkPill"),
		"/views/memory.js":   stubModule("renderMemory"),
		"/views/identity.js": stubModule("renderIdentity"),
		"/views/plugins.js":  stubModule("renderPlugins"),
		"/views/settings.js": stubModule("renderSettings", "acceptSettingsConfig", "rejectSettingsConfig", "acceptProviderSave", "rejectProviderSave", "settingsConnectionLost"),
	}
}

// TestBrowserEngineCoverage records which engines this host can actually
// execute. It is deliberately not a pass/fail gate on engine count: it
// prints the coverage so a green suite cannot be mistaken for proof that
// every target engine was exercised.
func TestBrowserEngineCoverage(t *testing.T) {
	engines := resolveEngines()
	if len(engines) == 0 {
		t.Skip("no browser engine available")
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
