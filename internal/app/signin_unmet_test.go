package app

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/oauth"
)

func TestAnEntryThatCannotOfferSignInNamesTheRequirement(t *testing.T) {
	// The shipped entry as an identity holds it: bound to the sign-in
	// contract it names, as every load of providers.json binds it.
	reg := providerRegistry{Providers: []providerEntry{shippedEntry(t, "ChatGPT (Plus/Pro)")}}
	if err := bindOAuth(&reg); err != nil {
		t.Fatal(err)
	}
	shipped := reg.Providers[0]
	if !canSignIn(shipped) || signInUnmet(shipped) != "" {
		t.Fatalf("the shipped ChatGPT entry: can sign in %v, unmet %q", canSignIn(shipped), signInUnmet(shipped))
	}
	change := func(f func(*providerEntry)) providerEntry {
		e := shipped
		e.CredentialOptions = map[string]string{}
		for k, v := range shipped.CredentialOptions {
			e.CredentialOptions[k] = v
		}
		f(&e)
		return e
	}
	for _, c := range []struct {
		what string
		e    providerEntry
		want string
	}{
		{"the credential file is pinned", change(func(e *providerEntry) { e.CredentialOptions["file"] = "/elsewhere/auth.json" }), "credential_options.file"},
		{"the credential kind has no file of this identity's", change(func(e *providerEntry) { e.Credential, e.signIn.CredentialFile = "a-kind-of-my-own", "" }), "no credential file of this identity's own"},
		{"the contract's credential file is a path", change(func(e *providerEntry) { e.signIn.CredentialFile = "../elsewhere/auth.json" }), "credential_file must be a filename"},
		{"no contract is named or lent", change(func(e *providerEntry) { e.OAuth, e.signIn = "", oauth.Provider{CredentialFile: "mine.json"} }), `it names no "oauth"`},
		{"the contract lacks its client", change(func(e *providerEntry) { e.signIn.ClientID = "" }), "states no client_id"},
		{"the contract lacks its token address", change(func(e *providerEntry) { e.signIn.TokenURL = "" }), "states no token_url"},
		{"the device contract lacks its device address", change(func(e *providerEntry) { e.signIn.DeviceURL = "" }), "states no device_url"},
		{"the device contract lacks its poll address", change(func(e *providerEntry) { e.signIn.DevicePollURL = "" }), "states no device_poll_url"},
		{"the device contract lacks its verification address", change(func(e *providerEntry) { e.signIn.VerificationURI = "" }), "states no verification_uri"},
		{"the device contract lacks its redirect", change(func(e *providerEntry) { e.signIn.DeviceRedirectURI = "" }), "states no device_redirect_uri"},
		{"the device contract lacks its expiry", change(func(e *providerEntry) { e.signIn.DeviceExpiresSeconds = 0 }), "states no device_expires_seconds"},
		{"a plain device contract lacks its device address", change(func(e *providerEntry) { e.signIn.SignIn, e.signIn.DeviceURL = "device", "" }), "states no device_url"},
		{"a callback contract lacks its authorize address", change(func(e *providerEntry) {
			e.signIn.SignIn, e.signIn.RedirectURI, e.signIn.AuthorizeURL = "callback", "http://127.0.0.1:1455/oauth/callback", ""
		}), "states no authorize_url"},
		{"a callback contract's redirect is not the dashboard's", change(func(e *providerEntry) {
			e.signIn.SignIn, e.signIn.RedirectURI, e.signIn.AuthorizeURL = "callback", "https://elsewhere.example/back", "https://auth.example/authorize"
		}), "redirect_uri"},
		{"the method is unknown", change(func(e *providerEntry) { e.signIn.SignIn = "by-post" }), `sign_in method "by-post"`},
	} {
		got := signInUnmet(c.e)
		if canSignIn(c.e) || !strings.Contains(got, c.want) {
			t.Errorf("%s: can sign in %v, and says %q; want the requirement %q named", c.what, canSignIn(c.e), got, c.want)
		}
	}
	// AN ENTRY THAT SIGNS IN NOWHERE SAYS NOTHING: one that carries a key,
	// and one whose credential is a token file the operator keeps. And a
	// reason never repeats a path the kind is.
	keyed := change(func(e *providerEntry) { e.Credential = "" })
	tokenFile := change(func(e *providerEntry) {
		e.Credential, e.OAuth, e.signIn = "file:/home/op/.tokens/t.json", "", oauth.Provider{}
	})
	for what, e := range map[string]providerEntry{"no credential kind": keyed, "a token file of the operator's": tokenFile} {
		if canSignIn(e) || signInUnmet(e) != "" {
			t.Errorf("an entry with %s: can sign in %v, and says %q; it signs in nowhere and says nothing", what, canSignIn(e), signInUnmet(e))
		}
	}
	if said := signInShortfall(tokenFile); said == "" || strings.Contains(said, "/home/op") {
		t.Errorf("the shortfall of a token-file kind repeats its path, or is none: %q", said)
	}

	// THE BUTTON IS OFFERED TO EXACTLY THE ENTRIES IT WAS OFFERED TO. The
	// judgement as it stood before the reason was added, kept here word
	// for word, against the new one over every combination of what it
	// reads; and a reason is given to every entry with a credential kind
	// that is refused, and to none that is not.
	before := func(e providerEntry) bool {
		if e.Credential == "" || e.CredentialOptions["file"] != "" {
			return false
		}
		if _, err := ownedCredentialFile(e.Credential, e.signIn.CredentialFile); err != nil {
			return false
		}
		p := e.signIn
		if p.ClientID == "" || p.TokenURL == "" {
			return false
		}
		switch p.SignIn {
		case "openai_device":
			return p.DeviceURL != "" && p.DevicePollURL != "" && p.VerificationURI != "" && p.DeviceRedirectURI != "" && p.DeviceExpiresSeconds > 0
		case "device":
			return p.DeviceURL != ""
		case "", "callback", "manual":
			return validateSignInRedirect(p.SignIn, p.RedirectURI) == nil && p.AuthorizeURL != ""
		default:
			return false
		}
	}
	either := func(a, b string) []string { return []string{a, b} }
	n, offered := 0, 0
	for _, kind := range []string{"", "codex", "my-kind", "file:/t.json"} {
		for _, pinned := range either("", "/elsewhere/auth.json") {
			for _, named := range either("", "openai-chatgpt") {
				for _, file := range []string{"", "mine.json", "../mine.json"} {
					for _, method := range []string{"", "callback", "manual", "device", "openai_device", "by-post"} {
						for _, redirect := range []string{"", "http://127.0.0.1:1455/oauth/callback", "https://elsewhere.example/back"} {
							for bits := 0; bits < 1<<8; bits++ {
								on := func(i int, v string) string {
									if bits&(1<<i) != 0 {
										return v
									}
									return ""
								}
								e := providerEntry{Name: "x", Credential: kind, OAuth: named, CredentialOptions: map[string]string{}}
								if pinned != "" {
									e.CredentialOptions["file"] = pinned
								}
								e.signIn = oauth.Provider{CredentialFile: file, SignIn: method, RedirectURI: redirect,
									ClientID: on(0, "c"), TokenURL: on(1, "https://a/token"), AuthorizeURL: on(2, "https://a/authorize"),
									DeviceURL: on(3, "https://a/device"), DevicePollURL: on(4, "https://a/poll"), VerificationURI: on(5, "https://a/verify"),
									DeviceRedirectURI: on(6, "https://a/back"), DeviceExpiresSeconds: bits >> 7 & 1 * 900}
								n++
								was, is, said := before(e), canSignIn(e), signInShortfall(e)
								if is {
									offered++
								}
								if was != is || (kind != "" && is != (said == "")) || (is && signInUnmet(e) != "") {
									t.Fatalf("entry %+v with contract %+v: the button was offered %v and is now offered %v; the shortfall said is %q, and the page is told %q", e, e.signIn, was, is, said, signInUnmet(e))
								}
							}
						}
					}
				}
			}
		}
	}
	if n < 100000 || offered == 0 || offered == n {
		t.Fatalf("rig: %d entries compared, %d of them offered the button", n, offered)
	}

	// The directory the page reads carries it, beside can_sign_in.
	f := newSignInFixture(t, "../../escape")
	if row := f.a.providerDirectory().Providers[0]; row.CanSignIn || !strings.Contains(row.SignInUnmet, "no credential file of this identity's own") {
		t.Fatalf("the directory's row for an entry that cannot sign in: can %v, unmet %q", row.CanSignIn, row.SignInUnmet)
	}
	f = newSignInFixture(t, "codex")
	if row := f.a.providerDirectory().Providers[0]; !row.CanSignIn || row.SignInUnmet != "" {
		t.Fatalf("the directory's row for an entry that can sign in: can %v, unmet %q", row.CanSignIn, row.SignInUnmet)
	}
}
