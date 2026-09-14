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

	"github.com/aiii-dot-id/aii-os/internal/memory"
	"github.com/aiii-dot-id/aii-os/internal/memory/trigram"
	"github.com/aiii-dot-id/aii-os/internal/store"
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

const (
	opMemoryRemember = "memory.remember"
	opMemoryRecall   = "memory.recall"
	// .
	// .
	capRing4Memory = "ring4.memory"

	// .
	reasonMemoryNotFound          = "MEMORY_NOT_FOUND"
	reasonMemoryTextTooLarge      = "MEMORY_TEXT_TOO_LARGE"
	reasonMemoryQuotaExceeded     = "MEMORY_QUOTA_EXCEEDED"
	reasonMemorySourceUnavailable = "MEMORY_SOURCE_UNAVAILABLE"

	// .
	// .
	// .
	// .
	reinforceSimilarity = 0.92
	updateSimilarity    = 0.75

	// .
	// .
	// .
	DefaultMaxMemoryTextBytes = 16 << 10
	// .
	// .
	// .
	DefaultMaxMemories = 10000

	// .
	// .
	memoryTargetBytes = 80
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
	declared := false
	for _, c := range b.envelope {
		if c == capRing4Memory {
			declared = true
			break
		}
	}
	if !declared {
		return errorReply(-32000,
			fmt.Sprintf("%s is not in plugin %s's signed capability envelope", capRing4Memory, b.pluginID),
			&errorData{ReasonCode: reasonNotInEnvelope, DeniedAt: deniedAtCapEval})
	}
	if !g.Memory {
		return errorReply(-32000,
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

// .
// .
// .
// .
// .
func (b *Binding) dispatchMemoryRemember(ctx context.Context, p invokeParams) ([]byte, error) {
	var args struct {
		Text       string `json:"text"`
		Supersedes string `json:"supersedes"`
	}
	if len(p.Arguments) > 0 {
		if err := json.Unmarshal(p.Arguments, &args); err != nil {
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
	scope := "persistent"
	if temp {
		scope = "temp"
	}
	now := time.Now()
	project := st.ActiveProjectID()

	if args.Supersedes != "" {
		old, found, err := st.PluginMemoryGet(b.pluginID, args.Supersedes)
		if err != nil {
			return nil, fmt.Errorf("broker: memory.remember: %w", err)
		}
		if !found || old.SupersededBy != "" {
			return b.resultReply(p.Operation, p.PluginOperation, args.Supersedes, outcome{
				status: statusFailed, reason: reasonMemoryNotFound, transportOK: true,
				detail: fmt.Sprintf("%s is not a current memory of this plugin", args.Supersedes)})
		}
		id, refusal, err := b.addMemory(p, st, text, project, temp, now)
		if refusal != nil || err != nil {
			return refusal, err
		}
		if err := st.PluginMemorySupersede(b.pluginID, args.Supersedes, id); err != nil {
			return nil, fmt.Errorf("broker: memory.remember: supersede: %w", err)
		}
		return b.memoryRemembered(p, id, "updated", args.Supersedes, now, scope)
	}

	// .
	// .
	// .
	res, err := b.host.instruments.Recall(ctx, memory.Query{
		Text: text, Stores: []string{"plugin_memories"}, PluginID: b.pluginID,
		Limit: 3, Decay: memory.DecayNone, Now: now,
	})
	if err != nil {
		return nil, fmt.Errorf("broker: memory.remember: %w", err)
	}
	bestID, best := "", 0.0
	for _, h := range res.Hits {
		if sim := trigram.Similarity(h.Text, text); sim > best {
			best, bestID = sim, h.ID
		}
	}
	switch {
	case bestID != "" && best >= reinforceSimilarity:
		if err := st.RecordMemoryAccess(now, store.MemoryRef{Store: "plugin_memories", ID: bestID}); err != nil {
			return nil, fmt.Errorf("broker: memory.remember: reinforce: %w", err)
		}
		return b.memoryRemembered(p, bestID, "reinforced", bestID, now, scope)
	case bestID != "" && best >= updateSimilarity:
		id, refusal, err := b.addMemory(p, st, text, project, temp, now)
		if refusal != nil || err != nil {
			return refusal, err
		}
		if err := st.PluginMemorySupersede(b.pluginID, bestID, id); err != nil {
			return nil, fmt.Errorf("broker: memory.remember: supersede: %w", err)
		}
		return b.memoryRemembered(p, id, "updated", bestID, now, scope)
	default:
		id, refusal, err := b.addMemory(p, st, text, project, temp, now)
		if refusal != nil || err != nil {
			return refusal, err
		}
		return b.memoryRemembered(p, id, "created", "", now, scope)
	}
}

// .
// .
func (b *Binding) addMemory(p invokeParams, st *store.Store, text, project string, temp bool, now time.Time) (string, []byte, error) {
	id := "pm_" + randomHex(16)
	err := st.PluginMemoryAdd(store.PluginMemory{
		ID: id, PluginID: b.pluginID, Text: text, Project: project,
		Attribution: "plugin", Temp: temp, CreatedAt: now,
	}, b.host.cfg.maxMemories())
	if errors.Is(err, store.ErrPluginMemoryQuota) {
		r, e := b.resultReply(p.Operation, p.PluginOperation, "", outcome{
			status: statusFailed, reason: reasonMemoryQuotaExceeded, transportOK: true, detail: err.Error()})
		return "", r, e
	}
	if err != nil {
		return "", nil, fmt.Errorf("broker: memory.remember: %w", err)
	}
	return id, nil, nil
}

func (b *Binding) memoryRemembered(p invokeParams, id, outcomeWord, of string, now time.Time, scope string) ([]byte, error) {
	or, err := json.Marshal(map[string]interface{}{
		"id": id, "outcome": outcomeWord, "of": of,
		"created_at": now.UTC().Format(time.RFC3339Nano), "scope": scope,
	})
	if err != nil {
		return nil, fmt.Errorf("broker: memory.remember encode: %w", err)
	}
	return b.resultReply(p.Operation, p.PluginOperation, id, outcome{
		status: statusSucceeded, transportOK: true, operationResult: or})
}

// .
// .
// .
func (b *Binding) dispatchMemoryRecall(ctx context.Context, p invokeParams) ([]byte, error) {
	var args struct {
		Query  string `json:"query"`
		Exact  bool   `json:"exact"`
		Since  string `json:"since"`
		Before string `json:"before"`
		Limit  int    `json:"limit"`
		ID     string `json:"id"`
		Decay  string `json:"decay"`
	}
	if len(p.Arguments) > 0 {
		if err := json.Unmarshal(p.Arguments, &args); err != nil {
			return b.resultReply(p.Operation, p.PluginOperation, "", outcome{
				status: statusDenied, reason: reasonArgumentInvalid, detail: "arguments must be an object"})
		}
	}
	st := b.host.cfg.Store
	now := time.Now()

	if args.ID != "" {
		m, found, err := st.PluginMemoryGet(b.pluginID, args.ID)
		if err != nil {
			return nil, fmt.Errorf("broker: memory.recall: %w", err)
		}
		if !found {
			return b.resultReply(p.Operation, p.PluginOperation, args.ID, outcome{
				status: statusFailed, reason: reasonMemoryNotFound, transportOK: true})
		}
		ref := store.MemoryRef{Store: "plugin_memories", ID: m.ID}
		if err := st.RecordMemoryAccess(now, ref); err != nil {
			return nil, fmt.Errorf("broker: memory.recall: reinforce: %w", err)
		}
		accesses := int64(0)
		if a, ok, _ := st.MemoryAccessOf(ref); ok {
			accesses = a.Count
		}
		hit := map[string]interface{}{
			"id": m.ID, "text": m.Text, "snippet": memory.Excerpt(m.Text, nil, memory.SnippetWords),
			"match": "id", "score": 1.0, "strength": 1.0, "attribution": m.Attribution,
			"ring": memory.RingWorking, "time": m.CreatedAt.UTC().Format(time.RFC3339Nano),
			"accesses": accesses, "class": "operational", "superseded_by": m.SupersededBy,
		}
		or, err := json.Marshal(map[string]interface{}{
			"hits": []interface{}{hit}, "status": memory.StatusFound, "matched": 1, "shown": 1,
			"policy": memory.DecayNone, "truncated": false,
		})
		if err != nil {
			return nil, fmt.Errorf("broker: memory.recall encode: %w", err)
		}
		return b.resultReply(p.Operation, p.PluginOperation, m.ID, outcome{
			status: statusSucceeded, transportOK: true, operationResult: or})
	}

	query := strings.TrimSpace(args.Query)
	if query == "" {
		return b.resultReply(p.Operation, p.PluginOperation, "", outcome{
			status: statusDenied, reason: reasonArgumentInvalid, detail: "memory.recall requires arguments.query (a non-empty string) or arguments.id"})
	}
	if args.Limit < 0 || args.Limit > memory.MaxLimit {
		return b.resultReply(p.Operation, p.PluginOperation, "", outcome{
			status: statusDenied, reason: reasonArgumentInvalid,
			detail: fmt.Sprintf("memory.recall limit must be 1..%d (0 for the default %d)", memory.MaxLimit, memory.DefaultLimit)})
	}
	var since, before time.Time
	var err error
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
	if args.Decay != "" && args.Decay != memory.DecayDefault && args.Decay != memory.DecayNone {
		return b.resultReply(p.Operation, p.PluginOperation, "", outcome{
			status: statusDenied, reason: reasonArgumentInvalid,
			detail: fmt.Sprintf("memory.recall decay must be %s or %s", memory.DecayDefault, memory.DecayNone)})
	}
	target := query
	if len(target) > memoryTargetBytes {
		target = target[:memoryTargetBytes]
	}
	res, err := b.host.instruments.Recall(ctx, memory.Query{
		Text: query, Exact: args.Exact, Since: since, Before: before, Limit: args.Limit,
		Stores: []string{"plugin_memories"}, PluginID: b.pluginID,
		Decay: args.Decay, Reinforce: true, Now: now,
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
	hits := make([]interface{}, 0, len(res.Hits))
	for _, h := range res.Hits {
		hits = append(hits, map[string]interface{}{
			"id": h.ID, "text": h.Text, "snippet": h.Snippet, "match": h.Match,
			"score": h.Score, "strength": h.Strength, "fused": h.Fused,
			"attribution": h.Attribution, "ring": h.Ring, "time": h.Time.UTC().Format(time.RFC3339Nano),
			"accesses": h.Accesses, "class": string(h.Class), "similarity": h.Similarity, "graph": h.Graph,
		})
	}
	or, err := json.Marshal(map[string]interface{}{
		"hits": hits, "status": src.Status, "matched": src.Matched, "shown": src.Shown,
		"policy": res.Policy, "truncated": res.Truncated, "warnings": res.Warnings,
		"meaning": map[string]interface{}{"status": res.Meaning.Status, "detail": res.Meaning.Detail, "basis": res.Meaning.Basis},
	})
	if err != nil {
		return nil, fmt.Errorf("broker: memory.recall encode: %w", err)
	}
	return b.resultReply(p.Operation, p.PluginOperation, target, outcome{
		status: statusSucceeded, transportOK: true, operationResult: or})
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		// .
		// .
		// .
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}
