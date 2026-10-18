//go:build (darwin && !ios) || (linux && !android)

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestTheFirstStartSaysHowToReadTheAccessToken(t *testing.T) {
	aii := filepath.Join(t.TempDir(), "AII OS.app", "Contents", "MacOS", "aii")
	dir := filepath.Join(t.TempDir(), "it's the slot")
	note := firstStartTokenNote(aii, dir)
	if !strings.Contains(note, "once in each browser") || !strings.Contains(note, "run this in "+terminalName+" (sh, bash or zsh):") {
		t.Fatalf("the note does not say what the page asks, or where and in which shell to run the command:\n%s", note)
	}
	cmd := tokenCommand(aii, dir)
	if !strings.Contains(note, cmd) {
		t.Fatalf("the note does not carry the command %q:\n%s", cmd, note)
	}
	out, err := exec.Command("/bin/sh", "-c", "set -- "+cmd+`; printf '%s\n' "$1" "$2" "$3" "$4" "$#"`).Output()
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{aii, "dashboard-token", "-dir", dir, "4"}, "\n") + "\n"
	if string(out) != want {
		t.Fatalf("a shell reads the command as\n%s\nwant\n%s", out, want)
	}
}

func TestTheLauncherOpensThePageSignedIn(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "it's the slot")
	const plain = "http://127.0.0.1:8190"
	calls := filepath.Join(home, "calls")
	fake := func(name, body string) string {
		t.Helper()
		aii := filepath.Join(home, name)
		if err := os.WriteFile(aii, []byte("#!/bin/sh\nprintf '%s|' \"$@\" >> '"+calls+"'\necho >> '"+calls+"'\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
		return aii
	}
	for _, tc := range []struct {
		name, body, want string
		linked           bool
	}{
		{"a sign-in link", "echo '" + plain + "/auth/link#ABCD1234EFGH'\n", plain + "/auth/link#ABCD1234EFGH", true},
		{"no token asked for", "echo '" + plain + "'\n", plain, true},
		{"aii could not", "echo 'dashboard-link: the dashboard did not answer' >&2\nexit 1\n", plain, false},
		{"another address", "echo 'http://elsewhere.test:8190/auth/link#ABCD'\n", plain, false},
		{"another port", "echo 'http://127.0.0.1:81900/auth/link#ABCD'\n", plain, false},
	} {
		os.Remove(calls)
		aii := fake("aii-"+strings.ReplaceAll(tc.name, " ", "-"), tc.body)
		var opened []string
		linked := !tc.linked
		open := signedInOpener(aii, dir, func(u string) error { opened = append(opened, u); return nil }, &linked)
		if err := open(plain); err != nil {
			t.Fatal(err)
		}
		if len(opened) != 1 || opened[0] != tc.want || linked != tc.linked {
			t.Errorf("%s: opened %q, linked %v; want %q, %v", tc.name, opened, linked, tc.want, tc.linked)
		}
		raw, _ := os.ReadFile(calls)
		if got := strings.TrimSpace(string(raw)); got != "dashboard-link|-dir|"+dir+"|-port|8190|" {
			t.Errorf("%s: aii was asked %q", tc.name, got)
		}
	}
}
