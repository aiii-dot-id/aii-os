package speecheval

import (
	"os"
	"testing"
)

// .
// .
// .
func TestTheDocumentedExampleManifestIsValid(t *testing.T) {
	const example = "../../docs/voice-corpus/manifest.example.json"
	if _, err := os.Stat(example); os.IsNotExist(err) {
		// .
		// .
		// .
		t.Skip("docs/voice-corpus/manifest.example.json not present in this tree (docs-free export)")
	}
	m, err := LoadManifest(example)
	if err != nil {
		t.Fatalf("the manifest the docs tell people to copy does not load: %v", err)
	}
	if len(m.Vocabulary) == 0 {
		t.Fatal("the example carries no vocabulary, so it teaches a corpus that cannot score domain terms")
	}
	// .
	// .
	// .
	for _, cond := range m.Conditions() {
		if _, ok := m.Contract.MaxWER[cond]; ok {
			continue
		}
		if _, ok := m.Contract.MaxWER[""]; ok {
			continue
		}
		for _, c := range m.Clips {
			if c.Condition == cond && c.Speech {
				t.Errorf("example condition %q has speech clips but no WER ceiling", cond)
				break
			}
		}
	}
}
