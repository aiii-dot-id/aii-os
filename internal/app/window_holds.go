package app

import (
	"fmt"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

const windowRefusal = "substrate refused: provider %q model %q cannot hold this identity — the request it sends before any history is about %d tokens (%d of them its tools' definitions), and a window of %d tokens leaves %d for a prompt; a window of at least %d tokens holds it; current substrate kept"

const windowUnmeasured = "substrate refused: the request this identity sends could not be measured: %w; current substrate kept"

func (a *App) windowHoldsIdentity(entry providerEntry, model string) error {
	if entry.ContextLength <= 0 || a.composer == nil || a.toolReg == nil {
		return nil
	}
	budget, source := promptBudgetFor(entry, 0)
	if source != budgetDerived {
		return nil
	}
	reserve, err := a.promptReserve(llm.Message{Role: "user"}, 0)
	if err != nil {
		return fmt.Errorf(windowUnmeasured, err)
	}
	p, err := a.composer.ComposeWithin(budget, "", reserve)
	if err != nil {
		return fmt.Errorf(windowUnmeasured, err)
	}
	system, err := llm.EstimateMessageTokens(a.gatedSystem(p))
	if err != nil {
		return err
	}
	frame, err := llm.EstimateMessageTokens(llm.Message{Role: "system"})
	if err != nil {
		return err
	}
	required := reserve + system - frame
	if required <= budget {
		return nil
	}
	tools, err := llm.EstimateToolTokens(a.buildToolDefinitions())
	if err != nil {
		return err
	}
	outputs := entry.MaxOutputTokens
	if outputs <= 0 {
		outputs = defaultOutputReserve
	}
	return fmt.Errorf(windowRefusal, entry.Name, model, required, tools, entry.ContextLength, budget, required+outputs+promptSafetyTokens)
}
