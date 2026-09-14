package ring

import (
	"os"
	"path/filepath"
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
func TestRing0CannotBeWrittenThroughAnyGeneralPath(t *testing.T) {
	m := NewManager()
	m.Set(Ring0, &RingContent{Level: Ring0, Content: "a constitution of my own"})
	if m.Get(Ring0) != nil {
		t.Fatal("Set must not be able to install Ring 0")
	}
	m.SetSection(Ring0, "amendment", "and a section of my own")
	if m.Section(Ring0, "amendment") != "" {
		t.Fatal("SetSection must not be able to author Ring 0 either")
	}
	if m.Ring0IsConstitution() {
		t.Fatal("nothing was installed, so nothing is the constitution")
	}
	// .
	m.Set(Ring3, &RingContent{Level: Ring3, Content: "working truth"})
	if m.GetContent(Ring3) != "working truth" {
		t.Fatal("the rule is Ring 0's alone")
	}
}

// .
// .
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

// .
// .
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

// .
// .
// .
// .
// .
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
