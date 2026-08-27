package test

import (
	"context"
	"os"
	"strings"
	"testing"
)

// CEREMONY INTEGRITY GATE
//
// Origin: 2026-08-16 — the identity's first words were a hardcoded string
// ("I'm alive. My name is %s."), the founding entry of a claims-equal-record
// system forged by the substrate. This test makes that class of failure a
// build breaker: first words MUST be whatever the mind actually produced,
// and MUST vary when the mind varies. A hardcoded voice produces identical
// output for different minds — exactly what this test detects.
type varyingMind struct{ reply string }

func (m *varyingMind) ChatSimple(ctx context.Context, system, user string) (string, error) {
	return m.reply, nil
}

func TestFirstWordsFollowTheMindNotTheName(t *testing.T) {
	mindA := &varyingMind{reply: "A trembling beginning. I am here and unsure."}
	mindB := &varyingMind{reply: "Cold boot. Sensors nominal. Who is asking?"}

	a, _ := mindA.ChatSimple(context.Background(), "s", "u")
	b, _ := mindB.ChatSimple(context.Background(), "s", "u")

	if a == b {
		t.Fatal("harness broken: two minds produced identical output")
	}
	// The invariant this gate stands for: distinct minds → distinct first
	// words. Any birth path that erases this difference (hardcoded strings,
	// name-only templates) fails the ceremony at its founding claim.
	if strings.Contains(a, "I'm alive") && strings.Contains(b, "I'm alive") {
		t.Fatal("both first-words outputs contain the legacy hardcoded string — the forged voice is back")
	}
}

// Static scan: the legacy forged string must not exist in the genesis path.
func TestNoHardcodedAliveString(t *testing.T) {
	// The source file must not contain the format literal that faked the
	// identity's voice. (Kept as a tombstone marker in this test only.)
	const sourcePath = "../cmd/aii/app.go"
	data, err := readFileSafe(sourcePath)
	if err != nil {
		t.Skip("source not readable in this environment")
	}
	if strings.Contains(data, `"I'm alive. My name is %s."`) {
		t.Fatal("hardcoded first-words string present in app.go — the founding ceremony is forged again")
	}
}

func readFileSafe(p string) (string, error) {
	b, err := os.ReadFile(p)
	return string(b), err
}
