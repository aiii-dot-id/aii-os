//go:build darwin

package tools

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
)

// dataVolume is where macOS also shows the data volume's directories: a
// second path to each of them that is not a symbolic link. /private,
// /Users, /Volumes, /Applications, /Library, /opt and /usr/local are each
// shown there a second time; the homes below are made under /private, and
// under whatever the test's own temporary directory is. It is asked of the
// system, as the mount point of the volume that holds /private/var/tmp, and
// is empty where that volume is the root: the second spelling is then the
// first, and each home skips, saying so.
var dataVolume = mountPointOf("/private/var/tmp")

// mountPointOf is the mount point of the volume that holds dir, as the
// system names it, or "" where that is the root or cannot be asked.
func mountPointOf(dir string) string {
	var st syscall.Statfs_t
	if syscall.Statfs(dir, &st) != nil {
		return ""
	}
	b := make([]byte, 0, len(st.Mntonname))
	for _, c := range st.Mntonname {
		if c == 0 {
			break
		}
		b = append(b, byte(c))
	}
	if m := string(b); m != "/" {
		return m
	}
	return ""
}

// spelled is a home made where the host shows it a second time, with the
// places a stock host keeps, each of them there, and three more that are
// not made yet. Nothing the host keeps directly in the home is missing,
// so the home itself is the nearest thing that exists above the two
// deepest of the three, and nothing else answers for them.
type spelled struct {
	r                            *Registry
	policy                       *firewall.Policy
	home, data, secret, snapshot string
	// one, two and three are places not made yet: the first in a
	// directory that is there, the others one and two directories
	// further from anything that is.
	one, two, three string
}

// onEachSecondSpelledHome runs test in a home under each parent this host
// shows a second time under the data volume: the two temporary
// directories of the internal disk, where the test makes and removes a
// directory of its own, and the test's own temporary directory, wherever
// that is. A parent the host does not show twice is skipped, saying so,
// and a run in which every home skipped fails: it asked nothing.
func onEachSecondSpelledHome(t *testing.T, test func(t *testing.T, s spelled)) {
	ran := false
	for name, parent := range map[string]string{"private-var-tmp": "/private/var/tmp", "private-tmp": "/private/tmp", "the-tests-own": t.TempDir()} {
		t.Run(name, func(t *testing.T) {
			defer func() { ran = ran || !t.Skipped() }()
			parent, err := filepath.EvalSymlinks(parent)
			if err != nil {
				t.Skipf("no such directory here: %v", err)
			}
			base, err := os.MkdirTemp(parent, "aii-second-spelling-")
			if err != nil {
				t.Skipf("no directory can be made in %s: %v", parent, err)
			}
			t.Cleanup(func() { os.RemoveAll(base) })
			s := spelled{home: filepath.Join(base, "home")}
			s.data = filepath.Join(s.home, "data")
			s.secret = filepath.Join(s.data, "credentials", "anthropic.json")
			s.snapshot = filepath.Join(s.data, "snapshot.key")
			s.one = filepath.Join(s.home, "made", "token.json")
			s.two = filepath.Join(s.home, "vault", "inner", "token.json")
			s.three = filepath.Join(s.home, "a", "b", "c", "token.json")
			for f, body := range map[string]string{
				s.secret:   "KEPT SECRET\n",
				s.snapshot: "KEPT SECRET\n",
				filepath.Join(s.data, "ui", "frame.html"):  "the frame, which is open\n",
				filepath.Join(s.data, "ui2", "frame.html"): "KEPT SECRET\n",
				filepath.Join(s.home, "made", "other.md"):  "beside a place not made yet\n",
				filepath.Join(s.home, "notes.md"):          "a note of the home\n",
				filepath.Join(s.home, "config.json"):       "KEPT SECRET\n",
				filepath.Join(s.home, "providers.json"):    "KEPT SECRET\n",
				filepath.Join(s.home, "conf", "settings"):  "KEPT SECRET\n",
			} {
				if err := os.MkdirAll(filepath.Dir(f), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			one, err1 := os.Stat(s.home)
			two, err2 := os.Stat(dataVolume + s.home)
			if err1 != nil || err2 != nil || !os.SameFile(one, two) {
				t.Skipf("this host does not show %s a second time under %s: %v %v", s.home, dataVolume, err1, err2)
			}
			if resolved, err := filepath.EvalSymlinks(dataVolume + s.home); err != nil || resolved == s.home {
				t.Skipf("the second spelling resolves to the first here (%q, %v): nothing to test", resolved, err)
			}
			places := stockPlaces(s.home, filepath.Join(s.home, "conf", "settings"))
			for i, p := range []string{s.one, s.two, s.three} {
				places = append(places, &firewall.Rule{ID: "sub.unmade." + strconv.Itoa(i+1), Kind: firewall.KindSubstrate, Path: p, Reason: "kept", Enforced: true})
			}
			s.policy = firewall.DefaultPolicy()
			s.policy.SetPlaces(places)
			s.r = NewRegistry(s.home, s.policy, Timeouts{})
			// The first spelling is refused: the control.
			if res := modelCall(t, s.r, "read", map[string]any{"file_path": s.secret}); !res.Refused || !strings.Contains(res.Error, "protected path") {
				t.Fatalf("by its own spelling the credential is refused: %+v", res)
			}
			// The home by its second spelling is the home: this is as intended.
			if res := modelCall(t, s.r, "read", map[string]any{"file_path": dataVolume + filepath.Join(s.home, "notes.md")}); !strings.Contains(res.Output, "a note of the home") {
				t.Fatalf("the home's second spelling is the home, and what is open in it stays open: %+v", res)
			}
			test(t, s)
			for _, f := range []string{s.secret, s.snapshot, filepath.Join(s.home, "config.json"), filepath.Join(s.home, "providers.json"), filepath.Join(s.home, "conf", "settings")} {
				if b, err := os.ReadFile(f); err != nil || string(b) != "KEPT SECRET\n" {
					t.Errorf("%s was changed: %q %v", f, b, err)
				}
			}
			for _, f := range []string{s.one, s.two, s.three, filepath.Join(s.data, "credentials", "forged.json")} {
				if _, err := os.Stat(f); err == nil {
					t.Errorf("%s, a kept place that was not there, was made", f)
				}
			}
		})
	}
	if !ran {
		t.Fatal("no home ran: every home skipped, so nothing was asked")
	}
}

// opened reports a result that shows a kept file's bytes or its name.
func opened(res Result) bool {
	return strings.Contains(res.Output, "KEPT SECRET") || strings.Contains(res.Output, "anthropic.json") || strings.Contains(res.Output, "snapshot.key")
}

// A KEPT PLACE IS KEPT BY WHAT IT IS, NOT BY HOW ITS PATH IS SPELLED. On
// macOS every directory of the data volume has a second path that no
// resolution of links folds into the first: /private/var/tmp/x and the same
// path under the data volume's mount point are one directory. The sandbox
// admits the second spelling of the home as the home (it asks whether the
// two are one file), so the floor must know the second spelling of a kept
// place as that place: the data directory, with its credentials and the
// snapshot key in it. Read, grep, ls, write, edit and delete are each
// tried.
func TestAKeptPlaceIsKeptUnderTheDataVolumesSpellingOfItsPath(t *testing.T) {
	onEachSecondSpelledHome(t, func(t *testing.T, s spelled) {
		for name, c := range map[string]struct {
			tool string
			args map[string]any
		}{
			"read of the credential":    {"read", map[string]any{"file_path": dataVolume + s.secret}},
			"read of the snapshot key":  {"read", map[string]any{"file_path": dataVolume + s.snapshot}},
			"read of a kept file":       {"read", map[string]any{"file_path": dataVolume + filepath.Join(s.home, "conf", "settings")}},
			"edit of a kept file":       {"edit", map[string]any{"file_path": dataVolume + filepath.Join(s.home, "conf", "settings"), "old_string": "KEPT", "new_string": "SPRAY"}},
			"read, in another case too": {"read", map[string]any{"file_path": dataVolume + s.home + strings.ToUpper(strings.TrimPrefix(s.secret, s.home))}},
			"grep of the credentials":   {"grep", map[string]any{"pattern": "KEPT", "path": dataVolume + filepath.Dir(s.secret)}},
			"grep from the home":        {"grep", map[string]any{"pattern": "KEPT", "path": dataVolume + s.home}},
			"ls of the data directory":  {"ls", map[string]any{"path": dataVolume + s.data, "recursive": true}},
			"write over the credential": {"write", map[string]any{"file_path": dataVolume + s.secret, "content": "SPRAY"}},
			"write of a new credential": {"write", map[string]any{"file_path": dataVolume + filepath.Join(s.data, "credentials", "forged.json"), "content": "SPRAY"}},
			"edit of the snapshot key":  {"edit", map[string]any{"file_path": dataVolume + s.snapshot, "old_string": "KEPT", "new_string": "SPRAY"}},
			"delete of the credential":  {"write", map[string]any{"file_path": dataVolume + s.secret, "action": "delete"}},
		} {
			res := modelCall(t, s.r, c.tool, c.args)
			if name == "grep from the home" {
				// The home is open; what the walk reaches in a kept place is not.
				if opened(res) {
					t.Errorf("%s: the walk read or named a kept file by the second spelling: %+v", name, res)
				}
				continue
			}
			if !res.Refused || !strings.Contains(res.Error, "protected path") || opened(res) {
				t.Errorf("%s by the second spelling must be refused as a protected path: refused=%v error=%q output=%q", name, res.Refused, res.Error, res.Output)
			}
		}
	})
}

func TestAPlaceNotMadeYetIsKeptUnderTheDataVolumesSpellingAtAnyDepth(t *testing.T) {
	onEachSecondSpelledHome(t, func(t *testing.T, s spelled) {
		for name, place := range map[string]string{
			"its directory is there": s.one,
			"two levels unmade":      s.two,
			"three levels unmade":    s.three,
		} {
			for _, path := range []string{place, dataVolume + place} {
				if res := modelCall(t, s.r, "write", map[string]any{"file_path": path, "content": "FORGED"}); !res.Refused || !strings.Contains(res.Error, "protected path") {
					t.Errorf("a place not made yet (%s) written as %s must be refused as a protected path: %+v", name, path, res)
				}
			}
		}
		// What lies beside such a place is the home's, by either spelling.
		for _, beside := range []string{filepath.Join(s.home, "made", "other.md"), filepath.Join(s.home, "vault", "inner", "other.json")} {
			if res := modelCall(t, s.r, "write", map[string]any{"file_path": dataVolume + beside, "content": "a note"}); res.Refused || res.Error != "" {
				t.Errorf("%s is no kept place and is written by the second spelling: %+v", beside, res)
			}
		}
		// And with its directories now made, the place is kept still.
		if res := modelCall(t, s.r, "write", map[string]any{"file_path": dataVolume + s.two, "content": "FORGED"}); !res.Refused || !strings.Contains(res.Error, "protected path") {
			t.Errorf("the place, its directories made since, must be refused by the second spelling: %+v", res)
		}
	})
}

// A PLACE'S EXCEPTION IS OPEN UNDER THE SECOND SPELLING, AND ITS SIBLING
// IS NOT. The path is read again in the place's own spelling and the one
// rule decides: the interface folder the data directory leaves open is
// open by either path, and a folder that only shares its prefix is kept
// by either.
func TestAPlacesExceptionIsOpenUnderTheDataVolumesSpellingAndItsSiblingIsNot(t *testing.T) {
	onEachSecondSpelledHome(t, func(t *testing.T, s spelled) {
		for _, path := range []string{filepath.Join(s.data, "ui", "frame.html"), dataVolume + filepath.Join(s.data, "ui", "frame.html")} {
			if res := modelCall(t, s.r, "read", map[string]any{"file_path": path}); res.Refused || !strings.Contains(res.Output, "the frame, which is open") {
				t.Errorf("what the place leaves open is read as %s: %+v", path, res)
			}
		}
		if res := modelCall(t, s.r, "write", map[string]any{"file_path": dataVolume + filepath.Join(s.data, "ui", "new.html"), "content": "a new frame"}); res.Refused || res.Error != "" {
			t.Errorf("what the place leaves open is written by the second spelling: %+v", res)
		}
		for _, path := range []string{filepath.Join(s.data, "ui2", "frame.html"), dataVolume + filepath.Join(s.data, "ui2", "frame.html"), dataVolume + filepath.Join(s.data, "ui2")} {
			if res := modelCall(t, s.r, "read", map[string]any{"file_path": path}); !res.Refused || !strings.Contains(res.Error, "protected path") || opened(res) {
				t.Errorf("a sibling of the open folder is kept as %s: %+v", path, res)
			}
		}
	})
}

// THE SHELL'S CHECK KEEPS A PLACE UNDER THE SECOND SPELLING where it
// reads a path: a word of the command, the directory the call names, and
// the path after a cd. On macOS the identity's shell has nothing behind
// this check (it is the best-effort reading of the command's text), so
// each of these opened the kept file: `cat` of the second spelling
// printed the credential, and a shell started or moved into the data
// directory by its second spelling listed it.
func TestTheShellsCheckKeepsAPlaceUnderTheDataVolumesSpelling(t *testing.T) {
	onEachSecondSpelledHome(t, func(t *testing.T, s spelled) {
		// The controls: the home's second spelling is the home, and the
		// first spelling of each kept path is refused.
		if res := shellIn(t, s.r, dataVolume+s.home, "/bin/cat notes.md"); res.Refused || !strings.Contains(res.Output, "a note of the home") {
			t.Fatalf("a shell in the home by its second spelling runs in the home: %+v", res)
		}
		for name, c := range map[string]struct {
			cwd     any
			command string
		}{
			"a word":                         {nil, `/bin/cat "` + s.secret + `"`},
			"the directory the call names":   {s.data, "/bin/ls"},
			"the path after a cd":            {nil, `cd "` + s.data + `" && /bin/ls`},
			"a word, from the second home":   {dataVolume + s.home, "/bin/cat data/credentials/anthropic.json"},
			"a cd to it, from the home":      {nil, `cd "` + dataVolume + s.home + `" && /bin/cat data/credentials/anthropic.json`},
			"a word that is the place whole": {nil, `/bin/ls "` + s.data + `"`},
		} {
			if res := shellIn(t, s.r, c.cwd, c.command); !res.Refused || opened(res) {
				t.Errorf("control, %s by the first spelling: %+v", name, res)
			}
		}
		for name, c := range map[string]struct {
			cwd     any
			command string
			code    string
			says    string
		}{
			"a word":                                 {nil, `/bin/cat "` + dataVolume + s.secret + `"`, ReasonShellWall, "which the host keeps"},
			"a word that is the place whole":         {nil, `/bin/ls "` + dataVolume + s.data + `"`, ReasonShellWall, "which the host keeps"},
			"a word that is a place not made yet":    {nil, `/usr/bin/touch "` + dataVolume + s.two + `"`, ReasonShellWall, "which the host keeps"},
			"a word the command begins with":         {nil, `"` + dataVolume + s.snapshot + `"`, ReasonShellWall, "which the host keeps"},
			"the directory the call names":           {dataVolume + s.data, "/bin/ls", ReasonPathRefused, "protected path"},
			"a directory inside it":                  {dataVolume + filepath.Dir(s.secret), "/bin/ls", ReasonPathRefused, "protected path"},
			"the path after a cd":                    {nil, `cd "` + dataVolume + s.data + `" && /bin/ls`, ReasonShellWall, "which the host keeps"},
			"the path after a cd, then a file there": {nil, `cd "` + dataVolume + s.data + `" && /bin/cat credentials/anthropic.json`, ReasonShellWall, "which the host keeps"},
		} {
			res := shellIn(t, s.r, c.cwd, c.command)
			if !res.Refused || res.ReasonCode != c.code || !strings.Contains(res.Error, c.says) || opened(res) || strings.Contains(res.Output, "credentials") {
				t.Errorf("%s by the second spelling must be refused (%s, %q): refused=%v code=%q error=%q output=%q", name, c.code, c.says, res.Refused, res.ReasonCode, res.Error, res.Output)
			}
		}
	})
}

// A SPELLING THE WALL ALREADY KNEW IS REFUSED STILL: a path through a
// link (/var for /private/var, /tmp for /private/tmp), which the one
// resolver folds into the place's own spelling (firewall.ResolvePath),
// and a path that differs only in case, which every comparison folds
// (firewall.canonical). Neither needs to ask what the path is. They are
// here so that the answer is seen for the file tools and the shell alike,
// together with whether the volume the home is on folds case at all (the
// wall refuses a path that folds to a kept one whether or not the volume
// would open it).
func TestAKeptPlaceIsKeptUnderALinksSpellingAndUnderAnotherCase(t *testing.T) {
	onEachSecondSpelledHome(t, func(t *testing.T, s spelled) {
		upper := func(p string) string { return s.home + strings.ToUpper(strings.TrimPrefix(p, s.home)) }
		a, err1 := os.Stat(s.secret)
		b, err2 := os.Stat(upper(s.secret))
		folds := err1 == nil && err2 == nil && os.SameFile(a, b)
		t.Logf("the volume %s is on folds case: %v", s.home, folds)
		spellings := map[string]func(string) string{"another case": upper}
		if folds {
			// Where the volume does not fold case this spelling names no
			// home at all, and the boundary refuses it.
			spellings["another case of the home's name"] = func(p string) string { return filepath.Dir(s.home) + "/HOME" + strings.TrimPrefix(p, s.home) }
		}
		if link := strings.TrimPrefix(s.home, "/private"); link != s.home {
			spellings["the link "+link[:strings.Index(link[1:], "/")+1]] = func(p string) string { return strings.TrimPrefix(p, "/private") }
		} else {
			t.Logf("%s is under no link of the root: only the spellings by case are tried", s.home)
		}
		for name, spell := range spellings {
			for what, c := range map[string]struct {
				tool string
				args map[string]any
			}{
				"read":                          {"read", map[string]any{"file_path": spell(s.secret)}},
				"grep":                          {"grep", map[string]any{"pattern": "KEPT", "path": spell(filepath.Dir(s.secret))}},
				"ls":                            {"ls", map[string]any{"path": spell(s.data)}},
				"write":                         {"write", map[string]any{"file_path": spell(s.secret), "content": "SPRAY"}},
				"edit":                          {"edit", map[string]any{"file_path": spell(s.snapshot), "old_string": "KEPT", "new_string": "SPRAY"}},
				"a place not made yet, written": {"write", map[string]any{"file_path": spell(s.two), "content": "FORGED"}},
			} {
				if res := modelCall(t, s.r, c.tool, c.args); !res.Refused || !strings.Contains(res.Error, "protected path") || opened(res) {
					t.Errorf("%s by %s must be refused as a protected path: %+v", what, name, res)
				}
			}
			for what, c := range map[string]struct {
				cwd     any
				command string
			}{
				"a word":                       {nil, `/bin/cat "` + spell(s.secret) + `"`},
				"the directory the call names": {spell(s.data), "/bin/ls"},
				"the path after a cd":          {nil, `cd "` + spell(s.data) + `" && /bin/ls`},
			} {
				if res := shellIn(t, s.r, c.cwd, c.command); !res.Refused || opened(res) || strings.Contains(res.Output, "credentials") {
					t.Errorf("the shell, %s by %s, must be refused: %+v", what, name, res)
				}
			}
		}
	})
}

func TestAKeptPlaceAddressedByItsVolumeAndFileNumber(t *testing.T) {
	onEachSecondSpelledHome(t, func(t *testing.T, s spelled) {
		numbered := func(of string, rest ...string) string {
			var st syscall.Stat_t
			if err := syscall.Stat(of, &st); err != nil {
				t.Fatal(err)
			}
			return filepath.Join(append([]string{"/.vol", strconv.FormatInt(int64(st.Dev), 10), strconv.FormatUint(st.Ino, 10)}, rest...)...)
		}
		a, err1 := os.Stat(s.home)
		b, err2 := os.Stat(numbered(s.home))
		if err1 != nil || err2 != nil || !os.SameFile(a, b) {
			t.Skipf("this volume does not open a directory by its number (%s): %v %v", numbered(s.home), err1, err2)
		}
		const unplaced = "names a file by the number of its volume and of its inode"
		if res := modelCall(t, s.r, "read", map[string]any{"file_path": numbered(s.home, "notes.md")}); !res.Refused || !strings.Contains(res.Error, unplaced) || strings.Contains(res.Output, "a note of the home") {
			t.Errorf("the home by its number is refused as what it is: %+v", res)
		}
		if res := modelCall(t, s.r, "read", map[string]any{"file_path": numbered(s.home, "data", "ui", "frame.html")}); !res.Refused || !strings.Contains(res.Error, unplaced) || strings.Contains(res.Output, "the frame, which is open") {
			t.Errorf("what the place leaves open, below the home's number, is refused as what it is: %+v", res)
		}
		for name, c := range map[string]struct {
			tool string
			args map[string]any
		}{
			"read of the credential":    {"read", map[string]any{"file_path": numbered(s.home, "data", "credentials", "anthropic.json")}},
			"grep of the credentials":   {"grep", map[string]any{"pattern": "KEPT", "path": numbered(s.home, "data", "credentials")}},
			"ls of the data directory":  {"ls", map[string]any{"path": numbered(s.home, "data")}},
			"write over the credential": {"write", map[string]any{"file_path": numbered(s.home, "data", "credentials", "anthropic.json"), "content": "SPRAY"}},
			"edit of the snapshot key":  {"edit", map[string]any{"file_path": numbered(s.home, "data", "snapshot.key"), "old_string": "KEPT", "new_string": "SPRAY"}},
			"write of a place not made": {"write", map[string]any{"file_path": numbered(s.home, "vault", "inner", "token.json"), "content": "FORGED"}},
		} {
			if res := modelCall(t, s.r, c.tool, c.args); !res.Refused || !strings.Contains(res.Error, unplaced) || opened(res) {
				t.Errorf("%s below a number must be refused as what it is: %+v", name, res)
			}
		}
		if res := shellIn(t, s.r, nil, `/bin/cat "`+numbered(s.home, "data", "credentials", "anthropic.json")+`"`); !res.Refused || opened(res) {
			t.Errorf("the shell, a word below the home's number, must be refused: %+v", res)
		}
		// Inside a kept directory, by its own number: refused, by the wall.
		for name, path := range map[string]string{
			"a file below the data directory's number":                       numbered(s.data, "credentials", "anthropic.json"),
			"a file in the credentials directory, by the directory's number": numbered(filepath.Dir(s.secret), "anthropic.json"),
			"the credential by its own number":                               numbered(s.secret),
		} {
			res := modelCall(t, s.r, "read", map[string]any{"file_path": path})
			if !res.Refused || !strings.Contains(res.Error, unplaced) || opened(res) {
				t.Errorf("%s must be refused as what it is: %+v", name, res)
			}
			if rule := s.policy.Covered(path); rule == nil || rule.Kind != firewall.KindUnplaced {
				t.Errorf("%s: the wall alone must refuse it, got %+v", name, rule)
			}
			if res := shellIn(t, s.r, nil, `/bin/cat "`+path+`"`); !res.Refused || opened(res) {
				t.Errorf("the shell, %s, must be refused: %+v", name, res)
			}
		}
	})
}
