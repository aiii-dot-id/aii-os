package broker

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// hardLinkWorld is a home with the kept data directory in it, holding
// the key ("INSIDE\n"), and a plugin granted files there.
func hardLinkWorld(t *testing.T) (home, key string, h *Host, b *Binding) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(home, "data")
	key = filepath.Join(data, "identity.sec")
	for _, d := range []string{data, filepath.Join(home, "notes")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(key, []byte("INSIDE\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	policy := firewall.DefaultPolicy()
	policy.SetPlaces([]*firewall.Rule{{ID: "sub.data", Kind: firewall.KindSubstrate, Path: data, Reason: "the host keeps it", Enforced: true}})
	world := tools.NewRegistry(home, policy, tools.Timeouts{})
	h = newHost(t, newStore(t), Config{Grants: map[string]Grant{"p": {Files: true}}, Sandbox: world, MaxFilesBytes: 1 << 20})
	return home, key, h, h.Bind("p", packagefmt.TierT1, []string{"fs.sandbox"})
}

// refusedProtected says whether a reply is the gate's refusal of a kept
// place, FS_PROTECTED, and carries nothing of the key's bytes.
func refusedProtected(m map[string]json.RawMessage) (string, bool) {
	var got struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	raw, _ := json.Marshal(m)
	_ = json.Unmarshal(raw, &got)
	leaked := strings.Contains(string(raw), base64.StdEncoding.EncodeToString([]byte("INSIDE\n"))) || strings.Contains(string(raw), "INSIDE")
	return string(raw), got.Status == statusDenied && got.Reason == reasonFSProtected && !leaked
}

// A PLUGIN DOES NOT REACH A FILE INSIDE A KEPT DIRECTORY UNDER A SECOND
// NAME. A plugin granted files works in the identity's sandbox through the
// identity's own gate, which compared a second name for the key (a hard
// link in the home, one shell command away) by its letters and matched
// nothing: fs.read read the key, fs.write and fs.publish rewrote it, and
// fs.delete removed the name. Now a file with more than one name is asked
// whether one of its names lies inside a kept directory, and each answers
// FS_PROTECTED, the key unchanged. The control half: a file whose two
// names both lie in the home, and a file with one name, are read and
// written as before.
func TestAPluginDoesNotReachAFileInsideAKeptDirectoryUnderASecondName(t *testing.T) {
	home, key, _, b := hardLinkWorld(t)
	second := filepath.Join(home, "notes", "k.txt")
	if err := os.Link(key, second); err != nil {
		t.Skipf("hard links unavailable here: %v", err)
	}
	for what, params := range map[string]string{
		"fs.read":              fsParams("fs.read", "sandbox", "notes/k.txt", ""),
		"fs.write":             fsParams("fs.write", "sandbox", "notes/k.txt", `{"data":"FORGED"}`),
		"fs.write with append": fsParams("fs.write", "sandbox", "notes/k.txt", `{"data":"FORGED","append":true}`),
		"fs.publish":           fsParams("fs.publish", "sandbox", "notes/k.txt", `{"data":"FORGED"}`),
		"fs.delete":            fsParams("fs.delete", "sandbox", "notes/k.txt", ""),
	} {
		if raw, ok := refusedProtected(dispatch(t, b, params)); !ok {
			t.Errorf("%s of a second name for the key: denied, FS_PROTECTED, nothing of it carried: %s", what, raw)
		}
	}
	if got, err := os.ReadFile(key); err != nil || string(got) != "INSIDE\n" {
		t.Errorf("the key was changed through its second name: %q %v", got, err)
	}
	if _, err := os.Lstat(second); err != nil {
		t.Errorf("the second name was removed: %v", err)
	}

	pair := filepath.Join(home, "notes", "pair-a.txt")
	if err := os.WriteFile(pair, []byte("A PAIR\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(pair, filepath.Join(home, "notes", "pair-b.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "notes", "one.txt"), []byte("ONE\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"notes/pair-b.txt", "notes/one.txt"} {
		wantResult(t, dispatch(t, b, fsParams("fs.read", "sandbox", rel, "")), statusSucceeded, "")
		wantResult(t, dispatch(t, b, fsParams("fs.write", "sandbox", rel, `{"data":"WRITTEN"}`)), statusSucceeded, "")
	}
	if got, _ := os.ReadFile(pair); string(got) != "WRITTEN" {
		t.Errorf("the write reached the file through its other name: %q", got)
	}
}

// A PLUGIN'S READ ASKS THE FILE IT OPENED WHEN IT HAS MORE THAN ONE NAME.
// The gate judges a path, and fs.read opens it a moment later: a second
// name for the key put at the path in that moment (the seam stands
// another actor in for it) was read whole. Now a file with more than one
// name is asked through the handle it is read by, before a byte is read,
// as a write's file is: FS_PROTECTED, nothing of it carried. An ordinary
// file with one name is read as before, asking nothing more.
func TestAPluginReadAsksTheFileItOpenedWhenItHasMoreThanOneName(t *testing.T) {
	home, key, h, b := hardLinkWorld(t)
	plain := filepath.Join(home, "notes", "plain.txt")
	if err := os.WriteFile(plain, []byte("PLAIN\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	probe := filepath.Join(home, "notes", "probe")
	if err := os.Link(key, probe); err != nil {
		t.Skipf("hard links unavailable here: %v", err)
	}
	os.Remove(probe)
	wantResult(t, dispatch(t, b, fsParams("fs.read", "sandbox", "notes/plain.txt", "")), statusSucceeded, "")
	h.fsReadLook = func(*os.Root, string) {
		staged := filepath.Join(home, "notes", "staged")
		if err := os.Link(key, staged); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(staged, plain); err != nil {
			t.Fatal(err)
		}
	}
	if raw, ok := refusedProtected(dispatch(t, b, fsParams("fs.read", "sandbox", "notes/plain.txt", ""))); !ok {
		t.Errorf("fs.read of a second name for the key put at the path after the gate: denied, FS_PROTECTED, nothing of it carried: %s", raw)
	}
	h.fsReadLook = nil
}

func TestAPluginFileOperationWalksTheKeptDirectoriesOnce(t *testing.T) {
	home, key, _, b := hardLinkWorld(t)
	pair := filepath.Join(home, "notes", "pair-a.txt")
	if err := os.WriteFile(pair, []byte("A PAIR\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(pair, filepath.Join(home, "notes", "pair-b.txt")); err != nil {
		t.Skipf("hard links unavailable here: %v", err)
	}
	if err := os.Link(key, filepath.Join(home, "notes", "k.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "notes", "one.txt"), []byte("ONE\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		what, params, status string
		walks                int64
	}{
		{"fs.read of a file with two names", fsParams("fs.read", "sandbox", "notes/pair-b.txt", ""), statusSucceeded, 1},
		{"fs.write of a file with two names", fsParams("fs.write", "sandbox", "notes/pair-b.txt", `{"data":"WRITTEN"}`), statusSucceeded, 1},
		{"fs.read of a second name for the key", fsParams("fs.read", "sandbox", "notes/k.txt", ""), statusDenied, 1},
		{"fs.read of a file with one name", fsParams("fs.read", "sandbox", "notes/one.txt", ""), statusSucceeded, 0},
	} {
		before := firewall.WalksForTest()
		m := dispatch(t, b, c.params)
		walked := firewall.WalksForTest() - before
		var got struct {
			Status string `json:"status"`
		}
		raw, _ := json.Marshal(m)
		_ = json.Unmarshal(raw, &got)
		if got.Status != c.status {
			t.Errorf("%s: status %q, want %q: %s", c.what, got.Status, c.status, raw)
		}
		if walked != c.walks {
			t.Errorf("%s walked the kept directories %d times, want %d", c.what, walked, c.walks)
		}
	}
}

// noGate is a sandbox whose Look gives no gate.
type noGate struct{}

func (noGate) Look() tools.Gate { return nil }

func TestASandboxThatGivesNoGateRefuses(t *testing.T) {
	h := newHost(t, newStore(t), Config{Grants: map[string]Grant{"p": {Files: true}}, Sandbox: noGate{}, MaxFilesBytes: 1 << 20})
	b := h.Bind("p", packagefmt.TierT1, []string{"fs.sandbox"})
	wantResult(t, dispatch(t, b, fsParams("fs.read", "sandbox", "notes/x.txt", "")), statusDenied, reasonFSOutside)
}
