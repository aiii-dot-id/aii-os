package app

import (
	"bytes"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/oauth"
)

// profileEdit is one custom profile on the fixture authority, with every
// route it offers.
func profileEdit(name string, tsURL string) dashboard.AuthProfileEdit {
	host, _ := url.Parse(tsURL)
	return dashboard.AuthProfileEdit{RedirectURI: "https://dashboard.example/oauth/callback", Name: name, Provider: "custom", ClientID: "cid", ClientSecret: "csecret", Scopes: []string{"repo"},
		Hosts: []string{host.Host}, AuthorizeURL: tsURL + "/authorize", TokenURL: tsURL + "/token", DeviceURL: tsURL + "/device", RevokeURL: tsURL + "/revoke"}
}

// connectProfile signs a profile in through the pasted redirect.
func connectProfile(t *testing.T, a *App, name string) {
	t.Helper()
	u, err := a.SignInProfile(name)
	if err != nil {
		t.Fatal(err)
	}
	au, _ := url.Parse(u)
	if err := a.CompleteProfileSignIn(name, au.Query().Get("redirect_uri")+"?code=the-code&state="+au.Query().Get("state")); err != nil {
		t.Fatalf("complete: %v", err)
	}
}

// pointTokenFile is the operator's hand in config.json: the profile's
// token_file set to what they wrote.
func pointTokenFile(t *testing.T, a *App, name, tokenFile string) {
	t.Helper()
	if err := a.commitAuthProfiles(name, func(m map[string]broker.AuthProfile) {
		p := m[name]
		p.TokenFile = tokenFile
		m[name] = p
	}); err != nil {
		t.Fatal(err)
	}
}

func profileView(t *testing.T, a *App, name string) dashboard.AuthProfileView {
	t.Helper()
	for _, v := range a.authProfileViews(&Config{Plugins: a.configSnapshot().Plugins}) {
		if v.Name == name {
			return v
		}
	}
	t.Fatalf("no view of profile %s", name)
	return dashboard.AuthProfileView{}
}

// The host writes a NAME for a new profile's token file, in the live
// configuration and in the file, and a sign-in writes the tokens at that
// name under the identity's credentials directory — not in the working
// directory, where an earlier version would have read a bare name. The
// floor keeps the file where it is.
func TestANewProfileNamesItsTokenFileAndASignInWritesItUnderTheCredentialsDirectory(t *testing.T) {
	f := &profileAuthority{}
	ts := f.serve(t)
	defer ts.Close()
	a := profileApp(t, ts)
	work := t.TempDir()
	t.Chdir(work)
	if err := a.SetAuthProfile(profileEdit("gh", ts.URL)); err != nil {
		t.Fatal(err)
	}
	if got := a.configSnapshot().Plugins.AuthProfiles["gh"].TokenFile; got != "gh.json" {
		t.Fatalf("a new profile's token_file is a file name, got %q", got)
	}
	persisted, err := LoadConfig(a.configSnapshot().SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if got := persisted.Plugins.AuthProfiles["gh"].TokenFile; got != "gh.json" {
		t.Fatalf("config.json carries the name, got %q", got)
	}
	connectProfile(t, a, "gh")
	dir, err := a.credentialsDir()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "gh.json")
	if st, err := oauth.ReadProfileState(want); err != nil || !st.Connected {
		t.Fatalf("the sign-in wrote %s: %+v %v", want, st, err)
	}
	if _, err := os.Lstat(filepath.Join(work, "gh.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("nothing is written in the working directory: %v", err)
	}
	if v := profileView(t, a, "gh"); v.State != "connected" || v.Borrowed != "" {
		t.Fatalf("the identity's own file is connected and not called borrowed: %+v", v)
	}
	kept := false
	for _, o := range substrateObjects(a.activeConfig(a.configSnapshot()), work) {
		kept = kept || (o.ID == "sub.profile" && o.Path == realPath(want))
	}
	if !kept {
		t.Fatalf("the floor keeps the token file where it is read, %s", realPath(want))
	}
}

// A path an earlier version wrote, absolute and inside the credentials
// directory, stays the identity's own: signed in to, refreshed, removed on
// disconnect with its grant revoked, and left as written by an edit.
func TestATokenFilePathInsideTheCredentialsDirectoryStaysTheIdentitysOwn(t *testing.T) {
	f := &profileAuthority{}
	ts := f.serve(t)
	defer ts.Close()
	a := profileApp(t, ts)
	dir, err := a.credentialsDir()
	if err != nil {
		t.Fatal(err)
	}
	edit := profileEdit("old", ts.URL)
	if err := a.SetAuthProfile(edit); err != nil {
		t.Fatal(err)
	}
	asWritten := filepath.Join(dir, "old.json")
	pointTokenFile(t, a, "old", asWritten)
	edit.ClientSecret, edit.Scopes = "", []string{"repo", "read:user"}
	if err := a.SetAuthProfile(edit); err != nil {
		t.Fatal(err)
	}
	if got := a.configSnapshot().Plugins.AuthProfiles["old"].TokenFile; got != asWritten {
		t.Fatalf("an edit leaves the path as it was written: %q", got)
	}
	connectProfile(t, a, "old")
	if st, err := oauth.ReadProfileState(asWritten); err != nil || !st.Connected {
		t.Fatalf("the sign-in wrote the file where the path says: %+v %v", st, err)
	}
	if v := profileView(t, a, "old"); v.State != "connected" || v.Borrowed != "" {
		t.Fatalf("a path inside is the identity's own: %+v", v)
	}
	if err := a.DisconnectProfile("old"); err != nil {
		t.Fatalf("its own file is disconnected with nothing more to say: %v", err)
	}
	if _, err := os.Stat(asWritten); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("its own file is removed: %v", err)
	}
	f.mu.Lock()
	revoked := append([]string(nil), f.revoked...)
	f.mu.Unlock()
	if len(revoked) != 1 || revoked[0] != "rt-code" {
		t.Fatalf("its own grant is revoked: %v", revoked)
	}
	if got := a.configSnapshot().Plugins.AuthProfiles["old"].TokenFile; got != asWritten {
		t.Fatalf("a disconnect of its own file leaves the path as written: %q", got)
	}
}

// A token file the operator wrote a path to, outside the credentials
// directory, is borrowed. The card says so and where it is. A sign-in to
// it is refused by name. A lapsed one reads expired though it holds a
// refresh token. A disconnect forgets the path, says where the file was
// left, removes the identity's own file the profile had before and
// revokes that grant alone. Through all of it the borrowed file is byte
// for byte what it was. Then the profile signs in to its own.
func TestABorrowedTokenFileIsNeverWrittenRemovedOrRevoked(t *testing.T) {
	f := &profileAuthority{}
	ts := f.serve(t)
	defer ts.Close()
	a := profileApp(t, ts)
	dir, err := a.credentialsDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := a.SetAuthProfile(profileEdit("acct", ts.URL)); err != nil {
		t.Fatal(err)
	}
	// It signed in here once: the identity's own file.
	connectProfile(t, a, "acct")
	own := filepath.Join(dir, "acct.json")
	if _, err := os.Stat(own); err != nil {
		t.Fatal(err)
	}
	// Then token_file was written by hand to another tool's file.
	outside := filepath.Join(t.TempDir(), "their-tool", "token.json")
	if err := oauth.WriteTokenFile(outside, &oauth.Tokens{Access: "at-theirs", Refresh: "rt-theirs", Expires: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	where := realPath(outside)
	want, _ := os.ReadFile(outside)
	unchanged := func(when string) {
		t.Helper()
		got, err := os.ReadFile(outside)
		if err != nil {
			t.Fatalf("%s: the borrowed file is still there: %v", when, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("%s: the borrowed file was rewritten:\n was %s\n now %s", when, want, got)
		}
	}
	pointTokenFile(t, a, "acct", outside)

	v := profileView(t, a, "acct")
	if v.State != "connected" || !strings.Contains(v.Borrowed, where) || !strings.Contains(v.Borrowed, "its own tool") || !strings.Contains(v.Borrowed, "Disconnect") {
		t.Fatalf("the card says the file is borrowed, where it is, and the routes: %+v", v)
	}
	// A second profile names the same borrowed file, at an authority that
	// offers no revocation: its card does not say a disconnect removes
	// the credential from this host, because it does not.
	second := profileEdit("second", ts.URL)
	second.RevokeURL = ""
	if err := a.SetAuthProfile(second); err != nil {
		t.Fatal(err)
	}
	if v := profileView(t, a, "second"); !strings.Contains(v.Revocation, "not available") {
		t.Fatalf("the fixture: a profile of its own at this authority says revocation is not available: %+v", v)
	}
	pointTokenFile(t, a, "second", outside)
	if v := profileView(t, a, "second"); v.State != "connected" || !strings.Contains(v.Borrowed, where) || v.Revocation != "" {
		t.Fatalf("two profiles read one borrowed file, and neither card speaks of removing it: %+v", v)
	}
	if _, err := a.SignInProfile("acct"); !errors.Is(err, errTokenFileBorrowed) || !strings.Contains(err.Error(), where) {
		t.Fatalf("a sign-in to a borrowed file is refused, naming it: %v", err)
	}
	if _, err := a.DeviceSignInProfile("acct"); !errors.Is(err, errTokenFileBorrowed) {
		t.Fatalf("a device sign-in to a borrowed file is refused: %v", err)
	}
	unchanged("after refused sign-ins")

	// Lapsed, with a refresh token in it: this identity does not renew
	// it, so the card does not call it connected.
	if err := oauth.WriteTokenFile(outside, &oauth.Tokens{Access: "at-theirs", Refresh: "rt-theirs", Expires: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	want, _ = os.ReadFile(outside)
	if v := profileView(t, a, "acct"); v.State != "expired" {
		t.Fatalf("a lapsed borrowed token is expired: %+v", v)
	}

	err = a.DisconnectProfile("acct")
	if !errors.Is(err, errTokenFileLeft) || !strings.Contains(err.Error(), where) {
		t.Fatalf("the disconnect says the borrowed file was left, and where: %v", err)
	}
	unchanged("after the disconnect")
	if _, err := os.Stat(own); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the identity's own file the profile had before is removed: %v", err)
	}
	f.mu.Lock()
	revoked := append([]string(nil), f.revoked...)
	f.mu.Unlock()
	if len(revoked) != 1 || revoked[0] != "rt-code" {
		t.Fatalf("only the identity's own grant is revoked, never the borrowed one: %v", revoked)
	}
	if got := a.configSnapshot().Plugins.AuthProfiles["acct"].TokenFile; got != "acct.json" {
		t.Fatalf("the profile forgot the path and names its own file: %q", got)
	}
	persisted, err := LoadConfig(a.configSnapshot().SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if got := persisted.Plugins.AuthProfiles["acct"].TokenFile; got != "acct.json" {
		t.Fatalf("config.json forgot the path: %q", got)
	}
	if v := profileView(t, a, "acct"); v.State != "disconnected" || v.Borrowed != "" {
		t.Fatalf("after the disconnect the profile is disconnected and borrows nothing: %+v", v)
	}
	if got := a.configSnapshot().Plugins.AuthProfiles["second"].TokenFile; got != outside {
		t.Fatalf("disconnecting one profile forgets only its own path; the other names %q", got)
	}
	if v := profileView(t, a, "second"); v.Borrowed == "" {
		t.Fatalf("the other profile still reads the borrowed file: %+v", v)
	}

	// Its own from here on.
	connectProfile(t, a, "acct")
	if st, err := oauth.ReadProfileState(own); err != nil || !st.Connected {
		t.Fatalf("the next sign-in writes the identity's own file: %+v %v", st, err)
	}
	unchanged("after the identity's own sign-in")
}

// Deleting a profile leaves its borrowed token file, and the answer says
// where, since no view is left to say it. Its grant is not revoked.
func TestDeletingAProfileLeavesItsBorrowedTokenFile(t *testing.T) {
	f := &profileAuthority{}
	ts := f.serve(t)
	defer ts.Close()
	a := profileApp(t, ts)
	if err := a.SetAuthProfile(profileEdit("acct", ts.URL)); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "token.json")
	if err := oauth.WriteTokenFile(outside, &oauth.Tokens{Access: "at-theirs", Refresh: "rt-theirs"}); err != nil {
		t.Fatal(err)
	}
	want, _ := os.ReadFile(outside)
	pointTokenFile(t, a, "acct", outside)
	err := a.DeleteAuthProfile("acct")
	if !errors.Is(err, errTokenFileLeft) || !strings.Contains(err.Error(), realPath(outside)) || !strings.Contains(err.Error(), "deleted") {
		t.Fatalf("the delete says the borrowed file was left, and where: %v", err)
	}
	if _, ok := a.configSnapshot().Plugins.AuthProfiles["acct"]; ok {
		t.Fatal("the profile is deleted")
	}
	if got, err := os.ReadFile(outside); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("the borrowed file is as it was: %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.revoked) != 0 {
		t.Fatalf("a borrowed grant is not this identity's to revoke: %v", f.revoked)
	}
}

// A file under the profile's own name that is a link out of the
// credentials directory is judged by where it leads: borrowed. A
// disconnect removes the link, which is the identity's entry, and leaves
// what it leads to.
func TestANameThatIsALinkOutOfTheCredentialsDirectoryIsBorrowed(t *testing.T) {
	f := &profileAuthority{}
	ts := f.serve(t)
	defer ts.Close()
	a := profileApp(t, ts)
	dir, err := a.credentialsDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := a.SetAuthProfile(profileEdit("acct", ts.URL)); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "token.json")
	if err := oauth.WriteTokenFile(outside, &oauth.Tokens{Access: "at-theirs", Refresh: "rt-theirs"}); err != nil {
		t.Fatal(err)
	}
	want, _ := os.ReadFile(outside)
	link := filepath.Join(dir, "acct.json")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("this account cannot make a symlink: %v", err)
	}
	if v := profileView(t, a, "acct"); v.State != "connected" || !strings.Contains(v.Borrowed, realPath(outside)) {
		t.Fatalf("a name that leads out of the directory is borrowed: %+v", v)
	}
	if _, err := a.SignInProfile("acct"); !errors.Is(err, errTokenFileBorrowed) {
		t.Fatalf("a sign-in through the link is refused: %v", err)
	}
	if err := a.DisconnectProfile("acct"); !errors.Is(err, errTokenFileLeft) {
		t.Fatalf("the disconnect says the file was left: %v", err)
	}
	if got, err := os.ReadFile(outside); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("what the link led to is as it was: %v", err)
	}
	if _, err := os.Lstat(link); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the link, the identity's own entry, is removed: %v", err)
	}
	if v := profileView(t, a, "acct"); v.State != "disconnected" || v.Borrowed != "" {
		t.Fatalf("the profile ends disconnected, with a name of its own: %+v", v)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.revoked) != 0 {
		t.Fatalf("nothing of the borrowed grant is revoked: %v", f.revoked)
	}
}

func TestADisconnectedBorrowerTakesANameNoOtherProfileUses(t *testing.T) {
	f := &profileAuthority{}
	ts := f.serve(t)
	defer ts.Close()
	a := profileApp(t, ts)
	dir, err := a.credentialsDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"acct", "other"} {
		if err := a.SetAuthProfile(profileEdit(n, ts.URL)); err != nil {
			t.Fatal(err)
		}
	}
	// The other profile's file stands at the name the borrower would take.
	pointTokenFile(t, a, "other", "acct.json")
	connectProfile(t, a, "other")
	theirs := filepath.Join(dir, "acct.json")
	before, err := os.ReadFile(theirs)
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "tool", "token.json")
	if err := oauth.WriteTokenFile(outside, &oauth.Tokens{Access: "at", Refresh: "rt-theirs", Expires: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	pointTokenFile(t, a, "acct", outside)

	if err := a.DisconnectProfile("acct"); !errors.Is(err, errTokenFileLeft) {
		t.Fatalf("the disconnect says the borrowed file was left: %v", err)
	}
	if got := a.configSnapshot().Plugins.AuthProfiles["acct"].TokenFile; got != "acct-2.json" {
		t.Fatalf("the borrower took %q; acct.json is the other profile's file", got)
	}
	if after, err := os.ReadFile(theirs); err != nil || !bytes.Equal(after, before) {
		t.Fatalf("the other profile's file was touched: %v", err)
	}
	f.mu.Lock()
	revoked := append([]string(nil), f.revoked...)
	f.mu.Unlock()
	if len(revoked) != 0 {
		t.Fatalf("another profile's grant was revoked: %v", revoked)
	}
}
