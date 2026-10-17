//go:build (linux && !android) || (darwin && !ios) || windows

package pluginhost

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

// A RUNTIME REFUSED FOR ITS DIRECTORIES SAYS WHICH SETTING TO RAISE, AND
// TO WHAT. The declaration says how many files a runtime holds, not how
// many directories, so a files ceiling the declaration fits can still be
// too low for the archive: the reader counts both. That refusal came
// after the download, in words about members that named no setting. It
// names the operator's key and the least value now, it is not held
// against the package, and the archive is kept, so raising the ceiling
// recovers without another download.
func TestARuntimePastTheMemberAllowanceNamesTheOperatorsSetting(t *testing.T) {
	fx := newRuntimeFixture(t) // three files, each in a directory of its own
	tight := packagefmt.TreeLimits{MaxFiles: fx.decl.Files}
	w := openStore(t)
	_, err := storeRoot(t, w, &fx.decl, fx.entry, fx.fetch, tight)
	var refused *RuntimeError
	if !errors.As(err, &refused) {
		t.Fatalf("a files ceiling that fits the declared files and not the archive's members: %v", err)
	}
	if refused.Declared {
		t.Error("the operator's ceiling was held against the package")
	}
	for _, want := range []string{"plugins.runtime.max_files", "to 5 or more", "3 files and 3 directories", "files ceiling of 3"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if storeRoots(t, w) != 0 || storePartials(t, w) != 0 {
		t.Errorf("a refused runtime left %d roots and %d partial trees", storeRoots(t, w), storePartials(t, w))
	}

	// The ceiling raised to what the refusal named: the same archive, no
	// second download.
	fetched := fx.fetches
	root, err := storeRoot(t, w, &fx.decl, fx.entry, fx.fetch, packagefmt.TreeLimits{MaxFiles: 5})
	if err != nil || root == "" {
		t.Fatalf("a files ceiling of 5, the least the refusal named, does not admit the runtime: %v", err)
	}
	if fx.fetches != fetched {
		t.Errorf("raising the ceiling fetched the archive again (%d fetches, then %d)", fetched, fx.fetches)
	}

	// And an installed runtime under a ceiling lowered since says the same,
	// and stays where it is.
	m := runtimeMaterial(t, w, "id.example.voice", "1", fx)
	m.limits = tight
	_, held, err := m.verify(context.Background())
	held.release()
	if !errors.As(err, &refused) || !strings.Contains(err.Error(), "the published runtime does not verify") || !strings.Contains(err.Error(), "set plugins.runtime.max_files to 5 or more") {
		t.Errorf("a published runtime under a lowered ceiling: %v", err)
	}
	if storeRoots(t, w) != 1 {
		t.Errorf("the published runtime was removed by a ceiling: %d roots", storeRoots(t, w))
	}
}
