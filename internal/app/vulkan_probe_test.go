package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/vulkancap"
)

// The probe is where the packages put it: aii itself on Windows, and on Linux
// the helper beside aii. Beside a test binary there is none, which is the
// case of a bare install made before the helper shipped: the query fails at
// once and the measurement leaves Vulkan unknown.
func TestTheShippedVulkanProbeIsBesideTheExecutable(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	probe, args, err := shippedVulkanProbe()
	if err != nil {
		t.Fatal(err)
	}
	if packagefmt.HostPlatform() == "windows" {
		if probe != exe || len(args) != 1 || args[0] != vulkancap.Subcommand {
			t.Fatalf("windows probe = %q %q, want %q [%s]", probe, args, exe, vulkancap.Subcommand)
		}
		return
	}
	if want := filepath.Join(filepath.Dir(exe), vulkancap.Helper); probe != want || len(args) != 0 {
		t.Fatalf("probe = %q %q, want %q beside the executable", probe, args, want)
	}
	if raw, err := selectionQuery(t.Context(), unhurriedQuery, probe, args...); err == nil {
		t.Fatalf("a probe that is not there answered: %q", raw)
	}
}
