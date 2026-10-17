//go:build windows

package install

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

// writeRefusing writes a tool that records who reached it and refuses: a
// .cmd, which a command named without its extension finds on PATH before
// the system's own .exe in a later directory.
func writeRefusing(dir, tool, reached string) error {
	script := "@echo off\r\necho " + tool + " %*>> \"" + reached + "\"\r\necho refused: a test reached the real " + tool + " 1>&2\r\nexit /b 99\r\n"
	return os.WriteFile(filepath.Join(dir, tool+".cmd"), []byte(script), 0o600)
}

// testKeys is the test binary's own subkey of HKCU, which runKey and
// startupApprovedKey are pointed under for the run.
var testKeys = fmt.Sprintf(`Software\AII OS install test %d`, os.Getpid())

// guardRegistry points the Run and StartupApproved keys at throwaway
// subkeys, and answers the removal of them.
func guardRegistry() (func() error, error) {
	savedRun, savedApproved := runKey, startupApprovedKey
	runKey = testKeys + `\Run`
	startupApprovedKey = testKeys + `\StartupApproved\Run`
	return func() error {
		runKey, startupApprovedKey = savedRun, savedApproved
		return deleteTree(registry.CURRENT_USER, testKeys)
	}, nil
}

// deleteTree removes a key and every subkey under it; absent is removed.
func deleteTree(root registry.Key, path string) error {
	k, err := registry.OpenKey(root, path, registry.ENUMERATE_SUB_KEYS)
	if err == registry.ErrNotExist {
		return nil
	}
	if err != nil {
		return err
	}
	subs, err := k.ReadSubKeyNames(-1)
	k.Close()
	if err != nil {
		return err
	}
	for _, s := range subs {
		if err := deleteTree(root, path+`\`+s); err != nil {
			return err
		}
	}
	if err := registry.DeleteKey(root, path); err != nil && err != registry.ErrNotExist {
		return err
	}
	return nil
}
