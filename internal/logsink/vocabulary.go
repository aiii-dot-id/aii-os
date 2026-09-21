package logsink

import (
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
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
type Aspect string

const (
	AspectPrompt   Aspect = "prompt"
	AspectReturn   Aspect = "return"
	AspectDecision Aspect = "decision"
	AspectSession  Aspect = "session"
	AspectPass     Aspect = "pass"
	AspectRenewal  Aspect = "renewal"
	AspectStart    Aspect = "start"
	AspectEnd      Aspect = "end"
	AspectRefusal  Aspect = "refusal"
	AspectError    Aspect = "error"
	AspectBudget   Aspect = "budget"
)

var aspects = []Aspect{
	AspectPrompt, AspectReturn, AspectDecision, AspectSession, AspectPass,
	AspectRenewal, AspectStart, AspectEnd, AspectRefusal, AspectError, AspectBudget,
}

// .
type Detail struct {
	Subsystem string
	Aspect    Aspect

	// .
	// .
	// .
	// .
	// .
	Payload bool

	// .
	What string
}

// .
func (d Detail) Name() string { return d.Subsystem + "." + string(d.Aspect) }

// .
// .
// .
var declared = []Detail{
	{Subsystem: "rhythm", Aspect: AspectPass, What: "a rhythm pass ran, and whether anything was due"},
	{Subsystem: "route", Aspect: AspectRenewal, What: "the public name's lease was renewed"},
	{Subsystem: "workq", Aspect: AspectEnd, What: "a queued alarm finished"},
	{Subsystem: "logs", Aspect: AspectError, What: "the logging facility could not do as it was told"},
	{Subsystem: "logs", Aspect: AspectDecision, What: "a tap began or ended recording payloads, and the expiry it was given"},

	// .
	{Subsystem: "turn", Aspect: AspectBudget, What: "a turn met a limit: tokens, calls, or context pressure"},
	{Subsystem: "turn", Aspect: AspectDecision, What: "the turn decided something about the model's output"},
	{Subsystem: "llm", Aspect: AspectReturn, What: "a model answered: finish reason, tool calls, content length"},
	{Subsystem: "llm", Aspect: AspectPrompt, Payload: true, What: "the raw request sent to a model — an artifact, captured only by a tap and never written to aii.log"},
	{Subsystem: "tool", Aspect: AspectStart, What: "a tool call was dispatched"},
	{Subsystem: "tool", Aspect: AspectEnd, What: "a tool call returned"},
	{Subsystem: "nudge", Aspect: AspectDecision, What: "the loop corrected the model mid-turn, and why"},

	// .
	{Subsystem: "dream", Aspect: AspectPass, What: "a dream pass ran, and what it surfaced"},
	{Subsystem: "dream", Aspect: AspectEnd, What: "a dream pass minted a surfacing"},
	{Subsystem: "dream", Aspect: AspectRefusal, What: "a dream pass was refused, and what was left unprocessed"},
	{Subsystem: "dream", Aspect: AspectBudget, What: "a dream pass met the model's input limit and read less conversation, or none"},
	{Subsystem: "dream", Aspect: AspectError, What: "a dream pass faulted"},
	{Subsystem: "selfmodel", Aspect: AspectPass, What: "a self-model pass ran with nothing to change"},
	{Subsystem: "selfmodel", Aspect: AspectEnd, What: "the self-model was committed"},
	{Subsystem: "selfmodel", Aspect: AspectDecision, What: "the self-model pass chose a corrective round"},
	{Subsystem: "selfmodel", Aspect: AspectRefusal, What: "a self-model pass was refused, and why"},
	{Subsystem: "selfmodel", Aspect: AspectError, What: "a self-model pass faulted"},
	{Subsystem: "brief", Aspect: AspectEnd, What: "the morning brief was written"},
	{Subsystem: "brief", Aspect: AspectRefusal, What: "the brief was deferred rather than written"},
	{Subsystem: "brief", Aspect: AspectError, What: "the brief faulted, and what it went without"},
	{Subsystem: "review", Aspect: AspectPass, What: "an identity review found nothing to look at"},
	{Subsystem: "review", Aspect: AspectDecision, What: "an identity review found something to look at"},
	{Subsystem: "review", Aspect: AspectError, What: "an identity review faulted"},
	{Subsystem: "ring", Aspect: AspectError, What: "a ring section could not be persisted"},
	{Subsystem: "consolidate", Aspect: AspectEnd, What: "a consolidation pass minted, and what it wrote"},
	{Subsystem: "consolidate", Aspect: AspectDecision, What: "a consolidation pass chose a path, and why"},
	{Subsystem: "consolidate", Aspect: AspectRefusal, What: "an operation or a whole pass was refused, and why"},
	{Subsystem: "consolidate", Aspect: AspectBudget, What: "a consolidation pass met a bound and was clamped"},
	{Subsystem: "consolidate", Aspect: AspectError, What: "a consolidation pass faulted"},
	{Subsystem: "time", Aspect: AspectDecision, What: "an alarm was retired, deferred or preserved"},
	{Subsystem: "time", Aspect: AspectRefusal, What: "an alarm was not run, and why"},
	{Subsystem: "time", Aspect: AspectError, What: "the clock, a wake or an alarm owner faulted"},
	{Subsystem: "rhythm", Aspect: AspectRefusal, What: "a rhythm pass was deferred or an alarm was unknown"},
	{Subsystem: "rhythm", Aspect: AspectDecision, What: "the rhythm nominated something to attend to"},
	{Subsystem: "rhythm", Aspect: AspectEnd, What: "the rhythm minted an attention brief"},
	{Subsystem: "rhythm", Aspect: AspectError, What: "a rhythm read faulted"},
	{Subsystem: "workq", Aspect: AspectDecision, What: "the queue swept expired leases"},
	{Subsystem: "workq", Aspect: AspectRefusal, What: "an item was refused: no handler, or fail-fast"},
	{Subsystem: "workq", Aspect: AspectError, What: "a queue operation or a handler faulted"},

	// .
	{Subsystem: "store", Aspect: AspectDecision, What: "the schema was reconciled, or a sidecar rebuilt"},
	{Subsystem: "store", Aspect: AspectEnd, What: "a store close step finished, or gave up"},
	{Subsystem: "store", Aspect: AspectError, What: "a store operation faulted"},
	{Subsystem: "ledger", Aspect: AspectError, What: "the ledger file was not what it should be"},
	{Subsystem: "witness", Aspect: AspectEnd, What: "the ledger was anchored, and through which record"},
	{Subsystem: "witness", Aspect: AspectDecision, What: "the anchor cadence was set by the server, not by us"},
	{Subsystem: "witness", Aspect: AspectRefusal, What: "anchoring was refused or latched off, and why"},
	{Subsystem: "witness", Aspect: AspectError, What: "an anchor step faulted"},
	{Subsystem: "note", Aspect: AspectRefusal, What: "an evidence edge or a mark was refused"},
	{Subsystem: "note", Aspect: AspectError, What: "a note could not be written where it had to be"},
	{Subsystem: "timer", Aspect: AspectRefusal, What: "a wake was suppressed as a duplicate"},
	{Subsystem: "timer", Aspect: AspectError, What: "a wake faulted"},
	{Subsystem: "llm", Aspect: AspectRefusal, What: "a provider or a request field was refused"},
	{Subsystem: "llm", Aspect: AspectBudget, What: "what a call cost"},
	{Subsystem: "llm", Aspect: AspectError, What: "a call failed or had to be retried"},
	// .
	{Subsystem: "boot", Aspect: AspectStart, What: "what this identity is, and where it lives"},
	{Subsystem: "boot", Aspect: AspectEnd, What: "the runtime is shutting down"},
	{Subsystem: "boot", Aspect: AspectRefusal, What: "boot refused something, or entered BOOT-SAFE, and why"},
	{Subsystem: "boot", Aspect: AspectError, What: "a boot or shutdown step faulted"},
	{Subsystem: "ring", Aspect: AspectStart, What: "a ring was loaded or restored"},
	{Subsystem: "ring", Aspect: AspectDecision, What: "a ring's contents were narrowed by configuration"},
	{Subsystem: "ring", Aspect: AspectRefusal, What: "a ring could not be loaded"},
	{Subsystem: "steering", Aspect: AspectDecision, What: "leftover operator words opened their own turn"},
	{Subsystem: "steering", Aspect: AspectRefusal, What: "operator words arrived with nowhere to go"},
	{Subsystem: "steering", Aspect: AspectError, What: "an operator turn could not be recorded"},
	{Subsystem: "updates", Aspect: AspectDecision, What: "what the updater decided about this host"},
	{Subsystem: "plugins", Aspect: AspectRefusal, What: "a plugin or the whole facility was refused"},
	{Subsystem: "dev", Aspect: AspectDecision, What: "a dev section is served, and under what terms"},
	{Subsystem: "dev", Aspect: AspectRefusal, What: "a dev section was refused, and why"},
	{Subsystem: "project", Aspect: AspectDecision, What: "the focused project changed, or was dropped"},
	{Subsystem: "route", Aspect: AspectRefusal, What: "the public name could not be served as asked"},
	{Subsystem: "work", Aspect: AspectDecision, What: "work sessions were swept or closed"},
	{Subsystem: "work", Aspect: AspectError, What: "a work-session operation faulted"},
	{Subsystem: "rhythm", Aspect: AspectStart, What: "the cognitive rhythm was armed, and at what cadence"},
	{Subsystem: "wake", Aspect: AspectDecision, What: "a timed wake chose how to reach the operator"},
	{Subsystem: "wake", Aspect: AspectEnd, What: "a timed wake took its turn and spoke"},
	{Subsystem: "wake", Aspect: AspectError, What: "a timed wake could not take its turn"},
	{Subsystem: "harvest", Aspect: AspectDecision, What: "a delivery was left unharvested and is being woken for"},
	{Subsystem: "continuation", Aspect: AspectEnd, What: "a continuation leg spoke"},
	{Subsystem: "continuation", Aspect: AspectRefusal, What: "a continuation was refused or stood down"},
	{Subsystem: "continuation", Aspect: AspectError, What: "a continuation leg faulted"},

	// .
	{Subsystem: "updates", Aspect: AspectEnd, What: "what version is running, and what happened to an update"},
	{Subsystem: "updates", Aspect: AspectRefusal, What: "an update or a rollback was refused, and why"},
	{Subsystem: "updates", Aspect: AspectError, What: "an update check, apply or rollback faulted"},
	{Subsystem: "dashboard", Aspect: AspectDecision, What: "how the dashboard is served, and on what terms"},
	{Subsystem: "dashboard", Aspect: AspectEnd, What: "a turn taken through the dashboard finished"},
	{Subsystem: "dashboard", Aspect: AspectRefusal, What: "a request or a session was refused, and why"},
	{Subsystem: "dashboard", Aspect: AspectError, What: "a listener, a socket or a write faulted"},
	{Subsystem: "voice", Aspect: AspectSession, What: "a voice session opened, changed or ended"},
	{Subsystem: "voice", Aspect: AspectError, What: "speech could not be heard or carried"},

	// .
	{Subsystem: "subagent", Aspect: AspectEnd, What: "a sub-agent leg finished, and how it ended"},
	{Subsystem: "subagent", Aspect: AspectDecision, What: "where a sub-agent role was routed, and why"},
	{Subsystem: "subagent", Aspect: AspectRefusal, What: "a leg was not run, and why"},
	{Subsystem: "subagent", Aspect: AspectError, What: "a sub-agent run or its delivery faulted"},
	{Subsystem: "channel", Aspect: AspectStart, What: "a channel began listening, and by what route"},
	{Subsystem: "channel", Aspect: AspectEnd, What: "a channel stopped listening"},
	{Subsystem: "channel", Aspect: AspectBudget, What: "a receive outran its budget"},
	{Subsystem: "channel", Aspect: AspectRefusal, What: "an adapter, a claim or an arrival was refused"},
	{Subsystem: "channel", Aspect: AspectError, What: "a channel receive or record faulted"},
	{Subsystem: "outbox", Aspect: AspectEnd, What: "what the outbox carried"},
	{Subsystem: "outbox", Aspect: AspectRefusal, What: "the outbox held messages rather than sending them"},
	{Subsystem: "outbox", Aspect: AspectError, What: "an outbox send faulted"},

	// .
	{Subsystem: "genesis", Aspect: AspectStart, What: "a ring or packet is being fetched, or a firstboot begins"},
	{Subsystem: "genesis", Aspect: AspectEnd, What: "a ring or packet verified, or an identity was born"},
	{Subsystem: "genesis", Aspect: AspectRefusal, What: "birth will refuse, and what is missing"},
	{Subsystem: "genesis", Aspect: AspectError, What: "a genesis fetch or verification failed"},
	{Subsystem: "config", Aspect: AspectStart, What: "the configuration was created or first loaded"},
	{Subsystem: "config", Aspect: AspectDecision, What: "what a reload applied live, and what waits for a boot"},
	{Subsystem: "config", Aspect: AspectRefusal, What: "a reload was refused or superseded, and why"},
	{Subsystem: "config", Aspect: AspectError, What: "the configuration could not be read"},
	{Subsystem: "route", Aspect: AspectStart, What: "the public name is served, and by what certificate"},
	{Subsystem: "route", Aspect: AspectDecision, What: "the public name moved"},
	{Subsystem: "route", Aspect: AspectError, What: "a certificate, relay or alarm step faulted"},
	{Subsystem: "theme", Aspect: AspectStart, What: "which theme the screen is painted with"},
	{Subsystem: "theme", Aspect: AspectDecision, What: "a token was kept but does nothing"},
	{Subsystem: "theme", Aspect: AspectRefusal, What: "a theme was refused and the current one kept"},
	{Subsystem: "theme", Aspect: AspectError, What: "a theme could not be read or re-encoded"},
	{Subsystem: "voice", Aspect: AspectDecision, What: "who holds the voice, and what was hushed"},
	{Subsystem: "voice", Aspect: AspectRefusal, What: "a reply, a transcript or a session was refused"},

	// .
	{Subsystem: "plugins", Aspect: AspectStart, What: "a plugin or section activated, and in which lane"},
	{Subsystem: "plugins", Aspect: AspectEnd, What: "a plugin stopped or was released"},
	{Subsystem: "plugins", Aspect: AspectDecision, What: "what the facility chose about a package"},
	{Subsystem: "plugins", Aspect: AspectError, What: "a plugin lifecycle step faulted"},
	{Subsystem: "catalog", Aspect: AspectStart, What: "which catalog is in force, and from where"},
	{Subsystem: "catalog", Aspect: AspectEnd, What: "a catalog refresh, install or uninstall completed"},
	{Subsystem: "catalog", Aspect: AspectError, What: "a catalog could not be read, kept or refreshed"},
	{Subsystem: "act", Aspect: AspectStart, What: "a plugin act ran under standing confirmation"},
	{Subsystem: "act", Aspect: AspectEnd, What: "a confirmed act ran once"},
	{Subsystem: "act", Aspect: AspectDecision, What: "an act was proposed, or the operator said ALWAYS"},
	{Subsystem: "act", Aspect: AspectRefusal, What: "the operator denied an act, or it refused"},
	{Subsystem: "act", Aspect: AspectError, What: "an act could not be dispatched or recorded"},
	{Subsystem: "maintenance", Aspect: AspectEnd, What: "what was verified, copied and proved restorable"},
	{Subsystem: "maintenance", Aspect: AspectRefusal, What: "maintenance did not copy, and why"},
	{Subsystem: "maintenance", Aspect: AspectError, What: "a chain, copy or alert step failed"},
	{Subsystem: "prompt", Aspect: AspectError, What: "the turn's prompt went without something it wanted"},
	{Subsystem: "harvest", Aspect: AspectEnd, What: "what the harvest swept"},
	{Subsystem: "harvest", Aspect: AspectError, What: "a delivery could not be marked harvested"},
	{Subsystem: "seed", Aspect: AspectEnd, What: "a seeded document was written"},
	{Subsystem: "seed", Aspect: AspectDecision, What: "a document was left alone or retired, and why"},
	{Subsystem: "seed", Aspect: AspectError, What: "a seeded document could not be written durably"},

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	{Subsystem: "safe", Aspect: AspectStart, What: "the identity entered SAFE or BOOT-SAFE, and why"},
	{Subsystem: "safe", Aspect: AspectEnd, What: "a degraded condition cleared"},
	{Subsystem: "safe", Aspect: AspectDecision, What: "what SAFE is refusing while it holds"},
	{Subsystem: "safe", Aspect: AspectRefusal, What: "what SAFE could not install or resolve"},
	{Subsystem: "snapshot", Aspect: AspectEnd, What: "a snapshot key was made"},
	{Subsystem: "snapshot", Aspect: AspectRefusal, What: "snapshots stay unencrypted, and which step failed"},
	{Subsystem: "restore", Aspect: AspectEnd, What: "a restore was performed at boot: what is in place, and where what was live is set aside"},
	{Subsystem: "restore", Aspect: AspectDecision, What: "an interrupted restore is rolled forward from its journal"},
	{Subsystem: "restore", Aspect: AspectRefusal, What: "a restore that was asked for was not performed, and why; nothing was touched"},
	{Subsystem: "restore", Aspect: AspectError, What: "a move of a journalled restore failed; the journal stays for the next start"},
	{Subsystem: "escrow", Aspect: AspectEnd, What: "an escrow file was made for the person, or checked, from the dashboard"},
	{Subsystem: "escrow", Aspect: AspectError, What: "a key did not read, a seal failed, or a receipt could not be written"},
	{Subsystem: "layout", Aspect: AspectStart, What: "which layout the screen is built from"},
	{Subsystem: "layout", Aspect: AspectDecision, What: "a profile was kept but does nothing"},
	{Subsystem: "layout", Aspect: AspectRefusal, What: "a layout was refused and the current one kept"},
	{Subsystem: "layout", Aspect: AspectError, What: "a layout could not be read or seeded"},
	{Subsystem: "harvest", Aspect: AspectRefusal, What: "a harvest wake stood down, and why"},

	// .
	{Subsystem: "ask", Aspect: AspectStart, What: "the identity asked the operator something"},
	{Subsystem: "ask", Aspect: AspectEnd, What: "an ask was withdrawn or settled"},
	{Subsystem: "ask", Aspect: AspectDecision, What: "an ask superseded another"},
	{Subsystem: "ask", Aspect: AspectError, What: "an answer or a grant could not be written"},
	{Subsystem: "webhook", Aspect: AspectRefusal, What: "a webhook was refused, and why the sender got nothing"},
	{Subsystem: "webhook", Aspect: AspectError, What: "a webhook operation or its record faulted"},

	// .
	// .
	{Subsystem: "relay", Aspect: AspectStart, What: "the relay registered this identity"},
	{Subsystem: "relay", Aspect: AspectRefusal, What: "a relay attach was not accepted"},
	{Subsystem: "relay", Aspect: AspectError, What: "a relay connection or attach faulted"},
	{Subsystem: "certs", Aspect: AspectDecision, What: "a stored certificate was kept or set aside"},
	{Subsystem: "certs", Aspect: AspectRefusal, What: "a stored certificate was not used, and why"},
	{Subsystem: "providers", Aspect: AspectStart, What: "the providers file was created or loaded"},
	{Subsystem: "providers", Aspect: AspectDecision, What: "what a provider was found to offer"},
	{Subsystem: "providers", Aspect: AspectError, What: "a provider could not be read or reached"},
	{Subsystem: "project", Aspect: AspectRefusal, What: "a project or an attribute was skipped, and why"},
	{Subsystem: "project", Aspect: AspectError, What: "a project's lineage could not be written"},
	{Subsystem: "oauth", Aspect: AspectEnd, What: "an auth profile was connected, disconnected or deleted"},
	{Subsystem: "oauth", Aspect: AspectDecision, What: "what an auth profile grants, and where"},
	{Subsystem: "oauth", Aspect: AspectError, What: "a revocation at the authority failed"},
	{Subsystem: "grade", Aspect: AspectEnd, What: "the operator graded a turn"},
	{Subsystem: "memory", Aspect: AspectEnd, What: "what a backfill pass landed"},
	{Subsystem: "memory", Aspect: AspectRefusal, What: "a backfill stopped early, and what stays"},
	{Subsystem: "turn", Aspect: AspectEnd, What: "what a turn spent and what it did"},
	{Subsystem: "turn", Aspect: AspectError, What: "a turn metric could not be recorded"},
	{Subsystem: "tool", Aspect: AspectDecision, What: "a tool call's arguments were repaired rather than refused"},
	{Subsystem: "tool", Aspect: AspectRefusal, What: "a tool call was refused before anything ran"},
	{Subsystem: "plugins", Aspect: AspectBudget, What: "a plugin's event queue filled and events were dropped"},
	{Subsystem: "prompt", Aspect: AspectBudget, What: "how the prompt was folded to fit"},
	{Subsystem: "logs", Aspect: AspectEnd, What: "what retention compressed or removed"},
	{Subsystem: "boot", Aspect: AspectDecision, What: "what the substrate decided about a tool at startup"},
	{Subsystem: "fsdir", Aspect: AspectRefusal, What: "a watcher has no event plane and is running on its heartbeat"},
	{Subsystem: "fsdir", Aspect: AspectError, What: "a watcher's event plane faulted"},
	{Subsystem: "tools", Aspect: AspectRefusal, What: "a grant was refused because of what it would expose"},
	{Subsystem: "tools", Aspect: AspectError, What: "a tool panicked on the arguments a model authored"},
	{Subsystem: "seed", Aspect: AspectRefusal, What: "a seed template was not what it claimed to be"},
}

// .
// .
// .
// .
// .
// .
// .
var index atomic.Pointer[map[string]Detail]

func init() { rebuildIndex() }

// .
// .
func rebuildIndex() {
	m := make(map[string]Detail, len(declared))
	for _, d := range declared {
		m[d.Name()] = d
	}
	index.Store(&m)
}

// .
// .
func byName() map[string]Detail { return *index.Load() }

// .
func Details() []Detail {
	out := append([]Detail(nil), declared...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// .
// .
// .
func Aspects() []Aspect { return append([]Aspect(nil), aspects...) }

// .
func IsDeclared(category string) bool {
	_, ok := byName()[category]
	return ok
}

// .
// .
// .
// .
func DerivedGroups() map[string][]string {
	g := map[string][]string{}
	for _, d := range declared {
		g[d.Subsystem] = append(g[d.Subsystem], d.Name())
		g[string(d.Aspect)] = append(g[string(d.Aspect)], d.Name())
	}
	for k := range g {
		sort.Strings(g[k])
	}
	return g
}

// .
// .
// .
// .
// .
// .
// .
// .
func definedGroupsFor(defined map[string][]string, detail string) []string {
	var names []string
	for name, members := range defined {
		for _, m := range members {
			if strings.EqualFold(strings.TrimSpace(m), detail) {
				names = append(names, name)
				break
			}
		}
	}
	sort.Slice(names, func(i, j int) bool {
		li, lj := len(defined[names[i]]), len(defined[names[j]])
		if li != lj {
			return li < lj
		}
		return names[i] < names[j]
	})
	return names
}

// .
// .
func ValidateDetailName(name string) error {
	name = strings.ToLower(strings.TrimSpace(name))
	sub, asp, found := strings.Cut(name, ".")
	if !found {
		return fmt.Errorf("%q is not a detail: a detail is subsystem.aspect (try `aii log details`)", name)
	}
	if sub == "" || asp == "" {
		return fmt.Errorf("%q is not a detail: both halves are needed", name)
	}
	known := false
	for _, a := range aspects {
		if Aspect(asp) == a {
			known = true
			break
		}
	}
	if !known {
		return fmt.Errorf("%q is not an aspect; the aspects are %s", asp, joinAspects())
	}
	if !IsDeclared(name) {
		return fmt.Errorf("%q is not declared: a detail is declared where it is emitted (internal/logsink/vocabulary.go)", name)
	}
	return nil
}

func joinAspects() string {
	parts := make([]string, 0, len(aspects))
	for _, a := range aspects {
		parts = append(parts, string(a))
	}
	return strings.Join(parts, ", ")
}
