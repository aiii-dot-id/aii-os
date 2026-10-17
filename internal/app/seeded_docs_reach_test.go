package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// readerIdentity starts an identity in the stock layout with the sandbox on
// or off; layout, when given, puts files in place before the start. The
// docs are seeded as Run seeds them, before the boot (lifecycle.go).
func readerIdentity(t *testing.T, sandboxOff bool, layout func(home, configDir string)) (a *App, home, configDir string) {
	t.Helper()
	home = filepath.Join(t.TempDir(), "identity-0")
	configDir = filepath.Join(home, ConfigDirName)
	data := filepath.Join(home, "data")
	for _, dir := range []string{configDir, data} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	keyPath, ledgerPath, dbPath := birthFixture(t, data, "Reader")
	t.Chdir(home)
	withProcessTable(t, "")
	cfg := defaultConfig()
	cfg.Identity = IdentityConfig{KeyPath: keyPath, LedgerPath: ledgerPath, DBPath: dbPath}
	cfg.LLM = withTestProvider(t, configDir, "test", "https://127.0.0.1:1", "m", "sk-x")
	cfg.SourcePath = filepath.Join(configDir, "config.json")
	cfg.Dashboard.Port = 0
	cfg.Dashboard.RequireToken = true // the sandbox is off only while the dashboard asks for its token
	cfg.Tools.CWD = home
	cfg.Tools.SandboxOff = sandboxOff
	cfg.Machine.Dir = filepath.Join(t.TempDir(), "machine")
	cfg.Genesis.FirewallURL = genesistest.Ring5Server(t)
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if layout != nil {
		layout(home, configDir)
	}
	a = New(cfg)
	a.seedSkillsDoc()
	a.seedMethodDoc()
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopApp(t, a) })
	if got := a.toolReg.SandboxOff(); got != sandboxOff {
		t.Fatalf("the started identity's sandbox is off: %v, want %v", got, sandboxOff)
	}
	return a, home, configDir
}

// use calls a tool through the identity's own dispatch and fails the test
// on any refusal: these docs are the identity's, wherever the sandbox
// stands.
func use(t *testing.T, a *App, tool string, args map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	call := llm.ToolCall{ID: "doc", Type: "function"}
	call.Function.Name, call.Function.Arguments = tool, string(raw)
	obs := a.executeToolCall(context.Background(), call)
	if obs.Failed || refusedSeededDoc(obs) {
		t.Fatalf("%s %s was refused to the identity: %s", tool, raw, obs.Text)
	}
	return obs.Text
}

func refusedSeededDoc(obs conversation.Observation) bool {
	for _, said := range []string{"access denied", "outside sandbox", "Kept by the host", "no such file"} {
		if strings.Contains(obs.Text, said) {
			return true
		}
	}
	return false
}

// seededDocs names each doc with a phrase only it carries.
var seededDocs = map[string]string{skillsFileName: "describes-build: ", methodFileName: "OCCAM'S RAZOR"}

// aLineOnlyOnce is a line that occurs once in the doc, for edit to replace.
func aLineOnlyOnce(t *testing.T, path string) string {
	t.Helper()
	body := mustRead(t, path)
	for _, line := range strings.Split(string(body), "\n") {
		if len(strings.TrimSpace(line)) > 12 && bytes.Count(body, []byte(line)) == 1 {
			return line
		}
	}
	t.Fatalf("%s has no line that occurs once", path)
	return ""
}

// shellCommands are the host shell's way to print the docs' annotated
// lines, list the home and append to a doc.
func shellCommands() (show, list, appendLine string) {
	if runtime.GOOS == "windows" { // the host shell is PowerShell there
		return "Select-String -SimpleMatch -Pattern 'annotated by the identity' -Path SKILLS.md,METHOD.md",
			"Get-ChildItem -Name",
			"Add-Content -Path METHOD.md -Value 'a line the shell wrote'"
	}
	return "grep -h 'annotated by the identity' SKILLS.md METHOD.md",
		"ls",
		"printf '%s\\n' 'a line the shell wrote' >> METHOD.md"
}

// readsSearchesListsAndAnnotates drives both docs through read (by a
// relative path and by the home's absolute path), grep, ls, edit and the
// shell, and then runs the seed again: an edit lands and is still there.
func readsSearchesListsAndAnnotates(t *testing.T, a *App, home string) {
	t.Helper()
	listing := use(t, a, "ls", map[string]any{})
	for doc, phrase := range seededDocs {
		if !strings.Contains(listing, doc) {
			t.Fatalf("ls of the home does not show %s:\n%s", doc, listing)
		}
		abs := filepath.Join(home, doc)
		for _, path := range []string{doc, abs} {
			if got := use(t, a, "read", map[string]any{"file_path": path}); !strings.Contains(got, phrase) {
				t.Fatalf("read %s is not the seeded doc:\n%.300s", path, got)
			}
		}
		if got := use(t, a, "grep", map[string]any{"pattern": phrase, "path": doc, "literal": true}); !strings.Contains(got, phrase) {
			t.Fatalf("grep %q in %s found nothing: %s", phrase, doc, got)
		}
		if got := use(t, a, "grep", map[string]any{"pattern": phrase, "literal": true, "output": "files"}); !strings.Contains(got, doc) {
			t.Fatalf("grep %q across the home does not name %s: %s", phrase, doc, got)
		}
		line := aLineOnlyOnce(t, abs)
		use(t, a, "edit", map[string]any{"file_path": doc, "old_string": line, "new_string": line + " (annotated by the identity)"})
	}
	show, list, appendLine := shellCommands()
	shown := use(t, a, "shell", map[string]any{"command": show})
	if strings.Count(shown, "annotated by the identity") != len(seededDocs) {
		t.Fatalf("the shell does not show both annotations:\n%s", shown)
	}
	if listed := use(t, a, "shell", map[string]any{"command": list}); !strings.Contains(listed, skillsFileName) || !strings.Contains(listed, methodFileName) {
		t.Fatalf("the shell's listing of the home does not show the docs:\n%s", listed)
	}
	use(t, a, "shell", map[string]any{"command": appendLine})

	// The next start's seed pass: the identity's edits win.
	a.seedSkillsDoc()
	a.seedMethodDoc()
	for doc := range seededDocs {
		if got := use(t, a, "read", map[string]any{"file_path": doc}); !strings.Contains(got, "annotated by the identity") {
			t.Fatalf("the seed pass took the identity's edit of %s away", doc)
		}
	}
	if got := use(t, a, "read", map[string]any{"file_path": methodFileName}); !strings.Contains(got, "a line the shell wrote") {
		t.Fatal("the seed pass took away the line the shell wrote")
	}
}

// THE STOCK LAYOUT, SANDBOX ON AND OFF: the docs are at the home's root,
// and every tool the identity has reaches them by the name the pointers
// give (SKILLS.md, relative) and by the home's path.
func TestAStartedIdentityReadsSearchesListsAndAnnotatesItsSeededDocs(t *testing.T) {
	for _, off := range []bool{false, true} {
		t.Run(map[bool]string{false: "sandbox on", true: "sandbox off"}[off], func(t *testing.T) {
			a, home, configDir := readerIdentity(t, off, nil)
			readsSearchesListsAndAnnotates(t, a, home)
			for doc := range seededDocs {
				absent(t, filepath.Join(configDir, doc))
			}

			// Where the identity is told to look: the tools organ names
			// both docs at the root, by name.
			organ := use(t, a, "tools", map[string]any{})
			if !strings.Contains(organ, "Seeded docs at your root") || !strings.Contains(organ, "  "+skillsFileName+" — ") || !strings.Contains(organ, "  "+methodFileName+" — ") {
				t.Fatalf("the tools organ does not point at the docs at the root:\n%s", organ)
			}
		})
	}
}

// THE LAYOUT BEFORE config/ HELD THE DOCS. The platform's copy of an older
// build left in config/ goes and the current one is seeded at home; the
// identity's own annotated copy moves home whole. Either way they are then
// read, searched, listed and annotated at home, never refused as kept.
func TestDocsLeftInTheConfigDirectoryAreTheIdentitysAtHomeAfterTheStart(t *testing.T) {
	for _, off := range []bool{false, true} {
		t.Run(map[bool]string{false: "sandbox on", true: "sandbox off"}[off], func(t *testing.T) {
			annotated := append(append([]byte(nil), methodMD...), "\n\nA margin note written before the move.\n"...)
			a, home, configDir := readerIdentity(t, off, func(_, configDir string) {
				for doc, body := range map[string][]byte{skillsFileName: skillsTemplate("0e1a2b3c4d5e"), methodFileName: annotated} {
					if err := os.WriteFile(filepath.Join(configDir, doc), body, 0o644); err != nil {
						t.Fatal(err)
					}
				}
			})
			for doc := range seededDocs {
				absent(t, filepath.Join(configDir, doc))
			}
			if got := use(t, a, "read", map[string]any{"file_path": methodFileName}); !strings.Contains(got, "A margin note written before the move.") {
				t.Fatal("the identity's own Method did not come home with its note")
			}
			readsSearchesListsAndAnnotates(t, a, home)
		})
	}
}

// TWO OF THE IDENTITY'S OWN COPIES, ONE AT HOME AND ONE LEFT IN config/:
// both stay (which is current is theirs to say), and both are theirs to
// read and edit — config/ holds the config file and the providers file,
// which the floor keeps, and nothing else of it is kept.
func TestBothOfTheIdentitysOwnCopiesAreTheirsToReadAndEdit(t *testing.T) {
	for _, off := range []bool{false, true} {
		t.Run(map[bool]string{false: "sandbox on", true: "sandbox off"}[off], func(t *testing.T) {
			atHome := append(append([]byte(nil), methodMD...), "\n\nwritten at home\n"...)
			inConfig := append(append([]byte(nil), methodMD...), "\n\nwritten in config\n"...)
			a, _, configDir := readerIdentity(t, off, func(home, configDir string) {
				for path, body := range map[string][]byte{filepath.Join(home, methodFileName): atHome, filepath.Join(configDir, methodFileName): inConfig} {
					if err := os.WriteFile(path, body, 0o644); err != nil {
						t.Fatal(err)
					}
				}
			})
			for path, note := range map[string]string{methodFileName: "written at home", filepath.Join(ConfigDirName, methodFileName): "written in config"} {
				if got := use(t, a, "read", map[string]any{"file_path": path}); !strings.Contains(got, note) {
					t.Fatalf("read %s is not the identity's copy:\n%.300s", path, got)
				}
				use(t, a, "edit", map[string]any{"file_path": path, "old_string": note, "new_string": note + ", and merged"})
			}
			if !bytes.Contains(mustRead(t, filepath.Join(configDir, methodFileName)), []byte("written in config, and merged")) {
				t.Fatal("the edit of the copy in config/ did not land")
			}
		})
	}
}
