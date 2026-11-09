package ledger

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type payloadMembers map[string]payloadMembers

var commitMembers = []string{"variant", "id", "ring", "model_id", "cites"}

func with(base []string, more ...string) payloadMembers {
	m := payloadMembers{}
	for _, name := range append(append([]string{}, base...), more...) {
		m[name] = nil
	}
	return m
}

var (
	originRefMembers = payloadMembers{"kind": nil, "id": nil}
	receiptMembers   = payloadMembers{
		"identity_id": nil, "previous_witnessed_ledger_ordinal": nil, "previous_witnessed_ledger_hash": nil,
		"ledger_ordinal": nil, "ledger_hash": nil, "witnessed_at": nil, "witness_key_id": nil, "witness_sig_b64": nil,
	}
)

var declaredPayloadMembers = func() map[EventType]payloadMembers {
	belief := with(commitMembers, "statement", "content", "confidence", "node_type", "evidence_refs",
		"distinct_from", "distinction", "duplicate_ok")
	belief["origin_refs"] = originRefMembers
	style := with(commitMembers, "content", "statement", "confidence", "evidence_refs",
		"distinct_from", "distinction", "duplicate_ok")
	style["origin_refs"] = originRefMembers
	portrait := with([]string{"id", "model_id"}, "synthesis_text", "continuity_thread",
		"changes_since_last", "previous_synthesis_id")
	portrait["source_entity_refs"] = payloadMembers{"class": nil, "id": nil}
	note := with([]string{"id", "model_id"}, "content", "category", "provenance", "unmetabolized",
		"source_turn", "source_url", "raw", "cites")
	note["dreamed_through"] = payloadMembers{"turn": nil, "position": nil}
	run := with([]string{"model_id"}, "inputs", "outputs")
	run["confirmed"] = payloadMembers{"id": nil, "ticks": nil}
	return map[EventType]payloadMembers{
		EventRing0Genesis: with([]string{"model_id"}, "name", "ring0_content", "ring0_provenance", "ring0_bundle_b64",
			"ring0_bundle_payload_sha", "ring0_identity_sig", "ring0_sig_alg", "public_key", "public_key_alg", "fingerprint"),
		EventBeliefUpsert:        belief,
		EventWorkingStyleUpsert:  style,
		EventBeliefPromote:       with(commitMembers),
		EventBeliefArchive:       with(commitMembers, "reason"),
		EventBeliefSupersede:     with(commitMembers, "old_id", "new_id", "reason"),
		EventEdgeArchive:         with(commitMembers),
		EventSelfModelSynthesize: portrait,
		EventRelationshipUpsert: with(commitMembers, "counterpart_name", "counterpart_role", "trust_level", "autonomy_level",
			"relationship_type", "supersedes", "charter_text", "operator_approval_excerpt", "operator_approval_turn", "approval_basis", "approval_digest", "approval_press_turn"),
		EventEdgeCreate:            with(commitMembers, "from_id", "to_id", "edge_type", "strength", "context"),
		EventExperienceCreate:      note,
		EventIntentionCreate:       with(commitMembers, "statement", "why"),
		EventIntentionStateChange:  with(commitMembers, "state", "outcome"),
		EventCommitmentPromised:    with(commitMembers, "description", "counterpart_id"),
		EventCommitmentStateChange: with(commitMembers, "state", "result", "repair_state", "note"),
		EventConsolidationRun:      run,
		EventDreamRun:              run,

		EventSystemWitnessed:    payloadMembers{"receipt": receiptMembers, "receipt_before_rewrap": nil, "model_id": nil},
		EventTrustEpochAccepted: with([]string{"model_id"}, "root", "trust_epoch", "payload_sha256"),
		EventNetworkNameClaimed: with([]string{"model_id"}, "name_id", "name", "zone"),
	}
}()

func sortedMembers(m payloadMembers) []string {
	out := make([]string, 0, len(m))
	for name := range m {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

type UndeclaredMemberError struct {
	Type     EventType
	Path     string
	Member   string
	Exact    string
	Declared []string
}

const (
	undeclaredMemberRefusal = "%s does not declare the member %q, so nothing was recorded; its members are %s"
	undeclaredCaseRefusal   = "%s does not declare the member %q (its member is %q, spelled exactly so), so nothing was recorded"
)

func (e *UndeclaredMemberError) Error() string {
	member := e.Member
	if e.Path != "" {
		member = e.Path + "." + e.Member
	}
	if e.Exact != "" {
		exact := e.Exact
		if e.Path != "" {
			exact = e.Path + "." + e.Exact
		}
		return fmt.Sprintf(undeclaredCaseRefusal, e.Type, member, exact)
	}
	return fmt.Sprintf(undeclaredMemberRefusal, e.Type, member, strings.Join(e.Declared, ", "))
}

func AdmitPayloadMembers(t EventType, payload []byte) error {
	decl, ok := declaredPayloadMembers[t]
	if !ok {
		return nil
	}
	return admitMembers(t, "", payload, decl)
}

func admitMembers(t EventType, path string, raw []byte, decl payloadMembers) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) > 0 && raw[0] == '[' {
		var each []json.RawMessage
		if err := json.Unmarshal(raw, &each); err != nil {
			return err
		}
		for _, e := range each {
			if err := admitMembers(t, path+"[]", e, decl); err != nil {
				return err
			}
		}
		return nil
	}
	if len(raw) == 0 || raw[0] != '{' {

		return nil
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil {
		return err
	}
	for _, name := range sortedKeys(members) {
		sub, declared := decl[name]
		if !declared {
			e := &UndeclaredMemberError{Type: t, Path: path, Member: name, Declared: sortedMembers(decl)}
			for d := range decl {
				if strings.EqualFold(d, name) {
					e.Exact = d
				}
			}
			return e
		}
		if sub != nil {
			if err := admitMembers(t, joinPath(path, name), members[name], sub); err != nil {
				return err
			}
		}
	}
	return nil
}

func joinPath(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

func sortedKeys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
