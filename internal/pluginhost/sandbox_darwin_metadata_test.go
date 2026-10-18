//go:build darwin

package pluginhost

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
)

// Model libraries canonicalize paths, requiring ancestor metadata. Keep the
// distinction between metadata and directory listing/content executable.
func TestDarwinSelectedMaterialCanResolveWithoutReadingPrivateAncestors(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	put := func(rel string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("model"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, rel := range []string{"data/models/selected/model", "data/runtime/selected/entry", "data/credentials/key", "data/models/sibling/model", "data/models/selected/secret/key", "data/models/selected/blocked"} {
		put(rel)
	}
	if err := os.Symlink(filepath.Join(root, "data/models/selected"), filepath.Join(root, "data/model-alias")); err != nil {
		t.Fatal(err)
	}
	files := nativeFiles{places: []*firewall.Rule{nativePlace(filepath.Join(root, "data")), nativePlace(filepath.Join(root, "data/models")), nativePlace(filepath.Join(root, "data/models/selected/secret")), nativePlace(filepath.Join(root, "data/models/selected/blocked"))}, read: []string{filepath.Join(root, "data/model-alias"), filepath.Join(root, "data/runtime/selected/entry"), filepath.Join(root, "data/models/selected/blocked")}, own: standInTools}
	script := `set -eu
root=$1
for rel in data/models/selected/model data/model-alias/model data/runtime/selected/entry; do
 /bin/realpath "$root/$rel" || exit 20
 value=$(/bin/cat "$root/$rel") || exit 21
 test "$value" = model || exit 22
done
for rel in data data/models data/runtime data/runtime/selected; do
 /usr/bin/stat -f %HT "$root/$rel" || exit 23
 if /bin/ls "$root/$rel"; then echo "private listing escaped: $rel"; exit 24; fi
done
for rel in data/credentials/key data/models/sibling/model data/models/selected/secret/key data/models/selected/blocked data/model-alias/secret/key; do
 if /bin/cat "$root/$rel"; then echo "private content escaped: $rel"; exit 25; fi
 if /usr/bin/stat "$root/$rel"; then echo "private metadata escaped: $rel"; exit 26; fi
done
if echo changed > "$root/data/models/selected/model"; then echo 'write escaped'; exit 27; fi
`
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	argv, _, err := containArgv(ctx, []string{"/bin/sh", "-c", script, "metadata-probe", root}, nil, files)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("live-shaped wall: %v\n%s", err, out)
	}
}
