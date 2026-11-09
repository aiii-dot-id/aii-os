//go:build windows

package supervisor

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"golang.org/x/sys/windows"
)

// startWalledForCleanup starts the fake child under the wall with a
// counting image, and answers the container's SID while it runs. The
// test's cleanup deletes this child's profile by its SID if anything
// left it, so no run leaves a profile on the machine.
func startWalledForCleanup(t *testing.T, id string) (*Supervisor, *containmentImage, string) {
	t.Helper()
	image := &containmentImage{}
	s, err := Start(Spec{
		PluginID: id, Argv: []string{fakechildBin, "ready-fields"},
		ReadyMark: "child-ready", ReadyTimeout: 30 * time.Second,
		Backoff:      Backoff{Initial: 20 * time.Millisecond, Max: 100 * time.Millisecond, MaxRestarts: 0},
		AppContainer: &AppContainer{Profile: "aiios.test.cleanup", GrantRead: []string{filepath.Dir(fakechildBin)}},
		Artifact:     image,
	}, nil)
	if err != nil {
		t.Fatalf("start under the wall: %v", err)
	}
	sid := s.Containment().AppContainerSID
	if !strings.HasPrefix(sid, "S-1-15-2-") {
		t.Fatalf("the child runs under no AppContainer: %q", sid)
	}
	t.Cleanup(func() {
		if moniker, err := profileMoniker(sid); err == nil {
			_ = deleteChildProfile(moniker)
		}
	})
	return s, image, sid
}

// A PROFILE WINDOWS WILL NOT DELETE, AFTER THE JOB IS EMPTY AND THE GRANTS
// ARE REVOKED, DOES NOT HOLD THE RETIREMENT (the Windows voice
// qualification of 111c8ba0, 2026-11-07: HRESULT 0x80070020 there kept the
// plugin's store pins and its image locked until the host exited). The
// close is clean, the image binding is released, and the operator is told
// which profile is left.
func TestARefusedProfileDeleteAfterRetirementReleasesTheImage(t *testing.T) {
	capture := logsink.CaptureForTest(t)
	refused := ""
	old := deleteProfile
	deleteProfile = func(name string) error {
		refused = name
		_ = deleteChildProfile(name) // the machine keeps no profile of this test's
		return fmt.Errorf("delete AppContainer %s: %w", name, hresult(0x80070020))
	}
	t.Cleanup(func() { deleteProfile = old })
	s, image, _ := startWalledForCleanup(t, "com.example.profile-refused")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := s.CloseContext(ctx); err != nil {
		t.Fatalf("a refused profile delete after an empty job and revoked grants held the retirement: %v", err)
	}
	if refused == "" {
		t.Fatal("the retirement never asked Windows to delete the profile: the seam did not run")
	}
	if image.closed.Load() != 1 {
		t.Fatalf("the image binding was closed %d times, not once", image.closed.Load())
	}
	out := capture.String()
	want := "plugin com.example.profile-refused: its Windows container profile " + refused + " could not be removed (HRESULT 0x80070020); it is empty and unused, and uninstalling the plugin removes it"
	if !strings.Contains(out, want) {
		t.Fatalf("the operator is not told, in the approved words, which profile is left\nwant: %s\ngot:\n%s", want, out)
	}
}

// AND A GRANT WINDOWS WILL NOT TAKE BACK STILL HOLDS IT (the control): the
// child's SID could still read what it was given, so retirement is not
// established, the close says so, and the image stays bound.
func TestARefusedGrantRevokeStillHoldsTheImage(t *testing.T) {
	old := revokeGrant
	revokeGrant = func(dir string, sid *windows.SID) error {
		_ = changeContainerGrant(dir, sid, false) // the machine keeps no grant of this test's
		return errors.New("revoke the container's read grant: Access is denied (test)")
	}
	t.Cleanup(func() { revokeGrant = old })
	s, image, _ := startWalledForCleanup(t, "com.example.revoke-refused")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err := s.CloseContext(ctx)
	var held *ContainmentCleanupError
	if !errors.As(err, &held) {
		t.Fatalf("a refused grant revoke was reported as a clean retirement: %v", err)
	}
	if image.closed.Load() != 0 {
		t.Fatal("the image binding was released while a grant was not taken back")
	}
}
