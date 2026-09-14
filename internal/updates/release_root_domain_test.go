package updates

import (
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
func TestAReleaseRootOfTheWrongDomainIsRefused(t *testing.T) {
	signer := newTestReleaseSigner(t)
	sig := signer.signReleasePayload(t, releaseArchivePayload{
		ArchiveHash: "0000000000000000000000000000000000000000000000000000000000000000",
		Version:     "9.9.9",
		Platform:    "linux",
		Arch:        "amd64",
		SourceRev:   "domaintest00",
	})

	for _, wrong := range []string{"platform", "reviewer", "plugin_publisher_certifier", "ring0", ""} {
		t.Run("key_type="+wrong, func(t *testing.T) {
			root := *signer.env
			root.KeyType = wrong
			_, err := verifyReleaseSig(sig, &root, "0000000000000000000000000000000000000000000000000000000000000000",
				"9.9.9", "linux", "amd64")
			if err == nil {
				t.Fatalf("a root declaring key_type %q authorised a binary replacement — trust domains are separate keys", wrong)
			}
			if !strings.Contains(err.Error(), "trust domains are separate keys") {
				t.Fatalf("the refusal must name the unmet requirement, got: %v", err)
			}
		})
	}

	// .
	if _, err := verifyReleaseSig(sig, signer.env, "0000000000000000000000000000000000000000000000000000000000000000",
		"9.9.9", "linux", "amd64"); err != nil {
		t.Fatalf("a genuine platform_release root must still verify: %v", err)
	}
}
