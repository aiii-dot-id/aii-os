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

// WHAT THE OPERATOR IS TOLD when the wall cannot be built. The kernel
// says "apparmor=DENIED ... capname=net_admin" in the audit log, which
// is not where anyone looks when a plugin will not start; bwrap says
// "loopback: Failed RTM_NEWADDR: Operation not permitted", which names
// a symptom and no cause.
func TestAContainmentRefusalCarriesTheRemedyOnlyWhenItKnowsIt(t *testing.T) {
	const observed = "bwrap: loopback: Failed RTM_NEWADDR: Operation not permitted"

	// The kernel's own switch says this IS the cause: the remedy is
	// offered, exact enough to run.
	restricted := classifyContainmentFailure(observed, true)
	if restricted.Remedy == "" {
		t.Fatal("a known cause must carry its remedy")
	}
	for _, want := range []string{"/etc/apparmor.d/bwrap", "apparmor_parser -r", "userns,", "security policy"} {
		if !strings.Contains(restricted.Error(), want) {
			t.Fatalf("the remedy must name %q: %s", want, restricted.Error())
		}
	}
	// And it must never propose running without the wall, or as root.
	for _, never := range []string{"--share-net", "run as root", "disable AppArmor", "sudo aii"} {
		if strings.Contains(restricted.Error(), never) {
			t.Fatalf("a containment refusal must not propose giving up containment (%q): %s", never, restricted.Error())
		}
	}

	// The switch says otherwise: report what was seen and instruct
	// nothing. A confident wrong instruction about a security policy is
	// worse than none.
	unknown := classifyContainmentFailure(observed, false)
	if unknown.Remedy != "" {
		t.Fatalf("an unexplained failure must not invent a remedy: %s", unknown.Remedy)
	}
	if !strings.Contains(unknown.Error(), observed) {
		t.Fatalf("what was seen must survive into the refusal: %s", unknown.Error())
	}
	// Silence is not a diagnosis either.
	if silent := classifyContainmentFailure("", true); !strings.Contains(silent.Error(), "without saying why") {
		t.Fatalf("a probe that said nothing must say so: %s", silent.Error())
	}
}

// THE PROBE CAN SEE A FAILURE — the control half. A preflight that
// cannot fail proves nothing about the host it passes on.
func TestTheContainmentProbeSeesAWallItCannotBuild(t *testing.T) {
	namespaceProven.Store(false)
	t.Cleanup(func() { namespaceProven.Store(false) })

	var unavailable *ContainmentUnavailableError
	if err := checkNamespace(context.Background(), "/bin/false"); !errors.As(err, &unavailable) {
		t.Fatalf("a mechanism that cannot build a namespace must refuse typed, got %v", err)
	}
	// A failure is never remembered: the operator's fix must take effect
	// on the next attempt, not after a restart.
	if namespaceProven.Load() {
		t.Fatal("a failed probe must not be cached as proof")
	}

	// And on a host where it DOES work, it passes and is remembered.
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

// A PROBE THAT DID NOT FINISH ANSWERED NOTHING. The host shutting down
// is not a verdict about this machine's containment, and must reach the
// caller as the cancellation it is — otherwise every plugin still
// starting at shutdown reports a containment defect, and an operator
// goes looking for an AppArmor problem that does not exist.
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

// THE DOCUMENTATION AND THE REFUSAL MUST SAY THE SAME THING. An
// operator meets this twice — once in the refusal the host prints, once
// in the install documentation — and two subtly different AppArmor
// profiles is exactly the drift that leaves somebody with a half-loaded
// policy and a plugin that still will not start.
func TestTheDocumentedProfileIsTheOneTheHostPrints(t *testing.T) {
	var doc string
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "CONTAINMENT-LINUX.md"))
	switch {
	case err == nil:
		doc = string(raw)
	case os.IsNotExist(err):
		t.Log("docs/CONTAINMENT-LINUX.md is not in this tree: pinning the refusal alone")
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
	// And neither may start suggesting the ways out that are not ways
	// out.
	for _, never := range []string{"--share-net", "aa-teardown", "systemctl stop apparmor", "apparmor=0"} {
		if strings.Contains(doc, never) && !strings.Contains(doc, "Do not") {
			t.Fatalf("the documentation proposes giving up containment: %q", never)
		}
	}
}

// AN ENABLED SWITCH IS NOT A DIAGNOSIS. A full disk, a missing
// executable and a broken mount all fail the probe too, and prescribing
// a replacement of /etc/apparmor.d/bwrap for any of them tells an
// administrator to rewrite a security profile over a problem it has
// nothing to do with — and leaves the real one unexamined.
func TestTheRemedyNeedsEvidenceNotJustTheSwitch(t *testing.T) {
	// What the confinement actually looks like: a permission refusal,
	// about something a namespace is made of.
	for _, observed := range []string{
		"bwrap: loopback: Failed RTM_NEWADDR: Operation not permitted",
		"bwrap: setting up uid map: Permission denied",
		"bwrap: Creating new namespace failed: Operation not permitted",
	} {
		if got := classifyContainmentFailure(observed, true); got.Remedy == "" {
			t.Fatalf("this is the confinement and must carry the remedy: %q", observed)
		}
	}
	// What it does not look like. Each of these is a real failure with a
	// real cause, and none of them is answered by an AppArmor profile.
	for _, observed := range []string{
		"bwrap: Can't write to /newroot: No space left on device",
		"bwrap: execvp /bin/true: No such file or directory",
		"bwrap: Can't mount proc on /newroot/proc: Invalid argument",
		// A daemon in a container that masks its own /proc: the private
		// procfs every child gets cannot be mounted, and no profile helps.
		"bwrap: Can't mount proc on /newroot/proc: Operation not permitted",
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
	// With the switch off, even the matching shape carries no remedy:
	// the kernel says that is not what is happening here.
	if got := classifyContainmentFailure("bwrap: loopback: Failed RTM_NEWADDR: Operation not permitted", false); got.Remedy != "" {
		t.Fatalf("the switch is off; this cannot be the cause: %s", got.Remedy)
	}
}
