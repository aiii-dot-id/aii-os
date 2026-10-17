package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/install"
)

// verbAnswer is what one invocation must say: the status, then the two
// streams, with {root} standing for the slots' directory.
type verbAnswer struct {
	args           []string
	code           int
	stdout, stderr string
}

// runVerb runs the probe binary with args under env in dir, bounded.
func runVerb(t *testing.T, bin, dir string, env []string, args ...string) (int, string, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), positionalProbeDeadline)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("aii %v did not finish within %v: %s%s", args, positionalProbeDeadline, stdout.String(), stderr.String())
	}
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run aii %v: %v", args, err)
	}
	return code, stdout.String(), stderr.String()
}

// verbHome is a home with no slots under it, and the environment that
// points the binary at it (HOME on unix, USERPROFILE on Windows, and no
// inherited SUDO_USER to send it elsewhere).
func verbHome(t *testing.T) (root string, env []string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SUDO_USER", "")
	root, err := install.Root()
	if err != nil {
		t.Fatal(err)
	}
	return root, os.Environ()
}

// checkAnswers runs each case in order — later cases see what earlier ones
// made — and compares every byte.
func checkAnswers(t *testing.T, bin, dir string, env []string, root string, cases []verbAnswer) {
	t.Helper()
	for _, c := range cases {
		code, stdout, stderr := runVerb(t, bin, dir, env, c.args...)
		wantOut := strings.ReplaceAll(c.stdout, "{root}", root)
		wantErr := strings.ReplaceAll(c.stderr, "{root}", root)
		if code != c.code || stdout != wantOut || stderr != wantErr {
			t.Errorf("aii %s:\n exit %d, want %d\n stdout %q\n   want %q\n stderr %q\n   want %q",
				strings.Join(c.args, " "), code, c.code, stdout, wantOut, stderr, wantErr)
		}
	}
}

const (
	initUsage = "Usage: aii init [--no-start]\n\nCreates the next identity slot under ~/" + install.Dir + " and starts it.\n" +
		"  -no-start\n    \tCreate the slot but do not register or start the service\n"
	registerUsage   = "Usage: aii register <slot>\n\nRegisters and starts the service for an existing slot.\n"
	stopUsage       = "Usage: aii stop <slot>\n\nAsks a running identity to shut down. It stays registered.\n"
	unregisterUsage = "Usage: aii unregister <slot>\n\nStops the identity and removes it from startup. The slot's data is left alone.\n"
	pluginUsageText = "usage: aii plugin <verify|catalog> ...\n" +
		"  verify  [flags] <bundle.aiiospkg>                                verify a bundle offline against the pinned AIII trust roots\n" +
		"  catalog -platform-key <root> -catalog-dir <dir> [search <q>]     browse the platform's signed plugin catalog\n"
	dashboardPortUsage = "Usage: aii dashboard-port [-dir directory] [-config file] <port>\n" +
		"  -config string\n    \tPath to config file (default: config/config.json)\n" +
		"  -dir string\n    \tIdentity install directory\n"
)

// TestTheVerbsAnswerByteForByte covers every verb's answers that touch no
// service manager: usage, flag errors, -h, a slot that is not there, the
// verbs that need a config they are not given, and init --no-start with
// the slots listing it made.
func TestTheVerbsAnswerByteForByte(t *testing.T) {
	bin := probeBinary(t)
	root, env := verbHome(t)
	dir := t.TempDir()
	missing := filepath.Join(root, "identity-7")
	_, statErr := os.Stat(missing)
	if statErr == nil {
		t.Fatal("the missing slot exists")
	}
	first := filepath.Join(root, install.SlotName(0))
	// The verbs that read a config read the working directory's, which
	// holds none: the refusal names the relative path and the system's own
	// reason for it.
	rel := install.ConfigPathIn("")
	_, cfgErr := os.Stat(filepath.Join(dir, rel))
	var pe *os.PathError
	if !errors.As(cfgErr, &pe) {
		t.Fatalf("the working directory holds a config: %v", cfgErr)
	}
	// The operation's name is the system's too, not only its reason: Go
	// calls the look at a file "stat" on Unix and by the system call's own
	// name on Windows. Written as "stat" here, the expectation was wrong
	// on Windows for both verbs (found by the whole suite's run there).
	cfgMissing := func(verb string) string {
		return verb + ": read " + rel + ": " + pe.Op + " " + rel + ": " + pe.Err.Error() + "\n"
	}

	cases := []verbAnswer{
		{args: []string{"slots"}, stdout: "No identity slots under {root} — create one with: aii init\n"},
		{args: []string{"init", "-h"}, stderr: initUsage},
		{args: []string{"init", "-bogus"}, code: 2, stderr: "flag provided but not defined: -bogus\n" + initUsage},
		{args: []string{"register"}, code: 2, stderr: registerUsage},
		{args: []string{"register", "-h"}, stderr: registerUsage},
		{args: []string{"register", "-bogus"}, code: 2, stderr: "flag provided but not defined: -bogus\n" + registerUsage},
		{args: []string{"register", "a", "b"}, code: 2, stderr: registerUsage},
		{args: []string{"register", "identity-7"}, code: 1, stderr: "register: " + missing + " does not exist — create it with: aii init\n"},
		{args: []string{"stop"}, code: 2, stderr: stopUsage},
		{args: []string{"stop", "-h"}, stderr: stopUsage},
		{args: []string{"stop", "-bogus"}, code: 2, stderr: "flag provided but not defined: -bogus\n" + stopUsage},
		{args: []string{"stop", "identity-7"}, code: 1, stderr: "stop: inspect " + missing + ": " + statErr.Error() + "\n"},
		{args: []string{"unregister"}, code: 2, stderr: unregisterUsage},
		{args: []string{"unregister", "-h"}, stderr: unregisterUsage},
		{args: []string{"unregister", "-bogus"}, code: 2, stderr: "flag provided but not defined: -bogus\n" + unregisterUsage},
		{args: []string{"unregister", "identity-7"}, code: 1, stderr: "unregister: " + missing + " does not exist\n"},
		{args: []string{"plugin"}, code: 2, stderr: pluginUsageText},
		{args: []string{"plugin", "nonsense"}, code: 2, stderr: pluginUsageText},
		{args: []string{"log"}, code: 1, stderr: cfgMissing("log")},
		{args: []string{"dashboard-token"}, code: 1, stderr: cfgMissing("dashboard-token")},
		{args: []string{"dashboard-port"}, code: 2, stderr: dashboardPortUsage},
		{args: []string{"dashboard-link"}, code: 1, stderr: cfgMissing("dashboard-link")},
		{args: []string{"init", "--no-start"}, stdout: "Created " + first + " (dashboard port " + strconv.Itoa(install.Port(0)) + ")\n\nStart it with:\n  " + install.StartCommand(install.SlotName(0), first) + "\n" +
			"\nOnce it runs, the dashboard asks for its access token, once in each browser; that browser then stays signed in.\n" + tokenLines(first)},
		{args: []string{"slots"}, stdout: "  identity-0   port " + strconv.Itoa(install.Port(0)) + "  (unnamed — not yet born)\n"},
		{args: []string{"slots", "extra"}, stdout: "  identity-0   port " + strconv.Itoa(install.Port(0)) + "  (unnamed — not yet born)\n"},
	}
	if runtime.GOOS != "windows" {
		cases = append(cases, verbAnswer{args: []string{"_hardware-vulkan"}, code: 2,
			stderr: "aii: _hardware-vulkan is the Windows Vulkan probe; on Linux the probe is aii-vulkan-probe, beside aii\n"})
	}
	checkAnswers(t, bin, dir, env, root, cases)

	// The verbs whose usage is long are compared by their first line and
	// their status: each is routed, and refuses with its own words.
	for verb, head := range map[string]string{
		"verify":       "usage: aii verify -ledger <path to ledger.jsonl>",
		"ledger":       "usage: aii ledger show [-dir D]",
		"snapshot":     "usage:\n  aii snapshot open -in",
		"escrow":       "usage:\n  aii escrow create  -out <escrow.age>",
		"memory-score": "usage: aii memory-score -db <path to aii.db>",
	} {
		code, stdout, stderr := runVerb(t, bin, dir, env, verb)
		if code != 2 || stdout != "" || !strings.HasPrefix(stderr, head) {
			t.Errorf("aii %s: exit %d, stdout %q, stderr %q — want 2 and a usage starting %q", verb, code, stdout, stderr, head)
		}
	}
}
