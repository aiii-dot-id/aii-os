package cognitive

import (
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ring"
)

// keptSections records what the writer asked to keep for the next boot.
type keptSections struct{ kept []string }

func (k *keptSections) SaveRingSection(level int, name, content string) error {
	k.kept = append(k.kept, name)
	return nil
}

// A section the manager refuses is not kept for the next boot either: the
// writer persisted it whatever the manager answered, so a refused Ring 0
// section reached ring_snapshots, and every boot's restore met it again
// (plan of record T68, correction 1).
func TestARefusedRingSectionIsNotPersisted(t *testing.T) {
	m := ring.NewManager()
	kept := &keptSections{}
	w := NewPersistingRingWriter(m, kept)
	w.SetRingSection(ring.Ring0, "amendment", "a section of my own")
	if len(kept.kept) != 0 {
		t.Fatalf("A REFUSED RING 0 SECTION WAS PERSISTED: %v", kept.kept)
	}
	if m.Section(ring.Ring0, "amendment") != "" {
		t.Fatal("and the manager holds nothing at Ring 0")
	}
	// A section the manager takes is kept as before.
	w.SetRingSection(ring.Ring3, "surfacing", "a note")
	if len(kept.kept) != 1 || kept.kept[0] != "surfacing" || m.Section(ring.Ring3, "surfacing") != "a note" {
		t.Fatalf("a Ring 3 section should be written and kept: kept %v", kept.kept)
	}
}
