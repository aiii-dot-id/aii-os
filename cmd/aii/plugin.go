package main

// aii plugin verify — the offline .aiiospkg verifier verb (build-order
// step 2). Thin by the same rule as main: parse flags, load the pinned
// roots the operator names, hand the file to internal/packagefmt, and
// report. No install, no registry, no network — those are later steps
// owned by the daemon path.

import (
	"flag"
	"fmt"
	"io"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

func runPlugin(args []string, stdout, stderr io.Writer) int {
	if len(args) >= 1 && args[0] == "devsign" {
		return runDevsign(args[1:], stdout, stderr)
	}
	if len(args) < 1 || args[0] != "verify" {
		fmt.Fprintln(stderr, "usage: aii plugin verify [flags] <bundle.aiiospkg> | aii plugin devsign -staging <dir> ...")
		fmt.Fprintln(stderr, "  Verifies a plugin bundle offline against the pinned AIII plugin trust roots.")
		return 2
	}

	fs := flag.NewFlagSet("aii plugin verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	certifierPath := fs.String("certifier-key", "", "pinned aiii plugin_publisher_certifier public key envelope (required to prove T1/T2)")
	reviewerPath := fs.String("reviewer-key", "", "pinned aiii plugin_reviewer public key envelope (required to prove T2)")
	platformPath := fs.String("platform-key", "", "pinned aiii platform_release public key envelope (required to prove T3)")
	trustDir := fs.String("trust-dir", "", "trust directory holding the signed revocation-status files (required to prove any signed tier; the runtime default is <data>/trust/)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: aii plugin verify [flags] <bundle.aiiospkg>")
		return 2
	}

	var roots packagefmt.TrustRoots
	var err error
	if roots.PublisherCertifier, err = loadPinnedRoot(*certifierPath); err != nil {
		fmt.Fprintf(stderr, "certifier-key: %v\n", err)
		return 1
	}
	if roots.Reviewer, err = loadPinnedRoot(*reviewerPath); err != nil {
		fmt.Fprintf(stderr, "reviewer-key: %v\n", err)
		return 1
	}
	if roots.PlatformRelease, err = loadPinnedRoot(*platformPath); err != nil {
		fmt.Fprintf(stderr, "platform-key: %v\n", err)
		return 1
	}
	// Revocation snapshots ride the same trust state (nil-safe: without
	// the dir every signed tier is unavailable and the verifier says so
	// — fail closed, loud). The CLI is stateless, so no epoch guard: the
	// anti-rollback high-water mark is the runtime's ledgered memory.
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

// loadPinnedRoot delegates to the shared loader (packagefmt owns it now
// — one validation for the CLI flag and the app's plugins.*_root config
// paths alike; step 4 cut the second caller).
func loadPinnedRoot(path string) (*sigenvelope.PublicKeyEnvelope, error) {
	return packagefmt.LoadPinnedRoot(path)
}
