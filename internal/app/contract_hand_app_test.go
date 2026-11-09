package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/project"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// The hand that agreed a contract reaches the record and the page (the
// operator, 2026-10-31: record the hand; F-DASH-044). The interaction
// notice of a contract change says which hand wrote it, in an annotation
// beside the notice (store.AnnotationProjectBy) that a plugin's read
// serves, never in its details: 0.1.23 and 0.2.0 decode details strictly
// and refuse a member they do not know, so the notice's details decode
// strictly and hold no hand. The projects frame carries agreed_by and
// agreed_at by name, and nothing for a contract written before the hand
// was kept. Control: the hand written into the details again.
func TestTheHandThatAgreedAContractReachesTheRecordAndThePage(t *testing.T) {
	dir := t.TempDir()
	st, err := store.NewForTest(filepath.Join(dir, "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	a := New(&Config{SourcePath: filepath.Join(dir, "config.json")})
	a.store = st
	a.projects = project.NewManager(filepath.Join(dir, "projects"))
	a.wireProjectInteractionRecorder()

	p, err := a.projects.Create("Alpha", "", "operator", nil, &project.Contract{Outcome: "a thing done"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.projects.Update(p.ID, "identity", "", "", "", nil, &project.Contract{Outcome: "a thing done well"}, nil); err != nil {
		t.Fatal(err)
	}
	// A project whose contract predates the hand: written with no hand.
	old, err := a.projects.Create("Old", "", "operator", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.projects.ApplyPatch(old.ID, "operator", nil, nil, nil, nil, &project.Contract{Outcome: "from before"}, nil); err != nil {
		t.Fatal(err)
	}
	loaded, err := a.projects.Load(old.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Written as an older build wrote it: the manifest with no hand.
	loaded.Contract.AgreedBy, loaded.Contract.AgreedAt = "", ""
	older, err := json.MarshalIndent(loaded.Manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(loaded.Dir, project.ManifestName), older, 0o644); err != nil {
		t.Fatal(err)
	}

	// The record: each notice names the hand that wrote its contract.
	page, err := st.QueryInteractions(t.Context(), interaction.Query{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	var hands []string
	for _, row := range page.Rows {
		if row.Details.Project == nil {
			continue
		}
		var hand struct {
			By string `json:"by"`
		}
		if raw, ok := row.Annotations[store.AnnotationProjectBy]; ok {
			if err := json.Unmarshal(raw, &hand); err != nil || hand.By == "" {
				t.Fatalf("a notice's hand %s does not read: %v", raw, err)
			}
		}
		hands = append(hands, hand.By+":"+row.Details.Project.After.Outcome)
	}
	// (A creation with no contract records a notice with no hand and no
	// words, as it did before the hand was kept.)
	if got := strings.Join(hands, "|"); got != "operator:a thing done|identity:a thing done well|:|operator:from before" {
		t.Fatalf("the record's notices name %q", got)
	}

	// Each notice's details hold no hand, and decode strictly as every
	// release reads them.
	rows, err := st.DB().Query(`SELECT details FROM conversations WHERE kind='notice' AND json_extract(details,'$.project') IS NOT NULL`)
	if err != nil {
		t.Fatal(err)
	}
	notices := 0
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		notices++
		var d struct {
			Project map[string]json.RawMessage `json:"project"`
		}
		if err := json.Unmarshal([]byte(raw), &d); err != nil {
			t.Fatal(err)
		}
		if _, has := d.Project["by"]; has {
			t.Errorf("a notice's details carry the hand: %s", raw)
		}
		var strict interaction.Details
		if err := canonicaljson.DecodeStrict([]byte(raw), &strict); err != nil {
			t.Errorf("a notice's details do not decode strictly: %v (%s)", err, raw)
		}
	}
	rows.Close()
	if notices != 4 {
		t.Fatalf("%d contract notices, want 4", notices)
	}

	// The page: the frame carries the hand by name, and none for the old contract.
	states, err := a.projectsState()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(states)
	var frame []map[string]any
	if err := json.Unmarshal(raw, &frame); err != nil {
		t.Fatal(err)
	}
	byName := map[string]map[string]any{}
	for _, s := range frame {
		byName[s["name"].(string)] = s["contract"].(map[string]any)
	}
	if byName["Alpha"]["agreed_by"] != "identity" || byName["Alpha"]["agreed_at"] == nil || byName["Alpha"]["agreed_at"] == "" {
		t.Fatalf("Alpha's contract on the wire: %v", byName["Alpha"])
	}
	if _, has := byName["Old"]["agreed_by"]; has {
		t.Fatalf("a contract from before the hand must say nothing about it: %v", byName["Old"])
	}
}
