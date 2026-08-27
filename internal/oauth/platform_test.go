package oauth

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The platform contract: a surface that cannot adopt offers nothing, and
// one that can offers exactly the stores it knows. Runs on every target,
// so the answer is checked where it is compiled rather than assumed.
func TestPlatformContract(t *testing.T) {
	mobile := runtime.GOOS == "android" || runtime.GOOS == "ios"
	if Available() == mobile {
		t.Fatalf("GOOS=%s: Available()=%v — mobile cannot adopt another app's files, desktop can",
			runtime.GOOS, Available())
	}
	if Available() != (len(Kinds()) > 0) {
		t.Fatalf("GOOS=%s: Available()=%v but Kinds()=%v — the picker must never offer what the platform cannot honour",
			runtime.GOOS, Available(), Kinds())
	}
	if !Available() {
		// And the refusal must explain itself, not name a path that was
		// never going to exist.
		_, err := New(KindClaudeCode)
		if err == nil {
			t.Fatal("adoption must refuse where it cannot work")
		}
		for _, want := range []string{"desktop", "API key"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("the refusal must say why and what to do instead, got: %v", err)
			}
		}
		return
	}
	// Desktop: the layout is the same relative path everywhere; only the
	// separator differs, and filepath.Join owns that.
	for _, k := range Kinds() {
		sp, err := specFor(k)
		if err != nil {
			t.Fatalf("%s: %v", k, err)
		}
		if len(sp.file) == 0 {
			t.Fatalf("%s declares no file location", k)
		}
		got := filepath.Join(append([]string{"HOME"}, sp.file...)...)
		if !strings.HasPrefix(got, "HOME"+string(filepath.Separator)) {
			t.Fatalf("%s: %q is not under the home directory with this platform's separator", k, got)
		}
		if strings.Contains(got, "/") && filepath.Separator != '/' {
			t.Fatalf("%s: %q carries a hard-coded slash on %s", k, got, runtime.GOOS)
		}
	}
}

// Every adoptable store declares the reader and wire route it owns. Credential
// renewal remains with the source owner.
func TestEverySpecIsComplete(t *testing.T) {
	if !Available() {
		t.Skip("no adoptable stores on this platform")
	}
	for _, k := range Kinds() {
		sp, err := specFor(k)
		if err != nil {
			t.Fatal(err)
		}
		if sp.parse == nil {
			t.Errorf("%s: no parser for its file shape", k)
		}
		if sp.dialect == "" {
			t.Errorf("%s: must declare the dialect its credential is valid for", k)
		}
	}
}
