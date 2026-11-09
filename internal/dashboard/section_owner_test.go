package dashboard

import (
	"encoding/json"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/sections"
)

// A section says which plugin owns it (F-DASH-045): the frame carries the
// verified manifest id the registry holds for the section, by name, and
// nothing for a section no plugin owns. The page opens a plugin's frame
// from that plugin's own row with it; an id or a title is not evidence.
func TestASectionSaysWhichPluginOwnsIt(t *testing.T) {
	reg := sections.NewRegistry()
	reg.SetSafeSource(func() (string, bool) { return "", false })
	for _, sec := range []*sections.Section{
		{Decl: sections.Decl{ID: "notes", Title: "Notes", Slot: "panel", Entry: "index.html"}, Dir: t.TempDir(), PackageID: "id.aiii.notes"},
		{Decl: sections.Decl{ID: "devsec", Title: "Dev", Slot: "panel", Entry: "index.html"}, Dir: t.TempDir(), Dev: true},
	} {
		if err := reg.Register(sec); err != nil {
			t.Fatal(err)
		}
	}
	s := New("127.0.0.1", 0, &WSHandler{})
	s.SetSections(reg)
	states, reason := s.sectionStates()
	if reason != "" || len(states) != 2 {
		t.Fatalf("states=%d reason=%q", len(states), reason)
	}
	raw, _ := json.Marshal(states)
	var frame []map[string]any
	if err := json.Unmarshal(raw, &frame); err != nil {
		t.Fatal(err)
	}
	byID := map[string]map[string]any{}
	for _, st := range frame {
		byID[st["id"].(string)] = st
	}
	if byID["notes"]["plugin"] != "id.aiii.notes" {
		t.Fatalf("the owned section must name its plugin by its manifest id, got %v", byID["notes"]["plugin"])
	}
	if _, has := byID["devsec"]["plugin"]; has {
		t.Fatalf("a section no plugin owns must say nothing about one: %v", byID["devsec"])
	}
}
