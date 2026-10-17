//go:build (linux && !android) || (darwin && !ios) || windows

package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

// The actual operator retry door must wake material now that status polls do
// not. Facility validation remains first: an unregistered acquisition is not
// authority to retry a plugin. The controller need not launch a child here.
func TestTryAgainWakesOnlyTheFacilitysKnownAcquisition(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var knownCalls, unknownCalls atomic.Int64
		var recovered atomic.Bool
		const known, unknown = "org.example.known", "org.example.unknown"
		// Each plugin its own bytes: two blobs, two fetches.
		data := map[string][]byte{"https://example.invalid/" + known: []byte("verified model " + known), "https://example.invalid/" + unknown: []byte("verified model " + unknown)}
		store := newTestStore(t)
		q := store.acquirer(func(_ context.Context, url string, offset int64, out io.Writer) (int64, error) {
			if url == "https://example.invalid/"+known {
				knownCalls.Add(1)
			} else {
				unknownCalls.Add(1)
			}
			if !recovered.Load() {
				return 0, errors.New("offline")
			}
			n, err := out.Write(data[url][offset:])
			return int64(n), err
		}, pluginhost.AcquirerConfig{
			After: func(time.Duration) <-chan time.Time { return nil },
			Logf:  func(string, ...any) {},
		})
		q.Attach(ctx)
		for _, id := range []string{known, unknown} {
			url := "https://example.invalid/" + id
			sum := sha256.Sum256(data[url])
			q.Want(pluginhost.ModelMaterialForTest(store.w.Dir, id, "", pluginhost.ModelDecl{
				Name: id, Size: int64(len(data[url])), SHA256: hex.EncodeToString(sum[:]), URL: url,
			}))
		}
		synctest.Wait()
		if knownCalls.Load() != 1 || unknownCalls.Load() != 1 {
			t.Fatal("fixture did not reach the two waiting acquisitions")
		}
		f := pluginfacility.New(pluginfacility.Config{})
		defer f.Close()
		f.Observe([]pluginfacility.Observed{{ID: known, Package: "known.aiiospkg", Hash: "known"}}, pluginfacility.Policy{Revision: 1})
		a := &App{pluginOpts: &pluginhost.Options{Acquirer: q}, sweepPoke: make(chan struct{}, 1)}
		a.facilityOnce.Do(func() { a.facility = f })
		recovered.Store(true)
		if err := a.RetryPlugin(unknown); err == nil {
			t.Fatal("an acquisition bypassed the facility's unknown-id refusal")
		}
		synctest.Wait()
		if unknownCalls.Load() != 1 || knownCalls.Load() != 1 {
			t.Fatal("a refused operator action still woke acquisition")
		}
		// SAFE entry has held activation, but its requested sweep has not
		// yet retired this waiting Acquirer job. Try again may reset the
		// facility budget; it must not wake an outside-world operation.
		a.enterSafe("test: integrity unverified before the sweep")
		defer a.stopSafeBeacon()
		if err := a.RetryPlugin(known); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if knownCalls.Load() != 1 {
			t.Fatal("Try again woke a download while SAFE held, before the sweep")
		}
		a.resetModeForTest()
		f.Unhold(func() bool { return false })
		if err := a.RetryPlugin(known); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if st, _ := q.Status(known); st.Phase != pluginhost.PhaseReady || knownCalls.Load() != 2 || unknownCalls.Load() != 1 {
			t.Fatalf("Try again failed to recover only its own material: known=%d unknown=%d status=%+v", knownCalls.Load(), unknownCalls.Load(), st)
		}
		// Retrying an already-ready acquisition does not create another job.
		if err := a.RetryPlugin(known); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if knownCalls.Load() != 2 {
			t.Fatal("retry restarted material that was already ready")
		}
	})
}
