package install

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/fileperm"
)

func TestSlotsAndPortsAreUniformFromZero(t *testing.T) {
	root := t.TempDir()
	for want := 0; want < 3; want++ {
		n, err := NextSlot(root)
		if err != nil {
			t.Fatalf("NextSlot: %v", err)
		}
		if n != want {
			t.Fatalf("NextSlot = %d, want %d", n, want)
		}
		dir, err := Create(root, n)
		if err != nil {
			t.Fatalf("Create(%d): %v", n, err)
		}
		if got := filepath.Base(dir); got != SlotName(want) {
			t.Fatalf("slot dir = %q, want %q", got, SlotName(want))
		}
		if got := Port(n); got != 8180+want {
			t.Fatalf("port for slot %d = %d, want %d", n, got, 8180+want)
		}
	}
}

// A removed slot's number is reused, so ports do not drift upward
// forever as an operator adds and removes identities.
func TestNextSlotFillsGaps(t *testing.T) {
	root := t.TempDir()
	for n := 0; n < 3; n++ {
		if _, err := Create(root, n); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.RemoveAll(filepath.Join(root, SlotName(1))); err != nil {
		t.Fatal(err)
	}
	n, err := NextSlot(root)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("NextSlot after removing slot 1 = %d, want 1", n)
	}
}

func TestCreateRefusesAnExistingDirectory(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, SlotName(0))
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	ledger := filepath.Join(dir, "data", "ledger.jsonl")
	if err := os.WriteFile(ledger, []byte(`{"seq":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(root, 0); !errors.Is(err, errSlotHeld) {
		t.Fatalf("Create must refuse an existing directory as a name already held, got: %v", err)
	}
	if _, err := os.Stat(ledger); err != nil {
		t.Fatalf("refusing to adopt must not disturb what is there: %v", err)
	}
}

// ONE CREATE MAKES THE SLOT AND EVERY OTHER HEARS THE REFUSAL. Two that
// look at once both find the name free; the one whose mkdir comes second
// is told by the system that the name exists, and that is the refusal
// Create gives for a name it found held — never the system's own words,
// which differ by platform and name no reason.
func TestCreatesOfOneSlotAtOnceMakeItOnceAndRefuseTheRest(t *testing.T) {
	root := t.TempDir()
	errs := make([]error, 8)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, errs[i] = Create(root, 0)
		}()
	}
	close(start)
	wg.Wait()
	made := 0
	for _, err := range errs {
		if err == nil {
			made++
		} else if !errors.Is(err, errSlotHeld) {
			t.Fatalf("a Create that lost the name must refuse as for a name already held, got: %v", err)
		}
	}
	if made != 1 {
		t.Fatalf("%d of %d Creates made %s; want exactly one", made, len(errs), SlotName(0))
	}
	if _, err := os.Stat(ConfigPathIn(filepath.Join(root, SlotName(0)))); err != nil {
		t.Fatalf("the slot that was made must be whole, the refusals having touched nothing: %v", err)
	}
}

func TestASlotIsMadeClosedToEveryoneButItsOwner(t *testing.T) {
	dir, err := Create(t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := fileperm.IsOwnerOnlyDir(dir); err != nil || !ok {
		t.Fatalf("fileperm.IsOwnerOnlyDir(%s) = %v, %v; want true — the slot is open to other accounts", dir, ok, err)
	}
}

// A slot without a config is not a slot: a half-made world must not be
// left behind for NextSlot to count as taken.
func TestCreateWritesTheSlotPort(t *testing.T) {
	root := t.TempDir()
	dir, err := Create(root, 2)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(ConfigPathIn(dir))
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Dashboard struct {
			Port int `json:"port"`
		} `json:"dashboard"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("slot config is not valid JSON: %v", err)
	}
	if cfg.Dashboard.Port != Port(2) {
		t.Fatalf("slot config port = %d, want %d", cfg.Dashboard.Port, Port(2))
	}
}

// by-name is a projection of the ledger, so it must survive being
// wrong: relinking a name onto a different slot replaces it rather
// than failing, exactly as rebuilding the SQLite projection would.
func TestLinkNameIsRebuildable(t *testing.T) {
	root := t.TempDir()
	for n := 0; n < 2; n++ {
		if _, err := Create(root, n); err != nil {
			t.Fatal(err)
		}
	}
	if err := LinkName(root, "TestPeer", 0); err != nil {
		t.Fatal(err)
	}
	if err := LinkName(root, "TestPeer", 0); err != nil {
		t.Fatalf("relinking the same name must be idempotent: %v", err)
	}
	if err := LinkName(root, "TestPeer", 1); err != nil {
		t.Fatalf("moving a name to another slot must succeed: %v", err)
	}
	got, err := os.Readlink(filepath.Join(root, ByName, "TestPeer"))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("..", SlotName(1)); got != want {
		t.Fatalf("link = %q, want %q", got, want)
	}
}

// The template instance is the slot name. systemd escapes a path into
// an instance name, so passing a directory silently produces a unit
// nobody can start.
func TestStartCommandUsesTheSlotNotThePath(t *testing.T) {
	cmd := StartCommand(SlotName(0), "/home/someone/.aii/identity-0")
	if cmd == "" {
		t.Fatal("StartCommand is empty")
	}
	if got := "/home/someone"; contains(cmd, got) && contains(cmd, ".service") {
		t.Fatalf("a systemd instance must not carry a raw path: %q", cmd)
	}
}

func contains(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

// A name is the identity's own choosing and was never validated as a
// path. Joined into root/by-name it must STAY there: with "../sentinel"
// the old code resolved to root/sentinel, removed it, and left a
// dangling symlink where the operator's file had been.
func TestLinkNameNeverEscapesByName(t *testing.T) {
	root := t.TempDir()
	sentinel := filepath.Join(root, "sentinel")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"../sentinel", "../../sentinel", "a/b", ".", "..", "/sentinel", `..\sentinel`} {
		if err := LinkName(root, bad, 0); err == nil {
			t.Fatalf("LinkName accepted %q as a by-name link", bad)
		}
	}
	fi, err := os.Lstat(sentinel)
	if err != nil {
		t.Fatalf("root/sentinel is gone: %v", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Fatal("root/sentinel was replaced by a symlink")
	}
	if b, _ := os.ReadFile(sentinel); string(b) != "keep" {
		t.Fatalf("root/sentinel was rewritten: %q", b)
	}
}
