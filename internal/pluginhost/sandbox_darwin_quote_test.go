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

// secret, and inside the same wall a sibling nobody keeps, in the directory
// the engine is handed, stays readable.
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
			files := nativeFiles{places: []*firewall.Rule{nativePlace(kept)}, read: []string{root}, own: standInTools}
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
	added := filepath.Join(root, "vendor tree")
	if err := os.MkdirAll(added, 0o755); err != nil {
		t.Fatal(err)
	}
	files := nativeFiles{places: []*firewall.Rule{nativePlace(data), nativePlace(key)}, read: []string{selected}, own: standInTools, view: []string{added}}
	argv, _, err := containArgv(context.Background(), []string{"/bin/sh", "-c", "true"}, &AcceleratorProfile{Backend: "metal"}, files)
	if err != nil {
		t.Fatal(err)
	}
	if len(argv) < 3 || argv[1] != "-p" {
		t.Fatalf("argv does not carry a profile: %q", argv)
	}
	q := strconv.Quote
	// A path under one of the root's links into /private (a temporary
	// directory is) is matched at both of its spellings.
	sp := func(kind, p string) string {
		if alias := rootLinkSpelling(p); alias != "" {
			return "(require-any (" + kind + " " + q(p) + ") (" + kind + " " + q(alias) + "))"
		}
		return "(" + kind + " " + q(p) + ")"
	}
	if real := "/private/var/"; strings.HasPrefix(added, real) && sp("subpath", added) == "(subpath "+q(added)+")" {
		t.Fatalf("a path under %s is not matched at its /var spelling", real)
	}
	// Each line the fixture draws, in the order the profile writes them:
	// the files' deny, the system's view and what of it is excepted, what
	// the operator adds (after the exception, so it may add back), the
	// handed material, what stays refused wherever those show, the compute
	// exceptions, the masks, the names above the material, the starts.
	want := []string{
		seatbeltProfile,
		"(allow file-read* file-test-existence (subpath " + q("/System") + "))\n",
		"(deny file-read* file-test-existence (subpath " + q("/System/Volumes") + "))\n",
		"(deny file-read* file-test-existence (subpath " + q("/usr/local") + "))\n",
		"(allow file-read* file-test-existence " + sp("subpath", added) + ")\n",
		"(allow file-read* file-test-existence " + sp("subpath", selected) + ")\n",
		seatbeltRefused,
		"(allow mach-lookup (global-name " + q("com.apple.MTLCompilerService") + "))\n",
		"(allow iokit-open (iokit-user-client-class " + q("AGXDeviceUserClient") + "))\n",
		"(deny file-read* file-test-existence (require-all " + sp("subpath", data) + " (require-not " + sp("subpath", selected) + ")))\n",
		"(deny file-read* file-test-existence (require-all " + sp("literal", key) + "))\n",
		"(allow file-read-metadata file-test-existence (literal " + q(models) + "))\n",
		"(allow file-read-metadata file-test-existence (literal " + q(data) + "))\n",
		"(deny process-exec*)\n",
		"(allow process-exec* (literal " + q("/bin/sh") + "))\n",
		"(allow process-exec* (subpath " + q("/bin") + "))\n",
		"(allow process-exec* (subpath " + q("/usr/bin") + "))\n",
	}
	got := argv[2]
	at := 0
	for _, line := range want {
		i := strings.Index(got[at:], line)
		if i < 0 {
			t.Fatalf("the profile for ordinary paths does not carry, in its place, the line\n%q\nprofile:\n%s", line, got)
		}
		at += i + len(line)
	}
	// Every file the profile refuses by name it refuses the existence of
	// with the read, the system's credential stores and the terminals as
	// the masks: where a view the operator widened shows existence, a read
	// refused alone tells a refused file apart from a missing one.
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "(deny file-read* ") && !strings.HasPrefix(line, "(deny file-read* file-test-existence ") {
			t.Errorf("a file refused its read and not its existence:\n%s", line)
		}
	}
}

// THE LINE SAYS WHAT THE VIEW WITHHOLDS. The default view shows /System and
// /usr but the volumes under /System/Volumes and /usr/local; an operator who
// adds one back, or a place above it, is told it is shown, and one who adds
// a place inside it is told it is shown only there. /usr is the default's
// already, so naming it again adds nothing back.
func TestDarwinContainmentLineSaysWhatTheViewWithholds(t *testing.T) {
	if _, err := exec.LookPath("sandbox-exec"); err != nil {
		t.Skip("sandbox-exec is absent: containment refuses instead, which TestContainmentRefusesRatherThanRunningBare covers")
	}
	for _, c := range []struct {
		view []string
		says string
	}{
		{nil, "material (not /System/Volumes or /usr/local), "},
		{[]string{"/usr"}, "material (not /System/Volumes or /usr/local), "},
		{[]string{"/System/Volumes"}, "material (not /usr/local), "},
		{[]string{filepath.Join("/System/Volumes", "Data")}, "material (not /usr/local; of /System/Volumes only what is named), "},
		{[]string{"/"}, "material, "},
	} {
		_, wall, err := containArgv(context.Background(), []string{"/bin/sh", "-c", "true"}, nil, nativeFiles{own: standInTools, view: c.view})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(wall.Description, c.says) {
			t.Errorf("view %q: the line does not say %q:\n%s", c.view, c.says, wall.Description)
		}
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
