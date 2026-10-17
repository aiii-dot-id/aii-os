//go:build darwin && !ios

package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fakeFlow is the app's uninstall with a fake aii answering from
// fixtures, and fake dialogs answering as told: everything asked and run
// is recorded.
type fakeFlow struct {
	t        *testing.T
	runs     [][]string
	asked    []string
	told     []string
	revealed []string
	answers  []bool // each ask's answer, in turn; a missing one is no
	yes      string // the fixture -yes answers with
	yesCode  int
}

func (f *fakeFlow) ui() uninstallUI {
	return uninstallUI{
		aii: func(args ...string) ([]byte, []byte, int, error) {
			f.runs = append(f.runs, args)
			name, code := "uninstall-plan.json", 0
			if slices.Contains(args, "-yes") {
				name, code = f.yes, f.yesCode
			}
			raw, err := os.ReadFile(filepath.Join("testdata", name))
			if err != nil {
				f.t.Fatal(err)
			}
			return raw, nil, code, nil
		},
		ask: func(q, yes, no string) (bool, bool) {
			f.asked = append(f.asked, q)
			if len(f.answers) == 0 {
				return false, false
			}
			a := f.answers[0]
			f.answers = f.answers[1:]
			return a, true
		},
		tell:   func(title, msg string) { f.told = append(f.told, title+": "+msg) },
		reveal: func(p string) error { f.revealed = append(f.revealed, p); return nil },
	}
}

const testBundle = "/Applications/AII OS.app"

func (f *fakeFlow) ranYes() bool {
	for _, r := range f.runs {
		if slices.Contains(r, "-yes") {
			return true
		}
	}
	return false
}

// CANCEL RUNS NOTHING: the plan is read, the question is asked, and an
// answer of Cancel — or no answer at all, no session to ask in — runs no
// uninstall.
func TestCancelRunsNothing(t *testing.T) {
	for _, answers := range [][]bool{{false}, nil} {
		f := &fakeFlow{t: t, answers: answers, yes: "uninstall-done.json"}
		if err := runUninstallFlow(f.ui(), testBundle); err != nil {
			t.Fatal(err)
		}
		if len(f.runs) != 1 || !slices.Equal(f.runs[0], []string{"uninstall", "-plan", "-json"}) || f.ranYes() {
			t.Fatalf("answers %v: aii ran %q; only the plan may run before a yes", answers, f.runs)
		}
	}
}

// THE QUESTION NAMES WHO STOPS AND SAYS THEIR DATA STAYS; THE SHEET NAMES
// EVERY IDENTITY'S FOLDER (the one on another volume by both its paths),
// what was removed and freed, and another account's use of the shared
// bundle; Show in Finder shows the bundle.
func TestTheSheetNamesEveryIdentitysFolder(t *testing.T) {
	f := &fakeFlow{t: t, answers: []bool{true, true}, yes: "uninstall-done.json"}
	if err := runUninstallFlow(f.ui(), testBundle); err != nil {
		t.Fatal(err)
	}
	if len(f.asked) != 2 || !f.ranYes() {
		t.Fatalf("asked %d questions, ran %q", len(f.asked), f.runs)
	}
	q := f.asked[0]
	for _, want := range []string{"Vega (/Users/example/.aii/identity-0)", "identity-4, not yet born", "data stays exactly where it is", "3.0 GB", "drag AII OS.app to the Trash"} {
		if !strings.Contains(q, want) {
			t.Errorf("the question does not say %q:\n%s", want, q)
		}
	}
	sheet := f.asked[1]
	for _, want := range []string{"Vega: /Users/example/.aii/identity-0", "/Users/example/.aii/identity-4 (on /srv/archive/slot)", "3.0 GB freed", "Another account on this Mac runs this AII OS.app", "Last step: drag AII OS.app to the Trash"} {
		if !strings.Contains(sheet, want) {
			t.Errorf("the sheet does not say %q:\n%s", want, sheet)
		}
	}
	if !slices.Equal(f.revealed, []string{testBundle}) {
		t.Fatalf("Show in Finder revealed %q", f.revealed)
	}
}

// A FAILED STEP IS SAID WITH ITS REMEDY, in a dialog of its own, and no
// sheet says it is uninstalled.
func TestAFailedStepIsSaidWithItsRemedy(t *testing.T) {
	f := &fakeFlow{t: t, answers: []bool{true}, yes: "uninstall-stopped.json", yesCode: 3}
	if err := runUninstallFlow(f.ui(), testBundle); err != nil {
		t.Fatal(err)
	}
	if len(f.told) != 1 || !strings.Contains(f.told[0], "store step") || !strings.Contains(f.told[0], "activation runs from") ||
		!strings.Contains(f.told[0], "What to do: quit the identity that runs from it") || !strings.Contains(f.told[0], "Nothing of any identity was removed") {
		t.Fatalf("the stop was said as %q", f.told)
	}
	if len(f.asked) != 1 {
		t.Fatalf("a sheet followed a stop: %q", f.asked)
	}
}

// AN UNINSTALL THAT CANNOT RUN IS AN ERROR THE CALLER SHOWS: aii's own
// refusal (exit 1) is said in its words, and nothing else runs.
func TestARefusedUninstallIsSaidInItsOwnWords(t *testing.T) {
	ui := uninstallUI{
		aii: func(...string) ([]byte, []byte, int, error) {
			return nil, []byte("uninstall: this acts on this account's own registrations\n"), 1, nil
		},
		ask: func(string, string, string) (bool, bool) { t.Fatal("asked after a refusal"); return false, false },
	}
	err := runUninstallFlow(ui, testBundle)
	if err == nil || !strings.Contains(err.Error(), "own registrations") {
		t.Fatalf("a refused plan: %v", err)
	}
	ui.aii = func(...string) ([]byte, []byte, int, error) { return nil, nil, 0, errors.New("no such file") }
	if err := runUninstallFlow(ui, testBundle); err == nil {
		t.Fatal("an aii that does not run was not an error")
	}
}
