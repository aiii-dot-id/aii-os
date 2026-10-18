package app

import (
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

func TestAnEmbeddingsModelSavedOnTheRunningProviderIsTheBasisAtOnce(t *testing.T) {
	f := newEffortFixture(t, "own", "")
	source := memoryEmbedder{f.a}
	if basis, err := source.Basis(); err == nil {
		t.Fatalf("rig: the running provider names no embeddings model yet, got basis %q", basis)
	}
	save := func(said bool, model string) {
		t.Helper()
		edit := dashboard.ProviderEdit{ProviderInfo: dashboard.ProviderInfo{
			Name: f.entry.Name, APIType: "openai", Endpoint: f.entry.URL, DefaultModel: "own",
			ConfiguredModels: f.entry.Models, ContextLength: 64000, MaxOutputTokens: 4096, Default: true, Chat: true,
			EmbeddingsModel: model,
		}, SetEmbeddingsModel: said}
		if err := f.a.setProviderInfo(edit); err != nil {
			t.Fatalf("save (said %v, model %q): %v", said, model, err)
		}
	}
	stored := func() string {
		t.Helper()
		reg, err := f.a.loadProviders()
		if err != nil {
			t.Fatal(err)
		}
		return reg.Providers[0].EmbeddingsModel
	}

	save(true, "bge-m3")
	if basis, err := source.Basis(); err != nil || basis != f.entry.Name+"/bge-m3" {
		t.Fatalf("after naming a model the running basis is %q (%v), want %s/bge-m3", basis, err, f.entry.Name)
	}
	if stored() != "bge-m3" {
		t.Fatalf("the file holds %q", stored())
	}

	// A save that says nothing of it: an older page, an API caller.
	save(false, "")
	if basis, err := source.Basis(); err != nil || basis != f.entry.Name+"/bge-m3" || stored() != "bge-m3" {
		t.Fatalf("a save that said nothing of the model changed it: basis %q (%v), file %q", basis, err, stored())
	}

	save(true, "")
	if basis, err := source.Basis(); err == nil || stored() != "" {
		t.Fatalf("after emptying the field the layer still has basis %q and the file holds %q", basis, stored())
	}
}
