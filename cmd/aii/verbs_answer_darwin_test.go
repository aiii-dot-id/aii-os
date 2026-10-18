//go:build darwin && !ios

package main

import (
	"bytes"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/install"
)

// fakeLaunchctl makes PATH a directory holding only a launchctl that
// answers each subcommand with the status and words the environment
// names, so the verbs' answers to launchd's answers are pinned without
// asking the real launchd for anything. PATH is REPLACED, not prepended
// to: the real launchctl lives in /bin, and a fake that went missing must
// leave the verb unable to run any launchctl at all, never able to reach
// the operator's own agents — the labels are per user, not per home.
func fakeLaunchctl(t *testing.T, env []string) []string {
	t.Helper()
	bin := t.TempDir()
	script := `#!/bin/sh
[ -n "$FAKE_CALLS" ] && echo "launchctl $*" >> "$FAKE_CALLS"
case "$1" in
kill) [ -n "$FAKE_KILL_SAYS" ] && echo "$FAKE_KILL_SAYS"; exit ${FAKE_KILL:-0};;
bootout) [ -n "$FAKE_BOOTOUT_SAYS" ] && echo "$FAKE_BOOTOUT_SAYS"; exit ${FAKE_BOOTOUT:-0};;
bootstrap) [ -n "$FAKE_BOOTSTRAP_SAYS" ] && echo "$FAKE_BOOTSTRAP_SAYS"; exit ${FAKE_BOOTSTRAP:-0};;
esac
exit 0
`
	if err := os.WriteFile(filepath.Join(bin, "launchctl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	out := []string{"PATH=" + bin}
	for _, kv := range env {
		if !strings.HasPrefix(kv, "PATH=") {
			out = append(out, kv)
		}
	}
	return out
}

// checkAnswersHere runs each case in this process, as the binary would run
// it, with env's variables set — for the verbs that act on the account's
// registrations, which the binary refuses in a home that is not the
// account's (TestTheServiceVerbsRefuseInAForeignHome), and which run here
// in a home taken for the account's (install.AccountHomeForTest).
func checkAnswersHere(t *testing.T, env []string, root string, cases []verbAnswer) {
	t.Helper()
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
	for _, c := range cases {
		var stdout, stderr bytes.Buffer
		code, _ := dispatch(c.args, &stdout, &stderr)
		wantOut := strings.ReplaceAll(c.stdout, "{root}", root)
		wantErr := strings.ReplaceAll(c.stderr, "{root}", root)
		if code != c.code || stdout.String() != wantOut || stderr.String() != wantErr {
			t.Errorf("aii %s:\n exit %d, want %d\n stdout %q\n   want %q\n stderr %q\n   want %q",
				strings.Join(c.args, " "), code, c.code, stdout.String(), wantOut, stderr.String(), wantErr)
		}
	}
}

// launchdEnv is the fake launchctl's environment alone, PATH among it.
func launchdEnv(t *testing.T, extra ...string) []string {
	t.Helper()
	return append(fakeLaunchctl(t, nil), extra...)
}

// TestTheServiceVerbsAnswerByteForByte pins what register, init, stop and
// unregister say to each of launchd's answers: started, nothing to signal,
// a refusal, and a failed load. They act on the account's own
// registrations, so they run in a home taken for the account's.
func TestTheServiceVerbsAnswerByteForByte(t *testing.T) {
	root, _ := verbHome(t)
	home := filepath.Dir(root)
	t.Cleanup(install.AccountHomeForTest(home))
	slot := func(n int) string { return filepath.Join(root, install.SlotName(n)) }
	// Slots 0 to 89 are taken, so every slot these verbs register is one
	// no operator's machine is likely to run: the second guard, behind
	// the PATH below.
	for n := 0; n < 90; n++ {
		if err := os.MkdirAll(slot(n), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	const note = "note: it will start again at login; macOS lists it under Settings → General → Login Items\n"
	unit := func(n int) string { return install.Unit + install.SlotName(n) }
	port := func(n int) string { return strconv.Itoa(install.Port(n)) }
	// The account's installed program is an AII OS.app in this home, of a
	// version above any /Applications copy the machine may carry, so this
	// test's binary is never it: the machine service's registration is
	// refused from it, naming the installed program, on every Mac alike.
	app := filepath.Join(home, "Applications", "AII OS.app", "Contents")
	if err := os.MkdirAll(filepath.Join(app, "MacOS"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "MacOS", "aii"), []byte("installed"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "Info.plist"), []byte(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>CFBundleShortVersionString</key><string>99.0.0</string></dict></plist>`), 0o644); err != nil {
		t.Fatal(err)
	}
	self, err := runningProgram()
	if err != nil {
		t.Fatal(err)
	}
	machineNote := "note: the machine service is registered only from this account's installed program (" + filepath.Join(app, "MacOS", "aii") + "); this is " + self + "\n"

	checkAnswersHere(t, launchdEnv(t), root, []verbAnswer{
		{args: []string{"init"}, stdout: "Created " + slot(90) + " (dashboard port " + port(90) + ")\nStarted " + unit(90) + "\n\nOpen http://127.0.0.1:" + port(90) + " to meet them.\n" +
			"\nThe dashboard asks for its access token, once in each browser; that browser then stays signed in.\n" + tokenLines(slot(90)), stderr: note + machineNote},
		{args: []string{"register", "identity-90"}, stdout: "Started " + unit(90) + "\n",
			stderr: note + machineNote + "note: identity-90 has no identity key yet; the machine service records the identity at its first link after birth\n"},
		{args: []string{"stop", "identity-90"}, stdout: "Stopped " + unit(90) + "\n"},
		{args: []string{"unregister", "identity-90"}, stdout: "Unregistered " + unit(90) + " — its data is still at " + slot(90) + "\n"},
	})
	checkAnswersHere(t, launchdEnv(t, "FAKE_KILL=3", "FAKE_KILL_SAYS=No process to signal."), root, []verbAnswer{
		{args: []string{"stop", "identity-90"}, stdout: unit(90) + " is not running\n"},
	})
	checkAnswersHere(t, launchdEnv(t, "FAKE_KILL=113", "FAKE_KILL_SAYS=Could not find service"), root, []verbAnswer{
		{args: []string{"stop", "identity-90"}, code: 1, stderr: "stop: stop " + unit(90) + ": exit status 113: Could not find service\n"},
	})
	checkAnswersHere(t, launchdEnv(t, "FAKE_KILL=", "FAKE_KILL_SAYS=", "FAKE_BOOTOUT=5", "FAKE_BOOTOUT_SAYS=Boot-out failed: 5: Input/output error"), root, []verbAnswer{
		{args: []string{"unregister", "identity-90"}, code: 1, stderr: "unregister: unload " + unit(90) + ": exit status 5: Boot-out failed: 5: Input/output error\n"},
	})
	failed := "exit status 5: Bootstrap failed: 5: Input/output error"
	checkAnswersHere(t, launchdEnv(t, "FAKE_BOOTOUT=", "FAKE_BOOTOUT_SAYS=", "FAKE_BOOTSTRAP=5", "FAKE_BOOTSTRAP_SAYS=Bootstrap failed: 5: Input/output error"), root, []verbAnswer{
		{args: []string{"register", "identity-90"}, code: 1, stderr: "register: load " + unit(90) + ": " + failed + "\n"},
		{args: []string{"init"}, code: 1, stdout: "Created " + slot(91) + " (dashboard port " + port(91) + ")\n",
			stderr: "init: load " + unit(91) + ": " + failed + "\n\nThe slot is ready. Start it with:\n  " + install.StartCommand(install.SlotName(91), slot(91)) + "\n"},
	})
}

func TestTheServiceVerbsRefuseInAForeignHome(t *testing.T) {
	bin := probeBinary(t)
	root, env := verbHome(t)
	calls := filepath.Join(t.TempDir(), "calls")
	env = append(fakeLaunchctl(t, env), "FAKE_CALLS="+calls)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "identity-90"), 0o700); err != nil {
		t.Fatal(err)
	}
	home := filepath.Dir(root)
	account, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	refused := ": this acts on this account's own registrations, as its home holds them: HOME is " + home + ", and the account's home is " + account.HomeDir + " — run it from the account's own session (an identity's shell has a home of its own)\n"
	checkAnswers(t, bin, dir, env, root, []verbAnswer{
		{args: []string{"register", "identity-90"}, code: 1, stderr: "register" + refused},
		{args: []string{"stop", "identity-90"}, code: 1, stderr: "stop" + refused},
		{args: []string{"unregister", "identity-90"}, code: 1, stderr: "unregister" + refused},
	})
	if raw, _ := os.ReadFile(calls); len(raw) != 0 {
		t.Fatalf("a refused verb asked launchctl: %q", raw)
	}
}
