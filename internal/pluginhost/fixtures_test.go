package pluginhost

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
)

// The signed packages, runtime trees and activation helpers the native
// tests share, on every row: what activates through the plugin store's
// writer (store_harness_test.go) is the desktop rows' alone, and these are
// also what tests that only verify, select and prepare read.

func platformRootsForTest(t *testing.T) (packagefmt.TrustRoots, *packagetest.Role) {
	t.Helper()
	root, status, err := packagetest.NewPlatformRelease("aiii_test_platform_release")
	if err != nil {
		t.Fatal(err)
	}
	trust := t.TempDir()
	if err := os.WriteFile(filepath.Join(trust, packagetest.StatusFilePlatform), status, 0o600); err != nil {
		t.Fatal(err)
	}
	roots := packagefmt.TrustRoots{PlatformRelease: root.Env}
	roots.Revocation = packagefmt.LoadRevocationStatus(trust, roots, nil)
	return roots, root
}

func hostVariantID() string {
	return packagefmt.HostPlatform() + "-" + packagefmt.HostArch() + "-native"
}

func TestParseRuntimesHoldsTheBounds(t *testing.T) {
	good := `{"runtimes":[{"variant_id":"win","url":"https://x/y.tar.gz","sha256":"` + strings.Repeat("a", 64) + `","size":1,"installed_bytes":2,"files":3,"inventory_sha256":"` + strings.Repeat("b", 64) + `"}]}`
	if _, err := ParseRuntimes([]byte(good)); err != nil {
		t.Fatalf("good: %v", err)
	}
	for name, bad := range map[string]string{
		"http url":      strings.Replace(good, "https://", "http://", 1),
		"short digest":  strings.Replace(good, strings.Repeat("a", 64), "abc", 1),
		"zero size":     strings.Replace(good, `"size":1`, `"size":0`, 1),
		"unknown field": strings.Replace(good, `"files":3`, `"files":3,"exec":true`, 1),
		"empty":         `{"runtimes":[]}`,
	} {
		if _, err := ParseRuntimes([]byte(bad)); err == nil {
			t.Fatalf("%s must be refused", name)
		}
	}
}

func activationStorageCall(t *testing.T, ap *ActivePlugin, params string) (string, json.RawMessage) {
	t.Helper()
	raw, err := ap.binding.Dispatch(t.Context(), "invoke-call", []byte(params))
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Status string          `json:"status"`
		Result json.RawMessage `json:"operation_result"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result.Status, result.Result
}

func writeActivationStorage(t *testing.T, ap *ActivePlugin) string {
	t.Helper()
	for _, params := range []string{
		`{"operation":"kv.put","target":{"key":"working"},"arguments":{"value":"retain this activation"}}`,
		`{"operation":"fs.write","target":{"root":"private","path":"working.txt"},"arguments":{"data":"retain this activation"}}`,
	} {
		if status, result := activationStorageCall(t, ap, params); status != "succeeded" {
			t.Fatalf("fixture write failed: %s %s", status, result)
		}
	}
	status, result := activationStorageCall(t, ap, `{"operation":"memory.remember","arguments":{"text":"retain this activation"}}`)
	var memory struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(result, &memory); err != nil || status != "succeeded" || memory.ID == "" {
		t.Fatalf("fixture memory write: %s %s %v", status, result, err)
	}
	return memory.ID
}

func requireActivationStorage(t *testing.T, ap *ActivePlugin, id string) {
	t.Helper()
	for _, params := range []string{
		`{"operation":"kv.get","target":{"key":"working"}}`,
		`{"operation":"fs.read","target":{"root":"private","path":"working.txt"}}`,
		`{"operation":"memory.recall","arguments":{"id":"` + id + `"}}`,
	} {
		status, result := activationStorageCall(t, ap, params)
		text := string(result)
		if strings.Contains(params, `"fs.read"`) && status == "succeeded" {
			var file struct {
				Data string `json:"data_b64"`
			}
			if err := json.Unmarshal(result, &file); err != nil {
				t.Fatal(err)
			}
			raw, err := base64.StdEncoding.DecodeString(file.Data)
			if err != nil {
				t.Fatal(err)
			}
			text = string(raw)
		}
		if status != "succeeded" || !strings.Contains(text, "retain this activation") {
			t.Errorf("another activation removed serving state: %s -> %s %s", params, status, result)
		}
	}
}

// sharedPlatform is one platform-release role and its trust roots for
// every native package this file signs: minting a role is seconds of
// signature work under the race detector, and these tests need nothing
// of a fresh one.
var sharedPlatform struct {
	once  sync.Once
	roots packagefmt.TrustRoots
	role  *packagetest.Role
	err   error
}

func sharedPlatformRoots(t *testing.T) (packagefmt.TrustRoots, *packagetest.Role) {
	t.Helper()
	sharedPlatform.once.Do(func() {
		root, status, err := packagetest.NewPlatformRelease("aiii_test_platform_release")
		if err != nil {
			sharedPlatform.err = err
			return
		}
		dir, err := os.MkdirTemp("", "aii-start-cleanup-trust-")
		if err != nil {
			sharedPlatform.err = err
			return
		}
		defer os.RemoveAll(dir)
		if err := os.WriteFile(filepath.Join(dir, packagetest.StatusFilePlatform), status, 0o600); err != nil {
			sharedPlatform.err = err
			return
		}
		roots := packagefmt.TrustRoots{PlatformRelease: root.Env}
		roots.Revocation = packagefmt.LoadRevocationStatus(dir, roots, nil)
		sharedPlatform.roots, sharedPlatform.role = roots, root
	})
	if sharedPlatform.err != nil {
		t.Fatal(sharedPlatform.err)
	}
	return sharedPlatform.roots, sharedPlatform.role
}
