//go:build darwin

package pluginhost

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
)

func TestAContainedNativeChildCannotReadAKeptPlaceNamedWithACharacterGoEscapes(t *testing.T) {
	requireWall(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	open := filepath.Join(root, "open")
	if err := os.MkdirAll(open, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(open, "notes.txt"), []byte("OPEN NOTES\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []struct{ what, dir string }{
		{"U+200B (Go writes \\u200b)", "kept-zw\u200bx"},
		{"BEL (Go writes \\a)", "kept-bell\ax"},
		{"U+F0000 (Go writes \\U000f0000)", "kept-plane15\U000f0000x"},
	} {
		t.Run(name.what, func(t *testing.T) {
			kept := filepath.Join(root, name.dir)
			if err := os.MkdirAll(kept, 0o755); err != nil {
				t.Skipf("this volume will not hold the name: %v", err)
			}
			secret := filepath.Join(kept, "secret.txt")
			if err := os.WriteFile(secret, []byte("KEPT SECRET\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command("/bin/cat", secret).CombinedOutput(); err != nil || !strings.Contains(string(out), "KEPT SECRET") {
				t.Fatalf("uncontained, the probe must read the file it is refused below: %v\n%s", err, out)
			}
			files := nativeFiles{places: []*firewall.Rule{nativePlace(kept)}, own: standInTools}
			run := func(path string) (string, error) {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				argv, _, err := containArgv(ctx, []string{"/bin/cat", path}, nil, files)
				if err != nil {
					t.Fatalf("containment refused on a host that can build it: %v", err)
				}
				out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()
				return string(out), err
			}
			if out, err := run(filepath.Join(open, "notes.txt")); err != nil || !strings.Contains(out, "OPEN NOTES") {
				t.Fatalf("inside the wall a place nobody keeps must stay readable: %v\n%s", err, out)
			}
			out, err := run(secret)
			if strings.Contains(out, "KEPT SECRET") {
				t.Fatalf("a contained child read a kept place named with %s:\n%s", name.what, out)
			}
			if err == nil {
				t.Fatalf("the contained read of a kept place exited 0:\n%s", out)
			}
			t.Logf("%s, inside the wall: %v: %s", name.what, err, strings.TrimSpace(out))
		})
	}
}

// AN ORDINARY PATH IS WRITTEN IN THE SAME BYTES AS BEFORE. The profile for a
// fixture that draws every kind of line the wall writes (a compute service
// and driver, a masked directory with a read exception inside it, the
// metadata lines for its ancestors, a masked file, the image and the own
// directories a child may start) is held to the bytes strconv.Quote gave it,
// which is what the wall wrote before Seatbelt's string spelling had a writer
// of its own. A space and accented letters are ordinary: Go writes them as
// they are.
func TestDarwinProfileSpellsAnOrdinaryPathAsBefore(t *testing.T) {
	if _, err := exec.LookPath("sandbox-exec"); err != nil {
		t.Skip("sandbox-exec is absent: containment refuses instead, which TestContainmentRefusesRatherThanRunningBare covers")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(root, "data")
	models := filepath.Join(data, "models")
	selected := filepath.Join(models, "sélection modèle")
	key := filepath.Join(data, "identity.key")
	if err := os.MkdirAll(selected, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(key, []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	files := nativeFiles{places: []*firewall.Rule{nativePlace(data), nativePlace(key)}, read: []string{selected}, own: standInTools}
	argv, _, err := containArgv(context.Background(), []string{"/bin/sh", "-c", "true"}, &AcceleratorProfile{Backend: "metal"}, files)
	if err != nil {
		t.Fatal(err)
	}
	if len(argv) < 3 || argv[1] != "-p" {
		t.Fatalf("argv does not carry a profile: %q", argv)
	}
	q := strconv.Quote
	want := seatbeltProfile +
		"(allow mach-lookup (global-name " + q("com.apple.MTLCompilerService") + "))\n" +
		"(allow iokit-open (iokit-user-client-class " + q("AGXDeviceUserClient") + "))\n" +
		"(deny file-read* (require-all (subpath " + q(data) + ") (require-not (subpath " + q(selected) + "))))\n" +
		"(allow file-read-metadata (literal " + q(models) + "))\n" +
		"(allow file-read-metadata (literal " + q(data) + "))\n" +
		"(deny file-read* (require-all (literal " + q(key) + ")))\n" +
		"(deny process-exec*)\n" +
		"(allow process-exec* (literal " + q("/bin/sh") + "))\n" +
		"(allow process-exec* (subpath " + q("/bin") + "))\n" +
		"(allow process-exec* (subpath " + q("/usr/bin") + "))\n"
	if got := argv[2]; got != want {
		t.Fatalf("the profile for ordinary paths changed bytes\n got: %q\nwant: %q", got, want)
	}
}

// A NAME THE WALL CANNOT WRITE IS NOT WALLED BY GUESS: the child is not
// started. The image is the one name written whether or not it exists, so
// it carries the two the writer refuses.
func TestDarwinWallRefusesANameItCannotWrite(t *testing.T) {
	if _, err := exec.LookPath("sandbox-exec"); err != nil {
		t.Skip("sandbox-exec is absent: containment refuses instead, which TestContainmentRefusesRatherThanRunningBare covers")
	}
	for _, image := range []string{"/bin/not-utf8-\xff", "/bin/nul-\x00"} {
		argv, wall, err := containArgv(context.Background(), []string{image}, nil, nativeFiles{own: standInTools})
		if err == nil || argv != nil || wall.Description != "" {
			t.Fatalf("%q: the wall must refuse rather than start the child: argv %q, %q, err %v", image, argv, wall.Description, err)
		}
		if !strings.Contains(err.Error(), "not run uncontained") {
			t.Fatalf("%q: the refusal must say the child is not run: %v", image, err)
		}
	}
}
