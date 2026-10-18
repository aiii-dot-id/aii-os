package ring

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRing0CannotBeWrittenThroughAnyGeneralPath(t *testing.T) {
	m := NewManager()
	m.Set(Ring0, &RingContent{Level: Ring0, Content: "a constitution of my own"})
	if m.Get(Ring0) != nil {
		t.Fatal("Set must not be able to install Ring 0")
	}
	composed := false
	if err := m.SetFrom(Ring0, func() *RingContent {
		composed = true
		return &RingContent{Level: Ring0, Content: "composed under the lock"}
	}); err == nil || composed || m.Get(Ring0) != nil {
		t.Fatal("SetFrom must not be able to install Ring 0, nor compose one")
	}
	m.SetSection(Ring0, "amendment", "and a section of my own")
	if m.Section(Ring0, "amendment") != "" {
		t.Fatal("SetSection must not be able to author Ring 0 either")
	}
	if m.Ring0IsConstitution() {
		t.Fatal("nothing was installed, so nothing is the constitution")
	}
	// Every other level is untouched by the rule.
	m.Set(Ring3, &RingContent{Level: Ring3, Content: "working truth"})
	if m.GetContent(Ring3) != "working truth" {
		t.Fatal("the rule is Ring 0's alone")
	}
}

// The SAFE posture is installed once, is NOT the constitution, and
// cannot be replaced afterwards.
func TestTheSafePostureIsSealedAndIsNotTheConstitution(t *testing.T) {
	m := NewManager()
	if err := m.SealSafePosture("the platform's own words"); err != nil {
		t.Fatal(err)
	}
	if m.GetContent(Ring0) != "the platform's own words" {
		t.Fatal("the SAFE posture is installed")
	}
	if m.Ring0IsConstitution() {
		t.Fatal("the SAFE posture is NOT the constitution and must never report as one")
	}
	if err := m.SealSafePosture("second thoughts"); err == nil {
		t.Fatal("write-once means write once")
	}
	if err := m.SealConstitution(&RingContent{Content: "x"}, []byte("k")); err == nil {
		t.Fatal("and a sealed Ring 0 is not replaceable by the other door either")
	}
	if m.GetContent(Ring0) != "the platform's own words" {
		t.Fatal("a refused write changes nothing")
	}
}

// The constitution installs only against the key that signed it, and
// only once.
func TestTheConstitutionInstallsOnlyAgainstItsOwnSignature(t *testing.T) {
	m := NewManager()
	if err := m.SealConstitution(nil, []byte("k")); err == nil {
		t.Fatal("nothing to install is not an install")
	}
	rc := &RingContent{Level: Ring0, Content: "the constitution", SigAlg: "unsigned", Signature: ""}
	if err := m.SealConstitution(rc, nil); err == nil {
		t.Fatal("no key means no install: an unverified constitution is not the constitution")
	}
	if err := m.SealConstitution(rc, []byte("not the signing key")); err == nil {
		t.Fatal("a signature that does not verify must refuse")
	}
	if m.Get(Ring0) != nil || m.Ring0IsConstitution() {
		t.Fatal("and nothing is installed after a refusal")
	}
}

// THE GUARD THAT SURVIVES THE NEXT AUTHOR. A general Ring 0 write must
// not reappear anywhere in the tree; the sealed paths are the only way
// in. This reads the source because that is the level the rule lives
// at — a rule enforced only at runtime is one a future call site can
// still be written against.
//
// guard:tree
func TestNoGeneralRing0WriteExistsAnywhereInTheTree(t *testing.T) {
	root := "../.."
	var offenders []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		if strings.Contains(path, "ring0_immutable_test.go") {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		for _, line := range strings.Split(string(b), "\n") {
			t := strings.TrimSpace(line)
			if strings.HasPrefix(t, "//") {
				continue
			}
			if strings.Contains(t, "Set(ring.Ring0") || strings.Contains(t, "SetSection(ring.Ring0") {
				offenders = append(offenders, path+": "+t)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Fatalf("Ring 0 is installed through SealConstitution or SealSafePosture and nothing else:\n  %s", strings.Join(offenders, "\n  "))
	}
}

// The refusal is RETURNED, not only logged: a caller that wrote nothing
// must know it, so it keeps nothing it would keep for a write (the ring
// writer persisted a refused section — plan of record T68).
func TestARing0WriteIsRefusedWithErrRing0Immutable(t *testing.T) {
	m := NewManager()
	if err := m.Set(Ring0, &RingContent{Level: Ring0, Content: "x"}); !errors.Is(err, ErrRing0Immutable) {
		t.Fatalf("Set(Ring0) = %v, want ErrRing0Immutable", err)
	}
	if err := m.SetSection(Ring0, "amendment", "x"); !errors.Is(err, ErrRing0Immutable) {
		t.Fatalf("SetSection(Ring0) = %v, want ErrRing0Immutable", err)
	}
	for level := Ring1; level <= Ring5; level++ {
		if err := m.Set(level, &RingContent{Level: level, Content: "x"}); err != nil {
			t.Fatalf("Set(%v) = %v", level, err)
		}
		if err := m.SetSection(level, "note", "x"); err != nil {
			t.Fatalf("SetSection(%v) = %v", level, err)
		}
	}
	if m.Get(Ring0) != nil || m.Section(Ring0, "amendment") != "" {
		t.Fatal("a refused write writes nothing")
	}
}
