package app

import (
	"os"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
)

// A PLUGINS DIRECTORY THAT CANNOT BE LISTED IS SAID, NOT TAKEN FOR EMPTY.
// The scan listed with filepath.Glob, which reports no filesystem error:
// a directory that could not be opened was "nothing installed", the
// facility withdrew every plugin, and the same pass removed their runtime
// material and released this identity's references in the store. The
// guard for that case ("nil when the directory cannot be listed") could
// never be reached.
func TestAPluginsDirectoryThatCannotBeListedIsAnErrorNotAnAbsence(t *testing.T) {
	t.Chdir(t.TempDir())
	a := &App{}

	// No plugins directory at all is no plugins, and no error.
	if d := a.discoverPlugins(); d.Err != nil || len(d.Found) != 0 {
		t.Fatalf("a home with no plugins directory: %+v", d)
	}
	if got := installedPluginIDs(pluginfacility.Snapshot{}); got == nil {
		t.Fatal("a home with no plugins directory was read as a listing that failed")
	}

	// Something that cannot be listed stands where the directory should.
	if err := os.WriteFile("plugins", []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if d := a.discoverPlugins(); d.Err == nil {
		t.Fatalf("A LISTING THAT FAILED WAS AN EMPTY DISCOVERY: every plugin is withdrawn on it: %+v", d)
	}
	if got := installedPluginIDs(pluginfacility.Snapshot{}); got != nil {
		t.Fatalf("a listing that failed answered an installed set (%v): runtime material is retired and references released on it", got)
	}
}
