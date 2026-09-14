package updates

import (
	"encoding/json"
	"strings"
	"testing"
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
	// .
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

// .
// .
func TestTheVerifierRefusesEveryWayTheShellPayloadWasWrong(t *testing.T) {
	dir := t.TempDir()
	archive := releaseArchive(t, []byte("the shipped binary"))
	signer := newTestReleaseSigner(t)
	signer.provisionTrustDir(t, dir)
	good := ReleasePayload{
		ArchiveHash: ArchiveHash(archive), Version: "1.2.3",
		Platform: "linux", Arch: "amd64", SourceRev: "cafebabe1234",
	}

	for _, c := range []struct {
		name    string
		kind    string
		payload map[string]any
		want    string
	}{
		{
			name: "the plugin artifact kind instead of the release one",
			kind: "plugin.platform_release",
			payload: map[string]any{
				"archive_hash": good.ArchiveHash, "version": good.Version,
				"platform": good.Platform, "arch": good.Arch, "source_rev": good.SourceRev,
			},
			want: "does not verify",
		},
		{
			name: "platform missing",
			kind: ArtifactKindRelease,
			payload: map[string]any{
				"archive_hash": good.ArchiveHash, "version": good.Version,
				"arch": good.Arch, "source_rev": good.SourceRev,
			},
			want: "cross-platform replay refused",
		},
		{
			name: "arch missing",
			kind: ArtifactKindRelease,
			payload: map[string]any{
				"archive_hash": good.ArchiveHash, "version": good.Version,
				"platform": good.Platform, "source_rev": good.SourceRev,
			},
			want: "cross-platform replay refused",
		},
		{
			name: "artifact_kind inside the payload",
			kind: ArtifactKindRelease,
			payload: map[string]any{
				"artifact_kind": "plugin.platform_release",
				"archive_hash":  good.ArchiveHash, "version": good.Version,
				"platform": good.Platform, "arch": good.Arch, "source_rev": good.SourceRev,
			},
			want: "closed",
		},
		{
			name: "asset inside the payload",
			kind: ArtifactKindRelease,
			payload: map[string]any{
				"asset":        "aii-os_1.2.3_linux_amd64.tar.gz",
				"archive_hash": good.ArchiveHash, "version": good.Version,
				"platform": good.Platform, "arch": good.Arch, "source_rev": good.SourceRev,
			},
			want: "closed",
		},
		{
			name: "the sha256-prefixed hash the shell emitted",
			kind: ArtifactKindRelease,
			payload: map[string]any{
				"archive_hash": "sha256:" + good.ArchiveHash, "version": good.Version,
				"platform": good.Platform, "arch": good.Arch, "source_rev": good.SourceRev,
			},
			want: "mismatch",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			sig := signer.signPayloadAs(t, c.kind, c.payload)
			err := VerifyRelease(sig, archive, signer.root(), dir+"/trust", good)
			if err == nil {
				t.Fatal("the verifier ACCEPTED a payload the producer must never emit")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("refusal must name the cause %q, got: %v", c.want, err)
			}
		})
	}

	// .
	if err := VerifyRelease(nil, archive, signer.root(), dir+"/trust", good); err == nil {
		t.Fatal("an empty signature verified — this is exactly what release.sh's presence check accepted")
	}

	// .
	sig := signer.signPayloadAs(t, ArtifactKindRelease, map[string]any{
		"archive_hash": good.ArchiveHash, "version": good.Version,
		"platform": good.Platform, "arch": good.Arch, "source_rev": good.SourceRev,
	})
	if err := VerifyRelease(sig, []byte("a different archive"), signer.root(), dir+"/trust", ReleasePayload{
		Version: good.Version, Platform: good.Platform, Arch: good.Arch,
	}); err == nil {
		t.Fatal("a signature verified against an archive it does not bind")
	}

	// .
	if err := VerifyRelease(sig, archive, signer.root(), dir+"/trust", ReleasePayload{
		Version: good.Version, Platform: good.Platform, Arch: good.Arch, SourceRev: "0000000000",
	}); err == nil {
		t.Fatal("a signature binding another commit verified — the candidate is not source-bound")
	}
}

// .
// .
// .
// .
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
