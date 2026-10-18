package app

import "github.com/aiii-dot-id/aii-os/internal/llm"

// adoptProvider is how a test names the provider an App is on without
// activating one: the entry, with the client the test has installed in the
// swap, if it has. The host itself always records the entry and the client
// built from it together (adoptRuntime).
func (a *App) adoptProvider(entry providerEntry) {
	var client *llm.Client
	if a.llmSwap != nil {
		client = a.llmSwap.Current()
	}
	a.adoptRuntime(entry, client)
}
