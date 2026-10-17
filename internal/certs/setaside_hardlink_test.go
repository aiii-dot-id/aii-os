package certs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAKeySetAsideLeavesNothingOfTheKeyItDisplacesUnderAnotherName(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write(stateFile, `{"name":"old.example"}`)
	write("public.key", "THE KEY SET ASIDE NOW")
	earlier := write("public.key.prev", "THE KEY SET ASIDE BEFORE")
	second := filepath.Join(dir, "second-name")
	if err := os.Link(earlier, second); err != nil {
		t.Skipf("hard links unavailable here: %v", err)
	}
	if _, err := New(Config{Dir: dir, Name: "new.example"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(earlier); string(got) != "THE KEY SET ASIDE NOW" {
		t.Errorf("the key now at .prev is the one set aside: %q", got)
	}
	if got, err := os.ReadFile(second); err != nil || len(got) != 0 {
		t.Errorf("a name made for the key it displaced holds nothing of it: %q %v", got, err)
	}
}
