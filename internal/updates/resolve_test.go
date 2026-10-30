package updates

import (
	"errors"
	"path/filepath"
	"testing"
)

// THE HANDLE ANSWERS WHERE THE LINKS CANNOT (W1). An install reached
// through a Windows junction could not update: EvalSymlinks stopped at the
// junction and the target was refused before any download. The order is
// held here on every system, with both answers stood in for: EvalSymlinks
// first, so a name stays the string an earlier release wrote into an
// update record; the handle's final path only when EvalSymlinks fails; and
// EvalSymlinks' own error when neither answers. The junction itself is
// run on Windows (resolve_windows_test.go).
func TestTheProgramIsResolvedByItsHandleWhereItsLinksCannotBe(t *testing.T) {
	restore := func(eval func(string) (string, error), final func(string) (string, error)) {
		t.Cleanup(func() { evalSymlinks, finalPath = eval, final })
	}
	restore(evalSymlinks, finalPath)
	dir := t.TempDir()
	exe := filepath.Join(dir, "through", "aii")
	lies := filepath.Join(dir, "lies", "aii")
	refused := errors.New("The system cannot find the path specified.")

	// Where EvalSymlinks answers, its answer is the name, and the handle is
	// not asked.
	asked := false
	evalSymlinks = func(string) (string, error) { return lies, nil }
	finalPath = func(string) (string, error) { asked = true; return "elsewhere", nil }
	tgt, err := targetOf(exe)
	if err != nil || tgt.path != lies || asked {
		t.Fatalf("a path EvalSymlinks resolves: target %q, %v, handle asked %v; want %q and the handle unasked", tgt.path, err, asked, lies)
	}

	// Where it cannot, the handle's final path is the target, and the
	// files that guard it lie beside where the program lies.
	evalSymlinks = func(string) (string, error) { return "", refused }
	var askedFor []string
	finalPath = func(p string) (string, error) { askedFor = append(askedFor, p); return lies, nil }
	tgt, err = targetOf(exe)
	if err != nil {
		t.Fatalf("a program behind what EvalSymlinks cannot follow was refused: %v", err)
	}
	if len(askedFor) != 1 || askedFor[0] != exe {
		t.Errorf("the handle was asked for %q, not the program %q", askedFor, exe)
	}
	if tgt.path != lies || tgt.record != filepath.Join(filepath.Dir(lies), ".aii.update.json") {
		t.Errorf("the target is not where the handle says the program lies: %+v", tgt)
	}
	// The identity that applies an update is named the same way.
	if owner, err := ownerOf(dir); err != nil || owner != lies {
		t.Errorf("the data directory behind what EvalSymlinks cannot follow: %q, %v", owner, err)
	}

	// Where neither answers, the refusal is EvalSymlinks' own, as before.
	finalPath = func(string) (string, error) { return "", errors.ErrUnsupported }
	if _, err := targetOf(exe); !errors.Is(err, refused) {
		t.Errorf("with neither answer the refusal is %v, want EvalSymlinks' own", err)
	}
}
