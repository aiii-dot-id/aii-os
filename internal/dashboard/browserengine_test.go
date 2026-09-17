//go:build !windows

// .
// .
// .
// .
// .
// .
// .

package dashboard

import (
	"context"
	"fmt"
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
type browserEngine struct {
	name string
	// .
	bins []string
	// .
	args func(profileDir, url string) []string
	// .
	// .
	// .
	// .
	// .
	sizeArgs func(w, h int) []string
	// .
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
			// .
			// .
			return []string{"--headless", "-no-remote", "-profile", profileDir, url}
		},
		sizeArgs: func(w, h int) []string {
			return []string{"-width", strconv.Itoa(w), "-height", strconv.Itoa(h)}
		},
		prepare: func(profileDir string) error {
			// .
			// .
			// .
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

// .
// .
// .
// .
// .
// .
// .
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
		var resolved string
		for _, bin := range candidate.bins {
			if path, err := exec.LookPath(bin); err == nil {
				resolved = path
				break
			}
		}
		if resolved == "" {
			resolved = macBundleBinary(candidate.name)
		}
		if resolved != "" {
			found = append(found, struct {
				engine browserEngine
				path   string
			}{candidate, resolved})
		}
	}
	return found
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
func macBundleBinary(engine string) string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	for _, p := range map[string][]string{
		"blink-chrome": {
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
		},
		"blink-edge": {
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		},
		"gecko-firefox": {
			"/Applications/Firefox.app/Contents/MacOS/firefox",
		},
	}[engine] {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	return ""
}

// .
// .
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
// asserted (found by a label mutant the page's own assertion
// should have caught). assert is also handed to the body, so pages may
// take it as a parameter or import it; both are the same function.
export function run(body) {
  let r;
  try { r = body(assert); } catch (e) { report(failure(e)); return; }
  if (r && typeof r.then === 'function') { r.then(() => report('OK'), (e) => report(failure(e))); }
  else report('OK');
}
window.addEventListener('error', ev => report('FAIL: uncaught ' + ((ev.error && ev.error.message) || ev.message)));
window.addEventListener('unhandledrejection', ev => report('FAIL: rejected ' + String(ev.reason)));
`

// .
// .
// .
// .
// .
func runPageInEngines(t *testing.T, page string, modules map[string][]byte) {
	t.Helper()
	runPageInEnginesWithHeaders(t, page, modules, nil)
}

// .
// .
// .
// .
// .
func runPageInEnginesWithHeaders(t *testing.T, page string, modules map[string][]byte, headers map[string]string) {
	t.Helper()

	engines := resolveEngines()
	if len(engines) == 0 {
		t.Skip("no browser engine available")
	}

	for _, found := range engines {
		found := found
		t.Run(found.engine.name, func(t *testing.T) {
			runPageInOneEngine(t, found.engine, found.path, page, modules, headers, 0, 0, 0)
		})
	}
}

// .
// .
// .
func runPageInOneEngine(t *testing.T, engine browserEngine, path string, page string, modules map[string][]byte, headers map[string]string, w, h int, inertFor time.Duration) {
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
		// .
		// .
		// .
		// .
		// .
		// .
		// .
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
		// .
		// .
		// .
		// .
		// .
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
		}
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
	} else if engine.sizeArgs != nil {
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		launchArgs = append(engine.sizeArgs(800, 600), launchArgs...)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, path, launchArgs...)
	cmd.Env = append(os.Environ(), "MOZ_HEADLESS=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	cmd.Env = append(cmd.Env, "DBUS_SESSION_BUS_ADDRESS=disabled:")
	if err := cmd.Start(); err != nil {
		t.Fatalf("%s failed to start: %v", engine.name, err)
	}
	defer func() {
		if err := stopBrowserProcess(cmd); err != nil {
			t.Errorf("%s cleanup: %v", engine.name, err)
		}
	}()

	if inertFor > 0 {
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		select {
		case result := <-results:
			t.Fatalf("%s (%s): a page expected to be INERT reported %q — it ran", engine.name, path, result)
		case <-time.After(inertFor):
		case <-ctx.Done():
		}
		return
	}
	select {
	case result := <-results:
		if result != "OK" {
			t.Fatalf("%s (%s): %s", engine.name, path, result)
		}
	case <-ctx.Done():
		t.Fatalf("%s (%s): page never reported a result within the timeout", engine.name, path)
	}
}

// .
// .
// .
func navigateInEnginesExpectingInertness(t *testing.T, page string, modules map[string][]byte, headers map[string]string, settle time.Duration) {
	t.Helper()
	engines := resolveEngines()
	if len(engines) == 0 {
		t.Skip("no browser engine available")
	}
	for _, found := range engines {
		found := found
		t.Run(found.engine.name, func(t *testing.T) {
			runPageInOneEngine(t, found.engine, found.path, page, modules, headers, 0, 0, settle)
		})
	}
}

// .
// .
// .
// .
func stopBrowserProcess(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	pgid := cmd.Process.Pid
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
	_ = cmd.Process.Kill()
	_ = cmd.Wait()

	deadline := time.Now().Add(2 * time.Second)
	for {
		err := syscall.Kill(-pgid, 0)
		if err == syscall.ESRCH {
			return nil
		}
		if err == syscall.EPERM {
			// .
			// .
			// .
			// .
			// .
			// .
			// .
			// .
			// .
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

// .
// .
// .
// .
// .
func runPageInEnginesAtSize(t *testing.T, page string, modules map[string][]byte, w, h int) {
	t.Helper()

	engines := resolveEngines()
	if len(engines) == 0 {
		t.Skip("no browser engine available")
	}

	for _, found := range engines {
		found := found
		t.Run(found.engine.name, func(t *testing.T) {
			runPageInOneEngine(t, found.engine, found.path, page, modules, nil, w, h, 0)
		})
	}
}

// .
// .
// .
// .
// .
func stubModule(names ...string) []byte {
	var b strings.Builder
	for _, n := range names {
		fmt.Fprintf(&b, "export const %s = () => {};\n", n)
	}
	return []byte(b.String())
}

// .
// .
// .
// .
// .
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
		"/views/chat.js":     stubModule("scrollThread", "addMsg", "attachSpeaker", "sysLine", "toolEventLive", "thinkingEvent", "renderHistory", "renderChatSubstrate", "renderComposer", "acceptSubstrateConfig", "rejectSubstrateConfig", "substrateConnectionLost", "renderSteering", "renderAsks"),
		"/views/home.js":     stubModule("renderHome"),
		"/views/work.js":     stubModule("renderWorkPill"),
		"/views/memory.js":   stubModule("renderMemory"),
		"/views/identity.js": stubModule("renderIdentity"),
		"/views/plugins.js":  stubModule("renderPlugins"),
		"/views/settings.js": stubModule("renderSettings", "acceptSettingsConfig", "rejectSettingsConfig", "acceptProviderSave", "rejectProviderSave", "acceptSpeechLists", "rejectSpeechLists", "acceptDashboardToken", "settingsConnectionLost"),
	}
}

// .
// .
// .
// .
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
