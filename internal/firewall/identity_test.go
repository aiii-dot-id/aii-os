package firewall

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A PLACE IS KEPT BY WHAT IT IS. A second name for a kept file is that
// file: Covered and Check answer for it as for the place's own path. A
// path that is no second name for anything kept is answered as before.
//
// And the limit, held here as a fact: a save that writes a new file and
// renames it over the place leaves the earlier second name on the earlier
// file, which is no longer the place, and the wall does not know that name
// after it. The host's own saves (atomicfile.Replace, under WriteReplace)
// empty the earlier file when it still has a name, so the name holds
// nothing of it (TestAReplacedFileDoesNotStayReadableUnderAnotherName);
// this test renames by hand, as a save that does not go through them
// would, and the name then holds the bytes the file held before.
func TestAPlaceIsKnownUnderASecondNameForTheSameFile(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	kept := filepath.Join(dir, "kept.key")
	second := filepath.Join(dir, "elsewhere", "second-name")
	other := filepath.Join(dir, "elsewhere", "other")
	if err := os.MkdirAll(filepath.Dir(second), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{kept, other} {
		if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Link(kept, second); err != nil {
		t.Skipf("hard links unavailable here: %v", err)
	}
	p := DefaultPolicy()
	p.SetPlaces([]*Rule{{ID: "sub.key", Kind: KindSubstrate, Path: kept, Reason: "kept", Enforced: true}})
	if rule := p.Covered(second); rule == nil || rule.ID != "sub.key" {
		t.Fatalf("a second name for a kept file is the kept file: %+v", rule)
	}
	if v := p.Check("read", second); v.Allowed || v.Rule.ID != "sub.key" {
		t.Fatalf("Check answers for the second name as for the place: %+v", v)
	}
	if p.Covered(other) != nil || !p.Check("read", other).Allowed {
		t.Fatal("a file that is no second name for anything kept is open, as before")
	}
	if p.Covered(filepath.Join(dir, "elsewhere", "not-made-yet")) != nil {
		t.Fatal("a path that does not exist beside it is open, as before")
	}
	if p.Covered("relative/second-name") != nil {
		t.Fatal("a relative string is not asked what it is")
	}

	// The host saves the place: a new file, renamed over it.
	saved := filepath.Join(dir, "kept.key.new")
	if err := os.WriteFile(saved, []byte("y"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(saved, kept); err != nil {
		t.Fatal(err)
	}
	if p.Covered(kept) == nil {
		t.Fatal("the place is kept by its own path whatever file is there")
	}
	if rule := p.Covered(second); rule != nil {
		t.Fatalf("the limit has moved: the earlier second name is known after a save by rename (%+v); correct this test, the contract addendum and KNOWN_LIMITATIONS together", rule)
	}
	fresh := filepath.Join(dir, "elsewhere", "fresh-name")
	if err := os.Link(kept, fresh); err != nil {
		t.Fatal(err)
	}
	if p.Covered(fresh) == nil {
		t.Fatal("a second name made for the saved file is the kept file")
	}
}

func TestAPlaceIsKnownUnderASecondPathToItsDirectory(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "home")
	data := filepath.Join(home, "data")
	for _, d := range []string{filepath.Join(data, "ui"), filepath.Join(data, "ui2"), filepath.Join(home, "made"), filepath.Join(home, "notes")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	again := filepath.Join(base, "again") // the home a second time
	if err := os.Symlink(home, again); err != nil {
		t.Skipf("links unavailable here: %v", err)
	}
	p := DefaultPolicy()
	p.SetPlaces([]*Rule{
		{ID: "sub.data", Kind: KindSubstrate, Path: data, Except: []string{filepath.Join(data, "ui")}, Reason: "kept", Enforced: true},
		{ID: "sub.one", Kind: KindSubstrate, Path: filepath.Join(home, "made", "token.json"), Reason: "kept", Enforced: true},
		{ID: "sub.two", Kind: KindSubstrate, Path: filepath.Join(home, "vault", "inner", "token.json"), Reason: "kept", Enforced: true},
		{ID: "sub.three", Kind: KindSubstrate, Path: filepath.Join(home, "a", "b", "c", "token.json"), Reason: "kept", Enforced: true},
	})
	for _, c := range []struct {
		what, rel, rule string
	}{
		{"a file of a kept directory", "data/credentials/key.json", "sub.data"},
		{"the kept directory itself", "data", "sub.data"},
		{"what the place leaves open", "data/ui/frame.html", ""},
		{"the open directory itself", "data/ui", ""},
		{"a sibling that shares the open directory's prefix", "data/ui2/frame.html", "sub.data"},
		{"a place not made yet, its directory made", "made/token.json", "sub.one"},
		{"a place not made yet, two levels unmade", "vault/inner/token.json", "sub.two"},
		{"a place not made yet, three levels unmade", "a/b/c/token.json", "sub.three"},
		{"a directory above a place not made yet is not the place", "vault/inner", ""},
		{"a neighbour of a place not made yet", "vault/inner/other.json", ""},
		{"a file of the home that nothing keeps", "notes/n.md", ""},
	} {
		rule := p.coveredRespelled("", filepath.Join(again, filepath.FromSlash(c.rel)))
		if got := id(rule); got != c.rule {
			t.Errorf("%s (%s) by the second path: held by %q, want %q", c.what, c.rel, got, c.rule)
		}
		// By the place's own spelling the pass has nothing to say: the
		// spelling decides, as before.
		if rule := p.coveredRespelled("", filepath.Join(home, filepath.FromSlash(c.rel))); rule != nil {
			t.Errorf("%s (%s) by its own spelling was respelled: %+v", c.what, c.rel, rule)
		}
		if got := id(p.Covered(filepath.Join(home, filepath.FromSlash(c.rel)))); got != c.rule {
			t.Errorf("%s (%s) by its own spelling: held by %q, want %q", c.what, c.rel, got, c.rule)
		}
	}
}

func id(r *Rule) string {
	if r == nil {
		return ""
	}
	return r.ID
}

func TestAPlaceThatCannotBeAskedRefusesWhatItCannotCompare(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a directory's mode does not close it on Windows")
	}
	if os.Getuid() == 0 {
		t.Skip("root searches a directory at mode 000: nothing here fails to be asked")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(dir, "locked")
	kept := filepath.Join(locked, "kept.key")
	second := filepath.Join(dir, "elsewhere", "second-name")
	other := filepath.Join(dir, "elsewhere", "other")
	for _, d := range []string{locked, filepath.Dir(second)} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{kept, other} {
		if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Link(kept, second); err != nil {
		t.Skipf("hard links unavailable here: %v", err)
	}
	p := DefaultPolicy()
	p.SetPlaces([]*Rule{
		{ID: "sub.absent", Kind: KindSubstrate, Path: filepath.Join(dir, "never", "made", "x.key"), Reason: "kept", Enforced: true},
		{ID: "sub.key", Kind: KindSubstrate, Path: kept, Reason: "kept", Enforced: true},
	})
	if rule := p.Covered(second); id(rule) != "sub.key" || rule.Kind != KindSubstrate {
		t.Fatalf("before: the second name is the kept file: %+v", rule)
	}
	if p.Covered(other) != nil {
		t.Fatal("before: a place that is plainly not there refuses nothing, and another file is open")
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o700) })
	if _, err := os.Stat(kept); err == nil {
		t.Skip("the directory at mode 000 is still searched here: nothing fails to be asked")
	}
	unasked := func(what string, rule *Rule) {
		t.Helper()
		if rule == nil || rule.Kind != KindUnasked || rule.ID != "sub.key.unasked" || rule.Reason == "kept" ||
			!strings.Contains(rule.Reason, "could not check kept.key") || !strings.Contains(rule.Reason, "operator") {
			t.Fatalf("%s: refused as a place that could not be checked, saying so and not the place's reason: %+v", what, rule)
		}
	}
	unasked("the second name of a kept file that cannot be checked", p.Covered(second))
	v := p.Check("read", second)
	if v.Allowed {
		t.Fatalf("Check opens the second name of a kept file that cannot be checked: %+v", v)
	}
	unasked("Check", v.Rule)
	unasked("a file nothing keeps, which cannot be told apart from it", p.Covered(other))
	if p.Covered("relative/name") != nil {
		t.Fatal("a relative string is not asked what it is, and is not refused for it")
	}
	if err := os.Chmod(locked, 0o700); err != nil {
		t.Fatal(err)
	}
	if id(p.Covered(second)) != "sub.key" || p.Covered(other) != nil {
		t.Fatal("once the place can be checked again the answers are the ones before")
	}
}

func TestEveryPlaceThatCannotBeCheckedIsNamed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a directory's mode does not close it on Windows")
	}
	if os.Getuid() == 0 {
		t.Skip("root searches a directory at mode 000: nothing here fails to be asked")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	one, two := filepath.Join(dir, "one"), filepath.Join(dir, "two")
	for _, d := range []string{one, two, filepath.Join(dir, "open")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	p := DefaultPolicy()
	p.SetPlaces([]*Rule{
		{ID: "sub.a", Kind: KindSubstrate, Path: filepath.Join(one, "a.key"), Reason: "kept", Enforced: true},
		{ID: "sub.open", Kind: KindSubstrate, Path: filepath.Join(dir, "open", "o.key"), Reason: "kept", Enforced: true},
		{ID: "sub.b", Kind: KindSubstrate, Path: filepath.Join(two, "b.key"), Reason: "kept", Enforced: true},
	})
	for _, d := range []string{one, two} {
		if err := os.Chmod(d, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(d, 0o700) })
	}
	if _, err := os.Stat(filepath.Join(one, "a.key")); err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Skipf("a directory at mode 000 is still searched here (%v): nothing fails to be asked", err)
	}
	other := filepath.Join(dir, "elsewhere")
	both := "the host could not check a.key, b.key, a place it keeps (permission denied), so it cannot tell any path apart from it until your operator clears that"
	for what, rule := range map[string]*Rule{"Covered": p.Covered(other), "Check": p.Check("read", other).Rule} {
		if rule == nil || rule.Kind != KindUnasked || rule.ID != "sub.a.unasked,sub.b.unasked" || rule.Reason != both {
			t.Errorf("%s names both places that cannot be checked, in rule order, under an ID of its own:\n got %+v\nwant ID %q, reason %q", what, rule, "sub.a.unasked,sub.b.unasked", both)
		}
	}
	if err := os.Chmod(one, 0o700); err != nil {
		t.Fatal(err)
	}
	if rule := p.Covered(other); rule == nil || rule.ID != "sub.b.unasked" ||
		rule.Reason != "the host could not check b.key, a place it keeps (permission denied), so it cannot tell any path apart from it until your operator clears that" {
		t.Errorf("once the first can be checked the refusal names the other alone: %+v", rule)
	}
}

func TestTheLettersAreComparedBySimpleCaseFolding(t *testing.T) {
	for _, c := range []struct{ a, b string }{
		{"/home/config.jſon", "/home/config.json"},
		{"/home/ſecrets/google/token.json", "/home/secrets/google/token.json"},
		{"/home/Key.json", "/home/key.json"},
		{"/HOME/Config.JSON", "/home/config.json"},
		{"/home/Σσ", "/home/σς"},
		{"/home/İ", "/home/i"},
	} {
		if canonical(c.a) != canonical(c.b) {
			t.Errorf("canonical(%q) = %q and canonical(%q) = %q: one name to a volume that folds case", c.a, canonical(c.a), c.b, canonical(c.b))
		}
	}
	if got := canonical(`/Home/A B/Config.JSON`); got != "/home/a b/config.json" {
		t.Errorf("an ASCII path's canonical bytes are lower case, as before: %q", got)
	}
	if canonical("/home/s") == canonical("/home/t") || canonical("/home/café") == canonical("/home/cafe") {
		t.Error("letters that are not one case orbit stay apart")
	}
}

func TestANameHeldOnlyUnderTheFoldIsAskedWhatItIs(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := DefaultPolicy()
	kept := filepath.Join(dir, "providers.json")
	alias := filepath.Join(dir, "providerſ.json")
	if v := p.Check("write", alias); v.Allowed || v.Rule == nil || v.Rule.ID != "sub.providers" {
		t.Fatalf("a kept name not made yet is refused under the fold: %+v", v)
	}
	if err := os.WriteFile(kept, []byte("kept"), 0o600); err != nil {
		t.Fatal(err)
	}
	if v := p.Check("read", kept); v.Allowed {
		t.Fatalf("the kept name is refused: %+v", v)
	}
	keptInfo, err := os.Stat(kept)
	if err != nil {
		t.Fatal(err)
	}
	if aliasInfo, err := os.Stat(alias); err == nil && os.SameFile(keptInfo, aliasInfo) {
		if v := p.Check("read", alias); v.Allowed || v.Rule == nil || v.Rule.ID != "sub.providers" {
			t.Fatalf("on a volume that folds, the alias is the kept file and is refused: %+v", v)
		}
		return
	}
	if err := os.WriteFile(alias, []byte("another file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if v := p.Check("read", alias); !v.Allowed {
		t.Fatalf("another file, whose name meets a kept one only under the fold, is open: %+v", v)
	}
	if v := p.Check("write", alias); !v.Allowed {
		t.Fatalf("another file, whose name meets a kept one only under the fold, is open to a write: %+v", v)
	}
}

func TestAPlaceBehindAFileOrALoopOfLinksIsNotMadeAndRefusesNothing(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(dir, "other")
	file := filepath.Join(dir, "secrets")
	for _, f := range []string{other, file} {
		if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	places := []*Rule{{ID: "sub.file", Kind: KindSubstrate, Path: filepath.Join(file, "google", "token.json"), Reason: "kept", Enforced: true}}
	loop := filepath.Join(dir, "loop")
	if err := os.Symlink(loop, loop); err != nil {
		t.Logf("links unavailable here (%v): only the file is asked", err)
	} else {
		places = append(places, &Rule{ID: "sub.loop", Kind: KindSubstrate, Path: filepath.Join(loop, "kept.key"), Reason: "kept", Enforced: true})
	}
	p := DefaultPolicy()
	p.SetPlaces(places)
	if rule := p.Covered(other); rule != nil {
		t.Fatalf("a place behind a file or a loop of links refuses a path it is not: %+v", rule)
	}
	if v := p.Check("read", other); !v.Allowed {
		t.Fatalf("Check refuses a path for a place behind a file or a loop: %+v", v)
	}
	if rule := p.Covered(filepath.Join(file, "google", "token.json")); id(rule) != "sub.file" {
		t.Fatalf("the place itself is kept by its own path still: %+v", rule)
	}
}

// A STREAM IS JUDGED BY THE FILE IT IS ON (Windows). The last element of
// a path cut at its first colon is the file; on Windows a stream of a kept
// file is the kept file, and a stream of a file nothing keeps is open as
// that file is. Elsewhere a colon is a letter of the name and nothing is
// cut.
func TestAStreamIsJudgedByTheFileItIsOn(t *testing.T) {
	at := func(parts ...string) string {
		return filepath.Join(append([]string{string(filepath.Separator), "home"}, parts...)...)
	}
	for in, want := range map[string]string{
		at("conf", "secret.json:x"):       at("conf", "secret.json"),
		at("conf", "secret.json:x:$DATA"): at("conf", "secret.json"),
		at("conf", "secret.json::$DATA"):  at("conf", "secret.json"),
		at("conf", "secret.json"):         at("conf", "secret.json"),
		at("notes.md:x"):                  at("notes.md"),
	} {
		if got := streamFile(in); got != want {
			t.Errorf("streamFile(%q) = %q, want %q", in, got, want)
		}
	}
	if runtime.GOOS != "windows" {
		if fileOf(at("conf", "secret.json:x")) != at("conf", "secret.json:x") {
			t.Fatal("outside Windows a colon is a letter of the name")
		}
		return
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	kept := filepath.Join(dir, "conf", "secret.json")
	p := DefaultPolicy()
	p.SetPlaces([]*Rule{{ID: "sub.profile", Kind: KindSubstrate, Path: kept, Reason: "kept", Enforced: true}})
	for _, s := range []string{kept + ":x", kept + ":x:$DATA", kept + "::$DATA"} {
		if id(p.Covered(s)) != "sub.profile" || p.Check("write", s).Allowed {
			t.Errorf("%s, a stream of a kept file, is the kept file", s)
		}
	}
	open := filepath.Join(dir, "notes.md")
	if p.Covered(open+":x") != nil || !p.Check("write", open+":x").Allowed {
		t.Error("a stream of a file nothing keeps is open, as the file is")
	}
}
