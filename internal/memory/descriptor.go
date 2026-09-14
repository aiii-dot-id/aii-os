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
package memory

import "github.com/aiii-dot-id/aii-os/internal/memory/carrd"

// .
// .
const RingWorking = 4

// .
type Store struct {
	// .
	// .
	Name string
	// .
	// .
	Content string
	// .
	Text []string
	// .
	FTS string
	Tri string
	// .
	// .
	// .
	Time string
	// .
	// .
	Seq string
	// .
	// .
	Ring       int
	RingColumn string
	// .
	// .
	// .
	Class carrd.Class
	// .
	// .
	// .
	Attribution       string
	AttributionColumn string
	// .
	// .
	// .
	Filter string
	// .
	// .
	// .
	// .
	Current string
	// .
	Reinforce bool
	// .
	// .
	// .
	VectorWindow int
}

// .
const ledgerTime = "(SELECT l.ts FROM ledger l WHERE l.seq = b.created_seq)"

var stores = []Store{
	{
		Name: "experiences", Content: "experiences", Text: []string{"content"},
		FTS: "experiences_fts", Tri: "experiences_tri",
		Time: "b.created_at", Seq: "created_seq", Ring: 3, Class: carrd.Operational,
		AttributionColumn: "provenance", Filter: "b.private = 0", Reinforce: true,
	},
	{
		Name: "beliefs", Content: "beliefs", Text: []string{"statement"},
		FTS: "beliefs_fts", Tri: "beliefs_tri",
		Time: "(SELECT l.ts FROM ledger l WHERE l.seq = b.first_seq)", Seq: "first_seq", RingColumn: "ring", Class: carrd.Core,
		Attribution: "self", Current: "b.archived = 0 AND b.superseded_by IS NULL", Reinforce: true,
	},
	{
		Name: "self_model_synthesis", Content: "self_model_synthesis", Text: []string{"synthesis_text"},
		FTS: "self_model_synthesis_fts", Tri: "self_model_synthesis_tri",
		Time: "b.created_at", Seq: "created_seq", Ring: 3, Class: carrd.Core,
		Attribution: "self", Current: "b.superseded_by IS NULL", Reinforce: true,
	},
	{
		Name: "intentions", Content: "intentions", Text: []string{"statement", "why", "outcome"},
		FTS: "intentions_fts", Tri: "intentions_tri",
		Time: ledgerTime, Seq: "created_seq", Ring: 3, Class: carrd.Standard,
		Attribution: "self", Current: "b.archived = 0", Reinforce: true,
	},
	{
		Name: "commitments", Content: "commitments", Text: []string{"description"},
		FTS: "commitments_fts", Tri: "commitments_tri",
		Time: ledgerTime, Seq: "created_seq", Ring: 3, Class: carrd.Standard,
		Attribution: "self", Reinforce: true,
	},
	{
		Name: "relationships", Content: "relationships", Text: []string{"counterpart_name", "charter_text"},
		FTS: "relationships_fts", Tri: "relationships_tri",
		Time: ledgerTime, Seq: "created_seq", Ring: 3, Class: carrd.Core,
		Attribution: "self", Current: "b.superseded_by IS NULL", Reinforce: true,
	},
	{
		Name: "conversations", Content: "conversations_searchable", Text: []string{"content"},
		FTS: "conversations_fts", Tri: "conversations_tri",
		Time: "b.created_at", Seq: "turn_seq", Ring: RingWorking, Class: carrd.Ephemeral,
		AttributionColumn: "role", Filter: "b.role IN ('resident', 'operator', 'participant')", Reinforce: true,
		VectorWindow: ConversationVectorWindow,
	},
	{
		Name: "inbound", Content: "inbound", Text: []string{"body"},
		FTS: "inbound_fts", Tri: "inbound_tri",
		Time: "strftime('%Y-%m-%dT%H:%M:%fZ', b.received_ms / 1000.0, 'unixepoch')", Ring: RingWorking, Class: carrd.Ephemeral,
		Attribution: "external", Reinforce: true,
	},
	{
		Name: "plugin_memories", Content: "plugin_memories", Text: []string{"text"},
		FTS: "plugin_memories_fts", Tri: "plugin_memories_tri",
		Time: "b.created_at", Ring: RingWorking, Class: carrd.Operational,
		AttributionColumn: "attribution", Current: "b.superseded_by IS NULL", Reinforce: true,
	},
}

// .
// .
// .
func Stores() []Store {
	out := make([]Store, len(stores))
	copy(out, stores)
	return out
}

// .
func Lookup(name string) (Store, bool) {
	for _, s := range stores {
		if s.Name == name {
			return s, true
		}
	}
	return Store{}, false
}
