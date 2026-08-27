package speecheval

import "testing"

// THE EXAMPLE IN THE DOCS IS MACHINE-CHECKED. A template that has
// drifted from the loader teaches the wrong format to whoever copies
// it, and the first they hear of it is a corpus that will not load.
func TestTheDocumentedExampleManifestIsValid(t *testing.T) {
	m, err := LoadManifest("../../docs/voice-corpus/manifest.example.json")
	if err != nil {
		t.Fatalf("the manifest the docs tell people to copy does not load: %v", err)
	}
	if len(m.Vocabulary) == 0 {
		t.Fatal("the example carries no vocabulary, so it teaches a corpus that cannot score domain terms")
	}
	// And every condition that contains speech must have a ceiling —
	// otherwise the example demonstrates the exact contract gap that
	// Violations exists to report.
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
