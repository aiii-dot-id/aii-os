package app

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/continuity"
	"github.com/aiii-dot-id/aii-os/internal/continuity/continuitytest"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/crypto/devicekeytest"
)

func TestOnAPhoneTheEscrowHoldsTheKeyAndPutsItBackWrapped(t *testing.T) {
	r := newRestoreRig(t)
	plain, err := os.ReadFile(r.keyPath)
	if err != nil {
		t.Fatal(err)
	}
	seed := plain[4:36]
	ks := devicekeytest.New()
	crypto.SetDeviceKey(ks)
	t.Cleanup(func() { crypto.SetDeviceKey(nil) })

	_, snap := r.lived(t, 2, "kept", true)
	if raw, _ := os.ReadFile(r.keyPath); bytes.Contains(raw, seed) || ks.Wraps() != 1 {
		t.Fatalf("THE KEY WAS NOT WRAPPED AT THE FIRST START WITH A KEYSTORE (%d wraps)", ks.Wraps())
	}
	a := r.boot(t)
	file, _, err := continuityDoor(a).EscrowCreate([]byte(pagePass))
	stopApp(t, a)
	if err != nil {
		t.Fatalf("AN ESCROW CANNOT BE MADE ON A PHONE: %v", err)
	}
	held, identity, err := continuitytest.EscrowedKey(file, []byte(pagePass))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(held, plain) {
		t.Fatal("THE ESCROW HOLDS THE KEY AS THIS PHONE WRAPS IT, which opens on no other machine")
	}

	for _, gone := range []string{r.ledgerPath, r.keyPath} {
		if err := os.Remove(gone); err != nil {
			t.Fatal(err)
		}
	}
	safe, err := r.bootThroughChoice(t)
	if err != nil {
		t.Fatalf("the start of a phone that lost its record and its key: %v", err)
	}
	if err := continuityDoor(safe).KeyBack(file, []byte(pagePass)); err != nil {
		stopApp(t, safe)
		t.Fatalf("THE ESCROW DID NOT PUT THE KEY BACK ON A PHONE: %v", err)
	}
	stopApp(t, safe)
	raw, err := os.ReadFile(r.keyPath)
	if err != nil || bytes.Contains(raw, seed) {
		t.Fatalf("THE KEY AN ESCROW PUT BACK ON A PHONE LIES IN THE CLEAR (%v)", err)
	}
	if back, err := crypto.LoadKeyPair(r.keyPath); err != nil || back.Fingerprint() != identity {
		t.Fatalf("the key put back is not the identity's: %v", err)
	}
	// The start after it reads the key through the keystore and is the
	// restore door, as on any machine.
	page, err := r.bootThroughChoice(t)
	if err != nil {
		t.Fatalf("the start with the key back: %v", err)
	}
	defer stopApp(t, page)
	if reason, _ := page.SafeMode(); !strings.HasPrefix(reason, safePostures[safeRecordMissing].what) {
		t.Fatalf("with the key back the start is not at the restore door: %s", reason)
	}
	if plan, err := continuityDoor(page).RestorePlan(continuity.SourceSnapshot, snap); err != nil || plan.ConfirmText == "" {
		t.Fatalf("the restore door offers no restore of the snapshot: %v", err)
	}
}
