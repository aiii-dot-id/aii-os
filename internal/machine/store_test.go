package machine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/install"
)

func TestTheStoreIsBesideTheAccountsServiceOrInsideADevelopmentOne(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	account, err := install.MachineDir()
	if err != nil {
		t.Fatal(err)
	}
	if got, err := StoreDir(account); err != nil || got != filepath.Join(filepath.Dir(account), "plugins") {
		t.Fatalf("the account's store: %s, %v", got, err)
	}
	dev := filepath.Join(t.TempDir(), "machine")
	if got, err := StoreDir(dev); err != nil || got != filepath.Join(dev, "store") {
		t.Fatalf("a development service's store: %s, %v", got, err)
	}
}

// THE PLACE THE OPERATOR NAMED IS THE STORE, read from the service's own
// state by the one parser; a state that does not read is a refusal, never
// the default place.
func TestTheStoreIsWhereTheOperatorPlacedIt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "machine")
	if err := ensureOwnerOnlyDir(dir); err != nil {
		t.Fatal(err)
	}
	placed := filepath.Join(t.TempDir(), "plugins")
	if err := saveState(filepath.Join(dir, stateFileName), State{Store: placed}); err != nil {
		t.Fatal(err)
	}
	if got, err := StoreDir(dir); err != nil || got != placed {
		t.Fatalf("a placed store: %s, %v; want %s", got, err, placed)
	}
	if err := os.WriteFile(filepath.Join(dir, stateFileName), []byte("{torn"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := StoreDir(dir); err == nil {
		t.Fatalf("a state that does not read answered the store %s", got)
	}
}
