package app

import (
	"bytes"
	_ "embed"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"os"
	"path/filepath"
	"regexp"
)

var (
	//go:embed SKILLS.md
	skillsMD []byte
)

const skillsFileName = "SKILLS.md"

const skillsStampPrefix = "describes-build: "

func (a *App) skillsPath() string { return a.seededDocPath(skillsFileName) }

const skillsStampMarker = "{BUILD_STAMP}"

func skillsTemplate(stamp string) []byte {
	if !bytes.Contains(skillsMD, []byte(skillsStampPrefix+skillsStampMarker)) {

		logsink.Warn("seed.refusal", "seed: template lacks the describes-build marker — seeding verbatim")
		return skillsMD
	}
	return bytes.Replace(skillsMD, []byte(skillsStampPrefix+skillsStampMarker), []byte(skillsStampPrefix+stamp), 1)
}

func normalizeSkillsStamp(b []byte) []byte {
	i := bytes.Index(b, []byte(skillsStampPrefix))
	if i < 0 {
		return b
	}
	rest := b[i+len(skillsStampPrefix):]
	j := bytes.IndexByte(rest, '\n')
	value := rest
	if j >= 0 {
		value = rest[:j]
	}
	if !skillsStampValue.Match(value) {
		return b
	}
	if j < 0 {
		return b[:i]
	}
	return append(b[:i:i], rest[j:]...)
}

var skillsStampValue = regexp.MustCompile(`^(?:` + regexp.QuoteMeta(skillsStampMarker) + `|unknown|[0-9a-f]{1,12}(?: \(dirty\))?)$`)

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

	"05b904f0de81854c166e9de7ca2e4f448546b1aade102a79a68122ee4c080395",
	"cee1c1cd88e57f8685ba24e2820b0cac02fc97adc5af549971bcb59de430c818",
	"688b91c259d1017c5f0af28fe0a78c8358c4f79687e6335919f67a493121c132",
	"96c9106cbbfb5f9bb7ff4f9e2d8cdaa9f8f3ac969a8cb9fd299de4564a72fa99",
	"4decccbc975b8df69dc6378a7b490f494c568f81fa1394f372fbc36d382b6ace",
	"a5427e5a9596c2b7d33ac35e6587e568ce90b3467c1841d406101c031b773be8",
	"a352d34bdb1ea2de5da43966ac70ba44f23213d7ef43d402b5e3a27a5d67899f",
	"faaa1dfc95eb79aa8306804d1bf9dda887cf80f799fd4f1ea3cf2247c5e19585",
	"112849c716f89ae16649da6a9010d740ff8f543362cbd8be02766a559c869ece",
	"3efed6197e3da21f0fc6167ea21dc73aa7e397d65a6e602700cadaa2ecca8406",
	"9faf7f175725dbba40bf954e4e420527a40a4d13420f02aeb4cc8d442e89f312",
	"aace3e1ec0a60199ca4f85c9874b17becf8921167a64c41d1ef17b24c2b90722",
	"1a50b4664d90871a976756cdfb079b82a6a033b4b90bce40c5f8944e259f3328",
	"f325a980481329ca1f3f231c6f8e61063fec6b9fe98485a2933f96ff29e6fd2e",
	"034b1d4ea7019bdc9edbeec2de8c6b1f44ea29f83f994636d55f6b4b9088b60c",
	"0ee2c49024e7d3085ce4a4c75e29269a13a3cdc35be89245bc85f0b5abba53c5",
	"909dc461d0723ccc616f060c99f0facbb2081acf156dae05bf78ec72b064f734",
	"a247b69b61bad34b8a10dfbb9efe73f540bd8d809287af7c955cd7d568210cf1",
	"57241f25ad3a1c26b691a1f2f59690aa7baa3a2e8e94e91d1160294659dfdf26",
	"37c8682e07a7531320286659ef8afd663d92db3ab38b808956f60cb4a1736673",
	"afcb4be7b3b7b4f16facc414341c2f26f7e5e249082916f0f5a48671cb22c067",
	"355b608af32ff10e6511a649a8af1e2cb21631bd6e804dda9de835c799cae53c",
	"0ce14882bb82a6860bdaeee3f7939a8ef0d7575dabbc686f56b0d1076024e265",
	"e68cbfc2d05f455cd68c895debd53a05664cc4c5864b4f6fbe6c02985ef494fb",
	"2d1b391982f200c895bd5baaa5010371e79eec581a0143ce15d633bc93ff3e53",
	"4ca8b077b620a04c30dce16a57399d00da5e807d6f02dbb85a04742f430109f9",
	"88b25e0dcfd1a6409007c3688720ad651f4630abfd1709cd890473ca01cccb0a",
	"02b9775a38ab0edcdfcbaa6d890c5a5abf04c151f374f5bb404d13a63ddc0596",
	"7dfb3a70bc0ffc85be9cb77c4acf6b81bc7104dcff3aad949d10716e65c1c934",
	"3656477df4d7fcb7e510e4c5a872bc828cf265e16394cfb674ae32a2730db17b",
	"b845ce0385746995bc79e298a2cecd61a8e086f6e89c9b136269704248d9fdb5",
	"bedef1a668ebdebe423c4b1dea8c7c48342db11087d66a240ccac3ca0bef87b7",
	"b06f0081169ab9e2c128d182b55236abd5397c461a5d5e69a981e456958b9f66",
	"dfdc14d721af6f217a447a394ccbd5362f3b7d6271309f5a4499cb530dbc18ef",
	"20a9081f8f53ac96ba65da0fe75453701022afc3e2505e6dacf5277b5b20c443",
	"dfdc14d721af6f217a447a394ccbd5362f3b7d6271309f5a4499cb530dbc18ef",
}

func (a *App) seedSkillsDoc() {
	path := a.skillsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		logsink.Warn("seed.error", "seed: mkdir %s: %v", filepath.Dir(path), err)
		return
	}
	a.bringSeededDocHome(path, normalizeSkillsStamp, skillsShippedSeeds, "[skills] seed")
	seedDoc(path, skillsTemplate(BuildIdentity()), normalizeSkillsStamp, skillsShippedSeeds, a.seedRecordPath(skillsFileName), "[skills] seed")
}
