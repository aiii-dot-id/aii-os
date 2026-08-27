package dashboard

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// expectedModules is the UP1 module layout — every file the shell needs.
// A module missing from the embed (a stray build tag, a rename that broke
// the //go:embed pattern) must fail HERE, not as a blank view in a
// shipped binary. New modules are picked up by the walk below without
// touching this list; listed ones are load-bearing.
var expectedModules = []string{
	"static/app.js",
	"static/state.js",
	"static/util.js",
	"static/ws.js",
	"static/presence.js",
	"static/firstboot.js",
	"static/sandbox.js",
	// The microphone and the voice. Load-bearing since app.js imports it
	// at boot: a missing embed is not a degraded feature, it is a module
	// resolution failure that stops the whole shell.
	"static/voice.js",
	// R66 UP2 — the section lane: the slot renderer, the pure bridge
	// core, and the frame-owned client sections import.
	"static/sections.js",
	"static/bridge.js",
	"static/section-api.js",
	"static/views/chat.js",
	"static/views/model-picker.js",
	"static/views/home.js",
	"static/views/work.js",
	"static/views/projects.js",
	"static/views/memory.js",
	"static/views/identity.js",
	"static/views/plugins.js",
	"static/views/settings.js",
}

// The dashboard UI is browser-native ES modules (UP1 of the R66 frame
// plan) — and Go compiles Go: NOTHING in the build pipeline parses
// JavaScript. On 2026-08-16 a quote collision shipped a syntax error that
// killed the then-single inline script (ws undefined, dead shell UI, the
// "frozen dashboard"). This test walks EVERY embedded .js module and
// syntax-checks it with node in module mode (`node --check` honors the
// .mjs extension, so import/export parse), so a broken module can never
// build green again — and a module the embed silently dropped fails the
// expected-set check. Skips when node is absent (CI without node still
// builds; dev8 and the dev loop always have it).
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

	// The shell must stay module-only: an inline <script> would dodge
	// every check above (the exact hole the 2026-08-16 freeze came
	// through), and the entry tag is the one line that boots everything.
	idx, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	shell := string(idx)
	if strings.Contains(shell, "<script>") {
		t.Error("index.html has an inline <script> — UP1 forbids unchecked inline JS; put it in a module")
	}
	if !strings.Contains(shell, `<script type="module" src="./app.js"></script>`) {
		t.Error("index.html does not load ./app.js as the module entry — the UI cannot boot")
	}
}
