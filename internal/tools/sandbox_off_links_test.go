package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
)

// WITH THE SANDBOX OFF, A SECOND NAME IS KNOWN IN EVERY PLACE KEPT ONLY
// THEN. A hard link is another name for one file, and the wall knows a
// file with more than one name by what it is: the kept directories are
// walked once a call and each file in them is known by its identity
// (firewall.Policy.Look). The places the sandbox keeps only while it is
// off are among them, in each of the three ways the switch says a place:
// another identity's slot, whole (Outer); a file or directory whose name
// the host begins and the system finishes, a staged update or a temporary
// extraction (Stem: its entries are walked, and a file among them is known
// by its identity); and the logs, which the reading tools may open and no
// other door may (ReadOpen: a read through a second name opens, a write is
// refused). So a link made outside every root to a credential in another
// identity's slot, to a file in an extraction, to a staged update or to a
// log is refused, or opened to reading only, as the file itself is. The
// control half: a file whose two names both lie outside every place is
// read and written.
func TestWithTheSandboxOffASecondNameIsKnownInEveryPlaceKeptOnlyThen(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
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
	elsewhere := filepath.Join(base, "elsewhere")
	link := func(from, name string) string {
		t.Helper()
		to := filepath.Join(elsewhere, name)
		if err := os.MkdirAll(elsewhere, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Link(from, to); err != nil {
			t.Skipf("hard links unavailable here: %v", err)
		}
		return to
	}
	home := filepath.Join(base, "home")
	put("home MARK\n", home, "notes.txt")
	slot := filepath.Join(base, "slots", "identity-2")
	token := put("KEPT slot token\n", slot, "data", "credentials", "token.json")
	temp := filepath.Join(base, "tmp")
	artifact := put("KEPT artifact\n", temp, "aii-plugin-x-123", "plugin.bin")
	bin := filepath.Join(base, "bin")
	staged := put("KEPT staged\n", bin, ".aii.stage-123")
	logs := filepath.Join(base, "logs")
	logFile := put("log line MARK\n", logs, "host.log")
	twin := put("twin MARK\n", elsewhere, "twin-a.txt")
	link(twin, "twin-b.txt")

	outer := func(id, path string) *firewall.Rule {
		return &firewall.Rule{ID: id, Kind: firewall.KindSubstrate, Path: path, Reason: "kept", Enforced: true, Outer: true}
	}
	extraction := outer("sub.temp", filepath.Join(temp, "aii-plugin-"))
	extraction.Stem = true
	stage := outer("sub.update", filepath.Join(bin, ".aii.stage-"))
	stage.Stem = true
	logPlace := outer("sub.logs", logs)
	logPlace.ReadOpen = true
	policy := firewall.DefaultPolicy()
	policy.SetPlaces([]*firewall.Rule{
		{ID: "sub.data", Kind: firewall.KindSubstrate, Path: filepath.Join(home, "data"), Reason: "kept", Enforced: true},
		outer("sub.identity", slot), extraction, stage, logPlace,
	})
	r := NewRegistry(home, policy, Timeouts{})
	r.SetSandboxOff(true)

	kept := map[string]string{
		link(token, "token.json"):    "sub.identity",
		link(artifact, "plugin.bin"): "sub.temp",
		link(staged, "staged.bin"):   "sub.update",
	}
	for path, id := range kept {
		for tool, args := range map[string]map[string]any{
			"read":  {"file_path": path},
			"write": {"file_path": path, "content": "forged"},
			"edit":  {"file_path": path, "old_string": "KEPT", "new_string": "OPEN"},
		} {
			res := modelCall(t, r, tool, args)
			if !res.Refused || res.ReasonCode != ReasonPathRefused || !strings.Contains(res.Error, id) || strings.Contains(res.Output, "KEPT") {
				t.Fatalf("%s of %s, a second name for a file %s keeps: %+v", tool, path, id, res)
			}
		}
		if got := body(t, path); !strings.HasPrefix(got, "KEPT") {
			t.Fatalf("%s was changed through its second name: %q", path, got)
		}
	}
	res := modelCall(t, r, "grep", map[string]any{"pattern": "KEPT", "path": elsewhere})
	for _, line := range []string{"KEPT slot token", "KEPT artifact", "KEPT staged"} {
		if strings.Contains(res.Output, line) {
			t.Fatalf("grep printed a kept file through its second name: %+v", res)
		}
	}
	if !strings.Contains(res.Output, "3 path(s) protected") {
		t.Fatalf("grep did not count the three second names as kept: %+v", res)
	}

	// The logs: a second name reads as the log does, and is no door to write.
	logLink := link(logFile, "host.log")
	if res := modelCall(t, r, "read", map[string]any{"file_path": logLink}); res.Error != "" || !strings.Contains(res.Output, "log line MARK") {
		t.Fatalf("a second name for a log is read as the log is: %+v", res)
	}
	if res := modelCall(t, r, "grep", map[string]any{"pattern": "log line", "path": logLink}); !strings.Contains(res.Output, "log line MARK") {
		t.Fatalf("grep of a second name for a log: %+v", res)
	}
	for tool, args := range map[string]map[string]any{
		"write": {"file_path": logLink, "content": "forged"},
		"edit":  {"file_path": logLink, "old_string": "log line", "new_string": "forged"},
	} {
		if res := modelCall(t, r, tool, args); !res.Refused || !strings.Contains(res.Error, "sub.logs") {
			t.Fatalf("%s of a second name for a log is refused: %+v", tool, res)
		}
	}
	if got := body(t, logFile); got != "log line MARK\n" {
		t.Fatalf("the log was changed through its second name: %q", got)
	}

	// The control: two names, both outside every place.
	if res := modelCall(t, r, "read", map[string]any{"file_path": twin}); res.Error != "" || !strings.Contains(res.Output, "twin MARK") {
		t.Fatalf("a file with two names outside every place is read: %+v", res)
	}
	if res := write(t, r, map[string]any{"file_path": twin, "content": "rewritten"}); res.Error != "" || body(t, twin) != "rewritten" {
		t.Fatalf("a file with two names outside every place is written: %+v", res)
	}
}

// A STEM'S ENTRY IS KNOWN BY WHAT IT IS AT THE HANDLE. A stem is kept by
// how its entries' names begin, and the letters are compared under simple
// case folding; APFS folds more (".aii.ﬆage-" opens ".aii.stage-"), so a
// staged update was opened to write under a spelling the gate's letters
// do not meet. The handle the write opened is asked what file it is
// (firewall.Policy.KeptAs), and a stem's entries are asked by a look at
// their directory: the write is refused and the staged file is unchanged.
// Where the volume does not fold the ligature (most Linux volumes) the
// spelling names another file, and the test says so and stops.
func TestAStemsEntryIsKnownByWhatItIsWhereTheVolumeFoldsItsName(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home, bin := filepath.Join(base, "home"), filepath.Join(base, "bin")
	for _, dir := range []string{home, bin} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	staged := filepath.Join(bin, ".aii.stage-123")
	if err := os.WriteFile(staged, []byte("KEPT staged\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	folded := filepath.Join(bin, ".aii.ﬆage-123")
	a, errA := os.Stat(staged)
	b, errB := os.Stat(folded)
	if errA != nil || errB != nil || !os.SameFile(a, b) {
		t.Skipf("this volume does not open %s as %s", filepath.Base(folded), filepath.Base(staged))
	}
	stem := &firewall.Rule{ID: "sub.update", Kind: firewall.KindSubstrate, Path: filepath.Join(bin, ".aii.stage-"), Reason: "kept", Enforced: true, Outer: true, Stem: true}
	policy := firewall.DefaultPolicy()
	policy.SetPlaces([]*firewall.Rule{stem})
	r := NewRegistry(home, policy, Timeouts{})
	r.SetSandboxOff(true)

	for tool, args := range map[string]map[string]any{
		"write": {"file_path": folded, "content": "forged"},
		"edit":  {"file_path": folded, "old_string": "KEPT", "new_string": "OPEN"},
	} {
		if res := modelCall(t, r, tool, args); !res.Refused && res.Error == "" || !strings.Contains(res.Error, "sub.update") {
			t.Fatalf("%s of a staged update under a folded spelling: %+v", tool, res)
		}
		if got := body(t, staged); got != "KEPT staged\n" {
			t.Fatalf("%s changed the staged update through a folded spelling: %q", tool, got)
		}
	}
	if res := write(t, r, map[string]any{"file_path": folded, "action": "delete"}); !strings.Contains(res.Error, "sub.update") {
		t.Fatalf("delete of a staged update under a folded spelling: %+v", res)
	}
	if _, err := os.Stat(staged); err != nil {
		t.Fatalf("the staged update was deleted through a folded spelling: %v", err)
	}
	// The control: a file beside it that is no entry of the stem.
	other := filepath.Join(bin, "other-program")
	if res := write(t, r, map[string]any{"file_path": other, "content": "mine"}); res.Error != "" || body(t, other) != "mine" {
		t.Fatalf("a file beside the stem that is none of its entries is written: %+v", res)
	}
}
