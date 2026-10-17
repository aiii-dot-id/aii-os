//go:build darwin && !ios

package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeLaunchd puts a launchctl first on PATH that MODELS launchd's gui
// domain rather than replaying one answer: a label is held from its
// bootstrap to its bootout, print answers 0 for a held label and 113 for
// one launchd does not hold, bootout of a label not held answers 3 (each
// status as the real one answered on macOS 27.0). It answers the
// directory holding a file per held label, and the calls in "calls".
func fakeLaunchd(t *testing.T) string {
	t.Helper()
	state := t.TempDir()
	bin := t.TempDir()
	script := `#!/bin/sh
echo "launchctl $*" >> "` + state + `/calls"
case "$1" in
bootstrap) label=$(basename "$3" .plist); : > "` + state + `/$label"; exit 0 ;;
bootout) label=${2##*/}
  if [ -e "` + state + `/$label" ]; then rm "` + state + `/$label"; exit 0; fi
  echo "Boot-out failed: 3: No such process"; exit 3 ;;
print) label=${2##*/}
  [ -e "` + state + `/$label" ] && exit 0
  echo "Could not find service \"$label\" in domain for user gui: 501"; exit 113 ;;
kill) label=${3##*/}
  [ -e "` + state + `/$label" ] && exit 0
  echo "Could not find service \"$label\" in domain for user gui: 501"; exit 113 ;;
esac
exit 0
`
	if err := os.WriteFile(filepath.Join(bin, "launchctl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return state
}

// calls are what the fake launchctl was asked, in order.
func calls(t *testing.T, state string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(state, "calls"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}
