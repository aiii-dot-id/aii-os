//go:build (linux && !android) || (darwin && !ios) || windows

package pluginhost

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
)

// Real signed package staging, acquisition and facility refusal/recovery.
// The adapter passes already-verified stages and observes acquisition like the
// App. Child launch is substituted: nothing executes here.
func TestSignedRuntimeContradictionReachesFacilityAndCorrectedPackageServes(t *testing.T) {
	skipWhereNativeIsRefused(t)
	roots, signer := platformRootsForTest(t)
	fx := newRuntimeFixture(t)
	const id = "org.example.runtime-refusal"
	store := openStore(t)
	q := storeAcquirer(store, fx.fetch, AcquirerConfig{Logf: func(string, ...any) {}})
	opts := supervisedOpts(Options{Roots: roots, Acquirer: q, StoreDir: store.Dir})
	goodPackage := nativePackage(t, id, []byte("not executed"), signer, runtimesJSON(t, fx))
	fx.decl.Files++
	badPackage := nativePackage(t, id, []byte("not executed"), signer, runtimesJSON(t, fx))
	stages := map[string]*Staged{}
	// Trust verification uses real time, outside the virtual retry clock.
	for _, path := range []string{badPackage, goodPackage} {
		verified, err := Verify(path, opts)
		if err != nil {
			t.Fatal(err)
		}
		st, err := StagePrepared(path, verified, opts)
		if err != nil {
			t.Fatal(err)
		}
		stages[path] = st
	}
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		q.Attach(ctx)
		rt := &runtimeRefusalJourney{id: id, stages: stages}
		f := pluginfacility.New(pluginfacility.Config{Runtime: rt})
		defer f.Close()
		observe := func(path string) {
			f.Observe([]pluginfacility.Observed{{ID: id, Package: path, Hash: path}}, pluginfacility.Policy{Revision: 1})
		}
		observe(badPackage)
		f.Attach(ctx)
		synctest.Wait()
		time.Sleep(time.Second) // the activation's observation poll, not a download retry
		synctest.Wait()
		rows := f.Snapshot().Instances
		if len(rows) != 1 || rows[0].Refusal == nil || rows[0].Refusal.Class != pluginfacility.ClassPermanent || !rows[0].RetryAt.IsZero() || rt.starts != 0 {
			t.Fatalf("the actual facility did not retain the declaration refusal: %+v starts=%d", rows, rt.starts)
		}
		time.Sleep(24 * time.Hour)
		synctest.Wait()
		if fx.fetches != 1 || rt.starts != 0 {
			t.Fatalf("the refused package kept downloading or ran: fetches=%d starts=%d", fx.fetches, rt.starts)
		}
		observe(goodPackage)
		synctest.Wait()
		time.Sleep(time.Second)
		synctest.Wait()
		rows = f.Snapshot().Instances
		if len(rows) != 1 || rows[0].State != pluginfacility.StateActive || rt.starts != 1 {
			t.Fatalf("the corrected package did not serve automatically: %+v starts=%d", rows, rt.starts)
		}
	})
}

type runtimeRefusalJourney struct {
	id     string
	stages map[string]*Staged
	starts int
}

func (r *runtimeRefusalJourney) Verify(_ context.Context, path string) (pluginfacility.Evidence, error) {
	return pluginfacility.Evidence{ID: r.id, Version: "0.1.0", Package: path, PackageHash: path, Handle: r.stages[path]}, nil
}
func (r *runtimeRefusalJourney) Prepare(_ context.Context, e pluginfacility.Evidence) (pluginfacility.Prepared, error) {
	return pluginfacility.Prepared{Evidence: e, Handle: e.Handle}, nil
}
func (r *runtimeRefusalJourney) Acquire(ctx context.Context, p pluginfacility.Prepared, _ func(pluginfacility.MaterialStatus)) error {
	for {
		err := p.Handle.(*Staged).Acquire(ctx)
		var pending *AcquiringError
		if !errors.As(err, &pending) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}
func (r *runtimeRefusalJourney) PluginID() string { return r.id }
func (r *runtimeRefusalJourney) Start(context.Context, pluginfacility.Prepared, *pluginfacility.Lease) (pluginfacility.Running, error) {
	r.starts++
	return r, nil
}
func (r *runtimeRefusalJourney) Health(context.Context, pluginfacility.Running) error { return nil }
func (r *runtimeRefusalJourney) Redirect(pluginfacility.Running, pluginfacility.Running) error {
	return nil
}
func (r *runtimeRefusalJourney) Stop(context.Context, pluginfacility.Running) (pluginfacility.Retirement, error) {
	return pluginfacility.Retirement{Established: true}, nil
}
