package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/oauth"
)

// The actual app source/adapter and LLM/discovery doors, not a direct call to
// ForceRefresh (which already worked while rejected model requests did not).
func TestOwnedProviderRejectionRecoversWithoutAnotherSignIn(t *testing.T) {
	for _, door := range []string{"chat", "typed chat", "discovery"} {
		for _, expires := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/expiry=%v", door, expires), func(t *testing.T) {
				f := newRejectedProvider(t, expires)
				f.typedRejection.Store(door == "typed chat")
				f.call(t, door, nil)
				if f.refreshes.Load() != 1 || f.resources.Load() != 2 {
					t.Fatalf("recovery must refresh then replay once: refresh=%d resource=%d", f.refreshes.Load(), f.resources.Load())
				}
				var saved map[string]any
				raw, err := os.ReadFile(f.path)
				if err != nil || json.Unmarshal(raw, &saved) != nil || saved["access_token"] != "renewed-access" || saved["refresh_token"] != "renewed-refresh" {
					t.Fatalf("rotated pair was not durably published: %v", err)
				}
				// A fresh App has no source cache. The same owned file must
				// support the next request without consent or another refresh.
				cfg := *f.app.cfg
				restarted := New(&cfg)
				restarted.oauthTransport, restarted.oauthGuard = f.app.oauthTransport, f.app.oauthGuard
				f.app = restarted
				f.call(t, door, nil)
				if f.refreshes.Load() != 1 || f.resources.Load() != 3 {
					t.Fatal("restart did not reuse the durable replacement")
				}
			})
		}
	}
}

func TestProviderRejectionReusesALoginPublishedDuringTheRequest(t *testing.T) {
	for _, door := range []string{"chat", "discovery"} {
		t.Run(door, func(t *testing.T) {
			f := newRejectedProvider(t, false)
			f.newLoginOnRejection.Store(true)
			f.call(t, door, nil)
			if f.refreshes.Load() != 0 || f.resources.Load() != 2 {
				t.Fatalf("old rejection spent the new login's grant: refresh=%d resource=%d", f.refreshes.Load(), f.resources.Load())
			}
			var saved map[string]any
			raw, err := os.ReadFile(f.path)
			if err != nil || json.Unmarshal(raw, &saved) != nil || saved["refresh_token"] != "new-login-refresh" {
				t.Fatal("old rejection replaced the newer login")
			}
		})
	}
}

func TestRejectedProviderRecoveryKeepsItsBoundaries(t *testing.T) {
	for _, door := range []string{"chat", "discovery"} {
		for _, mode := range []string{"borrowed", "invalid grant", "second rejection", "ordinary bad request"} {
			t.Run(door+"/"+mode, func(t *testing.T) {
				f := newRejectedProvider(t, false)
				before, err := os.ReadFile(f.path)
				if err != nil {
					t.Fatal(err)
				}
				wantRefresh, wantResource := int32(1), int32(1)
				var want error
				switch mode {
				case "borrowed":
					f.entry.Credential = oauth.KindFilePrefix + f.path
					f.entry.CredentialOptions = map[string]string{"file": f.path}
					want, wantRefresh = oauth.ErrOwnerRefreshRequired, 0
				case "invalid grant":
					f.invalidGrant.Store(true)
					want = oauth.ErrGrantInvalid
				case "second rejection":
					f.rejectRenewed.Store(true)
					wantResource = 2
				case "ordinary bad request":
					f.badRequest.Store(true)
					wantRefresh = 0
				}
				f.call(t, door, func(err error) {
					if err == nil || (want != nil && !errors.Is(err, want)) {
						t.Fatalf("wrong recovery refusal: %v (want %v)", err, want)
					}
				})
				if f.refreshes.Load() != wantRefresh || f.resources.Load() != wantResource {
					t.Fatalf("unbounded or unauthorized recovery: refresh=%d resource=%d", f.refreshes.Load(), f.resources.Load())
				}
				if mode != "second rejection" {
					after, err := os.ReadFile(f.path)
					if err != nil || string(before) != string(after) {
						t.Fatal("a refused recovery changed the original file")
					}
				}
			})
		}
	}
}

// AFTER THE AUTHORITY REFUSES A RENEWAL THE STATUS LINE SAYS SO. A refused
// renewal leaves the identity's own file as it was, so the file alone still
// reads as a credential the host renews, and the line that warns the
// operator before the identity stops thinking was silent for one that had
// already stopped it. The remedy is the one the source names, to the byte,
// whatever expiry the refused access token carries; the card shows it as
// past use, which is what brings the sign-in controls back.
func TestAfterTheAuthorityRefusesARenewalTheStatusLineSaysToSignInAgain(t *testing.T) {
	const want = "the fixture credential is no longer usable — sign in again; the identity cannot think until you do"
	for _, expires := range []bool{false, true} {
		t.Run(fmt.Sprintf("expiry=%v", expires), func(t *testing.T) {
			f := newRejectedProvider(t, expires)
			f.app.adoptRuntime(f.entry, nil)
			if got := f.app.credentialWarning(); got != "" {
				t.Fatalf("the host renews this credential and the status line says %q", got)
			}
			f.invalidGrant.Store(true)
			f.call(t, "chat", func(err error) {
				if !errors.Is(err, oauth.ErrGrantInvalid) {
					t.Fatalf("the fixture's authority did not refuse the renewal: %v", err)
				}
			})
			if got := f.app.credentialWarning(); got != want {
				t.Fatalf("the authority refused to renew the credential the identity is on and the status line says %q, want %q", got, want)
			}
			if ci := f.app.credentialInfo(f.entry); ci == nil || !ci.Expired || ci.ExpiresAt != "" {
				t.Fatalf("a credential the authority will not renew is not shown as past use: %+v", ci)
			}
		})
	}
}

// A REFUSED RENEWAL IS STILL SAID AFTER ANOTHER CREDENTIAL'S SIGN-IN. The
// refusal lives in the credential source, in memory, because the file it
// refused still reads as a credential the host renews. A completed sign-in
// emptied every cached source, so a sign-in for another credential (another
// entry's, or a connector profile's) handed the status line a new source for
// the same file with no memory of the refusal, and the line fell silent while
// the identity still could not think. The control half: a sign-in of the
// refused credential itself does start it afresh, since that sign-in is the
// remedy the line names.
func TestARefusedRenewalIsStillSaidAfterAnotherCredentialsSignIn(t *testing.T) {
	const want = "the fixture credential is no longer usable — sign in again; the identity cannot think until you do"
	f := newRejectedProvider(t, false)
	f.app.adoptRuntime(f.entry, nil)
	f.invalidGrant.Store(true)
	f.call(t, "chat", func(err error) {
		if !errors.Is(err, oauth.ErrGrantInvalid) {
			t.Fatalf("the fixture's authority did not refuse the renewal: %v", err)
		}
	})
	if got := f.app.credentialWarning(); got != want {
		t.Fatalf("before any sign-in the status line says %q, want %q", got, want)
	}
	other := filepath.Join(t.TempDir(), "other.json")
	f.app.endSignIn("provider:Other", &pendingSignIn{kind: "other-credential", dest: other, cancel: func() {}}, nil)
	if got := f.app.credentialWarning(); got != want {
		t.Fatalf("a sign-in for another credential silenced the refusal: the status line says %q, want %q", got, want)
	}
	f.app.endSignIn("provider:fixture", &pendingSignIn{kind: f.entry.Credential, dest: f.path, cancel: func() {}}, nil)
	f.app.bgWG.Wait() // the reload this sign-in asks for, on the client the identity is on
	if got := f.app.credentialWarning(); got != "" {
		t.Fatalf("a sign-in of the refused credential itself did not start it afresh: the status line says %q", got)
	}
}

type rejectedProvider struct {
	app                 *App
	entry               providerEntry
	path                string
	refreshes           atomic.Int32
	resources           atomic.Int32
	invalidGrant        atomic.Bool
	rejectRenewed       atomic.Bool
	typedRejection      atomic.Bool
	badRequest          atomic.Bool
	newLoginOnRejection atomic.Bool
}

func newRejectedProvider(t *testing.T, expires bool) *rejectedProvider {
	t.Helper()
	f := &rejectedProvider{}
	authority := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.refreshes.Add(1)
		if err := r.ParseForm(); err != nil || r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "original-refresh" {
			t.Error("refresh did not use the existing owned grant")
		}
		w.Header().Set("Content-Type", "application/json")
		if f.invalidGrant.Load() {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"invalid_grant"}`)
			return
		}
		fmt.Fprint(w, `{"access_token":"renewed-access","refresh_token":"renewed-refresh","expires_in":3600}`)
	}))
	t.Cleanup(authority.Close)
	resource := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.resources.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if f.badRequest.Load() {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":{"code":"invalid_request_error"}}`)
			return
		}
		if r.Header.Get("Authorization") != "Bearer renewed-access" || f.rejectRenewed.Load() {
			if f.newLoginOnRejection.CompareAndSwap(true, false) {
				if err := oauth.WriteTokenFile(f.path, &oauth.Tokens{Access: "renewed-access", Refresh: "new-login-refresh"}); err != nil {
					t.Error(err)
				}
			}
			status := http.StatusUnauthorized
			if f.typedRejection.Load() {
				status = http.StatusBadRequest
			}
			w.WriteHeader(status)
			fmt.Fprint(w, `{"error":{"code":"invalid_token"}}`)
			return
		}
		if r.Method == http.MethodGet {
			fmt.Fprint(w, `{"data":[{"id":"fixture-model"}]}`)
		} else {
			fmt.Fprint(w, textReply("alive"))
		}
	}))
	t.Cleanup(resource.Close)
	dir := t.TempDir()
	f.app = New(&Config{SourcePath: filepath.Join(dir, "config.json")})
	f.app.cfg.Identity.LedgerPath = filepath.Join(dir, "data", "ledger.jsonl")
	f.app.oauthTransport = authority.Client().Transport
	f.app.oauthGuard = func(context.Context, string) error { return nil } // isolated authority only
	f.entry = providerEntry{Name: "fixture", URL: resource.URL + "/v1", APIType: "openai", Credential: "fixture", DefaultModel: "fixture-model",
		signIn: oauth.Provider{ClientID: "fixture", TokenURL: authority.URL, CredentialFile: "fixture.json"}}
	var err error
	f.path, err = f.app.ownedCredentialPath(f.entry.Credential, f.entry.signIn.CredentialFile)
	if err != nil {
		t.Fatal(err)
	}
	tok := &oauth.Tokens{Access: "original-access", Refresh: "original-refresh"}
	if expires {
		tok.Expires = time.Now().Add(time.Hour)
	}
	if err := oauth.WriteTokenFile(f.path, tok); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *rejectedProvider) call(t *testing.T, door string, refusal func(error)) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var err error
	if door != "discovery" {
		var cc llm.ClientConfig
		cc, err = f.app.entryTransport(f.entry, "")
		if err == nil {
			cc.Model, cc.NoStream, cc.Retries = "fixture-model", true, -1
			var result *llm.Response
			result, err = llm.New(&cc).Chat(ctx, []llm.Message{{Role: "user", Content: "Reply alive"}}, llm.ChatOptions{})
			if err == nil && (result == nil || len(result.Choices) != 1 || result.Choices[0].Message.Content != "alive") {
				t.Fatal("renewed request did not produce the reply")
			}
		}
	} else {
		var models []string
		models, _, err = f.app.discoverMetaForEntry(ctx, f.entry, "")
		if err == nil && !slices.Equal(models, []string{"fixture-model"}) {
			t.Fatalf("renewed discovery did not return its models: %v", models)
		}
	}
	if refusal != nil {
		refusal(err)
	} else if err != nil {
		t.Fatalf("rejected owned credential did not recover (refresh=%d resource=%d): %v", f.refreshes.Load(), f.resources.Load(), err)
	}
}
