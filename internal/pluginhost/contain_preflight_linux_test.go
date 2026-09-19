//go:build linux

package pluginhost

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// .
// .
// .
// .
// .
func TestAContainmentRefusalCarriesTheRemedyOnlyWhenItKnowsIt(t *testing.T) {
	const observed = "bwrap: loopback: Failed RTM_NEWADDR: Operation not permitted"

	// .
	// .
	restricted := classifyContainmentFailure(observed, true)
	if restricted.Remedy == "" {
		t.Fatal("a known cause must carry its remedy")
	}
	for _, want := range []string{"/etc/apparmor.d/bwrap", "apparmor_parser -r", "userns,", "security policy"} {
		if !strings.Contains(restricted.Error(), want) {
			t.Fatalf("the remedy must name %q: %s", want, restricted.Error())
		}
	}
	// .
	for _, never := range []string{"--share-net", "run as root", "disable AppArmor", "sudo aii"} {
		if strings.Contains(restricted.Error(), never) {
			t.Fatalf("a containment refusal must not propose giving up containment (%q): %s", never, restricted.Error())
		}
	}

	// .
	// .
	// .
	unknown := classifyContainmentFailure(observed, false)
	if unknown.Remedy != "" {
		t.Fatalf("an unexplained failure must not invent a remedy: %s", unknown.Remedy)
	}
	if !strings.Contains(unknown.Error(), observed) {
		t.Fatalf("what was seen must survive into the refusal: %s", unknown.Error())
	}
	// .
	if silent := classifyContainmentFailure("", true); !strings.Contains(silent.Error(), "without saying why") {
		t.Fatalf("a probe that said nothing must say so: %s", silent.Error())
	}
}

// .
// .
func TestTheContainmentProbeSeesAWallItCannotBuild(t *testing.T) {
	namespaceProven.Store(false)
	t.Cleanup(func() { namespaceProven.Store(false) })

	var unavailable *ContainmentUnavailableError
	if err := checkNamespace(context.Background(), "/bin/false"); !errors.As(err, &unavailable) {
		t.Fatalf("a mechanism that cannot build a namespace must refuse typed, got %v", err)
	}
	// .
	// .
	if namespaceProven.Load() {
		t.Fatal("a failed probe must not be cached as proof")
	}

	// .
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		t.Skip("no bubblewrap on this host")
	}
	if err := checkNamespace(context.Background(), bwrap); err != nil {
		t.Skipf("this host cannot build a namespace either — that is the condition under test, not a defect: %v", err)
	}
	if !namespaceProven.Load() {
		t.Fatal("a host that built one must not be asked again")
	}
}

// .
// .
// .
// .
// .
func TestAnInterruptedProbeIsACancellationNotAVerdict(t *testing.T) {
	namespaceProven.Store(false)
	t.Cleanup(func() { namespaceProven.Store(false) })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := checkNamespace(ctx, "/bin/false")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled probe must surface the cancellation, got %v", err)
	}
	var unavailable *ContainmentUnavailableError
	if errors.As(err, &unavailable) {
		t.Fatalf("and must not be dressed up as a diagnosis: %v", err)
	}
	if namespaceProven.Load() {
		t.Fatal("nothing was proved")
	}
}

// .
// .
// .
// .
// .
func TestTheDocumentedProfileIsTheOneTheHostPrints(t *testing.T) {
	// .
	// .
	// .
	// .
	// .
	var doc string
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "CONTAINMENT-LINUX.md"))
	switch {
	case err == nil:
		doc = string(raw)
	case os.IsNotExist(err):
		t.Log("docs/CONTAINMENT-LINUX.md is not in this tree (docs-free export): pinning the refusal alone")
	default:
		t.Fatalf("the remedy the host prints must be documented: %v", err)
	}
	for _, line := range []string{
		"abi <abi/4.0>,",
		"include <tunables/global>",
		"profile bwrap /usr/bin/bwrap flags=(unconfined) {",
		"userns,",
		"include if exists <local/bwrap>",
		"/etc/apparmor.d/bwrap",
		"apparmor_parser -r /etc/apparmor.d/bwrap",
	} {
		if !strings.Contains(bwrapProfileRemedy, line) {
			t.Fatalf("the refusal has stopped printing %q", line)
		}
		if err == nil && !strings.Contains(doc, line) {
			t.Fatalf("docs/CONTAINMENT-LINUX.md no longer matches the refusal: missing %q", line)
		}
	}
	// .
	// .
	for _, never := range []string{"--share-net", "aa-teardown", "systemctl stop apparmor", "apparmor=0"} {
		if strings.Contains(doc, never) && !strings.Contains(doc, "Do not") {
			t.Fatalf("the documentation proposes giving up containment: %q", never)
		}
	}
}

// .
// .
// .
// .
// .
func TestTheRemedyNeedsEvidenceNotJustTheSwitch(t *testing.T) {
	// .
	// .
	for _, observed := range []string{
		"bwrap: loopback: Failed RTM_NEWADDR: Operation not permitted",
		"bwrap: setting up uid map: Permission denied",
		"bwrap: Creating new namespace failed: Operation not permitted",
	} {
		if got := classifyContainmentFailure(observed, true); got.Remedy == "" {
			t.Fatalf("this is the confinement and must carry the remedy: %q", observed)
		}
	}
	// .
	// .
	for _, observed := range []string{
		"bwrap: Can't write to /newroot: No space left on device",
		"bwrap: execvp /bin/true: No such file or directory",
		"bwrap: Can't mount proc on /newroot/proc: Invalid argument",
		"bwrap: Can't find source path /opt/models: No such file or directory",
	} {
		got := classifyContainmentFailure(observed, true)
		if got.Remedy != "" {
			t.Fatalf("a %q failure must not prescribe rewriting a security profile", observed)
		}
		if !strings.Contains(got.Error(), observed) {
			t.Fatalf("and the real cause must survive: %s", got.Error())
		}
	}
	// .
	// .
	if got := classifyContainmentFailure("bwrap: loopback: Failed RTM_NEWADDR: Operation not permitted", false); got.Remedy != "" {
		t.Fatalf("the switch is off; this cannot be the cause: %s", got.Remedy)
	}
}
