package app

import (
	"bytes"
	_ "embed"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"os"
	"path/filepath"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
var (
	//go:embed SKILLS.md
	skillsMD []byte
)

// .
const skillsFileName = "SKILLS.md"

// .
// .
// .
const skillsStampPrefix = "describes-build: "

// .
// .
// .
// .
// .
// .
func (a *App) skillsPath() string {
	dir := filepath.Dir(a.cfg.SourcePath)
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	return filepath.Join(dir, skillsFileName)
}

// .
// .
const skillsStampMarker = "{BUILD_STAMP}"

// .
// .
// .
func skillsTemplate(stamp string) []byte {
	if !bytes.Contains(skillsMD, []byte(skillsStampPrefix+skillsStampMarker)) {
		// .
		// .
		logsink.Warn("seed.refusal", "seed: template lacks the describes-build marker — seeding verbatim")
		return skillsMD
	}
	return bytes.Replace(skillsMD, []byte(skillsStampPrefix+skillsStampMarker), []byte(skillsStampPrefix+stamp), 1)
}

// .
// .
// .
// .
// .
// .
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

// .
// .
// .
// .
// .
// .
// .
// .
// .
var skillsShippedSeeds = []string{
	"8fef1b341a31e317e6d7fd6233d0f04ca319797b0878735800f2f14edaf436e3",
	"e0d3e3e0965be6face42f02a356bc82566159b52d60dfbd2aef31db79df6d434",
	"8e3e681353f05dcaccf492be9c985d534d79925baf6a7af9877c2b6dd41f3a94",
	"cdd7e67f3d400a332d3e5f525c9af4656807c6d309c1e083d253d0818adedd55",
	"8968a8b9242152bf584bba2865979f7112312fb903bcba2450b54541d57b61ae",
	"ce6fee83cbc6970314823ea291e89169905e80e430122fa2c30ced2d0deeac86",
	"5fc0e4db266dfa0a1aa46a2bacd57f2cc6ec4095424d554210765c22fdf0b8ba",
	"248f1d88cad97c9feddea474e1216677ddff6f27a0c0aac731695324bc7d82fd",
	"2a81e74d09d4c303860b17df9c67e00aa6b501d6b09d22ccf6946c20846ee6a8",
	"b1a885ef83dbd9123462f66d2c2ec3e004aab5e5c2942478da0c0a1a6ba3cd57",
	"4b007fc15b8ae2f3da9bc8d4e3e1f1c9a078d688282d9b8aeb04bf9d209e792c",
	"623f38dfebf98f47f27e34791ae561df7ea8ba277067e39c8a0c382e9fa14874",
	"11f4e5248567de29ee6a07922e078fe71ff30d9985d78f878e2193ecaab3c035",
	// .
	// .
	// .
	"05b904f0de81854c166e9de7ca2e4f448546b1aade102a79a68122ee4c080395",
	"cee1c1cd88e57f8685ba24e2820b0cac02fc97adc5af549971bcb59de430c818",
	"688b91c259d1017c5f0af28fe0a78c8358c4f79687e6335919f67a493121c132",
	"96c9106cbbfb5f9bb7ff4f9e2d8cdaa9f8f3ac969a8cb9fd299de4564a72fa99",
	"4decccbc975b8df69dc6378a7b490f494c568f81fa1394f372fbc36d382b6ace",
	"a5427e5a9596c2b7d33ac35e6587e568ce90b3467c1841d406101c031b773be8",
	"a352d34bdb1ea2de5da43966ac70ba44f23213d7ef43d402b5e3a27a5d67899f",
	"faaa1dfc95eb79aa8306804d1bf9dda887cf80f799fd4f1ea3cf2247c5e19585",
	"112849c716f89ae16649da6a9010d740ff8f543362cbd8be02766a559c869ece",
}

// .
// .
// .
// .
func (a *App) seedSkillsDoc() {
	path := a.skillsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		logsink.Warn("seed.error", "seed: mkdir %s: %v", filepath.Dir(path), err)
		return
	}
	seedDoc(path, skillsTemplate(BuildIdentity()), normalizeSkillsStamp, skillsShippedSeeds, "[skills] seed")
}
