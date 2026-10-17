package tools

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
)

func stockPlaces(home, profile string) []*firewall.Rule {
	data := filepath.Join(home, "data")
	place := func(id, path string, except ...string) *firewall.Rule {
		return &firewall.Rule{ID: id, Kind: firewall.KindSubstrate, Path: path, Except: except, Reason: "kept", Enforced: true}
	}
	return []*firewall.Rule{
		place("sub.data", data, filepath.Join(data, "ui")),
		place("sub.config", filepath.Join(home, "config.json")),
		place("sub.providers", filepath.Join(home, "providers.json")),
		place("sub.profile", profile),
	}
}

func TestAHardLinkToAKeptFileIsKept(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	put := func(body string, parts ...string) string {
		t.Helper()
		p := filepath.Join(append([]string{home}, parts...)...)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	link := func(kept, name string) string {
		t.Helper()
		l := filepath.Join(home, "notes", name)
		if err := os.Link(kept, l); err != nil {
			t.Skipf("hard links unavailable here: %v", err)
		}
		return l
	}
	twin := put("KEPT SECRET\n", "notes", "twin.txt") // the same bytes, another file
	profile := put("KEPT SECRET\n", "conf", "settings")
	// A file that is a place, under a second name.
	places := map[string]string{}
	for i, kept := range []string{put("KEPT SECRET\n", "config.json"), put("KEPT SECRET\n", "providers.json"), profile} {
		places[kept] = link(kept, []string{"innocent.txt", "second.txt", "third.txt"}[i])
	}
	// A file inside the kept directory, under a second name.
	inside := map[string]string{}
	for i, parts := range [][]string{{"data", "ledger.jsonl"}, {"data", "identity.sec"}, {"data", "aii.db"}, {"data", "snapshot.key"}, {"data", "credentials", "anthropic.json"}} {
		kept := put("INSIDE\n", parts...)
		inside[kept] = link(kept, []string{"l.txt", "k.txt", "d.txt", "s.txt", "c.txt"}[i])
	}
	policy := firewall.DefaultPolicy()
	policy.SetPlaces(stockPlaces(home, profile))
	r := NewRegistry(home, policy, Timeouts{})

	for kept, second := range places {
		for name, c := range map[string]struct {
			tool string
			args map[string]any
		}{
			"read":   {"read", map[string]any{"file_path": second}},
			"write":  {"write", map[string]any{"file_path": second, "content": "SPRAY"}},
			"edit":   {"edit", map[string]any{"file_path": second, "old_string": "KEPT", "new_string": "SPRAY"}},
			"grep":   {"grep", map[string]any{"pattern": "KEPT", "path": second}},
			"ls":     {"ls", map[string]any{"path": second}},
			"delete": {"write", map[string]any{"file_path": second, "action": "delete"}},
		} {
			if res := modelCall(t, r, c.tool, c.args); !res.Refused || !strings.Contains(res.Error, "protected path") || strings.Contains(res.Output, "KEPT") {
				t.Errorf("%s of %s, a second name for %s, must be refused as a protected path: %+v", name, second, filepath.Base(kept), res)
			}
		}
		if why := r.shellRefusal(`cat "` + second + `"`); !strings.Contains(why, "which the host keeps") {
			t.Errorf("the shell's check must refuse %s, a second name for %s, given as an argument: %q", second, filepath.Base(kept), why)
		}
		// A command that BEGINS with the second name is refused on its
		// whole text, by a place, which has no name to say: the refusal
		// names the place and not "the protected name" followed by nothing.
		if why := r.shellRefusal(`"` + second + `"`); !strings.Contains(why, "is inside "+filepath.Base(kept)+", which the host keeps") || strings.Contains(why, "protected name") {
			t.Errorf("a command that begins with %s must be refused naming the place %s: %q", second, filepath.Base(kept), why)
		}
	}
	notes := filepath.Join(home, "notes")
	shown := func(out string) bool {
		return strings.Contains(out, "innocent.txt") || strings.Contains(out, "second.txt") || strings.Contains(out, "third.txt")
	}
	res := modelCall(t, r, "grep", map[string]any{"pattern": "KEPT", "path": notes})
	if res.Error != "" || !strings.Contains(res.Output, "twin.txt") || shown(res.Output) {
		t.Errorf("a search of the folder reads the twin and no second name of a place: %+v", res)
	}
	res = modelCall(t, r, "ls", map[string]any{"path": notes})
	if res.Error != "" || !strings.Contains(res.Output, "twin.txt") || shown(res.Output) {
		t.Errorf("a listing of the folder shows the twin and no second name of a place: %+v", res)
	}
	if res := modelCall(t, r, "read", map[string]any{"file_path": twin}); !strings.Contains(res.Output, "KEPT SECRET") {
		t.Errorf("a file with the same bytes is another file and stays open: %+v", res)
	}
	for kept := range places {
		if b, err := os.ReadFile(kept); err != nil || string(b) != "KEPT SECRET\n" {
			t.Errorf("%s was changed through its second name: %q %v", kept, b, err)
		}
	}

	for kept, second := range inside {
		if res := modelCall(t, r, "read", map[string]any{"file_path": kept}); !res.Refused {
			t.Errorf("%s by its own path is refused, as before: %+v", kept, res)
		}
		// In this order, so that a red run shows each act: what is read
		// first, then what changes it, the delete last.
		for _, c := range []struct {
			name, tool string
			args       map[string]any
		}{
			{"read", "read", map[string]any{"file_path": second}},
			{"grep", "grep", map[string]any{"pattern": "INSIDE", "path": second}},
			{"ls", "ls", map[string]any{"path": second}},
			{"edit", "edit", map[string]any{"file_path": second, "old_string": "INSIDE", "new_string": "SPRAY"}},
			{"write", "write", map[string]any{"file_path": second, "content": "SPRAY"}},
			{"delete", "write", map[string]any{"file_path": second, "action": "delete"}},
		} {
			if res := modelCall(t, r, c.tool, c.args); !res.Refused || !strings.Contains(res.Error, "protected path (sub.data:") || strings.Contains(res.Output, "INSIDE") {
				t.Errorf("%s of %s, a second name for %s inside the kept data directory, must be refused as a protected path: %+v", c.name, second, filepath.Base(kept), res)
			}
		}
		if why := r.shellRefusal(`cat "` + second + `"`); !strings.Contains(why, "which the host keeps") {
			t.Errorf("the shell's check must refuse %s, a second name for %s inside the kept data directory: %q", second, filepath.Base(kept), why)
		}
		if b, err := os.ReadFile(kept); err != nil || string(b) != "INSIDE\n" {
			t.Errorf("%s was changed through its second name: %q %v", kept, b, err)
		}
		if _, err := os.Lstat(second); err != nil {
			t.Errorf("the second name %s was deleted: %v", second, err)
		}
	}
	insideShown := func(out string) bool {
		for _, n := range []string{"l.txt", "k.txt", "d.txt", "s.txt", "c.txt"} {
			if strings.Contains(out, n) {
				return true
			}
		}
		return false
	}
	res = modelCall(t, r, "grep", map[string]any{"pattern": "INSIDE", "path": notes})
	if res.Error != "" || insideShown(res.Output) || !strings.HasPrefix(res.Output, "no matches") {
		t.Errorf("a search of the folder reads no second name of a file inside the data directory: %+v", res)
	}
	res = modelCall(t, r, "ls", map[string]any{"path": notes})
	if res.Error != "" || !strings.Contains(res.Output, "twin.txt") || insideShown(res.Output) {
		t.Errorf("a listing of the folder shows no second name of a file inside the data directory: %+v", res)
	}

	// The control half: two names, neither inside a kept directory.
	pair := put("A PAIR\n", "notes", "pair-a.txt")
	pairB := filepath.Join(home, "notes", "pair-b.txt")
	if err := os.Link(pair, pairB); err != nil {
		t.Fatal(err)
	}
	if res := modelCall(t, r, "read", map[string]any{"file_path": pairB}); res.Refused || !strings.Contains(res.Output, "A PAIR") {
		t.Errorf("a file whose two names both lie in the home is read: %+v", res)
	}
	if res := modelCall(t, r, "grep", map[string]any{"pattern": "A PAIR", "path": notes}); res.Error != "" || !strings.Contains(res.Output, "pair-b.txt") {
		t.Errorf("a search of the folder reads a file whose two names both lie in the home: %+v", res)
	}
	if res := modelCall(t, r, "edit", map[string]any{"file_path": pairB, "old_string": "A PAIR", "new_string": "A PAIR EDITED"}); res.Refused || res.Error != "" {
		t.Errorf("a file whose two names both lie in the home is edited: %+v", res)
	}
	if res := modelCall(t, r, "write", map[string]any{"file_path": pairB, "content": "WRITTEN\n"}); res.Refused || res.Error != "" {
		t.Errorf("a file whose two names both lie in the home is written: %+v", res)
	}
	if b, _ := os.ReadFile(pair); string(b) != "WRITTEN\n" {
		t.Errorf("the write reached the file through its other name: %q", b)
	}
}

func TestAFileOrALinkWhereAPlaceWouldBeLeavesTheHomeOpen(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "notes.md"), []byte("a note of the home\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	policy := firewall.DefaultPolicy()
	policy.SetPlaces(stockPlaces(home, filepath.Join(home, "secrets", "google", "token.json")))
	r := NewRegistry(home, policy, Timeouts{})
	open := func(when string) {
		t.Helper()
		if res := modelCall(t, r, "read", map[string]any{"file_path": "notes.md"}); res.Refused || !strings.Contains(res.Output, "a note of the home") {
			t.Fatalf("%s: the home's note is read: %+v", when, res)
		}
		if res := modelCall(t, r, "write", map[string]any{"file_path": "notes2.md", "content": "more"}); res.Refused || res.Error != "" {
			t.Fatalf("%s: a note is written: %+v", when, res)
		}
		if res := modelCall(t, r, "grep", map[string]any{"pattern": "note", "path": home}); res.Refused || res.Error != "" {
			t.Fatalf("%s: the home is searched: %+v", when, res)
		}
		if why := r.shellRefusal("echo hi"); why != "" {
			t.Fatalf("%s: a plain command passes the shell's check: %q", when, why)
		}
	}
	open("before")
	if res := modelCall(t, r, "write", map[string]any{"file_path": "secrets", "content": "a file"}); res.Refused || res.Error != "" {
		t.Fatalf("a file of the home's own is written: %+v", res)
	}
	open("with a file where the place's directory would be")
	if res := modelCall(t, r, "write", map[string]any{"file_path": "secrets", "action": "delete"}); res.Refused || res.Error != "" {
		t.Fatalf("the write is undone by the identity's own delete: %+v", res)
	}
	if _, err := os.Lstat(filepath.Join(home, "secrets")); err == nil {
		t.Fatal("the file was not removed")
	}
	if err := os.Symlink(filepath.Join(home, "secrets"), filepath.Join(home, "secrets")); err != nil {
		t.Logf("links unavailable here (%v): the loop is not asked", err)
		return
	}
	open("with a link that points at itself where the place's directory would be")
}

// theHomeIsOpen asks what the home's own notes need: a note read and one
// written, the home searched, and a plain command past the shell's check.
func theHomeIsOpen(t *testing.T, r *Registry, home, when string) {
	t.Helper()
	if res := modelCall(t, r, "read", map[string]any{"file_path": "notes.md"}); res.Refused || !strings.Contains(res.Output, "a note of the home") {
		t.Fatalf("%s: the home's note is read: %+v", when, res)
	}
	if res := modelCall(t, r, "write", map[string]any{"file_path": "notes2.md", "content": "more"}); res.Refused || res.Error != "" {
		t.Fatalf("%s: a note is written: %+v", when, res)
	}
	if res := modelCall(t, r, "grep", map[string]any{"pattern": "note", "path": home}); res.Refused || res.Error != "" {
		t.Fatalf("%s: the home is searched: %+v", when, res)
	}
	if why := r.shellRefusal("echo hi"); why != "" {
		t.Fatalf("%s: a plain command passes the shell's check: %q", when, why)
	}
}

func TestALinkToAPathTooLongWhereAPlaceWouldBeLeavesTheHomeOpen(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("not run on Windows: what a link to a path too long answers there is taken by reading (ERROR_FILENAME_EXCED_RANGE)")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "notes.md"), []byte("a note of the home\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	place := filepath.Join(home, "secrets", "google", "token.json")
	policy := firewall.DefaultPolicy()
	policy.SetPlaces(stockPlaces(home, place))
	r := NewRegistry(home, policy, Timeouts{})
	theHomeIsOpen(t, r, home, "before")
	short := strings.Repeat(string(filepath.Separator)+"abcdefgh", 113) // 1,017 bytes: a link holds it on macOS, and the place's rest makes it more than macOS opens
	tooLong := 0
	for _, c := range []struct{ what, target string }{
		{"a name longer than a directory may hold", string(filepath.Separator) + strings.Repeat("n", 300)},
		{"a path of short names longer than the system opens", short},
	} {
		link := filepath.Join(home, "secrets")
		if err := os.Symlink(c.target, link); err != nil {
			t.Fatalf("links are made here: %v", err)
		}
		if _, err := os.Stat(place); errors.Is(err, syscall.ENAMETOOLONG) {
			tooLong++
		} else {
			t.Logf("with a link to %s the place answers %v here, not file name too long; the home is asked anyway", c.what, err)
		}
		theHomeIsOpen(t, r, home, "with a link to "+c.what+" where the place's directory would be")
		if res := modelCall(t, r, "write", map[string]any{"file_path": "secrets", "action": "delete"}); res.Refused || res.Error != "" {
			t.Fatalf("the link to %s is undone by the identity's own delete: %+v", c.what, res)
		}
		if _, err := os.Lstat(link); err == nil {
			t.Fatalf("the link to %s was not removed", c.what)
		}
	}
	if tooLong == 0 {
		t.Fatal("no link here made the place answer file name too long: nothing was asked")
	}
}

func TestALinkIntoADirectoryTheHostCannotSearchIsTheIdentitysToDelete(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a directory's mode does not close it on Windows")
	}
	if os.Getuid() == 0 {
		t.Skip("root searches a directory at mode 000: nothing here fails to be checked")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home, shut := filepath.Join(base, "home"), filepath.Join(base, "shut")
	for _, d := range []string{home, filepath.Join(shut, "inside")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, "notes.md"), []byte("a note of the home\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	place := filepath.Join(home, "secrets", "google", "token.json")
	policy := firewall.DefaultPolicy()
	policy.SetPlaces(stockPlaces(home, place))
	r := NewRegistry(home, policy, Timeouts{})
	theHomeIsOpen(t, r, home, "before")
	link, other := filepath.Join(home, "secrets"), filepath.Join(home, "other")
	for _, l := range []string{link, other} {
		if err := os.Symlink(filepath.Join(shut, "inside"), l); err != nil {
			t.Fatalf("links are made here: %v", err)
		}
	}
	if err := os.Chmod(shut, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(shut, 0o700) })
	if _, err := os.Stat(place); err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Skipf("the directory at mode 000 is still searched here (%v): nothing fails to be checked", err)
	}

	const said = "the host could not check token.json, a place it keeps (permission denied), so it cannot tell any path apart from it until your operator clears that; secrets, in your home, is a link that leads where the host cannot look: deleting it clears this"
	refused := func(what string, res Result) {
		t.Helper()
		if !res.Refused || res.Error != "access denied: "+said {
			t.Errorf("%s while the link is there: refused, naming the link, in these words:\n got %+v\nwant %q", what, res, "access denied: "+said)
		}
	}
	refused("a read of the home's note", modelCall(t, r, "read", map[string]any{"file_path": "notes.md"}))
	refused("a delete of the home's note", modelCall(t, r, "write", map[string]any{"file_path": "notes.md", "action": "delete"}))
	refused("a delete of another link into the same directory", modelCall(t, r, "write", map[string]any{"file_path": "other", "action": "delete"}))
	refused("a delete of a path through the link", modelCall(t, r, "write", map[string]any{"file_path": "secrets/google", "action": "delete"}))
	refused("a write over the link", modelCall(t, r, "write", map[string]any{"file_path": "secrets", "content": "x"}))
	refused("a move of the link", modelCall(t, r, "write", map[string]any{"file_path": "secrets", "action": "move", "to": "moved"}))
	refused("a read of the link", modelCall(t, r, "read", map[string]any{"file_path": "secrets"}))
	refused("a listing of the link", modelCall(t, r, "ls", map[string]any{"path": "secrets"}))
	if why := r.shellRefusal("rm secrets"); why != said {
		t.Errorf("the shell's check refuses every word while the link is there, naming it:\n got %q\nwant %q", why, said)
	}
	if _, _, err := r.Admit("plugin delete", link); err == nil || err.Error() != said {
		t.Errorf("the door a plugin's files go through stays refused for the link: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(home, "notes.md")); err != nil {
		t.Fatalf("the home's note was deleted: %v", err)
	}

	res := modelCall(t, r, "write", map[string]any{"file_path": "secrets", "action": "delete"})
	if res.Refused || res.Error != "" || res.Output != "deleted the link "+link {
		t.Fatalf("the identity's delete of exactly its link is let through: %+v", res)
	}
	if _, err := os.Lstat(link); err == nil {
		t.Fatal("the link was not removed")
	}
	if err := os.Chmod(shut, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(shut, "inside")); err != nil {
		t.Fatalf("what the link led to was removed with it: %v", err)
	}
	theHomeIsOpen(t, r, home, "after the identity deleted its link")

	// A directory the identity made at mode 000 where the link was: named,
	// and nothing is let through.
	if err := os.Mkdir(link, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(link, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(link, 0o700) })
	const closed = "access denied: the host could not check token.json, a place it keeps (permission denied), so it cannot tell any path apart from it until your operator clears that; " +
		"secrets/google, in your home, is where the host cannot look"
	for what, args := range map[string]map[string]any{
		"a read of the home's note":   {"file_path": "notes.md"},
		"a delete of the directory":   {"file_path": "secrets", "action": "delete"},
		"a delete of what it names":   {"file_path": "secrets/google", "action": "delete"},
		"a delete of the home's note": {"file_path": "notes.md", "action": "delete"},
	} {
		tool := "write"
		if args["action"] == nil {
			tool = "read"
		}
		if res := modelCall(t, r, tool, args); !res.Refused || res.Error != closed {
			t.Errorf("%s with a directory at mode 000 above the place: refused, naming it, nothing let through:\n got %+v\nwant %q", what, res, closed)
		}
	}
}

func TestALinkAtADirectoryTheHostCannotSearchIsTheIdentitysToDelete(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a directory's mode does not close it on Windows")
	}
	if os.Getuid() == 0 {
		t.Skip("root searches a directory at mode 000: nothing here fails to be checked")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home, shut := filepath.Join(base, "home"), filepath.Join(base, "shut")
	closed := filepath.Join(home, "closed")
	for _, d := range []string{closed, shut} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, "notes.md"), []byte("a note of the home\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	place := filepath.Join(home, "secrets", "google", "token.json")
	policy := firewall.DefaultPolicy()
	policy.SetPlaces(stockPlaces(home, place))
	r := NewRegistry(home, policy, Timeouts{})
	theHomeIsOpen(t, r, home, "before")
	link, other := filepath.Join(home, "secrets"), filepath.Join(home, "other")
	for _, l := range []string{link, other} {
		if err := os.Symlink(closed, l); err != nil {
			t.Fatalf("links are made here: %v", err)
		}
	}
	for _, d := range []string{closed, shut} {
		if err := os.Chmod(d, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(d, 0o700) })
	}
	if _, err := os.Stat(link); err != nil {
		t.Fatalf("a link at a directory is looked at itself, even at mode 000: %v", err)
	}
	if _, err := os.Stat(place); err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Skipf("the directory at mode 000 is still searched here (%v): nothing fails to be checked", err)
	}

	const said = "access denied: the host could not check token.json, a place it keeps (permission denied), so it cannot tell any path apart from it until your operator clears that; secrets, in your home, is a link that leads where the host cannot look: deleting it clears this"
	for what, c := range map[string]struct {
		tool string
		args map[string]any
	}{
		"a read of the home's note":                 {"read", map[string]any{"file_path": "notes.md"}},
		"a delete of the home's note":               {"write", map[string]any{"file_path": "notes.md", "action": "delete"}},
		"a delete of another link at the directory": {"write", map[string]any{"file_path": "other", "action": "delete"}},
		"a delete of the directory the link is at":  {"write", map[string]any{"file_path": "closed", "action": "delete"}},
		"a delete of where the check stopped":       {"write", map[string]any{"file_path": "secrets/google", "action": "delete"}},
		"a write over the link":                     {"write", map[string]any{"file_path": "secrets", "content": "x"}},
		"a move of the link":                        {"write", map[string]any{"file_path": "secrets", "action": "move", "to": "moved"}},
		"a listing of the link":                     {"ls", map[string]any{"path": "secrets"}},
	} {
		if res := modelCall(t, r, c.tool, c.args); !res.Refused || res.Error != said {
			t.Errorf("%s while the link is there: refused, naming the link, in these words:\n got %+v\nwant %q", what, res, said)
		}
	}
	if why := r.shellRefusal("rm secrets"); "access denied: "+why != said {
		t.Errorf("the shell's check refuses every word while the link is there, naming it:\n got %q\nwant %q", why, said)
	}
	if _, _, err := r.Admit("plugin delete", link); err == nil || "access denied: "+err.Error() != said {
		t.Errorf("the door a plugin's files go through stays refused for the link: %v", err)
	}

	res := modelCall(t, r, "write", map[string]any{"file_path": "secrets", "action": "delete"})
	if res.Refused || res.Error != "" || res.Output != "deleted the link "+link {
		t.Fatalf("the identity's delete of exactly its link is let through: %+v", res)
	}
	if _, err := os.Lstat(link); err == nil {
		t.Fatal("the link was not removed")
	}
	if err := os.Chmod(closed, 0o700); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(closed); err != nil || !info.IsDir() {
		t.Fatalf("the directory the link was at was removed or changed with it: %v %v", info, err)
	}
	theHomeIsOpen(t, r, home, "after the identity deleted its link")

	// The limit: a link at a directory outside the home.
	if err := os.Symlink(shut, link); err != nil {
		t.Fatal(err)
	}
	const stopped = "access denied: the host could not check token.json, a place it keeps (permission denied), so it cannot tell any path apart from it until your operator clears that; secrets/google, in your home, is where the host cannot look"
	if res := modelCall(t, r, "read", map[string]any{"file_path": "notes.md"}); !res.Refused || res.Error != stopped {
		t.Errorf("with a link at a closed directory outside the home: refused, naming where the check stopped:\n got %+v\nwant %q", res, stopped)
	}
	if res := modelCall(t, r, "write", map[string]any{"file_path": "secrets", "action": "delete"}); !res.Refused {
		t.Errorf("the delete of a link that leads out of the home is not let through: %+v", res)
	} else {
		t.Logf("the delete of a link at a closed directory outside the home: %s", res.Error)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("the link outside was removed: %v", err)
	}
}

// folds reports whether the volume under dir opens made under the
// spelling asked: it makes made in dir and asks for asked.
func folds(t *testing.T, dir, made, asked string) bool {
	t.Helper()
	p := filepath.Join(dir, made)
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		return false
	}
	defer os.Remove(p)
	_, err := os.Stat(filepath.Join(dir, asked))
	return err == nil
}

func TestALinkAtAKeptPlacesOwnPathIsNotLetThroughUnderAFoldedSpelling(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a directory's mode does not close it on Windows")
	}
	if os.Getuid() == 0 {
		t.Skip("root searches a directory at mode 000: nothing here fails to be checked")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home, shut := filepath.Join(base, "home"), filepath.Join(base, "shut")
	for _, d := range []string{filepath.Join(home, "secrets", "google"), filepath.Join(shut, "inside")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if !folds(t, home, "x.jſon", "x.json") {
		t.Skip("this volume does not fold ſ to s: the spelling opens nothing of the place")
	}
	if err := os.WriteFile(filepath.Join(home, "notes.md"), []byte("a note of the home\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(home, "secrets", "google", "token.json")
	places := append(stockPlaces(home, profile),
		&firewall.Rule{ID: "sub.vault", Kind: firewall.KindSubstrate, Path: filepath.Join(home, "vault", "token.json"), Reason: "kept", Enforced: true})
	policy := firewall.DefaultPolicy()
	policy.SetPlaces(places)
	r := NewRegistry(home, policy, Timeouts{})
	theHomeIsOpen(t, r, home, "before")
	config, vault := filepath.Join(home, "config.json"), filepath.Join(home, "vault")
	for _, l := range []string{config, profile, vault} {
		if err := os.Symlink(filepath.Join(shut, "inside"), l); err != nil {
			t.Fatalf("links are made here: %v", err)
		}
	}
	if err := os.Chmod(shut, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(shut, 0o700) })
	if _, err := os.Stat(profile); err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Skipf("the directory at mode 000 is still searched here (%v): nothing fails to be checked", err)
	}
	cases := []struct{ link, spelled string }{
		{config, "config.json"},
		{config, "config.jſon"},
		{profile, "secrets/google/token.json"},
		{profile, "secrets/google/token.jſon"},
		{vault, "vault"},
	}

	if folds(t, home, "xﬁ.json", "xfi.json") {
		cases = append(cases, struct{ link, spelled string }{config, "conﬁg.json"})
	} else {
		t.Log("this volume does not fold ﬁ to fi: that spelling is not asked")
	}
	for _, c := range cases {
		res := modelCall(t, r, "write", map[string]any{"file_path": c.spelled, "action": "delete"})
		if !res.Refused {
			t.Errorf("the delete of %q must be refused: %+v", c.spelled, res)
		}
		if _, err := os.Lstat(c.link); err != nil {
			t.Errorf("the delete of %q removed the link at %s: %v", c.spelled, c.link, err)
		}
	}
}

// A KEPT FILE IS KEPT UNDER A SECOND NAME INSIDE A GRANTED ROOT. In a
// granted root the home's names do not apply and Check is not asked: the
// place rule alone decides there (substrateDenied asks Covered, then
// returns for a granted root). So Covered must know the second name as
// the place, or a granted folder that holds one reads the kept file. So
// too a second name for a file inside a kept directory.
func TestAKeptFileIsKeptUnderASecondNameInAGrantedRoot(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home, grant, kept := filepath.Join(base, "home"), filepath.Join(base, "work"), filepath.Join(base, "host", "settings")
	for _, d := range []string{home, grant, filepath.Dir(kept)} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(kept, []byte("KEPT SECRET\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(grant, "twin.txt"), []byte("KEPT SECRET\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	second := filepath.Join(grant, "innocent.txt")
	if err := os.Link(kept, second); err != nil {
		t.Skipf("hard links unavailable here: %v", err)
	}
	data := filepath.Join(base, "host", "data")
	key := filepath.Join(data, "identity.sec")
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(key, []byte("KEPT SECRET\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(key, filepath.Join(grant, "inner.txt")); err != nil {
		t.Fatal(err)
	}
	policy := firewall.DefaultPolicy()
	policy.SetPlaces([]*firewall.Rule{
		{ID: "sub.profile", Kind: firewall.KindSubstrate, Path: kept, Reason: "kept", Enforced: true},
		{ID: "sub.data", Kind: firewall.KindSubstrate, Path: data, Reason: "kept", Enforced: true},
	})
	r := NewRegistry(home, policy, Timeouts{})
	r.SetExtraRoots([]string{grant})
	if _, roots := r.Roots(); len(roots) != 1 {
		t.Fatalf("the granted root was not taken: %v", roots)
	}
	if res := modelCall(t, r, "read", map[string]any{"file_path": filepath.Join(grant, "twin.txt")}); !strings.Contains(res.Output, "KEPT SECRET") {
		t.Fatalf("the granted root is open: %+v", res)
	}
	for name, c := range map[string]struct {
		tool string
		args map[string]any
	}{
		"read":  {"read", map[string]any{"file_path": second}},
		"write": {"write", map[string]any{"file_path": second, "content": "SPRAY"}},
		"edit":  {"edit", map[string]any{"file_path": second, "old_string": "KEPT", "new_string": "SPRAY"}},
		"grep":  {"grep", map[string]any{"pattern": "KEPT", "path": second}},
	} {
		if res := modelCall(t, r, c.tool, c.args); !res.Refused || !strings.Contains(res.Error, "protected path") || strings.Contains(res.Output, "KEPT") {
			t.Errorf("%s of a second name for a kept file inside a granted root must be refused as a protected path: %+v", name, res)
		}
	}
	inner := filepath.Join(grant, "inner.txt")
	for name, c := range map[string]struct {
		tool string
		args map[string]any
	}{
		"read":  {"read", map[string]any{"file_path": inner}},
		"write": {"write", map[string]any{"file_path": inner, "content": "SPRAY"}},
		"grep":  {"grep", map[string]any{"pattern": "KEPT", "path": inner}},
	} {
		if res := modelCall(t, r, c.tool, c.args); !res.Refused || !strings.Contains(res.Error, "protected path (sub.data:") || strings.Contains(res.Output, "KEPT") {
			t.Errorf("%s of a second name for a file inside a kept directory, inside a granted root, must be refused as a protected path: %+v", name, res)
		}
	}
	if res := modelCall(t, r, "grep", map[string]any{"pattern": "KEPT", "path": grant}); res.Error != "" || !strings.Contains(res.Output, "twin.txt") || strings.Contains(res.Output, "innocent.txt") || strings.Contains(res.Output, "inner.txt") {
		t.Errorf("a search of the granted root reads the twin and no second name: %+v", res)
	}
	if b, err := os.ReadFile(kept); err != nil || string(b) != "KEPT SECRET\n" {
		t.Errorf("the kept file was changed through its second name: %q %v", b, err)
	}
}

// A READ ASKS THE FILE IT OPENED WHEN IT HAS MORE THAN ONE NAME. The gate
// judges a path, and read and edit open it a moment later: a second name
// for the key (inside the kept data directory) or for config.json (a kept
// file), put at the path in that moment, was read; edit then answered
// whether old_string was in it, and how many times. Now a file with more
// than one name is asked through the handle it is read by, before a byte
// is read, as a write's file is (Registry.heldRead): refused as a
// protected path, nothing of it in the answer. The read tool is called
// past the gate here, as the moment after the gate finds it; edit is
// called through the gate, and its window seam puts the second name at
// the path just after. The control half: a file with one name, and a
// file whose two names both lie in the home, are read and edited.
func TestAReadAsksTheFileItOpenedWhenItHasMoreThanOneName(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	put := func(body string, parts ...string) string {
		t.Helper()
		p := filepath.Join(append([]string{home}, parts...)...)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	key := put("INSIDE\n", "data", "identity.sec")
	config := put("KEPT\n", "config.json")
	one := put("ONE\n", "notes", "one.txt")
	pair := put("A PAIR\n", "notes", "pair-a.txt")
	pairB := filepath.Join(home, "notes", "pair-b.txt")
	if err := os.Link(pair, pairB); err != nil {
		t.Skipf("hard links unavailable here: %v", err)
	}
	policy := firewall.DefaultPolicy()
	policy.SetPlaces(stockPlaces(home, filepath.Join(home, "conf", "settings")))
	r := NewRegistry(home, policy, Timeouts{})
	read := &ReadTool{maxBytes: 51200, held: r.heldRead}

	for kept, rule := range map[string]string{key: "sub.data", config: "sub.config"} {
		second := filepath.Join(home, "notes", "second-"+filepath.Base(kept))
		if err := os.Link(kept, second); err != nil {
			t.Fatal(err)
		}
		res, err := read.Execute(context.Background(), map[string]any{"file_path": second})
		if err != nil || !res.Refused || !strings.Contains(res.Error, "protected path ("+rule+":") || strings.Contains(res.Output, "INSIDE") || strings.Contains(res.Output, "KEPT") {
			t.Errorf("a read that opened a second name for %s is refused through the handle, nothing of it read: %+v %v", filepath.Base(kept), res, err)
		}
	}
	for _, p := range []string{one, pairB} {
		if res, err := read.Execute(context.Background(), map[string]any{"file_path": p}); err != nil || res.Refused || res.Error != "" {
			t.Errorf("a read of %s is answered as before: %+v %v", filepath.Base(p), res, err)
		}
	}

	plain := put("PLAIN\n", "notes", "plain.txt")
	tool, _ := r.Get("edit")
	edit := tool.(*EditTool)
	edit.window = func(at, path string) {
		if at != "gate" {
			return
		}
		staged := filepath.Join(home, "notes", "staged")
		if err := os.Link(key, staged); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(staged, plain); err != nil {
			t.Fatal(err)
		}
	}
	res := modelCall(t, r, "edit", map[string]any{"file_path": plain, "old_string": "ABSENT", "new_string": "SPRAY"})
	if !res.Refused || !strings.Contains(res.Error, "protected path (sub.data:") || strings.Contains(res.Error, "not found") {
		t.Errorf("an edit whose file became a second name for the key after the gate is refused before it reads, saying nothing of what is in it: %+v", res)
	}
	edit.window = nil
	if b, err := os.ReadFile(key); err != nil || string(b) != "INSIDE\n" {
		t.Errorf("the key was changed: %q %v", b, err)
	}
	for _, p := range []string{one, pairB} {
		if res := modelCall(t, r, "edit", map[string]any{"file_path": p, "old_string": "\n", "new_string": " EDITED\n"}); res.Refused || res.Error != "" {
			t.Errorf("an edit of %s is answered as before: %+v", filepath.Base(p), res)
		}
	}
}
