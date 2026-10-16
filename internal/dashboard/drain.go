package dashboard

import (
	"context"
	"fmt"
	"net/http"
	"sync"
)

type DrainCut struct{ Cause error }

func (e *DrainCut) Error() string {
	return "the dashboard's requests and jobs had not all ended at the stop's bound, and its connections were closed: " + e.Cause.Error()
}
func (e *DrainCut) Unwrap() error { return e.Cause }

func (s *Server) Settle(ctx context.Context) error {

	joined := make(chan struct{})
	go func() {
		s.jobsWG.Wait()
		close(joined)
	}()
	requests := s.requests.wait(ctx)
	jobs := true
	select {
	case <-joined:
		jobs = false
	default:
		select {
		case <-joined:
			jobs = false
		case <-ctx.Done():
		}
	}
	switch {
	case jobs:
		return fmt.Errorf("the dashboard's jobs had not all ended, and %d of its requests had not returned", requests)
	case requests > 0:
		return fmt.Errorf("%d of the dashboard's requests had not returned", requests)
	}
	return nil
}

type inFlight struct {
	mu   sync.Mutex
	n    int
	idle chan struct{}
}

func (f *inFlight) track(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		if f.n == 0 {
			f.idle = make(chan struct{})
		}
		f.n++
		f.mu.Unlock()
		defer func() {
			f.mu.Lock()
			if f.n--; f.n == 0 {
				close(f.idle)
			}
			f.mu.Unlock()
		}()
		next.ServeHTTP(w, r)
	})
}

func (f *inFlight) wait(ctx context.Context) int {
	for {
		f.mu.Lock()
		n, idle := f.n, f.idle
		f.mu.Unlock()
		if n == 0 {
			return 0
		}
		select {
		case <-idle:
		case <-ctx.Done():
			f.mu.Lock()
			n = f.n
			f.mu.Unlock()
			return n
		}
	}
}
