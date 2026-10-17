package broker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
	"github.com/aiii-dot-id/aii-os/internal/memory"
	"github.com/aiii-dot-id/aii-os/internal/memory/trigram"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

const (
	opMemoryRemember = "memory.remember"
	opMemoryRecall   = "memory.recall"

	capRing4Memory = "ring4.memory"

	reasonMemoryNotFound          = "MEMORY_NOT_FOUND"
	reasonMemoryTextTooLarge      = "MEMORY_TEXT_TOO_LARGE"
	reasonMemoryQuotaExceeded     = "MEMORY_QUOTA_EXCEEDED"
	reasonMemorySourceUnavailable = "MEMORY_SOURCE_UNAVAILABLE"

	reinforceSimilarity = 0.92
	updateSimilarity    = 0.75

	DefaultMaxMemoryTextBytes = 16 << 10

	DefaultMaxMemories = 10000

	memoryTargetBytes = 80

	memoryRecallResultBytes = 256 << 10

	contextRecallResultBytes = 32 << 10
)

func (c Config) maxMemoryTextBytes() int {
	if c.MaxMemoryTextBytes > 0 {
		return c.MaxMemoryTextBytes
	}
	return DefaultMaxMemoryTextBytes
}

func (c Config) maxMemories() int {
	if c.MaxMemories > 0 {
		return c.MaxMemories
	}
	return DefaultMaxMemories
}

func (b *Binding) dispatchMemory(ctx context.Context, p invokeParams, g Grant) ([]byte, error) {
	if !b.envelopeHas(capRing4Memory) {
		return errorReply(rpcForbidden,
			fmt.Sprintf("%s is not in plugin %s's signed capability envelope", capRing4Memory, b.pluginID),
			&errorData{ReasonCode: reasonNotInEnvelope, DeniedAt: deniedAtCapEval})
	}
	if !g.Memory {
		return errorReply(rpcForbidden,
			fmt.Sprintf("memory is not granted to plugin %s; the operator grants it in plugins.grants.%s.memory", b.pluginID, b.pluginID),
			&errorData{ReasonCode: reasonPolicyDeny, DeniedAt: deniedAtCapEval})
	}
	if r, denied := b.scopeDenies(p.Operation, capRing4Memory); denied {
		return r, nil
	}
	if p.Operation == opMemoryRecall {
		return b.dispatchMemoryRecall(ctx, p)
	}
	return b.dispatchMemoryRemember(ctx, p)
}

func (b *Binding) dispatchMemoryRemember(ctx context.Context, p invokeParams) ([]byte, error) {
	var args struct {
		Text          string                    `json:"text"`
		Supersedes    string                    `json:"supersedes"`
		Applicability string                    `json:"applicability"`
		Evidence      []store.MemoryEvidenceRef `json:"evidence"`
	}
	if len(p.Arguments) > 0 {
		if err := canonicaljson.DecodeStrict(p.Arguments, &args); err != nil {
			return b.resultReply(p.Operation, p.PluginOperation, "", outcome{
				status: statusDenied, reason: reasonArgumentInvalid, detail: "arguments must be an object"})
		}
	}
	text := strings.TrimSpace(args.Text)
	if text == "" {
		return b.resultReply(p.Operation, p.PluginOperation, "", outcome{
			status: statusDenied, reason: reasonArgumentInvalid, detail: "memory.remember requires arguments.text, a non-empty string"})
	}
	if len(text) > b.host.cfg.maxMemoryTextBytes() {
		return b.resultReply(p.Operation, p.PluginOperation, "", outcome{
			status: statusFailed, reason: reasonMemoryTextTooLarge, transportOK: true,
			detail: fmt.Sprintf("text of %d bytes exceeds the %d-byte ceiling", len(text), b.host.cfg.maxMemoryTextBytes())})
	}
	st := b.host.cfg.Store
	temp := !b.tier.PublisherProven()
	now := time.Now()
	binding, err := b.memoryWriteContext(args.Applicability)
	if err != nil {
		return b.resultReply(p.Operation, p.PluginOperation, "", outcome{status: statusDenied, reason: reasonArgumentInvalid, detail: err.Error()})
	}
	evidence, err := b.validateMemoryEvidence(ctx, args.Evidence)
	if err != nil {
		return b.resultReply(p.Operation, p.PluginOperation, "", outcome{status: statusDenied, reason: reasonArgumentInvalid, detail: err.Error()})
	}
	note := store.PluginMemory{Text: text, Project: binding.ProjectID, Applicability: binding.Applicability, WorkSession: binding.WorkSessionID, SourceIncarnation: binding.SourceIncarnation, Evidence: evidence, Temp: temp, CreatedAt: now}
	selection := store.PluginMemorySelection{Kind: binding.Applicability, Project: binding.ProjectID, WorkSession: binding.WorkSessionID}

	if args.Supersedes != "" {
		old, found, err := st.PluginMemoryGet(b.storageScope(), args.Supersedes)
		if err != nil {
			return nil, fmt.Errorf("broker: memory.remember: %w", err)
		}
		if !found || old.SupersededBy != "" {
			return b.resultReply(p.Operation, p.PluginOperation, args.Supersedes, outcome{
				status: statusFailed, reason: reasonMemoryNotFound, transportOK: true,
				detail: fmt.Sprintf("%s is not a current memory of this plugin", args.Supersedes)})
		}
		id, refusal, err := b.addMemory(p, st, note, args.Supersedes, false)
		if refusal != nil || err != nil {
			return refusal, err
		}
		return b.memoryRemembered(ctx, p, id, "updated", args.Supersedes)
	}

	if binding.Applicability != "legacy" || len(args.Evidence) > 0 {
		note.ID = "pm_" + randomHex(16)
		id, refusal, err := b.addMemory(p, st, note, "", true)
		if refusal != nil || err != nil {
			return refusal, err
		}
		if id != note.ID {
			if err := st.RecordMemoryAccess(now, store.MemoryRef{Store: "plugin_memories", ID: id}); err != nil {
				return nil, err
			}
			return b.memoryRemembered(ctx, p, id, "reinforced", id)
		}
		return b.memoryRemembered(ctx, p, id, "created", "")
	}

	res, err := b.host.instruments.Recall(ctx, memory.Query{
		Text: text, Stores: []string{"plugin_memories"}, PluginID: b.pluginID, Activation: b.storageScope().Activation,
		Limit: 3, Decay: memory.DecayNone, Now: now, WordsOnly: true, Selection: selection,
	})
	if err != nil {
		return nil, fmt.Errorf("broker: memory.remember: %w", err)
	}
	bestID, best := "", 0.0
	like := trigram.Against(text)
	for _, h := range res.Hits {
		if sim := like(h.Text); sim > best {
			best, bestID = sim, h.ID
		}
	}
	switch {
	case bestID != "" && best >= reinforceSimilarity:
		if err := st.RecordMemoryAccess(now, store.MemoryRef{Store: "plugin_memories", ID: bestID}); err != nil {
			return nil, fmt.Errorf("broker: memory.remember: reinforce: %w", err)
		}
		return b.memoryRemembered(ctx, p, bestID, "reinforced", bestID)
	case bestID != "" && best >= updateSimilarity:
		id, refusal, err := b.addMemory(p, st, note, bestID, false)
		if refusal != nil || err != nil {
			return refusal, err
		}
		return b.memoryRemembered(ctx, p, id, "updated", bestID)
	default:
		id, refusal, err := b.addMemory(p, st, note, "", false)
		if refusal != nil || err != nil {
			return refusal, err
		}
		return b.memoryRemembered(ctx, p, id, "created", "")
	}
}

func (b *Binding) addMemory(p invokeParams, st *store.Store, note store.PluginMemory, supersedes string, exact bool) (string, []byte, error) {
	id := note.ID
	if id == "" {
		id = "pm_" + randomHex(16)
	}
	note.ID, note.PluginID, note.Attribution = id, b.pluginID, "plugin"
	var err error
	if exact {
		id, err = st.PluginMemoryAddExact(b.storageScope(), note, b.host.cfg.maxMemories())
	} else {
		err = st.PluginMemoryAdd(b.storageScope(), note, b.host.cfg.maxMemories(), supersedes)
	}
	if errors.Is(err, store.ErrPluginMemoryQuota) {
		r, e := b.resultReply(p.Operation, p.PluginOperation, "", outcome{
			status: statusFailed, reason: reasonMemoryQuotaExceeded, transportOK: true, detail: err.Error()})
		return "", r, e
	}
	if errors.Is(err, store.ErrPluginMemoryNotFound) {
		r, e := b.resultReply(p.Operation, p.PluginOperation, supersedes, outcome{
			status: statusFailed, reason: reasonMemoryNotFound, transportOK: true, detail: err.Error()})
		return "", r, e
	}
	if err != nil {
		return "", nil, fmt.Errorf("broker: memory.remember: %w", err)
	}
	return id, nil, nil
}

func (b *Binding) memoryRemembered(ctx context.Context, p invokeParams, id, outcomeWord, of string) ([]byte, error) {
	m, found, err := b.host.cfg.Store.PluginMemoryGet(b.storageScope(), id)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("remembered memory is no longer available")
	}
	evidence, err := b.memoryEvidence(ctx, m)
	if err != nil {
		return nil, err
	}
	scope := "persistent"
	if m.Temp {
		scope = "temp"
	}
	or, err := json.Marshal(map[string]any{
		"context": contextOfMemory(m), "evidence": evidence,
		"id": id, "outcome": outcomeWord, "of": of,
		"created_at": m.CreatedAt.UTC().Format(time.RFC3339Nano), "scope": scope,
	})
	if err != nil {
		return nil, fmt.Errorf("broker: memory.remember encode: %w", err)
	}
	return b.resultReply(p.Operation, p.PluginOperation, id, outcome{
		status: statusSucceeded, transportOK: true, operationResult: or})
}

func (b *Binding) dispatchMemoryRecall(ctx context.Context, p invokeParams) ([]byte, error) {
	var args struct {
		Query     string `json:"query"`
		Exact     bool   `json:"exact"`
		AnyWords  bool   `json:"any_words"`
		Since     string `json:"since"`
		Before    string `json:"before"`
		Limit     int    `json:"limit"`
		ID        string `json:"id"`
		Decay     string `json:"decay"`
		Selection string `json:"selection"`
		Recent    bool   `json:"recent"`
	}
	if len(p.Arguments) > 0 {
		if err := canonicaljson.DecodeStrict(p.Arguments, &args); err != nil {
			return b.resultReply(p.Operation, p.PluginOperation, "", outcome{
				status: statusDenied, reason: reasonArgumentInvalid, detail: "arguments must be an object"})
		}
	}
	if args.AnyWords && (args.Exact || args.Recent || args.ID != "") {
		return b.resultReply(p.Operation, p.PluginOperation, "", outcome{status: statusDenied, reason: reasonArgumentInvalid, detail: "any_words requires a query without exact, recent or id"})
	}
	st := b.host.cfg.Store
	now := time.Now()
	contextRead := false
	if sc := b.scope.Load(); sc != nil {
		contextRead = sc.ContextRead
	}
	resultBudget := memoryRecallResultBytes
	if contextRead {
		resultBudget = contextRecallResultBytes
	}
	selection, err := b.memorySelection(args.Selection)
	if err != nil {
		return b.resultReply(p.Operation, p.PluginOperation, "", outcome{status: statusDenied, reason: reasonArgumentInvalid, detail: err.Error()})
	}
	selectionContext := selectionContextOf(selection)
	selectors := 0
	if strings.TrimSpace(args.Query) != "" {
		selectors++
	}
	if args.ID != "" {
		selectors++
	}
	if args.Recent {
		selectors++
	}
	if selectors != 1 {
		return b.resultReply(p.Operation, p.PluginOperation, "", outcome{status: statusDenied, reason: reasonArgumentInvalid, detail: "select exactly one of query, id or recent"})
	}
	if args.ID != "" && (args.Exact || args.Since != "" || args.Before != "" || args.Limit != 0 || args.Decay != "") {
		return b.resultReply(p.Operation, p.PluginOperation, "", outcome{status: statusDenied, reason: reasonArgumentInvalid, detail: "by-ID read does not accept query filters"})
	}
	if args.ID != "" {
		m, found, err := st.PluginMemoryGet(b.storageScope(), args.ID)
		if err != nil {
			return nil, fmt.Errorf("broker: memory.recall: %w", err)
		}
		if found {
			selected, selectErr := st.PluginMemorySelected(m.ID, selection)
			if selectErr != nil {
				return nil, selectErr
			}
			found = selected
		}
		if !found {
			or, err := json.Marshal(map[string]any{"selection_context": selectionContext})
			if err != nil {
				return nil, fmt.Errorf("broker: memory.recall encode: %w", err)
			}
			if len(or) > resultBudget {
				return b.resultReply(p.Operation, p.PluginOperation, args.ID, outcome{status: statusFailed, reason: reasonMemorySourceUnavailable, transportOK: true, detail: "memory selection metadata exceeds the response budget"})
			}
			return b.resultReply(p.Operation, p.PluginOperation, args.ID, outcome{
				status: statusFailed, reason: reasonMemoryNotFound, transportOK: true, operationResult: or})
		}
		ref := store.MemoryRef{Store: "plugin_memories", ID: m.ID}
		if !contextRead {
			if err := st.RecordMemoryAccess(now, ref); err != nil {
				return nil, fmt.Errorf("broker: memory.recall: reinforce: %w", err)
			}
		}
		accesses := int64(0)
		if a, ok, _ := st.MemoryAccessOf(ref); ok {
			accesses = a.Count
		}
		evidence, err := b.memoryEvidence(ctx, m)
		if err != nil {
			return nil, err
		}
		hit := map[string]any{
			"context": contextOfMemory(m), "evidence": evidence,
			"id": m.ID, "text": m.Text, "snippet": memory.Excerpt(m.Text, nil, memory.SnippetWords),
			"match": "id", "score": 1.0, "strength": 1.0, "attribution": m.Attribution,
			"ring": memory.RingWorking, "time": m.CreatedAt.UTC().Format(time.RFC3339Nano),
			"accesses": accesses, "class": "operational", "superseded_by": m.SupersededBy,
		}
		or, err := json.Marshal(map[string]any{
			"hits": []any{hit}, "status": memory.StatusFound, "matched": 1, "shown": 1,
			"policy": memory.DecayNone, "truncated": false,
			"selection_context": selectionContext,
		})
		if err != nil {
			return nil, fmt.Errorf("broker: memory.recall encode: %w", err)
		}
		if contextRead && len(or) > resultBudget {
			return b.resultReply(p.Operation, p.PluginOperation, m.ID, outcome{status: statusFailed, reason: reasonMemorySourceUnavailable, transportOK: true, detail: "memory detail exceeds the automatic context response budget; use an ordinary detail read"})
		}
		return b.resultReply(p.Operation, p.PluginOperation, m.ID, outcome{
			status: statusSucceeded, transportOK: true, operationResult: or})
	}

	query := strings.TrimSpace(args.Query)
	if query == "" && !args.Recent {
		return b.resultReply(p.Operation, p.PluginOperation, "", outcome{
			status: statusDenied, reason: reasonArgumentInvalid, detail: "memory.recall requires arguments.query (a non-empty string) or arguments.id"})
	}
	if args.Limit < 0 || args.Limit > memory.MaxLimit {
		return b.resultReply(p.Operation, p.PluginOperation, "", outcome{
			status: statusDenied, reason: reasonArgumentInvalid,
			detail: fmt.Sprintf("memory.recall limit must be 1..%d (0 for the default %d)", memory.MaxLimit, memory.DefaultLimit)})
	}
	var since, before time.Time
	if args.Since != "" {
		if since, err = time.Parse(time.RFC3339, args.Since); err != nil {
			return b.resultReply(p.Operation, p.PluginOperation, "", outcome{
				status: statusDenied, reason: reasonArgumentInvalid, detail: "memory.recall since must be RFC 3339"})
		}
	}
	if args.Before != "" {
		if before, err = time.Parse(time.RFC3339, args.Before); err != nil {
			return b.resultReply(p.Operation, p.PluginOperation, "", outcome{
				status: statusDenied, reason: reasonArgumentInvalid, detail: "memory.recall before must be RFC 3339"})
		}
	}
	target := query
	if len(target) > memoryTargetBytes {
		target = target[:memoryTargetBytes]
	}
	res, err := b.host.instruments.Recall(ctx, memory.Query{
		Text: query, Exact: args.Exact, AnyWords: args.AnyWords, Since: since, Before: before, Limit: args.Limit,
		Stores: []string{"plugin_memories"}, PluginID: b.pluginID, Activation: b.storageScope().Activation,
		Decay: args.Decay, Reinforce: false, WordsOnly: contextRead, Now: now, Selection: selection, Recent: args.Recent,
	})
	if err != nil {
		return b.resultReply(p.Operation, p.PluginOperation, target, outcome{
			status: statusDenied, reason: reasonArgumentInvalid, detail: err.Error()})
	}
	src := memory.Source{Status: memory.StatusFoundNothing}
	if len(res.Sources) > 0 {
		src = res.Sources[0]
	}
	if src.Status == memory.StatusQueryFailed || src.Status == memory.StatusSourceUnavailable {
		return b.resultReply(p.Operation, p.PluginOperation, target, outcome{
			status: statusFailed, reason: reasonMemorySourceUnavailable, transportOK: true, detail: src.Detail})
	}
	hits := make([]any, 0, len(res.Hits))
	for _, h := range res.Hits {
		m, found, err := st.PluginMemoryGet(b.storageScope(), h.ID)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, fmt.Errorf("recalled memory became unavailable")
		}
		evidence, err := b.memoryEvidence(ctx, m)
		if err != nil {
			return nil, err
		}
		hits = append(hits, map[string]any{
			"context": contextOfMemory(m), "evidence": evidence, "superseded_by": m.SupersededBy,
			"id": h.ID, "text": h.Text, "snippet": h.Snippet, "match": h.Match,
			"score": h.Score, "strength": h.Strength, "fused": h.Fused,
			"attribution": h.Attribution, "ring": h.Ring, "time": h.Time.UTC().Format(time.RFC3339Nano),
			"accesses": h.Accesses, "class": string(h.Class), "similarity": h.Similarity, "graph": h.Graph,
		})
	}
	payload := map[string]any{
		"hits": hits, "status": src.Status, "matched": src.Matched, "shown": src.Shown,
		"policy": res.Policy, "truncated": res.Truncated, "warnings": res.Warnings,
		"selection_context": selectionContext,
		"meaning":           map[string]any{"status": res.Meaning.Status, "detail": res.Meaning.Detail, "basis": res.Meaning.Basis, "floor": res.Meaning.Floor},
	}
	or, err := json.Marshal(payload)
	for err == nil && len(or) > resultBudget && len(hits) > 0 {
		hits = hits[:len(hits)-1]
		payload["hits"], payload["shown"], payload["truncated"], payload["status"] = hits, len(hits), true, memory.StatusPartial
		or, err = json.Marshal(payload)
	}
	if err != nil {
		return nil, fmt.Errorf("broker: memory.recall encode: %w", err)
	}
	if len(or) > resultBudget {
		return b.resultReply(p.Operation, p.PluginOperation, target, outcome{status: statusFailed, reason: reasonMemorySourceUnavailable, transportOK: true, detail: "memory result metadata exceeds the response budget"})
	}

	if !contextRead && !args.Recent && len(hits) > 0 {
		if err := b.host.instruments.Reinforce(now, res.Hits[:len(hits)]); err != nil {
			payload["warnings"] = append(res.Warnings, "reinforcement not recorded: "+err.Error())
			or, err = json.Marshal(payload)
			if err != nil {
				return nil, err
			}
			if len(or) > resultBudget {
				return b.resultReply(p.Operation, p.PluginOperation, target, outcome{status: statusFailed, reason: reasonMemorySourceUnavailable, transportOK: true, detail: "memory result with reinforcement warning exceeds the response budget"})
			}
		}
	}
	return b.resultReply(p.Operation, p.PluginOperation, target, outcome{
		status: statusSucceeded, transportOK: true, operationResult: or})
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {

		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}
