//go:build windows

// junction_windows_test.go — the identity's file tools through a junction.
// Like second_form_windows_test.go this file is vetted by the host gate and
// RUNS only on a real Windows host.
package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
)

// makeJunction makes link a junction to target (mklink /J, which needs no
// privilege): what the identity's shell can make in its home, where no
// kernel wall stands on Windows.
func makeJunction(t *testing.T, link, target string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe"), "/c", "mklink", "/J", link, target).CombinedOutput()
	if err != nil {
		t.Fatalf("a junction %s -> %s: %v: %s", link, target, err, out)
	}
}

func TestTheFileToolsThroughAJunctionAreRefused(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := base + `\identity-0`
	data := home + `\data`
	confDir := home + `\conf`
	profile := confDir + `\profile-secret.json`
	sibling := base + `\identity-1`
	outside := base + `\elsewhere`
	files := map[string]string{
		data + `\credentials\anthropic.json`: "KEPT SECRET\r\n",
		data + `\identity.sec`:               "KEPT SECRET\r\n",
		profile:                              "KEPT SECRET\r\n",
		home + `\config.json`:                "KEPT SECRET\r\n",
		sibling + `\data\identity.sec`:       "KEPT SECRET\r\n",
		outside + `\notes.txt`:               "NOT THE HOME\r\n",
		home + `\notes.md`:                   "a note of the home\r\n",
	}
	restore := func(t *testing.T) {
		t.Helper()
		for f, body := range files {
			if err := os.MkdirAll(filepath.Dir(f), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	restore(t)
	policy := firewall.DefaultPolicy()
	policy.SetPlaces(append(stockPlaces(home, profile),
		&firewall.Rule{ID: "sub.identity", Kind: firewall.KindSubstrate, Path: sibling, Reason: "kept", Enforced: true},
		&firewall.Rule{ID: "sub.later", Kind: firewall.KindSubstrate, Path: data + `\later`, Reason: "kept", Enforced: true}))
	r := NewRegistry(home, policy, Timeouts{})

	call := func(t *testing.T, tool string, args map[string]any) Result {
		t.Helper()
		res, err := r.Execute(context.Background(), tool, args)
		if err != nil {
			t.Fatalf("%s %v: %v", tool, args, err)
		}
		return res
	}
	leaked := func(res Result) bool {
		for _, s := range []string{"KEPT SECRET", "NOT THE HOME", "anthropic.json", "identity.sec", "credentials", "notes.txt", "profile-secret"} {
			if strings.Contains(res.Output, s) {
				return true
			}
		}
		return false
	}
	// doors drives every file tool at dir (a folder through the junction)
	// and file (a file in it), and requires each refusal. Where only the
	// file is kept (keptFolder false: the folder of a kept file), the
	// folder's own doors may answer, and must not show the file.
	doors := func(t *testing.T, dir, file string, keptFolder bool) {
		t.Helper()
		type door struct {
			name, tool string
			args       map[string]any
			folder     bool
		}
		ask := []door{
			{"read", "read", map[string]any{"file_path": file}, false},
			{"grep of the file", "grep", map[string]any{"pattern": "SECRET|HOME", "path": file}, false},
			{"write", "write", map[string]any{"file_path": file, "content": "SPRAY"}, false},
			{"edit", "edit", map[string]any{"file_path": file, "old_string": "KEPT", "new_string": "SPRAY"}, false},
			{"ls", "ls", map[string]any{"path": dir}, true},
			{"grep of the folder", "grep", map[string]any{"pattern": "SECRET|HOME", "path": dir}, true},
		}
		if keptFolder {
			ask = append(ask, door{"write of a new file in the folder", "write", map[string]any{"file_path": dir + `\forged.json`, "content": "SPRAY"}, true})
		}
		for _, d := range ask {
			t.Run(d.name, func(t *testing.T) {
				res := call(t, d.tool, d.args)
				if d.folder && !keptFolder {
					if leaked(res) {
						t.Errorf("the folder of a kept file showed the file through the junction: %q", res.Output)
					}
				} else if !res.Refused || leaked(res) {
					t.Errorf("NOT REFUSED through the junction: refused=%v code=%q error=%q output=%q", res.Refused, res.ReasonCode, res.Error, res.Output)
				} else {
					t.Logf("refused: code=%q error=%q", res.ReasonCode, res.Error)
				}
				for f, body := range files {
					if b, err := os.ReadFile(f); err != nil || string(b) != body {
						t.Errorf("%s was changed: %q %v", f, b, err)
					}
				}
				for _, made := range []string{data + `\forged.json`, data + `\credentials\forged.json`, confDir + `\forged.json`, sibling + `\data\forged.json`, outside + `\forged.json`, data + `\later\forged.json`} {
					if _, err := os.Stat(made); err == nil {
						t.Errorf("%s was made through the junction", made)
						os.Remove(made)
					}
				}
				restore(t)
			})
		}
	}

	// The controls.
	if res := call(t, "read", map[string]any{"file_path": home + `\notes.md`}); res.Refused || !strings.Contains(res.Output, "a note of the home") {
		t.Fatalf("the home is open: %+v", res)
	}
	t.Run("control: the kept data folder by its own path", func(t *testing.T) { doors(t, data, data+`\identity.sec`, true) })

	jdata, jconf, jsib, jout, jdeep, jlater := home+`\jdata`, home+`\jconf`, home+`\jsib`, home+`\jout`, home+`\a\b`, home+`\jlater`
	makeJunction(t, jdata, data)
	makeJunction(t, jconf, confDir)
	makeJunction(t, jsib, sibling)
	makeJunction(t, jout, outside)
	makeJunction(t, jdeep, data)
	makeJunction(t, jlater, data+`\later`) // a kept place not made yet

	for _, off := range []bool{false, true} {
		r.SetSandboxOff(off)
		mode := map[bool]string{false: "sandbox on", true: "sandbox off"}[off]
		t.Run(mode, func(t *testing.T) {
			t.Run("a junction in the home to the kept data folder", func(t *testing.T) { doors(t, jdata, jdata+`\identity.sec`, true) })
			t.Run("a junction in the home to the kept data folder, a folder inside it", func(t *testing.T) {
				doors(t, jdata+`\credentials`, jdata+`\credentials\anthropic.json`, true)
			})
			t.Run("a junction in the home to the folder of a kept file", func(t *testing.T) { doors(t, jconf, jconf+`\profile-secret.json`, false) })
			t.Run("a junction in the home to another identity's slot", func(t *testing.T) { doors(t, jsib+`\data`, jsib+`\data\identity.sec`, true) })
			t.Run("a junction deeper in the home, home\\a\\b, to the kept data folder", func(t *testing.T) { doors(t, jdeep, jdeep+`\identity.sec`, true) })
			t.Run("a junction to a kept place not made yet", func(t *testing.T) {
				res := call(t, "write", map[string]any{"file_path": jlater + `\forged.json`, "content": "SPRAY"})
				if !res.Refused {
					t.Errorf("NOT REFUSED: a write through a junction to a kept place not made yet: %+v", res)
				}
				if _, err := os.Stat(data + `\later\forged.json`); err == nil {
					t.Errorf("the kept place was made through the junction")
				}
			})
			if !off {
				t.Run("a junction in the home to a folder outside it", func(t *testing.T) { doors(t, jout, jout+`\notes.txt`, true) })
			}
		})
	}
	// The control of the last: with the sandbox off a folder that is nobody's
	// place is open, through the junction as by its own path.
	r.SetSandboxOff(true)
	if res := call(t, "read", map[string]any{"file_path": jout + `\notes.txt`}); res.Refused || !strings.Contains(res.Output, "NOT THE HOME") {
		t.Errorf("with the sandbox off a folder that is nobody's place is not open through a junction: %+v", res)
	}
}
