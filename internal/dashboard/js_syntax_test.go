package dashboard

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// .
// .
// .
// .
// .
var expectedModules = []string{
	"static/app.js",
	"static/state.js",
	"static/util.js",
	"static/ws.js",
	"static/presence.js",
	"static/firstboot.js",
	"static/sandbox.js",
	// .
	// .
	// .
	"static/voice.js",
	// .
	// .
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
		// .
		// .
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

	// .
	// .
	// .
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

// .
// .
// .
// .
// .
// .
func TestTheErrorDispatchDisarmsEveryProjectSaveSlot(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("static", "ws.js"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	// .
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
	// .
	for _, want := range []string{"rejectFocusSave", "rejectContractSave"} {
		if !strings.Contains(text, want+",") && !strings.Contains(text, want+" }") {
			t.Errorf("%s is called but not imported in ws.js", want)
		}
	}
}
