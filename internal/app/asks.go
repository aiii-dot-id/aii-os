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
	"github.com/aiii-dot-id/aii-os/internal/store"
)

const (
	askKindClarify = "clarify"
	askKindChoose  = "choose"
	askKindConnect = "connect"
	askKindConfirm = "confirm"
	askKindWord    = "word"

	asksPending = 8
	askTextMax  = 2000
	askChoices  = 6
)

type pendingAsk struct {
	ID        string
	Kind      string
	Session   string
	Text      string
	Choices   []string
	Connector string
	Digest    string
	Proposed  time.Time

	Turn uint64
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

func (a *App) proposeAsk(session, text string, choices []string, connector string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		a.withdrawAsk(session, "the identity cleared the decision it had owed")
		return nil
	}
	if err := askShapeFits(text, choices); err != nil {
		return err
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
		if ask.Kind == askKindWord {
			continue
		}
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
		return asksFull(pending)
	}
	if superseded != nil {
		delete(r.asks, superseded.ID)
	}
	id, err := newAskID()
	if err != nil {
		r.mu.Unlock()
		return err
	}
	ask := &pendingAsk{ID: id, Kind: kind, Session: session, Text: text,
		Choices: append([]string(nil), choices...), Connector: connector, Digest: digest, Proposed: time.Now()}
	r.asks[ask.ID] = ask
	r.mu.Unlock()
	if superseded != nil {
		a.sayOnPage(fmt.Sprintf("The identity withdrew its earlier question (%s) for a newer one.", clip(superseded.Text, 80)))

		logsink.Info("ask.decision", "%s superseded %s (session %s)", ask.ID, superseded.ID, session)
	}
	logsink.Info("ask.start", "%s asks the operator (%s, session %s): %s", ask.ID, kind, session, clip(text, 120))
	a.broadcastAsks()
	return nil
}

func askShapeFits(text string, choices []string) error {
	if len(text) > askTextMax {
		return fmt.Errorf("the decision text is %d characters; a card carries %d — say the short version and put the rest in the plan", len(text), askTextMax)
	}
	if len(choices) > askChoices {
		return fmt.Errorf("%d choices; a card offers at most %d", len(choices), askChoices)
	}
	return nil
}

func asksFull(pending int) error {
	return fmt.Errorf("%d questions already await your operator; the ceiling is %d — answer or withdraw one first", pending, asksPending)
}

func (a *App) askFits(session, text string, choices []string, connector string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if err := askShapeFits(text, choices); err != nil {
		return err
	}
	digest := askDigest(session, text, choices, connector)
	r := &a.asks
	r.mu.Lock()
	defer r.mu.Unlock()
	pending := 0
	for _, ask := range r.asks {
		if ask.Kind == askKindWord || ask.Session == session && session != "" {
			continue
		}
		if ask.Digest == digest {
			return nil
		}
		pending++
	}
	if pending >= asksPending {
		return asksFull(pending)
	}
	return nil
}

func newAskID() (string, error) {
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	return "ask-" + hex.EncodeToString(nonce[:]), nil
}

const askWordsMax = 1500

func (a *App) confirmSpokenWord(turn uint64) error {
	if a.store == nil {
		return fmt.Errorf("no record to read the words from")
	}
	t, err := a.store.GetTurnBySeq(turn)
	if err != nil {
		return fmt.Errorf("read turn %d: %w", turn, err)
	}
	if t == nil {
		return fmt.Errorf("no turn %d is on record", turn)
	}
	words := strings.TrimSpace(strings.TrimPrefix(t.Content, store.SpokenMarker))
	if r := []rune(words); len(r) > askWordsMax {
		words = string(r[:askWordsMax]) + fmt.Sprintf("… (the first %d of %d characters; the whole is in the conversation, at turn %d)", askWordsMax, len(r), turn)
	}
	text := fmt.Sprintf("At turn %d you said: \"%s\". Spoken words stand as your affirmation of a change to the identity's relationships (Ring 1) only once you confirm here that you said them: recognizing your voice is evidence, not your word.", turn, words)
	r := &a.asks
	r.mu.Lock()
	if r.asks == nil {
		r.asks = map[string]*pendingAsk{}
	}
	var superseded *pendingAsk
	for _, ask := range r.asks {
		if ask.Kind != askKindWord {
			continue
		}
		if ask.Turn == turn {
			r.mu.Unlock()
			return nil
		}
		superseded = ask
	}
	if superseded != nil {
		delete(r.asks, superseded.ID)
	}
	id, err := newAskID()
	if err != nil {
		r.mu.Unlock()
		return err
	}
	r.asks[id] = &pendingAsk{ID: id, Kind: askKindWord, Text: text, Turn: turn, Proposed: time.Now()}
	r.mu.Unlock()
	if superseded != nil {
		logsink.Info("ask.decision", "%s superseded %s: newer spoken words (turn %d) await confirmation", id, superseded.ID, turn)
	}
	logsink.Info("ask.start", "%s asks the operator to confirm the words they spoke at turn %d", id, turn)
	a.broadcastAsks()
	return nil
}

func (a *App) withdrawAsk(session, why string) {
	r := &a.asks
	r.mu.Lock()
	var gone *pendingAsk
	for id, ask := range r.asks {
		if ask.Kind != askKindWord && ask.Session == session {
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

func (a *App) askViews() []dashboard.AskView {
	var out []dashboard.AskView
	r := &a.asks
	r.mu.Lock()
	for _, ask := range r.asks {
		from := "identity"
		if ask.Kind == askKindWord {
			from = "host"
		}
		out = append(out, dashboard.AskView{ID: ask.ID, Kind: ask.Kind, From: from, Session: ask.Session, Text: ask.Text,
			Choices: append([]string(nil), ask.Choices...), Connector: ask.Connector, Proposed: ask.Proposed.UTC().Format(time.RFC3339)})
	}
	r.mu.Unlock()
	acts := &a.acts
	acts.mu.Lock()
	acts.sweepLocked(time.Now())
	for _, act := range acts.acts {
		v := dashboard.AskView{ID: act.ID, Kind: askKindConfirm, From: act.Plugin, Plugin: act.Plugin, Operation: act.Operation,
			Text: act.Summary, Effects: act.Effects, Session: act.Session, Proposed: act.Proposed.UTC().Format(time.RFC3339),
			Expires: act.Expires.UTC().Format(time.RFC3339), Args: map[string]any{}}
		for k, val := range act.Args {
			v.Args[k] = val
		}
		out = append(out, v)
	}
	acts.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Proposed < out[j].Proposed })
	return out
}

func (a *App) answerAsk(req dashboard.AskAnswer) (text string, settle func(recorded bool), err error) {
	r := &a.asks
	r.mu.Lock()
	ask := r.asks[req.ID]
	if ask != nil {
		delete(r.asks, req.ID)
	}
	r.mu.Unlock()
	if ask == nil {
		return "", nil, fmt.Errorf("no question %q awaits an answer — it was answered, withdrawn, or its session ended", req.ID)
	}
	defer a.broadcastAsks()

	reasked := func() bool {
		for _, other := range r.asks {
			if (other.Kind == askKindWord) == (ask.Kind == askKindWord) && other.Session == ask.Session {
				return true
			}
		}
		return false
	}
	clear := func() {
		if a.store == nil {
			return
		}
		r.mu.Lock()
		newer := reasked()
		r.mu.Unlock()
		if newer {
			return
		}
		empty := ""
		if err := a.store.UpdateWorkPlan(a.lifetime(), ask.Session, nil, nil, nil, nil, nil, &empty); err != nil {
			logsink.Warn("ask.error", "%s answered but the session's decision could not be cleared: %v", ask.ID, err)
		}
	}

	keep := func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if reasked() {
			return
		}
		if r.asks == nil {
			r.asks = map[string]*pendingAsk{}
		}
		r.asks[ask.ID] = ask
	}

	answered := func(words string, clears bool) (string, func(bool), error) {
		return store.AskMarker + ask.ID + "] " + words, func(recorded bool) {
			switch {
			case !recorded:
				keep()
				logsink.Warn("ask.error", "%s: the operator's answer was not recorded, so the ask is not answered and the decision stays owed", ask.ID)
			case clears:
				clear()
			}
			a.broadcastAsks()
		}, nil
	}

	refused := func(err error) (string, func(bool), error) {
		keep()
		return "", nil, err
	}
	if ask.Kind == askKindWord {

		switch req.Answer {
		case "confirm":
			if a.store == nil {
				return refused(fmt.Errorf("your confirmation could not be recorded: no record"))
			}
			if err := a.store.AnnotateTurn(ask.Turn, store.AnnotationConfirmed, ask.ID, fmt.Sprintf(`{"ask":%q}`, ask.ID)); err != nil {
				return refused(fmt.Errorf("your confirmation could not be recorded, so the words at turn %d do not stand yet: %w", ask.Turn, err))
			}
			logsink.Info("ask.decision", "%s: the operator confirmed in the page that the words they spoke at turn %d are theirs", ask.ID, ask.Turn)
			return answered(fmt.Sprintf("confirmed — the words I spoke at turn %d are mine", ask.Turn), false)
		case "no":
			logsink.Info("ask.decision", "%s: the operator said the words at turn %d are not their affirmation", ask.ID, ask.Turn)
			return answered(fmt.Sprintf("no — the words at turn %d are not my affirmation", ask.Turn), false)
		}
		return refused(fmt.Errorf("a card confirming your spoken words is answered confirm or no, not %q", req.Answer))
	}
	switch req.Answer {
	case "chose":
		choice := strings.TrimSpace(req.Choice)
		if choice == "" {
			return refused(fmt.Errorf("a choice names one of the options"))
		}
		return answered("chose: "+choice, true)
	case "said":
		said := strings.TrimSpace(req.Text)
		if said == "" {
			return refused(fmt.Errorf("nothing was said"))
		}
		return answered(said, true)
	case "no":
		return answered("no — the answer is no; decide without it", true)
	case "not_now":

		return answered("not now — proceed with what does not depend on it; the decision stays owed; ask again when the work needs it", false)
	case "connect":
		scope := "read only"
		if req.Scope == "modify" {
			scope = "read and modify"
		}
		what := ask.Connector
		if what == "" {
			what = "the connector"
		}

		if a.activePlugin(ask.Connector) != nil {
			if _, gerr := a.applyConfigChange(map[string]any{"plugins.grants." + ask.Connector + ".read_only": req.Scope != "modify"}); gerr != nil {
				return refused(fmt.Errorf("connect %s (%s) was not answered: writing the plugin's read_only grant failed: %w", what, scope, gerr))
			}
		}
		return answered(fmt.Sprintf("connect %s (%s) — being set up on the Plugins page; installing, granting and any credential are the operator's acts there", what, scope), true)
	}
	return refused(fmt.Errorf("unknown answer %q (chose, said, no, not_now, connect)", req.Answer))
}

func (a *App) broadcastAsks() {
	if a.dashboard != nil {
		a.dashboard.BroadcastAsks()
	}
}

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

func (a *App) wireAskHooks(h *dashboard.WSHandler) {
	h.GetAsks = a.askViews
	h.AnswerAsk = a.answerAsk
}
