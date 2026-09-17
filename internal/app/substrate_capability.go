package app

import "time"

// .
// .
// .
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

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
type substrateCapability struct {
	provider  string
	model     string
	toolCalls capState
	note      string
	// .
	// .
	// .
	modalityURL      string
	inputModalities  []string
	outputModalities []string
	checkedAt        time.Time
}

// .
func (a *App) setSubstrateCapability(c substrateCapability) {
	a.capMu.Lock()
	a.substrateCap = c
	a.capMu.Unlock()
}

// .
// .
// .
// .
func (a *App) substrateCapabilityRecord() substrateCapability {
	a.capMu.RLock()
	defer a.capMu.RUnlock()
	return a.substrateCap
}

// .
// .
// .
// .
// .
func (a *App) substrateCapabilityFor(provider, model string) substrateCapability {
	a.capMu.RLock()
	defer a.capMu.RUnlock()
	if a.substrateCap.provider != provider || a.substrateCap.model != model {
		return substrateCapability{provider: provider, model: model}
	}
	c := a.substrateCap
	mods, _ := a.modalities.get(c.modalityURL)
	c.inputModalities, c.outputModalities = mods.in, mods.out
	return c
}
