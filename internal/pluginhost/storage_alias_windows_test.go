//go:build windows

package pluginhost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"golang.org/x/sys/windows"
)

// The explicit fixture is the repository's cross-built fakechild, never an
// installed plugin. This proves execution through the real AppContainer from
// the longer, renamed runtime path, not merely cross-compilation.
func TestPluginStorageWindowsNativeChildUsesMovedRuntime(t *testing.T) {
	fixture := os.Getenv("AII_STORAGE_NATIVE_FIXTURE")
	if fixture == "" {
		t.Skip("native qualification supplies the compiled fakechild fixture")
	}
	raw, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	const id = "id.example.native-migration"
	scratch, err := os.MkdirTemp("", "f027-runtime-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(scratch); err != nil {
			t.Error(err)
		}
	})
	// Exercise exact lengths, not the random suffix of t.TempDir. The 259
	// character working directory exposed CreateProcess's trailing separator.
	for _, length := range []int{248, 258, 259, 260, 261, 300} {
		t.Run(fmt.Sprint(length), func(t *testing.T) {
			rootKey := strings.Repeat("a", 64)
			padding := length - len(filepath.Join(pluginStorageDir(scratch, id), rootKey)) - 1
			if padding < 1 {
				t.Fatalf("fixture temp parent too long for boundary %d: %s", length, scratch)
			}
			base := filepath.Join(scratch, strings.Repeat("p", padding))
			legacy := filepath.Join(base, sanitizeToken(id))
			oldRoot := filepath.Join(legacy, rootKey)
			if err := os.MkdirAll(oldRoot, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(oldRoot, "child.exe"), raw, 0o700); err != nil {
				t.Fatal(err)
			}
			current, err := upgradePluginDir(t.Context(), base, id, func(context.Context) ([]string, error) { return []string{id}, nil })
			if err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(current, rootKey)
			if len(root) != length {
				t.Fatalf("wrong path length %d, wanted %d", len(root), length)
			}
			sum := sha256.Sum256(raw)
			digest := "sha256:" + hex.EncodeToString(sum[:])
			variant := packagefmt.Variant{Entrypoint: "variants/v/child.exe"}
			res := &packagefmt.Result{Manifest: &packagefmt.Manifest{ID: id, Version: "1.0.0"}, FileDigests: map[string]string{variant.Entrypoint: digest}}
			entry := entrypointSpec{Name: "child.exe", Bytes: raw, Digest: digest}
			rt := &runtimeBinding{root: root, entry: entry, check: func() error {
				b, err := os.ReadFile(filepath.Join(root, entry.Name))
				if err != nil {
					return err
				}
				if sha256.Sum256(b) != sum {
					return fmt.Errorf("fixture changed")
				}
				return nil
			}}
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			sup, _, contained, err := startSupervisedNativeWith(ctx, res, &variant, raw, nil, &Options{}, nil, startupAllowance(&Options{}, res.Manifest.ID, nil), "", rt)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := sup.Close(); err != nil {
					t.Error(err)
				}
			}()
			if !contained {
				t.Fatal("renamed runtime executed without its containment")
			}
			response, err := sup.Invoke(ctx, []byte(`{"jsonrpc":"2.0","id":"probe","method":"invoke.call","params":{}}`))
			if err != nil || !strings.Contains(string(response), `"answered":true`) {
				t.Fatalf("native child did not answer from renamed runtime: %v %s", err, response)
			}
		})
	}
}

// Drop privileges in a disposable child, not the host, and prove the junction
// operation itself. The parent process and machine policy are unchanged.
func TestPluginStorageJunctionNeedsNoEnabledPrivileges(t *testing.T) {
	if os.Getenv(storageJunctionChild) != "1" {
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		// The child does one small act and prepares nothing (TestMain sees
		// the mark and builds no helper), so no clock of this test's own
		// bounds it: a child that hung would be ended with the test, by the
		// package's limit, and that would be a fault to see, not to time.
		cmd := exec.CommandContext(t.Context(), exe, "-test.run=^TestPluginStorageJunctionNeedsNoEnabledPrivileges$", "-test.count=1", "-test.v")
		cmd.Env = append(os.Environ(), storageJunctionChild+"=1")
		out, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(out), "--- PASS: TestPluginStorageJunctionNeedsNoEnabledPrivileges (") {
			t.Fatalf("unprivileged child did not run the test and pass it: %v\n%s", err, out)
		}
		return
	}
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY, &token); err != nil {
		t.Fatal(err)
	}
	defer token.Close()
	if err := windows.AdjustTokenPrivileges(token, true, nil, 0, nil, nil); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	target := filepath.Join(root, "target")
	alias := filepath.Join(root, "alias")
	storageFixture(t, target, "readable without a privilege")
	if err := atomicfile.LinkDir(target, alias); err != nil {
		t.Fatal(err)
	}
	assertStorage(t, alias, "readable without a privilege")
}
