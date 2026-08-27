package main

// aii plugin devsign — the T3 dev-packaging and signing verb (the "Go
// v1.27 signing mechanism": internal/crypto's stdlib ML-DSA-87 plus the
// SLH-DSA half of the root profile). Two modes, one packager:
//
//   EPHEMERAL (default): generate a throwaway in-memory dev platform
//   root, build the package from the staging dir, sign the hash pair,
//   emit pkg + the pubkey envelope to pin as plugins.platform_root +
//   the root's EMPTY revocation snapshot (PLUGIN_REVOCATION_DESIGN §1:
//   a ceremony that skips the snapshot mints a tier nothing verifies).
//   No secret ever touches disk; each run mints a fresh root — dev
//   roots are throwaway by doctrine. A dev root is NOT platform trust:
//   it verifies only on hosts that pin the emitted envelope.
//
//   CEREMONY (the real path, operator-suggested 2026-08-19): the C
//   stack's ai3-bundle tool is the reference signer for this exact
//   envelope grammar. Phase 1: -payload-out writes the closed
//   {package_hash, manifest_hash} object for the AIII platform signing
//   authority to sign — wherever AIII operates it; no host is part of
//   this design
//   (ai3-bundle -artifact-kind plugin.platform_release -profile
//   AIII-PQ-SIGNATURE-V1-ROOT -payload pair.json -private-key <ml>
//   -private-key <slh>). Phase 2: -attach-sig assembles the package
//   with the returned envelope and self-verifies against -root. Keys
//   never touch this tool; the payload/envelope files are the seam,
//   per the air-gap doctrine.
//
// Staging layout: <dir>/devsign.json (the spec below) + <dir>/install-root/**.
// The manifest is GENERATED with honest hashes (artifact_hash and
// schema_hash from the actual staged bytes); the built package is
// self-verified through internal/packagefmt before the verb reports
// success — the verifier is the only judge of what was produced.
//
// This verb is the interim T3 dev packager until build-order step 8;
// the SDK's aii-plugin handles the T0/T1 author lanes.

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

// devsignSpec is the staging-side declaration (devsign.json): identity
// plus the manifest shapes whose hashes the packager derives from the
// staged bytes.
type devsignSpec struct {
	ID         string `json:"id"`
	Version    string `json:"version"`
	Interfaces []struct {
		ID         string   `json:"id"`
		Version    int      `json:"version"`
		SchemaFile string   `json:"schema_file"`
		Methods    []string `json:"methods"`
	} `json:"interfaces"`
	Variants []struct {
		ID               string   `json:"id"`
		Platform         string   `json:"platform"`
		Arch             string   `json:"arch"`
		Topology         string   `json:"topology"`
		Runtime          string   `json:"runtime"`
		Profile          string   `json:"profile"`
		Entrypoint       string   `json:"entrypoint"`
		Capabilities     []string `json:"capabilities,omitempty"`
		RequiresRequired []string `json:"requires_required,omitempty"`
		RequiresOptional []string `json:"requires_optional,omitempty"`
	} `json:"variants"`
	CapabilityEnvelope []string `json:"capability_envelope,omitempty"`
}

func runDevsign(args []string, stdout, stderr io.Writer) int {
	fs2 := flag.NewFlagSet("devsign", flag.ContinueOnError)
	fs2.SetOutput(stderr)
	staging := fs2.String("staging", "", "staging dir: devsign.json + install-root/**")
	out := fs2.String("o", "", "output .aiiospkg path")
	rootOut := fs2.String("root-out", "", "ephemeral mode: write the dev platform root envelope here (pin as plugins.platform_root)")
	statusOut := fs2.String("status-out", "", "ephemeral mode: write the empty signed revocation snapshot here (default: aiii_platform_release_status.json beside -root-out; install into <data>/trust/)")
	payloadOut := fs2.String("payload-out", "", "ceremony phase 1: write the {package_hash, manifest_hash} payload for ai3-bundle and stop")
	attachSig := fs2.String("attach-sig", "", "ceremony phase 2: platform.sig envelope produced by ai3-bundle")
	rootIn := fs2.String("root", "", "ceremony phase 2: the pinned platform_release root envelope to self-verify against")
	statusIn := fs2.String("status", "", "ceremony phase 2: the ceremony-signed platform revocation snapshot (T3 verifies only with its root's snapshot installed)")
	if err := fs2.Parse(args); err != nil {
		return 2
	}
	if *staging == "" {
		fmt.Fprintln(stderr, "usage: aii plugin devsign -staging <dir> [-o pkg.aiiospkg -root-out root.pub.json] | [-payload-out pair.json] | [-attach-sig sig.json -root root.pub.json -o pkg.aiiospkg]")
		return 2
	}

	spec, files, err := loadStaging(*staging)
	if err != nil {
		fmt.Fprintf(stderr, "devsign: %v\n", err)
		return 1
	}
	var ifaces []packagetest.InterfaceSpec
	for _, i := range spec.Interfaces {
		ifaces = append(ifaces, packagetest.InterfaceSpec{ID: i.ID, Version: i.Version, SchemaFile: i.SchemaFile, Methods: i.Methods})
	}
	var variants []packagetest.VariantSpec
	for _, v := range spec.Variants {
		variants = append(variants, packagetest.VariantSpec{
			ID: v.ID, Platform: v.Platform, Arch: v.Arch, Topology: v.Topology,
			Runtime: v.Runtime, Profile: v.Profile, Entrypoint: v.Entrypoint,
			Capabilities: v.Capabilities, RequiresRequired: v.RequiresRequired, RequiresOptional: v.RequiresOptional,
		})
	}
	var extra map[string]interface{}
	if len(spec.CapabilityEnvelope) > 0 {
		extra = map[string]interface{}{"capability_envelope": spec.CapabilityEnvelope}
	}
	manifest := packagetest.BuildManifestJSON(spec.ID, spec.Version, ifaces, variants, files, extra)
	pair := map[string]string{
		"package_hash":  packagetest.ReferencePackageHash(files),
		"manifest_hash": packagetest.ReferenceManifestHash(manifest),
	}

	// Ceremony phase 1: hand the exact closed payload to the signer and
	// stop — the package is rebuilt deterministically in phase 2 (the
	// canonical writer is a pure function of the staged bytes).
	if *payloadOut != "" {
		raw, _ := json.Marshal(pair)
		if err := os.WriteFile(*payloadOut, raw, 0o644); err != nil {
			fmt.Fprintf(stderr, "devsign: write payload: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "PAYLOAD %s\npackage_hash %s\nmanifest_hash %s\nsign with: ai3-bundle -artifact-kind plugin.platform_release -profile AIII-PQ-SIGNATURE-V1-ROOT -payload %s -private-key <ml> -private-key <slh>\n",
			*payloadOut, pair["package_hash"], pair["manifest_hash"], *payloadOut)
		fmt.Fprintf(stdout, "ceremony reminder: the platform root's EMPTY revocation snapshot (artifact-kind plugin.revocation_status, payload {\"schema_version\":1,\"trust_epoch\":1,\"revoked\":[]}) must exist or no T3 ever verifies — pass it to phase 2 as -status\n")
		return 0
	}

	if *out == "" {
		fmt.Fprintln(stderr, "devsign: -o is required to build a package")
		return 2
	}

	var sigBytes, statusBytes []byte
	var verifyRoot *sigenvelope.PublicKeyEnvelope
	if *attachSig != "" {
		// Ceremony phase 2: the envelope came from ai3-bundle; the pinned
		// root the operator supplies is what activation will pin too.
		if *rootIn == "" {
			fmt.Fprintln(stderr, "devsign: -attach-sig requires -root (the platform_release root to verify against)")
			return 2
		}
		if *statusIn == "" {
			// T3 verifies only when the platform root's snapshot exists
			// (fail-closed per tier) — a ceremony without one produced a
			// root whose tier can never verify, which phase 2 must say
			// now, not activation later.
			fmt.Fprintln(stderr, "devsign: -attach-sig requires -status (the ceremony-signed empty revocation snapshot; without it no T3 verifies)")
			return 2
		}
		sigBytes, err = os.ReadFile(*attachSig)
		if err != nil {
			fmt.Fprintf(stderr, "devsign: read signature: %v\n", err)
			return 1
		}
		statusBytes, err = os.ReadFile(*statusIn)
		if err != nil {
			fmt.Fprintf(stderr, "devsign: read status: %v\n", err)
			return 1
		}
		verifyRoot, err = packagefmt.LoadPinnedRoot(*rootIn)
		if err != nil {
			fmt.Fprintf(stderr, "devsign: root: %v\n", err)
			return 1
		}
	} else {
		// Ephemeral mode: a fresh throwaway dev root per run — and its
		// empty revocation snapshot, because a root ceremony that skips
		// the snapshot mints a tier nothing can verify (design §1).
		role, rerr := packagetest.NewRole(fmt.Sprintf("aiii_dev_platform_%d", time.Now().UTC().Unix()), packagetest.KeyTypePlatformRelease)
		if rerr != nil {
			fmt.Fprintf(stderr, "devsign: dev root: %v\n", rerr)
			return 1
		}
		sigBytes, err = role.Sign(packagetest.ArtifactKindPlatformSig, pair)
		if err != nil {
			fmt.Fprintf(stderr, "devsign: sign: %v\n", err)
			return 1
		}
		statusBytes, err = role.SignRevocationStatus(1, nil)
		if err != nil {
			fmt.Fprintf(stderr, "devsign: status snapshot: %v\n", err)
			return 1
		}
		verifyRoot = role.Env
		if *rootOut != "" {
			envRaw, _ := json.MarshalIndent(role.Env, "", "  ")
			if err := os.WriteFile(*rootOut, envRaw, 0o644); err != nil {
				fmt.Fprintf(stderr, "devsign: write root: %v\n", err)
				return 1
			}
			if *statusOut == "" {
				*statusOut = filepath.Join(filepath.Dir(*rootOut), platformStatusFileName())
			}
		}
		if *statusOut != "" {
			if err := os.WriteFile(*statusOut, statusBytes, 0o644); err != nil {
				fmt.Fprintf(stderr, "devsign: write status: %v\n", err)
				return 1
			}
		}
	}

	pkg := packagetest.Build(packagetest.PackageSpec{
		Root: spec.ID + "-" + spec.Version, Manifest: manifest, InstallFiles: files,
		Signatures: map[string][]byte{packagetest.SigFilePlatformSig: sigBytes},
	})
	if err := os.WriteFile(*out, pkg, 0o644); err != nil {
		fmt.Fprintf(stderr, "devsign: write package: %v\n", err)
		return 1
	}

	// Self-verify through the real verifier — the only judge. The
	// snapshot rides through the SAME loader activation uses (a scratch
	// trust dir), so what devsign proves is what the runtime will see.
	roots := packagefmt.TrustRoots{PlatformRelease: verifyRoot}
	roots.Revocation, err = loadStatusSetForVerify(statusBytes, roots)
	if err != nil {
		fmt.Fprintf(stderr, "devsign: status snapshot staging: %v\n", err)
		return 1
	}
	res, err := packagefmt.VerifyFile(*out, roots)
	if err != nil {
		fmt.Fprintf(stderr, "devsign: built package does NOT verify: %v\n", err)
		return 1
	}
	if res.Tier != packagefmt.TierT3 {
		fmt.Fprintf(stderr, "devsign: built package verified %s, want T3\n", res.Tier)
		return 1
	}
	fmt.Fprintf(stdout, "SIGNED T3 %s %s\npackage %s\npackage_hash %s\nmanifest_hash %s\n", spec.ID, spec.Version, *out, res.PackageHash, res.ManifestHash)
	if *rootOut != "" && *attachSig == "" {
		fmt.Fprintf(stdout, "pin as plugins.platform_root: %s (DEV root — throwaway, not platform trust)\n", *rootOut)
	}
	if *statusOut != "" && *attachSig == "" {
		fmt.Fprintf(stdout, "install as <data>/trust/%s: %s (empty revocation snapshot — without it no T3 verifies)\n", platformStatusFileName(), *statusOut)
	}
	return 0
}

// platformStatusFileName reads the platform root's canonical status
// filename from THE domain table — one source, never restated here.
func platformStatusFileName() string {
	for _, d := range packagefmt.RevocationDomains() {
		if d.RootKeyType == packagetest.KeyTypePlatformRelease {
			return d.FileName
		}
	}
	return "" // unreachable: the table is fixed contract data
}

// loadStatusSetForVerify stages one platform snapshot in a scratch
// trust dir and loads it with the production loader (nil guard: the
// packager is stateless — no ledger, no acceptance memory).
func loadStatusSetForVerify(statusBytes []byte, roots packagefmt.TrustRoots) (*packagefmt.RevocationStatusSet, error) {
	dir, err := os.MkdirTemp("", "aii-devsign-trust-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	if err := os.WriteFile(filepath.Join(dir, platformStatusFileName()), statusBytes, 0o600); err != nil {
		return nil, err
	}
	return packagefmt.LoadRevocationStatus(dir, roots, nil), nil
}

// loadStaging reads devsign.json and every install-root file.
func loadStaging(dir string) (*devsignSpec, map[string][]byte, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "devsign.json"))
	if err != nil {
		return nil, nil, fmt.Errorf("read devsign.json: %w", err)
	}
	var spec devsignSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return nil, nil, fmt.Errorf("parse devsign.json: %w", err)
	}
	if spec.ID == "" || spec.Version == "" || len(spec.Variants) == 0 {
		return nil, nil, fmt.Errorf("devsign.json needs id, version, and at least one variant")
	}
	rootDir := filepath.Join(dir, "install-root")
	files := map[string][]byte{}
	err = filepath.WalkDir(rootDir, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(rootDir, p)
		if rerr != nil {
			return rerr
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		files[filepath.ToSlash(rel)] = b
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("read install-root: %w", err)
	}
	if len(files) == 0 {
		return nil, nil, fmt.Errorf("install-root is empty")
	}
	return &spec, files, nil
}
