package oauth

import (
	"os"
	"path/filepath"
	"testing"
)

func TestASavedCredentialLeavesNothingOfItUnderAnotherName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secrets", "token.json")
	if err := writePrivate(path, []byte(`{"refresh_token":"THE EARLIER TOKEN"}`)); err != nil {
		t.Fatal(err)
	}
	second := filepath.Join(dir, "second.txt")
	if err := os.Link(path, second); err != nil {
		t.Skipf("hard links unavailable here: %v", err)
	}
	if err := writePrivate(path, []byte(`{"refresh_token":"THE NEW TOKEN"}`)); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != `{"refresh_token":"THE NEW TOKEN"}` {
		t.Errorf("the credential holds what was saved: %q", got)
	}
	if got, err := os.ReadFile(second); err != nil || len(got) != 0 {
		t.Errorf("the other name holds nothing of the earlier token: %q %v", got, err)
	}
	single := filepath.Join(dir, "single.json")
	for _, body := range []string{"{}", `{"x":1}`} {
		if err := writePrivate(single, []byte(body)); err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(single); string(got) != body {
			t.Errorf("a credential with one name holds what was saved: %q", got)
		}
	}
}

func TestADisconnectedTokenLeavesNothingUnderAnotherName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token.json")
	if err := writePrivate(path, []byte(`{"refresh_token":"THE TOKEN"}`)); err != nil {
		t.Fatal(err)
	}
	second := filepath.Join(dir, "second.txt")
	if err := os.Link(path, second); err != nil {
		t.Skipf("hard links unavailable here: %v", err)
	}
	if _, err := RemoveTokenFile(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the token file is removed: %v", err)
	}
	if got, err := os.ReadFile(second); err != nil || len(got) != 0 {
		t.Errorf("the other name holds nothing of the token: %q %v", got, err)
	}
	if _, err := RemoveTokenFile(path); err != nil {
		t.Errorf("a token file that is not there is no error: %v", err)
	}
}
