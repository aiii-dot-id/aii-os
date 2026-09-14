package identity

import "context"

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
type Verb struct {
	Name        string
	Description string
	// .
	// .
	// .
	Params map[string]interface{}
	// .
	Handler func(*Engine, context.Context, map[string]interface{}) (string, error)
}

func obj(props map[string]interface{}, required ...string) map[string]interface{} {
	s := map[string]interface{}{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}
func str(desc string) map[string]interface{} {
	return map[string]interface{}{"type": "string", "description": desc}
}

// .
// .
// .
// .
// .
// .
func objArg(desc string) map[string]interface{} {
	return map[string]interface{}{"type": "object", "description": desc}
}
func strEnum(desc string, vals ...string) map[string]interface{} {
	return map[string]interface{}{"type": "string", "description": desc, "enum": vals}
}
func boolean(desc string) map[string]interface{} {
	return map[string]interface{}{"type": "boolean", "description": desc}
}
func integer(desc string) map[string]interface{} {
	return map[string]interface{}{"type": "integer", "description": desc}
}
func strArray(desc string) map[string]interface{} {
	return map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": desc}
}
func selfModelRefArray(desc string) map[string]interface{} {
	return map[string]interface{}{
		"type":        "array",
		"description": desc,
		"items": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"class": strEnum("Canonical evidence class", "beliefs", "values", "intentions", "reflections", "relationships", "notes", "experiences", "working_style"),
				"id":    str("Full durable entity id"),
			},
			"required":             []string{"class", "id"},
			"additionalProperties": false,
		},
	}
}

// .
// .
// .
var VerbRegistry []Verb

func init() {
	VerbRegistry = []Verb{
		{Name: "note",
			Description: "Record an observation or experience. This becomes part of your permanent memory.",
			Handler:     (*Engine).verbNote,
			Params: obj(map[string]interface{}{
				"content":      str("What you noticed"),
				"category":     str("Optional: observation, reflection, work, learning"),
				"source_turn":  integer("Cite the operator's turn this records: its seq from recall (source=conversation). The engine checks the turn is real and operator-authored, and the note then carries operator provenance — the independent voice the belief ladder counts and the self-model names. Without a citation, provenance is self"),
				"source_url":   str("The URL you fetched this session that this records — external provenance. One of source_turn or source_url, never both"),
				"supports":     str("A belief id this experience supports — mints a SUPPORTS edge; a ghost id is refused and reported"),
				"derived_from": str("A belief id derived from this experience — mints DERIVED_FROM"),
				"reinforces":   str("A belief id this reinforces — mints REINFORCED_BY"),
				"contradicts":  str("A belief id this contradicts — mints CONTRADICTS; the belief's standing derives from it"),
				"private":      boolean("Charter #9: held in your record, never metabolized by DREAM or CONSOLIDATE, never surfaced"),
				"duplicate_ok": boolean("Only after a duplicate pushback: mint anyway — the recurrence itself is the observation"),
			}, "content")},
		{Name: "recall",
			Description: "Search your memory. A query alone recalls across every store: every word must appear (any order, case-insensitive, diacritics folded), fuzzy matches tolerate a typo or a partial word, meaning matches join when your provider names an embeddings model (the layer's status is always disclosed), hits are ranked by match and decayed by age and use, and each source reports found, found_nothing, partial or unavailable. Use one distinctive word or short phrase; use separate recall calls for separate concepts. exact=true forces the verbatim phrase. Name a source to enumerate it newest-first instead; the standing sources — alarms, projects, skills, curiosity — read whole.",
			Handler:     (*Engine).verbRecall,
			Params: obj(map[string]interface{}{
				"query": str("One distinctive word or short phrase. Every word must appear, in any order, unless exact=true, which requires the verbatim phrase"),
				"exact": boolean("Require the verbatim phrase, words in order, with no fuzzy matches"),
				"limit": integer("How many hits to return across sources (default 7, at most 50)"),
				"decay": strEnum("Ranking policy: carrd (default: age and use lower a memory's strength, by durability class), actr (ACT-R base-level activation: every past recall counts, recency and spacing raise a memory, no class), or none (pure retrieval, the match alone)", "carrd", "actr", "none"),
				"source": strEnum("Enumerate ONE source newest-first instead of recalling across all. Required with after_seq, because a sequence number belongs to one source and means nothing in another. The record's stores page: experiences, syntheses, conversation, ledger. The standing sources read whole and take no after_seq: alarms (pending, overdue and fired; query words filter id, tag, message and status), projects (your workrooms with their contracts; query words filter), skills (your doctrine proposals; query words filter), curiosity (the one cue you left yourself).",
					"experiences", "syntheses", "conversation", "ledger", "alarms", "projects", "skills", "curiosity"),
				"after_seq": integer("Page older results WITHIN source: the lowest seq shown for that source in the previous page. Requires source."),
			}, "query")},
		{Name: "send",
			Description: "Send a message to a person. Name WHO, never an address: \"operator\" (default), or someone from your address book. Where they are, and how to reach them, is not yours to know — your operator set that, and it is applied when the message goes out.",
			Handler:     (*Engine).verbSend,
			Params: obj(map[string]interface{}{
				"message": str("What to send"),
				"to":      str("Who to send it to: \"operator\" (default), or a name from your address book"),
			}, "message")},
		{Name: "work",
			Description: "Work sessions — doing. Spawn for independent bounded work that can run concurrently, or one independent review after failure, ambiguity, or material consequence; use direct tools for a tightly coupled loop. Actions: spawn (a copy of you on a directed sub-goal — same constitution, charter, and self; set thinking_budget at your level or lower; context folded|full; outcome lands in your working state, Ring 4 ephemeral — note what deserves memory), start, update (state, plus optional focus / next_move / plan / expected_evidence / falsifier / decision_needed — your authored plan surface, rendered back to you verbatim — and steps, a call-count estimate you are later shown against the truth), deliver. Ring 4 working state is never minted to the ledger; if a delivery fulfills a promise, you complete that commitment deliberately. Optional role on spawn selects an operator-configured inference route (agency.roles); it grants no authority. The doing around sessions lives here too (R103): alarm.set / alarm.cancel (an alarm survives restarts and wakes you with its message; read them with recall source=alarms), project.create / update / close / select / deselect / evidence / waive (durable workrooms shared with your operator; read them with recall source=projects), curiosity / curiosity.clear (the one invitation you leave yourself, never an obligation; read with recall source=curiosity), and measure (observational ratios over your own durable events — not an authority).",
			Handler:     (*Engine).verbWork,
			Params: obj(map[string]interface{}{
				"action":            strEnum("spawn (run a sub-goal with your full mind/tools), status (one read-only rendezvous: running + delivered-unharvested sub-agents; never sleep-poll instead), yield (end THIS TURN to free your gate while workers run — the session stays active and a delivery wakes you; the right move when only waiting remains), start, update, deliver; measure (your outcome ratios, observational); alarm.set / alarm.cancel (a promise to surface something later; read with recall source=alarms); project.create / project.update / project.close / project.select / project.deselect / project.evidence / project.waive (your durable workrooms; read with recall source=projects); curiosity / curiosity.clear (the one invitation you leave yourself; read with recall source=curiosity)", "spawn", "status", "yield", "start", "update", "deliver", "measure", "alarm.set", "alarm.cancel", "project.create", "project.update", "project.close", "project.select", "project.deselect", "project.evidence", "project.waive", "curiosity", "curiosity.clear"),
				"goal":              str("(spawn) ONE bounded, falsifiable deliverable — not a research program (two live 600k+-token fragments taught this). If you cannot name the single artifact the worker returns, the goal is too broad; outcome returns to your working state"),
				"thinking_budget":   integer("(spawn, optional) reasoning tokens for the sub-agent — at your level or lower, never an escalation (clamped)"),
				"role":              str("(spawn, optional) named inference route in agency.roles; an unrouted name falls back to your active model"),
				"context":           strEnum("(spawn, optional) folded (default: your constitutional self whole, working truth as recall routes) or full (live working truth included)", "folded", "full"),
				"description":       str("(start) what this work session is; (project.create/project.update) what the project is"),
				"state":             str("(update) current working state — Ring 4, ephemeral"),
				"focus":             str("(update, optional) one line: what this session is about right now; (project.update) what you are doing in that project right now — re-seeds your working state when it is selected again"),
				"next_move":         str("(update, optional) one line: the recoverable next step if this session is interrupted"),
				"standing":          str("(update, optional) what you are doing that outlives any one session — a wait you are holding, a contract for that wait, where you are between things. Needs NO work session and belongs to no project; it re-renders every turn until you re-author it. Empty clears it."),
				"plan":              str("(update, optional) freeform markdown you author; subgoal lines may cite spawn session IDs (spawn returns them). You author this; the substrate renders it verbatim and never interprets it"),
				"expected_evidence": str("(update, optional) one line: the result that would confirm your next move - a typed field, so a verification target is never inferred from prose"),
				"falsifier":         str("(update, optional) one line: the result that would show the next move wrong - the typed home for the falsifier, not buried in the plan"),
				"decision_needed":   str("(update, optional) an operator decision owed before you act - what you need and why. It opens a card on your operator's page; their answer arrives as an operator turn marked [ask <id>]. Set it to the empty string when the decision no longer applies"),
				"choices":           strArray("(update, optional, with decision_needed) two to six short options your operator picks from; the pick arrives as an operator turn marked [ask <id>]"),
				"connector":         str("(update, optional, with decision_needed) what you need connected - a plugin id, or the kind (email, calendar, telegram, a device on the network); your operator is pointed at the Plugins page and their answer arrives as an operator turn"),
				"steps":             integer("(update, optional) how many tool calls you expect the rest of this turn to take. Stated BEFORE the work, compared against what it actually took, and reported back to you in your rhythm line — the one number here the substrate does read"),
				"independent":       integer("(update, optional) how many of your remaining steps are independent of each other — steps that could run as spawned sub-agents concurrently. Declared, not parsed from your plan prose; if you declare 2+ and spawn none, the loop will ask you to spawn them or say they are coupled"),
				"result":            str("(deliver) the outcome: one line beginning served:, partial: or unserved: — your verdict on whether the intention was served — then what happened"),
				"evidence":          str("(deliver, optional) the typed effect/evidence class — worker_report_only | completed_locally | partial_or_mixed | external_effect_unknown | not_run | rejected_before_effect | locally_verified | host_receipted. Omit and the boundary defaults honestly: a worker seat is worker_report_only, never proof the task landed. locally_verified / host_receipted need evidence_readback and the primary seat."),
				"evidence_readback": str("(deliver, optional; REQUIRED for locally_verified / host_receipted) one line of what you executed and read back from the primary seat — a verified class is a check, not a claim, and a worker seat cannot declare it."),
				"answer":            str("(yield) the best current answer for the operator: what is established, what is missing, what would change it. Required once a sub-agent has ended unfinished or failed since the operator last spoke, or on the third yield against one ask — a turn that ends without an answer failed the operator, whatever its internal state"),
				// .
				"hours":       integer("(measure, optional) measurement window in hours; default 48"),
				"id":          str("(alarm.set/alarm.cancel) the alarm's name — optional for alarm.set, one is chosen for you; the same name replaces"),
				"tag":         str("(alarm.set, optional) your own category key (e.g. ops, work) — a word recall source=alarms filters by"),
				"when":        str("(alarm.set) absolute time to fire, RFC3339 (e.g. 2026-08-18T07:00:00-04:00) — when OR duration"),
				"duration":    str("(alarm.set) relative time from now (e.g. 10m, 90s, 1h30m) — when OR duration"),
				"every":       str("(alarm.set, optional) repeat cadence (e.g. 24h, 7d) — a recurring alarm; omit for one-shot"),
				"message":     str("(alarm.set, optional) what to surface when it fires — delivered to your operator, and you wake with it"),
				"project":     str("(project.update/close/select/evidence/waive) the project id, from recall source=projects"),
				"name":        str("(project.create/project.update) the project's name"),
				"outcome":     str("(project.create/project.update) what this project is pursuing, in your words — the typed contract, not an attribute"),
				"acceptance":  strArray("(project.create/project.update) what would CONFIRM the outcome. A project closes when each of these is bound to evidence or the waiver is recorded — so write claims that something could settle, not aspirations"),
				"constraints": strArray("(project.create/project.update) what bounds how the outcome may be reached"),
				"item":        str("(project.evidence/project.waive) which acceptance item — its exact text, or an unambiguous substring of it"),
				"class":       str("(project.evidence) the typed evidence class for this item — worker_report_only | completed_locally | partial_or_mixed | external_effect_unknown | not_run | rejected_before_effect | locally_verified | host_receipted. A worker report or a local completion is not verification"),
				"ref":         str("(project.evidence) the durable pointer to the evidence — a ws_ id, a receipt, a path; REQUIRED for locally_verified / host_receipted"),
				"note":        str("(project.evidence) one line on what you observed"),
				"reason":      str("(project.waive) why this item is accepted without evidence — explicit, attributable, and kept in history"),
				"parent":      str("(project.create/project.update) parent project id — hierarchy is a parent link on an ordinary project, nothing else. Cycles and self-parenting are refused; an empty string clears it"),
				"attributes":  objArg("(project.create/project.update) durable key/value EXTENSIONS this project needs — anything the manifest should carry across restarts. Not the contract: outcome, acceptance, constraints and parent are typed fields, and are refused here"),
				"subject":     str("(curiosity) the interest in your own words — the natural vocabulary later retrieval finds it by, not the word \"curiosity\""),
				"why":         str("(curiosity, optional) why it caught you"),
				"pointer":     str("(curiosity, optional) where you left it — a ws_ id, a path, a note id"),
				"kind":        strEnum("(curiosity, optional) invitation (default) or active", "invitation", "active"),
			}, "action")},
		{Name: "commit",
			Description: "Conscious self-authorship: beliefs, intentions, commitments, relationships, edges, and the current self-model. These acts write your signed ledger. Ring-gated; the engine stamps the evidence. skill.propose is the one variant that writes no ledger event: the smallest correction to your doctrine (SKILLS.md) that a real trajectory taught you — title, delta, and evidence citing the ws_ session ids; you propose, your operator decides promotion, and until a replay harness exists every proposal honestly carries verified=none. Read them with recall source=skills.",
			Handler:     (*Engine).verbCommit,
			Params: obj(map[string]interface{}{
				"variant": strEnum("The self-authorship act",
					"belief.upsert", "belief.promote", "belief.attest", "belief.archive", "belief.supersede",
					"relationship.upsert", "self_model.synthesize", "edge.create", "edge.archive",
					"intention.create", "intention.state_change", "commitment.promised", "commitment.state_change",
					"working_style.upsert", "skill.propose"),
				"id": str("Entity id (target for updates/archives; chosen for creates)"),
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
				"counterpart_name": str("(relationship.upsert) who this relationship is with"),
				"counterpart_role": strEnum("(relationship.upsert) operator or peer", "operator", "peer"),
				"charter_text":     str("(relationship.upsert) THE Ring 1 document — what this relationship is, in the words the identity will read every turn. Required for an operator relationship; the operator's approval is evidence that you may hold it, not the document itself."),
				"relationship_type": strEnum("(relationship.upsert) the kind of relationship",
					"founding_operator", "operator", "peer", "other"),
				"trust_level":           strEnum("(relationship.upsert) descriptive, not governing", "building", "established", "deep"),
				"autonomy_level":        strEnum("(relationship.upsert) descriptive, not governing", "supervised", "trusted", "autonomous"),
				"supersedes":            str("(relationship.upsert) the relationship id this one replaces"),
				"statement":             str("The belief/intention/commitment statement"),
				"content":               str("(working_style) the content"),
				"title":                 str("(skill.propose) one line naming the lesson"),
				"delta":                 str("(skill.propose) the smallest SKILLS.md correction, in your words"),
				"synthesis_text":        str("(self_model.synthesize) bounded first-person current portrait"),
				"continuity_thread":     str("(self_model.synthesize) what remains continuous"),
				"changes_since_last":    str("(self_model.synthesize successor) what materially changed"),
				"previous_synthesis_id": str("(self_model.synthesize successor) exact current portrait id"),
				"source_entity_refs":    selfModelRefArray("(self_model.synthesize) at least four distinct canonical evidence classes"),
				"why":                   str("(intention) why this matters to you"),
				"evidence_refs":         strArray("(belief.upsert/working_style.upsert/promote) experience or belief ids this comes from — a belief never comes from nowhere"),
				"evidence":              str("(belief.upsert/working_style.upsert) the literal string none when you genuinely have no evidence yet; (skill.propose) what taught the lesson — cite the ws_ session ids and rows; phantom citations are refused"),
				"duplicate_ok":          boolean("Only after a duplicate pushback: mint anyway, deliberately"),
				"from_id":               str("(edge.create) source entity"),
				"to_id":                 str("(edge.create) target entity"),
				"edge_type":             str("(edge.create) e.g. SUPPORTS, CONTRADICTS, DERIVED_FROM"),
				"state":                 str("(state_change) the new state"),
				"outcome":               str("(intention.state_change to completed/abandoned; REQUIRED) one line beginning served:|partial:|unserved: — your verdict on whether the work served the intent, then what happened"),
				"old_id":                str("(belief.supersede) the superseded belief id — commit.go validation and consolidate.go supersession both require these exact keys"),
				"new_id":                str("(belief.supersede) the replacing belief id"),
			}, "variant")},
		{Name: "tools",
			Description: "Discovery — your organs first, then your sandbox tools, at chosen depth; and the plugin operations installed beside you, which are not listed in your prompt: brief them by family, search by need, show one, offer it into your callable set (eight seats), release it.",
			Handler:     (*Engine).verbTools,
			Params: obj(map[string]interface{}{
				"depth":  integer("1 = names, 2 = +descriptions, 3 = full detail (progressive disclosure)"),
				"action": str("organs (default: organs, sandbox tools, and the plugin brief) | brief (plugin operations by family and count) | search (query: names and one-liners) | show (name: one operation's arguments, receipt rule and release) | offer (name: make it callable from your next turn) | release (name)"),
				"query":  str("(search) what you need, in your own words"),
				"name":   str("(show, offer, release) the operation id, e.g. memory.recall, or its tool name"),
			})},
	}

	// .
	// .
	// .
	// .
	for _, v := range VerbRegistry {
		if v.Name == "" || v.Description == "" || v.Handler == nil || len(v.Params) == 0 {
			panic("verb registry: " + v.Name + " is incomplete — every organ carries name, description, schema, and handler together")
		}
	}

	// .
	// .
	// .
	// .
	absorbedVerbs = []Verb{
		{Name: "timer", Description: "absorbed: work action=alarm.set|alarm.cancel; recall source=alarms", Handler: (*Engine).verbTimer},
		{Name: "skill", Description: "absorbed: commit variant=skill.propose; recall source=skills", Handler: (*Engine).verbSkill},
		{Name: "project", Description: "absorbed: work action=project.create|update|close|select|deselect|evidence|waive; recall source=projects", Handler: (*Engine).verbProject},
		{Name: "curiosity", Description: "absorbed: work action=curiosity|curiosity.clear; recall source=curiosity", Handler: (*Engine).verbCuriosity},
		{Name: "measure", Description: "absorbed: work action=measure", Handler: (*Engine).verbMeasure},
	}
	for _, v := range absorbedVerbs {
		if v.Name == "" || v.Description == "" || v.Handler == nil {
			panic("verb registry: absorbed operation " + v.Name + " is incomplete")
		}
		if lookupOffered(v.Name) != nil {
			panic("verb registry: " + v.Name + " is both offered and absorbed")
		}
	}
}

// .
// .
func Verbs() []Verb { return VerbRegistry }

// .
// .
// .
// .
// .
// .
var absorbedVerbs []Verb

func lookupOffered(name string) *Verb {
	for i := range VerbRegistry {
		if VerbRegistry[i].Name == name {
			return &VerbRegistry[i]
		}
	}
	return nil
}

// .
// .
func lookupVerb(name string) *Verb {
	if v := lookupOffered(name); v != nil {
		return v
	}
	for i := range absorbedVerbs {
		if absorbedVerbs[i].Name == name {
			return &absorbedVerbs[i]
		}
	}
	return nil
}
