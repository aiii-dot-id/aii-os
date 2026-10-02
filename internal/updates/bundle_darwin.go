//go:build darwin || ios

package updates

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/hostcap"
	"golang.org/x/sys/unix"
)

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

func swapBundles(a, b string) error {
	if err := unix.RenamexNp(a, b, unix.RENAME_SWAP); err != nil {
		return fmt.Errorf("atomic swap %s <-> %s: %w", a, b, err)
	}
	return nil
}

func applyBundleUpdate(tgt target, archiveBytes []byte, owner string) (already bool, retErr error) {

	if cap := hostcap.Can(hostcap.Subprocess); !cap.Available {
		return false, fmt.Errorf("cannot install an application bundle here: %s "+
			"(verifying one requires ditto, codesign and spctl)", cap.Reason)
	}

	parent := filepath.Dir(tgt.path)

	stageDir, err := os.MkdirTemp(parent, ".aii-update-")
	if err != nil {
		return false, fmt.Errorf("stage beside %s: %w", tgt.path, err)
	}

	keepStage := false
	defer func() {

		if !keepStage {
			_ = os.RemoveAll(stageDir)
		}
	}()

	archivePath := filepath.Join(stageDir, "payload.zip")
	if err := os.WriteFile(archivePath, archiveBytes, 0o600); err != nil {
		return false, fmt.Errorf("write payload: %w", err)
	}
	staged, err := extractBundleArchive(archivePath, stageDir)
	if err != nil {
		return false, err
	}
	if err := verifyStagedBundle(staged, tgt.path); err != nil {
		return false, err
	}
	already, keepStage, err = installBundle(tgt, staged, owner)
	return already, err
}

func installBundle(tgt target, staged, owner string) (already, keepStage bool, err error) {
	want, err := imageHash(staged)
	if err != nil {
		return false, false, fmt.Errorf("read the staged bundle to compare: %w", err)
	}
	already, err = tgt.replace(want, func(have string) error {

		if err := writeRecord(tgt.record, updatePending{BackupSHA256: have, NewSHA256: want, Owner: owner}); err != nil {
			return errors.Join(fmt.Errorf("write update record: %w", err), withdraw(tgt.record))
		}
		replaceStep("recorded")

		if err := os.RemoveAll(tgt.backup); err != nil {
			return errors.Join(fmt.Errorf("clear previous bundle backup: %w", err), withdraw(tgt.record))
		}
		keep, err := installSwapped(tgt.path, staged, tgt.backup)
		keepStage = keep
		if err != nil && !keep {

			return errors.Join(err, withdraw(tgt.record))
		}

		return err
	})
	return already, keepStage, err
}

var renameOutgoing = os.Rename

func installSwapped(bundlePath, staged, prev string) (bool, error) {

	if err := swapBundles(bundlePath, staged); err != nil {
		return false, err
	}

	if err := renameOutgoing(staged, prev); err != nil {

		if undo := swapBundles(bundlePath, staged); undo == nil {
			return false, fmt.Errorf("update NOT applied: the previous bundle could not be kept for rollback (%w) — the swap was undone and the installed bundle is unchanged", err)
		}

		return true, fmt.Errorf("update installed but UNSAFE: the previous bundle could not be moved to %s (%w) and the swap could not be undone — the only copy of the previous bundle is %s; move it there to restore rollback", prev, err, staged)
	}
	return false, nil
}

func applyIfBundle(tgt target, archiveBytes []byte, owner string) (handled, already bool, err error) {
	if !tgt.bundle {
		return false, false, nil
	}
	already, err = applyBundleUpdate(tgt, archiveBytes, owner)
	return true, already, err
}
