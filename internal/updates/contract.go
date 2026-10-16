package updates

import (
	"encoding/json"
	"fmt"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
	"github.com/aiii-dot-id/aii-os/internal/vulkancap"
)

type ReleasePayload struct {
	ArchiveHash string `json:"archive_hash"`
	Version     string `json:"version"`
	Platform    string `json:"platform"`
	Arch        string `json:"arch"`
	SourceRev   string `json:"source_rev"`
}

const ArtifactKindRelease = artifactKindReleaseSig

const (
	ArtifactKindEvidence = artifactKindEvidenceSig
	EvidencePlatform     = "evidence"
	EvidenceArch         = "bundle"
)

type Target struct {
	Platform string
	Arch     string
}

func SupportedTargets() []Target {
	return []Target{
		{"linux", "amd64"},
		{"linux", "arm64"},
		{"macos", "arm64"},
		{"windows", "amd64"},
	}
}

func BundleTargets() []Target {
	return []Target{{"macos", "arm64"}}
}

func PackageTargets() []Target {
	return []Target{{"linux", "amd64"}, {"linux", "arm64"}}
}

type ShippedProgram struct {
	Command string
	Name    string
}

const (
	binaryCommand   = "./cmd/aii"
	launcherCommand = "./cmd/aii-app"
	windowsLauncher = "AII OS.exe"
)

func ShippedPrograms(platform string) []ShippedProgram {
	switch platform {
	case "linux":
		return []ShippedProgram{
			{binaryCommand, tarBinary},
			{launcherCommand, "aii-app"},

			{"./cmd/aii-vulkan-probe", vulkancap.Helper},
		}
	case "macos":
		return []ShippedProgram{{binaryCommand, tarBinary}, {launcherCommand, "aii-app"}}
	case "windows":
		return []ShippedProgram{{binaryCommand, zipBinary}, {launcherCommand, windowsLauncher}}
	}
	return nil
}

func ShippedDocuments() []string { return []string{"LICENSE", "THIRD_PARTY_LICENSES"} }

var linuxIconSizes = []int{16, 24, 32, 48, 64, 128, 256, 512}

func ArchiveFiles(platform string) []string {
	var files []string
	for _, p := range ShippedPrograms(platform) {
		files = append(files, p.Name)
	}
	if files == nil {
		return nil
	}
	files = append(files, ShippedDocuments()...)
	if platform == "linux" {
		files = append(files, "share/applications/aii-os.desktop", "share/man/man1/aii.1.gz")
		for _, s := range linuxIconSizes {
			files = append(files, fmt.Sprintf("share/icons/hicolor/%dx%d/apps/aii-os.png", s, s))
		}
	}
	return files
}

func InstalledFrom(platform string, archive []byte) ([]string, error) {
	extract := extractFromTarGz
	if platform == "windows" {
		extract = extractFromZip
	}
	if _, beside, err := extract(platform, archive); err != nil {
		return nil, err
	} else {
		names := []string{ShippedPrograms(platform)[0].Name}
		for _, f := range beside {
			names = append(names, f.name)
		}
		return names, nil
	}
}

func Installs(platform string) []string {
	programs := ShippedPrograms(platform)
	if len(programs) == 0 {
		return nil
	}
	return append([]string{programs[0].Name}, installedBeside(platform)...)
}

func installedBeside(platform string) []string {
	var names []string
	for i, p := range ShippedPrograms(platform) {
		if i > 0 {
			names = append(names, p.Name)
		}
	}
	if platform == "windows" {
		names = append(names, ShippedDocuments()...)
	}
	return names
}

func PackageAssetName(version, arch string) string {
	return fmt.Sprintf("aii-os_%s_%s.deb", version, arch)
}

func BundleAssetName(version, platform, arch string) string {
	return fmt.Sprintf("aii-os-app_%s_%s_%s.zip", version, platform, arch)
}

func AssetName(version, platform, arch string) string {
	ext := "tar.gz"
	if platform == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf("aii-os_%s_%s_%s.%s", version, platform, arch, ext)
}

func SigAssetName(assetName string) string { return assetName + ".platform.sig" }

func MarshalPayload(p ReleasePayload) ([]byte, error) {
	return json.MarshalIndent(p, "", "  ")
}

func VerifyRelease(sigBytes, archiveBytes []byte, root *sigenvelope.PublicKeyEnvelope, trustDir string, want ReleasePayload) error {
	hash := sha256hex(archiveBytes)
	if want.ArchiveHash != "" && want.ArchiveHash != hash {
		return fmt.Errorf("artifact hashes to %s, candidate claims %s", hash, want.ArchiveHash)
	}
	signed, err := verifyReleaseSig(sigBytes, root, hash, want.Version, want.Platform, want.Arch)
	if err != nil {
		return err
	}
	revRoots := packagefmt.ReleaseTrustRoots(trustDir, root)
	if err := packagefmt.CheckReleaseRevocation(revRoots, ArtifactKindRelease, signed); err != nil {
		return err
	}

	var got ReleasePayload
	if err := json.Unmarshal(signed, &got); err != nil {
		return fmt.Errorf("verified payload is unreadable: %w", err)
	}
	if want.SourceRev != "" && got.SourceRev != want.SourceRev {
		return fmt.Errorf("signature binds source_rev %s, candidate built %s", got.SourceRev, want.SourceRev)
	}
	return nil
}

func VerifyEvidence(sigBytes, bundleBytes []byte, root *sigenvelope.PublicKeyEnvelope, trustDir string, want ReleasePayload) error {
	hash := sha256hex(bundleBytes)
	if want.ArchiveHash != "" && want.ArchiveHash != hash {
		return fmt.Errorf("bundle hashes to %s, candidate claims %s", hash, want.ArchiveHash)
	}
	if want.Platform == "" {
		want.Platform = EvidencePlatform
	}
	if want.Arch == "" {
		want.Arch = EvidenceArch
	}
	signed, err := verifySigOfKind(ArtifactKindEvidence, sigBytes, root, hash, want.Version, want.Platform, want.Arch)
	if err != nil {
		return err
	}
	revRoots := packagefmt.ReleaseTrustRoots(trustDir, root)
	if err := packagefmt.CheckReleaseRevocation(revRoots, ArtifactKindEvidence, signed); err != nil {
		return err
	}
	var got ReleasePayload
	if err := json.Unmarshal(signed, &got); err != nil {
		return fmt.Errorf("verified payload is unreadable: %w", err)
	}
	if want.SourceRev != "" && got.SourceRev != want.SourceRev {
		return fmt.Errorf("signature binds source_rev %s, bundle built from %s", got.SourceRev, want.SourceRev)
	}
	return nil
}

func ArchiveHash(archive []byte) string { return sha256hex(archive) }
