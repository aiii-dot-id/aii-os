package pluginhost

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
)

func nativePlace(path string, except ...string) *firewall.Rule {
	return &firewall.Rule{Kind: firewall.KindSubstrate, Enforced: true, Path: path, Except: except}
}

func TestNativePathsHideSecretsAndKeepWorkingMaterial(t *testing.T) {
	for _, scope := range []string{"private-directory", "standalone-file"} {
		t.Run(scope, func(t *testing.T) { nativePathsProbe(t, scope == "private-directory") })
	}
}

func nativePathsProbe(t *testing.T, privateConfig bool) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("argv filesystem masks belong to Linux and macOS")
	}
	skipWhereNativeWallCannotBeEstablished(t)
	root := t.TempDir()
	data := filepath.Join(root, "data")
	model := filepath.Join(data, "models", "selected", "weights")
	runtimeFile := filepath.Join(data, "runtime", "selected", "library")
	private := filepath.Join(data, "private", "selected")
	secret := filepath.Join(data, "credentials", "key")
	external := filepath.Join(root, "shared", "provider.json")
	if privateConfig {
		external = filepath.Join(root, "private-config", "provider.json")
	}
	nested := filepath.Join(filepath.Dir(model), "private-credentials", "secret")
	project := filepath.Join(root, "shared", "project")
	layout := filepath.Join(data, "ui-layout.json")
	write := func(path, value string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{secret, external, nested, filepath.Join(data, "private", "sibling", "state")} {
		write(p, "canary-secret")
	}
	for _, p := range []string{model, runtimeFile, project, layout} {
		write(p, "working-material")
	}
	alias := filepath.Join(root, "secret-link")
	if err := os.Symlink(secret, alias); err != nil {
		t.Fatal(err)
	}

	modelAlias := filepath.Join(data, "selected-models")
	if err := os.Symlink(filepath.Dir(model), modelAlias); err != nil {
		t.Fatal(err)
	}
	outsideModel := filepath.Join(root, "outside-models", "weights")
	write(outsideModel, "working-material")
	outsideAlias := filepath.Join(root, "outside-model-link")
	if err := os.Symlink(filepath.Dir(outsideModel), outsideAlias); err != nil {
		t.Fatal(err)
	}
	files := nativeFiles{places: []*firewall.Rule{
		nativePlace(data, layout), nativePlace(external), nativePlace(filepath.Dir(nested)),
		nativePlace(filepath.Join(data, "models")),
	}, read: []string{modelAlias, outsideAlias, filepath.Dir(runtimeFile)}, private: private, own: standInTools}
	if privateConfig {
		files.places = append(files.places, nativePlace(filepath.Dir(external)))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	blockedFile, unprotectedFile := external, ""
	if runtime.GOOS == "linux" && !privateConfig {
		blockedFile, unprotectedFile = "", external
	}
	script := `check() {
for p in "$1/data/credentials/key" "$1/data/private/sibling/state" "$1/secret-link" "$2" "$1/data/models/selected/private-credentials/secret" "$1/data/selected-models/private-credentials/secret"; do
  value=$(cat "$p") || value=
  case "$value" in canary-secret*) echo "LEAK:$p"; exit 21;; esac
done
for p in "$1/data/selected-models/weights" "$1/outside-model-link/weights" "$1/data/runtime/selected/library" "$1/shared/project" "$1/data/ui-layout.json"; do
  value=$(cat "$p") || exit 22
  test "$value" = working-material || exit 23
done
if test -n "$3"; then
  value=$(cat "$3") || exit 28
  case "$value" in canary-secret*) :;; *) exit 29;; esac
fi
}
check "$1" "$2" "$3"
echo ready
read go || exit 24
check "$1" "$2" "$3"
value=$(cat "$1/data/credentials/later") || value=
test "$value" != canary-secret-later || exit 25
test "$(cat "$1/data/private/selected/state")" = private-later || exit 26
if (echo bad > "$1/data/new-file"); then exit 27; fi
echo passed`
	argv, wall, err := containArgv(ctx, []string{"/bin/sh", "-c", script, "probe", root, blockedFile, unprotectedFile}, nil, files)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(wall.Description, nativePathLimit) {
		t.Fatalf("missing scope limit: %s", wall.Description)
	}
	if runtime.GOOS == "linux" && !strings.Contains(wall.Description, "standalone files outside masked directories are NOT protected") {
		t.Fatalf("Linux overstated its file protection: %s", wall.Description)
	}
	cmd := walled(t, exec.CommandContext(ctx, argv[0], argv[1:]...), wall)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = cmd.Wait() })
	scan := bufio.NewScanner(stdout)
	if !scan.Scan() || scan.Text() != "ready" {
		_ = cmd.Wait()
		t.Fatalf("child did not reach file-change step: %q %s", scan.Text(), stderr.String())
	}
	write(filepath.Join(data, "credentials", "later"), "canary-secret-later")
	write(filepath.Join(private, "state"), "private-later")

	write(external+".next", "canary-secret-rotated")
	if err := os.Rename(external+".next", external); err != nil {
		t.Fatal(err)
	}
	if _, err := stdin.Write([]byte("go\n")); err != nil {
		t.Fatal(err)
	}
	_ = stdin.Close()
	if !scan.Scan() || scan.Text() != "passed" {
		_ = cmd.Wait()
		t.Fatalf("child failed after file change: %q %s", scan.Text(), stderr.String())
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("probe: %v: %s", err, stderr.String())
	}
	for p, want := range map[string]string{secret: "canary-secret", external: "canary-secret-rotated", model: "working-material"} {
		got, err := os.ReadFile(p)
		if err != nil || string(got) != want {
			t.Fatalf("host file changed: %s: %q %v", p, got, err)
		}
	}
}

func TestNativePathsDoNotMaskMissingFilesParents(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "not-created")
	paths, err := (nativeFiles{places: []*firewall.Rule{nativePlace(missing)}}).paths()
	if err != nil || len(paths) != 0 {
		t.Fatalf("missing object must not widen to the shared parent: %+v %v", paths, err)
	}
	_, err = (nativeFiles{places: []*firewall.Rule{nativePlace("relative")}}).paths()
	if !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("want classifiable invalid path, got %v", err)
	}
}

func TestNativePathsHandBackNoExceptionThatLeadsOutOfItsPlace(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "data")
	sibling := filepath.Join(root, "sibling")
	archive, sttq := filepath.Join(data, "archive"), filepath.Join(data, "sttq")
	for _, d := range []string{filepath.Join(data, "theme"), filepath.Join(sibling, "data"), archive, sttq} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	for _, target := range []string{"/", sibling, archive, sttq} {
		ui := filepath.Join(data, "ui")
		os.Remove(ui)
		if err := os.Symlink(target, ui); err != nil {
			t.Fatal(err)
		}
		paths, err := (nativeFiles{places: []*firewall.Rule{nativePlace(data, ui, filepath.Join(data, "theme"))}}).paths()
		if err != nil {
			t.Fatal(err)
		}
		realData, _ := filepath.EvalSymlinks(data)
		realTheme, _ := filepath.EvalSymlinks(filepath.Join(data, "theme"))
		masked, theme := false, false
		for _, p := range paths {
			real, _ := filepath.EvalSymlinks(target)
			if p.read && (p.source == real || p.path == real) {
				t.Errorf("data/ui a link to %s: the link's target was handed back to the child: %+v", target, p)
			}
			masked = masked || (!p.read && p.path == realData)
			theme = theme || (p.read && p.path == realTheme)
		}
		if !masked || !theme {
			t.Errorf("data/ui a link to %s: the place masked %v, the exception inside it handed back %v: %+v", target, masked, theme, paths)
		}
	}
}
