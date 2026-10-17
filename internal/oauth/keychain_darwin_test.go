//go:build darwin

package oauth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// THE CASE THIS EXISTS FOR. Claude Code on macOS moved its credential
// into the login Keychain and DELETES the file when it migrates, so the
// file is simply absent on a machine that worked the day before.
func TestAnAbsentFileFallsBackToTheKeychain(t *testing.T) {
	old := keychainLookup
	defer func() { keychainLookup = old }()
	forgetAdopted("Claude Code-credentials") // the memo outlives a test; whichever ran before must not answer this one
	t.Cleanup(func() { forgetAdopted("Claude Code-credentials") })
	var asked string
	keychainLookup = func(_ context.Context, service string) ([]byte, error) {
		asked = service
		return []byte("  {\"claudeAiOauth\":{\"accessToken\":\"t\"}}  \n"), nil
	}
	got, err := adoptedBytes(t.Context(), "Claude Code-credentials", filepath.Join(t.TempDir(), "gone.json"))
	if err != nil {
		t.Fatalf("keychain fallback failed: %v", err)
	}
	if asked != "Claude Code-credentials" {
		t.Fatalf("asked the keychain for %q", asked)
	}
	// Trimmed, because the tool prints a trailing newline and the parse
	// downstream is the file's parse, unchanged.
	if strings.HasPrefix(string(got), " ") || strings.HasSuffix(string(got), "\n") {
		t.Fatalf("bytes were not trimmed: %q", got)
	}
}

// A keychain that refuses must leave the FILE'S error standing: "not
// signed in" is what an operator can act on, and os.IsNotExist has to
// keep working for the caller that writes that sentence.
func TestAKeychainFailureKeepsTheFilesNotExistError(t *testing.T) {
	old := keychainLookup
	defer func() { keychainLookup = old }()
	forgetAdopted("Claude Code-credentials") // the memo outlives a test; whichever ran before must not answer this one
	t.Cleanup(func() { forgetAdopted("Claude Code-credentials") })
	keychainLookup = func(context.Context, string) ([]byte, error) {
		return nil, errors.New("user denied access")
	}
	_, err := adoptedBytes(t.Context(), "Claude Code-credentials", filepath.Join(t.TempDir(), "gone.json"))
	if !os.IsNotExist(err) {
		t.Fatalf("err = %v, want the file's not-exist error", err)
	}
}

// The note says what was actually looked at, so a refusal names the
// whole search rather than half of it.
func TestTheNoteNamesWhatWasSearched(t *testing.T) {
	if got := keychainNote("Claude Code-credentials"); !strings.Contains(got, "Claude Code-credentials") {
		t.Fatalf("note does not name the item: %q", got)
	}
	if got := keychainNote(""); !strings.Contains(got, "cannot read them") {
		t.Fatalf("with no item configured the old warning must stand: %q", got)
	}
}

// THE KEYCHAIN IS ASKED ONCE A MINUTE, NOT ONCE A REQUEST — and a
// rejection asks again at once.
func TestKeychainIsReadOnceAMinuteAndAgainAfterARejection(t *testing.T) {
	dir := t.TempDir()
	missing := dir + "/absent.json"
	calls := 0
	old := keychainLookup
	keychainLookup = func(ctx context.Context, service string) ([]byte, error) { calls++; return []byte(`{"n":1}`), nil }
	defer func() { keychainLookup = old }()
	forgetAdopted("test.service")
	for i := 0; i < 5; i++ {
		if _, err := adoptedBytes(t.Context(), "test.service", missing); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("the Keychain was asked %d times for five reads", calls)
	}
	forgetAdopted("test.service")
	if _, err := adoptedBytes(t.Context(), "test.service", missing); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("a rejection did not ask the Keychain again: %d calls", calls)
	}
}
