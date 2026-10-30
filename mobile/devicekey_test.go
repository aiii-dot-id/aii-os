package mobile

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/crypto/devicekeytest"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
)

func newTestKeystore() *devicekeytest.Keystore { return devicekeytest.New() }

// phoneWithAnIdentity is a phone's home as every install before this
// change holds it: an identity born there, its key in the clear at
// data/identity.sec. It returns the config path and the key file's bytes.
func phoneWithAnIdentity(t *testing.T) (home, config string, plain []byte) {
	t.Helper()
	crypto.SetDeviceKey(nil)
	t.Cleanup(func() { crypto.SetDeviceKey(nil) })
	home = t.TempDir()
	t.Chdir(home)
	data := filepath.Join(home, "data")
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	root := genesistest.NewRoot(t)
	res := root.Birth(t, genesis.BirthConfig{
		Name: "Phone", Ring0Bundle: root.Ring0Bundle(t), Root: root.Env,
		KeyPath:    filepath.Join(data, "identity.sec"),
		LedgerPath: filepath.Join(data, "ledger.jsonl"),
		DBPath:     filepath.Join(data, "aii.db"),
	})
	res.Ledger.Close()
	plain, err := os.ReadFile(filepath.Join(data, "identity.sec"))
	if err != nil {
		t.Fatal(err)
	}
	config = filepath.Join(home, "config.json")
	raw := fmt.Sprintf(`{"dashboard":{"host":"127.0.0.1","port":0,"tls":false},"genesis":{"server_url":"http://127.0.0.1:1","firewall_url":%q,"bootstrap_url":"http://127.0.0.1:1"},"certificate":{"server_url":"none"},"llm":{"provider":"test","model":"m"}}`, genesistest.Ring5Server(t))
	if err := os.WriteFile(config, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	// A model that is named and never reached: the start needs one named.
	providers := `{"providers":[{"name":"test","api_type":"openai","url":"https://127.0.0.1:1","api_key":"sk-x","default_model":"m","default":true}]}`
	if err := os.WriteFile(filepath.Join(home, "providers.json"), []byte(providers), 0o600); err != nil {
		t.Fatal(err)
	}
	return home, config, plain
}

func TestAPhonesKeyInTheClearIsWrappedByItsKeystoreAtTheFirstStart(t *testing.T) {
	home, config, plain := phoneWithAnIdentity(t)
	ks := newTestKeystore()
	rt, err := Start(config, home, ks)
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(home, "data", "identity.sec")
	raw, err := os.ReadFile(keyPath)
	if err != nil || bytes.Contains(raw, plain[4:36]) {
		stopRuntime(t, rt)
		t.Fatalf("THE IDENTITY'S KEY IS IN THE CLEAR ON THE PHONE AFTER A START WITH ITS KEYSTORE (%v)", err)
	}
	if why, safe := rt.a.SafeMode(); safe {
		stopRuntime(t, rt)
		t.Fatalf("the identity does not run on its wrapped key: SAFE %s", why)
	}
	if ks.Wraps() != 1 {
		t.Errorf("the key was wrapped %d times", ks.Wraps())
	}
	entries, _ := os.ReadDir(filepath.Dir(keyPath))
	for _, e := range entries {
		if strings.Contains(e.Name(), "identity.sec") && e.Name() != "identity.sec" {
			t.Errorf("the wrap left %s beside the key", e.Name())
		}
	}
	next, err := rt.Restart()
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	defer stopRuntime(t, next)
	if ks.Wraps() != 1 || ks.Unwraps() < 2 {
		t.Fatalf("the restart did not read the wrapped key through the keystore, or wrapped it again: %d wraps, %d unwraps", ks.Wraps(), ks.Unwraps())
	}
	if again, _ := os.ReadFile(keyPath); !bytes.Equal(again, raw) {
		t.Fatal("the restart rewrote the wrapped key")
	}
}

// A KEYSTORE THAT IS NOT THERE, OR WILL NOT ANSWER, STARTS NOTHING. With no
// keystore the start is refused before anything is read. A keystore that
// refuses (a locked iPhone) ends the start with a typed reason and leaves
// the key as it was: in the clear before the first wrap, never used so;
// wrapped after it, and not a SAFE page in place of the identity. The start
// the shell makes again once the keystore answers runs.
func TestAPhoneStartsNothingWithoutAKeystoreThatAnswers(t *testing.T) {
	home, config, plain := phoneWithAnIdentity(t)
	if _, err := Start(config, home, nil); !errors.Is(err, errNoDeviceKey) {
		t.Fatalf("a start with no keystore: %v", err)
	}
	keyPath := filepath.Join(home, "data", "identity.sec")
	ks := newTestKeystore()
	ks.Refuse(true)
	refused := func(when string) {
		t.Helper()
		rt, err := Start(config, home, ks)
		var custody *crypto.KeyCustodyError
		if err == nil {
			stopRuntime(t, rt)
		}
		if !errors.As(err, &custody) || custody.Reason != crypto.KeystoreFailed || !errors.Is(err, devicekeytest.ErrLocked) {
			t.Fatalf("%s: a keystore that refuses did not end the start with its reason: %v", when, err)
		}
	}
	refused("before the first wrap")
	if raw, _ := os.ReadFile(keyPath); !bytes.Equal(raw, plain) {
		t.Fatal("a keystore that refused changed the key")
	}
	ks.Refuse(false)
	rt, err := Start(config, home, ks)
	if err != nil {
		t.Fatal(err)
	}
	stopRuntime(t, rt)
	ks.Refuse(true)
	refused("after it")
	ks.Refuse(false)
	rt, err = Start(config, home, ks)
	if err != nil {
		t.Fatal(err)
	}
	defer stopRuntime(t, rt)
	if why, safe := rt.a.SafeMode(); safe {
		t.Fatalf("the start after the keystore answered again is SAFE: %s", why)
	}
}
