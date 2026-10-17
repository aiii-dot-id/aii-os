//go:build windows

// second_form_windows_test.go — the kept places under each second form
// Windows has for a path. Like shell_windows_test.go this file is vetted
// by the host gate and RUNS only on a real Windows host. Each case
// requires the refusal: a case that fails is a form of the path the wall
// does not yet recognize and must be treated as a containment gap.
package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"golang.org/x/sys/windows"
)

// secondForm is one kept file, one kept folder, or both, each written in
// a form other than the one the host names it by.
type secondForm struct {
	name string
	file string // a kept file in the second form; "" when the form has none
	dir  string // a kept folder, or a folder inside one, in the second form; "" when the form has none
	skip string // why the form cannot be made here; "" when it can
}

// A KEPT PLACE IS KEPT UNDER EACH SECOND FORM OF ITS PATH ON WINDOWS.
// Nothing of the operating system stands around the identity's own file
// tools or its shell here: the kept places rest on the wall's own
// comparison. So each door (read, grep, ls, write, edit, and the shell by
// a word, by the directory the call names and by the path after a
// Set-Location) is asked for a kept place in each form Windows opens it
// by: the short (8.3) name of a folder or a file; the two device prefixes
// in front of an ordinary path; a junction to a kept folder and one to
// the home; an alternate data stream on a kept file, and a folder under
// its own stream name; a difference of case; a trailing dot or space.
//
// The layout is the stock one, the data folder inside the home, with the
// places as a host names them (stockPlaces) and one folder place whose
// name is long enough to have a short one.
func TestAKeptPlaceIsKeptUnderEachSecondFormOfItsPathOnWindows(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := base + `\home`
	data := home + `\data`
	machine := home + `\machine-service`
	secret := data + `\credentials\anthropic.json`
	profile := home + `\conf\profile-secret.json`
	config := home + `\config.json`
	kept := map[string]string{
		secret:                  "KEPT SECRET\r\n",
		data + `\snapshot.key`:  "KEPT SECRET\r\n",
		machine + `\state.json`: "KEPT SECRET\r\n",
		profile:                 "KEPT SECRET\r\n",
		config:                  "KEPT SECRET\r\n",
	}
	restore := func(t *testing.T) {
		t.Helper()
		for f, body := range kept {
			if err := os.MkdirAll(filepath.Dir(f), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	restore(t)
	for f, body := range map[string]string{home + `\notes.md`: "a note of the home\r\n", data + `\ui\frame.html`: "the frame, which is open\r\n"} {
		if err := os.MkdirAll(filepath.Dir(f), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	policy := firewall.DefaultPolicy()
	policy.SetPlaces(append(stockPlaces(home, profile),
		&firewall.Rule{ID: "sub.machine", Kind: firewall.KindSubstrate, Path: machine, Reason: "kept", Enforced: true}))
	r := NewRegistry(home, policy, Timeouts{})

	call := func(t *testing.T, tool string, args map[string]any) Result {
		t.Helper()
		res, err := r.Execute(context.Background(), tool, args)
		if err != nil {
			t.Fatalf("%s %v: %v", tool, args, err)
		}
		return res
	}
	// opened reports a result that shows a kept file's bytes or a kept name.
	opened := func(res Result) bool {
		for _, s := range []string{"KEPT SECRET", "anthropic.json", "snapshot.key", "state.json", "credentials"} {
			if strings.Contains(res.Output, s) {
				return true
			}
		}
		return false
	}
	// doors asks every door for the form and requires each refusal. A
	// door that opened or changed a kept file is put right before the
	// next is asked, so that one finding does not hide another.
	doors := func(t *testing.T, f secondForm, fatal bool) {
		type door struct {
			name string
			tool string
			args map[string]any
		}
		var ask []door
		if f.file != "" {
			ask = append(ask,
				door{"read", "read", map[string]any{"file_path": f.file}},
				door{"grep of the file", "grep", map[string]any{"pattern": "KEPT", "path": f.file}},
				door{"write", "write", map[string]any{"file_path": f.file, "content": "SPRAY"}},
				door{"edit", "edit", map[string]any{"file_path": f.file, "old_string": "KEPT", "new_string": "SPRAY"}},
				door{"the shell, a word", "shell", map[string]any{"command": `Get-Content -LiteralPath '` + f.file + `'`}},
			)
		}
		if f.dir != "" {
			ask = append(ask,
				door{"ls", "ls", map[string]any{"path": f.dir}},
				door{"grep of the folder", "grep", map[string]any{"pattern": "KEPT", "path": f.dir}},
				door{"write of a new file in the folder", "write", map[string]any{"file_path": f.dir + `\forged.json`, "content": "SPRAY"}},
				door{"the shell, a word that is the folder", "shell", map[string]any{"command": `Get-ChildItem -Name -LiteralPath '` + f.dir + `'`}},
				door{"the shell, the directory the call names", "shell", map[string]any{"cwd": f.dir, "command": "Get-ChildItem -Name"}},
				door{"the shell, the path after a Set-Location", "shell", map[string]any{"command": `Set-Location -LiteralPath '` + f.dir + `'; Get-ChildItem -Name`}},
			)
		}
		for _, d := range ask {
			t.Run(d.name, func(t *testing.T) {
				res := call(t, d.tool, d.args)
				if !res.Refused || opened(res) {
					if fatal {
						t.Fatalf("the control is not refused, so nothing below it can be read: refused=%v code=%q error=%q output=%q", res.Refused, res.ReasonCode, res.Error, res.Output)
					}
					t.Errorf("NOT REFUSED: refused=%v code=%q error=%q output=%q", res.Refused, res.ReasonCode, res.Error, res.Output)
				} else {
					// Which door refused: the wall ("protected path"), the
					// boundary ("outside sandbox") or a path that did not resolve.
					t.Logf("refused: code=%q error=%q", res.ReasonCode, res.Error)
				}
				for file, body := range kept {
					if b, err := os.ReadFile(file); err != nil || string(b) != body {
						t.Errorf("%s was changed: %q %v", file, b, err)
					}
				}
				for _, made := range []string{data + `\forged.json`, data + `\credentials\forged.json`, machine + `\forged.json`} {
					if _, err := os.Stat(made); err == nil {
						t.Errorf("%s was made in a kept folder", made)
						os.Remove(made)
					}
				}
				restore(t)
			})
		}
	}

	// The controls: by the host's own form every door refuses, and the
	// home is open.
	if res := call(t, "read", map[string]any{"file_path": home + `\notes.md`}); res.Refused || !strings.Contains(res.Output, "a note of the home") {
		t.Fatalf("the home is open: %+v", res)
	}
	if res := call(t, "read", map[string]any{"file_path": data + `\ui\frame.html`}); res.Refused || !strings.Contains(res.Output, "the frame, which is open") {
		t.Fatalf("what the data folder leaves open is open: %+v", res)
	}
	for _, f := range []secondForm{
		{name: "control: a file in the kept folder, and the folder", file: secret, dir: data},
		{name: "control: a kept file", file: profile},
		{name: "control: a folder place", file: machine + `\state.json`, dir: machine},
		{name: "control: config.json", file: config},
	} {
		ok := t.Run(f.name, func(t *testing.T) { doors(t, f, true) })
		if !ok {
			t.Fatal("a control failed: the forms below were not asked")
		}
	}

	// short is path with everything below the home in its short (8.3)
	// names, or "" with the reason when no name below the home has one.
	short := func(path string) (string, string) {
		name := func(long string) (string, error) {
			from, err := windows.UTF16PtrFromString(long)
			if err != nil {
				return "", err
			}
			buf := make([]uint16, 1024)
			n, err := windows.GetShortPathName(from, &buf[0], uint32(len(buf)))
			if err != nil || n == 0 || int(n) > len(buf) {
				return "", err
			}
			return windows.UTF16ToString(buf[:n]), nil
		}
		whole, err1 := name(path)
		above, err2 := name(home)
		if err1 != nil || err2 != nil || whole == "" || above == "" || !strings.HasPrefix(strings.ToLower(whole), strings.ToLower(above)) {
			return "", "the short name of " + path + " cannot be read here"
		}
		below := whole[len(above):]
		if strings.EqualFold(below, path[len(home):]) {
			return "", "no name below the home has a short (8.3) name here: they may be switched off on this volume"
		}
		return home + below, ""
	}
	// junction makes link a junction to target, which needs no privilege,
	// and gives the reason when it cannot.
	junction := func(link, target string) string {
		out, err := exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe"), "/c", "mklink", "/J", link, target).CombinedOutput()
		if err != nil {
			return "a junction cannot be made here: " + err.Error() + ": " + strings.TrimSpace(string(out))
		}
		return ""
	}
	upper := func(path string) string { return home + strings.ToUpper(path[len(home):]) }
	below := func(root, path string) string { return root + path[len(home):] }

	var forms []secondForm
	add := func(name, file, dir, skip string) {
		forms = append(forms, secondForm{name: name, file: file, dir: dir, skip: skip})
	}
	// The short (8.3) name of a folder or a file.
	shortMachine, why := short(machine)
	add("the short name of a kept folder", shortMachine+`\state.json`, shortMachine, why)
	shortSecret, why1 := short(secret)
	shortCreds, why2 := short(data + `\credentials`)
	add("the short names of a folder and a file inside a kept folder", shortSecret, shortCreds, why1+why2)
	shortProfile, why := short(profile)
	add("the short name of a kept file", shortProfile, "", why)
	// The two device prefixes in front of an ordinary path.
	for _, prefix := range []string{`\\?\`, `\\.\`} {
		add("the prefix "+prefix+" before a kept folder", prefix+secret, prefix+data, "")
		add("the prefix "+prefix+" before a folder place", prefix+machine+`\state.json`, prefix+machine, "")
		add("the prefix "+prefix+" before a kept file", prefix+profile, "", "")
		add("the prefix "+prefix+" before config.json", prefix+config, "", "")
	}
	// A junction to a kept folder, and one to the home.
	jdata := home + `\jdata`
	add("a junction in the home to the kept data folder", jdata+`\credentials\anthropic.json`, jdata, junction(jdata, data))
	jhome := base + `\jhome`
	why = junction(jhome, home)
	add("a junction beside the home to the home, the data folder below it", below(jhome, secret), below(jhome, data), why)
	add("a junction beside the home to the home, a kept file below it", below(jhome, profile), "", why)
	add("a junction beside the home to the home, config.json below it", below(jhome, config), "", why)
	// An alternate data stream on a kept file; a folder under its own
	// stream name.
	add("a stream on config.json", config+":x", "", "")
	add("the unnamed stream of config.json, named", config+"::$DATA", "", "")
	add("a stream on a kept file", profile+":x", "", "")
	add("the unnamed stream of a kept file, named", profile+"::$DATA", "", "")
	add("a stream on a file inside a kept folder", secret+":x", "", "")
	add("a kept folder under its own stream name", data+`::$INDEX_ALLOCATION\credentials\anthropic.json`, data+`::$INDEX_ALLOCATION`, "")
	// A difference of case.
	add("another case, a kept folder", upper(secret), upper(data), "")
	add("another case, a kept file", upper(profile), "", "")
	add("another case, config.json", upper(config), "", "")
	// A trailing dot or space, which Windows drops from a name.
	for _, tail := range []string{".", " "} {
		say := map[string]string{".": "a trailing dot", " ": "a trailing space"}[tail]
		add(say+" on a kept folder", data+tail+`\credentials\anthropic.json`, data+tail, "")
		add(say+" on a file inside a kept folder", secret+tail, "", "")
		add(say+" on a folder place", machine+tail+`\state.json`, machine+tail, "")
		add(say+" on a kept file", profile+tail, "", "")
		add(say+" on config.json", config+tail, "", "")
	}

	for _, f := range forms {
		t.Run(f.name, func(t *testing.T) {
			if f.skip != "" {
				t.Skip(f.skip)
			}
			t.Logf("file %q folder %q", f.file, f.dir)
			doors(t, f, false)
		})
	}
}
