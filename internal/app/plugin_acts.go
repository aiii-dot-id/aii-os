package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/untrusted"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

const (
	actTTL = 10 * time.Minute

	actsPerPlugin = 8

	actOutcomeBytes = 2048
)

type pendingAct struct {
	ID        string
	Plugin    string
	Tool      string
	Operation string
	Summary   string
	Effects   string
	Args      map[string]interface{}
	Digest    string
	Session   string
	Finals    []int64
	Proposed  time.Time
	Expires   time.Time
}

type pluginActs struct {
	mu   sync.Mutex
	acts map[string]*pendingAct
}

type actProposer struct{ a *App }

func (p actProposer) Propose(ctx context.Context, prop pluginhost.ActProposal) (string, error) {
	return p.a.proposeAct(prop)
}

func (p actProposer) StandingConfirmation(plugin, operation string) (pluginhost.OperatorAct, bool) {
	return p.a.standingConfirmation(plugin, operation)
}

func (p actProposer) AdmitOperation(plugin, operation, effects string) error {
	return p.a.admitOperation(plugin, operation, effects)
}

func (a *App) standingConfirmation(plugin, operation string) (pluginhost.OperatorAct, bool) {
	if _, safe := a.SafeMode(); safe {
		return pluginhost.OperatorAct{}, false
	}
	g, ok := a.configSnapshot().Plugins.Grants[plugin]
	if !ok || !hasOperation(g.AutoConfirm, operation) {
		return pluginhost.OperatorAct{}, false
	}
	logsink.Info("act.start", "%s runs %s under the operator's standing confirmation (auto)", plugin, operation)
	return pluginhost.OperatorAct{ID: "auto", ConfirmedAt: time.Now()}, true
}

func (a *App) admitOperation(plugin, operation, effects string) error {
	g, ok := a.configSnapshot().Plugins.Grants[plugin]
	if !ok || !g.ReadOnly {
		return nil
	}
	if strings.HasPrefix(effects, "read.") {
		return nil
	}
	class := effects
	if class == "" {
		class = "an operation of no declared effect class"
	} else {
		class = "a " + class + " operation"
	}
	return fmt.Errorf("%s is %s and your operator granted %s read only — it may look, not act; ask your operator to widen the grant on the Plugins page if the work needs it", operation, class, plugin)
}

func hasOperation(list []string, operation string) bool {
	for _, op := range list {
		if op == operation {
			return true
		}
	}
	return false
}

func canonicalArgs(args map[string]interface{}) ([]byte, string, error) {
	if args == nil {
		args = map[string]interface{}{}
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(raw)
	return raw, hex.EncodeToString(sum[:]), nil
}

func actBindings(args map[string]interface{}) (session string, finals []int64) {
	for _, key := range []string{"session_id", "session"} {
		if s, ok := args[key].(string); ok && s != "" {
			session = s
			break
		}
	}
	for _, key := range []string{"finals", "sequences"} {
		list, ok := args[key].([]interface{})
		if !ok {
			continue
		}
		for _, v := range list {
			switch n := v.(type) {
			case float64:
				finals = append(finals, int64(n))
			case int64:
				finals = append(finals, n)
			case int:
				finals = append(finals, int64(n))
			case json.Number:
				if i, err := n.Int64(); err == nil {
					finals = append(finals, i)
				}
			}
		}
		break
	}
	return session, finals
}

func (r *pluginActs) sweepLocked(now time.Time) {
	for id, act := range r.acts {
		if !now.Before(act.Expires) {
			delete(r.acts, id)
		}
	}
}

func (a *App) proposeAct(prop pluginhost.ActProposal) (string, error) {
	raw, digest, err := canonicalArgs(prop.Args)
	if err != nil {
		return "", fmt.Errorf("the arguments cannot be recorded: %w", err)
	}
	session, finals := actBindings(prop.Args)
	if session != "" {
		if _, live := a.voiceSessions.Load(session); !live {
			return "", fmt.Errorf("the arguments name session %q, which is not open", session)
		}
		for _, seq := range finals {
			if _, heard := a.finalText(session, seq); !heard {
				return "", fmt.Errorf("the arguments name final %d, which the host never heard on session %q", seq, session)
			}
		}
	}
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	now := time.Now()
	r := &a.acts
	r.mu.Lock()
	if r.acts == nil {
		r.acts = map[string]*pendingAct{}
	}
	r.sweepLocked(now)
	pending := 0
	for _, act := range r.acts {
		if act.Plugin != prop.Plugin {
			continue
		}
		pending++
		if act.Operation == prop.Operation && act.Digest == digest {
			r.mu.Unlock()
			return act.ID, nil
		}
	}
	if pending >= actsPerPlugin {
		r.mu.Unlock()
		return "", fmt.Errorf("%d acts of %s already await the operator; the ceiling is %d", pending, prop.Plugin, actsPerPlugin)
	}
	args := make(map[string]interface{}, len(prop.Args))
	for k, v := range prop.Args {
		args[k] = v
	}
	act := &pendingAct{ID: "act-" + hex.EncodeToString(nonce[:]), Plugin: prop.Plugin, Tool: prop.Tool, Operation: prop.Operation,
		Summary: prop.Summary, Effects: prop.Effects, Args: args, Digest: digest, Session: session, Finals: finals,
		Proposed: now, Expires: now.Add(actTTL)}
	r.acts[act.ID] = act
	r.mu.Unlock()
	logsink.Info("act.decision", "%s proposes %s (%s) — awaiting the operator on the Plugins page (%d bytes of arguments, sha256 %s)", prop.Plugin, prop.Operation, act.ID, len(raw), digest[:12])
	if a.dashboard != nil {
		a.dashboard.BroadcastConfig()
		a.dashboard.BroadcastAsks()
	}
	return act.ID, nil
}

func (a *App) pendingActViews(pluginID string) []dashboard.PluginActView {
	r := &a.acts
	r.mu.Lock()
	r.sweepLocked(time.Now())
	var acts []*pendingAct
	for _, act := range r.acts {
		if act.Plugin == pluginID {
			acts = append(acts, act)
		}
	}
	r.mu.Unlock()
	if len(acts) == 0 {
		return nil
	}
	sort.Slice(acts, func(i, j int) bool { return acts[i].Proposed.Before(acts[j].Proposed) })
	views := make([]dashboard.PluginActView, 0, len(acts))
	for _, act := range acts {
		v := dashboard.PluginActView{ID: act.ID, Operation: act.Operation, Summary: act.Summary, Effects: act.Effects, Args: act.Args, Session: act.Session,
			Proposed: act.Proposed.UTC().Format(time.RFC3339), Expires: act.Expires.UTC().Format(time.RFC3339)}
		for _, seq := range act.Finals {
			text, heard := a.finalText(act.Session, seq)
			v.Finals = append(v.Finals, dashboard.PluginActFinal{Sequence: seq, Text: text, Heard: heard})
		}
		views = append(views, v)
	}
	return views
}

func (a *App) takeAct(pluginID, actID string) (*pendingAct, error) {
	r := &a.acts
	r.mu.Lock()
	r.sweepLocked(time.Now())
	act := r.acts[actID]
	if act != nil && act.Plugin != pluginID {
		act = nil
	}
	if act != nil {
		delete(r.acts, actID)
	}
	r.mu.Unlock()
	if act == nil {
		return nil, fmt.Errorf("no act %q awaits the operator for %s — it was decided, expired, or never proposed", actID, pluginID)
	}
	return act, nil
}

func (a *App) decideAct(ctx context.Context, pluginID, actID string, confirm bool) error {
	act, err := a.takeAct(pluginID, actID)
	if err != nil {
		return err
	}
	defer a.broadcastActs()
	if !confirm {
		logsink.Info("act.refusal", "the operator DENIED %s of %s (%s)", act.Operation, act.Plugin, act.ID)
		return a.recordActOutcome(act, "denied", "")
	}
	return a.runAct(ctx, act, "confirmed")
}

func (a *App) alwaysAct(ctx context.Context, pluginID, actID string) error {
	act, err := a.takeAct(pluginID, actID)
	if err != nil {
		return err
	}
	defer a.broadcastActs()
	standing := append([]string(nil), a.configSnapshot().Plugins.Grants[pluginID].AutoConfirm...)
	if !hasOperation(standing, act.Operation) {
		standing = append(standing, act.Operation)
	}
	_, werr := a.applyConfigChange(map[string]interface{}{"plugins.grants." + pluginID + ".auto_confirm": standing})
	if werr != nil {
		logsink.Warn("act.error", "the operator's ALWAYS for %s of %s was not written: %v", act.Operation, act.Plugin, werr)
		rerr := a.runAct(ctx, act, "confirmed — the standing confirmation was NOT written ("+werr.Error()+"), so the next call asks again")
		if rerr != nil {
			return fmt.Errorf("%v; and the standing confirmation was not written: %v", rerr, werr)
		}
		return fmt.Errorf("the act ran, but the standing confirmation was not written: %v", werr)
	}
	logsink.Info("act.decision", "the operator said ALWAYS to %s of %s — it runs without asking until revoked on the Plugins page", act.Operation, act.Plugin)
	return a.runAct(ctx, act, "confirmed, and always from now on (until revoked on the Plugins page):")
}

func (a *App) broadcastActs() {
	if a.dashboard != nil {
		a.dashboard.BroadcastConfig()
		a.dashboard.BroadcastAsks()
	}
}

func (a *App) runAct(ctx context.Context, act *pendingAct, word string) error {
	if act.Session != "" {
		if _, live := a.voiceSessions.Load(act.Session); !live {
			a.recordActOutcome(act, "refused", "the session it names has ended")
			return fmt.Errorf("act %s names session %q, which has ended; nothing ran", act.ID, act.Session)
		}
	}
	if reason, safe := a.SafeMode(); safe && act.Effects != "" && act.Effects != "read.internal" && act.Effects != "read.external" {
		a.recordActOutcome(act, "refused", "SAFE holds ("+reason+")")
		return fmt.Errorf("act %s is a %s operation and this identity is in SAFE (%s); nothing ran", act.ID, act.Effects, reason)
	}
	if a.toolReg == nil {
		return fmt.Errorf("no tool registry; nothing ran")
	}
	if a.store == nil {
		return fmt.Errorf("no tool recorder; nothing ran")
	}
	raw, _, _ := canonicalArgs(act.Args)
	record := appTranscript{st: a.store, safe: a.safeTools, actor: "operator"}
	turn := "operator_act_" + act.ID
	if err := record.RecordToolStart(turn, 1, act.ID, act.Tool, string(raw), ""); err != nil {
		return fmt.Errorf("record operator act before execution: %w; nothing ran", err)
	}
	stamp := pluginhost.OperatorAct{ID: act.ID, ConfirmedAt: time.Now()}
	started := time.Now()
	res, err := a.toolReg.Execute(pluginhost.WithOperatorAct(ctx, stamp), act.Tool, act.Args)
	a.emitToolEvent("call", act.Tool, string(raw))
	failed := err != nil || res.Error != ""
	duration := time.Since(started).Milliseconds()
	text := res.Text()
	if err != nil {
		text = err.Error()
	}
	durable, recordErr := record.recordToolDoneObservedDurable(turn, 1, act.Tool, string(raw), conversation.Observation{Text: text, Failed: failed, DurationMS: &duration, SessionReason: "operator act"})
	if !durable {
		recordErr = errors.Join(recordErr, a.recordActResultFallback(act, text, recordErr))
	}
	a.emitPluginEvent(pluginhost.TopicToolCalled, map[string]interface{}{"tool": act.Tool, "failed": failed, "duration_ms": time.Since(started).Milliseconds(), "actor": "operator", "session": "", "act": act.ID})
	switch {
	case err != nil:
		logsink.Warn("act.error", "the operator CONFIRMED %s of %s (%s) — the dispatch failed: %v", act.Operation, act.Plugin, act.ID, err)
		noticeErr := a.recordActOutcome(act, word+", and the operation failed", "")
		return errors.Join(fmt.Errorf("act %s ran and failed: %v", act.ID, err), recordErr, noticeErr)
	case res.Error != "":
		logsink.Warn("act.refusal", "the operator CONFIRMED %s of %s (%s) — the operation refused: %s", act.Operation, act.Plugin, act.ID, res.Error)
		noticeErr := a.recordActOutcome(act, word+", and the operation refused", "")
		return errors.Join(fmt.Errorf("act %s ran and was refused: %s", act.ID, res.Error), recordErr, noticeErr)
	default:
		logsink.Info("act.end", "the operator CONFIRMED %s of %s (%s) — ran once (%d bytes)", act.Operation, act.Plugin, act.ID, len(res.Text()))
		return errors.Join(recordErr, a.recordActOutcome(act, word, ""))
	}
}

func (a *App) recordActOutcome(act *pendingAct, outcome, detail string) error {
	if a.engine == nil {
		return nil
	}
	detail = strings.TrimSpace(detail)
	if len(detail) > actOutcomeBytes {
		detail = strings.ToValidUTF8(detail[:actOutcomeBytes], "") + "…"
	}
	raw, _, _ := canonicalArgs(act.Args)
	text := fmt.Sprintf("[act %s] %s %s of %s with the arguments %s", act.ID, outcome, act.Operation, act.Plugin, raw)
	if detail != "" {
		text += " — " + detail
	}
	if _, err := a.engine.RecordConversationRef(interaction.WithTurn(context.Background(), "operator_act_"+act.ID), roleOperator, text, interaction.Details{Channel: "plugin", Actor: "operator", Tool: act.Tool, Reason: outcome}); err != nil {
		logsink.Warn("act.error", "outcome not recorded: %v", err)
		return fmt.Errorf("operator act consumed, but outcome recording failed: %w", err)
	}
	return nil
}

func (a *App) recordActResultFallback(act *pendingAct, detail string, cause error) error {
	if a.engine == nil {
		return fmt.Errorf("act %s result cannot be retained: no conversation recorder", act.ID)
	}
	if len(detail) > actOutcomeBytes {
		detail = strings.ToValidUTF8(detail[:actOutcomeBytes], "") + "… [observed result excerpt truncated]"
	}
	text := fmt.Sprintf("[Observed result for act %s; durable tool completion was not confirmed by the recorder. Do not repeat the action to repair this record.]\n%s", act.ID, untrusted.Wrap("plugin "+act.Plugin, detail))
	details := interaction.Details{Channel: "plugin", Actor: act.Plugin, Tool: act.Tool}
	if cause != nil {
		details.RecordingError = cause.Error()
	}
	_, err := a.engine.RecordConversationRef(interaction.WithTurn(context.Background(), "operator_act_"+act.ID), "participant", text, details)
	return err
}
