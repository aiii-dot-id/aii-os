//go:build darwin

package updates

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/hostcap"
	"golang.org/x/sys/unix"
)

// .
// .
// .
// .
// .
const previousBundleName = ".aii-os-previous.app"

// .
// .
// .
// .
// .
// .
// .
// .
func extractBundleArchive(archivePath, destDir string) (string, error) {
	cmd := exec.Command("/usr/bin/ditto", "-x", "-k", archivePath, destDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("ditto extract: %w: %s", err, strings.TrimSpace(string(out)))
	}
	entries, err := os.ReadDir(destDir)
	if err != nil {
		return "", fmt.Errorf("read staged directory: %w", err)
	}
	var found string
	for _, e := range entries {
		if e.IsDir() && strings.HasSuffix(e.Name(), bundleSuffix) {
			if found != "" {
				return "", fmt.Errorf("archive carries more than one .app; a release payload must contain exactly one")
			}
			found = filepath.Join(destDir, e.Name())
		}
	}
	if found == "" {
		return "", fmt.Errorf("archive contains no .app bundle")
	}
	return found, nil
}

// .
// .
// .
func codesignIdentity(bundlePath string) (team, authority string, err error) {
	cmd := exec.Command("/usr/bin/codesign", "-dv", "--verbose=4", bundlePath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("read signature of %s: %w: %s", bundlePath, err, strings.TrimSpace(string(out)))
	}
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "TeamIdentifier="):
			team = strings.TrimPrefix(line, "TeamIdentifier=")
		case strings.HasPrefix(line, "Authority=") && authority == "":
			authority = strings.TrimPrefix(line, "Authority=")
		}
	}
	if team == "" || team == "not set" {
		return "", "", fmt.Errorf("%s carries no Team Identifier — it is not Developer ID signed", bundlePath)
	}
	if authority == "" {
		return "", "", fmt.Errorf("%s carries no signing authority", bundlePath)
	}
	return team, authority, nil
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func verifyStagedBundle(staged, installed string) error {
	cmd := exec.Command("/usr/bin/codesign", "--verify", "--deep", "--strict", "--verbose=2", staged)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("staged bundle fails its own signature: %w: %s", err, strings.TrimSpace(string(out)))
	}

	newTeam, newAuth, err := codesignIdentity(staged)
	if err != nil {
		return err
	}
	oldTeam, oldAuth, err := codesignIdentity(installed)
	if err != nil {
		// .
		// .
		// .
		return fmt.Errorf("cannot read the installed bundle's identity to compare against: %w", err)
	}
	if newTeam != oldTeam || newAuth != oldAuth {
		return fmt.Errorf(
			"staged bundle is signed by a DIFFERENT identity than the installed one — refusing: "+
				"installed team %q authority %q, staged team %q authority %q",
			oldTeam, oldAuth, newTeam, newAuth)
	}

	cmd = exec.Command("/usr/sbin/spctl", "--assess", "--type", "exec", "--verbose=2", staged)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("Gatekeeper refuses the staged bundle: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// .
// .
// .
// .
// .
// .
// .
// .
func swapBundles(a, b string) error {
	if err := unix.RenamexNp(a, b, unix.RENAME_SWAP); err != nil {
		return fmt.Errorf("atomic swap %s <-> %s: %w", a, b, err)
	}
	return nil
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func applyBundleUpdate(bundlePath string, archiveBytes []byte) (retErr error) {
	// .
	// .
	// .
	// .
	// .
	// .
	if cap := hostcap.Can(hostcap.Subprocess); !cap.Available {
		return fmt.Errorf("cannot install an application bundle here: %s "+
			"(verifying one requires ditto, codesign and spctl)", cap.Reason)
	}

	parent := filepath.Dir(bundlePath)

	// .
	// .
	stageDir, err := os.MkdirTemp(parent, ".aii-update-")
	if err != nil {
		return fmt.Errorf("stage beside %s: %w", bundlePath, err)
	}
	// .
	// .
	// .
	// .
	// .
	keepStage := false
	defer func() {
		// .
		// .
		if !keepStage {
			_ = os.RemoveAll(stageDir)
		}
	}()

	archivePath := filepath.Join(stageDir, "payload.zip")
	if err := os.WriteFile(archivePath, archiveBytes, 0o600); err != nil {
		return fmt.Errorf("write payload: %w", err)
	}
	staged, err := extractBundleArchive(archivePath, stageDir)
	if err != nil {
		return err
	}
	if err := verifyStagedBundle(staged, bundlePath); err != nil {
		return err
	}

	prev := filepath.Join(parent, previousBundleName)
	if err := os.RemoveAll(prev); err != nil {
		return fmt.Errorf("clear previous bundle backup: %w", err)
	}

	keep, err := installSwapped(bundlePath, staged, prev)
	keepStage = keep
	return err
}

// .
// .
// .
var renameOutgoing = os.Rename

// .
// .
// .
// .
// .
// .
func installSwapped(bundlePath, staged, prev string) (bool, error) {
	// .
	if err := swapBundles(bundlePath, staged); err != nil {
		return false, err
	}
	// .
	// .
	if err := renameOutgoing(staged, prev); err != nil {
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		if undo := swapBundles(bundlePath, staged); undo == nil {
			return false, fmt.Errorf("update NOT applied: the previous bundle could not be kept for rollback (%w) — the swap was undone and the installed bundle is unchanged", err)
		}
		// .
		// .
		return true, fmt.Errorf("update installed but UNSAFE: the previous bundle could not be moved to %s (%w) and the swap could not be undone — the only copy of the previous bundle is %s; move it there to restore rollback", prev, err, staged)
	}
	return false, nil
}

// .
// .
// .
func applyIfBundle(exePath string, archiveBytes []byte) (bool, error) {
	bundlePath, ok := bundleRoot(exePath)
	if !ok {
		return false, nil
	}
	return true, applyBundleUpdate(bundlePath, archiveBytes)
}
