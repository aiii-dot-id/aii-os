package app

import "time"

type capState int

const (
	capUnknown capState = iota
	capYes
	capNo
)

func (c capState) String() string {
	switch c {
	case capYes:
		return "yes"
	case capNo:
		return "no"
	}
	return "unknown"
}

type substrateCapability struct {
	provider  string
	model     string
	toolCalls capState
	note      string

	modalityURL      string
	inputModalities  []string
	outputModalities []string
	checkedAt        time.Time
}

func (a *App) setSubstrateCapability(c substrateCapability) {
	a.providers.runtime.mu.Lock()
	a.providers.runtime.substrateCap = c
	a.providers.runtime.mu.Unlock()
}

func (a *App) substrateCapabilityFor(provider, model string) substrateCapability {
	a.providers.runtime.mu.RLock()
	defer a.providers.runtime.mu.RUnlock()
	if a.providers.runtime.substrateCap.provider != provider || a.providers.runtime.substrateCap.model != model {
		return substrateCapability{provider: provider, model: model}
	}
	c := a.providers.runtime.substrateCap
	mods, _ := a.modalities.get(c.modalityURL)
	c.inputModalities, c.outputModalities = mods.in, mods.out
	return c
}
