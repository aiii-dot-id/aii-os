package oauth

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

type refreshTransport func(*http.Request) (*http.Response, error)

func (f refreshTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type cancelAtEOF struct {
	io.Reader
	cancel context.CancelFunc
}

func TestOwnedRefreshBoundsAuthorityWait(t *testing.T) {
	for _, clientLimit := range []time.Duration{0, 2 * time.Second} {
		t.Run(clientLimit.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "owned.json")
				if err := WriteTokenFile(path, &Tokens{Access: "old", Refresh: "old-refresh", Expires: time.Now().Add(-time.Minute)}); err != nil {
					t.Fatal(err)
				}
				before, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				calls := 0
				src, err := NewProfileSource(path, OAuthParams{ClientID: "fixture", TokenURL: "https://authority.invalid/token"}, &http.Client{
					Timeout: clientLimit,
					Transport: refreshTransport(func(r *http.Request) (*http.Response, error) {
						calls++
						cancel() // a cancelled turn must not erase the exchange's own bound
						if _, ok := r.Context().Deadline(); !ok {
							return nil, errors.New("refresh has no deadline")
						}
						<-r.Context().Done()
						return nil, r.Context().Err()
					}),
				})
				if err != nil {
					t.Fatal(err)
				}
				start := time.Now()
				_, err = src.Credential(ctx)
				want := tokenExchangeTimeout
				if clientLimit > 0 {
					want = clientLimit
				}
				if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) != want || calls != 1 {
					t.Fatalf("bounded refresh: elapsed=%v want=%v calls=%d err=%v", time.Since(start), want, calls, err)
				}
				after, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(before, after) {
					t.Fatal("a failed exchange replaced the original credential")
				}
			})
		})
	}
}

func (r cancelAtEOF) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err == io.EOF {
		r.cancel()
	}
	return n, err
}

func TestOwnedRefreshKeepsRotatedCredentialsDespiteTurnCancellation(t *testing.T) {
	for _, when := range []string{"during exchange", "after response"} {
		t.Run(when, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "owned.json")
			if err := WriteTokenFile(path, &Tokens{Access: "old-access", Refresh: "old-refresh", Expires: time.Now().Add(-time.Minute)}); err != nil {
				t.Fatal(err)
			}
			type requestScope struct{}
			ctx, cancel := context.WithCancel(context.WithValue(t.Context(), requestScope{}, "preserved"))
			defer cancel()
			calls := 0
			client := &http.Client{Transport: refreshTransport(func(r *http.Request) (*http.Response, error) {
				calls++ // the authority has consumed the old refresh token here
				if r.Context().Value(requestScope{}) != "preserved" {
					t.Error("refresh lost request-scope policy values")
				}
				if deadline, ok := r.Context().Deadline(); !ok || time.Until(deadline) > 31*time.Second {
					t.Error("an admitted refresh must retain a bounded exchange deadline")
				}
				if when == "during exchange" {
					cancel()
					if err := r.Context().Err(); err != nil {
						return nil, err // lost response after the authority consumed the token
					}
				}
				var body io.Reader = strings.NewReader(`{"access_token":"new-access","refresh_token":"new-refresh","expires_in":3600}`)
				if when == "after response" {
					body = cancelAtEOF{body, cancel}
				}
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(body)}, nil
			})}
			params := OAuthParams{ClientID: "fixture", TokenURL: "https://authority.invalid/token"}
			src, err := NewProfileSource(path, params, client)
			if err != nil {
				t.Fatal(err)
			}
			cred, err := src.Credential(ctx)
			if err != nil || cred.Token != "new-access" || !cred.Refreshed {
				t.Errorf("completed owned rotation must report its actual result: token=%q refreshed=%v err=%v", cred.Token, cred.Refreshed, err)
			}
			if !errors.Is(ctx.Err(), context.Canceled) {
				t.Fatal("cancellation step was not reached")
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			st, err := parseGeneric(raw)
			if err != nil || st.access != "new-access" || st.refresh != "new-refresh" {
				t.Fatalf("owned file lost the rotated pair: %v", err)
			}
			// A reopened source uses the durable replacement, with no second
			// refresh or user sign-in. The caller's cancellation remains set.
			reopened, err := NewProfileSource(path, params, client)
			if err != nil {
				t.Fatal(err)
			}
			if got, err := reopened.Credential(t.Context()); err != nil || got.Token != "new-access" || calls != 1 {
				t.Fatalf("reopen: token=%q calls=%d err=%v", got.Token, calls, err)
			}
		})
	}
}

func TestCancelledCallerDoesNotBeginAnOwnedRefresh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owned.json")
	if err := WriteTokenFile(path, &Tokens{Access: "old", Refresh: "old-refresh", Expires: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	src, err := NewProfileSource(path, OAuthParams{ClientID: "fixture", TokenURL: "https://authority.invalid/token"}, &http.Client{Transport: refreshTransport(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("must not contact authority")
	})})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := src.Credential(ctx); !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatalf("cancelled caller spent the refresh token: calls=%d err=%v", calls, err)
	}
	// The same check belongs at the refresh admission itself, not only at
	// Credential entry: a turn can cancel while waiting for the refresh slot.
	if _, err := src.refreshOwned(ctx, "old-refresh", src.Generation()); !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatalf("cancelled refresh admission spent a token: calls=%d err=%v", calls, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("cancelled-before-admission call changed the file")
	}
}

// A WAITER WHOSE TURN ENDED STOPS WAITING. One refresh is at the authority
// and holds the refresh slot; a second caller whose turn was cancelled must
// return at once rather than wait out that round-trip (bounded at
// tokenExchangeTimeout, 30 s), and spends nothing. The refresh in flight
// completes unharmed and is the only round-trip.
func TestARefreshWaiterWhoseTurnEndedStopsWaiting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owned.json")
	if err := WriteTokenFile(path, &Tokens{Access: "old", Refresh: "old-refresh", Expires: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	src, err := NewProfileSource(path, OAuthParams{ClientID: "fixture", TokenURL: "https://authority.invalid/token"}, &http.Client{Transport: refreshTransport(func(r *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		select {
		case <-release:
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
		body := `{"access_token":"new","refresh_token":"new-refresh","expires_in":3600}`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		cred Credential
		err  error
	}
	first := make(chan result, 1)
	go func() { c, err := src.Credential(t.Context()); first <- result{c, err} }()
	<-entered // the first refresh holds the slot, at the authority

	ended, cancel := context.WithCancel(t.Context())
	cancel()
	waited := make(chan error, 1)
	go func() { _, err := src.refreshOwned(ended, "old-refresh", src.Generation()); waited <- err }()
	select {
	case err := <-waited:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("the waiter whose turn ended: %v, want context.Canceled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a waiter whose turn ended waited out the refresh in flight")
	}
	close(release)
	if r := <-first; r.err != nil || r.cred.Token != "new" || !r.cred.Refreshed || calls.Load() != 1 {
		t.Fatalf("the refresh in flight: token=%q refreshed=%v err=%v round-trips=%d", r.cred.Token, r.cred.Refreshed, r.err, calls.Load())
	}
}
