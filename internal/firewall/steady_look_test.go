package firewall

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// steadyRig is a policy with many kept places, some made and some not
// (whose ancestors a question walks up through), and a tree a listing
// walks: the shape that made a two-level listing of a large drive take
// nineteen minutes (an identity's log, 2026-11-08).
func steadyRig(t *testing.T, places int) (p *Policy, base string, entries []string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var rules []*Rule
	for i := 0; i < places; i++ {
		path := filepath.Join(base, "places", fmt.Sprintf("p%03d", i))
		if i%2 == 0 {
			if err := os.MkdirAll(path, 0o700); err != nil {
				t.Fatal(err)
			}
		} else {
			path = filepath.Join(path, "not", "made", "yet")
		}
		rules = append(rules, &Rule{ID: fmt.Sprintf("sub.p%03d", i), Kind: KindSubstrate, Path: path, Reason: "kept", Enforced: true})
	}
	p = DefaultPolicy()
	p.SetPlaces(rules)
	walked := filepath.Join(base, "drive", "events")
	if err := os.MkdirAll(walked, 0o700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		e := filepath.Join(walked, fmt.Sprintf("%016x", i))
		if err := os.WriteFile(e, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, e)
	}
	return p, base, entries
}

// A STEADY LOOK ASKS EACH PLACE ONCE FOR THE CALL. A call that changes
// nothing asks every entry it walks about every kept place; a steady look
// asks each place's path (and a place not made, its ancestors) once for the
// whole call, however many entries are asked about. The control: a look a
// writing call is given asks them again at every question.
func TestASteadyLookAsksEachPlaceOnceForTheCall(t *testing.T) {
	p, _, entries := steadyRig(t, 50)
	steady := p.SteadyLook()
	before := PlaceStatsForTest()
	if v := steady.Check("ls", entries[0]); !v.Allowed {
		t.Fatalf("an open entry was refused: %+v", v)
	}
	once := PlaceStatsForTest() - before
	if once == 0 {
		t.Fatal("the first question asked no place: the count does not see the places' paths")
	}
	for _, e := range entries[1:] {
		if v := steady.Check("ls", e); !v.Allowed {
			t.Fatalf("an open entry was refused: %+v", v)
		}
		if rule, err := steady.CoveredFor("ls", e); rule != nil || err != nil {
			t.Fatalf("an open entry is covered: %+v %v", rule, err)
		}
	}
	if all := PlaceStatsForTest() - before; all != once {
		t.Fatalf("a steady look asked the places %d times over %d entries; want the first question's %d, once for the call", all, len(entries), once)
	}

	look := p.Look()
	before = PlaceStatsForTest()
	for _, e := range entries[:10] {
		look.Check("write", e)
	}
	if again := PlaceStatsForTest() - before; again < 10*once {
		t.Fatalf("a look a writing call is given asked the places %d times over 10 questions; want them asked again at each (%d each)", again, once)
	}
}

// A STEADY LOOK ANSWERS AS THE POLICY DOES. What it keeps is each path's own
// answer, so a kept place, a second spelling of it through a link, a hard
// link to a kept file, a file under a gone path, a place not made with an
// entry standing at its path, and an open file read the same through a
// steady look, a look, and a policy that is no call's.
func TestASteadyLookAnswersAsThePolicyDoes(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data, home, notMade := filepath.Join(base, "data"), filepath.Join(base, "home"), filepath.Join(base, "later", "kept")
	for _, d := range []string{data, home, filepath.Dir(notMade)} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	secret := filepath.Join(data, "secret.key")
	if err := os.WriteFile(secret, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	open := filepath.Join(home, "notes.txt")
	if err := os.WriteFile(open, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	asked := []string{secret, open, filepath.Join(data, "missing.txt"), filepath.Join(notMade, "x")}
	if runtime.GOOS != "windows" {
		alias := filepath.Join(home, "alias")
		if err := os.Symlink(data, alias); err != nil {
			t.Fatal(err)
		}
		dangling := filepath.Join(home, "dangling")
		if err := os.Symlink(filepath.Join(base, "nowhere"), dangling); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(dangling, notMade); err != nil {
			t.Fatal(err)
		}
		asked = append(asked, filepath.Join(alias, "secret.key"), notMade)
	}
	if err := os.Link(secret, filepath.Join(home, "twin.key")); err == nil {
		asked = append(asked, filepath.Join(home, "twin.key"))
	}
	p := DefaultPolicy()
	p.SetPlaces([]*Rule{
		{ID: "sub.data", Kind: KindSubstrate, Path: data, Reason: "kept", Enforced: true},
		{ID: "sub.later", Kind: KindSubstrate, Path: notMade, Reason: "kept", Enforced: true},
	})
	// A Without that takes a kept place's own path as gone changes what the
	// places answer, so a steady look's copy must not answer from the look's.
	gone := p.Without(data)
	answer := func(q *Policy, path string) string {
		v := q.Check("ls", path)
		rule, err := q.CoveredFor("ls", path)
		return fmt.Sprintf("allowed=%v rule=%s covered=%s err=%v", v.Allowed, ruleID(v.Rule), ruleID(rule), err != nil)
	}
	steady, look := p.SteadyLook(), p.Look()
	steadyGone := steady.Without(data)
	for _, path := range asked {
		want := answer(p, path)
		if got := answer(steady, path); got != want {
			t.Errorf("%s: a steady look answered %s; the policy %s", path, got, want)
		}
		if got := answer(look, path); got != want {
			t.Errorf("%s: a look answered %s; the policy %s", path, got, want)
		}
		if got, want := answer(steadyGone, path), answer(gone, path); got != want {
			t.Errorf("%s: a Without of a steady look answered %s; the policy's Without %s", path, got, want)
		}
	}
	// And what the places came to is kept for each set of gone paths: the
	// look's keeps the data place, its Without's does not, however the two
	// are asked in turn.
	keeps := func(q *Policy) bool {
		for _, k := range q.placeAnswers().kept {
			if k.path == data {
				return true
			}
		}
		return false
	}
	if !keeps(steady) || keeps(steadyGone) || !keeps(steady) {
		t.Fatalf("a steady look and its Without answered the places from one another's set (look keeps data: %v, Without keeps it: %v)", keeps(steady), keeps(steadyGone))
	}
}

func ruleID(r *Rule) string {
	if r == nil {
		return "-"
	}
	return r.ID
}

// A STEADY LOOK IS STEADY FOR ITS CALL ONLY. A place made after a steady
// look's first question is not known to that call (it changes nothing, so
// nothing it does makes one); the next call's look, and a policy that is no
// call's, know it. A look a writing call is given knows it at once.
func TestASteadyLookIsSteadyForItsCallOnly(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		t.Skip("the second spelling here is a symbolic link")
	}
	kept, home := filepath.Join(base, "kept"), filepath.Join(base, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	p := DefaultPolicy()
	p.SetPlaces([]*Rule{{ID: "sub.kept", Kind: KindSubstrate, Path: kept, Reason: "kept", Enforced: true}})
	steady, look := p.SteadyLook(), p.Look()
	alias := filepath.Join(home, "alias")
	steady.Check("ls", filepath.Join(home, "first"))
	look.Check("write", filepath.Join(home, "first"))
	if err := os.MkdirAll(kept, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(kept, alias); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(alias, "x")
	if ruleID(look.Check("write", inside).Rule) != "sub.kept" {
		t.Error("a look a writing call is given did not know a place made during its call")
	}
	if ruleID(p.Check("ls", inside).Rule) != "sub.kept" || ruleID(p.SteadyLook().Check("ls", inside).Rule) != "sub.kept" {
		t.Error("the policy and the next call's steady look do not know the place made")
	}
	if steady.SteadyLook() != steady || steady.Look() != steady {
		t.Error("a policy that is already one call's is answered as it is")
	}
}
