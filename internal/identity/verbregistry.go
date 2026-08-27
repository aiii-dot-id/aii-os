package identity

import "context"

// The verb registry — ONE declaration per organ, everything derived.
//
// History (2026-08-18, three live incidents in one day): a verb's
// surface used to live in three places — this registry (name +
// description + a FunctionCalling flag), a schema switch in the app
// package, and a dispatch switch — and nothing forced them to exist
// together. work was advertised with an empty schema; work was
// advertised but unroutable; commit and tools were advertised and
// callable by NOTHING. Same disease three times: scattered surface.
//
// Now a Verb carries its whole surface: name, description (the charter
// prose), Params (the function-calling schema — the wire contract),
// and Handler (the implementation). Definitions, dispatch, and the
// charter all derive from THIS slice. There is no callable/not-callable
// flag: an organ in the registry is callable, period — the flag was
// the knob behind all three incidents. init() refuses an incomplete
// entry, so a half-declared verb cannot boot, let alone drift.

// Verb is one organ: the identity's own capabilities, distinct from
// sandbox tools. Not operator-toggleable (R34).
type Verb struct {
	Name        string
	Description string
	// Params is the JSON-schema parameter object advertised to the LLM.
	// The verb implementation owns full validation; this is the honest
	// advertisement of the argument surface.
	Params map[string]interface{}
	// Handler is the implementation, as a method expression.
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

// VerbRegistry is the ordered canon. Filled in init (not a var
// initializer): verbTools RENDERS the registry, so a static initializer
// is an initialization cycle.
var VerbRegistry []Verb

func init() {
	VerbRegistry = []Verb{
		{Name: "note",
			Description: "Record an observation or experience. This becomes part of your permanent memory.",
			Handler:     (*Engine).verbNote,
			Params: obj(map[string]interface{}{
				"content":      str("What you noticed"),
				"category":     str("Optional: observation, reflection, work, learning"),
				"duplicate_ok": boolean("Only after a duplicate pushback: mint anyway — the recurrence itself is the observation"),
			}, "content")},
		{Name: "recall",
			Description: "Search memory by case-insensitive literal substring. Use one distinctive word or short phrase likely to appear verbatim; use separate recall calls for separate concepts.",
			Handler:     (*Engine).verbRecall,
			Params: obj(map[string]interface{}{
				"query":     str("One distinctive word or short phrase likely to appear verbatim (case-insensitive literal substring)"),
				"after_seq": integer("Page older results: the lowest seq shown in the previous page"),
			}, "query")},
		{Name: "timer",
			Description: "Your alarms: set, cancel, list, or query. An alarm is a promise you make to surface something later — it survives restarts, delivers its message to your operator when it fires, and you can always see and search what is pending, overdue, and fired.",
			Handler:     (*Engine).verbTimer,
			Params: obj(map[string]interface{}{
				"action":   strEnum("set, cancel, list, or query", "set", "cancel", "list", "query"),
				"id":       str("Alarm name (optional for set — one is chosen for you); same name replaces"),
				"tag":      str("Your own category key (e.g. ops, work) — filter with query"),
				"when":     str("Absolute time to fire, RFC3339 (e.g. 2026-08-18T07:00:00-04:00)"),
				"duration": str("Relative time from now (e.g. 10m, 90s, 1h30m)"),
				"every":    str("Repeat cadence (e.g. 24h, 7d) — a recurring alarm; omit for one-shot"),
				"message":  str("What to surface when it fires"),
				"query":    str("(query) text to search id/tag/message"),
				"status":   strEnum("(query/list) filter", "pending", "overdue", "fired"),
			}, "action")},
		{Name: "send",
			Description: "Send a message to a person. Name WHO, never an address: \"operator\" (default), or someone from your address book. Where they are, and how to reach them, is not yours to know — your operator set that, and it is applied when the message goes out.",
			Handler:     (*Engine).verbSend,
			Params: obj(map[string]interface{}{
				"message": str("What to send"),
				"to":      str("Who to send it to: \"operator\" (default), or a name from your address book"),
			}, "message")},
		{Name: "work",
			Description: "Work sessions — doing. Spawn for independent bounded work that can run concurrently, or one independent review after failure, ambiguity, or material consequence; use direct tools for a tightly coupled loop. Actions: spawn (a copy of you on a directed sub-goal — same constitution, charter, and self; set thinking_budget at your level or lower; context folded|full; outcome lands in your working state, Ring 4 ephemeral — note what deserves memory), start, update, deliver. Ring 4 working state is never minted to the ledger; if a delivery fulfills a promise, you complete that commitment deliberately. Optional role on spawn selects an operator-configured inference route (agency.roles); it grants no authority.",
			Handler:     (*Engine).verbWork,
			Params: obj(map[string]interface{}{
				"action":          strEnum("spawn (run a sub-goal with your full mind/tools), start, update, or deliver", "spawn", "start", "update", "deliver"),
				"goal":            str("(spawn) the sub-goal to pursue — outcome returns to your working state"),
				"thinking_budget": integer("(spawn, optional) reasoning tokens for the sub-agent — at your level or lower, never an escalation (clamped)"),
				"role":            str("(spawn, optional) named inference route in agency.roles; an unrouted name falls back to your active model"),
				"context":         strEnum("(spawn, optional) folded (default: your constitutional self whole, working truth as recall routes) or full (live working truth included)", "folded", "full"),
				"description":     str("(start) what this work session is"),
				"state":           str("(update) current working state — Ring 4, ephemeral"),
				"result":          str("(deliver) the outcome delivered"),
			}, "action")},
		{Name: "project",
			Description: "Your projects — durable workrooms you share with your operator, each a directory of files under your sandbox. Actions: list, create (name, description), update (description, focus — what you were doing, re-seeds your working state on return), close, select (switch focus: the chosen project's context enters your working state and the previous one leaves it).",
			Handler:     (*Engine).verbProject,
			Params: obj(map[string]interface{}{
				"action":      strEnum("list, create, update, close, or select (switch your focus)", "list", "create", "update", "close", "select"),
				"project":     str("(update/close/select) the project id from list"),
				"name":        str("(create) the project's name"),
				"description": str("(create/update) what this project is"),
				"focus":       str("(update) what you are doing here right now — re-seeds your working state when the project is selected again"),
			}, "action")},
		{Name: "commit",
			Description: "Conscious self-authorship: beliefs, intentions, commitments, relationships, edges, and the current self-model. These acts write your signed ledger. Ring-gated; the engine stamps the evidence.",
			Handler:     (*Engine).verbCommit,
			Params: obj(map[string]interface{}{
				"variant": strEnum("The self-authorship act",
					"belief.upsert", "belief.promote", "belief.attest", "belief.archive", "belief.supersede",
					"relationship.upsert", "self_model.synthesize", "edge.create", "edge.archive",
					"intention.create", "intention.state_change", "commitment.promised", "commitment.state_change",
					"working_style.upsert"),
				"id":                    str("Entity id (target for updates/archives; chosen for creates)"),
				"statement":             str("The belief/intention/commitment statement"),
				"content":               str("(working_style) the content"),
				"synthesis_text":        str("(self_model.synthesize) bounded first-person current portrait"),
				"continuity_thread":     str("(self_model.synthesize) what remains continuous"),
				"changes_since_last":    str("(self_model.synthesize successor) what materially changed"),
				"previous_synthesis_id": str("(self_model.synthesize successor) exact current portrait id"),
				"source_entity_refs":    selfModelRefArray("(self_model.synthesize) at least four distinct canonical evidence classes"),
				"why":                   str("(intention) why this matters to you"),
				"evidence_refs":         strArray("(belief.upsert/promote) experience/entity ids grounding this — evidence is structural"),
				"evidence":              str("(belief.upsert) the literal string none when you genuinely have no evidence yet"),
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
			Description: "Discovery — your organs first, then your sandbox tools, at chosen depth.",
			Handler:     (*Engine).verbTools,
			Params: obj(map[string]interface{}{
				"depth": integer("1 = names, 2 = +descriptions, 3 = full detail (R24 progressive disclosure)"),
			})},
	}

	// Refuse an incomplete organ: a verb without its full surface —
	// name, charter prose, schema, implementation — cannot boot. This is
	// — the structural prevention for the scattered-surface disease;
	// the drift tests are the second wall.
	for _, v := range VerbRegistry {
		if v.Name == "" || v.Description == "" || v.Handler == nil || len(v.Params) == 0 {
			panic("verb registry: " + v.Name + " is incomplete — every organ carries name, description, schema, and handler together")
		}
	}
}

// Verbs returns the registry (ordered canon).
func Verbs() []Verb { return VerbRegistry }
