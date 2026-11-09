package app

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"slices"
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
	askKindVoice   = "voice"

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

	voiceMu sync.Mutex

	settling func()
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
	out = append(out, a.voiceAskViews()...)
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
	if v, ok := a.voiceAskWaiting(req.ID); ok {
		return a.answerVoiceAsk(v, req)
	}
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

func (a *App) namedVoices() map[string]bool {
	named := map[string]bool{}
	for _, id := range a.configSnapshot().Speech.OperatorSpeakers {
		named[id] = true
	}
	return named
}

func (a *App) voiceHeardWords(turn uint64) (string, error) {
	t, err := a.store.GetTurnBySeq(turn)
	if err != nil {
		return "", fmt.Errorf("read turn %d: %w", turn, err)
	}
	if t == nil {
		return "", fmt.Errorf("no turn %d is on record", turn)
	}
	words, _ := store.HeardWords(t.Content)
	words = strings.TrimSpace(words)
	if r := []rune(words); len(r) > askWordsMax {
		words = string(r[:askWordsMax]) + "…"
	}
	return words, nil
}

func (a *App) voiceAsksWaiting() []store.VoiceAsk {
	if a.store == nil {
		return nil
	}
	if _, safe := a.SafeMode(); safe {
		return nil
	}
	asked, err := a.store.VoiceAsks()
	if err != nil {
		logsink.Warn("ask.error", "the questions about voices could not be read: %v", err)
		return nil
	}
	named := a.namedVoices()
	return slices.DeleteFunc(asked, func(v store.VoiceAsk) bool { return v.Answer != "" || named[v.UUID] })
}

func (a *App) voiceAskWaiting(id string) (store.VoiceAsk, bool) {
	for _, v := range a.voiceAsksWaiting() {
		if v.Ask == id {
			return v, true
		}
	}
	return store.VoiceAsk{}, false
}

func (a *App) voiceAskViews() []dashboard.AskView {
	var out []dashboard.AskView
	for _, v := range a.voiceAsksWaiting() {
		words, err := a.voiceHeardWords(v.Turn)
		if err != nil {
			logsink.Warn("ask.error", "%s could not be drawn: %v", v.Ask, err)
			continue
		}
		out = append(out, dashboard.AskView{ID: v.Ask, Kind: askKindVoice, From: "identity", Text: v.Question, Heard: words, Proposed: v.Proposed})
	}
	return out
}

func (a *App) proposeVoiceAsk(uuid, question string) (string, error) {
	question = strings.TrimSpace(question)
	switch {
	case a.store == nil:
		return "", fmt.Errorf("no record to keep the question on; nothing was asked")
	case question == "":
		return "", fmt.Errorf("question is your words to your operator, and it is empty; nothing was asked")
	case !speakerUUID(uuid):
		return "", fmt.Errorf("voice_uuid %q is not a speaker UUID: copy speaker_uuid from the [voice · …] line; nothing was asked", uuid)
	}
	if err := askShapeFits(question, nil); err != nil {
		return "", err
	}
	if a.namedVoices()[uuid] {
		return "", fmt.Errorf("that voice is already named as your operator's; nothing was asked")
	}
	ask := store.VoiceAsk{Ask: "voice-" + uuid, Question: question, Proposed: time.Now().UTC().Format(time.RFC3339)}
	switch err := a.store.AskAboutVoice(uuid, ask); {
	case errors.Is(err, store.ErrVoiceNotHeard):
		return "", fmt.Errorf("your record has heard no voice with that UUID; nothing was asked")
	case errors.Is(err, store.ErrVoiceAlreadyAsked):
		return "", fmt.Errorf("you have already asked about this voice, and it is asked once; nothing was asked")
	case err != nil:
		return "", fmt.Errorf("the question could not be recorded, so it was not asked: %w", err)
	}
	logsink.Info("ask.start", "%s asks the operator whether a voice nobody has named was theirs", ask.Ask)
	a.broadcastAsks()
	return fmt.Sprintf("voice_uuid=%q: Your question is on your operator's page. Go on as you were; nothing waits on it. Only their press on it names the voice.", uuid), nil
}

func (a *App) answerVoiceAsk(v store.VoiceAsk, req dashboard.AskAnswer) (string, func(bool), error) {
	var words, answer string
	switch req.Answer {
	case "confirm":
		words, answer = "yes, that voice is mine", "yes"
	case "no":
		words, answer = "no, that voice is not mine", "no"
		if who := strings.TrimSpace(req.Text); who != "" {
			words = "no, that voice is " + who
		}
	default:
		return "", nil, fmt.Errorf("a card about a voice is answered yes or no, not %q", req.Answer)
	}
	gone := fmt.Errorf("no question %q awaits an answer — it was answered, withdrawn, or its session ended", v.Ask)
	a.asks.voiceMu.Lock()
	defer a.asks.voiceMu.Unlock()
	if _, open := a.voiceAskWaiting(v.Ask); !open {
		return "", nil, gone
	}
	if answer == "yes" {
		said, err := a.nameVoice(v.UUID, saveConfig)
		if err != nil {
			return "", nil, fmt.Errorf("that voice was not named as yours: %w", err)
		}
		if said != "" {
			a.sayOnPage(said)
		}
		if _, err := a.store.MoveVoiceAnswer(v.UUID, "", answer); err != nil {
			logsink.Warn("ask.error", "%s: the voice is named, and its answer could not be recorded: %v", v.Ask, err)
		}
		logsink.Info("ask.decision", "%s: the operator named a voice as theirs", v.Ask)
	} else {
		closed, err := a.store.MoveVoiceAnswer(v.UUID, "", answer)
		if err != nil {
			return "", nil, fmt.Errorf("the answer could not be recorded: %w", err)
		}
		if !closed {
			return "", nil, gone
		}
		logsink.Info("ask.decision", "%s: the operator said a voice is not theirs", v.Ask)
	}
	return store.AskMarker + v.Ask + "] " + words, func(recorded bool) {
		defer a.broadcastAsks()
		if recorded {
			return
		}
		logsink.Warn("ask.error", "%s: the operator's answer was not recorded", v.Ask)
		if answer == "yes" {
			a.sayOnPage("That voice is named as yours, but your answer did not reach the conversation.")
			return
		}
		if _, err := a.store.MoveVoiceAnswer(v.UUID, answer, ""); err != nil {
			logsink.Warn("ask.error", "%s: the question could not be opened again: %v", v.Ask, err)
		}
	}, nil
}

type voiceToName string

func (a *App) nameVoice(uuid string, persist func(*Config) (bool, error)) (string, error) {
	var err error
	for range 3 {
		if _, err = a.applyConfigChangeWith(map[string]any{"speech.operator_speakers": voiceToName(uuid)}, persist); !errors.Is(err, errConfigMoved) {
			break
		}
	}
	if !a.namedVoices()[uuid] {
		return "", err
	}
	if err != nil {
		return err.Error(), nil
	}
	return "", nil
}

func (a *App) settleNamedVoices(named []string) {
	if len(named) == 0 {
		return
	}
	asked, err := a.store.VoiceAsks()
	if err != nil {
		logsink.Warn("ask.error", "the questions about voices could not be read: %v", err)
		return
	}
	for _, v := range asked {
		if v.Answer != "yes" && slices.Contains(named, v.UUID) {
			if a.asks.settling != nil {
				a.asks.settling()
			}
			if err := a.store.SettleVoiceAnswer(v.UUID); err != nil {
				logsink.Warn("ask.error", "%s: its answer could not be recorded: %v", v.Ask, err)
			}
		}
	}
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
