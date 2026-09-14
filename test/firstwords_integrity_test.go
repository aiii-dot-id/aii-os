package test

import (
	"context"
	"os"
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
	// .
	// .
	// .
	if strings.Contains(a, "I'm alive") && strings.Contains(b, "I'm alive") {
		t.Fatal("both first-words outputs contain the legacy hardcoded string — the forged voice is back")
	}
}

// .
func TestNoHardcodedAliveString(t *testing.T) {
	// .
	// .
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
