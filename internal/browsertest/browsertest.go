package browsertest

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	Chrome  = "blink-chrome"
	Edge    = "blink-edge"
	Firefox = "gecko-firefox"
	WebKit  = "webkit-playwright"
)

const playwrightDir = "AII_PLAYWRIGHT_DIR"

var onPath = map[string][]string{
	Chrome:  {"google-chrome", "google-chrome-stable", "chromium", "chromium-browser"},
	Edge:    {"microsoft-edge", "microsoft-edge-stable"},
	Firefox: {"firefox", "firefox-esr"},
}

var onWindowsPath = map[string][]string{
	Chrome: {"chrome"},
	Edge:   {"msedge"},
}

func pathNames(engine string) []string {
	names := onPath[engine]
	if runtime.GOOS == "windows" {
		names = append(append([]string(nil), names...), onWindowsPath[engine]...)
	}
	return names
}

func installed(engine string) []string {
	switch runtime.GOOS {
	case "darwin":
		bundles := map[string][]string{
			Chrome:  {"Google Chrome.app/Contents/MacOS/Google Chrome", "Chromium.app/Contents/MacOS/Chromium"},
			Edge:    {"Microsoft Edge.app/Contents/MacOS/Microsoft Edge"},
			Firefox: {"Firefox.app/Contents/MacOS/firefox"},
		}[engine]
		var roots []string
		if home, err := os.UserHomeDir(); err == nil {
			roots = append(roots, filepath.Join(home, "Applications"))
		}
		roots = append(roots, "/Applications")
		var out []string
		for _, root := range roots {
			for _, b := range bundles {
				out = append(out, filepath.Join(root, b))
			}
		}
		return out
	case "windows":
		rel := map[string][]string{
			Chrome:  {`Google\Chrome\Application\chrome.exe`, `Chromium\Application\chrome.exe`},
			Edge:    {`Microsoft\Edge\Application\msedge.exe`},
			Firefox: {`Mozilla Firefox\firefox.exe`},
		}[engine]
		var out []string
		for _, root := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LOCALAPPDATA")} {
			if root == "" {
				continue
			}
			for _, r := range rel {
				out = append(out, filepath.Join(root, r))
			}
		}
		return out
	}
	return nil
}

func Find(engine string) (path string, looked []string) {
	if engine == WebKit {
		return findWebKit()
	}
	for _, name := range pathNames(engine) {
		looked = append(looked, name+" on PATH")
		if p, err := exec.LookPath(name); err == nil {
			return p, looked
		}
	}
	for _, p := range installed(engine) {
		looked = append(looked, p)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p, looked
		}
	}
	return "", looked
}

func findWebKit() (string, []string) {
	var looked []string
	if runtime.GOOS == "windows" {
		return "", append(looked, "WebKit is not driven on Windows")
	}
	dir := os.Getenv(playwrightDir)
	if dir == "" {
		return "", append(looked, playwrightDir+" unset (a folder with node_modules/playwright-core and browsers/webkit-*)")
	}
	looked = append(looked, "node on PATH")
	if _, err := exec.LookPath("node"); err != nil {
		return "", looked
	}
	core := filepath.Join(dir, "node_modules", "playwright-core", "package.json")
	looked = append(looked, core)
	if _, err := os.Stat(core); err != nil {
		return "", looked
	}
	browsers := filepath.Join(dir, "browsers", "webkit-*", "INSTALLATION_COMPLETE")
	looked = append(looked, browsers)
	if found, _ := filepath.Glob(browsers); len(found) == 0 {
		return "", looked
	}
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		return "", append(looked, "this package's own folder (unknown)")
	}
	script := filepath.Join(filepath.Dir(here), "webkit-playwright.mjs")
	looked = append(looked, script)
	if fi, err := os.Stat(script); err != nil || fi.IsDir() {
		return "", looked
	}
	return script, looked
}

func Allowed(engine string) bool {
	named := false
	for _, name := range strings.Split(os.Getenv("AII_BROWSER_ENGINES"), ",") {
		switch strings.TrimSpace(name) {
		case "":
		case engine:
			return true
		default:
			named = true
		}
	}
	return !named
}

func Looked(engine string, looked []string) string {
	if !Allowed(engine) {
		return engine + " is not among AII_BROWSER_ENGINES=" + os.Getenv("AII_BROWSER_ENGINES")
	}
	return strings.Join(looked, ", ")
}
