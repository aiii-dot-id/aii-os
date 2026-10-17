//go:build darwin

package oauth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

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

	if _, err := adoptedBytes(t.Context(), service, absent); !os.IsNotExist(err) || asked != 1 {
		t.Fatalf("the first miss: err=%v asked=%d", err, asked)
	}
	// EVERY READ IN THE MINUTE ANSWERS FROM MEMORY — the providers push, the
	// probe, the card — and the answer stays the file's own.
	for i := 0; i < 5; i++ {
		if _, err := adoptedBytes(t.Context(), service, absent); !os.IsNotExist(err) {
			t.Fatalf("a remembered miss changed its answer: %v", err)
		}
	}
	if asked != 1 {
		t.Fatalf("a remembered miss asked the Keychain again: %d asks", asked)
	}
	// A REJECTION FORGETS IT, as it forgets a hit.
	forgetAdopted(service)
	_, _ = adoptedBytes(t.Context(), service, absent)
	if asked != 2 {
		t.Fatalf("after forgetting, the Keychain was not asked: %d asks", asked)
	}
	// THE MINUTE PASSES: asked again, and a hit replaces the miss.
	keychainMu.Lock()
	e := keychainKept[service]
	e.at = time.Now().Add(-2 * keychainFresh)
	keychainKept[service] = e
	keychainMu.Unlock()
	answer = []byte(`{"access_token":"x"}`)
	got, err := adoptedBytes(t.Context(), service, absent)
	if err != nil || string(got) != `{"access_token":"x"}` || asked != 3 {
		t.Fatalf("after the minute, the hit did not replace the miss: got=%q err=%v asked=%d", got, err, asked)
	}
	if _, err := adoptedBytes(t.Context(), service, absent); err != nil || asked != 3 {
		t.Fatalf("a fresh hit asked again: err=%v asked=%d", err, asked)
	}
	// A FILE THAT APPEARS IS READ FIRST and asks nothing.
	present := filepath.Join(t.TempDir(), "present.json")
	if err := os.WriteFile(present, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := adoptedBytes(t.Context(), service, present); err != nil || string(got) != "{}" || asked != 3 {
		t.Fatalf("a present file did not come first: got=%q err=%v asked=%d", got, err, asked)
	}
}

// ONE LOOKUP SERVES EVERY READER. With the file absent, every request and
// every providers push reads the credential at once, and each read that ran
// its own system tool could raise its own approval dialog. The first reader
// asks; the rest wait for its answer.
func TestOneKeychainLookupServesEveryReader(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const service = "test-keychain-single-flight"
		old := keychainLookup
		t.Cleanup(func() { keychainLookup = old; forgetAdopted(service) })
		forgetAdopted(service)
		release := make(chan struct{})
		var asked atomic.Int32
		keychainLookup = func(context.Context, string) ([]byte, error) {
			asked.Add(1)
			<-release
			return []byte(`{"access_token":"x"}`), nil
		}
		absent := filepath.Join(t.TempDir(), "absent.json")
		const readers = 8
		got := make([]string, readers)
		var wg sync.WaitGroup
		for i := range readers {
			wg.Go(func() {
				b, err := adoptedBytes(t.Context(), service, absent)
				if err != nil {
					t.Errorf("reader %d: %v", i, err)
				}
				got[i] = string(b)
			})
		}
		synctest.Wait() // one reader in the lookup, every other one waiting
		close(release)
		wg.Wait()
		if n := asked.Load(); n != 1 {
			t.Fatalf("%d concurrent readers ran the system tool %d times, want once", readers, n)
		}
		for i, g := range got {
			if g != `{"access_token":"x"}` {
				t.Fatalf("reader %d got %q", i, g)
			}
		}
	})
}

// A REQUEST THAT ENDS STOPS WAITING; THE LOOKUP DOES NOT END WITH IT. A
// waiter whose request was cancelled returns at once. The reader that
// started the lookup may be cancelled too, and the lookup still finishes
// under its own bound and is remembered as what it found — had it died
// with the request, its error would have been remembered for a minute as
// a miss, "not signed in", for every reader.
func TestACancelledReaderNeitherEndsNorPoisonsTheKeychainLookup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const service = "test-keychain-cancelled"
		old := keychainLookup
		t.Cleanup(func() { keychainLookup = old; forgetAdopted(service) })
		forgetAdopted(service)
		release := make(chan struct{})
		asked := 0
		keychainLookup = func(ctx context.Context, _ string) ([]byte, error) {
			asked++
			if _, bounded := ctx.Deadline(); !bounded {
				return nil, errors.New("the lookup has no bound of its own")
			}
			<-release
			if err := ctx.Err(); err != nil {
				return nil, err // the lookup died with a request
			}
			return []byte(`{"access_token":"x"}`), nil
		}
		absent := filepath.Join(t.TempDir(), "absent.json")

		ownerCtx, cancelOwner := context.WithCancel(t.Context())
		owner := make(chan error, 1)
		go func() { _, err := adoptedBytes(ownerCtx, service, absent); owner <- err }()
		synctest.Wait()
		waiterCtx, cancelWaiter := context.WithCancel(t.Context())
		waiter := make(chan error, 1)
		go func() { _, err := adoptedBytes(waiterCtx, service, absent); waiter <- err }()
		synctest.Wait()

		cancelWaiter()
		if err := <-waiter; !errors.Is(err, context.Canceled) {
			t.Fatalf("a cancelled waiter: %v, want context.Canceled while the lookup runs", err)
		}
		cancelOwner()
		close(release)
		if err := <-owner; err != nil {
			t.Fatalf("the reader that started the lookup: %v, want the answer it found", err)
		}
		got, err := adoptedBytes(t.Context(), service, absent)
		if err != nil || string(got) != `{"access_token":"x"}` || asked != 1 {
			t.Fatalf("after the cancellations: got=%q err=%v asks=%d, want the remembered hit and one ask", got, err, asked)
		}
	})
}

func TestASourceReadsWithoutItsLockAndNeverGoesBackwards(t *testing.T) {
	const service = "test-keychain-source"
	old := keychainLookup
	t.Cleanup(func() { keychainLookup = old; forgetAdopted(service) })
	forgetAdopted(service)
	asked := make(chan chan []byte, 4)
	keychainLookup = func(ctx context.Context, _ string) ([]byte, error) {
		answer := make(chan []byte)
		asked <- answer
		select {
		case b := <-answer:
			return b, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	token := func(s string) []byte { return []byte(`{"access_token":"` + s + `"}`) }
	absent := filepath.Join(t.TempDir(), "absent.json")

	opened := make(chan *Source, 1)
	go func() {
		src, err := New(KindFilePrefix+absent, map[string]string{"keychain_service": service})
		if err != nil {
			t.Error(err)
		}
		opened <- src
	}()
	(<-asked) <- token("opened")
	src := <-opened
	if src == nil {
		t.FailNow()
	}
	gen := src.Generation()

	type result struct {
		cred Credential
		err  error
	}
	forgetAdopted(service)
	a := make(chan result, 1)
	go func() { c, err := src.Credential(t.Context()); a <- result{c, err} }()
	older := <-asked

	forgetAdopted(service)
	b := make(chan result, 1)
	go func() { c, err := src.Credential(t.Context()); b <- result{c, err} }()
	select {
	case newer := <-asked:
		newer <- token("newer")
	case <-time.After(10 * time.Second):
		t.Fatal("a second reader could not read while the first waited on the Keychain: the source's lock is held across the read")
	}
	if r := <-b; r.err != nil || r.cred.Token != "newer" {
		t.Fatalf("reader B: %q %v, want the newer credential", r.cred.Token, r.err)
	}

	older <- token("older")
	if r := <-a; r.err != nil || r.cred.Token != "newer" {
		t.Fatalf("reader A: %q %v — its older read was adopted over the newer one", r.cred.Token, r.err)
	}
	if got := src.Generation(); got != gen+1 {
		t.Fatalf("generation %d, want %d: one change of bytes is one generation", got, gen+1)
	}
}
