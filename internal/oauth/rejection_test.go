package oauth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRejectedGenerationReloadsANewerOriginalBeforeRefreshing(t *testing.T) {
	for _, force := range []bool{false, true} {
		name := "Stale"
		if force {
			name = "ForceRefresh"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "owned.json")
			if err := WriteTokenFile(path, &Tokens{Access: "old", Refresh: "old-refresh", Expires: time.Now().Add(time.Hour)}); err != nil {
				t.Fatal(err)
			}
			calls := 0
			src, err := NewProfileSource(path, OAuthParams{ClientID: "fixture", TokenURL: "https://authority.invalid/token"}, &http.Client{Transport: refreshTransport(func(*http.Request) (*http.Response, error) {
				calls++
				return nil, errors.New("must reuse the owner's new original")
			})})
			if err != nil {
				t.Fatal(err)
			}
			gen := src.Generation()
			// The provider rejected gen, not the new login published while
			// that request was in flight. No refresh of the latter is needed.
			if err := WriteTokenFile(path, &Tokens{Access: "new-login", Refresh: "new-login-refresh"}); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if force {
				_, err = src.ForceRefresh(t.Context(), gen)
			} else {
				err = src.Stale(t.Context(), gen)
			}
			if err != nil || calls != 0 {
				t.Fatalf("stale rejection spent the newer login's grant: calls=%d err=%v", calls, err)
			}
			cred, err := src.Credential(t.Context())
			if err != nil || cred.Token != "new-login" || cred.Gen == gen {
				t.Fatalf("new original not selected: %+v %v", cred, err)
			}
			after, err := os.ReadFile(path)
			if err != nil || string(before) != string(after) {
				t.Fatal("rejecting the old generation modified the newer original")
			}
		})
	}
}

func TestStaleOwnedRefreshRetainsCancellationCustody(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owned.json")
	if err := WriteTokenFile(path, &Tokens{Access: "old", Refresh: "old-refresh"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	client := &http.Client{Transport: refreshTransport(func(r *http.Request) (*http.Response, error) {
		cancel() // authority spent the old refresh grant, then the turn ended
		if r.Context().Err() != nil {
			return nil, r.Context().Err()
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"access_token":"renewed","refresh_token":"rotated"}`))}, nil
	})}
	params := OAuthParams{ClientID: "fixture", TokenURL: "https://authority.invalid/token"}
	src, err := NewProfileSource(path, params, client)
	if err != nil {
		t.Fatal(err)
	}
	if err := src.Stale(ctx, src.Generation()); err != nil || !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("rejected-token recovery abandoned its admitted rotation: %v", err)
	}
	reopened, err := NewProfileSource(path, params, client)
	if err != nil {
		t.Fatal(err)
	}
	cred, err := reopened.Credential(t.Context())
	if err != nil || cred.Token != "renewed" {
		t.Fatalf("restart lost rotated credential: %v", err)
	}
}
