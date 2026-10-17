//go:build (darwin && !ios) || (linux && !android)

package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/app"
	"github.com/aiii-dot-id/aii-os/internal/install"
)

// launcherRoot is ~/.aii as `aii init` leaves it: slot 0 on port 8180, and a
// second slot moved to 8181 by its operator, which the offer must step over.
func launcherRoot(t *testing.T) (root, slot string) {
	t.Helper()
	root = t.TempDir()
	slot, err := install.Create(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := install.Create(root, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.SetDashboardPort(install.ConfigPathIn(second), 8181); err != nil {
		t.Fatal(err)
	}
	return root, slot
}

func TestAPortAnotherProgramHoldsIsAnsweredWithTheNextFreeOne(t *testing.T) {
	root, slot := launcherRoot(t)
	path := install.ConfigPathIn(slot)
	raw, _ := os.ReadFile(path)
	free := func(p int) bool { return p != 8182 } // 8180 is held; 8181 is slot 1's; 8182 is taken too
	var wrote []int
	write := func(p int) error { wrote = append(wrote, p); return nil }
	var asked []string
	answer := func(yes, ok bool) asker {
		return func(question, yesLabel, noLabel string) (bool, bool) {
			asked = append(asked, question+"|"+yesLabel+"|"+noLabel)
			return yes, ok
		}
	}
	untouched := func(when string) {
		t.Helper()
		if now, _ := os.ReadFile(path); !bytes.Equal(raw, now) {
			t.Fatalf("%s: the launcher wrote config.json itself", when)
		}
	}

	port, err := movePort(root, slot, 8180, answer(true, true), write, free)
	if err != nil || port != 8183 || len(wrote) != 1 || wrote[0] != 8183 {
		t.Fatalf("yes: port %d, wrote %v, err %v", port, wrote, err)
	}
	if len(asked) != 1 {
		t.Fatalf("the person was asked %d times before the port moved", len(asked))
	}
	q := asked[0]
	for _, said := range []string{"Port 8180 is already in use by another program", "http://127.0.0.1:8183", "sign-in per address", "sign in once more", "|Use port 8183|Not now"} {
		if !strings.Contains(q, said) {
			t.Fatalf("the question does not say %q: %s", said, q)
		}
	}
	if strings.Contains(q, "config.json") {
		t.Fatalf("the question sends the person to config.json: %s", q)
	}
	untouched("yes")

	wrote = nil
	if _, err := movePort(root, slot, 8180, answer(false, true), write, free); !errors.Is(err, errPortKept) || len(wrote) != 0 {
		t.Fatalf("not now: wrote %v, err %v", wrote, err)
	}
	untouched("not now")

	_, err = movePort(root, slot, 8180, answer(false, false), write, free)
	if err == nil || len(wrote) != 0 || !strings.Contains(err.Error(), "aii dashboard-port -dir") || strings.Contains(err.Error(), "config.json") {
		t.Fatalf("no way to ask: wrote %v, err %v", wrote, err)
	}
	untouched("no way to ask")

	asked = nil
	if _, err := movePort(root, slot, 8180, answer(true, true), write, func(int) bool { return false }); err == nil || len(asked) != 0 || len(wrote) != 0 {
		t.Fatalf("no free port: asked %v, wrote %v, err %v", asked, wrote, err)
	}
	if _, err := movePort(root, slot, 8180, answer(true, true), func(int) error { return errors.New("disk full") }, free); err == nil || !strings.Contains(err.Error(), "nothing was started") {
		t.Fatalf("a failed write is not said: %v", err)
	}
	untouched("a failed write")
}

// THE LAUNCHER'S WRITER IS aii, THE CONFIG'S OWNER — never an edit of
// config.json here. A stand-in aii records what it was asked and writes
// nothing, so a launcher that wrote the file itself is caught; the real aii,
// built from this tree, then carries the same request to the one writer.
func TestTheLauncherWritesThePortThroughAii(t *testing.T) {
	_, slot := launcherRoot(t)
	path := install.ConfigPathIn(slot)
	raw, _ := os.ReadFile(path)

	standIn := filepath.Join(t.TempDir(), "aii")
	if err := os.WriteFile(standIn, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$0.args\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writePort(standIn, slot)(8183); err != nil {
		t.Fatal(err)
	}
	args, err := os.ReadFile(standIn + ".args")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(args), strings.Join([]string{"dashboard-port", "-dir", slot, "8183"}, "\n")+"\n"; got != want {
		t.Fatalf("the launcher asked aii %q, want %q", got, want)
	}
	if now, _ := os.ReadFile(path); !bytes.Equal(raw, now) {
		t.Fatal("the launcher wrote config.json itself")
	}

	real := filepath.Join(t.TempDir(), "aii")
	build := exec.Command("go", "build", "-o", real, "../aii")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build aii: %v\n%s", err, out)
	}
	if err := writePort(real, slot)(8183); err != nil {
		t.Fatal(err)
	}
	if got := install.ConfiguredPort(slot, 0); got != 8183 {
		t.Fatalf("the real aii did not move the port: %d", got)
	}
	cfg, err := app.LoadConfig(path)
	if err != nil {
		t.Fatalf("the port aii wrote left a config the loader refuses: %v", err)
	}
	if cfg.Identity.LedgerPath == "" || cfg.Identity.KeyPath == "" {
		t.Fatal("the written config lost the slot's identity defaults")
	}
	if err := writePort(real, slot)(70000); err == nil || !strings.Contains(err.Error(), "out of range") {
		t.Fatalf("aii's refusal did not reach the launcher: %v", err)
	}
	if got := install.ConfiguredPort(slot, 0); got != 8183 {
		t.Fatalf("a refused port moved the dashboard to %s", strconv.Itoa(got))
	}
}
