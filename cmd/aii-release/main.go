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
// .
// .
// .
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/updates"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr *os.File) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: aii-release payload|verify|assets [flags]")
		return 2
	}
	switch args[0] {
	case "payload":
		return runPayload(args[1:], stdout, stderr)
	case "verify":
		return runVerify(args[1:], stdout, stderr)
	case "assets":
		return runAssets(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "aii-release: unknown verb %q (payload, verify, assets)\n", args[0])
		return 2
	}
}

// .
// .
// .
func runPayload(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("payload", flag.ContinueOnError)
	fs.SetOutput(stderr)
	artifact := fs.String("artifact", "", "the release archive to sign, or the evidence tarball (required)")
	version := fs.String("version", "", "release version (required)")
	platform := fs.String("platform", "", "target platform (required)")
	arch := fs.String("arch", "", "target arch (required)")
	sourceRev := fs.String("source-rev", "", "the commit this artifact was built from (required)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *artifact == "" || *version == "" || *platform == "" || *arch == "" || *sourceRev == "" {
		fs.Usage()
		return 2
	}
	data, err := os.ReadFile(*artifact)
	if err != nil {
		fmt.Fprintf(stderr, "aii-release: %v\n", err)
		return 1
	}
	out, err := updates.MarshalPayload(updates.ReleasePayload{
		ArchiveHash: updates.ArchiveHash(data),
		Version:     *version,
		Platform:    *platform,
		Arch:        *arch,
		SourceRev:   *sourceRev,
	})
	if err != nil {
		fmt.Fprintf(stderr, "aii-release: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, string(out))
	return 0
}

// .
// .
// .
func runVerify(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	kind := fs.String("kind", "release", "release (an update archive) or evidence (a sealed bundle)")
	artifact := fs.String("artifact", "", "the release archive (required)")
	sig := fs.String("sig", "", "its detached .platform.sig (required)")
	rootPath := fs.String("root", "", "pinned platform_release root envelope (default: the shipped root)")
	trustDir := fs.String("trust-dir", "", "revocation snapshot directory (default: the shipped snapshot)")
	version := fs.String("version", "", "expected version (required)")
	platform := fs.String("platform", "", "expected platform (required)")
	arch := fs.String("arch", "", "expected arch (required)")
	sourceRev := fs.String("source-rev", "", "expected source revision (optional but recommended)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *artifact == "" || *sig == "" || *version == "" || *platform == "" || *arch == "" {
		fs.Usage()
		return 2
	}
	archiveBytes, err := os.ReadFile(*artifact)
	if err != nil {
		fmt.Fprintf(stderr, "aii-release: %v\n", err)
		return 1
	}
	sigBytes, err := os.ReadFile(*sig)
	if err != nil {
		fmt.Fprintf(stderr, "aii-release: %v\n", err)
		return 1
	}
	root, err := packagefmt.PinnedOrShipped(*rootPath, packagefmt.KeyTypePlatformRelease)
	if err != nil {
		fmt.Fprintf(stderr, "aii-release: platform root: %v\n", err)
		return 1
	}
	want := updates.ReleasePayload{Version: *version, Platform: *platform, Arch: *arch, SourceRev: *sourceRev}
	var verr error
	switch *kind {
	case "release":
		verr = updates.VerifyRelease(sigBytes, archiveBytes, root, *trustDir, want)
	case "evidence":
		verr = updates.VerifyEvidence(sigBytes, archiveBytes, root, *trustDir, want)
	default:
		fmt.Fprintf(stderr, "aii-release: -kind must be release or evidence, not %q\n", *kind)
		return 2
	}
	if verr != nil {
		fmt.Fprintf(stderr, "NOT VERIFIED: %v\n", verr)
		return 1
	}
	fmt.Fprintf(stdout, "VERIFIED %s %s %s/%s\n", *kind, *version, *platform, *arch)
	return 0
}

// .
// .
// .
func runAssets(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("assets", flag.ContinueOnError)
	fs.SetOutput(stderr)
	version := fs.String("version", "", "release version (required)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *version == "" {
		fs.Usage()
		return 2
	}
	for _, t := range updates.SupportedTargets() {
		fmt.Fprintln(stdout, updates.AssetName(*version, t.Platform, t.Arch))
	}
	// .
	// .
	for _, t := range updates.BundleTargets() {
		fmt.Fprintln(stdout, updates.BundleAssetName(*version, t.Platform, t.Arch))
	}
	return 0
}
