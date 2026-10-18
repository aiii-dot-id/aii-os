//go:build (linux && !android) || (darwin && !ios) || windows

package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// Virtual time and Wait make the actual Acquire/pending/job loop deterministic:
// a poll while fetching must not leave a wake buffered for the next failure.
// No wall-clock sleeps, network, or probabilistic repeated race attempts.
func TestActivationPollingPreservesAcquisitionBackoff(t *testing.T) {
	for _, fetching := range []bool{false, true} {
		t.Run(fmt.Sprint(fetching), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				fx := newModelFixture(t, "weights", 1000)
				fx.failWith = &NetworkError{Cause: errors.New("offline")}
				if fetching {
					fx.block = make(chan struct{})
				}
				w := openStore(t)
				q := storeAcquirer(w, fx.fetch, AcquirerConfig{After: neverAfter, Logf: func(string, ...any) {}})
				q.Attach(ctx)
				m := modelMaterial(w, "id.example.poll", "1", fx.decl)
				opts := &Options{Acquirer: q}
				poll := func() {
					t.Helper()
					var acquiring *AcquiringError
					if _, err := Acquire(ctx, m, opts); !errors.As(err, &acquiring) {
						t.Fatalf("pending activation changed its contract: %v", err)
					}
					synctest.Wait()
				}
				poll() // starts the one owned acquisition
				poll() // observation must not enqueue an immediate retry
				if fetching {
					close(fx.block)
					synctest.Wait()
				}
				first, _ := q.Status(m.PluginID)
				if calls, _ := fx.seen(); calls != 1 || first.Attempt != 1 || first.Phase != PhaseWaiting {
					t.Fatalf("poll woke the acquisition: calls=%d status=%+v", calls, first)
				}
				for i := 0; i < 3; i++ {
					time.Sleep(time.Minute) // virtual time, still before the declared retry
					poll()
				}
				if calls, _ := fx.seen(); calls != 1 {
					t.Fatalf("observation bypassed backoff: %d fetches", calls)
				}
				if now, _ := q.Status(m.PluginID); now.RetryAt != first.RetryAt || now.Attempt != first.Attempt {
					t.Fatalf("poll changed the retry shown on the card: %+v -> %+v", first, now)
				}
				fx.mu.Lock()
				fx.failWith = nil
				fx.mu.Unlock()
				q.Want(m) // an explicit retry still runs now, not in an hour
				synctest.Wait()
				if st, _ := q.Status(m.PluginID); st.Phase != PhaseReady || st.Attempt != 2 {
					t.Fatalf("explicit retry did not recover: %+v", st)
				}
				if _, err := Acquire(ctx, m, opts); err != nil {
					t.Fatalf("recovered material did not reach the activation: %v", err)
				}
			})
		})
	}
}

func TestActivationPollingKeepsDefaultRetryDeadlines(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		fx := newModelFixture(t, "weights", 1000)
		fx.failWith = &NetworkError{Cause: errors.New("offline")}
		w := openStore(t)
		q := storeAcquirer(w, fx.fetch, AcquirerConfig{Logf: func(string, ...any) {}})
		q.Attach(ctx)
		m := modelMaterial(w, "id.example.clock", "", fx.decl)
		opts := &Options{Acquirer: q}
		_, _ = Acquire(ctx, m, opts)
		synctest.Wait()
		for attempt := 1; attempt < MaxDownloadAttempts; attempt++ {
			st, _ := q.Status(m.PluginID)
			wait, full := time.Until(st.RetryAt), downloadWait(attempt)
			if st.Attempt != attempt || wait > full || wait < full-full/4 {
				t.Fatalf("retry schedule diverged at attempt %d: wait %s of %s: %+v", attempt, wait, full, st)
			}
			time.Sleep(wait - time.Nanosecond)
			_, _ = Acquire(ctx, m, opts)
			synctest.Wait()
			if calls, _ := fx.seen(); calls != attempt {
				t.Fatalf("poll started attempt %d before its deadline; got %d fetches", attempt+1, calls)
			}
			time.Sleep(time.Nanosecond)
			synctest.Wait()
		}
		st, _ := q.Status(m.PluginID)
		if calls, _ := fx.seen(); calls != MaxDownloadAttempts || st.Phase != PhaseWaiting || !st.RetryAt.IsZero() || !strings.Contains(st.Remedy, "Try again") {
			t.Fatalf("five attempts that brought nothing did not end the download: %d fetches, %+v", calls, st)
		}
		time.Sleep(time.Hour)
		synctest.Wait()
		var final *DownloadError
		if _, err := Acquire(ctx, m, opts); !errors.As(err, &final) || final.Attempts != MaxDownloadAttempts {
			t.Fatalf("the activation was not told the download ended: %v", err)
		}
		if calls, _ := fx.seen(); calls != MaxDownloadAttempts {
			t.Fatalf("an ended download was asked again on a clock: %d fetches", calls)
		}
	})
}

func TestAcquisitionNoticesAPlacedFileAtTryAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		fx := newModelFixture(t, "weights", 1000)
		w := openStore(t)
		q := storeAcquirer(w, nil, AcquirerConfig{Logf: func(string, ...any) {}})
		q.Attach(ctx)
		m := modelMaterial(w, "id.example.placed", "", fx.decl)
		slot := t.TempDir()
		m.adopt = Adoption{ModelDirs: []string{slot}}
		_, _ = Acquire(ctx, m, &Options{Acquirer: q})
		synctest.Wait()
		if err := os.WriteFile(filepath.Join(slot, fx.decl.Name), fx.data, 0600); err != nil {
			t.Fatal(err)
		}

		q.Retry(m.PluginID)
		synctest.Wait()
		if st, _ := q.Status(m.PluginID); st.Phase != PhaseReady {
			t.Fatalf("a valid file placed while offline was stranded: %+v", st)
		}
		if _, err := Acquire(ctx, m, &Options{Acquirer: q}); err != nil {
			t.Fatal(err)
		}
	})
}

func TestActivationPollReplacesChangedMaterialAndStopsTheOldWriter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		old := newModelFixture(t, "weights", 1000)
		old.block = make(chan struct{})
		next := newModelFixture(t, "weights", 2000)
		next.decl.URL += "/new"
		w := openStore(t)
		q := storeAcquirer(w, func(ctx context.Context, url string, offset int64, out io.Writer) (int64, error) {
			if url == old.decl.URL {
				return old.fetch(ctx, url, offset, out)
			}
			if _, cancelled := old.cancelled(); !cancelled {
				t.Error("successor reached the directory before its old writer stopped")
			}
			return next.fetch(ctx, url, offset, out)
		}, AcquirerConfig{Logf: func(string, ...any) {}})
		q.Attach(ctx)
		m := modelMaterial(w, "id.example.replaced", "", old.decl)
		opts := &Options{Acquirer: q}
		_, _ = Acquire(ctx, m, opts)
		synctest.Wait()
		m = modelMaterial(w, "id.example.replaced", "", next.decl)
		_, _ = Acquire(ctx, m, opts)
		synctest.Wait()
		if st, _ := q.Status(m.PluginID); st.Phase != PhaseReady || st.BytesTotal != 2000 {
			t.Fatalf("changed material stranded behind its predecessor: %+v", st)
		}
		if _, err := Acquire(ctx, m, opts); err != nil {
			t.Fatalf("changed material did not reach activation: %v", err)
		}
		if count, _ := old.seen(); count != 1 {
			t.Fatalf("old declaration fetched %d times", count)
		}
		if count, _ := next.seen(); count != 1 {
			t.Fatalf("new declaration fetched %d times", count)
		}
	})
}

func TestAcquirerRetryLeavesAbsentReadyAndRetiringJobsAlone(t *testing.T) {
	// Unattached jobs are owned synchronously: inspect the retry boundary
	// directly, without launching an irrelevant download for these states.
	q := NewAcquirer(AcquirerConfig{})
	q.Retry("absent")
	if len(q.jobs) != 0 {
		t.Fatal("retry invented an acquisition")
	}
	m := Material{PluginID: "id.example.settled"}
	q.Want(m)
	job := q.jobs[m.PluginID]
	job.phase = PhaseReady
	q.Retry(m.PluginID)
	if q.jobs[m.PluginID] != job {
		t.Fatal("retry replaced material that was already ready")
	}
	job.phase, job.stopping = PhaseWaiting, true
	q.Retry(m.PluginID)
	if q.jobs[m.PluginID] != job || len(job.wake) != 0 {
		t.Fatal("retry revived a retiring acquisition")
	}
}
