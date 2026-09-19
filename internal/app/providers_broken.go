package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/oauth"
)

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
type brokenEntry struct {
	raw       json.RawMessage
	position  int
	after     int
	name      string
	isDefault bool
	reason    string
	repair    *entryRepair
}

// .
type entryRepair struct {
	what  string
	entry providerEntry
}

// .
// .
func admitEntries(top providerRegistry, raws []json.RawMessage) (*providerRegistry, error) {
	entries := make([]json.RawMessage, len(raws))
	for i, raw := range raws {
		entries[i] = compactEntry(raw)
	}
	// .
	if reg, err := prepareRegistry(top, entries); err == nil {
		return reg, nil
	}
	var admitted []json.RawMessage
	var broken []brokenEntry
	for i, raw := range entries {
		trial := append(slices.Clone(admitted), raw)
		if _, err := prepareRegistry(top, trial); err != nil {
			broken = append(broken, brokenEntry{raw: raw, position: i, after: len(admitted), reason: err.Error()})
			continue
		}
		admitted = trial
	}
	reg, err := prepareRegistry(top, admitted)
	if err != nil {
		// .
		// .
		return nil, err
	}
	names := map[string]bool{}
	for _, e := range reg.Providers {
		names[e.Name] = true
	}
	taken := map[string]bool{}
	for name := range names {
		taken[name] = true
	}
	for i := range broken {
		e, err := decodeProviderEntry(broken[i].raw)
		if err != nil {
			// .
			// .
			// .
			var shown struct {
				Name    string `json:"name"`
				Default bool   `json:"default"`
			}
			_ = json.Unmarshal(broken[i].raw, &shown)
			e.Name, e.Default = shown.Name, shown.Default
		}
		broken[i].name, broken[i].isDefault = e.Name, e.Default
		if e.Name != "" {
			taken[e.Name] = true
		}
	}
	for i := range broken {
		broken[i].repair = repairFor(top, admitted, broken[i], names, taken)
	}
	reg.broken = broken
	return reg, nil
}

// .
// .
// .
func prepareRegistry(top providerRegistry, entries []json.RawMessage) (*providerRegistry, error) {
	reg := top
	reg.Providers = make([]providerEntry, 0, len(entries))
	for _, raw := range entries {
		e, err := decodeProviderEntry(raw)
		if err != nil {
			return nil, err
		}
		reg.Providers = append(reg.Providers, e)
	}
	// .
	// .
	reg.eff = newEffectiveCaps(&reg)
	fillEmbeddedCredentialOptions(&reg)
	fillEmbeddedEffortLevels(&reg)
	fillEmbeddedCatalogueAuthors(&reg)
	fillEmbeddedSpeech(&reg)
	if err := bindOAuth(&reg); err != nil {
		return nil, err
	}
	if err := validateEntries(&reg); err != nil {
		return nil, err
	}
	return &reg, nil
}

// .
func decodeProviderEntry(raw json.RawMessage) (providerEntry, error) {
	var e providerEntry
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&e); err != nil {
		return providerEntry{}, fmt.Errorf("the entry is not a provider: %w", err)
	}
	return e, nil
}

func compactEntry(raw json.RawMessage) json.RawMessage {
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		return slices.Clone(raw)
	}
	return b.Bytes()
}

// .
// .
func entryDigest(raw json.RawMessage) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
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
// .
// .
// .
// .
// .
func repairFor(top providerRegistry, admitted []json.RawMessage, b brokenEntry, names, taken map[string]bool) *entryRepair {
	if _, err := decodeProviderEntry(b.raw); err != nil {
		return nil
	}
	type change struct {
		what  string
		apply func(*providerEntry) bool
	}
	changes := []change{
		{"clear its default flag", func(e *providerEntry) bool {
			if !e.Default {
				return false
			}
			e.Default = false
			return true
		}},
		{"remove the OAuth contract name this release does not ship, so its credential's own contract applies", func(e *providerEntry) bool {
			if e.OAuth == "" {
				return false
			}
			if _, known := oauth.ProviderTemplate(e.OAuth, top.oauthContracts()); known {
				return false
			}
			e.OAuth = ""
			return true
		}},
		{"remove its speech settings, so the ones this release ships for " + b.name + " apply", func(e *providerEntry) bool {
			if e.Speech == nil || !shippedSpeechVendor(e.Name) {
				return false
			}
			e.Speech = nil
			return true
		}},
	}
	if b.name != "" && names[b.name] {
		free := b.name
		for n := 2; taken[free]; n++ {
			free = fmt.Sprintf("%s (%d)", b.name, n)
		}
		changes = append(changes, change{fmt.Sprintf("rename it to %q; the first entry named %q stays in use", free, b.name), func(e *providerEntry) bool {
			e.Name = free
			return true
		}})
	}
	for _, c := range changes {
		e, _ := decodeProviderEntry(b.raw)
		if !c.apply(&e) {
			continue
		}
		raw, err := json.Marshal(e)
		if err != nil {
			continue
		}
		trial := slices.Insert(slices.Clone(admitted), b.after, json.RawMessage(raw))
		if _, err := prepareRegistry(top, trial); err == nil {
			return &entryRepair{what: c.what, entry: e}
		}
	}
	return nil
}

// .
// .
func withBrokenEntries(reg *providerRegistry) *providerRegistry {
	if len(reg.broken) == 0 {
		return reg
	}
	cp := *reg
	cp.Providers = make([]providerEntry, 0, len(reg.Providers)+len(reg.broken))
	next := 0
	for i := 0; i <= len(reg.Providers); i++ {
		for next < len(reg.broken) && (reg.broken[next].after <= i || i == len(reg.Providers)) {
			cp.Providers = append(cp.Providers, providerEntry{raw: reg.broken[next].raw})
			next++
		}
		if i < len(reg.Providers) {
			cp.Providers = append(cp.Providers, reg.Providers[i])
		}
	}
	return &cp
}

// .
func brokenIndex(reg *providerRegistry, position int, digest string) (int, error) {
	for i, b := range reg.broken {
		if b.position == position && entryDigest(b.raw) == digest {
			return i, nil
		}
	}
	return -1, fmt.Errorf("providers.json no longer holds that broken entry at position %d; the list shown was out of date and has been refreshed", position)
}

// .
func brokenNamed(reg *providerRegistry, name string) *brokenEntry {
	for i := range reg.broken {
		if reg.broken[i].name == name {
			return &reg.broken[i]
		}
	}
	return nil
}

// .
func (a *App) removeBrokenProvider(position int, digest string) error {
	return a.changeProviders("", func(reg *providerRegistry) error {
		i, err := brokenIndex(reg, position, digest)
		if err != nil {
			return err
		}
		reg.broken = slices.Delete(reg.broken, i, i+1)
		return nil
	})
}

// .
// .
func (a *App) repairBrokenProvider(position int, digest string) error {
	return a.changeProviders("", func(reg *providerRegistry) error {
		i, err := brokenIndex(reg, position, digest)
		if err != nil {
			return err
		}
		b := reg.broken[i]
		if b.repair == nil {
			return fmt.Errorf("no repair is known for the broken entry at position %d: remove it, or edit providers.json by hand", position)
		}
		at := min(b.after, len(reg.Providers))
		reg.Providers = slices.Insert(reg.Providers, at, b.repair.entry)
		reg.broken = slices.Delete(reg.broken, i, i+1)
		for j := i; j < len(reg.broken); j++ {
			reg.broken[j].after++
		}
		return nil
	})
}

// .
// .
func brokenProviderInfo(reg *providerRegistry) []dashboard.BrokenProviderInfo {
	if len(reg.broken) == 0 {
		return nil
	}
	out := make([]dashboard.BrokenProviderInfo, 0, len(reg.broken))
	for _, b := range reg.broken {
		info := dashboard.BrokenProviderInfo{Position: b.position, SHA256: entryDigest(b.raw), Name: b.name, Reason: b.reason}
		if b.repair != nil {
			info.Repair = b.repair.what
		}
		out = append(out, info)
	}
	return out
}
