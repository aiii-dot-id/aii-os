package app

import (
	_ "embed"
	"log"
	"os"
	"path/filepath"
)

// method_seed.go — the epistemic method, seeded as the identity's own.
//
// THE METHOD ENTERS THE RUNTIME AS A DOCUMENT, NOT A RITUAL. The
// 2026-08-26 review of "put the Method in the metabolism prompts"
// discarded that design against its own gates: the mechanical halves
// already run (evidence refs, duplicate pushback, NO_CHANGE, capacity
// gating — every facility prompt already says "no change is a valid
// result"), and a prompt that asks a model to certify its own surprise
// harvests claimed surprise — the doc's §VI names that exact failure.
// What was genuinely absent: the Method itself in the identity's
// hands. So it seeds like SKILLS.md — theirs to annotate (edits win),
// upgraded through the answer key, newer versions waiting in a .new
// sidecar when they have written in the margins. Methodology as a
// versioned, identity-owned artifact.
var (
	//go:embed METHOD.md
	methodMD []byte
)

const methodFileName = "METHOD.md"

// methodShippedSeeds is every version of METHOD.md this platform has
// shipped (docSeedKey over raw bytes — no stamp line here), oldest
// first, CURRENT LAST. Same contract, same gate test, same discipline
// as skillsShippedSeeds: edit the doc, append the key, never remove one.
var methodShippedSeeds = []string{
	"de11e86ed497bdca808df97631744cefb401bb55fffd44ca53d721ce977210d6", // 2026-08-26: The Method v2.2 (canonical, verbatim)
}

// seedMethodDoc deploys the Method beside SKILLS.md at the identity
// root: absent → seed; any version WE shipped → upgrade; their
// annotated copy → theirs forever, upgrades waiting beside it.
func (a *App) seedMethodDoc() {
	dir := filepath.Dir(a.cfg.SourcePath)
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("[method] seed: mkdir %s: %v", dir, err)
		return
	}
	seedDoc(filepath.Join(dir, methodFileName), methodMD, nil, methodShippedSeeds, "[method] seed")
}
