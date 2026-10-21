package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

const (
	speakerModeAll    = "all"
	speakerModeOnly   = "only"
	speakerModeIgnore = "ignore"

	unidentifiedDeliver  = "deliver"
	unidentifiedWithhold = "withhold"

	maxSpeakerPolicyIDs = 64
)

var reSpeakerID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

const maxOperatorSpeakers = 8

func operatorSpeakersFromChange(v any) ([]string, error) {
	if v == nil {
		return nil, nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("the list cannot be read: %v", err)
	}
	var ids []string
	if err := json.Unmarshal(raw, &ids); err != nil {
		return nil, fmt.Errorf("the operator's voices are a list of speaker UUIDs: %v", err)
	}
	if len(ids) > maxOperatorSpeakers {
		return nil, fmt.Errorf("%d speaker UUIDs; the most that are one operator's is %d", len(ids), maxOperatorSpeakers)
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if !speakerUUID(id) {
			return nil, fmt.Errorf("%q is not a speaker UUID (an enrolled speaker's id does not name the operator's voice)", id)
		}
		if seen[id] {
			return nil, fmt.Errorf("speaker UUID %q is listed twice", id)
		}
		seen[id] = true
	}
	if len(ids) == 0 {
		return nil, nil
	}
	return ids, nil
}

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

func speakerPolicyFromChange(v any) (SpeakerPolicyConfig, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return SpeakerPolicyConfig{}, fmt.Errorf("the policy cannot be read: %v", err)
	}

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

func (p speakerPolicy) restricted() bool { return p.mode != speakerModeAll }

func (p speakerPolicy) decide(decision, speakerID string) (bool, string) {
	if decision != "known" || speakerID == "" {
		return p.decideIdentity(nil)
	}
	return p.decideIdentity([]string{speakerID})
}

func (p speakerPolicy) lists(ids []string) bool {
	for _, id := range ids {
		if p.uids[id] {
			return true
		}
	}
	return false
}

func (p speakerPolicy) ignores(ids []string) bool {
	return p.mode == speakerModeIgnore && p.lists(ids)
}

func (p speakerPolicy) decideIdentity(ids []string) (bool, string) {
	known := len(ids) > 0
	switch p.mode {
	case speakerModeOnly:
		if p.lists(ids) {
			return true, ""
		}
		if known {
			return false, "only the listed speakers are heard; this speaker identifier is not listed"
		}
		return false, "only the listed speakers are heard; this voice was not identified"
	case speakerModeIgnore:
		if p.lists(ids) {
			return false, "this speaker is ignored"
		}
		if known {
			return true, ""
		}
		if p.unidentified == unidentifiedWithhold {
			return false, "unidentified voices are withheld"
		}
		return true, ""
	case speakerModeAll:
		return true, ""
	}

	return false, "the speaker policy's mode is not one this host knows; nobody is heard until it is corrected"
}

func (a *App) speakerPolicyNow() speakerPolicy { return policyFrom(a.configSnapshot().Speech.Speakers) }

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

type heldFinal struct {
	ve      dashboard.VoiceEvent
	shown   bool
	heard   heardUtterance
	enqueue func(dashboard.VoiceEvent)
	timer   *time.Timer

	resolved    bool
	deliver     bool
	decision    string
	speakerIDs  []string
	reason      string
	revision    uint64
	observation *voiceFrame
}

func (a *App) holdFinal(h *voiceHandle, f *heldFinal) {
	seq := f.heard.Sequence
	h.work.Add(1)
	h.heldMu.Lock()
	if h.held == nil {
		h.held = map[int64]*heldFinal{}
	}
	h.held[seq] = f
	f.timer = time.AfterFunc(speakerDecisionBound, func() { a.decideHeld(h, seq, "", nil, true, nil) })
	h.heldMu.Unlock()
}

func (a *App) decideHeld(h *voiceHandle, seq int64, decision string, speakerIDs []string, timedOut bool, observation *voiceFrame) {
	p := a.speakerPolicyNow()
	h.heldMu.Lock()
	f := h.held[seq]
	if f == nil || f.resolved {
		h.heldMu.Unlock()
		return
	}
	f.timer.Stop()
	f.resolved, f.revision, f.observation = true, p.revision, observation
	f.decision, f.speakerIDs = decision, speakerIDs
	f.deliver, f.reason = p.decideIdentity(speakerIDs)
	if observation != nil && !f.deliver {
		if observation.err == nil && observation.observation().Reason == "speaker_profile_pending" {
			f.reason += "; speaker identification pending — awaiting corroborating speech"
		}
	}
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

		if f.deliver {
			p := a.speakerPolicyNow()
			if deliver, reason := p.decideIdentity(f.speakerIDs); !deliver {
				f.deliver, f.reason, f.revision = false, reason, p.revision
			}
		}
		_, safe := a.SafeMode()
		if safe || h.closing.Load() {
			f.deliver, f.reason = false, "SAFE or the session's end withheld the held words"
		}
		if !safe {
			delivery := "withheld"
			if f.deliver {
				delivery = "delivered"
			}
			a.retainHeard(h.id, seq, f.heard.Text, f.ve, delivery, f.reason, f.observation)
		}
		if f.deliver {
			if f.observation == nil && f.ve.TrackID != "" {

				payload, _ := json.Marshal(map[string]any{"session": h.id, "sequence": seq,
					"decision": "uncertain", "reason": "host_attribution_wait_expired", "source": "host"})
				a.speakerPending.Store(voiceRefKey(h.id, seq), string(payload))
				f.ve.Attribution = attributionOf(string(payload))
			}
			if f.shown {
				f.enqueue(f.ve)
			}
			a.rememberFinal(h.id, seq, f.heard.Text, f.ve)
			if f.observation != nil {
				a.rememberSpeakerRevision(h, *f.observation)
			}
		} else {
			a.withhold(h, seq, f.reason, f.revision)
			f.heard.trace.end("withheld by the speaker policy, no reply")
		}

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

func (a *App) admitHeldInOrder(h *voiceHandle, f *heldFinal) {
	f.heard.trace.stamp(stageReleased)
	if f.heard.words == nil {

		f.heard.words = a.deliverSpoken(f.heard, h.work.Done)
	}
	words := f.heard.words
	h.heldMu.Lock()
	previous := h.heardTail
	next := make(chan struct{})
	h.heardTail = next
	h.heldMu.Unlock()

	a.engines.start("the heard words of session "+h.id, func() {
		defer a.settleSpoken(words)
		var once sync.Once
		f.heard.admitted = func() { once.Do(func() { close(next) }) }
		defer f.heard.admitted()
		if previous != nil && !a.awaitWordsAhead(h, f.heard, previous) {
			f.heard.trace.end("not recorded: the identity is stopping and the session is over")
			return
		}
		if reason, safe := a.SafeMode(); safe {

			a.voiceSafeDropped.Add(1)
			logsink.Warn("voice.refusal", "SAFE holds (%s): final %d on %s, delivered before SAFE began, is heard by no record", reason, f.heard.Sequence, h.id)
			a.noteReplyOutcome(h.id, "withheld under SAFE, no reply")
			f.heard.trace.end("withheld under SAFE, no reply")
			if f.heard.Sequence != 0 {
				a.speakerPending.Delete(voiceRefKey(h.id, f.heard.Sequence))
			}
			return
		}
		a.handleHeard(a.lifetime(), f.heard)
	})
}

func (a *App) withhold(h *voiceHandle, seq int64, reason string, revision uint64) {
	a.speakerWithheldFinals.Add(1)
	h.heldMu.Lock()
	if h.withheld == nil {
		h.withheld = map[int64]bool{}
	}
	h.withheld[seq] = true
	h.heldMu.Unlock()
	logsink.Info("voice.refusal", "final %d on %s withheld by the speaker policy (revision %d): %s", seq, h.id, revision, reason)
	a.fanVoiceEvent(dashboard.VoiceEvent{SessionID: h.id, Sequence: seq, Type: "transcript_withheld", Reason: reason + "; recent context is available through recall source=heard", Revision: revision})
	a.noteReplyOutcome(h.id, "withheld by the speaker policy, no reply")
}

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
		a.retainHeard(h.id, seq, f.heard.Text, f.ve, "withheld", why, f.observation)
		a.withhold(h, seq, why, revision)
		f.heard.trace.end("withheld: " + why)
		h.work.Done()
	}
}

func (a *App) observationForHeld(ev voiceFrame) bool {
	h := a.voiceHandle(ev.SessionID)
	if h == nil {
		return false
	}
	body := ev.observation()
	if ev.err != nil || body.RefersTo == 0 {
		return false
	}
	h.heldMu.Lock()
	_, held := h.held[body.RefersTo]
	withheld := h.withheld[body.RefersTo]
	h.heldMu.Unlock()
	if held {
		a.decideHeld(h, body.RefersTo, body.Decision, body.filterIDs(), false, &ev)
	}
	return held || withheld
}
