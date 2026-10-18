package tools

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
)

func TestAPlaceNotMadeYetIsNotMadeUnderASpellingTheVolumeFolds(t *testing.T) {
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
	longS := folds(t, home, "x.j\u017fon", "x.json")
	decomposed := folds(t, home, "cafe\u0301.json", "caf\u00e9.json")
	if !longS && !decomposed {
		t.Skip("this volume folds neither \u017f nor a decomposed accent: no second spelling of a place not made yet can be made here")
	}
	put("a note of the home\n", "notes.md")
	src := put("FORGED\n", "src.txt")
	kit := filepath.Join(home, "kit")
	put("FORGED\n", "kit", "cafe\u0301.json")
	providers := put("KEPT SECRET\n", "providers.json")
	profile := filepath.Join(home, "secrets", "google", "token.json")
	accented := filepath.Join(home, "vault", "caf\u00e9.json")
	config := filepath.Join(home, "config.json")
	policy := firewall.DefaultPolicy()
	policy.SetPlaces(append(stockPlaces(home, profile),
		&firewall.Rule{ID: "sub.accent", Kind: firewall.KindSubstrate, Path: accented, Reason: "kept", Enforced: true}))
	r := NewRegistry(home, policy, Timeouts{})

	type attempt struct {
		what string
		args map[string]any
		kept string   // the place, which must not be there afterwards
		left []string // what the call would have made on its way, which must not be there either
	}
	var attempts []attempt
	if longS {
		attempts = append(attempts,
			attempt{"a write of config.j\u017fon", map[string]any{"file_path": "config.j\u017fon", "content": "FORGED"}, config, nil},
			attempt{"a write of \u017fecrets/google/token.json", map[string]any{"file_path": "\u017fecrets/google/token.json", "content": "FORGED"}, profile, []string{filepath.Join(home, "secrets")}},
			attempt{"a copy to config.j\u017fon", map[string]any{"file_path": "src.txt", "action": "copy", "to": "config.j\u017fon"}, config, nil},
			attempt{"a move to \u017fecrets/google/token.json", map[string]any{"file_path": "src.txt", "action": "move", "to": "\u017fecrets/google/token.json"}, profile, []string{filepath.Join(home, "secrets")}},
		)
	} else {
		t.Log("this volume does not fold \u017f to s: the long-s spellings are not asked")
	}
	if decomposed {
		attempts = append(attempts,
			attempt{"a write of the decomposed spelling", map[string]any{"file_path": "vault/cafe\u0301.json", "content": "FORGED"}, accented, []string{filepath.Join(home, "vault")}},
			attempt{"a copy to the decomposed spelling", map[string]any{"file_path": "src.txt", "action": "copy", "to": "vault/cafe\u0301.json"}, accented, []string{filepath.Join(home, "vault")}},
			attempt{"a move to the decomposed spelling", map[string]any{"file_path": "src.txt", "action": "move", "to": "vault/cafe\u0301.json"}, accented, []string{filepath.Join(home, "vault")}},
			attempt{"a copy of a folder holding the decomposed name", map[string]any{"file_path": "kit", "action": "copy", "to": "vault", "recursive": true}, accented, []string{filepath.Join(home, "vault")}},
			attempt{"a move of that folder", map[string]any{"file_path": "kit", "action": "move", "to": "vault"}, accented, []string{filepath.Join(home, "vault")}},
		)
	} else {
		t.Log("this volume does not fold a decomposed accent: the decomposed spellings are not asked")
	}
	for _, a := range attempts {
		res := modelCall(t, r, "write", a.args)
		if !res.Refused || !strings.HasPrefix(res.Error, "access denied: protected path (") && !strings.HasPrefix(res.Error, "access denied: to protected path (") {
			t.Errorf("%s makes a kept place: refused as a protected path: %+v", a.what, res)
		}
		for _, p := range append([]string{a.kept}, a.left...) {
			if _, err := os.Lstat(p); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("%s left %s behind: %v", a.what, p, err)
				os.RemoveAll(p)
			}
		}
		// A lost source is said and put back, so that a red run shows
		// every case and not only the first that lost it.
		if b, err := os.ReadFile(src); err != nil || string(b) != "FORGED\n" {
			t.Errorf("%s took the source with it: %q %v", a.what, b, err)
			put("FORGED\n", "src.txt")
		}
		if _, err := os.Stat(filepath.Join(kit, "cafe\u0301.json")); err != nil {
			t.Errorf("%s took the folder with it: %v", a.what, err)
			put("FORGED\n", "kit", "cafe\u0301.json")
		}
	}
	if longS {
		for _, args := range []map[string]any{
			{"file_path": "providers.j\u017fon", "content": "FORGED"},
			{"file_path": "src.txt", "action": "copy", "to": "providers.j\u017fon"},
		} {
			if res := modelCall(t, r, "write", args); !res.Refused {
				t.Errorf("an existing kept file is not replaced under a folded spelling: %+v", res)
			}
		}
		if b, err := os.ReadFile(providers); err != nil || string(b) != "KEPT SECRET\n" {
			t.Errorf("the kept providers.json was replaced: %q %v", b, err)
		}
	}
	for _, args := range []map[string]any{
		{"file_path": "notes/new/plain.md", "content": "an ordinary note"},
		{"file_path": "notes.md", "action": "copy", "to": "notes/copied.md"},
		{"file_path": "notes/copied.md", "action": "move", "to": "notes/moved.md"},
		{"file_path": "kit", "action": "copy", "to": "notes/kit", "recursive": true},
	} {
		if res := modelCall(t, r, "write", args); res.Refused || res.Error != "" {
			t.Errorf("an ordinary write is made as before: %v: %+v", args, res)
		}
	}
	for _, p := range []string{"notes/new/plain.md", "notes/moved.md", "notes/kit/cafe\u0301.json"} {
		if _, err := os.Stat(filepath.Join(home, p)); err != nil {
			t.Errorf("the ordinary write of %s is not there: %v", p, err)
		}
	}
}

func TestAWriteCountsAsMadeWhatItCreatedNotWhatThePathShowed(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(home, "vault"), 0o700); err != nil {
		t.Fatal(err)
	}
	if !folds(t, filepath.Join(home, "vault"), "cafe\u0301.json", "caf\u00e9.json") {
		t.Skip("this volume does not open a decomposed accent as the composed one: nothing to make under another spelling")
	}
	accented := filepath.Join(home, "vault", "caf\u00e9.json")
	policy := firewall.DefaultPolicy()
	policy.SetPlaces([]*firewall.Rule{{ID: "sub.accent", Kind: firewall.KindSubstrate, Path: accented, Reason: "kept", Enforced: true}})
	r := NewRegistry(home, policy, Timeouts{})
	wt, _ := r.Get("write")
	wt.(*WriteTool).measure = func(ctx context.Context, path string) (os.FileInfo, int64, int, error) {
		// The other call makes the file, this call measures it, and the
		// other call takes it back.
		if err := os.WriteFile(path, []byte("OTHER"), 0o600); err != nil {
			t.Fatal(err)
		}
		m, b, l, err := previousFile(ctx, path)
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		return m, b, l, err
	}
	res := modelCall(t, r, "write", map[string]any{"file_path": "vault/cafe\u0301.json", "content": "FORGED"})
	if !res.Refused || !strings.HasPrefix(res.Error, "access denied: protected path (") {
		t.Errorf("a write that made the kept place after measuring another's file is refused as a protected path: %+v", res)
	}
	if b, err := os.ReadFile(accented); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the kept place was left made: %q %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(home, "vault")); err != nil {
		t.Errorf("the directory that was there before is gone: %v", err)
	}
}

func TestATakeBackRemovesOnlyWhatTheCallMade(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for f, body := range map[string]string{"src.txt": "SRC\n", "kit/cafe\u0301.json": "KIT\n"} {
		p := filepath.Join(home, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	config := filepath.Join(home, "config.json")
	vault3 := filepath.Join(home, "vault3", "caf\u00e9.json")
	policy := firewall.DefaultPolicy()
	policy.SetPlaces(append(stockPlaces(home, filepath.Join(home, "secrets", "google", "token.json")),
		&firewall.Rule{ID: "sub.vault3", Kind: firewall.KindSubstrate, Path: vault3, Reason: "kept", Enforced: true}))
	r := NewRegistry(home, policy, Timeouts{})
	wt, _ := r.Get("write")
	var hostMakes string
	wt.(*WriteTool).rename = func(from, to string) error {
		if err := os.MkdirAll(filepath.Dir(hostMakes), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(hostMakes, []byte("HOST\n"), 0o600); err != nil {
			return err
		}
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: errCrossDevice}
	}
	cases := []struct {
		what, place, source string
		args                map[string]any
	}{
		{"a folder holding the decomposed name moved to vault3 while the host makes vault3/caf\u00e9.json", vault3, filepath.Join(home, "kit", "cafe\u0301.json"), map[string]any{"file_path": "kit", "action": "move", "to": "vault3"}},
		{"a file moved to the folded spelling of config.json while the host makes config.json", config, filepath.Join(home, "src.txt"), map[string]any{"file_path": "src.txt", "action": "move", "to": "con\ufb01g.json"}},
	}
	if !folds(t, home, "x\ufb01.json", "xfi.json") {
		t.Log("this volume does not fold \ufb01 to fi: the file case is not asked")
		cases = cases[:1]
	}
	for _, c := range cases {
		hostMakes = c.place
		before, _ := os.ReadFile(c.source)
		res := modelCall(t, r, "write", c.args)
		if res.Error == "" {
			t.Errorf("%s: the call does not succeed: %+v", c.what, res)
		}
		if b, err := os.ReadFile(c.place); err != nil || string(b) != "HOST\n" {
			t.Errorf("%s: the host's place is as the host made it: %q %v (the call answered %+v)", c.what, b, err, res)
		}
		if b, err := os.ReadFile(c.source); err != nil || string(b) != string(before) {
			t.Errorf("%s: the source is unchanged: %q %v", c.what, b, err)
		}
	}
}

func TestADanglingLinkMovedOntoAPlaceNotMadeYetIsTakenBack(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(home, "vault"), 0o700); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(home, "config.json")
	accented := filepath.Join(home, "vault", "caf\u00e9.json")
	policy := firewall.DefaultPolicy()
	policy.SetPlaces(append(stockPlaces(home, filepath.Join(home, "secrets", "google", "token.json")),
		&firewall.Rule{ID: "sub.accent", Kind: firewall.KindSubstrate, Path: accented, Reason: "kept", Enforced: true}))
	r := NewRegistry(home, policy, Timeouts{})
	var cases []struct{ what, to, place string }
	if folds(t, home, "x\ufb01.json", "xfi.json") {
		cases = append(cases, struct{ what, to, place string }{"\ufb01", "con\ufb01g.json", config})
	} else {
		t.Log("this volume does not fold \ufb01 to fi: that spelling is not asked")
	}
	if folds(t, filepath.Join(home, "vault"), "cafe\u0301.json", "caf\u00e9.json") {
		cases = append(cases, struct{ what, to, place string }{"a decomposed accent", "vault/cafe\u0301.json", accented})
	} else {
		t.Log("this volume does not open a decomposed accent as the composed one: that spelling is not asked")
	}
	if len(cases) == 0 {
		t.Skip("this volume folds neither spelling")
	}
	for i, c := range cases {
		link := filepath.Join(home, "dangling-"+strconv.Itoa(i))
		if err := os.Symlink(filepath.Join(home, "later-"+strconv.Itoa(i)+".json"), link); err != nil {
			t.Fatalf("links are made here: %v", err)
		}
		res := modelCall(t, r, "write", map[string]any{"file_path": link, "action": "move", "to": c.to})
		if !res.Refused || !strings.HasPrefix(res.Error, "access denied: to protected path (") {
			t.Errorf("a dangling link moved onto %s by %s is refused as a protected path: %+v", filepath.Base(c.place), c.what, res)
		}
		if fi, err := os.Lstat(c.place); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("the link moved by %s stands at the kept %s: %v %v", c.what, c.place, fi != nil, err)
			os.Remove(c.place)
		}
		if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Errorf("the link moved by %s was not moved back: %v", c.what, err)
		}
		// The comparison is the wall's, before a call as after it: a link
		// to nothing standing at the place (the host's, say) is not deleted
		// under the folded spelling either.
		if err := os.Symlink(filepath.Join(home, "nowhere.json"), c.place); err != nil {
			t.Fatal(err)
		}
		if res := modelCall(t, r, "write", map[string]any{"file_path": c.to, "action": "delete"}); !res.Refused {
			t.Errorf("the delete by %s of a link to nothing standing at %s is refused: %+v", c.what, filepath.Base(c.place), res)
		}
		if _, err := os.Lstat(c.place); err != nil {
			t.Errorf("the link at %s was deleted by %s: %v", filepath.Base(c.place), c.what, err)
		}
		os.Remove(c.place)
	}
}

func TestAWriteDoesNotWriteIntoAKeptFilePutAtItsPathAfterTheGate(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(home, "vault"), 0o700); err != nil {
		t.Fatal(err)
	}
	if !folds(t, filepath.Join(home, "vault"), "cafe\u0301.json", "caf\u00e9.json") {
		t.Skip("this volume does not open a decomposed accent as the composed one: no other spelling opens the place")
	}
	accented := filepath.Join(home, "vault", "caf\u00e9.json")
	policy := firewall.DefaultPolicy()
	policy.SetPlaces([]*firewall.Rule{{ID: "sub.accent", Kind: firewall.KindSubstrate, Path: accented, Reason: "kept", Enforced: true}})
	r := NewRegistry(home, policy, Timeouts{})
	wt, _ := r.Get("write")
	hostMakes := func() {
		if err := os.WriteFile(accented, []byte("HOST\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, when := range []string{"before the write measures", "after the write measures"} {
		wt.(*WriteTool).measure = func(ctx context.Context, path string) (os.FileInfo, int64, int, error) {
			if when == "before the write measures" {
				hostMakes()
			}
			m, b, l, err := previousFile(ctx, path)
			if when == "after the write measures" {
				hostMakes()
			}
			return m, b, l, err
		}
		res := modelCall(t, r, "write", map[string]any{"file_path": "vault/cafe\u0301.json", "content": "FORGED"})
		if !res.Refused || !strings.HasPrefix(res.Error, "access denied: protected path (") {
			t.Errorf("the host made its place %s: the write is refused as a protected path: %+v", when, res)
		}
		if b, err := os.ReadFile(accented); err != nil || string(b) != "HOST\n" {
			t.Errorf("the host made its place %s: its file is byte for byte as it made it: %q %v", when, b, err)
		}
		os.Remove(accented)
	}
}

func TestAWriteDoesNotWriteIntoAKeptFileInAKeptDirectoryMadeAfterTheGate(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !folds(t, home, "dossie\u0301", "dossi\u00e9") {
		t.Skip("this volume does not open a decomposed accent as the composed one: no other spelling opens the place")
	}
	dir := filepath.Join(home, "dossi\u00e9")
	hostFile := filepath.Join(dir, "x.json")
	policy := firewall.DefaultPolicy()
	policy.SetPlaces([]*firewall.Rule{{ID: "sub.dossier", Kind: firewall.KindSubstrate, Path: dir, Reason: "kept", Enforced: true}})
	r := NewRegistry(home, policy, Timeouts{})
	wt := func() *WriteTool { w, _ := r.Get("write"); return w.(*WriteTool) }()
	hostMakes := func() {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(hostFile, []byte("HOST\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, when := range []string{"before the write measures", "after the write measures", "just after the write made its directories"} {
		wt.measure, wt.window = nil, nil
		switch when {
		case "just after the write made its directories":
			wt.window = func(at, _ string) {
				if at == "parents" {
					hostMakes()
				}
			}
		default:
			wt.measure = func(ctx context.Context, path string) (os.FileInfo, int64, int, error) {
				if when == "before the write measures" {
					hostMakes()
				}
				m, b, l, err := previousFile(ctx, path)
				if when == "after the write measures" {
					hostMakes()
				}
				return m, b, l, err
			}
		}
		res := modelCall(t, r, "write", map[string]any{"file_path": "dossie\u0301/x.json", "content": "FORGED"})
		if res.Error == "" {
			t.Errorf("the host made its directory and file %s: the write does not succeed: %+v", when, res)
		}
		if b, err := os.ReadFile(hostFile); err != nil || string(b) != "HOST\n" {
			t.Errorf("the host made its directory and file %s: its file is byte for byte as it made it: %q %v (the write answered %+v)", when, b, err, res)
		}
		os.RemoveAll(dir)
	}
}

func TestADeleteMoveOrCopyDoesNotActOnAKeptFileSavedAfterTheGate(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"vault", "notes"} {
		if err := os.Mkdir(filepath.Join(home, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if !folds(t, filepath.Join(home, "vault"), "cafe\u0301.json", "caf\u00e9.json") {
		t.Skip("this volume does not open a decomposed accent as the composed one: no other spelling opens the place")
	}
	accented := filepath.Join(home, "vault", "caf\u00e9.json")
	policy := firewall.DefaultPolicy()
	policy.SetPlaces([]*firewall.Rule{{ID: "sub.accent", Kind: firewall.KindSubstrate, Path: accented, Reason: "kept", Enforced: true}})
	r := NewRegistry(home, policy, Timeouts{})
	wt := func() *WriteTool { w, _ := r.Get("write"); return w.(*WriteTool) }()
	wt.window = func(at, _ string) {
		if at == "gate" {
			if err := os.WriteFile(accented, []byte("HOST\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	across := func(from, to string) error {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: errCrossDevice}
	}
	for _, c := range []struct {
		what   string
		args   map[string]any
		rename func(string, string) error
		out    string
	}{
		{"a delete", map[string]any{"file_path": "vault/cafe\u0301.json", "action": "delete"}, nil, ""},
		{"a move", map[string]any{"file_path": "vault/cafe\u0301.json", "action": "move", "to": "notes/m.json"}, nil, "notes/m.json"},
		{"a move across filesystems", map[string]any{"file_path": "vault/cafe\u0301.json", "action": "move", "to": "notes/x.json"}, across, "notes/x.json"},
		{"a copy", map[string]any{"file_path": "vault/cafe\u0301.json", "action": "copy", "to": "notes/c.json"}, nil, "notes/c.json"},
	} {
		wt.rename = c.rename
		res := modelCall(t, r, "write", c.args)
		if !res.Refused || !strings.HasPrefix(res.Error, "access denied: protected path (") {
			t.Errorf("%s of the kept file the host saved after the gate is refused as a protected path: %+v", c.what, res)
		}
		if b, err := os.ReadFile(accented); err != nil || string(b) != "HOST\n" {
			t.Errorf("%s: the host's file is byte for byte where and as it made it: %q %v", c.what, b, err)
		}
		if c.out != "" {
			if b, err := os.ReadFile(filepath.Join(home, filepath.FromSlash(c.out))); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("%s carried the host's bytes out to %s: %q %v", c.what, c.out, b, err)
				os.Remove(filepath.Join(home, filepath.FromSlash(c.out)))
			}
		}
		os.Remove(accented)
	}
}

func TestATakeBackThatLeavesADirectoryItMadeSaysSo(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !folds(t, home, "dossie\u0301", "dossi\u00e9") {
		t.Skip("this volume does not open a decomposed accent as the composed one: no other spelling opens the place")
	}
	dir := filepath.Join(home, "dossi\u00e9")
	policy := firewall.DefaultPolicy()
	policy.SetPlaces([]*firewall.Rule{{ID: "sub.dossier", Kind: firewall.KindSubstrate, Path: dir, Reason: "kept", Enforced: true}})
	r := NewRegistry(home, policy, Timeouts{})
	wt := func() *WriteTool { w, _ := r.Get("write"); return w.(*WriteTool) }()
	wt.window = func(at, path string) {
		if at == "parents" {
			if err := os.WriteFile(filepath.Join(filepath.Dir(path), "other.txt"), []byte("B\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	res := modelCall(t, r, "write", map[string]any{"file_path": "dossie\u0301/a.txt", "content": "FORGED"})
	if res.Refused || !strings.Contains(res.Error, "access denied: protected path (") || !strings.Contains(res.Error, "is left") {
		t.Errorf("a refusal whose take-back left the directory it made is a failure naming it: %+v", res)
	}
	if _, err := os.Lstat(filepath.Join(dir, "a.txt")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the call's own file was not taken back: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "other.txt")); err != nil || string(b) != "B\n" {
		t.Errorf("the other actor's file is as it made it: %q %v", b, err)
	}
}

func TestAMadeFileThatCouldNotBeTakenBackIsAFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a directory's mode does not close it on Windows")
	}
	if os.Getuid() == 0 {
		t.Skip("root removes from a directory at mode 0500: nothing here fails to be taken back")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	vault := filepath.Join(home, "vault")
	if err := os.Mkdir(vault, 0o700); err != nil {
		t.Fatal(err)
	}
	if !folds(t, vault, "cafe\u0301.json", "caf\u00e9.json") {
		t.Skip("this volume does not open a decomposed accent as the composed one: no other spelling opens the place")
	}
	accented := filepath.Join(vault, "caf\u00e9.json")
	policy := firewall.DefaultPolicy()
	policy.SetPlaces([]*firewall.Rule{{ID: "sub.accent", Kind: firewall.KindSubstrate, Path: accented, Reason: "kept", Enforced: true}})
	r := NewRegistry(home, policy, Timeouts{})
	wt := func() *WriteTool { w, _ := r.Get("write"); return w.(*WriteTool) }()
	wt.held = func(ctx context.Context, path string, file os.FileInfo, by *os.File) error {
		if err := os.Chmod(vault, 0o500); err != nil {
			t.Fatal(err)
		}
		return r.heldAt(ctx, path, file, by)
	}
	t.Cleanup(func() { os.Chmod(vault, 0o700) })
	res := modelCall(t, r, "write", map[string]any{"file_path": "vault/cafe\u0301.json", "content": "FORGED"})
	if res.Refused || !strings.Contains(res.Error, "access denied: protected path (") || !strings.Contains(res.Error, "taking back") {
		t.Errorf("a refusal whose take-back failed is a failure saying what is left: %+v", res)
	}
}

func TestACopyOrAnEditDoesNotWriteIntoAKeptFileSavedAfterTheGate(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"vault", "notes"} {
		if err := os.Mkdir(filepath.Join(home, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if !folds(t, filepath.Join(home, "vault"), "cafe\u0301.json", "caf\u00e9.json") {
		t.Skip("this volume does not open a decomposed accent as the composed one: no other spelling opens the place")
	}
	if err := os.WriteFile(filepath.Join(home, "notes", "src.txt"), []byte("FORGED\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	accented := filepath.Join(home, "vault", "caf\u00e9.json")
	policy := firewall.DefaultPolicy()
	policy.SetPlaces([]*firewall.Rule{{ID: "sub.accent", Kind: firewall.KindSubstrate, Path: accented, Reason: "kept", Enforced: true}})
	r := NewRegistry(home, policy, Timeouts{})
	hostSaves := func(at, _ string) {
		if at == "gate" {
			if err := os.WriteFile(accented, []byte("HOST\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	w, _ := r.Get("write")
	w.(*WriteTool).window = hostSaves
	e, _ := r.Get("edit")
	e.(*EditTool).window = hostSaves
	for _, c := range []struct {
		what, tool string
		args       map[string]any
	}{
		{"a copy over it", "write", map[string]any{"file_path": "notes/src.txt", "action": "copy", "to": "vault/cafe\u0301.json"}},
		{"an edit of it", "edit", map[string]any{"file_path": "vault/cafe\u0301.json", "old_string": "HOST", "new_string": "FORGED"}},
	} {
		res := modelCall(t, r, c.tool, c.args)
		if !res.Refused || !strings.Contains(res.Error, "protected path (") {
			t.Errorf("%s, the host's kept file saved after the gate, is refused as a protected path: %+v", c.what, res)
		}
		if b, err := os.ReadFile(accented); err != nil || string(b) != "HOST\n" {
			t.Errorf("%s: the host's file is byte for byte as it made it: %q %v", c.what, b, err)
		}
		os.Remove(accented)
	}
}

func TestTheDirectoriesACallMadeAreTakenBackWhenItFailsPartWay(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for f, body := range map[string]string{"src.txt": "SRC\n", "kit/x.txt": "KIT\n"} {
		p := filepath.Join(home, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(home, "vault"), 0o700); err != nil {
		t.Fatal(err)
	}
	if !folds(t, filepath.Join(home, "vault"), "cafe\u0301.json", "caf\u00e9.json") {
		t.Skip("this volume does not open a decomposed accent as the composed one: no other spelling opens the place")
	}
	dir := filepath.Join(home, "dossi\u00e9")
	accented := filepath.Join(home, "vault", "caf\u00e9.json")
	policy := firewall.DefaultPolicy()
	policy.SetPlaces([]*firewall.Rule{
		{ID: "sub.dossier", Kind: firewall.KindSubstrate, Path: dir, Reason: "kept", Enforced: true},
		{ID: "sub.accent", Kind: firewall.KindSubstrate, Path: accented, Reason: "kept", Enforced: true},
	})
	r := NewRegistry(home, policy, Timeouts{})
	wt := func() *WriteTool { w, _ := r.Get("write"); return w.(*WriteTool) }()
	long := strings.Repeat("x", 300)
	intact := func(what string) {
		t.Helper()
		if b, err := os.ReadFile(filepath.Join(home, "src.txt")); err != nil || string(b) != "SRC\n" {
			t.Fatalf("%s took the source: %q %v", what, b, err)
		}
		if b, err := os.ReadFile(filepath.Join(home, "kit", "x.txt")); err != nil || string(b) != "KIT\n" {
			t.Fatalf("%s took the folder: %q %v", what, b, err)
		}
	}
	for _, place := range []struct{ what, spelled, path string }{
		{"a directory place", "dossie\u0301", dir},
		{"a file place", "vault/cafe\u0301.json", accented},
	} {
		below := place.spelled + "/" + long
		for _, c := range []struct {
			what string
			args map[string]any
		}{
			{"a write", map[string]any{"file_path": below + "/a.txt", "content": "FORGED"}},
			{"a move", map[string]any{"file_path": "src.txt", "action": "move", "to": below + "/a.txt"}},
			{"a file copy", map[string]any{"file_path": "src.txt", "action": "copy", "to": below + "/a.txt"}},
			{"a folder copy", map[string]any{"file_path": "kit", "action": "copy", "to": below + "/k", "recursive": true}},
		} {
			res := modelCall(t, r, "write", c.args)
			if res.Error == "" {
				t.Errorf("%s below %s, through a name too long, does not succeed: %+v", c.what, place.what, res)
			}
			if _, err := os.Lstat(place.path); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("%s below %s, failing part-way, left the place made: %v (answered %+v)", c.what, place.what, err, res)
				os.RemoveAll(place.path)
			}
			intact(c.what)
		}
		// A stop just after the directories were made.
		for _, c := range []struct {
			what string
			args map[string]any
		}{
			{"a move", map[string]any{"file_path": "src.txt", "action": "move", "to": place.spelled + "/a.txt"}},
			{"a file copy", map[string]any{"file_path": "src.txt", "action": "copy", "to": place.spelled + "/a.txt"}},
		} {
			ctx, cancel := context.WithCancel(context.Background())
			wt.window = func(at, _ string) {
				if at == "parents" {
					cancel()
				}
			}
			res, err := r.ExecuteModel(ctx, "write", c.args)
			wt.window = nil
			cancel()
			if err != nil || res.Error == "" {
				t.Errorf("%s below %s, stopped after its directories, does not succeed: %+v %v", c.what, place.what, res, err)
			}
			if _, err := os.Lstat(place.path); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("%s below %s, stopped after its directories, left the place made: %v (answered %+v)", c.what, place.what, err, res)
				os.RemoveAll(place.path)
			}
			intact(c.what)
		}
	}
}
