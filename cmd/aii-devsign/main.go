package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

type devsignSpec struct {
	ID      string `json:"id"`
	Version string `json:"version"`

	Title        string `json:"title,omitempty"`
	Description  string `json:"description,omitempty"`
	Publisher    string `json:"publisher,omitempty"`
	License      string `json:"license,omitempty"`
	Homepage     string `json:"homepage,omitempty"`
	PluginFamily string `json:"plugin_family,omitempty"`

	AiiosMinVersion          string `json:"aiios_min_version,omitempty"`
	AiiosMaxExclusiveVersion string `json:"aiios_max_exclusive_version,omitempty"`
	Interfaces               []struct {
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
		fmt.Fprintln(stderr, "usage: aii-devsign -staging <dir> [-o pkg.aiiospkg -root-out root.pub.json] | [-payload-out pair.json] | [-attach-sig sig.json -root root.pub.json -status status.json -o pkg.aiiospkg]")
		return 2
	}

	if refusal := modeRefusal(*payloadOut, *attachSig, *out, *rootOut, *statusOut, *rootIn, *statusIn); refusal != "" {
		fmt.Fprintf(stderr, "devsign: %s\n", refusal)
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
	if err := packagefmt.CheckHostWindow(spec.AiiosMinVersion, spec.AiiosMaxExclusiveVersion); err != nil {
		fmt.Fprintf(stderr, "devsign: %v\n", err)
		return 1
	}
	extra := map[string]interface{}{}
	if len(spec.CapabilityEnvelope) > 0 {
		extra["capability_envelope"] = spec.CapabilityEnvelope
	}
	for key, value := range map[string]string{
		"title": spec.Title, "description": spec.Description, "publisher": spec.Publisher,
		"license": spec.License, "homepage": spec.Homepage, "plugin_family": spec.PluginFamily,
		"aiios_min_version": spec.AiiosMinVersion, "aiios_max_exclusive_version": spec.AiiosMaxExclusiveVersion,
	} {
		if value != "" {
			extra[key] = value
		}
	}
	if len(extra) == 0 {
		extra = nil
	}
	manifest := packagetest.BuildManifestJSON(spec.ID, spec.Version, ifaces, variants, files, extra)
	pair := map[string]string{
		"package_hash":  packagetest.ReferencePackageHash(files),
		"manifest_hash": packagetest.ReferenceManifestHash(manifest),
	}

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

	var sigBytes, statusBytes, rootBytes []byte
	var verifyRoot *sigenvelope.PublicKeyEnvelope
	if *attachSig != "" {

		if *rootIn == "" {
			fmt.Fprintln(stderr, "devsign: -attach-sig requires -root (the platform_release root to verify against)")
			return 2
		}
		if *statusIn == "" {

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

		role, status, rerr := packagetest.NewPlatformRelease(fmt.Sprintf("aiii_dev_platform_%d", time.Now().UTC().Unix()))
		if rerr != nil {
			fmt.Fprintf(stderr, "devsign: dev root: %v\n", rerr)
			return 1
		}
		sigBytes, err = role.Sign(packagetest.ArtifactKindPlatformSig, pair)
		if err != nil {
			fmt.Fprintf(stderr, "devsign: sign: %v\n", err)
			return 1
		}
		statusBytes, verifyRoot = status, role.Env
		rootBytes, _ = json.MarshalIndent(role.Env, "", "  ")
		if *statusOut == "" {
			*statusOut = filepath.Join(filepath.Dir(*rootOut), platformStatusFileName())
		}
	}

	pkg, err := build(packagetest.PackageSpec{
		Root: spec.ID + "-" + spec.Version, Manifest: manifest, InstallFiles: files,
		Signatures: map[string][]byte{packagetest.SigFilePlatformSig: sigBytes},
	})
	if err != nil {
		fmt.Fprintf(stderr, "devsign: %v\n", err)
		return 1
	}

	roots := packagefmt.TrustRoots{PlatformRelease: verifyRoot}
	roots.Revocation, err = loadStatusSetForVerify(statusBytes, roots)
	if err != nil {
		fmt.Fprintf(stderr, "devsign: status snapshot staging: %v\n", err)
		return 1
	}
	res, err := packagefmt.Verify(bytes.NewReader(pkg), roots)
	if err != nil {
		fmt.Fprintf(stderr, "devsign: built package does NOT verify: %v\n", err)
		return 1
	}
	if res.Tier != packagefmt.TierT3 {
		fmt.Fprintf(stderr, "devsign: built package verified %s, want T3\n", res.Tier)
		return 1
	}
	type output struct {
		path, what string
		data       []byte
	}
	writes := []output{{*out, "package", pkg}}
	if rootBytes != nil {
		writes = append(writes, output{*rootOut, "root", rootBytes}, output{*statusOut, "status", statusBytes})
	}
	for _, w := range writes {
		if err := os.WriteFile(w.path, w.data, 0o644); err != nil {
			fmt.Fprintf(stderr, "devsign: write %s: %v\n", w.what, err)
			return 1
		}
	}
	fmt.Fprintf(stdout, "SIGNED T3 %s %s\npackage %s\npackage_hash %s\nmanifest_hash %s\n", spec.ID, spec.Version, *out, res.PackageHash, res.ManifestHash)
	if rootBytes != nil {
		fmt.Fprintf(stdout, "pin as plugins.platform_root: %s (DEV root — throwaway, not platform trust; expires %s, after which a host that pins it runs every plugin quarantined)\n", *rootOut, verifyRoot.ExpiresAt)
		fmt.Fprintf(stdout, "install as <data>/trust/%s: %s (empty revocation snapshot — without it no T3 verifies)\n", platformStatusFileName(), *statusOut)
	}
	return 0
}

func modeRefusal(payloadOut, attachSig, out, rootOut, statusOut, rootIn, statusIn string) string {
	switch {
	case payloadOut != "" && (attachSig != "" || out != "" || rootOut != "" || statusOut != "" || rootIn != "" || statusIn != ""):
		return "-payload-out is ceremony phase 1 and takes only -staging; phase 2 is a second run with -attach-sig"
	case attachSig == "" && (rootIn != "" || statusIn != ""):
		return "-root and -status belong to ceremony phase 2 (-attach-sig); without it the package is signed by a throwaway dev root"
	case attachSig != "" && (rootOut != "" || statusOut != ""):
		return "-root-out and -status-out belong to ephemeral mode; phase 2 verifies against the ceremony's -root and -status"
	case payloadOut == "" && attachSig == "" && rootOut == "":
		return "ephemeral mode needs -root-out: without the dev root it signed with, no host can verify the package"
	}
	return ""
}

func build(spec packagetest.PackageSpec) (pkg []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("the staging dir cannot be packaged: %v", r)
		}
	}()
	return packagetest.Build(spec), nil
}

func platformStatusFileName() string {
	for _, d := range packagefmt.RevocationDomains() {
		if d.RootKeyType == packagetest.KeyTypePlatformRelease {
			return d.FileName
		}
	}
	return ""
}

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

func loadStaging(dir string) (*devsignSpec, map[string][]byte, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "devsign.json"))
	if err != nil {
		return nil, nil, fmt.Errorf("read devsign.json: %w", err)
	}
	var spec devsignSpec
	if err := canonicaljson.DecodeStrict(raw, &spec); err != nil {
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
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file (a link or device would sign bytes from outside the staging dir)", filepath.ToSlash(rel))
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

func main() {
	os.Exit(runDevsign(os.Args[1:], os.Stdout, os.Stderr))
}
