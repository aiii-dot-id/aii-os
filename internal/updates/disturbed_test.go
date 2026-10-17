package updates

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

// disturbFirst makes the first publication of the file named name beside
// aii end as a publication ends when storage fails under it, and no other:
// what is put back afterwards publishes as it really does.
func disturbFirst(t *testing.T, name string, how func(tmp, path string) (bool, error)) *int {
	t.Helper()
	publish := publishBeside
	t.Cleanup(func() { publishBeside = publish })
	hit := 0
	publishBeside = func(aside bool, tmp, path string) (bool, error) {
		if filepath.Base(path) != name || hit > 0 {
			return publish(aside, tmp, path)
		}
		hit++
		if how != nil {
			return how(tmp, path)
		}
		// Published, and then its directory entry could not be made
		// durable: the new bytes are at the name.
		if _, err := publish(aside, tmp, path); err != nil {
			t.Errorf("rig: the publication itself failed: %v", err)
		}
		return true, errors.New("the directory would not sync")
	}
	return &hit
}

// movedAsideAndLost is the Windows double failure: the file that stood was
// moved aside, the new one could not be put at the name, and the old one
// could not be moved back. Nothing stands at the name.
func movedAsideAndLost(tmp, path string) (bool, error) {
	os.Remove(path)
	os.Remove(tmp)
	return true, errors.New("publish failed AND restore failed")
}

// A FILE AN UPDATE DISTURBED AND COULD NOT FINISH IS PUT BACK TOO. The
// update is abandoned with the earlier aii standing, and every file beside
// it is as it stood, the one that failed included: it is not left as the
// newer release's, nor missing, with the copies that could have restored
// it removed.
func TestAFileAnUpdateDisturbedAndCouldNotFinishIsPutBackToo(t *testing.T) {
	for _, c := range []struct {
		name string
		how  func(tmp, path string) (bool, error)
	}{
		{"published and not durable", nil},
		{"moved aside and nothing at the name", movedAsideAndLost},
	} {
		t.Run(c.name, func(t *testing.T) {
			rig := newHelperRig(t)
			platform := packagefmt.HostPlatform()
			in := rig.install(t, contractArchive(t, platform))
			names := installedBeside(platform)
			for _, name := range names {
				if err := os.WriteFile(filepath.Join(in.dir, name), []byte("previous "+name), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			hit := disturbFirst(t, names[0], c.how)
			if err := in.apply(); err == nil {
				t.Fatal("an update whose first file beside aii could not be finished was reported applied")
			}
			if *hit != 1 {
				t.Fatalf("rig: the publication of %s was reached %d times", names[0], *hit)
			}
			for _, name := range names {
				assertApplyFile(t, filepath.Join(in.dir, name), "previous "+name)
			}
			in.unswapped(t)
			tgt := targetForTest(t, in.exe)
			for _, name := range names {
				if exists(besideBackup(tgt, name)) {
					t.Errorf("the abandoned update left its copy of %s", name)
				}
			}
		})
	}
}

// A ROLLBACK PUTS BACK A FILE THAT IS NOT THERE AND HAS A BACKUP. An
// install that failed after moving a file aside leaves nothing at its
// name; a rollback that read "not the file the update installed" and
// passed it over would restore aii without its launcher.
func TestARollbackPutsBackAFileThatIsNotThereAndHasABackup(t *testing.T) {
	in, names := besideInstall(t, newHelperRig(t))
	if err := os.Remove(filepath.Join(in.dir, names[0])); err != nil {
		t.Fatal(err)
	}
	if failedBoots(in) == "" {
		t.Fatal("the update that never finished a start was not rolled back")
	}
	for _, name := range names {
		assertApplyFile(t, filepath.Join(in.dir, name), "previous "+name)
	}
	assertApplyFile(t, in.exe, "old binary")
}

// A COMPLETION WRITES NOTHING BESIDE A PROGRAM THAT WAS REPLACED WHILE ITS
// RELEASE WAS BEING FETCHED. The image is read before the fetch and the
// install's lock is taken after it; another identity that runs the same
// program can install a newer release in between. The earlier release's
// files are not written beside the newer aii.
func TestACompletionWritesNothingBesideAProgramReplacedDuringItsFetch(t *testing.T) {
	rig := newHelperRig(t)
	archive := contractArchive(t, packagefmt.HostPlatform())
	in, names := byAnOlderUpdater(t, rig)
	tgt := targetForTest(t, in.exe)
	writeBootMarkerAt(in.dir, in.exe)
	if !exists(tgt.incomplete) {
		t.Fatal("rig: the healthy boot left no note of an incomplete install")
	}
	c, fetched := restarted(t, rig, in, archive)
	own := c.ownRelease
	c.ownRelease = func(ctx context.Context) (*githubRelease, error) {
		// While this install's release is being fetched, another
		// identity's update replaces the program.
		if err := os.WriteFile(in.exe, []byte("a newer release's aii"), 0o755); err != nil {
			t.Error(err)
		}
		return own(ctx)
	}
	if err := c.completeInstall(context.Background(), in.exe); err != nil {
		t.Fatalf("the completion: %v", err)
	}
	if len(fetched()) == 0 {
		t.Fatal("rig: nothing was fetched, so the program was not replaced during a fetch")
	}
	for _, name := range names {
		assertApplyFile(t, filepath.Join(in.dir, name), "previous "+name)
	}
	assertApplyFile(t, in.exe, "a newer release's aii")
	if exists(tgt.incomplete) {
		t.Error("the note of an image that is no longer the program was kept")
	}
}

// A COMPLETION THAT DISTURBED A FILE AND COULD NOT FINISH IT PUTS IT BACK,
// as an update does.
func TestACompletionPutsBackAFileItDisturbed(t *testing.T) {
	rig := newHelperRig(t)
	archive := contractArchive(t, packagefmt.HostPlatform())
	in, names := byAnOlderUpdater(t, rig)
	tgt := targetForTest(t, in.exe)
	writeBootMarkerAt(in.dir, in.exe)
	_, beside, err := extractBinary(archive)
	if err != nil || len(beside) == 0 {
		t.Fatalf("rig: the archive names nothing beside aii: %v", err)
	}
	hit := disturbFirst(t, beside[0].name, nil)
	c, _ := restarted(t, rig, in, archive)
	var incomplete *IncompleteInstallError
	if err := c.completeInstall(context.Background(), in.exe); !errors.As(err, &incomplete) {
		t.Fatalf("a completion that could not finish %s was not said to be incomplete: %v", beside[0].name, err)
	}
	if *hit != 1 {
		t.Fatalf("rig: the publication of %s was reached %d times", beside[0].name, *hit)
	}
	for _, name := range names {
		assertApplyFile(t, filepath.Join(in.dir, name), "previous "+name)
	}
	if !exists(tgt.incomplete) {
		t.Error("a completion that was put back forgot it is still owed")
	}
}
