//go:build (linux && !android) || (darwin && !ios) || windows

package pluginhost

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestADownloadTheNetworkEndedIsTriedAgainWhenThisHostNextCompletesAFetch(t *testing.T) {
	decl, _, st := storeModel(t, 4096)
	cut := answer(&NetworkError{Cause: errors.New("dial tcp: connect: network is unreachable")})
	gate := make(chan struct{})
	held := func(offset int64, w io.Writer) (int64, error) { <-gate; return cut(offset, w) }
	s := &scripted{steps: []func(int64, io.Writer) (int64, error){cut}}
	w := &waits{}
	q, _ := ruleAcquirer(t, s, w, st)
	m := engineMaterial(st, decl)
	q.Want(m)
	stop := stopped(t, q, m.PluginID)
	if n, _, _ := s.calls(); n != MaxDownloadAttempts || !stop.Resumes || !strings.Contains(stop.Remedy, "tried again by itself when the plugin catalog is next fetched") {
		t.Fatalf("five attempts the network failed: %d calls, %+v", n, stop)
	}
	// The next attempt is held in flight, so the words that come while the
	// resumed download runs find nothing ended to reopen.
	s.mu.Lock()
	s.steps = []func(int64, io.Writer) (int64, error){cut, cut, cut, cut, cut, held}
	s.mu.Unlock()
	if got := q.Resume(); !slices.Equal(got, []string{m.PluginID}) {
		t.Fatalf("the host's completed fetch reopened %v", got)
	}
	waitFor(t, "the resumed attempt in flight", func() bool { n, _, _ := s.calls(); return n == MaxDownloadAttempts+1 })
	for range 3 {
		if got := q.Resume(); len(got) != 0 {
			t.Fatalf("a download in flight was reopened again: %v", got)
		}
	}
	close(gate)
	stop = stopped(t, q, m.PluginID)
	time.Sleep(50 * time.Millisecond)
	if n, _, _ := s.calls(); n != 2*MaxDownloadAttempts || !stop.Resumes {
		t.Fatalf("the resumed download: %d calls in all, want %d; %+v", n, 2*MaxDownloadAttempts, stop)
	}
}

func TestAnAnswersEndIsNotTriedAgainWhenThisHostCompletesAFetch(t *testing.T) {
	decl, _, st := storeModel(t, 4096)
	s := &scripted{steps: []func(int64, io.Writer) (int64, error){answer(&FetchStatusError{Status: 404})}}
	q, _ := ruleAcquirer(t, s, &waits{}, st)
	m := engineMaterial(st, decl)
	q.Want(m)
	if stop := stopped(t, q, m.PluginID); stop.Resumes || strings.Contains(stop.Remedy, "by itself") {
		t.Fatalf("a 404 is said to be tried again by itself: %+v", stop)
	}
	if got := q.Resume(); len(got) != 0 {
		t.Fatalf("the host's completed fetch reopened an answer's end: %v", got)
	}
	time.Sleep(50 * time.Millisecond)
	if n, _, _ := s.calls(); n != 1 {
		t.Fatalf("an answer's end was asked again: %d calls", n)
	}
}

func TestTheWritersNetworkEndIsAskedAgainWithRetryWhenThisHostCompletesAFetch(t *testing.T) {
	for _, network := range []bool{true, false} {
		decl, _, st := storeModel(t, 4096)
		var mu sync.Mutex
		var retries []bool
		q := NewAcquirer(AcquirerConfig{
			Materialize: func(_ context.Context, m Material, _ ModelFetcher) error {
				mu.Lock()
				defer mu.Unlock()
				retries = append(retries, m.Retry())
				return &MaterialEnded{Cause: "the download stopped", Remedy: "then Try again", Network: network}
			},
			Logf: func(string, ...any) {},
		})
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		q.Attach(ctx)
		m := engineMaterial(st, decl)
		q.Want(m)
		waitFor(t, "the writer's end", func() bool { return q.EndCause(m.PluginID) != nil })
		got := q.Resume()
		time.Sleep(50 * time.Millisecond)
		mu.Lock()
		asked := append([]bool(nil), retries...)
		mu.Unlock()
		switch {
		case network && (!slices.Equal(got, []string{m.PluginID}) || !slices.Equal(asked, []bool{false, true})):
			t.Fatalf("the writer's network end: reopened %v, asks with Retry %v", got, asked)
		case !network && (len(got) != 0 || !slices.Equal(asked, []bool{false})):
			t.Fatalf("the writer's other end: reopened %v, asks with Retry %v", got, asked)
		}
	}
}
