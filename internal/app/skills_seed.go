package app

import (
	"bytes"
	_ "embed"
	"log"
	"os"
	"path/filepath"
)

// skillsMD is the capability index for identities WITHOUT the Go
// source: instruments and their non-obvious use, reach and walls, the
// live UI matrix, and the craft of honest self-reporting. Seeded at
// boot into the identity root (beside the operator config file) —
// the same deploy semantics as the operator's own JSON seeds.
// Ruled 2026-08-26: the doc DESCRIBES the binary it shipped with, so
// upgrades must reach identities that never edited it, while an
// identity's edited bytes win forever. Two mechanisms carry that:
// stamp normalization (the describes-build line the seeder itself
// rewrites is erased before any compare), and skillsShippedSeeds —
// the answer key of every version ever shipped, because remembering
// what we shipped is the only way to tell our older seed from their
// edit (see seeddoc.go for the defect this replaced).
var (
	//go:embed SKILLS.md
	skillsMD []byte
)

// skillsFileName is the doc's name in the identity root.
const skillsFileName = "SKILLS.md"

// skillsStampPrefix is the frontmatter key the seeder substitutes
// with the running binary's BuildIdentity() at seed time. The
// template carries the marker; deployed bytes carry the real stamp.
const skillsStampPrefix = "describes-build: "

// skillsPath returns the seeded doc's location: the install root,
// beside the operator's config — the exact directory config.json
// and providers.json deploy into, derived from the config's own
// path so it can never disagree with them. (Not the data dir: the
// doc's audience is the identity at first boot, and the install
// root beside the binary is where their ls looks first.)
func (a *App) skillsPath() string {
	dir := filepath.Dir(a.cfg.SourcePath)
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	return filepath.Join(dir, skillsFileName)
}

// skillsStampMarker is the template placeholder the seeder replaces
// with the running build's BuildIdentity() at seed time.
const skillsStampMarker = "{BUILD_STAMP}"

// skillsTemplate returns the embed with the stamp marker replaced by
// the running build's identity, so the deployed doc states which
// binary it describes.
func skillsTemplate(stamp string) []byte {
	if !bytes.Contains(skillsMD, []byte(skillsStampPrefix+skillsStampMarker)) {
		// Template drifted: ship it verbatim rather than fabricating
		// a stamp line that is not ours to write. Loud, not silent.
		log.Printf("[skills] seed: template lacks the describes-build marker — seeding verbatim")
		return skillsMD
	}
	return bytes.Replace(skillsMD, []byte(skillsStampPrefix+skillsStampMarker), []byte(skillsStampPrefix+stamp), 1)
}

// normalizeSkillsStamp erases the one line the seeder itself rewrites
// (the describes-build frontmatter), so a deployed doc from ANY build
// compares structurally — everything except the stamp — instead of
// byte-wise. Without this, every deployed file differs from every
// template forever (real stamp vs marker), "their edits win" fires
// unconditionally, and upgrades never reach untouched docs.
func normalizeSkillsStamp(b []byte) []byte {
	i := bytes.Index(b, []byte(skillsStampPrefix))
	if i < 0 {
		return b
	}
	rest := b[i+len(skillsStampPrefix):]
	j := bytes.IndexByte(rest, '\n')
	if j < 0 {
		return b[:i]
	}
	return append(b[:i:i], rest[j:]...)
}

// skillsShippedSeeds is every version of SKILLS.md this platform has
// ever shipped, as docSeedKey over the STAMP-NORMALIZED bytes, oldest
// first, CURRENT LAST. A deployed doc matching any entry is ours and
// upgrades; anything else is the identity's and is never touched.
//
// WHEN YOU EDIT SKILLS.md, APPEND THE NEW KEY — and never remove an
// old one, or identities holding that version stop receiving
// upgrades. TestSkillsShippedSeedsEndWithTheCurrentTemplate holds the
// door: it fails, and prints the key to append, until you do.
var skillsShippedSeeds = []string{
	"8fef1b341a31e317e6d7fd6233d0f04ca319797b0878735800f2f14edaf436e3", // 2026-08-26: the first shipped index (1d75294)
	"e0d3e3e0965be6face42f02a356bc82566159b52d60dfbd2aef31db79df6d434", // 2026-08-26: documents the answer key and the .new sidecar
	"8e3e681353f05dcaccf492be9c985d534d79925baf6a7af9877c2b6dd41f3a94", // 2026-08-26: memory reclassified as the ring4 plugin, tools verb added, fsdir jargon replaced with a testable probe
	"cdd7e67f3d400a332d3e5f525c9af4656807c6d309c1e083d253d0818adedd55", // 2026-08-26: the evaluate layer — outcome verdicts, sub-agent verdict line, project lineage.md
	"8968a8b9242152bf584bba2865979f7112312fb903bcba2450b54541d57b61ae", // 2026-08-26: rule 10 (the mint-question) and the seeded METHOD.md
	"ce6fee83cbc6970314823ea291e89169905e80e430122fa2c30ced2d0deeac86", // 2026-08-26: prompt-pointer wired into the tools verb; the Not-yet bullet shrinks
	"5fc0e4db266dfa0a1aa46a2bacd57f2cc6ec4095424d554210765c22fdf0b8ba", // 2026-08-27: Occam evaluation protocol; roles are routes, not authorities
}

// seedSkillsDoc drops the capability index into the identity root at
// boot: absent → seed; any version WE shipped (stamp-normalized,
// checked against skillsShippedSeeds) → re-seed; anything else is the
// identity's edited doc and is never touched again.
func (a *App) seedSkillsDoc() {
	path := a.skillsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Printf("[skills] seed: mkdir %s: %v", filepath.Dir(path), err)
		return
	}
	seedDoc(path, skillsTemplate(BuildIdentity()), normalizeSkillsStamp, skillsShippedSeeds, "[skills] seed")
}
