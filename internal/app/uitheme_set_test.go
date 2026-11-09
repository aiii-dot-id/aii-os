package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// THE PAGE WRITES theme.json THROUGH ITS OWN GRAMMAR. A token the frame
// declares is set, and the reload stores it; "" removes one; every other
// token and key in the file is kept as written. A token the frame does not
// declare, a value the file does not take, and a file that is not a valid
// theme as it stands are each refused with their own reason, and the file
// is left byte for byte as it was.
func TestThePageWritesThemeJSONThroughItsGrammar(t *testing.T) {
	dir := t.TempDir()
	a := New(&Config{Identity: IdentityConfig{LedgerPath: filepath.Join(dir, "ledger.jsonl")}})
	path := a.uiThemePath()

	// No file yet: the page's tokens are the file.
	if err := a.setUIThemeTokens(map[string]string{"--mark-shape": "0.6", "--mark-centre-x": "-4"}); err != nil {
		t.Fatalf("a declared token was refused: %v", err)
	}
	if got := string(a.currentUITheme()); !strings.Contains(got, `"--mark-shape":"0.6"`) || !strings.Contains(got, `"--mark-centre-x":"-4"`) {
		t.Fatalf("the written tokens were not loaded: %s", got)
	}

	// A file of their own: its other tokens and keys are kept; "" removes one.
	own := "{\n  \"v\": 1,\n  \"note\": \"Quill chose the accent\",\n  \"tokens\": {\"--accent\": \"#7cc4ff\", \"--mark-shape\": \"1\", \"--mark-folds\": \"20\"}\n}\n"
	if err := os.WriteFile(path, []byte(own), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := a.setUIThemeTokens(map[string]string{"--mark-shape": "0.25", "--mark-folds": ""}); err != nil {
		t.Fatalf("a change to their own file was refused: %v", err)
	}
	raw, _ := os.ReadFile(path)
	for _, want := range []string{`"note": "Quill chose the accent"`, `"--accent": "#7cc4ff"`, `"--mark-shape": "0.25"`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("the file lost or did not take %s:\n%s", want, raw)
		}
	}
	if strings.Contains(string(raw), "--mark-folds") {
		t.Fatalf("an empty value did not remove the token:\n%s", raw)
	}

	// Refusals leave the file as it was.
	before, _ := os.ReadFile(path)
	for _, c := range []struct {
		set  map[string]string
		want error
	}{
		{map[string]string{"--mark-shap": "1"}, errThemeToken},                 // not declared: stored, and changing nothing
		{map[string]string{"--mark-shape": "url(https://x/y)"}, errThemeToken}, // the beacon
		{map[string]string{"--mark-shape": "1;} body{display:none"}, errThemeToken},
		{map[string]string{"mark-shape": "1"}, errThemeToken}, // not a custom property
	} {
		if err := a.setUIThemeTokens(c.set); !errors.Is(err, c.want) {
			t.Fatalf("%v was not refused as %v: %v", c.set, c.want, err)
		}
		if after, _ := os.ReadFile(path); string(after) != string(before) {
			t.Fatalf("a refused change touched the file: %v", c.set)
		}
	}
	// A file near the ceiling, compact: laid out with the change it would
	// pass it. The change is refused and the file is left as it was.
	big := map[string]string{"--accent": "#7cc4ff"}
	for i := 0; ; i++ {
		raw, _ := json.Marshal(map[string]any{"v": 1, "note": "kept", "tokens": big})
		if len(raw) > maxUIThemeBytes-400 {
			break
		}
		big[fmt.Sprintf("--t%03d", i)] = strings.Repeat("a", 190)
	}
	compact, _ := json.Marshal(map[string]any{"v": 1, "note": "kept", "tokens": big})
	if len(compact) > maxUIThemeBytes {
		t.Fatalf("rig: the compact file is %d bytes, over the ceiling", len(compact))
	}
	if err := os.WriteFile(path, compact, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := a.setUIThemeTokens(map[string]string{"--mark-shape": "1"}); !errors.Is(err, errThemeToken) {
		t.Fatalf("a change that would lay the file out past its ceiling was not refused: %v", err)
	}
	if after, _ := os.ReadFile(path); string(after) != string(compact) {
		t.Fatal("a change refused for the file's size touched the file")
	}
	mid := `{"v":1,"tokens":{`
	if err := os.WriteFile(path, []byte(mid), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := a.setUIThemeTokens(map[string]string{"--mark-shape": "1"}); !errors.Is(err, errThemeFile) {
		t.Fatalf("a file mid-edit was not refused as such: %v", err)
	}
	if after, _ := os.ReadFile(path); string(after) != mid {
		t.Fatalf("a file mid-edit was overwritten: %s", after)
	}
}
