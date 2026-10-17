package app

import "testing"

func TestBothBootsRecordTheProviderTheyResolved(t *testing.T) {
	for _, boot := range []struct {
		name string
		run  func(a *App) error
	}{
		{"live", startLiveForTest},
		{"safe", func(a *App) error { return a.startSafeBoot(safeRecord, "test: SAFE keeps the conversation") }},
	} {
		t.Run(boot.name, func(t *testing.T) {
			dir := t.TempDir()
			keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Provider")
			a := New(safebootConfig(t, dir, "Provider", keyPath, ledgerPath, dbPath))
			t.Cleanup(func() { stopApp(t, a) })
			if err := boot.run(a); err != nil {
				t.Fatalf("%s boot: %v", boot.name, err)
			}
			_, want, err := a.resolveLLM()
			if err != nil {
				t.Fatal(err)
			}
			if got := a.currentProvider(); got.Name == "" || got.Name != want.Name || got.EmbeddingsModel != want.EmbeddingsModel {
				t.Fatalf("after the %s boot the identity is on provider %q (embeddings %q); it resolved %q (embeddings %q)", boot.name, got.Name, got.EmbeddingsModel, want.Name, want.EmbeddingsModel)
			}
			// And the client built from it is recorded with it: what embeds a
			// text reads the two as one (adoptRuntime).
			if entry, client := a.activeRuntime(); client == nil || client != a.llmSwap.Current() || entry.Name != want.Name {
				t.Fatalf("after the %s boot provider %q is recorded with a client: %v; the same client as the swap holds: %v", boot.name, entry.Name, client != nil, client == a.llmSwap.Current())
			}
		})
	}
}
