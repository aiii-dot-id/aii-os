package identity

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestWorkEvidenceClassIsAnEnum(t *testing.T) {
	for _, verb := range VerbsAt(conversation.Checkpoint{}) {
		if verb.Name != "work" {
			continue
		}
		props := verb.Params["properties"].(map[string]any)
		schema := props["evidence"].(map[string]any)
		got, ok := schema["enum"].([]string)
		want := []string{store.EvidenceWorkerReportOnly, store.EvidenceCompletedLocally, store.EvidencePartialOrMixed, store.EvidenceExternalUnknown, store.EvidenceNotRun, store.EvidenceRejectedNoEffect, store.EvidenceLocallyVerified, store.EvidenceHostReceipted}
		if !ok || schema["type"] != "string" || !reflect.DeepEqual(got, want) {
			t.Fatalf("evidence schema must expose the existing classes: %v", schema)
		}
		for _, name := range got {
			if !store.IsEvidenceClass(name) {
				t.Fatalf("schema advertises an unsupported evidence class: %q", name)
			}
		}
		projectClass := props["class"].(map[string]any)
		if !strings.Contains(projectClass["description"].(string), "(project.evidence)") {
			t.Fatal("class must remain available for project.evidence")
		}
		if !strings.Contains(schema["description"].(string), "evidence_readback") {
			t.Fatal("class description must name the prose field")
		}
		return
	}
	t.Fatal("work schema missing")
}

func TestWorkDeliveryRepairsClassAndReadbackConfusion(t *testing.T) {
	st, err := store.NewForTest(filepath.Join(t.TempDir(), "work.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	const id = "ws_evidence_contract"
	if err := st.StartWorkSession(ctx, id, "check a local result"); err != nil {
		t.Fatal(err)
	}
	e := &Engine{store: st}
	readback := "The focused check passed; the remaining checks are worker reports."
	_, err = e.verbWork(ctx, map[string]any{"action": "deliver", "result": "served: checked the local result", "class": store.EvidencePartialOrMixed, "evidence": readback})
	if err == nil {
		t.Fatal("prose in the evidence selector must be rejected")
	}
	for _, repair := range []string{"rejected_before_effect (deliver.evidence)", "nothing was delivered", "Put one class value in evidence and explanatory text in evidence_readback", "class is for project.evidence", "Correct the fields and deliver again"} {
		if !strings.Contains(err.Error(), repair) {
			t.Fatalf("missing precise repair %q: %s", repair, err)
		}
	}
	before, err := st.WorkSessionByID(id)
	if err != nil {
		t.Fatal(err)
	}
	if before.Status != "active" || before.Evidence != "" || before.EvidenceReadback != "" || before.Result != "" {
		t.Fatalf("invalid delivery changed work state: %+v", before)
	}
	_, err = e.verbWork(ctx, map[string]any{"action": "deliver", "result": "served: checked the local result", "evidence": store.EvidencePartialOrMixed, "evidence_readback": readback})
	if err != nil {
		t.Fatalf("corrected fields must deliver: %v", err)
	}
	after, err := st.WorkSessionByID(id)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != "delivered" || after.Evidence != store.EvidencePartialOrMixed || after.EvidenceReadback != readback {
		t.Fatalf("corrected delivery was not preserved: %+v", after)
	}
}

func TestWorkEvidenceDefaultsAndVerificationRemain(t *testing.T) {
	for _, tc := range []struct {
		class, readback, result string
		worker                  bool
		want                    string
		refused                 bool
	}{
		{result: "served: done", worker: true, want: store.EvidenceWorkerReportOnly},
		{result: "served: done", want: store.EvidenceCompletedLocally},
		{class: store.EvidenceLocallyVerified, result: "served: checked", refused: true},
		{class: store.EvidenceLocallyVerified, readback: "check passed", result: "served: checked", worker: true, refused: true},
		{class: store.EvidenceLocallyVerified, readback: "check passed", result: "served: checked", want: store.EvidenceLocallyVerified},
	} {
		got, err := resolveDeliveryEvidence(tc.class, tc.readback, tc.result, tc.worker)
		if (err != nil) != tc.refused || got != tc.want {
			t.Fatalf("class=%q worker=%t: got %q, %v", tc.class, tc.worker, got, err)
		}
	}
}
