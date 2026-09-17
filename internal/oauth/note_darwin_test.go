//go:build darwin

package oauth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// .
// .
// .
// .
// .
// .
// .
func TestAKeychainMissIsRememberedLikeAHit(t *testing.T) {
	const service = "test-keychain-item"
	old := keychainLookup
	t.Cleanup(func() { keychainLookup = old; forgetAdopted(service) })
	forgetAdopted(service)
	asked := 0
	var answer []byte
	keychainLookup = func(ctx context.Context, _ string) ([]byte, error) {
		asked++
		if answer != nil {
			return answer, nil
		}
		return nil, errors.New("the specified item could not be found in the keychain")
	}
	absent := filepath.Join(t.TempDir(), "absent.json")

	if _, err := adoptedBytes(service, absent); !os.IsNotExist(err) || asked != 1 {
		t.Fatalf("the first miss: err=%v asked=%d", err, asked)
	}
	// .
	// .
	for i := 0; i < 5; i++ {
		if _, err := adoptedBytes(service, absent); !os.IsNotExist(err) {
			t.Fatalf("a remembered miss changed its answer: %v", err)
		}
	}
	if asked != 1 {
		t.Fatalf("a remembered miss asked the Keychain again: %d asks", asked)
	}
	// .
	forgetAdopted(service)
	_, _ = adoptedBytes(service, absent)
	if asked != 2 {
		t.Fatalf("after forgetting, the Keychain was not asked: %d asks", asked)
	}
	// .
	keychainMu.Lock()
	e := keychainKept[service]
	e.at = time.Now().Add(-2 * keychainFresh)
	keychainKept[service] = e
	keychainMu.Unlock()
	answer = []byte(`{"access_token":"x"}`)
	got, err := adoptedBytes(service, absent)
	if err != nil || string(got) != `{"access_token":"x"}` || asked != 3 {
		t.Fatalf("after the minute, the hit did not replace the miss: got=%q err=%v asked=%d", got, err, asked)
	}
	if _, err := adoptedBytes(service, absent); err != nil || asked != 3 {
		t.Fatalf("a fresh hit asked again: err=%v asked=%d", err, asked)
	}
	// .
	present := filepath.Join(t.TempDir(), "present.json")
	if err := os.WriteFile(present, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := adoptedBytes(service, present); err != nil || string(got) != "{}" || asked != 3 {
		t.Fatalf("a present file did not come first: got=%q err=%v asked=%d", got, err, asked)
	}
}
