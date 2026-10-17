package firewall

import (
	"strings"
	"testing"
)

// spotlightLine is the profile's last line: the Spotlight metadata server's
// global names, by their prefix, refused on and off (shellDeniedServicePrefix).
const spotlightLine = `(deny mach-lookup (global-name-prefix "com.apple.metadata.mds"))`

func TestShellProfileIsTheTrialsShape(t *testing.T) {
	in := ShellWallInput{
		Kept: []*Rule{
			{ID: "sub.data", Path: "/h/data", Except: []string{"/h/data/ui"}},
			{ID: "sub.config", Path: "/h/config.json"},
			{ID: "sub.later", Path: "/h/later"},
			{ID: "sub.update", Path: "/opt/app/update-", Stem: true},
			{ID: "sub.logs", Path: "/var/log/aii", ReadOpen: true},
		},
	}
	got, err := ShellProfile(in)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		`(version 1)`,
		`(allow default)`,
		`(deny file-read* file-write* (subpath "/h/data"))`,
		`(deny file-read* file-write* (subpath "/h/config.json"))`,
		`(deny file-read* file-write* (subpath "/h/later"))`,
		`(deny file-read* file-write* (prefix "/opt/app/update-"))`,
		`(deny file-write* (subpath "/var/log/aii"))`,
		`(allow file-read* file-write* (subpath "/h/data/ui"))`,
		`(allow file-read-metadata (literal "/h/data"))`,
		`(deny file-write-unlink (literal "/"))`,
		`(deny file-write-unlink (literal "/h"))`,
		`(deny file-write-unlink (literal "/opt"))`,
		`(deny file-write-unlink (literal "/opt/app"))`,
		`(deny file-write-unlink (literal "/var"))`,
		`(deny file-write-unlink (literal "/var/log"))`,
		`(deny lsopen) (deny appleevent-send) (deny job-creation)`,
		spotlightLine,
		``,
	}, "\n")
	if got != want {
		t.Errorf("profile text differs.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// With the boundary (row 2, the sandbox on) the profile adds the write
// deny and the home, roots, temp and device write allows BEFORE the kept
// denies, so a kept place beats the home's own write allow (12h).
func TestShellProfileWithTheBoundaryAddsTheWriteDenyFirst(t *testing.T) {
	in := ShellWallInput{
		Home:     "/h",
		Roots:    []string{"/grant"},
		Boundary: true,
		Temp:     []string{"/private/tmp", "/private/var/folders/x/y/T"},
		Kept:     []*Rule{{ID: "sub.config", Path: "/h/config.json"}},
	}
	got, err := ShellProfile(in)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		`(version 1)`,
		`(allow default)`,
		`(deny file-write*)`,
		`(allow file-write* (subpath "/h") (subpath "/grant"))`,
		`(allow file-write* (subpath "/private/tmp") (subpath "/private/var/folders/x/y/T"))`,
		`(allow file-write* (literal "/dev/null") (literal "/dev/zero") (literal "/dev/tty") (literal "/dev/dtracehelper") (regex #"^/dev/fd/") (regex #"^/dev/ttys"))`,
		`(deny file-read* file-write* (subpath "/h/config.json"))`,
		`(deny file-write-unlink (literal "/"))`,
		`(deny file-write-unlink (literal "/h"))`,
		`(deny lsopen) (deny appleevent-send) (deny job-creation)`,
		spotlightLine,
		``,
	}, "\n")
	if got != want {
		t.Errorf("boundary profile differs.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// With the boundary off (the sandbox off) the write deny is not emitted:
// the shell writes anywhere but a kept place (row 5).
func TestShellProfileOffHasNoWriteBoundary(t *testing.T) {
	in := ShellWallInput{Home: "/h", Boundary: false, Kept: []*Rule{{ID: "c", Path: "/h/config.json"}}}
	got, err := ShellProfile(in)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "(deny file-write*)") {
		t.Errorf("with the boundary off there must be no write deny:\n%s", got)
	}
}

// A kept place whose resolved path Seatbelt's string reader cannot spell
// leaves that place open, so the profile is refused and, above, the shell
// is not run (row 12, fail closed).
func TestShellProfileRefusesAnUnspellablePlace(t *testing.T) {
	in := ShellWallInput{Kept: []*Rule{{ID: "sub.bad", Path: "/h/bad\xffname"}}}
	if got, err := ShellProfile(in); err == nil || got != "" {
		t.Fatalf("an unspellable kept place must refuse the profile; got %q, %v", got, err)
	}
}

// No kept place: the deny-default of the profile is still a profile — the
// version line, nothing to keep, row 3. (The home-alone case of a
// hand-built shell tool.)
func TestShellProfileWithNoPlacesIsStillAProfile(t *testing.T) {
	got, err := ShellProfile(ShellWallInput{})
	if err != nil {
		t.Fatal(err)
	}
	want := "(version 1)\n(allow default)\n(deny lsopen) (deny appleevent-send) (deny job-creation)\n" + spotlightLine + "\n"
	if got != want {
		t.Errorf("empty profile differs:\n%q", got)
	}
}

// THE PROFILE IS DARWIN TEXT ON EVERY BUILDER. The ancestor walk and the
// metadata literals are slash paths whatever system runs the builder: on a
// Windows guest they were walked with filepath and came out as
// (literal "\\h") for "/h", which no Mac's Seatbelt would ever match.
func TestTheShellProfilesPathsAreSlashPathsOnEveryHost(t *testing.T) {
	got := sbplAncestors("/opt/app/data/ledger.jsonl")
	if want := []string{"/opt/app/data", "/opt/app", "/opt", "/"}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("the ancestor walk: %q, want %q", got, want)
	}
	for _, a := range got {
		if strings.Contains(a, `\`) {
			t.Fatalf("an ancestor is not a slash path: %q", a)
		}
	}
	profile, err := ShellProfile(ShellWallInput{
		Home:     "/h",
		Boundary: true,
		Temp:     []string{"/private/tmp"},
		Kept:     []*Rule{{ID: "sub.data", Path: "/h/data", Except: []string{"/h/data/ui/name"}}, {ID: "sub.config", Path: "/h/config/config.json"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(profile, `\`) {
		t.Fatalf("the profile carries a backslash:\n%s", profile)
	}
	for _, want := range []string{
		`(deny file-write-unlink (literal "/"))`,
		`(deny file-write-unlink (literal "/h"))`,
		`(deny file-write-unlink (literal "/h/config"))`,
		`(allow file-read-metadata (literal "/h/data"))`,
		`(allow file-read-metadata (literal "/h/data/ui"))`,
	} {
		if !strings.Contains(profile, want) {
			t.Errorf("the profile lacks %s:\n%s", want, profile)
		}
	}
}

func TestShellProfileHoldsTheNodesOnTheNamedPath(t *testing.T) {
	got, err := ShellProfile(ShellWallInput{
		Home: "/h",
		Kept: []*Rule{{ID: "sub.identity", Path: "/real/slot"}},
		Held: []string{"/h/links/slots", "/h/links", "/h", "/"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`(deny file-write-unlink (literal "/h/links/slots"))`,
		`(deny file-write-unlink (literal "/h/links"))`,
		`(deny file-write-unlink (literal "/real"))`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the profile lacks %s:\n%s", want, got)
		}
	}
	if strings.Count(got, `(deny file-write-unlink (literal "/"))`) != 1 {
		t.Errorf("a node both held and an ancestor is written more than once:\n%s", got)
	}
}

func TestAPathThroughAnUnreadableLinkIsRefused(t *testing.T) {
	if r := unplacedAt("/h/j/x", unresolvable+"/h/j/x"); r == nil || r.Kind != KindUnplaced || !strings.Contains(r.Reason, "cannot be read") {
		t.Fatalf("a path through an unreadable link was not refused: %+v", r)
	}
	if r := unplacedAt("/h/x", "/h/x"); r != nil {
		t.Fatalf("an ordinary path was refused: %+v", r)
	}
}
