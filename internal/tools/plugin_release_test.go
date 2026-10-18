package tools

import (
	"errors"
	"slices"
	"testing"
)

type pluginReleaser interface {
	ReleasePlugin(string) error
	PluginSelection(string) (bool, int)
}

func pluginSelected(api pluginReleaser, id string) bool {
	selected, _ := api.PluginSelection(id)
	return selected
}

func pluginReleaseAPI(t *testing.T, r *Registry) pluginReleaser {
	t.Helper()
	api, ok := any(r).(pluginReleaser)
	if !ok {
		t.Fatal("registry has no plugin release control")
	}
	return api
}

func TestPluginReleasePreservesOtherAndAbsentChoices(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	api := pluginReleaseAPI(t, r)
	names := primaryPlugin(t, r, "org.example.notes")
	rival := primaryPlugin(t, r, "org.example.notes.extra")
	absent := StandingSeat{Name: "pl_org_example_notes_absent", Print: "preserved-absent"}
	var saved []StandingSeat
	r.SetStandingOffer([]StandingSeat{absent}, func(s []StandingSeat) error { saved = s; return nil })
	for _, n := range []string{rival[0], names[0], names[2], rival[1]} {
		if _, err := r.Offer(n); err != nil {
			t.Fatal(err)
		}
	}
	if !pluginSelected(api, "org.example.notes") {
		t.Fatal("partial non-primary choice not selected")
	}
	before := slices.Clone(saved)
	if err := api.ReleasePlugin("org.example.notes"); err != nil {
		t.Fatal(err)
	}
	want := []StandingSeat{before[0], before[1], before[4]}
	if !slices.Equal(saved, want) || !slices.Equal(r.Offered(), rival[:2]) || pluginSelected(api, "org.example.notes") {
		t.Fatalf("release changed unrelated choices: saved=%v active=%v", saved, r.Offered())
	}
	if err := api.ReleasePlugin("org.example.notes"); err != nil || !slices.Equal(saved, want) {
		t.Fatalf("repeat changed choices: %v", err)
	}
	if err := api.ReleasePlugin("org.example"); err == nil {
		t.Fatal("prefix released plugin")
	}
	restarted := NewRegistry(t.TempDir(), nil, Timeouts{})
	restarted.SetStandingOffer(saved, nil)
	primaryPlugin(t, restarted, "org.example.notes")
	primaryPlugin(t, restarted, "org.example.notes.extra")
	if !slices.Equal(restarted.Offered(), rival[:2]) {
		t.Fatal("release did not survive restart")
	}
}

func TestPluginReleaseIncludesWithheldAndDisabledChoices(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	api := pluginReleaseAPI(t, r)
	names := primaryPlugin(t, r, "org.example.notes")
	r.SetStandingOffer([]StandingSeat{{Name: names[0], Print: "changed-contract"}, {Name: names[1], Print: Fingerprint(r.tools[names[1]])}}, nil)
	r.SetToolEnabled(names[1], false)
	if !pluginSelected(api, "org.example.notes") {
		t.Fatal("withheld choices hidden")
	}
	if err := api.ReleasePlugin("org.example.notes"); err != nil {
		t.Fatal(err)
	}
	if len(r.standing) != 0 || len(r.Offered()) != 0 || pluginSelected(api, "org.example.notes") {
		t.Fatal("withheld or disabled choice retained")
	}
	for _, name := range names {
		r.Deregister(name)
	}
	if err := api.ReleasePlugin("org.example.notes"); err == nil {
		t.Fatal("unregistered plugin accepted")
	}
}

func TestPluginReleaseFailurePreservesSelectionAndRejectsUnavailableNoop(t *testing.T) {
	for _, selected := range []bool{false, true} {
		t.Run(map[bool]string{false: "noop", true: "selected"}[selected], func(t *testing.T) {
			r := NewRegistry(t.TempDir(), nil, Timeouts{})
			api := pluginReleaseAPI(t, r)
			names := primaryPlugin(t, r, "org.example.notes")
			var seats []StandingSeat
			if selected {
				seats = []StandingSeat{{Name: names[0], Print: Fingerprint(r.tools[names[0]])}}
			}
			failure := errors.New("saved choices unavailable")
			r.SetStandingOffer(seats, func([]StandingSeat) error { return failure })
			active := r.Offered()
			if err := api.ReleasePlugin("org.example.notes"); !errors.Is(err, failure) || !slices.Equal(seats, r.standing) || !slices.Equal(active, r.Offered()) {
				t.Fatalf("failed release changed choice: %v", err)
			}
		})
	}
}
