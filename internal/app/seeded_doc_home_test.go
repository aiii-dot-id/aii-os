package app

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// seeded_doc_home_test.go — where SKILLS.md and METHOD.md live. The tools
// organ and SKILLS.md tell an identity the docs are "at your root", and a
// relative read resolves in the identity's home. The seed tests knew only
// the layout with the config beside the binary, so when the config moved
// into config/ the docs followed it there and nothing failed.

// seedHome makes an identity home with its config file in the new layout
// and returns an App that seeds into it.
func seedHome(t *testing.T) (a *App, home, configDir string) {
	t.Helper()
	home = t.TempDir()
	configDir = filepath.Join(home, ConfigDirName)
	if err := os.Mkdir(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	return &App{cfg: &Config{SourcePath: filepath.Join(configDir, "config.json")}}, home, configDir
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func absent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("%s must not exist (err=%v)", path, err)
	}
}

func TestTheSeededDocsAreInTheIdentitysHomeInBothLayouts(t *testing.T) {
	for name, configRel := range map[string]string{
		"config in config/":        filepath.Join(ConfigDirName, "config.json"),
		"config beside the binary": "config.json",
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			cfgPath := filepath.Join(home, configRel)
			if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
				t.Fatal(err)
			}
			a := &App{cfg: &Config{SourcePath: cfgPath}}
			a.seedSkillsDoc()
			a.seedMethodDoc()
			for _, doc := range []string{skillsFileName, methodFileName} {
				if len(mustRead(t, filepath.Join(home, doc))) == 0 {
					t.Errorf("%s is empty in the identity's home", doc)
				}
				absent(t, filepath.Join(home, ConfigDirName, doc))
			}
		})
	}
}

// An identity that booted under the old rule holds the platform's own copy
// in config/. It is a duplicate of what is seeded at home, and it goes.
func TestThePlatformsCopyLeftInTheConfigDirectoryIsRemoved(t *testing.T) {
	a, home, configDir := seedHome(t)
	olderBuild := skillsTemplate("0e1a2b3c4d5e")
	for doc, body := range map[string][]byte{skillsFileName: olderBuild, methodFileName: methodMD} {
		if err := os.WriteFile(filepath.Join(configDir, doc), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	a.seedSkillsDoc()
	a.seedMethodDoc()
	absent(t, filepath.Join(configDir, skillsFileName))
	absent(t, filepath.Join(configDir, methodFileName))
	if !bytes.Equal(mustRead(t, filepath.Join(home, skillsFileName)), skillsTemplate(BuildIdentity())) {
		t.Fatal("SKILLS.md at home is not this build's")
	}
	if !bytes.Equal(mustRead(t, filepath.Join(home, methodFileName)), methodMD) {
		t.Fatal("METHOD.md at home is not the current Method")
	}
}

// A copy the identity wrote in is theirs wherever the old rule put it: it
// moves home whole, the stale offer beside it goes, and the current
// platform version is offered beside it at home.
func TestTheIdentitysOwnCopyMovesHomeFromTheConfigDirectory(t *testing.T) {
	a, home, configDir := seedHome(t)
	annotated := append(append([]byte(nil), methodMD...), "\n\n## My margin note\n"...)
	if err := os.WriteFile(filepath.Join(configDir, methodFileName), annotated, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, methodFileName+sidecarSuffix), []byte("an older offer"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.seedMethodDoc()
	if !bytes.Equal(mustRead(t, filepath.Join(home, methodFileName)), annotated) {
		t.Fatal("the identity's own Method did not arrive home byte for byte")
	}
	if !bytes.Equal(mustRead(t, filepath.Join(home, methodFileName+sidecarSuffix)), methodMD) {
		t.Fatal("the current Method is not offered beside the identity's own at home")
	}
	absent(t, filepath.Join(configDir, methodFileName))
	absent(t, filepath.Join(configDir, methodFileName+sidecarSuffix))
}

// Two texts of the identity's own, one in each place: which is current is
// theirs to say. Neither is moved, overwritten or removed.
func TestTwoOfTheIdentitysOwnCopiesAreBothLeftAsTheyAre(t *testing.T) {
	a, home, configDir := seedHome(t)
	atHome := append(append([]byte(nil), methodMD...), "\n\nwritten at home\n"...)
	inConfig := append(append([]byte(nil), methodMD...), "\n\nwritten in config\n"...)
	if err := os.WriteFile(filepath.Join(home, methodFileName), atHome, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, methodFileName), inConfig, 0o644); err != nil {
		t.Fatal(err)
	}
	a.seedMethodDoc()
	if !bytes.Equal(mustRead(t, filepath.Join(home, methodFileName)), atHome) ||
		!bytes.Equal(mustRead(t, filepath.Join(configDir, methodFileName)), inConfig) {
		t.Fatal("one of the identity's own two copies was changed")
	}
}
