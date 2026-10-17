package test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ONE RELEASE AUTHORITY, AND THE SCHEMA WRITTEN ONCE.
//
// Two defects made the release seam unusable, and both are structural
// rather than behavioural — no unit test could see either:
//
//   - packaging/release.sh hand-wrote the signing payload in shell, and
//     every field disagreed with the verifier. A schema written twice is
//     a schema that disagrees with itself.
//   - .github/workflows/release.yml published unsigned archives on a
//     pushed tag, competing with release.sh and creating exactly the
//     unsigned public interval BETA1_CONTRACT forbids.
//
// Pinned as source policy because that is what they are.
func TestTheReleaseScriptDoesNotReproduceTheSigningSchema(t *testing.T) {
	root := repoRoot(t)
	script := readScript(t, filepath.Join(root, "packaging", "release.sh"))
	code := stripComments(script)

	// The payload field names belong to internal/updates. Finding them
	// quoted in shell means the schema has been copied again.
	//
	// source_rev is deliberately NOT in this list: the candidate
	// inventory legitimately carries it as a line key, and the script
	// reads it back with awk. Checking for it flagged that read as a
	// schema copy — an assertion broader than the claim, which is the
	// same mistake this session has been correcting elsewhere. These two
	// have no legitimate use in shell at all.
	for _, field := range []string{`"archive_hash"`, `"artifact_kind"`} {
		if strings.Contains(code, field) {
			t.Errorf("release.sh writes the signing field %s itself — the schema must come from aii-release, or the two halves drift apart exactly as they did", field)
		}
	}
	if !strings.Contains(code, "aii-release") {
		t.Error("release.sh must render payloads through the aii-release tool")
	}
	// attach must VERIFY, not merely observe that a file exists.
	if !strings.Contains(code, "verify -artifact") {
		t.Error("release.sh attach must run the real verification; a presence check accepted an empty signature")
	}
	// An unconditional || true is how a failed draft-create turned into
	// an upload to a public release.
	for _, line := range strings.Split(code, "\n") {
		if strings.Contains(line, "|| true") {
			t.Errorf("release.sh swallows a failure with `|| true`: %s", strings.TrimSpace(line))
		}
	}
}

func TestNoWorkflowCanPublishARelease(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("no workflows directory: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yml") {
			continue
		}
		body := readScript(t, filepath.Join(dir, e.Name()))
		code := stripYAMLComments(body)
		if strings.Contains(code, "action-gh-release") || strings.Contains(code, "gh release create") {
			t.Errorf("%s can publish a GitHub Release — releases are assembled and staged by packaging/release.sh, which signs first and stages a DRAFT", e.Name())
		}
		// A tag trigger plus write permission is the shape that published
		// unsigned archives.
		if strings.Contains(code, "tags:") && strings.Contains(code, "contents: write") {
			t.Errorf("%s triggers on a tag with write permission — that is the unsigned-publish path", e.Name())
		}
	}
}

// stripYAMLComments drops YAML comments so a workflow's PROSE about the
// old behaviour cannot fail a check about its current behaviour.
func stripYAMLComments(s string) string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
