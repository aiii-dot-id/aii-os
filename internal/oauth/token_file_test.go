package oauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLateRefreshCannotOverwriteLoginOrDisconnect(t *testing.T) {
	for _, action := range []string{"login", "disconnect"} {
		t.Run(action, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "owned.json")
			if err := WriteTokenFile(path, &Tokens{Access: "old", Refresh: "old-refresh", Expires: time.Now().Add(-time.Minute)}); err != nil {
				t.Fatal(err)
			}
			entered, release := make(chan struct{}), make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(entered)
				<-release
				fmt.Fprint(w, `{"access_token":"late","refresh_token":"late-refresh","expires_in":3600}`)
			}))
			defer server.Close()
			src, err := NewProfileSource(path, OAuthParams{ClientID: "client", TokenURL: server.URL}, server.Client())
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { _, err := src.Credential(context.Background()); done <- err }()
			<-entered
			if action == "login" {
				err = WriteTokenFile(path, &Tokens{Access: "new-login"})
			} else {
				err = RemoveTokenFile(path)
			}
			close(release)
			if err != nil {
				t.Fatal(err)
			}
			if err = <-done; !errors.Is(err, ErrCredentialChanged) {
				t.Fatalf("expected conflict, got %v", err)
			}
			raw, err := os.ReadFile(path)
			if action == "disconnect" {
				if !errors.Is(err, os.ErrNotExist) {
					t.Fatal("refresh restored disconnected credential")
				}
				return
			}
			st, err := parseGeneric(raw)
			if err != nil || st.access != "new-login" {
				t.Fatal("refresh replaced newer login")
			}
		})
	}
}

func TestOwnedConfiguredIsGenericAndValidatesClaimHeaders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vendor.json")
	tok := &Tokens{Access: fakeJWTClaims(t, map[string]any{"tenant": "tenant-a"})}
	if err := WriteTokenFile(path, tok); err != nil {
		t.Fatal(err)
	}
	p := OAuthParams{ClientID: "client", TokenURL: "https://auth.example/token", ResourceHeaders: map[string]string{"X-Client": "configured"}, ClaimHeaders: map[string][]string{"X-Tenant": {"tenant"}}}
	src, err := NewOwnedConfigured("new-vendor", path, p)
	if err != nil {
		t.Fatal(err)
	}
	cred, err := src.Credential(context.Background())
	if err != nil || cred.Headers["X-Tenant"] != "tenant-a" || cred.Headers["X-Client"] != "configured" {
		t.Fatal("configured headers did not reach shared source")
	}
	p.ClaimHeaders = map[string][]string{"X-Tenant": {"missing"}}
	if _, err := NewOwnedConfigured("new-vendor", path, p); err == nil {
		t.Fatal("missing configured claim accepted")
	}
	p.ClaimHeaders = nil
	p.ResourceHeaders = map[string]string{"Authorization": "replace bearer"}
	if _, err := NewOwnedConfigured("new-vendor", path, p); err == nil {
		t.Fatal("resource headers replaced protocol authorization")
	}
}
