package app

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

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

const (
	speakerModeAll    = "all"
	speakerModeOnly   = "only"
	speakerModeIgnore = "ignore"

	unidentifiedDeliver  = "deliver"
	unidentifiedWithhold = "withhold"

	// .
	maxSpeakerPolicyIDs = 64
)

// .
// .
var reSpeakerID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// .
// .
// .
// .
var speakerDecisionBound = 3 * time.Second

// .
// .
// .
// .
func validateSpeakerPolicy(c SpeakerPolicyConfig) error {
	mode := c.Mode
	if mode == "" {
		mode = speakerModeAll
	}
	switch mode {
	case speakerModeAll, speakerModeOnly, speakerModeIgnore:
	default:
		return fmt.Errorf("mode %q is not all, only or ignore", c.Mode)
	}
	if len(c.UIDs) > maxSpeakerPolicyIDs {
		return fmt.Errorf("%d speaker ids; the most a policy lists is %d", len(c.UIDs), maxSpeakerPolicyIDs)
	}
	seen := map[string]bool{}
	for _, id := range c.UIDs {
		if len(id) > 128 || !reSpeakerID.MatchString(id) {
			return fmt.Errorf("speaker id %q is not an enrolled id (letters, digits, dots, dashes and underscores; the speaker list names them)", id)
		}
		if seen[id] {
			return fmt.Errorf("speaker id %q is listed twice", id)
		}
		seen[id] = true
	}
	switch c.Unidentified {
	case "", unidentifiedDeliver, unidentifiedWithhold:
	default:
		return fmt.Errorf("unidentified %q is not deliver or withhold", c.Unidentified)
	}
	if mode == speakerModeAll && len(c.UIDs) > 0 {
		return fmt.Errorf("mode all takes no speaker ids")
	}
	return nil
}

// .
// .
// .
func speakerPolicyFromChange(v interface{}) (SpeakerPolicyConfig, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return SpeakerPolicyConfig{}, fmt.Errorf("the policy cannot be read: %v", err)
	}
	// .
	// .
	// .
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return SpeakerPolicyConfig{}, fmt.Errorf("the policy is an object with mode, uids and unidentified: %v", err)
	}
	if fields == nil {
		return SpeakerPolicyConfig{}, fmt.Errorf("the policy must be an object, not null")
	}
	var out SpeakerPolicyConfig
	for key, value := range fields {
		var target any
		switch key {
		case "mode":
			target = &out.Mode
		case "uids":
			target = &out.UIDs
		case "unidentified":
			target = &out.Unidentified
		default:
			return SpeakerPolicyConfig{}, fmt.Errorf("unknown speaker policy field %q", key)
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return SpeakerPolicyConfig{}, fmt.Errorf("speaker policy field %q cannot be null", key)
		}
		if err := json.Unmarshal(value, target); err != nil {
			return SpeakerPolicyConfig{}, fmt.Errorf("speaker policy field %q: %w", key, err)
		}
	}
	if out.Mode == "" {
		out.Mode = speakerModeAll
	}
	if err := validateSpeakerPolicy(out); err != nil {
		return SpeakerPolicyConfig{}, err
	}
	if out.Mode != speakerModeIgnore {
		out.Unidentified = ""
	}
	return out, nil
}

// .
type speakerPolicy struct {
	mode         string
	uids         map[string]bool
	unidentified string
	revision     uint64
}

func policyFrom(c SpeakerPolicyConfig) speakerPolicy {
	p := speakerPolicy{mode: c.Mode, uids: map[string]bool{}, unidentified: c.Unidentified, revision: c.Revision}
	if p.mode == "" {
		p.mode = speakerModeAll
	}
	if p.unidentified == "" {
		p.unidentified = unidentifiedDeliver
	}
	for _, id := range c.UIDs {
		p.uids[id] = true
	}
	return p
}

// .
func (p speakerPolicy) restricted() bool { return p.mode != speakerModeAll }

// .
// .
// .
// .
func (p speakerPolicy) decide(decision, speakerID string) (bool, string) {
	known := decision == "known" && speakerID != ""
	switch p.mode {
	case speakerModeOnly:
		if known && p.uids[speakerID] {
			return true, ""
		}
		if known {
			return false, "only the listed speakers are heard; this voice is enrolled but not listed"
		}
		return false, "only the listed speakers are heard; this voice was not identified"
	case speakerModeIgnore:
		if known && p.uids[speakerID] {
			return false, "this speaker is ignored"
		}
		if known {
			return true, ""
		}
		if p.unidentified == unidentifiedWithhold {
			return false, "unidentified voices are withheld"
		}
		return true, ""
	}
	return true, ""
}

// .
// .
// .
func (a *App) speakerPolicyNow() speakerPolicy { return policyFrom(a.configSnapshot().Speech.Speakers) }

// .
// .
func (a *App) speakerPolicyState() *dashboard.SpeakerPolicyState {
	c := a.configSnapshot().Speech.Speakers
	p := policyFrom(c)
	uids := make([]string, 0, len(p.uids))
	for id := range p.uids {
		uids = append(uids, id)
	}
	sort.Strings(uids)
	return &dashboard.SpeakerPolicyState{Mode: p.mode, UIDs: uids, Unidentified: p.unidentified, Revision: p.revision,
		WithheldFinals: a.speakerWithheldFinals.Load(), WithheldPartials: a.speakerWithheldPartials.Load()}
}

// .
// .
// .
// .
type heldFinal struct {
	ve      dashboard.VoiceEvent
	shown   bool
	heard   heardUtterance
	enqueue func(dashboard.VoiceEvent)
	timer   *time.Timer
	// .
	// .
	resolved    bool
	deliver     bool
	decision    string
	speakerID   string
	reason      string
	revision    uint64
	observation *pluginhost.Event
}

// .
// .
// .
func (a *App) holdFinal(h *voiceHandle, f *heldFinal) {
	seq := f.heard.Sequence
	h.work.Add(1)
	h.heldMu.Lock()
	if h.held == nil {
		h.held = map[int64]*heldFinal{}
	}
	h.held[seq] = f
	f.timer = time.AfterFunc(speakerDecisionBound, func() { a.decideHeld(h, seq, "", "", true, nil) })
	h.heldMu.Unlock()
}

// .
// .
// .
// .
func (a *App) decideHeld(h *voiceHandle, seq int64, decision, speakerID string, timedOut bool, observation *pluginhost.Event) {
	p := a.speakerPolicyNow()
	h.heldMu.Lock()
	f := h.held[seq]
	if f == nil || f.resolved {
		h.heldMu.Unlock()
		return
	}
	f.timer.Stop()
	f.resolved, f.revision, f.observation = true, p.revision, observation
	f.decision, f.speakerID = decision, speakerID
	f.deliver, f.reason = p.decide(decision, speakerID)
	if timedOut && !f.deliver {
		f.reason += " (no observation within the bound)"
	}
	if h.heldDraining {
		h.heldMu.Unlock()
		return
	}
	h.heldDraining = true
	h.heldMu.Unlock()
	a.drainHeld(h)
}

// .
// .
// .
func (a *App) drainHeld(h *voiceHandle) {
	for {
		h.heldMu.Lock()
		var f *heldFinal
		var seq int64
		for n, candidate := range h.held {
			if f == nil || n < seq {
				seq, f = n, candidate
			}
		}
		if f == nil || !f.resolved {
			h.heldDraining = false
			h.heldMu.Unlock()
			return
		}
		delete(h.held, seq)
		h.heldMu.Unlock()
		// .
		// .
		// .
		if f.deliver {
			p := a.speakerPolicyNow()
			if deliver, reason := p.decide(f.decision, f.speakerID); !deliver {
				f.deliver, f.reason, f.revision = false, reason, p.revision
			}
		}
		_, safe := a.SafeMode()
		if safe || h.closing.Load() {
			f.deliver, f.reason = false, "SAFE or the session's end withheld the held words"
		}
		if f.deliver {
			if f.shown {
				f.enqueue(f.ve)
			}
			a.rememberFinal(h.id, seq, f.heard.Text)
		} else {
			a.withhold(h, seq, f.reason, f.revision)
		}
		// .
		// .
		// .
		if f.observation != nil && !safe {
			if ve, shown := voiceEventFor(*f.observation, false); shown {
				f.enqueue(ve)
			}
			if f.deliver {
				a.noteSpeakerObservation(*f.observation, false)
			}
		}
		if f.deliver {
			a.admitHeldInOrder(h, f)
		} else {
			h.work.Done()
		}
	}
}

// .
// .
// .
// .
// .
func (a *App) admitHeldInOrder(h *voiceHandle, f *heldFinal) {
	h.heldMu.Lock()
	previous := h.heardTail
	next := make(chan struct{})
	h.heardTail = next
	h.heldMu.Unlock()
	go func() {
		defer h.work.Done()
		var once sync.Once
		f.heard.admitted = func() { once.Do(func() { close(next) }) }
		defer f.heard.admitted()
		if previous != nil {
			select {
			case <-previous:
			case <-h.done:
				return
			}
		}
		if _, safe := a.SafeMode(); safe || h.closing.Load() {
			return
		}
		a.handleHeard(context.Background(), f.heard)
	}()
}

// .
// .
// .
func (a *App) withhold(h *voiceHandle, seq int64, reason string, revision uint64) {
	a.speakerWithheldFinals.Add(1)
	h.heldMu.Lock()
	if h.withheld == nil {
		h.withheld = map[int64]bool{}
	}
	h.withheld[seq] = true
	h.heldMu.Unlock()
	log.Printf("VOICE: final %d on %s withheld by the speaker policy (revision %d): %s", seq, h.id, revision, reason)
	a.fanVoiceEvent(dashboard.VoiceEvent{SessionID: h.id, Sequence: seq, Type: "transcript_withheld", Reason: reason, Revision: revision})
	a.noteReplyOutcome(h.id, "withheld by the speaker policy, no reply")
}

// .
// .
func (a *App) withholdHeld(h *voiceHandle, why string) {
	h.heldMu.Lock()
	held := h.held
	h.held = nil
	h.heldMu.Unlock()
	if len(held) == 0 {
		return
	}
	revision := a.speakerPolicyNow().revision
	for seq, f := range held {
		f.timer.Stop()
		a.withhold(h, seq, why, revision)
		h.work.Done()
	}
}

// .
// .
// .
func (a *App) observationForHeld(ev pluginhost.Event) bool {
	val, ok := a.voiceSessions.Load(ev.SessionID)
	if !ok {
		return false
	}
	h := val.(*voiceHandle)
	var body speakerObservation
	if json.Unmarshal(ev.Raw, &body) != nil || body.RefersTo == 0 {
		return false
	}
	h.heldMu.Lock()
	_, held := h.held[body.RefersTo]
	withheld := h.withheld[body.RefersTo]
	h.heldMu.Unlock()
	if held {
		a.decideHeld(h, body.RefersTo, body.Decision, body.knownID(), false, &ev)
	}
	return held || withheld
}
