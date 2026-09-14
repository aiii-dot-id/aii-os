package main

// .
// .
// .
// .
// .

import (
	"flag"
	"fmt"
	"io"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

func runPlugin(args []string, stdout, stderr io.Writer) int {
	// .
	// .
	// .
	// .
	if len(args) < 1 {
		pluginUsage(stderr)
		return 2
	}
	switch args[0] {
	case "verify":
		return runPluginVerify(args[1:], stdout, stderr)
	case "catalog":
		return runPluginCatalog(args[1:], stdout, stderr)
	default:
		pluginUsage(stderr)
		return 2
	}
}

func pluginUsage(stderr io.Writer) {
	fmt.Fprintln(stderr, "usage: aii plugin <verify|catalog> ...")
	fmt.Fprintln(stderr, "  verify  [flags] <bundle.aiiospkg>                                verify a bundle offline against the pinned AIII trust roots")
	fmt.Fprintln(stderr, "  catalog -platform-key <root> -catalog-dir <dir> [search <q>]     browse the platform's signed plugin catalog")
}

// .
// .
// .
func runPluginVerify(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("aii plugin verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	certifierPath := fs.String("certifier-key", "", "pinned aiii plugin_publisher_certifier public key envelope (required to prove T1/T2)")
	reviewerPath := fs.String("reviewer-key", "", "pinned aiii plugin_reviewer public key envelope (required to prove T2)")
	platformPath := fs.String("platform-key", "", "pinned aiii platform_release public key envelope (required to prove T3)")
	trustDir := fs.String("trust-dir", "", "trust directory holding the signed revocation-status files (required to prove any signed tier; the runtime default is <data>/trust/)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: aii plugin verify [flags] <bundle.aiiospkg>")
		return 2
	}

	var roots packagefmt.TrustRoots
	var err error
	if roots.PublisherCertifier, err = loadPinnedRoot(*certifierPath, packagefmt.KeyTypePublisherCertifier); err != nil {
		fmt.Fprintf(stderr, "certifier-key: %v\n", err)
		return 1
	}
	if roots.Reviewer, err = loadPinnedRoot(*reviewerPath, packagefmt.KeyTypeReviewer); err != nil {
		fmt.Fprintf(stderr, "reviewer-key: %v\n", err)
		return 1
	}
	if roots.PlatformRelease, err = loadPinnedRoot(*platformPath, packagefmt.KeyTypePlatformRelease); err != nil {
		fmt.Fprintf(stderr, "platform-key: %v\n", err)
		return 1
	}
	// .
	// .
	// .
	// .
	if *trustDir != "" {
		roots.Revocation = packagefmt.LoadRevocationStatus(*trustDir, roots, nil)
		for _, line := range roots.Revocation.Describe() {
			fmt.Fprintln(stderr, line)
		}
	}

	result, err := packagefmt.VerifyFile(fs.Arg(0), roots)
	if err != nil {
		fmt.Fprintf(stderr, "NOT VERIFIED: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "VERIFIED %s\n", result)
	return 0
}

// .
// .
// .
// .
// .
func runPluginCatalog(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("aii plugin catalog", flag.ContinueOnError)
	fs.SetOutput(stderr)
	platformPath := fs.String("platform-key", "", "pinned aiii platform_release public key envelope (required: the catalog is platform_release-signed)")
	catalogDir := fs.String("catalog-dir", "", "local checkout of the plugin-catalog repository (holds "+pluginhost.CatalogFile+" and its .sig)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *catalogDir == "" {
		fmt.Fprintln(stderr, "usage: aii plugin catalog -platform-key <root> -catalog-dir <dir> [search <query>]")
		return 2
	}
	root, err := loadPinnedRoot(*platformPath, packagefmt.KeyTypePlatformRelease)
	if err != nil {
		fmt.Fprintf(stderr, "platform-key: %v\n", err)
		return 1
	}
	cat, err := pluginhost.LoadCatalog(*catalogDir, root)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	query := ""
	if fs.NArg() >= 2 && fs.Arg(0) == "search" {
		query = fs.Arg(1)
	}
	entries := cat.Search(query)
	fmt.Fprintf(stdout, "AII OS plugin catalog v%d (%s) — %d plugin(s)\n", cat.Version, cat.Generated, len(entries))
	for _, e := range entries {
		here := "no build for this host"
		if _, pkg, serr := cat.Select(e.ID); serr == nil && pkg != nil {
			here = fmt.Sprintf("%s/%s %s", pkg.Platform, pkg.Arch, pkg.SHA256)
		}
		fmt.Fprintf(stdout, "  %s %s [%s] — %s\n    %s\n", e.ID, e.Version, e.Tier, e.Summary, here)
	}
	return 0
}

// .
// .
// .
func loadPinnedRoot(path, keyType string) (*sigenvelope.PublicKeyEnvelope, error) {
	return packagefmt.PinnedOrShipped(path, keyType)
}
