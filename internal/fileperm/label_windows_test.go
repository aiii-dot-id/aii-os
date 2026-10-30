//go:build windows

package fileperm

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestLabelReadsAndReplacementDoNotRequireWriteOwner(t *testing.T) {
	for _, class := range []Label{LabelNone, LabelKept, LabelReadable, LabelIdentity} {
		t.Run(labelSDDL[class], func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, ".aii.exe.update.json")
			if err := os.WriteFile(path, []byte("existing update state"), 0o600); err != nil {
				t.Fatal(err)
			}
			if class != LabelNone {
				if err := SetLabel(dir, class); err != nil {
					t.Fatal(err)
				}
				if err := SetLabel(path, class); err != nil {
					t.Fatal(err)
				}
			}
			want, err := LabelOf(path)
			if err != nil {
				t.Fatal(err)
			}
			user, err := windows.GetCurrentProcessToken().GetTokenUser()
			if err != nil {
				t.Fatal(err)
			}
			// An ordinary writable predecessor file need not grant the
			// host authority to change its mandatory label or owner.
			sid := user.User.Sid.String()
			sd, err := windows.SecurityDescriptorFromString("D:P(D;;WO;;;" + sid + ")(A;;FA;;;" + sid + ")")
			if err != nil {
				t.Fatal(err)
			}
			dacl, _, err := sd.DACL()
			if err != nil {
				t.Fatal(err)
			}
			if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
				t.Fatal(err)
			}
			if err := SetLabel(path, LabelKept); !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
				t.Fatalf("fixture must still refuse label writes: %v", err)
			}
			if got, err := LabelOf(path); err != nil || got != want {
				t.Errorf("reading a label requires no write authority: %q (%v), want %q", got, err, want)
			}
			if class != LabelNone {
				if stray, err := Census(dir, class); err != nil || len(stray) != 0 {
					t.Errorf("read-only census requires no write authority: %v (%v)", stray, err)
				}
			}
			f, err := CreateTempForReplacement(path)
			if err != nil {
				t.Fatalf("stage replacement of a writable predecessor file: %v", err)
			}
			defer f.Close()
			if got, err := labelOfHandle(windows.Handle(f.Fd())); err != nil || got != want {
				t.Fatalf("staging changed the mandatory policy: %q (%v), want %q", got, err, want)
			}
			if got, err := os.ReadFile(path); err != nil || string(got) != "existing update state" {
				t.Fatalf("label inspection changed the predecessor: %q (%v)", got, err)
			}
		})
	}
}

func TestReplacementTempAcceptsGoPathSpellings(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	for _, spelling := range []string{"native", "mixed", "forward", "relative"} {
		for _, existing := range []bool{false, true} {
			name := spelling + "/missing"
			if existing {
				name = spelling + "/protected"
			}
			t.Run(name, func(t *testing.T) {
				dir := t.TempDir()
				target := filepath.Join(dir, "target")
				if err := SetLabel(dir, LabelIdentity); err != nil {
					t.Fatal(err)
				}
				want := labelInheritedSDDL(LabelIdentity, false)
				if existing {
					if err := os.WriteFile(target, []byte("old secret"), 0o600); err != nil {
						t.Fatal(err)
					}
					if err := SetLabel(target, LabelKept); err != nil {
						t.Fatal(err)
					}
					want = labelSDDL[LabelKept]
				}
				path := target
				switch spelling {
				case "mixed":
					path = dir + "/target"
				case "forward":
					path = filepath.ToSlash(target)
				case "relative":
					var err error
					path, err = filepath.Rel(root, target)
					if err != nil {
						t.Fatal(err)
					}
					path = "./" + filepath.ToSlash(path)
				}
				f, err := CreateTempForReplacement(path)
				if err != nil {
					t.Fatalf("replace %q: %v", path, err)
				}
				defer f.Close()
				if got, err := labelOfHandle(windows.Handle(f.Fd())); err != nil || got != want {
					t.Fatalf("empty staging file has label %q (%v), want %q", got, err, want)
				}
				if info, err := f.Stat(); err != nil || info.Size() != 0 {
					t.Fatalf("staging file is not empty: %v (%v)", info, err)
				}
				if existing {
					if got, err := os.ReadFile(target); err != nil || string(got) != "old secret" {
						t.Fatalf("original changed: %q (%v)", got, err)
					}
				}
			})
		}
	}
}

func TestReplacementTempCarriesTheExistingLabelBeforeAnyWrite(t *testing.T) {
	for _, name := range []string{"kept", "readable", "identity", "inherited-kept", "unlabelled", "missing"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "target")
			if name == "inherited-kept" {
				if err := SetLabel(dir, LabelKept); err != nil {
					t.Fatal(err)
				}
			}
			if name != "missing" {
				if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			for labelName, class := range map[string]Label{"kept": LabelKept, "readable": LabelReadable, "identity": LabelIdentity} {
				if name == labelName {
					if err := SetLabel(target, class); err != nil {
						t.Fatal(err)
					}
				}
			}
			want, err := LabelOf(target)
			if err != nil && name != "missing" {
				t.Fatal(err)
			}
			// The parent differs from a kept target; copying its current
			// inheritance instead of the target's label would expose it.
			if err := SetLabel(dir, LabelIdentity); err != nil {
				t.Fatal(err)
			}
			if name == "missing" || name == "unlabelled" {
				want = labelInheritedSDDL(LabelIdentity, false)
			}
			for range 2 {
				f, err := CreateTempForReplacement(target)
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				if fi, err := f.Stat(); err != nil || fi.Size() != 0 {
					t.Fatalf("test must observe the new handle before any data: %v, %v", fi, err)
				}
				if got, err := LabelOf(f.Name()); err != nil || got != want {
					t.Fatalf("new empty staging file has label %q (%v), want existing label %q", got, err, want)
				}
				if err := f.Close(); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
				if err := os.Rename(f.Name(), target); err != nil {
					t.Fatal(err)
				}
			}
			class := LabelIdentity
			if name == "kept" || name == "inherited-kept" {
				class = LabelKept
			} else if name == "readable" {
				class = LabelReadable
			}
			if err := SetLabel(dir, class); err != nil {
				t.Fatal(err)
			}
			if stray, err := Census(dir, class); err != nil || len(stray) != 0 {
				t.Fatalf("replacement no longer satisfies its label census: %v (%v)", stray, err)
			}
			if name == "kept" || name == "readable" || name == "identity" {
				if ok, err := HasLabel(target, class); err != nil || !ok {
					t.Fatalf("replacement lost its explicit label: %v (%v)", ok, err)
				}
			}
			if name == "inherited-kept" {
				if err := LabelTree(dir, LabelReadable, nil); err != nil {
					t.Fatal(err)
				}
				if got, err := LabelOf(target); err != nil || got != labelInheritedSDDL(LabelReadable, false) {
					t.Fatalf("replacement became a permanent explicit exception: %q (%v)", got, err)
				}
			}
		})
	}
}

func TestReplacementCreationHasTheMandatoryPolicyBeforeFlagRestoration(t *testing.T) {
	for _, class := range []Label{LabelKept, LabelReadable, LabelIdentity} {
		t.Run(labelSDDL[class], func(t *testing.T) {
			dir := t.TempDir()
			if err := SetLabel(dir, LabelIdentity); err != nil {
				t.Fatal(err)
			}
			want := strings.Replace(labelSDDL[class], ";OICI;", ";;", 1)
			f, err := createTempWithLabel(filepath.Join(dir, "target"), want)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if fi, err := f.Stat(); err != nil || fi.Size() != 0 {
				t.Fatalf("test must observe creation before any data: %v (%v)", fi, err)
			}
			if got, err := labelOfHandle(windows.Handle(f.Fd())); err != nil || got != want {
				t.Fatalf("creation exposed a different mandatory policy: %q (%v), want %q", got, err, want)
			}
		})
	}
}

// THE LOW WALK LABELS WHAT LIES BELOW ITS ROOT, AND NO PROGRAM FILE: a
// program under a Low label would start at Low for whoever runs it.
func TestTheLowWalkLabelsTheTreeAndLeavesProgramFiles(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{filepath.Join(root, "a.txt"), filepath.Join(sub, "c.txt"), filepath.Join(root, "tool.exe"), filepath.Join(sub, "Run.COM")} {
		if err := os.WriteFile(n, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := LabelTree(root, LabelIdentity, func(p string, why error) { t.Errorf("skipped %s: %v", p, why) }); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		root:                            labelSDDL[LabelIdentity],
		sub:                             labelInheritedSDDL(LabelIdentity, true),
		filepath.Join(root, "a.txt"):    labelInheritedSDDL(LabelIdentity, false),
		filepath.Join(sub, "c.txt"):     labelInheritedSDDL(LabelIdentity, false),
		filepath.Join(root, "tool.exe"): "",
		filepath.Join(sub, "Run.COM"):   "",
	} {
		if got, err := LabelOf(path); err != nil || got != want {
			t.Errorf("%s carries %q (%v), want %q", path, got, err, want)
		}
	}
	if stray, err := Census(root, LabelIdentity); err != nil || len(stray) != 2 {
		t.Errorf("the census must find the two program files and nothing else: %v (%v)", stray, err)
	}
}

// UNDER THE KEPT LABEL A PROGRAM FILE IS LABELLED like any other: the kept
// label is at Medium, which lowers no process, and a kept place leaves
// nothing readable.
func TestTheKeptWalkLabelsProgramFilesToo(t *testing.T) {
	root := t.TempDir()
	exe := filepath.Join(root, "tool.exe")
	if err := os.WriteFile(exe, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := LabelTree(root, LabelKept, nil); err != nil {
		t.Fatal(err)
	}
	if stray, err := Census(root, LabelKept); err != nil || len(stray) != 0 {
		t.Errorf("a kept place left objects without its label: %v (%v)", stray, err)
	}
}

// A GRANT REMOVED GIVES ITS FOLDER BACK AS IT WAS: the Low label comes off
// the root and what lies below, and a place with a label of its own inside
// it keeps it.
func TestUnlabelTreeTakesTheLowLabelOff(t *testing.T) {
	root := t.TempDir()
	sub, kept := filepath.Join(root, "sub"), filepath.Join(root, "kept")
	for _, d := range []string{sub, kept} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	file := filepath.Join(sub, "a.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetLabel(kept, LabelKept); err != nil {
		t.Fatal(err)
	}
	if err := LabelTree(root, LabelIdentity, nil); err != nil {
		t.Fatal(err)
	}
	if lbl, _ := LabelOf(file); lbl != labelInheritedSDDL(LabelIdentity, false) {
		t.Fatalf("setup: the file does not carry the Low label: %q", lbl)
	}
	if err := UnlabelTree(root, LabelIdentity); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{root, sub, file} {
		if lbl, err := LabelOf(p); err != nil || lbl != "" {
			t.Errorf("%s still carries %q (%v)", p, lbl, err)
		}
	}
	if ok, err := HasLabel(kept, LabelKept); err != nil || !ok {
		t.Errorf("the kept place inside lost its own label (%v)", err)
	}
}
