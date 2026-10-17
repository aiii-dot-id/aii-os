//go:build linux && !android

package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeSystemd puts a systemctl first on PATH that MODELS the account's user
// manager: enable links a unit into its target's .wants directory under
// the user unit directory and disable unlinks it, --now starts or stops
// it, is-active answers "active" (0) or "inactive" (3), and enable or
// disable of a unit whose file does not exist refuses as systemd does
// ("Unit file X does not exist.", status 1) — a slot's instance has its
// file while the package's template is installed (template). It answers
// the directory holding a file per active unit, and the calls in "calls".
func fakeSystemd(t *testing.T, unitDir string, template bool) string {
	t.Helper()
	state := t.TempDir()
	bin := t.TempDir()
	tmpl := ""
	if template {
		tmpl = "1"
	}
	script := `#!/bin/sh
S="` + state + `"; U="` + unitDir + `"; T="` + tmpl + `"
echo "systemctl $*" >> "$S/calls"
action=; now=; units=
for a in "$@"; do
  case "$a" in
    --user|--quiet) ;;
    --now) now=1 ;;
    enable|disable|stop|start|is-active|daemon-reload|try-restart)
      if [ -z "$action" ]; then action=$a; else units="$units $a"; fi ;;
    *) units="$units $a" ;;
  esac
done
hasfile() { case "$1" in aii-os@*) [ -n "$T" ] ;; *) [ -e "$U/$1" ] ;; esac; }
wants() { case "$1" in *.socket) echo "$U/sockets.target.wants" ;; *) echo "$U/default.target.wants" ;; esac; }
case "$action" in
enable|disable)
  for u in $units; do hasfile "$u" || { echo "Failed to $action unit: Unit file $u does not exist."; exit 1; }; done
  for u in $units; do
    w=$(wants "$u")
    if [ "$action" = enable ]; then mkdir -p "$w"; ln -sf "/usr/lib/systemd/user/$u" "$w/$u"; [ -n "$now" ] && : > "$S/$u"
    else rm -f "$w/$u"; [ -n "$now" ] && rm -f "$S/$u"; fi
  done ;;
start) for u in $units; do : > "$S/$u"; done ;;
stop) for u in $units; do rm -f "$S/$u"; done ;;
is-active) u=$(echo $units); if [ -e "$S/$u" ]; then echo active; exit 0; fi; echo inactive; exit 3 ;;
esac
exit 0
`
	if err := os.WriteFile(filepath.Join(bin, "systemctl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "loginctl"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return state
}

// active says the fake manager runs unit.
func active(state, unit string) bool {
	_, err := os.Stat(filepath.Join(state, unit))
	return err == nil
}

// managerCalls are what the fake systemctl was asked, in order.
func managerCalls(t *testing.T, state string) []string {
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
