package store

import (
	"fmt"
	"sort"
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
type Provenance int

const (
	// .
	// .
	Derived Provenance = iota + 1
	// .
	// .
	// .
	Ephemeral
)

func (p Provenance) String() string {
	switch p {
	case Derived:
		return "derived"
	case Ephemeral:
		return "ephemeral"
	}
	return fmt.Sprintf("Provenance(%d)", int(p))
}

// .
type TableEntry struct {
	Provenance Provenance
	// .
	// .
	// .
	ClearOrder int
	// .
	// .
	// .
	// .
	// .
	AlsoWrittenBy map[string]string
}

// .
// .
// .
// .
// .
// .
var writers = map[string]map[string]string{
	"trust_epochs":         {"trustepochs.go": "the materializer for trust.epoch_accepted lives there"},
	"experiences":          {"experiences.go": "MarkExperiencesProcessed clears raw at runtime; a consolidation.run record carries the same fact and replay restores it"},
	"self_model_synthesis": {"self_model.go": "the materializer for self_model.synthesize lives there"},
	"ledger":               {"replay.go": "the clear", "reconcile.go": "recreateMirror, the carry-across's one DDL act"},
	"tool_events":          {"reconcile.go": "schema reconciliation carries rows through a shape change"},
	"identity_lifetime":    {"materialize.go": "materializeBirth seeds the singleton with INSERT OR IGNORE; lived time accumulates at runtime and is never cleared"},
	"public_name":          {"publicname.go": "the materializer for network.name_claimed lives there"},
}

// .
// .
// .
var Catalog = mustCatalog()

func mustCatalog() map[string]TableEntry {
	raw, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		panic("store: cannot read embedded schema.sql — broken build: " + err.Error())
	}
	c, err := parseProvenance(string(raw))
	if err != nil {
		panic("store: schema.sql provenance declarations — broken build: " + err.Error())
	}
	return c
}

// .
// .
func DerivedTables() []string {
	var out []string
	for name, e := range Catalog {
		if e.Provenance == Derived {
			out = append(out, name)
		}
	}
	sort.Slice(out, func(i, j int) bool { return Catalog[out[i]].ClearOrder < Catalog[out[j]].ClearOrder })
	return out
}

// .
func EphemeralTables() []string {
	var out []string
	for name, e := range Catalog {
		if e.Provenance == Ephemeral {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// .

// .
// .
// .
// .
// .
// .
type SidecarEntry struct {
	Base    string
	Content string
	Args    string
}

// .
// .
// .
var Sidecars = mustSidecars()

func mustSidecars() map[string]SidecarEntry {
	raw, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		panic("store: cannot read embedded schema.sql — broken build: " + err.Error())
	}
	sc, err := parseSidecars(string(raw), Catalog)
	if err != nil {
		panic("store: schema.sql sidecar declarations — broken build: " + err.Error())
	}
	return sc
}

// .
func SidecarNames() []string {
	out := make([]string, 0, len(Sidecars))
	for name := range Sidecars {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// .
func SidecarsOf(base string) []string {
	var out []string
	for name, e := range Sidecars {
		if e.Base == base {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}
