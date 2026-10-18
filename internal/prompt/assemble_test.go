package prompt

import "testing"

// The cache seam is the stable prefix of the system text, in declared
// order: it ends at the first section that changes per turn (Volatile) or
// was folded this compose (its bytes differ from last turn's), counts the
// "\n\n" joiners between the sections it spans, and never counts an empty
// section, a block section or the closing. Compose and the gate share this
// one assembly.
func TestAssembleEndsTheSeamAtTheFirstChangingSection(t *testing.T) {
	a := Section{Name: "A", Content: "AAAA", Source: "ring0"}
	b := Section{Name: "B", Content: "BBB", Source: "ring2", Elastic: true}
	folded := b
	folded.Folded = true
	volatile := Section{Name: "V", Content: "VV", Source: "ring3", Elastic: true, Volatile: true}
	empty := Section{Name: "E", Source: "ring1"}
	block := Section{Name: "T", Content: "TT", Source: "turn", Turn: true}
	closing := Section{Name: "Contract", Content: "\n\nCC", Source: "contract"}
	for _, tc := range []struct {
		name     string
		sections []Section
		text     string
		block    string
		seam     int
	}{
		{"all stable", []Section{a, empty, b}, "AAAA\n\nBBB", "", 4 + 2 + 3},
		{"volatile seals", []Section{a, b, volatile}, "AAAA\n\nBBB\n\nVV", "", 4 + 2 + 3},
		{"folded seals", []Section{a, folded, volatile}, "AAAA\n\nBBB\n\nVV", "", 4},
		{"block and closing stay out", []Section{a, block, b, closing}, "AAAA\n\nBBB\n\nCC", "TT", 4 + 2 + 3},
		{"stable after a seal stays out", []Section{a, volatile, b}, "AAAA\n\nVV\n\nBBB", "", 4},
	} {
		text, blk, seam := assemble(tc.sections, nil, map[string]bool{"turn": true}, budgetRoute)
		if text != tc.text || blk != tc.block || seam != tc.seam {
			t.Errorf("%s: assemble = (%q, %q, %d), want (%q, %q, %d)", tc.name, text, blk, seam, tc.text, tc.block, tc.seam)
		}
	}
}
