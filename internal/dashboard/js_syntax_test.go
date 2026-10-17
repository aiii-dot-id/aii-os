package dashboard

import (
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// expectedModules is the UP1 module layout — every file the shell needs.
// A module missing from the embed (a stray build tag, a rename that broke
// the //go:embed pattern) must fail HERE, not as a blank view in a
// shipped binary. New modules are picked up by the walk below without
// touching this list; listed ones are load-bearing, and every module the
// shell reaches from index.html is listed
// (TestExpectedModulesCoverEveryModuleTheShellLoads).
var expectedModules = []string{
	// Loaded by index.html itself: the theme before first paint, the
	// entry, the way out, and the identity's last word.
	"static/theme-boot.js",
	"static/app.js",
	// What each view reads and draws, apart from app.js.
	"static/views.js",

	"static/recovery.js",
	"static/custom.js",
	"static/state.js",
	"static/util.js",
	"static/ws.js",
	"static/presence.js",
	"static/firstboot.js",
	"static/firstboot-restore.js",
	"static/sandbox.js",
	"static/scripts.js",
	"static/announce.js",
	"static/overlay.js",
	"static/panel.js",
	"static/pending.js",
	"static/say.js",
	"static/shell-auth.js",
	"static/signin.js",
	"static/theme.js",
	"static/interaction-view.js",
	"static/interaction-display.js",
	// The microphone and the voice. Load-bearing since app.js imports it
	// at boot: a missing embed is not a degraded feature, it is a module
	// resolution failure that stops the whole shell.
	"static/voice.js",
	// The streaming frame's codec, lane and pre-roll; voice.js imports it
	// and re-exports every name.
	"static/voice-stream.js",
	"static/voice-echo.js",
	// Loaded by voice.js as an AudioWorklet module (new URL(..., import.meta.url)).
	"static/voice-capture.worklet.js",

	"static/sections.js",
	"static/bridge.js",
	"static/section-api.js",
	"static/views/chat.js",
	"static/views/attach.js",
	"static/views/model-picker.js",
	"static/views/home.js",
	"static/views/work.js",
	"static/views/projects.js",
	"static/views/memory.js",
	"static/views/identity.js",
	"static/views/plugins.js",
	"static/views/settings.js",
	"static/views/plugin-setting.js",
	"static/views/backups.js",
	"static/views/mail-repair.js",
	"static/views/messages.js",
	"static/views/expert.js",
	"static/views/record.js",
}

// moduleRef finds what a module loads: static imports and re-exports, bare
// imports, dynamic imports of a literal, and a worklet's
// new URL('...', import.meta.url). Only relative references are the frame's.
var moduleRef = regexp.MustCompile(`(?m)(?:^\s*(?:import|export)\s[^;]*?\sfrom\s*'([^']+)'|^\s*import\s*'([^']+)'|\bimport\(\s*'([^']+)'\s*\)|new URL\(\s*'([^']+)'\s*,\s*import\.meta\.url\))`)

// TestExpectedModulesCoverEveryModuleTheShellLoads walks the module graph
// from the scripts index.html loads and fails on any module reached that
// expectedModules does not list, so the list stays the whole boot.
func TestExpectedModulesCoverEveryModuleTheShellLoads(t *testing.T) {
	listed := map[string]bool{}
	for _, m := range expectedModules {
		listed[m] = true
	}
	idx, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	var queue []string
	for _, m := range regexp.MustCompile(`<script[^>]*\ssrc="\./([^"]+\.js)"`).FindAllSubmatch(idx, -1) {
		queue = append(queue, "static/"+string(m[1]))
	}
	if len(queue) == 0 {
		t.Fatal("index.html loads no module")
	}
	seen := map[string]bool{}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		if seen[p] {
			continue
		}
		seen[p] = true
		if !listed[p] {
			t.Errorf("%s is loaded by the shell but not listed in expectedModules", p)
		}
		src, err := staticFS.ReadFile(p)
		if err != nil {
			t.Errorf("%s is loaded by the shell and is not embedded: %v", p, err)
			continue
		}
		for _, m := range moduleRef.FindAllSubmatch(src, -1) {
			ref := string(m[1]) + string(m[2]) + string(m[3])
			if !strings.HasPrefix(ref, "./") && !strings.HasPrefix(ref, "../") {
				ref = ""
			}
			// A URL resolved against import.meta.url is relative with or
			// without "./"; one with a scheme or a leading "/" is not the
			// frame's neighbour.
			if u := string(m[4]); u != "" && !strings.Contains(u, ":") && !strings.HasPrefix(u, "/") {
				ref = u
			}
			if ref == "" {
				continue
			}
			queue = append(queue, path.Join(path.Dir(p), ref))
		}
	}
	if len(seen) < 30 {
		t.Fatalf("the walk reached %d modules — the reference pattern no longer matches the frame's imports", len(seen))
	}
}

func TestEmbeddedJSSyntax(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not available — JS syntax check skipped")
	}

	found := map[string]bool{}
	total := 0
	tmp := t.TempDir()
	err = fs.WalkDir(staticFS, "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".js") {
			return nil
		}
		raw, rerr := staticFS.ReadFile(p)
		if rerr != nil {
			t.Fatalf("read embedded %s: %v", p, rerr)
		}
		if len(raw) == 0 {
			t.Fatalf("%s is EMPTY — the embed carried a hollow module", p)
		}
		found[p] = true
		total += len(raw)
		// .mjs puts node --check in module mode; flatten the path so
		// views/chat.js cannot collide with a future chat.js.
		flat := strings.ReplaceAll(strings.TrimPrefix(p, "static/"), "/", "__") + ".mjs"
		fpath := filepath.Join(tmp, flat)
		if werr := os.WriteFile(fpath, raw, 0600); werr != nil {
			t.Fatal(werr)
		}
		if out, cerr := exec.Command(node, "--check", fpath).CombinedOutput(); cerr != nil {
			t.Errorf("%s has a SYNTAX ERROR — the UI is dead on load:\n%s", p, out)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, m := range expectedModules {
		if !found[m] {
			t.Errorf("expected module %s is not embedded — a view would load blank", m)
		}
	}
	if total < 10000 {
		t.Fatalf("all modules together are %d bytes — suspiciously small, extraction broken?", total)
	}

	idx, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	shell := string(idx)
	if strings.Contains(shell, "<script>") {
		t.Error("index.html has an inline <script> — UP1 forbids unchecked inline JS; put it in a module")
	}
	entry := strings.Index(shell, `<script type="module" src="./app.js"></script>`)
	if entry < 0 {
		t.Error("index.html does not load ./app.js as the module entry — the UI cannot boot")
	}

	way := strings.Index(shell, `<script type="module" src="./recovery.js"></script>`)
	last := strings.Index(shell, `<script type="module" src="./custom.js"></script>`)
	if way < 0 || way < entry || last < way {
		t.Errorf("index.html must load ./app.js, then ./recovery.js, then ./custom.js (at %d, %d, %d)", entry, way, last)
	}
	// It asks how app.js ended by the one import that answers with the
	// failure: a static import would fail recovery.js with it, and nothing
	// would be left to say so.
	way2, err := staticFS.ReadFile("static/recovery.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(way2), "import('./app.js')") || regexp.MustCompile(`(?m)^\s*import\s`).Match(way2) {
		t.Error("recovery.js must ask for ./app.js by a dynamic import and import nothing statically — a module it cannot load would take the way out with it")
	}
}

// The browser failure tests call rejectContractSave directly, which
// proves what the SURFACE does with a refusal and nothing about whether
// the dispatcher ever delivers one. Deleting the call from ws.js would
// leave every one of those tests green while the editor wedged in the
// real app — the same gap that let "output isolation proves query
// isolation" stand in recall. So the delivery is pinned where it lives.
func TestTheErrorDispatchDisarmsEveryProjectSaveSlot(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("static", "ws.js"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	// The one line that consumes a correlated error.
	var line string
	for _, l := range strings.Split(text, "\n") {
		if strings.Contains(l, "rejectCreate(requestID)") {
			line = l
			break
		}
	}
	if line == "" {
		t.Fatal("could not find the correlated-error dispatch line in ws.js")
	}
	for _, want := range []string{"rejectCreate", "rejectFocusSave", "rejectContractSave"} {
		if !strings.Contains(line, want+"(requestID)") {
			t.Errorf("the error dispatch does not disarm %s — a refused save leaves its slot armed and the button inert until reload:\n\t%s", want, strings.TrimSpace(line))
		}
	}
	// And each must actually be imported, or the call is a runtime error.
	for _, want := range []string{"rejectFocusSave", "rejectContractSave"} {
		if !strings.Contains(text, want+",") && !strings.Contains(text, want+" }") {
			t.Errorf("%s is called but not imported in ws.js", want)
		}
	}
}
