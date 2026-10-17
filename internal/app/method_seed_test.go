package app

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// method_seed_test.go — the Method as a seeded, identity-owned artifact.

func TestMethodShippedSeedsEndWithTheCurrentTemplate(t *testing.T) {
	want := docSeedKey(nil, methodMD)
	if n := len(methodShippedSeeds); n == 0 || methodShippedSeeds[n-1] != want {
		t.Fatalf("METHOD.md changed without updating its answer key.\n"+
			"Append this to methodShippedSeeds (and never remove old entries —\n"+
			"identities holding those versions stop receiving upgrades):\n\t%q", want)
	}
}

func TestSeedMethodDoc(t *testing.T) {
	dir := t.TempDir()
	a := &App{cfg: &Config{SourcePath: filepath.Join(dir, "config.json")}}
	seedPath := filepath.Join(dir, methodFileName)

	// Absent → seeded verbatim.
	a.seedMethodDoc()
	got, err := os.ReadFile(seedPath)
	if err != nil || !bytes.Equal(got, methodMD) {
		t.Fatalf("absent must seed the canonical Method: err=%v equal=%v", err, bytes.Equal(got, methodMD))
	}
	if !bytes.Contains(got, []byte("OCCAM'S RAZOR")) || !bytes.Contains(got, []byte("HONEST")) {
		t.Fatal("the seeded doc is not the Method")
	}

	// Their annotations win — and the platform's newer copy waits beside.
	annotated := append(append([]byte(nil), methodMD...), []byte("\n\n## My margin note\nThe HONEST gate caught me on 08-26.\n")...)
	if err := os.WriteFile(seedPath, annotated, 0o644); err != nil {
		t.Fatal(err)
	}
	a.seedMethodDoc()
	got, _ = os.ReadFile(seedPath)
	if !bytes.Equal(got, annotated) {
		t.Fatal("the identity's margin notes were clobbered")
	}
	sidecar, err := os.ReadFile(seedPath + ".new")
	if err != nil {
		t.Fatal("the annotated doc was not offered the current version beside it")
	}
	if !bytes.Equal(sidecar, methodMD) {
		t.Fatal("the sidecar is not the current Method")
	}
}
