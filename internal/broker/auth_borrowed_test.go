package broker

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

// A token file is the host's own only where it resolves inside the
// profiles directory: a name is kept there; a path is judged by where it
// leads once its links are followed and its ".." folded, and the path
// handed back is that one. Everything else is borrowed.
func TestATokenFileIsTheHostsOwnOnlyWhereItResolvesInsideTheProfilesDirectory(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "data", "credentials", "profiles")
	outside := filepath.Join(root, "elsewhere")
	for _, d := range []string{dir, outside} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{filepath.Join(dir, "own.json"), filepath.Join(outside, "theirs.json"), filepath.Join(root, "bare.json")} {
		if err := os.WriteFile(f, []byte(`{}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// What the rule compares: both sides as the filesystem names them (a
	// temporary directory is itself reached through a link on macOS).
	physical := func(p string) string {
		r, err := filepath.EvalSymlinks(p)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	realDir, realOutside := physical(dir), physical(outside)
	// A relative path is taken from the working directory, as it always was.
	t.Chdir(root)

	type tc struct {
		name, tokenFile, dir string
		path                 string
		owned                bool
	}
	cases := []tc{
		{"no token file", "", dir, "", false},
		{"a name is kept under the directory", "own.json", dir, filepath.Join(realDir, "own.json"), true},
		{"a name not yet written is the host's own too", "new.json", dir, filepath.Join(realDir, "new.json"), true},
		{"a name is not read from the working directory", "bare.json", dir, filepath.Join(realDir, "bare.json"), true},
		{"a name with no directory to keep it in is nobody's", "own.json", "", "", false},
		{"an absolute path inside, as an earlier version wrote it", filepath.Join(dir, "own.json"), dir, filepath.Join(realDir, "own.json"), true},
		{"an absolute path outside", filepath.Join(outside, "theirs.json"), dir, filepath.Join(realOutside, "theirs.json"), false},
		{"an absolute path with no directory to compare", filepath.Join(dir, "own.json"), "", filepath.Join(realDir, "own.json"), false},
		{"dot-dot leading out", filepath.Join(dir, "..", "..", "..", "elsewhere", "theirs.json"), dir, filepath.Join(realOutside, "theirs.json"), false},
		{"dot-dot leading back in", filepath.Join(outside, "..", "data", "credentials", "profiles", "own.json"), dir, filepath.Join(realDir, "own.json"), true},
		{"a relative path inside, from the working directory", filepath.Join("data", "credentials", "profiles", "own.json"), dir, filepath.Join(realDir, "own.json"), true},
		{"a relative path outside, from the working directory", filepath.Join("elsewhere", "theirs.json"), dir, filepath.Join(realOutside, "theirs.json"), false},
		{"the directory itself is not a file in it", dir, dir, realDir, false},
		{"dot is a path", ".", dir, physical(root), false},
		{"another volume's path is a path, never a name", `C:\Users\op\token.json`, dir, "", false},
	}
	// Links, where this account may make them.
	linkOut := filepath.Join(dir, "linked.json")
	linkIn := filepath.Join(root, "tokens")
	if err := os.Symlink(filepath.Join(outside, "theirs.json"), linkOut); err != nil {
		t.Logf("no symlinks here, the link cases are not run: %v", err)
	} else {
		if err := os.Symlink(dir, linkIn); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(outside, "not-there.json"), filepath.Join(dir, "dangling.json")); err != nil {
			t.Fatal(err)
		}
		dangling := tc{"a link to nothing is the entry itself", "dangling.json", dir, filepath.Join(realDir, "dangling.json"), true}
		if runtime.GOOS == "windows" {
			// Windows resolves even a missing reparse target; the same
			// resolver that protects file tools also determines custody.
			dangling = tc{"a dangling Windows link leads outside", "dangling.json", dir, filepath.Join(realOutside, "not-there.json"), false}
		}
		cases = append(cases,
			tc{"a name that is a link out of the directory", "linked.json", dir, filepath.Join(realOutside, "theirs.json"), false},
			tc{"a path inside that is a link out", linkOut, dir, filepath.Join(realOutside, "theirs.json"), false},
			tc{"a path that is inside only through a link", filepath.Join(linkIn, "own.json"), dir, filepath.Join(realDir, "own.json"), true},
			tc{"the directory named through a link", "own.json", linkIn, filepath.Join(realDir, "own.json"), true},
			dangling,
		)
	}
	for _, c := range cases {
		path, owned := AuthProfile{TokenFile: c.tokenFile}.TokenPath(c.dir)
		if owned != c.owned {
			t.Errorf("%s: token_file %q is the host's own = %v, want %v (resolved %s)", c.name, c.tokenFile, owned, c.owned, path)
		}
		if strings.Contains(c.tokenFile, `C:\`) {
			// Where it leads depends on the platform; that it is not a
			// name under the directory does not.
			if runtime.GOOS != "windows" && strings.HasPrefix(path, realDir) {
				t.Errorf("%s: read as a name under the directory: %s", c.name, path)
			}
			continue
		}
		if path != c.path {
			t.Errorf("%s: token_file %q resolves to %s, want %s", c.name, c.tokenFile, path, c.path)
		}
	}
}

// A profile whose token file lies outside the profiles directory is
// borrowed: it is read and rides while it is good; when it nears expiry,
// or the API rejects it, nothing is asked of the authority, the denial
// says whose it is to renew, the receipt says no refresh was made, and
// the file is byte for byte what it was. The same file under the
// profiles directory is refreshed and rewritten.
func TestABorrowedTokenFileRidesAndIsNeverRefreshedOrRewritten(t *testing.T) {
	f := &fakeAuthority{current: "at-0"}
	ts := httptest.NewTLSServer(f.handler(t))
	defer ts.Close()
	host, port := tsHostPort(t, ts)
	hostPort := fmt.Sprintf("%s:%d", host, port)
	dir := t.TempDir()
	profiles := filepath.Join(dir, "data", "credentials", "profiles")
	borrowed := filepath.Join(dir, "elsewhere", "token.json")
	secretFile := filepath.Join(dir, "x.client")
	if err := os.WriteFile(secretFile, []byte("csecret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	profile := func(tokenFile string) AuthProfile {
		return AuthProfile{Scheme: SchemeOAuth2, Provider: "custom", ClientID: "cid", ClientSecretFile: secretFile,
			TokenURL: ts.URL + "/token", Hosts: []string{hostPort}, TokenFile: tokenFile}
	}
	st := newStore(t)
	grants := map[string]Grant{"p": {Hosts: []string{hostPort}, CredentialHandles: []string{"theirs", "ours"}}}
	table := map[string]AuthProfile{"theirs": profile(borrowed), "ours": profile("ours.json")}
	h := newHost(t, st, Config{Grants: grants, AuthProfiles: table, ProfilesDir: profiles, Guard: guardFor(ts), Transport: ts.Client().Transport})
	envelope := []string{"net.outbound:" + hostPort}
	api := ts.URL + "/api/x"
	unchanged := func(when string, want []byte) {
		t.Helper()
		got, err := os.ReadFile(borrowed)
		if err != nil {
			t.Fatalf("%s: the borrowed file is still there: %v", when, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("%s: the borrowed file was rewritten:\n was %s\n now %s", when, want, got)
		}
	}

	// Good: it is read and rides.
	writeTokens(t, borrowed, "at-0", "rt-theirs", time.Now().Add(2*time.Hour))
	good, _ := os.ReadFile(borrowed)
	b := h.Bind("p", packagefmt.TierT2, envelope)
	wantResult(t, dispatch(t, b, netParams(api, `{"auth_profile":"theirs"}`)), statusSucceeded, "")
	unchanged("after a call", good)

	// Rejected by the API: reread, never renewed.
	f.mu.Lock()
	f.current = "at-rotated-by-its-owner"
	f.mu.Unlock()
	m := dispatch(t, b, netParams(api, `{"auth_profile":"theirs"}`))
	wantResult(t, m, statusDenied, reasonAuthDisconnected)
	if whole, _ := json.Marshal(m); !strings.Contains(string(whole), "borrowed token file") || !strings.Contains(string(whole), "its own tool") || strings.Contains(string(whole), "Plugins page") {
		t.Fatalf("the denial says whose the token is to renew: %s", whole)
	}
	unchanged("after a rejected call", good)

	// Near expiry: refused before anything is dialled.
	writeTokens(t, borrowed, "at-old", "rt-theirs", time.Now().Add(-time.Minute))
	lapsed, _ := os.ReadFile(borrowed)
	h.ReplacePolicy(grants, table)
	b = h.Bind("p", packagefmt.TierT2, envelope)
	before := f.apiCalls.Load()
	m = dispatch(t, b, netParams(api, `{"auth_profile":"theirs"}`))
	wantResult(t, m, statusDenied, reasonAuthDisconnected)
	if whole, _ := json.Marshal(m); !strings.Contains(string(whole), "borrowed token file") {
		t.Fatalf("the denial says the file is borrowed: %s", whole)
	}
	if f.apiCalls.Load() != before {
		t.Fatal("a lapsed borrowed token dials nothing")
	}
	unchanged("after a lapsed call", lapsed)
	f.mu.Lock()
	issued := f.issued
	f.mu.Unlock()
	if issued != 0 {
		t.Fatalf("the authority was asked to refresh a borrowed token %d time(s)", issued)
	}
	recs, err := st.PluginReceipts("p")
	if err != nil {
		t.Fatal(err)
	}
	said := 0
	for _, r := range recs {
		if r.Operation != opAuthRefresh {
			continue
		}
		if !strings.Contains(string(r.ReceiptJSON), `"detail":"not refreshed: the token file is borrowed"`) {
			t.Fatalf("a receipt claims a refresh of a borrowed token: %s", r.ReceiptJSON)
		}
		said++
	}
	if said == 0 {
		t.Fatal("no receipt says the borrowed token was not refreshed")
	}

	// The same lapsed file by name under the profiles directory is the
	// host's own: refreshed once, and rewritten.
	own := filepath.Join(profiles, "ours.json")
	writeTokens(t, own, "at-old", "rt-ours", time.Now().Add(-time.Minute))
	was, _ := os.ReadFile(own)
	wantResult(t, dispatch(t, b, netParams(api, `{"auth_profile":"ours"}`)), statusSucceeded, "")
	now, _ := os.ReadFile(own)
	f.mu.Lock()
	issued = f.issued
	f.mu.Unlock()
	if issued != 1 || bytes.Equal(was, now) {
		t.Fatalf("the host's own token is refreshed and rewritten: issued %d, rewritten %v", issued, !bytes.Equal(was, now))
	}
	unchanged("after the host's own refresh", lapsed)
}
