package updates

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

func TestAProducedReleaseIsAdmittedByTheVerifier(t *testing.T) {
	dir := t.TempDir()
	archive := releaseArchive(t, []byte("the shipped binary"))
	signer := newTestReleaseSigner(t)
	signer.provisionTrustDir(t, dir)

	want := ReleasePayload{
		ArchiveHash: ArchiveHash(archive),
		Version:     "1.2.3",
		Platform:    "linux",
		Arch:        "amd64",
		SourceRev:   "cafebabe1234",
	}
	// The producer's own rendering — not a hand-built object.
	raw, err := MarshalPayload(want)
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	sig := signer.signPayloadAs(t, ArtifactKindRelease, obj)

	if err := VerifyRelease(sig, archive, signer.root(), dir+"/trust", want); err != nil {
		t.Fatalf("a release produced by the producer was refused by the verifier: %v", err)
	}
}

// The six ways the shell payload was wrong, each on its own, so a
// regression names which one returned.
func TestTheVerifierRefusesEveryWayTheShellPayloadWasWrong(t *testing.T) {
	// Admission binds opaque bytes, not their archive encoding. Fixed input
	// keeps these public vectors identical across ZIP and tar host platforms.
	archive := []byte("the shipped binary")
	vectors := releaseVectors(t)
	root := vectors.ContractRoot
	trust := t.TempDir()
	// Verify genuine signatures and exact contents independently of the
	// admission refusal. A broken signature must not masquerade as a payload
	// defect, especially the deliberately wrong artifact-kind case.
	vector := func(name, kind string, want map[string]any) []byte {
		t.Helper()
		sig := vectors.Signatures[name]
		raw, err := sigenvelope.VerifyPayload(sig, root, kind, crypto.ProfileRoot)
		if err != nil {
			t.Fatalf("fixture %s is not genuinely signed as %s: %v", name, kind, err)
		}
		var got map[string]any
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("fixture %s payload: got %v, want %v", name, got, want)
		}
		return sig
	}
	snapshot := vector("contract_empty_revocations", packagefmt.ArtifactKindRevocationStatus, map[string]any{
		"schema_version": float64(1), "trust_epoch": float64(1), "revoked": []any{},
	})
	if err := os.WriteFile(filepath.Join(trust, "aiii_platform_release_status.json"), snapshot, 0o600); err != nil {
		t.Fatal(err)
	}
	good := ReleasePayload{
		ArchiveHash: ArchiveHash(archive), Version: "1.2.3",
		Platform: "linux", Arch: "amd64", SourceRev: "cafebabe1234",
	}

	for _, c := range []struct {
		name    string
		vector  string
		kind    string
		payload map[string]any
		want    string
	}{
		{
			name:   "the plugin artifact kind instead of the release one",
			vector: "contract_wrong_kind",
			kind:   "plugin.platform_release",
			payload: map[string]any{
				"archive_hash": good.ArchiveHash, "version": good.Version,
				"platform": good.Platform, "arch": good.Arch, "source_rev": good.SourceRev,
			},
			want: "does not verify",
		},
		{
			name:   "platform missing",
			vector: "contract_no_platform",
			kind:   ArtifactKindRelease,
			payload: map[string]any{
				"archive_hash": good.ArchiveHash, "version": good.Version,
				"arch": good.Arch, "source_rev": good.SourceRev,
			},
			want: "cross-platform replay refused",
		},
		{
			name:   "arch missing",
			vector: "contract_no_arch",
			kind:   ArtifactKindRelease,
			payload: map[string]any{
				"archive_hash": good.ArchiveHash, "version": good.Version,
				"platform": good.Platform, "source_rev": good.SourceRev,
			},
			want: "cross-platform replay refused",
		},
		{
			name:   "artifact_kind inside the payload",
			vector: "contract_extra_kind",
			kind:   ArtifactKindRelease,
			payload: map[string]any{
				"artifact_kind": "plugin.platform_release",
				"archive_hash":  good.ArchiveHash, "version": good.Version,
				"platform": good.Platform, "arch": good.Arch, "source_rev": good.SourceRev,
			},
			want: "closed",
		},
		{
			name:   "asset inside the payload",
			vector: "contract_extra_asset",
			kind:   ArtifactKindRelease,
			payload: map[string]any{
				"asset":        "aii-os_1.2.3_linux_amd64.tar.gz",
				"archive_hash": good.ArchiveHash, "version": good.Version,
				"platform": good.Platform, "arch": good.Arch, "source_rev": good.SourceRev,
			},
			want: "closed",
		},
		{
			name:   "the sha256-prefixed hash the shell emitted",
			vector: "contract_prefixed_hash",
			kind:   ArtifactKindRelease,
			payload: map[string]any{
				"archive_hash": "sha256:" + good.ArchiveHash, "version": good.Version,
				"platform": good.Platform, "arch": good.Arch, "source_rev": good.SourceRev,
			},
			want: "mismatch",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			sig := vector(c.vector, c.kind, c.payload)
			err := VerifyRelease(sig, archive, root, trust, good)
			if err == nil {
				t.Fatal("the verifier ACCEPTED a payload the producer must never emit")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("refusal must name the cause %q, got: %v", c.want, err)
			}
		})
	}

	// An EMPTY signature file satisfied the old release script's check.
	if err := VerifyRelease(nil, archive, root, trust, good); err == nil {
		t.Fatal("an empty signature verified — this is exactly what release.sh's presence check accepted")
	}

	// A stale artifact under a valid signature.
	sig := vector("contract_valid", ArtifactKindRelease, map[string]any{
		"archive_hash": good.ArchiveHash, "version": good.Version,
		"platform": good.Platform, "arch": good.Arch, "source_rev": good.SourceRev,
	})
	// A valid control prevents a broken trust fixture from making the
	// stale-archive and wrong-source cases pass for an unrelated reason.
	if err := VerifyRelease(sig, archive, root, trust, good); err != nil {
		t.Fatalf("valid fixture was refused: %v", err)
	}
	if err := VerifyRelease(sig, []byte("a different archive"), root, trust, ReleasePayload{
		Version: good.Version, Platform: good.Platform, Arch: good.Arch,
	}); err == nil {
		t.Fatal("a signature verified against an archive it does not bind")
	}

	// A source revision the candidate did not build.
	if err := VerifyRelease(sig, archive, root, trust, ReleasePayload{
		Version: good.Version, Platform: good.Platform, Arch: good.Arch, SourceRev: "0000000000",
	}); err == nil {
		t.Fatal("a signature binding another commit verified — the candidate is not source-bound")
	}
}

// The asset names the producer writes are the ones the updater searches
// for. They were not: the candidate collected .deb/.exe/.dmg while the
// updater looked for aii-os_<version>_<platform>_<arch>.<ext>, so even a
// correctly signed release was undiscoverable.
func TestTheProducerNamesTheAssetsTheUpdaterSearchesFor(t *testing.T) {
	const version = "1.2.3"
	for _, tgt := range SupportedTargets() {
		got := AssetName(version, tgt.Platform, tgt.Arch)
		if !strings.HasPrefix(got, "aii-os_"+version+"_") {
			t.Errorf("%s/%s asset %q does not carry the version the updater matches on", tgt.Platform, tgt.Arch, got)
		}
		wantExt := ".tar.gz"
		if tgt.Platform == "windows" {
			wantExt = ".zip"
		}
		if !strings.HasSuffix(got, wantExt) {
			t.Errorf("%s/%s asset %q does not end in %s", tgt.Platform, tgt.Arch, got, wantExt)
		}
		if SigAssetName(got) != got+".platform.sig" {
			t.Errorf("signature asset name for %q is wrong", got)
		}
	}
	if len(SupportedTargets()) == 0 {
		t.Fatal("a candidate with no required targets can never be incomplete, which makes the inventory check meaningless")
	}
}
