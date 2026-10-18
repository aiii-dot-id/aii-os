package firewall

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestASecondNameForAFileInsideAKeptDirectoryIsThatFile(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data, home := filepath.Join(base, "data"), filepath.Join(base, "home")
	put := func(path, body string) string {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	link := func(from, to string) string {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Link(from, to); err != nil {
			t.Skipf("hard links unavailable here: %v", err)
		}
		return to
	}
	key := put(filepath.Join(data, "identity.sec"), "KEY")
	deep := put(filepath.Join(data, "credentials", "deep", "token.json"), "TOKEN")
	frame := put(filepath.Join(data, "ui", "frame.html"), "FRAME")
	one := put(filepath.Join(home, "one.txt"), "a note")
	twin := put(filepath.Join(home, "twin-a.txt"), "a note twice")
	ownFile := put(filepath.Join(home, "planted.txt"), "the home's own")
	secondKey := link(key, filepath.Join(home, "k.txt"))
	secondDeep := link(deep, filepath.Join(home, "notes", "t.txt"))
	secondFrame := link(frame, filepath.Join(home, "f.html"))
	twinB := link(twin, filepath.Join(home, "twin-b.txt"))
	link(ownFile, filepath.Join(data, "planted.txt"))

	p := DefaultPolicy()
	p.SetPlaces([]*Rule{{ID: "sub.data", Kind: KindSubstrate, Path: data, Except: []string{filepath.Join(data, "ui")}, Reason: "kept", Enforced: true}})

	byHandle := func(path string) *Rule {
		t.Helper()
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			t.Fatal(err)
		}
		return p.KeptAs(info, f)
	}
	for what, path := range map[string]string{
		"a second name for the key":                           secondKey,
		"a second name for a credential two directories down": secondDeep,
		"the home's file, a second name of which is inside":   ownFile,
	} {
		if got := id(p.Covered(path)); got != "sub.data" {
			t.Errorf("Covered: %s (%s) is held by %q, want sub.data", what, path, got)
		}
		if v := p.Check("read", path); v.Allowed || id(v.Rule) != "sub.data" {
			t.Errorf("Check: %s (%s) is refused by the directory's rule: %+v", what, path, v)
		}
		if got := id(byHandle(path)); got != "sub.data" {
			t.Errorf("KeptAs through a handle: %s (%s) is held by %q, want sub.data", what, path, got)
		}
	}
	if runtime.GOOS != "windows" {
		info, err := os.Lstat(secondKey)
		if err != nil {
			t.Fatal(err)
		}
		if got := id(p.KeptAs(info, nil)); got != "sub.data" {
			t.Errorf("KeptAs of an entry's own stat (an Lstat before an unlink): held by %q, want sub.data", got)
		}
	}
	for what, path := range map[string]string{
		"a second name for a file the place leaves open": secondFrame,
		"a file with one name":                           one,
		"a file whose two names both lie in the home":    twinB,
	} {
		if rule := p.Covered(path); rule != nil {
			t.Errorf("Covered: %s (%s) is open, and was held by %s", what, path, rule.ID)
		}
		if v := p.Check("read", path); !v.Allowed {
			t.Errorf("Check: %s (%s) is open: %+v", what, path, v)
		}
		if rule := byHandle(path); rule != nil {
			t.Errorf("KeptAs through a handle: %s (%s) is open, and was held by %s", what, path, rule.ID)
		}
	}
	f, err := os.Open(twinB)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := f.Stat()
	if n := Names(info, f); n != 2 {
		t.Errorf("Names through a handle counts the two names of the twin: %d", n)
	}
	f.Close()
	if Names(nil, nil) != 0 {
		t.Error("nothing asked says nothing")
	}

	if runtime.GOOS == "windows" {
		t.Log("a directory's mode does not close it on Windows: the walk that cannot read is not asked")
		return
	}
	if os.Getuid() == 0 {
		t.Log("root reads a directory at mode 000: the walk that cannot read is not asked")
		return
	}
	shut := filepath.Join(data, "credentials", "deep")
	if err := os.Chmod(shut, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(shut, 0o700) })
	if _, err := os.ReadDir(shut); err == nil {
		t.Log("a directory at mode 000 is still read here: the walk that cannot read is not asked")
		return
	}
	rule := p.Covered(twinB)
	const said = "the host could not read " + "data/credentials/deep" + ", in a place it keeps (permission denied), so it cannot tell whether a file with more than one name is one of the files there, and refuses every such file until your operator makes " + "data/credentials/deep" + " readable to the host (removing it instead is the operator's own choice)"
	if rule == nil || rule.Kind != KindUnasked || rule.ID != "sub.data.unasked" || rule.Reason != filepath.FromSlash(said) || len(rule.Stops) != 0 {
		t.Errorf("a file with more than one name, while a kept directory cannot be walked whole: refused, naming the directory where the walk stopped and who clears it:\n got %+v\nwant %q", rule, filepath.FromSlash(said))
	}
	if v := p.Check("read", twinB); v.Allowed || v.Rule.Kind != KindUnasked {
		t.Errorf("Check refuses it too: %+v", v)
	}
	if rule := p.Covered(one); rule != nil {
		t.Errorf("a file with one name is open while a kept directory cannot be walked: %+v", rule)
	}
}

// ONE LOOK WALKS THE KEPT DIRECTORIES ONCE FOR THE CALL, AND THE LIMIT IS
// HELD HERE. A call that walks a tree asks about every file in it; the
// look it is given (Look) walks the kept directories at the first file
// with more than one name and answers the rest of the call from that
// walk. So a file the host makes inside a kept directory after that
// moment, given a second name before the call reaches it, is not known
// to that call; the next call's look, and a policy that is no call's,
// know it.
func TestOneLookWalksTheKeptDirectoriesOnceForTheCall(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data, home := filepath.Join(base, "data"), filepath.Join(base, "home")
	for _, d := range []string{data, home} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	twin := filepath.Join(home, "twin-a.txt")
	if err := os.WriteFile(twin, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(twin, filepath.Join(home, "twin-b.txt")); err != nil {
		t.Skipf("hard links unavailable here: %v", err)
	}
	p := DefaultPolicy()
	p.SetPlaces([]*Rule{{ID: "sub.data", Kind: KindSubstrate, Path: data, Reason: "kept", Enforced: true}})
	look := p.Look()
	if look.Look() != look {
		t.Fatal("a policy that is already one call's is answered as it is")
	}
	if look.Covered(twin) != nil {
		t.Fatal("the twin is open")
	}
	made := filepath.Join(data, "made-later.key")
	if err := os.WriteFile(made, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	second := filepath.Join(home, "later.txt")
	if err := os.Link(made, second); err != nil {
		t.Fatal(err)
	}
	if rule := look.Covered(second); rule != nil {
		t.Fatalf("the limit has moved: the call's walk holds a file made after it (%+v); correct this test and the KNOWN_LIMITATIONS row together", rule)
	}
	if id(p.Covered(second)) != "sub.data" {
		t.Error("a policy that is no call's walks for the question, and knows the file made later")
	}
	if id(p.Look().Covered(second)) != "sub.data" {
		t.Error("the next call's look knows the file made later")
	}
}

// AN OPEN NAME DOES NOT HIDE A KEPT NAME OF THE SAME FILE. A file may
// have a name a place leaves open (its Except) and a name the place keeps.
// The walk passes over what is left open, so the name it notes is the kept
// one; noting the open one first (it sorts first here) would read the file
// in the open spelling, and the place would let a third name read it. A
// file whose only kept-folder name is open stays open.
func TestAnOpenNameDoesNotHideAKeptNameOfTheSameFile(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data, home, open := filepath.Join(base, "data"), filepath.Join(base, "home"), filepath.Join(base, "data", "a-open")
	for _, d := range []string{open, home} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	key := filepath.Join(data, "z.key")
	frame := filepath.Join(open, "frame.html")
	for _, f := range []string{key, frame} {
		if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for from, to := range map[string]string{
		key:   filepath.Join(open, "copy-of-key"),
		frame: filepath.Join(home, "f.html"),
	} {
		if err := os.Link(from, to); err != nil {
			t.Skipf("hard links unavailable here: %v", err)
		}
	}
	if err := os.Link(key, filepath.Join(home, "k.txt")); err != nil {
		t.Fatal(err)
	}
	p := DefaultPolicy()
	p.SetPlaces([]*Rule{{ID: "sub.data", Kind: KindSubstrate, Path: data, Except: []string{open}, Reason: "kept", Enforced: true}})
	if got := id(p.Covered(filepath.Join(home, "k.txt"))); got != "sub.data" {
		t.Errorf("a name in the home for a file kept in data, with an open name too, is held by %q, want sub.data", got)
	}
	if rule := p.Covered(filepath.Join(home, "f.html")); rule != nil {
		t.Errorf("a name in the home for a file whose only name in data is open stays open: %+v", rule)
	}
}

func TestAWithoutOfOneCallsLookAsksTheSameWalk(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data, home := filepath.Join(base, "data"), filepath.Join(base, "home")
	for _, d := range []string{data, home} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	key := filepath.Join(data, "k.key")
	if err := os.WriteFile(key, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	second := filepath.Join(home, "k.txt")
	if err := os.Link(key, second); err != nil {
		t.Skipf("hard links unavailable here: %v", err)
	}
	p := DefaultPolicy()
	p.SetPlaces([]*Rule{{ID: "sub.data", Kind: KindSubstrate, Path: data, Reason: "kept", Enforced: true}})
	before := WalksForTest()
	look := p.Look()
	if id(look.Covered(second)) != "sub.data" {
		t.Fatal("the look keeps the second name")
	}
	if rule := look.Without(data).Covered(second); rule != nil {
		t.Errorf("a Without that takes the place as gone leaves its file's second name open: %+v", rule)
	}
	if id(look.Without(filepath.Join(base, "elsewhere")).Covered(second)) != "sub.data" {
		t.Error("a Without of something else keeps it")
	}
	if n := WalksForTest() - before; n != 1 {
		t.Errorf("the look and its Withouts walked the kept directories %d times, want once", n)
	}
}

func TestAFolderThatCannotBeReadUnderAGonePathRefusesNothingForTheWithout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a directory's mode does not close it on Windows")
	}
	if os.Getuid() == 0 {
		t.Skip("root reads a directory at mode 000")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gone, home := filepath.Join(base, "gone"), filepath.Join(base, "home")
	data := filepath.Join(gone, "data")
	shut := filepath.Join(data, "shut")
	for _, d := range []string{shut, home} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	twin := filepath.Join(home, "twin-a")
	if err := os.WriteFile(twin, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(twin, filepath.Join(home, "twin-b")); err != nil {
		t.Skipf("hard links unavailable here: %v", err)
	}
	if err := os.Chmod(shut, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(shut, 0o700) })
	if _, err := os.ReadDir(shut); err == nil {
		t.Skip("a directory at mode 000 is still read here")
	}
	p := DefaultPolicy()
	p.SetPlaces([]*Rule{{ID: "sub.data", Kind: KindSubstrate, Path: data, Reason: "kept", Enforced: true}})
	look := p.Look()
	if rule := look.Covered(twin); rule == nil || rule.Kind != KindUnasked {
		t.Fatalf("the look refuses a file with two names while a kept folder cannot be read: %+v", rule)
	}
	if rule := look.Without(gone).Covered(twin); rule != nil {
		t.Errorf("for a Without that takes the folder's place as gone, the folder that cannot be read refuses nothing: %+v", rule)
	}
}

func TestAKeptNameOfAFileIsFoundPastOneUnderAGonePath(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data, home := filepath.Join(base, "data"), filepath.Join(base, "home")
	for _, d := range []string{filepath.Join(data, "a-gone"), filepath.Join(data, "z"), home} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	first := filepath.Join(data, "a-gone", "k.key")
	if err := os.WriteFile(first, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, to := range []string{filepath.Join(data, "z", "k.key"), filepath.Join(home, "k.txt")} {
		if err := os.Link(first, to); err != nil {
			t.Skipf("hard links unavailable here: %v", err)
		}
	}
	p := DefaultPolicy()
	p.SetPlaces([]*Rule{{ID: "sub.data", Kind: KindSubstrate, Path: data, Reason: "kept", Enforced: true}})
	if got := id(p.Look().Without(filepath.Join(data, "a-gone")).Covered(filepath.Join(home, "k.txt"))); got != "sub.data" {
		t.Errorf("the home's name is kept by the file's second kept name, past the first under a gone path: held by %q", got)
	}
}
