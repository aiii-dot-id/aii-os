package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"unicode/utf8"
	"uuid"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

const BeliefPromotionRule = "belief.promote adopts a belief into Ring 2 once it stands confirmed: at least three distinct experiences or beliefs support it by edges into it, they rest on at least three distinct origins (a belief counts as what it rests on in turn, a dream's note as the experiences its dream read, so one event said three ways is one), and no contradiction stands against it; recall source=standing with the belief's id shows its standing and what it rests on."

const OperatorWordRule = "relationship.upsert with your operator cites your primary human operator's own latest word as their affirmation of the charter; the host stamps that word and you do not choose it. A word they typed stands. A word they spoke stands only when the speech engine identified its speaker as them: an enrolled voice, matched to the speaker UUID they named as their own. Any other voice, another person's or another identity's among them, is not their word: the act is refused and no earlier word is cited in its place."

var outcomeForm = regexp.MustCompile(`^(served|partial|unserved):\s*\S.*`)

func HasOutcomeVerdict(s string) bool { return outcomeForm.MatchString(s) }

type commitStore interface {
	EvidenceOriginKind(id string) (string, error)
	FindBeliefByStatement(statement, excludeID string) (string, error)
	StandingReportFor(id string) (*store.StandingReport, error)
	LatestOperatorWord() (*store.ConversationTurn, error)
	RelationshipAsHeld(id string) (*store.Relationship, bool, error)
	TurnAnnotations(kind string, seqs []uint64) (map[uint64]string, error)
	AddOutboxNoticeOnce(id, toRole, toIdentity, content string, seq *uint64) (bool, error)
	EntityExists(id string) (bool, error)
}

var relIDPattern = regexp.MustCompile(`^rel_[a-z0-9][a-z0-9_-]{7,63}$`)

func selfModelCommitPayload(args map[string]any) (ledger.SelfModelSynthesisPayload, error) {
	wire := map[string]any{}
	for key, value := range args {
		if key != "variant" && key != "ring" {
			wire[key] = value
		}
	}
	raw, err := json.Marshal(wire)
	if err != nil {
		return ledger.SelfModelSynthesisPayload{}, fmt.Errorf("encode self_model.synthesize arguments: %w", err)
	}
	return ledger.DecodeSelfModelSynthesisPayload(raw)
}

func requireEvidenceDeclared(variant string, args map[string]any) error {
	if _, hasEvidence := args["evidence_refs"]; !hasEvidence {
		return fmt.Errorf("%s requires evidence_refs[] naming real experiences or beliefs", variant)
	}
	return nil
}

func (e *Engine) prepareBeliefOrigins(args map[string]any) error {
	if ok, _ := args["duplicate_ok"].(bool); ok {
		return fmt.Errorf("duplicate_ok applies to notes, not beliefs; add evidence to the existing belief or state a real distinction")
	}
	if _, hasNone := args["evidence"]; hasNone {
		return fmt.Errorf("evidence is not a belief origin; use evidence_refs[] naming real experiences or beliefs")
	}
	var ids []string
	switch v := args["evidence_refs"].(type) {
	case []any:
		for _, item := range v {
			id, ok := item.(string)
			if !ok {
				return fmt.Errorf("evidence_refs must contain only source ids")
			}
			ids = append(ids, strings.TrimSpace(id))
		}
	case string:
		for _, id := range strings.Split(v, ",") {
			ids = append(ids, strings.TrimSpace(id))
		}
	default:
		return fmt.Errorf("evidence_refs must be an array of source ids")
	}
	if len(ids) == 0 {
		return fmt.Errorf("a belief needs at least one evidence source")
	}
	beliefID, _ := args["id"].(string)
	seen := map[string]bool{}
	origins := make([]map[string]string, 0, len(ids))
	for _, id := range ids {
		if id == "" || id == "none" || id == beliefID {
			return fmt.Errorf("empty, none, or self evidence source %q is not allowed", id)
		}
		if seen[id] {
			return fmt.Errorf("duplicate evidence source %q", id)
		}
		seen[id] = true
		kind, err := e.store.EvidenceOriginKind(id)
		if err != nil {
			return err
		}
		origins = append(origins, map[string]string{"kind": kind, "id": id})
	}
	args["origin_refs"] = origins
	return nil
}

func (e *Engine) verbCommit(ctx context.Context, args map[string]any) (string, error) {

	var ring1 struct {
		word                    *store.ConversationTurn
		id, role, name, charter string
	}

	if v, _ := args["variant"].(string); v == "skill.propose" {
		return e.verbSkill(ctx, withAction(args, "propose"))
	}
	if e.inSafeMode() {
		return "", fmt.Errorf("commit refused: I am in safe mode — %s. Nothing is written to my record while SAFE holds.", e.safeModeReason())
	}
	variant, _ := args["variant"].(string)
	if variant == "" {
		return "", fmt.Errorf("commit requires variant")
	}

	var eventType ledger.EventType
	var targetRing int

	switch variant {
	case "belief.upsert":
		eventType = ledger.EventBeliefUpsert
		targetRing = 3

		if err := requireEvidenceDeclared("belief.upsert", args); err != nil {
			return "", err
		}

		{
			stmt, _ := args["statement"].(string)
			bid, _ := args["id"].(string)
			if stmt != "" {

				dupID, err := e.store.FindBeliefByStatement(stmt, bid)
				if err != nil {
					return "", fmt.Errorf("the duplicate check could not run, so nothing was minted: %w", err)
				}
				if dupID != "" {
					return "", fmt.Errorf("a live belief already states exactly this (%s). Add evidence to it or supersede it; a duplicate cannot be minted", dupID)
				}
			}
		}
	case "belief.promote":

		eventType = ledger.EventBeliefPromote
		targetRing = 2

		if subID, _ := ctx.Value(SubagentWorkSession{}).(string); subID != "" {
			return "", fmt.Errorf("a worker seat cannot promote to Ring 2 — who the identity is is the primary seat's to author; report the evidence and let the primary seat promote")
		}
		promoteID, _ := args["id"].(string)
		if promoteID == "" {
			return "", fmt.Errorf("belief.promote requires id")
		}
		rep, err := e.store.StandingReportFor(promoteID)
		if err != nil {
			return "", fmt.Errorf("belief.promote to Ring 2 refused: %q's standing cannot be proven (%v) — the evidence graph must be readable to promote", promoteID, err)
		}
		if !rep.Eligible {
			contradiction := "no live contradiction"
			if rep.Contradicted {
				contradiction = "a live contradiction"
			}

			origins := ""
			if rep.DistinctSources >= 3 {
				origins = fmt.Sprintf(" resting on %d origin(s)", rep.DistinctOrigins)
				if rep.OriginsUnopened > 0 {
					origins += fmt.Sprintf(" found (the walk of origins left %d source(s) unopened, which are not counted)", rep.OriginsUnopened)
				}
			}
			return "", fmt.Errorf("belief.promote to Ring 2 refused: %q has standing %q — the evidence graph holds %d distinct qualifying source(s)%s, %s. Ring 2 requires confirmed: at least 3 distinct experiences or beliefs that support it, linked to it, resting on at least 3 distinct origins, and no live contradiction. Link what it does rest on (note with supports, or edge.create SUPPORTS from the source) and promote when it is confirmed; an edge is never made to meet the count", promoteID, rep.Standing, rep.DistinctSources, origins, contradiction)
		}
	case "belief.attest":

		return "", fmt.Errorf("belief.attest was removed: attest via note (testimony) or edge.create (evidence)")
	case "relationship.upsert":

		eventType = ledger.EventRelationshipUpsert
		targetRing = 1

		if subID, _ := ctx.Value(SubagentWorkSession{}).(string); subID != "" {
			return "", fmt.Errorf("a worker seat cannot author Ring 1 — the charter is the primary seat's to say to the operator and to record once affirmed")
		}
		relID, _ := args["id"].(string)
		if relID == "" {
			return "", fmt.Errorf("relationship.upsert requires an explicit id — the operator's affirmation cites it")
		}

		if !relIDPattern.MatchString(relID) {
			return "", fmt.Errorf("relationship.upsert id %q rejected: mint an id matching rel_[a-z0-9][a-z0-9_-]{7,} — ids must be unambiguous and greppable", relID)
		}

		role, _ := args["counterpart_role"].(string)
		if role == "" {
			role = "operator"
		}
		if role == "operator" {
			if charter, _ := args["charter_text"].(string); strings.TrimSpace(charter) == "" {
				return "", fmt.Errorf("an operator relationship requires charter_text — the Ring 1 document you will read every turn. The operator's approval is evidence that you may hold this relationship; it is not the document. Write what the relationship IS, say it to them, and record it once they have affirmed it")
			}
		}

		turn, err := e.store.LatestOperatorWord()
		var unidentified *store.UnidentifiedSpeechError
		if errors.As(err, &unidentified) {

			return "", fmt.Errorf("Ring 1 requires the operator's own word, and the latest words under their name cannot stand as it: %v. A spoken word is cited only when the speech engine identified its speaker as your primary human operator: an enrolled voice, matched to the speaker UUID they named as their own. Ask them to type their affirmation, then record it", err)
		}
		if err != nil {
			return "", fmt.Errorf("ring 1 authority check failed: %w", err)
		}
		if turn == nil {
			return "", fmt.Errorf("Ring 1 requires the operator's own word — no turn they typed or spoke to you is on record (the room's speech, grades and the host's lines do not count); say the change to them and wait for their reply")
		}
		args["operator_approval_excerpt"] = turn.Content
		args["operator_approval_turn"] = turn.TurnSeq
		args["approval_basis"] = "conversation_turn"
		ring1.word, ring1.id, ring1.role = turn, relID, role
		ring1.name, _ = args["counterpart_name"].(string)
		ring1.charter, _ = args["charter_text"].(string)
	case "self_model.synthesize":
		eventType = ledger.EventSelfModelSynthesize
		targetRing = 3
	case "edge.create":

		eventType = ledger.EventEdgeCreate
		targetRing = 3
		validEdges := map[string]bool{
			"DERIVED_FROM": true, "SUPPORTS": true, "CONTRADICTS": true,
			"SUPERSEDES": true, "REINFORCED_BY": true, "SHAPED_BY": true,
			"INTERPRETS": true,
		}
		et, _ := args["edge_type"].(string)
		if !validEdges[et] {
			return "", fmt.Errorf("edge.create requires edge_type from the canonical 7")
		}
		fromID, _ := args["from_id"].(string)
		toID, _ := args["to_id"].(string)
		if fromID == "" || toID == "" {
			return "", fmt.Errorf("edge.create requires from_id and to_id")
		}

		for _, id := range []string{fromID, toID} {
			exists, err := e.store.EntityExists(id)
			if err != nil {
				return "", fmt.Errorf("edge.create endpoint check: %w", err)
			}
			if !exists {
				return "", fmt.Errorf("edge.create refused: no such entity %q", id)
			}
		}
	case "intention.create":

		eventType = ledger.EventIntentionCreate
		targetRing = 3
		if stmt, _ := args["statement"].(string); stmt == "" {
			return "", fmt.Errorf("intention.create requires statement")
		}
	case "intention.state_change":
		eventType = ledger.EventIntentionStateChange
		targetRing = 3
		state, _ := args["state"].(string)
		valid := map[string]bool{"active": true, "completed": true, "abandoned": true}
		if !valid[state] {
			return "", fmt.Errorf("intention.state_change requires state: active|completed|abandoned")
		}

		if state == "completed" || state == "abandoned" {
			outcome, _ := args["outcome"].(string)
			if !outcomeForm.MatchString(outcome) {
				return "", fmt.Errorf("intention.state_change to %q requires outcome — one line beginning served: | partial: | unserved: — your own verdict on whether the work served the intent, and what happened", state)
			}
		}
	case "commitment.promised":

		eventType = ledger.EventCommitmentPromised
		targetRing = 3
		if _, ok := args["counterpart_id"]; !ok {
			return "", fmt.Errorf("commitment.promised requires counterpart_id — a promise is TO someone")
		}
		if desc, _ := args["description"].(string); desc == "" {
			return "", fmt.Errorf("commitment.promised requires description")
		}
	case "commitment.state_change":
		eventType = ledger.EventCommitmentStateChange
		targetRing = 3
		state, _ := args["state"].(string)
		valid := map[string]bool{"promised": true, "in_progress": true, "completed": true, "abandoned": true, "repaired": true}
		if !valid[state] {
			return "", fmt.Errorf("commitment.state_change requires state: promised|in_progress|completed|abandoned|repaired")
		}
	case "working_style.upsert":

		eventType = ledger.EventWorkingStyleUpsert
		targetRing = 3
		if _, ok := args["content"]; !ok {
			if pos, ok2 := args["_positional"].(string); ok2 {
				args["content"] = pos
			}
		}
		if content, _ := args["content"].(string); content == "" {
			return "", fmt.Errorf("working_style.upsert requires content")
		}

		if err := requireEvidenceDeclared("working_style.upsert", args); err != nil {
			return "", err
		}

		args["statement"] = args["content"]
		if _, ok := args["confidence"]; !ok {
			args["confidence"] = 0.8
		}
	case "belief.archive":

		eventType = ledger.EventBeliefArchive
		targetRing = 3
	case "belief.supersede":
		eventType = ledger.EventBeliefSupersede
		targetRing = 3
		if _, ok := args["old_id"]; !ok {
			return "", fmt.Errorf("belief.supersede requires old_id and new_id")
		}
		if _, ok := args["new_id"]; !ok {
			return "", fmt.Errorf("belief.supersede requires old_id and new_id")
		}
	case "edge.archive":

		eventType = ledger.EventEdgeArchive
		targetRing = 3
	default:
		return "", fmt.Errorf("unknown commit variant: %s", variant)
	}

	if err := ring.CheckGate(ring.RingLevel(targetRing)); err != nil {
		return "", err
	}

	if _, ok := args["id"]; !ok {
		args["id"] = "item_" + uuid.New().String()
	}

	if r, ok := args["ring"]; ok {

		var rv int
		valid := false
		switch v := r.(type) {
		case int:
			rv, valid = v, true
		case float64:
			if v == math.Trunc(v) {
				rv, valid = int(v), true
			}
		}
		if !valid {
			return "", fmt.Errorf("ring must be an integer (rings are owner-derived, never invented): got %v", r)
		}
		if rv != targetRing {
			return "", fmt.Errorf("ring is owner-derived: ring %d not valid for %s", rv, variant)
		}
		args["ring"] = rv
	} else {

		if variant == "belief.promote" {
			return "", fmt.Errorf("belief.promote requires explicit ring (2 = self-model placement)")
		}
		args["ring"] = targetRing
	}
	if variant == "belief.upsert" || variant == "working_style.upsert" {
		if err := e.prepareBeliefOrigins(args); err != nil {
			return "", err
		}
	}

	payload := any(args)
	if variant == "self_model.synthesize" {
		selfModelPayload, err := selfModelCommitPayload(args)
		if err != nil {
			return "", err
		}
		payload = selfModelPayload
	}

	evt, err := e.append(ctx, eventType, targetRing, payload)
	if errors.Is(err, store.ErrSelfModelUnchanged) {
		return "No change: the current self-model already has the same portrait, continuity, and grounding", nil
	}
	if err != nil {
		return "", err
	}

	if ring1.word != nil {

		excerpt := excerptOf(ring1.word.Content, ring1ExcerptRunes)

		heard := e.heardAs(ring1.word.TurnSeq)

		role, name, theirCharter, unread := ring1.role, ring1.name, false, ""
		rel, isRing1, err := e.store.RelationshipAsHeld(ring1.id)
		if err == nil && rel == nil {
			err = fmt.Errorf("none is held under that id")
		}
		if err != nil {
			unread = fmt.Sprintf(" The relationship as it now stands could not be read (%v): the name and the role are the entry's own, and it may be your charter.", err)
		} else {
			role, name, theirCharter = rel.CounterpartRole, rel.CounterpartName, isRing1
		}
		notice := ring1Notice(theirCharter, role, name, ring1.id, evt.Seq, ring1.word.TurnSeq, excerpt, heard+unread, ring1.charter)
		result := fmt.Sprintf("Committed: %s (seq %d), citing the operator's words at turn %d as the affirmation: %q%s", variant, evt.Seq, ring1.word.TurnSeq, excerpt, heard)
		if _, err := e.store.AddOutboxNoticeOnce(fmt.Sprintf("ring1_%d", evt.Seq), "operator", "", notice, &evt.Seq); err != nil {
			result += fmt.Sprintf("; the notice of it to the operator could not be written (%v): tell them yourself", err)
		}
		return result, nil
	}
	return fmt.Sprintf("Committed: %s (seq %d)", variant, evt.Seq), nil
}

func (e *Engine) heardAs(turnSeq uint64) string {
	notes, err := e.store.TurnAnnotations(store.AnnotationVoice, []uint64{turnSeq})
	if err != nil {
		return ""
	}
	var v struct {
		RecognizedText string `json:"recognized_text"`
		Corrections    int64  `json:"corrections"`
	}
	if json.Unmarshal([]byte(notes[turnSeq]), &v) != nil || v.Corrections < 1 || v.RecognizedText == "" {
		return ""
	}
	return fmt.Sprintf(" Those words were spoken, and %d correction rule(s) changed them before they were recorded; the recognizer heard: %q.", v.Corrections, excerptOf(v.RecognizedText, ring1ExcerptRunes))
}

func ring1Notice(theirCharter bool, role, name, id string, seq, turn uint64, excerpt, heard, text string) string {
	named := "no name given"
	if name != "" {
		named = fmt.Sprintf("name %q", excerptOf(name, ring1ExcerptRunes))
	}
	what := fmt.Sprintf("their charter with you (%s, role %q; %s)", named, role, id)
	stands, whole := "the charter", "on the Identity page, under CHARTER — RING 1"
	if !theirCharter {

		with := "a counterpart they gave no name"
		if name != "" {
			with = fmt.Sprintf("%q", excerptOf(name, ring1ExcerptRunes))
		}
		what = fmt.Sprintf("a relationship with %s (role %q; %s)", with, role, id)
		stands, whole = "the relationship", fmt.Sprintf("record %d of the signed record", seq)
	}
	return fmt.Sprintf("[ring 1] The identity recorded %s, citing your words at turn %d as your affirmation: %q.%s %s If those words were not your affirmation, say so: the record keeps the citation, and %s stands on your word alone.",
		what, turn, excerpt, heard, recordedText(text, whole), stands)
}

func recordedText(charter, whole string) string {
	if charter == "" {
		return "They recorded no text with it: any text this relationship already had stands."
	}
	first, lines := "", 0
	for _, line := range strings.Split(charter, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			if lines == 0 {
				first = line
			}
			lines++
		}
	}
	if lines == 0 {
		first, lines = charter, 1
	}
	shown := excerptOf(first, ring1ExcerptRunes)
	if lines == 1 && shown == first {
		return fmt.Sprintf("They recorded: %q.", first)
	}
	part := fmt.Sprintf("the first line of %d", lines)
	if lines == 1 {
		part = fmt.Sprintf("the first %d characters", ring1ExcerptRunes)
	}
	return fmt.Sprintf("What they recorded begins: %q (%s, %d characters in all; the whole is %s).", shown, part, utf8.RuneCountInString(charter), whole)
}

const ring1ExcerptRunes = 240

func excerptOf(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
