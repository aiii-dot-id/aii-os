package app

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/oauth"
)

// A replacement name can still name someone else's token through a hard
// link. Removing that name empties the shared file (atomicfile.Remove),
// so neither another profile's token nor the borrowed token may be chosen.
func TestADisconnectedBorrowerLeavesHardLinkedTokensAlone(t *testing.T) {
	for _, owner := range []string{"another profile", "the borrowed file"} {
		t.Run(owner, func(t *testing.T) {
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
			outside := filepath.Join(t.TempDir(), "borrowed.json")
			if err := oauth.WriteTokenFile(outside, &oauth.Tokens{Access: "at-theirs", Refresh: "rt-theirs", Expires: time.Now().Add(time.Hour)}); err != nil {
				t.Fatal(err)
			}
			pointTokenFile(t, a, "acct", outside)
			protected := outside
			if owner == "another profile" {
				if err := a.SetAuthProfile(profileEdit("other", ts.URL)); err != nil {
					t.Fatal(err)
				}
				connectProfile(t, a, "other")
				protected = filepath.Join(dir, "other.json")
			}
			alias := filepath.Join(dir, "acct.json")
			if err := os.Link(protected, alias); err != nil {
				t.Fatalf("make the token's second name: %v", err)
			}
			before := map[string][]byte{}
			for _, path := range []string{outside, protected, alias} {
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				before[path] = raw
			}
			if err := a.DisconnectProfile("acct"); !errors.Is(err, errTokenFileLeft) {
				t.Fatalf("disconnect must say the borrowed file was left: %v", err)
			}
			if got := a.configSnapshot().Plugins.AuthProfiles["acct"].TokenFile; got != "acct-2.json" {
				t.Errorf("the replacement must avoid the hard link, got %q", got)
			}
			for path, want := range before {
				if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, want) {
					t.Errorf("disconnect changed a token belonging to %s at %s: %v", owner, path, err)
				}
			}
			f.mu.Lock()
			revoked := append([]string(nil), f.revoked...)
			f.mu.Unlock()
			if len(revoked) != 0 {
				t.Errorf("disconnect revoked someone else's token: %v", revoked)
			}
			if t.Failed() {
				return
			}
			connectProfile(t, a, "acct")
			for path, want := range before {
				if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, want) {
					t.Errorf("the next sign-in changed a token belonging to %s at %s: %v", owner, path, err)
				}
			}
		})
	}
}

// A failed file-identity check is no evidence that a token is unrelated.
// Refuse before saving the replacement name or removing any credential.
func TestABorrowerDisconnectRefusesAnUnreadableFileIdentity(t *testing.T) {
	f := &profileAuthority{}
	ts := f.serve(t)
	defer ts.Close()
	a := profileApp(t, ts)
	for _, name := range []string{"acct", "other"} {
		if err := a.SetAuthProfile(profileEdit(name, ts.URL)); err != nil {
			t.Fatal(err)
		}
	}
	connectProfile(t, a, "acct")
	dir, err := a.credentialsDir()
	if err != nil {
		t.Fatal(err)
	}
	own := filepath.Join(dir, "acct.json")
	outside := filepath.Join(t.TempDir(), "borrowed.json")
	if err := oauth.WriteTokenFile(outside, &oauth.Tokens{Access: "at-theirs", Refresh: "rt-theirs", Expires: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	pointTokenFile(t, a, "acct", outside)
	// Require an actual metadata inspection error after TokenPath resolution.
	// Windows rejects NUL earlier in filepath.Abs; '<' reaches Stat instead.
	bad := outside + "\x00"
	if runtime.GOOS == "windows" {
		bad = outside + "<"
	}
	resolved, _ := (broker.AuthProfile{TokenFile: bad}).TokenPath(dir)
	if _, err := os.Stat(resolved); resolved == "" || err == nil || errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fixture must reach unreadable metadata: path=%q err=%v", resolved, err)
	}
	pointTokenFile(t, a, "other", bad)
	before := map[string][]byte{}
	for _, path := range []string{own, outside, a.configSnapshot().SourcePath} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		before[path] = raw
	}
	err = a.DisconnectProfile("acct")
	var pathErr *os.PathError
	if !errors.As(err, &pathErr) || errors.Is(err, errTokenFileLeft) {
		t.Fatalf("the failed identity check must refuse the disconnect: %v", err)
	}
	if got := a.configSnapshot().Plugins.AuthProfiles["acct"].TokenFile; got != outside {
		t.Errorf("the refused disconnect changed the live profile to %q", got)
	}
	for path, want := range before {
		if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, want) {
			t.Errorf("the refused disconnect changed %s: %v", path, err)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.revoked) != 0 {
		t.Errorf("the refused disconnect revoked tokens: %v", f.revoked)
	}
}
