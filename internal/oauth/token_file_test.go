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

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
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
				_, err = RemoveTokenFile(path)
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

func TestARefreshDoesNotStoreItsTokenAfterARefusedDisconnect(t *testing.T) {
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
	refused := errors.New("the removal was refused")
	removeToken = func(string) error { return refused }
	t.Cleanup(func() { removeToken = atomicfile.Remove })
	done := make(chan error, 1)
	go func() { _, err := src.Credential(context.Background()); done <- err }()
	<-entered
	if _, err := RemoveTokenFile(path); !errors.Is(err, refused) {
		t.Fatalf("the removal is answered as refused: %v", err)
	}
	close(release)
	if err := <-done; err == nil {
		t.Error("a refresh that commits after a refused disconnect is refused")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if st, err := parseGeneric(raw); err != nil || st.refresh != "old-refresh" {
		t.Errorf("the file holds the token the disconnect revoked, not the refresh's: %s", raw)
	}
	removeToken = atomicfile.Remove
	if err := WriteTokenFile(path, &Tokens{Access: "signed-in", Refresh: "new-refresh"}); err != nil {
		t.Fatal(err)
	}
	if err := replaceTokenFile(path, mustRead(t, path), &Tokens{Access: "refreshed", Refresh: "new-refresh"}); err != nil {
		t.Errorf("after a sign-in a refresh stores again: %v", err)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestARemovalThatLandsClearsTheNote(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owned.json")
	if err := WriteTokenFile(path, &Tokens{Access: "a", Refresh: "the-refresh"}); err != nil {
		t.Fatal(err)
	}
	removeToken = func(string) error { return errors.New("the removal was refused") }
	t.Cleanup(func() { removeToken = atomicfile.Remove })
	if _, err := RemoveTokenFile(path); err == nil {
		t.Fatal("the seam refuses the removal")
	}
	tokenFileMu.Lock()
	noted := disconnected[tokenKey(path)]
	tokenFileMu.Unlock()
	if !noted {
		t.Fatal("a refused removal is noted")
	}
	removeToken = atomicfile.Remove
	raw, err := RemoveTokenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if st, err := parseGeneric(raw); err != nil || st.refresh != "the-refresh" {
		t.Errorf("the removal answers what the file held: %s", raw)
	}
	tokenFileMu.Lock()
	noted = disconnected[tokenKey(path)]
	tokenFileMu.Unlock()
	if noted {
		t.Error("a removal that lands clears the note")
	}
}
