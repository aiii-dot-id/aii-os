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
	"static/tools.js",
	// Settings → Plugins and permissions (step 16a): its words and its stylesheet.
	"static/strings/settings-permissions.js",
	"static/views/settings/permissions.css",
	"static/announce.js",
	"static/overlay.js",
	// The right column's cards moved to Status (step 5); panel.js stays as a
	// stand-in for an identity's own copy of a module that calls it.
	"static/panel.js",
	"static/pending.js",
	// The half of the sign-in the page holds: every request to the
	// dashboard carries it.
	"static/pagekey.js",
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
	// voice.js, split by what each part does (step 21).
	"static/voice/playback.js",
	"static/voice/state.js",
	"static/voice/capture.js",
	"static/voice/speech.js",
	"static/voice/session.js",
	"static/voice/modes.js",
	// What a view fetched when first opened gives the socket (step 21).
	"static/shell/later.js",
	// The socket's way to the voice, which is fetched once the shell has
	// started (step 22).
	"static/shell/voice-later.js",
	// The page's keys, heard from the start; Search is fetched once the shell
	// has started, and a view's sheets come with it (step 22).
	"static/shell/keys.js",
	"static/shell/sheet.js",
	// The streaming frame's codec, lane and pre-roll; voice.js imports it
	// and re-exports every name.
	"static/voice-stream.js",
	"static/voice-echo.js",
	// Loaded by voice.js as an AudioWorklet module (new URL(..., import.meta.url)).
	"static/voice-capture.worklet.js",
	// R66 UP2 — the section lane: the slot renderer and its frames (fetched
	// with the first section to show), the pure bridge core (with a section's
	// first hello), and the frame-owned client sections import.
	"static/sections.js",
	"static/sections-frames.js",
	"static/bridge.js",
	"static/section-api.js",
	// The shell (spec section 8, step 3): the router, the table of where
	// everything lives, the components app.js draws, and the words.
	"static/shell/project-pill.js",
	"static/shell/router.js",
	"static/registry.js",
	"static/components/button/button.js",
	"static/components/chip/chip.js",
	"static/components/os-note/os-note.js",
	"static/components/tabs/tabs.js",
	"static/components/top-bar/top-bar.js",
	"static/components/model-switch/model-switch.js",
	"static/shell/reload-keep.js",
	"static/shell/keep.js",
	"static/shell/visit.js",
	"static/components/side-nav/side-nav.js",
	// First run (spec step 7): the one screen, and its words.
	"static/views/firstrun/start.js",
	"static/strings/firstrun.js",
	"static/strings/shell.js",
	"static/strings/identity.js",
	"static/views/chat.js",
	// The conversation (step 4): views/chat.js re-exports it. Its parts
	// (step 6): the shared DOM steps, the questions, what became of a sent
	// message, long words, a stretch of work, a reply's thinking and the
	// reply box's presses.
	"static/views/chat/thread.js",
	"static/views/chat/draft.js",
	"static/views/chat/draft.css",
	"static/views/chat/dom.js",
	"static/views/chat/asks.js",
	"static/views/chat/sends.js",
	"static/views/chat/long-text.js",
	"static/views/chat/receipts.js",
	"static/views/chat/thinking.js",
	"static/views/chat/reply.js",
	"static/components/ask/ask.js",
	"static/components/reply-box/reply-box.js",
	// Home (step 6): their note, the one thread and reply box placed under
	// it, and the words.
	"static/views/home/note.js",
	"static/components/note/note.js",
	"static/shell/thread-place.js",
	"static/strings/home.js",
	// The channel straight between two devices, moved from ws.js.
	"static/transport/channel.js",
	// About {name} → Beliefs (step 9): the view, the trail's read, and
	// their components.
	"static/views/identity/beliefs.js",
	"static/views/identity/trail.js",
	"static/views/chat/why.js",
	"static/components/belief-row/belief-row.js",
	"static/components/why-trail/why-trail.js",
	// The Show chips and the beliefs no longer held, fetched when the tab
	// first draws, and their words.
	"static/views/identity/beliefs-show.js",
	"static/strings/beliefs-show.js",
	// About {name} → Who they are and Between you, and their mark (step 10).
	"static/views/identity/who.js",
	"static/views/identity/between.js",
	"static/views/identity/experiences.js",
	"static/views/identity/record.js",
	"static/components/in-words/in-words.js",
	"static/views/identity/relationships.js",
	"static/views/identity/page.js",
	"static/views/identity/record-read.js",
	"static/views/identity/when.js",
	"static/components/mark/look.js",
	"static/components/mark/mark.js",
	"static/shell/mark-place.js",
	"static/shell/menu.js",
	"static/components/outcome-row/outcome-row.js",
	"static/strings/chat.js",
	"static/strings/chat-draft.js",
	// AII OS's note at the top of every page, and its words.
	"static/shell/page-note.js",
	"static/strings/status.js",
	// Settings → Status (step 5), and the words the components share.
	"static/views/settings/status.js",
	// Settings → App (step 18): theme, notifications, updates, uninstall, and its words.
	"static/views/settings/app.js",
	"static/views/settings/app.css",
	"static/strings/settings-app.js",
	"static/strings/settings-voice.js",
	"static/views/settings/voice.css",
	"static/views/settings/access.css",
	"static/strings/settings-access.js",
	"static/strings/settings-phone.js",
	"static/views/settings/people.css",
	"static/views/settings/people-say.js",
	"static/strings/settings-people.js",
	// Settings → Model (step 15 Model b): its words.
	"static/strings/settings-model.js",
	// Settings → Technical (step 19): the page, its first panel and its words.
	"static/views/settings/technical/technical.js",
	"static/views/settings/technical/sent.js",
	"static/views/settings/technical/logs.js",
	"static/strings/settings-technical.js",
	"static/strings/components.js",
	"static/views/attach.js",
	"static/views/model-picker.js",
	"static/views/home.js",
	"static/views/work.js",
	// What each work session records, drawn as text under its row.
	"static/views/work-recorded.js",
	// The page's title and the notifications of an open question.
	"static/waiting.js",
	"static/views/projects.js",
	"static/views/projects.css",
	// The progress card's words (step 12); the rest of the view's come with its rebuild.
	"static/strings/projects.js",
	// The single owner of the delivered list's grade state (step 12,
	// host disposition 12.1): projects.js draws it on the Projects page.
	"static/views/delivered.js",
	"static/views/memory.js",
	"static/views/identity.js",
	"static/views/plugins.js",
	// Plugins and permissions, split by part (step 16b): views/plugins.js
	// keeps the page and imports these.
	"static/views/plugins/catalog.js",
	"static/views/plugins/card.js",
	"static/views/plugins/stored.js",
	"static/views/plugins/accounts.js",
	"static/views/settings.js",
	// Settings, split by section (step 14): views/settings.js keeps the
	// page and imports these.
	"static/views/settings/parts.js",
	"static/views/settings/state.js",
	"static/views/settings/save.js",
	"static/views/settings/applies.js",
	"static/views/settings/modelwords.js",
	"static/views/settings/keep.js",
	"static/views/settings/access.js",
	"static/views/settings/access-routes.js",
	"static/views/settings/phone.js",
	"static/views/settings/people.js",
	"static/views/settings/permissions.js",
	"static/views/settings/model.js",
	// The Model page's local-model part (0.2.1 item F): the part, its
	// visibility rule apart from the socket, and its words.
	"static/views/settings/local.js",
	"static/views/settings/local-rule.js",
	"static/strings/settings-local.js",
	"static/views/settings/capabilities.js",
	"static/views/settings/voice.js",
	"static/views/settings/voice-speakers.js",
	"static/views/settings/technical/forms.js",
	"static/views/settings/technical/record-state.js",
	"static/views/settings/technical/savebar.js",
	"static/views/settings/technical/reflection.js",
	"static/views/settings/technical/limits.js",
	"static/views/settings/technical/in-force.js",
	"static/views/settings/technical/layout.js",
	"static/views/settings/pass-now.js",
	"static/views/settings/limit-row.css",
	"static/strings/settings-technical-forms.js",
	"static/views/plugin-setting.js",
	// Settings → Backups (step 18): the view, its stylesheet and its words.
	"static/views/backups.js",
	"static/views/backups.css",
	"static/strings/settings-backups.js",
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
		// A stylesheet a module loads (Settings → App's) is listed with the
		// modules; it is checked for presence, not as JavaScript.
		if !strings.HasSuffix(m, ".js") {
			if _, err := staticFS.ReadFile(m); err != nil {
				t.Errorf("expected file %s is not embedded — a view would load unstyled", m)
			}
			continue
		}
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
