//go:build (linux && !android) || (darwin && !ios) || windows

package pluginhost

import (
	"context"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

// THE FIXTURES' OWN PARTS, WITH NO PACKAGE, NO ENGINE AND NO MODEL. What the
// journeys lean on that is the fixtures' and not the host's or the plugin's is
// tried here by itself, so that a journey that fails does not fail for it:
//
//   - the reading of the host's line for a storage call that took long;
//   - the settings answer held: the function the host calls for the values
//     waits as long as a journey asked, and a held answer is let go;
//   - THE STORE'S WRITE LOCK HELD: a store made as the journeys make theirs,
//     a second connection keeping its write lock, and the host's own call that
//     records a plugin's durable write (store.AppendPluginReceipt, the call
//     broker.resultReply makes before it answers). With nothing written the
//     holder must see no one waiting; each of two writes must wait about the
//     named time and then succeed; a record given to the runner meanwhile
//     must be kept and written when the lock is let go; and every record
//     must be in the store.
//
// It starts no child and reads no package. The qualification runs it before
// its rows.
func journeySelfTest(t *testing.T) {
	// The host's line, as internal/broker/broker.go (noteSlowStorage) writes it.
	log := "2026/10/08 01:02:03 broker.budget: plugin id.aiii.voice: fs.write took 2.512s on this machine's storage (said from 250ms: plugins.runtime.slow_storage_call_ms)\n" +
		"2026/10/08 01:02:04 plugins.session: plugin id.aiii.voice: event=contained\n" +
		"2026/10/08 01:02:05 broker.budget: plugin id.aiii.voice: fs.publish took 300ms on this machine's storage (said from 250ms: plugins.runtime.slow_storage_call_ms)\n" +
		"2026/10/08 01:03:05 broker.budget: plugin id.aiii.voice: fs.write took 1m2.5s on this machine's storage (said from 250ms: plugins.runtime.slow_storage_call_ms)\n"
	calls := journeySlowStorageCalls(log)
	is := func(call journeySlowCall, operation string, seconds float64) bool {
		return call.Operation == operation && math.Abs(call.Seconds-seconds) < 1e-9
	}
	if len(calls) != 3 || !is(calls[0], "fs.write", 2.512) || !is(calls[1], "fs.publish", 0.3) || !is(calls[2], "fs.write", 62.5) {
		t.Errorf("the host's lines for slow storage calls were read as %+v", calls)
	}

	// The settings answer held.
	t.Cleanup(func() { journeyHeld.answerAfter(0); journeyStored.Store(nil); journeyHeld.taken() })
	journeyStored.Store(nil)
	journeyHeld.answerAfter(0)
	began := time.Now()
	if values := journeyStoredSettings("id.aiii.voice"); values["tts_voice"] != "alba" || time.Since(began) > 100*time.Millisecond {
		t.Errorf("with nothing held the settings function answered %v after %s", values, time.Since(began))
	}
	journeyHeld.answerAfter(400 * time.Millisecond)
	began = time.Now()
	journeyStoredSettings("id.aiii.voice")
	if waited := time.Since(began); waited < 350*time.Millisecond || waited > 2*time.Second {
		t.Errorf("an answer held for 400 ms came after %s", waited)
	}
	journeyHeld.answerAfter(30 * time.Second)
	answered := make(chan time.Duration, 1)
	began = time.Now()
	go func() { journeyStoredSettings("id.aiii.voice"); answered <- time.Since(began) }()
	time.Sleep(200 * time.Millisecond)
	journeyHeld.answerAfter(0) // lets go of the answer that is being held
	select {
	case waited := <-answered:
		if waited < 150*time.Millisecond || waited > 5*time.Second {
			t.Errorf("an answer held for 30 s and let go after 200 ms came after %s", waited)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("an answer that was let go did not come")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := journeyHeld.idle(ctx); err != nil {
		t.Errorf("after every answer came, one is still said to be held: %v", err)
	}
	if held := journeyHeld.taken(); len(held) != 2 {
		t.Errorf("two answers were held and %d are recorded: %+v", len(held), held)
	}

	// The store's write lock held.
	path := filepath.Join(t.TempDir(), "aii.db")
	st, err := store.NewForTest(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	const plugin = "id.aiii.voice"
	write := func(id, operation string) time.Duration {
		began := time.Now()
		if err := st.AppendPluginReceipt(id, plugin, operation, "", true, []byte(`{}`)); err != nil {
			t.Fatalf("the host's write of the record %s failed %s after it began: %v", id, time.Since(began), err)
		}
		return time.Since(began)
	}
	free := write("selftest-free", "fs.write")
	const hold = 1500 * time.Millisecond
	lock := journeyHoldStore(t, st, path, plugin, hold)
	var record map[string]any
	end := func() map[string]any {
		if record == nil {
			record = lock.end()
		}
		return record
	}
	t.Cleanup(func() { end() })
	taken := func(after int) {
		for deadline := time.Now().Add(10 * time.Second); lock.waited() < after || !lock.has(); time.Sleep(5 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("the second connection did not hold the store's write lock ten seconds after %d waits: %v", after, end())
			}
		}
	}
	taken(0)
	// What the holder tells a waiting write by: with the lock kept and no
	// write waiting, a read through the store comes back at once, and the
	// holder sees no one waiting.
	began = time.Now()
	if _, err := st.PluginReceipts(plugin); err != nil {
		t.Fatalf("a read through the store while its write lock was kept: %v", err)
	}
	read := time.Since(began)
	time.Sleep(400 * time.Millisecond)
	if lock.waited() != 0 || !lock.has() {
		t.Errorf("with the lock kept and nothing written, the holder says %d waits and holding=%v (a read took %s): it would let the lock go for no one", lock.waited(), lock.has(), read)
	}
	// The record of an answer given first is kept while the lock is kept.
	keptRan := make(chan struct{})
	journeyKept.do(func() { write("selftest-kept", "fs.read"); close(keptRan) })
	select {
	case <-keptRan:
		t.Error("the record of an answer given first was written while the lock was kept: it would be the first to wait for the lock")
	case <-time.After(200 * time.Millisecond):
	}
	// Two writes, one after the other: each is kept waiting about the hold.
	first := write("selftest-held-1", "fs.write")
	select {
	case <-keptRan:
	case <-time.After(5 * time.Second):
		t.Error("the kept record was not written in the five seconds after the lock was let go")
	}
	taken(1)
	second := write("selftest-held-2", "fs.publish")
	// A record still kept when the holding ends is written by its end.
	endRan := make(chan struct{})
	journeyKept.do(func() { close(endRan) })
	end()
	select {
	case <-endRan:
	default:
		t.Error("a record kept when the holding ended was not written by its end")
	}
	t.Logf("a record written with the lock free took %s; a read with the lock kept took %s; two writes with the lock kept %s for whatever waits took %s and %s; the holder: %v", free, read, hold, first, second, record)
	for i, waited := range []time.Duration{first, second} {
		if waited < hold-100*time.Millisecond {
			t.Errorf("write %d was made in %s while whatever waits for the store's write lock was to be kept waiting %s: a storeheld row would show nothing", i+1, waited, hold)
		}
		if waited > hold+2*time.Second {
			t.Errorf("write %d took %s; the lock is kept %s for what waits", i+1, waited, hold)
		}
	}
	if waits, _ := record["times_the_host_was_kept_waiting"].(int); waits != 2 {
		t.Errorf("two writes waited and the holder says it kept the host waiting %v times", record["times_the_host_was_kept_waiting"])
	}
	if kept, _ := record["records_kept_and_written_later"].(int); kept != 2 {
		t.Errorf("two records were kept and the holder says %v", record["records_kept_and_written_later"])
	}
	if errs, _ := record["errors_of_the_holder"].([]string); len(errs) != 0 {
		t.Errorf("the holder said %v", errs)
	}
	if waits, _ := record["waits"].([]journeyStoreWait); len(waits) == 2 {
		for i, wait := range waits {
			named := false
			for _, written := range wait.Records {
				named = named || (written.WaitedSeconds >= (hold-100*time.Millisecond).Seconds() && written.Operation == []string{"fs.write", "fs.publish"}[i])
			}
			if !named {
				t.Errorf("wait %d does not name the write that waited: %+v", i+1, wait)
			}
		}
	}
	receipts, err := st.PluginReceipts(plugin)
	if err != nil || len(receipts) != 4 {
		t.Errorf("four records were written and the store holds %d (%v)", len(receipts), err)
	}
}
