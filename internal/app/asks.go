package app

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
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

const (
	askKindClarify = "clarify"
	askKindChoose  = "choose"
	askKindConnect = "connect"
	askKindConfirm = "confirm"
	// .
	// .
	asksPending = 8
	askTextMax  = 2000
	askChoices  = 6
)

// .
type pendingAsk struct {
	ID        string
	Kind      string
	Session   string
	Text      string
	Choices   []string
	Connector string
	Digest    string
	Proposed  time.Time
}

type askRegistry struct {
	mu   sync.Mutex
	asks map[string]*pendingAsk
}

func askDigest(session, text string, choices []string, connector string) string {
	h := sha256.New()
	h.Write([]byte(session))
	h.Write([]byte{0})
	h.Write([]byte(text))
	for _, c := range choices {
		h.Write([]byte{0})
		h.Write([]byte(c))
	}
	h.Write([]byte{1})
	h.Write([]byte(connector))
	return hex.EncodeToString(h.Sum(nil))
}

// .
// .
func (a *App) proposeAsk(session, text string, choices []string, connector string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		a.withdrawAsk(session, "the identity cleared the decision it had owed")
		return nil
	}
	if len(text) > askTextMax {
		return fmt.Errorf("the decision text is %d characters; a card carries %d — say the short version and put the rest in the plan", len(text), askTextMax)
	}
	if len(choices) > askChoices {
		return fmt.Errorf("%d choices; a card offers at most %d", len(choices), askChoices)
	}
	kind := askKindClarify
	switch {
	case connector != "":
		kind = askKindConnect
	case len(choices) > 0:
		kind = askKindChoose
	}
	digest := askDigest(session, text, choices, connector)
	r := &a.asks
	r.mu.Lock()
	if r.asks == nil {
		r.asks = map[string]*pendingAsk{}
	}
	var superseded *pendingAsk
	pending := 0
	for _, ask := range r.asks {
		if ask.Digest == digest {
			r.mu.Unlock()
			return nil
		}
		if ask.Session == session {
			superseded = ask
			continue
		}
		pending++
	}
	if pending >= asksPending {
		r.mu.Unlock()
		return fmt.Errorf("%d questions already await your operator; the ceiling is %d — answer or withdraw one first", pending, asksPending)
	}
	if superseded != nil {
		delete(r.asks, superseded.ID)
	}
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		r.mu.Unlock()
		return err
	}
	ask := &pendingAsk{ID: "ask-" + hex.EncodeToString(nonce[:]), Kind: kind, Session: session, Text: text,
		Choices: append([]string(nil), choices...), Connector: connector, Digest: digest, Proposed: time.Now()}
	r.asks[ask.ID] = ask
	r.mu.Unlock()
	if superseded != nil {
		a.sayOnPage(fmt.Sprintf("The identity withdrew its earlier question (%s) for a newer one.", clip(superseded.Text, 80)))
		// .
		// .
		// .
		// .
		logsink.Info("ask.decision", "%s superseded %s (session %s)", ask.ID, superseded.ID, session)
	}
	logsink.Info("ask.start", "%s asks the operator (%s, session %s): %s", ask.ID, kind, session, clip(text, 120))
	a.broadcastAsks()
	return nil
}

// .
func (a *App) withdrawAsk(session, why string) {
	r := &a.asks
	r.mu.Lock()
	var gone *pendingAsk
	for id, ask := range r.asks {
		if ask.Session == session {
			gone = ask
			delete(r.asks, id)
		}
	}
	r.mu.Unlock()
	if gone == nil {
		return
	}
	a.sayOnPage(fmt.Sprintf("The identity's question (%s) is withdrawn: %s.", clip(gone.Text, 80), why))
	logsink.Info("ask.end", "%s withdrawn (session %s): %s", gone.ID, session, why)
	a.broadcastAsks()
}

// .
// .
func (a *App) askViews() []dashboard.AskView {
	var out []dashboard.AskView
	r := &a.asks
	r.mu.Lock()
	for _, ask := range r.asks {
		out = append(out, dashboard.AskView{ID: ask.ID, Kind: ask.Kind, From: "identity", Session: ask.Session, Text: ask.Text,
			Choices: append([]string(nil), ask.Choices...), Connector: ask.Connector, Proposed: ask.Proposed.UTC().Format(time.RFC3339)})
	}
	r.mu.Unlock()
	acts := &a.acts
	acts.mu.Lock()
	acts.sweepLocked(time.Now())
	for _, act := range acts.acts {
		v := dashboard.AskView{ID: act.ID, Kind: askKindConfirm, From: act.Plugin, Plugin: act.Plugin, Operation: act.Operation,
			Text: act.Summary, Effects: act.Effects, Session: act.Session, Proposed: act.Proposed.UTC().Format(time.RFC3339),
			Expires: act.Expires.UTC().Format(time.RFC3339), Args: map[string]interface{}{}}
		for k, val := range act.Args {
			v.Args[k] = val
		}
		out = append(out, v)
	}
	acts.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Proposed < out[j].Proposed })
	return out
}

// .
// .
// .
// .
func (a *App) answerAsk(req dashboard.AskAnswer) (string, error) {
	r := &a.asks
	r.mu.Lock()
	ask := r.asks[req.ID]
	if ask != nil {
		delete(r.asks, req.ID)
	}
	r.mu.Unlock()
	if ask == nil {
		return "", fmt.Errorf("no question %q awaits an answer — it was answered, withdrawn, or its session ended", req.ID)
	}
	defer a.broadcastAsks()
	clear := func() {
		if a.store == nil {
			return
		}
		empty := ""
		if err := a.store.UpdateWorkPlan(ask.Session, nil, nil, nil, nil, nil, &empty); err != nil {
			logsink.Warn("ask.error", "%s answered but the session's decision could not be cleared: %v", ask.ID, err)
		}
	}
	marker := "[ask " + ask.ID + "] "
	switch req.Answer {
	case "chose":
		choice := strings.TrimSpace(req.Choice)
		if choice == "" {
			return "", fmt.Errorf("a choice names one of the options")
		}
		clear()
		return marker + "chose: " + choice, nil
	case "said":
		text := strings.TrimSpace(req.Text)
		if text == "" {
			return "", fmt.Errorf("nothing was said")
		}
		clear()
		return marker + text, nil
	case "no":
		clear()
		return marker + "no — the answer is no; decide without it", nil
	case "not_now":
		// .
		// .
		// .
		return marker + "not now — proceed with what does not depend on it; the decision stays owed; ask again when the work needs it", nil
	case "connect":
		scope := "read only"
		if req.Scope == "modify" {
			scope = "read and modify"
		}
		what := ask.Connector
		if what == "" {
			what = "the connector"
		}
		// .
		// .
		// .
		// .
		if a.pluginInstalled(ask.Connector) {
			if _, gerr := a.applyConfigChange(map[string]interface{}{"plugins.grants." + ask.Connector + ".read_only": req.Scope != "modify"}); gerr != nil {
				logsink.Warn("ask.error", "%s connect: the read_only grant for %s was not written: %v", ask.ID, ask.Connector, gerr)
			}
		}
		clear()
		return marker + fmt.Sprintf("connect %s (%s) — being set up on the Plugins page; installing, granting and any credential are the operator's acts there", what, scope), nil
	}
	// .
	r.mu.Lock()
	if r.asks == nil {
		r.asks = map[string]*pendingAsk{}
	}
	r.asks[ask.ID] = ask
	r.mu.Unlock()
	return "", fmt.Errorf("unknown answer %q (chose, said, no, not_now, connect)", req.Answer)
}

func (a *App) broadcastAsks() {
	if a.dashboard != nil {
		a.dashboard.BroadcastAsks()
	}
}

// .
func (a *App) sayOnPage(text string) {
	if a.dashboard != nil {
		a.dashboard.BroadcastSystemLine(text)
	}
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// .
func (a *App) pluginInstalled(id string) bool {
	if id == "" {
		return false
	}
	for _, ap := range a.plugins {
		if ap != nil && ap.ID == id {
			return true
		}
	}
	return false
}
