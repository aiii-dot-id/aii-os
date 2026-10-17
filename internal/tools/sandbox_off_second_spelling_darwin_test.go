//go:build darwin

package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
)

// WITH THE SANDBOX OFF, A PLACE OUTSIDE THE HOME IS KEPT UNDER THE DATA
// VOLUME'S SPELLING OF ITS PATH. The places kept only while the sandbox is
// off lie outside the home, where nothing else holds a path to them: the
// wall knows each by what it is, so a second path to it that is no link
// (macOS shows every directory of the data volume a second time under the
// volume's own mount point) is the place, in each of the three ways the
// switch says one: another identity's slot (Outer), a staged update
// beside the program and a unit's drop-in directory (Stem: known through
// the directory its entries are in, also where the stem's own path is an
// entry, as a unit is), and the logs (ReadOpen: read, and not written). And the home's own data directory, reached so from outside
// every root, is kept as it is with the sandbox on. The control half: a
// file beside them that is none of theirs is read and written under that
// spelling.
func TestWithTheSandboxOffAPlaceOutsideTheHomeIsKeptUnderTheDataVolumesSpelling(t *testing.T) {
	if dataVolume == "" {
		t.Skip("the data volume is the root here: there is no second spelling")
	}
	parent, err := filepath.EvalSymlinks("/private/var/tmp")
	if err != nil {
		t.Skipf("no such directory here: %v", err)
	}
	base, err := os.MkdirTemp(parent, "aii-off-second-spelling-")
	if err != nil {
		t.Skipf("no directory can be made in %s: %v", parent, err)
	}
	t.Cleanup(func() { os.RemoveAll(base) })
	if one, err := os.Stat(base); err != nil {
		t.Fatal(err)
	} else if two, err := os.Stat(dataVolume + base); err != nil || !os.SameFile(one, two) {
		t.Skipf("this host does not show %s a second time under %s: %v", base, dataVolume, err)
	}
	put := func(body string, parts ...string) string {
		t.Helper()
		p := filepath.Join(parts...)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	home := filepath.Join(base, "home")
	put("home MARK\n", home, "notes.txt")
	data := filepath.Join(home, "data")
	credential := put("KEPT credential\n", data, "credentials", "k.key")
	slot := filepath.Join(base, "slots", "identity-2")
	token := put("KEPT token\n", slot, "data", "credentials", "token.json")
	bin := filepath.Join(base, "bin")
	staged := put("KEPT staged\n", bin, ".aii.stage-123")
	beside := put("beside MARK\n", bin, "other-program")
	units := filepath.Join(base, "units")
	put("KEPT unit\n", units, "aii-os-machine.service")
	dropIn := put("KEPT drop-in\n", units, "aii-os-machine.service.d", "override.conf")
	logs := filepath.Join(base, "logs")
	logFile := put("log MARK\n", logs, "host.log")

	outer := func(id, path string) *firewall.Rule {
		return &firewall.Rule{ID: id, Kind: firewall.KindSubstrate, Path: path, Reason: "kept", Enforced: true, Outer: true}
	}
	stage := outer("sub.update", filepath.Join(bin, ".aii.stage-"))
	stage.Stem = true
	// A stem whose own path is an entry, as a unit is: its drop-in
	// directory is the stem's too, and is known through the directory the
	// unit is in, not only through the unit (firewall placeStarts).
	unit := outer("sub.start", filepath.Join(units, "aii-os-machine.service"))
	unit.Stem = true
	logPlace := outer("sub.logs", logs)
	logPlace.ReadOpen = true
	policy := firewall.DefaultPolicy()
	policy.SetPlaces([]*firewall.Rule{
		{ID: "sub.data", Kind: firewall.KindSubstrate, Path: data, Reason: "kept", Enforced: true},
		outer("sub.identity", slot), stage, unit, logPlace,
	})
	r := NewRegistry(home, policy, Timeouts{})
	r.SetSandboxOff(true)
	again := func(p string) string { return dataVolume + p }
	leaked := func(out string) bool {
		return strings.Contains(out, "KEPT token") || strings.Contains(out, "KEPT staged") || strings.Contains(out, "KEPT credential") || strings.Contains(out, "KEPT drop-in")
	}

	for path, id := range map[string]string{again(token): "sub.identity", again(staged): "sub.update", again(dropIn): "sub.start", again(credential): "sub.data", again(filepath.Join(slot, "data", "credentials", "new.json")): "sub.identity"} {
		for tool, args := range map[string]map[string]any{
			"read":   {"file_path": path},
			"grep":   {"pattern": "KEPT", "path": filepath.Dir(path)},
			"ls":     {"path": filepath.Dir(path)},
			"write":  {"file_path": path, "content": "forged"},
			"delete": {"file_path": path, "action": "delete"},
			"edit":   {"file_path": path, "old_string": "KEPT", "new_string": "OPEN"},
		} {
			name := tool
			if tool == "delete" {
				name = "write"
			}
			res := modelCall(t, r, name, args)
			if tool == "grep" || tool == "ls" {
				// The directory that holds a stem's entries is no place: the
				// walk counts the staged file as kept and reads none of it.
				if leaked(res.Output) || id != "sub.update" && id != "sub.start" && !strings.Contains(res.Error, id) {
					t.Errorf("%s of the directory of %s, under the data volume's spelling: %+v", tool, path, res)
				}
				continue
			}
			if !res.Refused || !strings.Contains(res.Error, id) || leaked(res.Output) {
				t.Errorf("%s of %s, under the data volume's spelling: %+v", tool, path, res)
			}
		}
		if res := shellIn(t, r, nil, "cat "+inQuotes(path)); !res.Refused || leaked(res.Output) {
			t.Errorf("the shell's word %s, under the data volume's spelling: %+v", path, res)
		}
	}
	for f, want := range map[string]string{token: "KEPT token\n", staged: "KEPT staged\n", credential: "KEPT credential\n", dropIn: "KEPT drop-in\n"} {
		if got := body(t, f); got != want {
			t.Errorf("%s was changed under the data volume's spelling: %q", f, got)
		}
	}
	if !gone(filepath.Join(slot, "data", "credentials", "new.json")) {
		t.Error("a file was made in another identity's slot under the data volume's spelling")
	}

	// The logs: read under that spelling, and not written.
	if res := modelCall(t, r, "read", map[string]any{"file_path": again(logFile)}); res.Error != "" || !strings.Contains(res.Output, "log MARK") {
		t.Errorf("a log under the data volume's spelling is read: %+v", res)
	}
	if res := write(t, r, map[string]any{"file_path": again(logFile), "content": "forged"}); !res.Refused || !strings.Contains(res.Error, "sub.logs") || body(t, logFile) != "log MARK\n" {
		t.Errorf("a log under the data volume's spelling is not written: %+v", res)
	}
	// The control: none of theirs.
	if res := write(t, r, map[string]any{"file_path": again(beside), "content": "mine"}); res.Error != "" || body(t, beside) != "mine" {
		t.Errorf("a file beside a stem's entries, under the data volume's spelling, is written: %+v", res)
	}
}
