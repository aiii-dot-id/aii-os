package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/continuity"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/memory"
	"github.com/aiii-dot-id/aii-os/internal/memory/attention"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tlsfault"
	"github.com/aiii-dot-id/aii-os/internal/witness"
)

func dreamConfig(cfg Config) cognitive.DreamConfig {
	return cognitive.DreamConfig{
		Threshold:        1,
		MaxChars:         cfg.Prompt.SurfacingMaxChars,
		TensionsMaxChars: cfg.Prompt.TensionsMaxChars,
		ExcerptChars:     cfg.Prompt.ExcerptChars,

		ConversationMaxChars: cfg.Prompt.DreamConversationMaxChars,
		RoomNotePrefix:       voiceMarker + voiceRoomNote,

		Spacing: rhythmSpacing(cfg),
	}
}

func consolidateConfig(cfg Config) cognitive.ConsolidateConfig {
	return cognitive.ConsolidateConfig{
		Threshold:        3,
		Salience:         cfg.Memory.Salience,
		Ring3MaxChars:    cfg.Prompt.Ring3MaxChars,
		TensionsMaxChars: cfg.Prompt.TensionsMaxChars,

		ExcerptChars:      cfg.Prompt.ExcerptChars,
		RecentExperiences: cfg.Prompt.Ring3RecentExperiences,

		OutcomeWindow:       cfg.Agency.OutcomeWindow,
		ObservationMaxChars: cfg.Prompt.SurfacingMaxChars,

		Batch:  cfg.Cognition.ConsolidateBatch,
		MaxOps: cfg.Cognition.ConsolidateMaxOps,
	}
}

func selfModelConfig(cfg Config) cognitive.SelfModelConfig {
	return cognitive.SelfModelConfig{
		EvidenceRows: cfg.Cognition.SelfModelEvidenceRows,
		SpokenRows:   cfg.Cognition.SelfModelSpokenRows,
	}
}

func briefConfig(cfg Config) cognitive.MorningBriefConfig {
	return cognitive.MorningBriefConfig{
		LocalTime: cfg.Cognition.MorningBriefAt,
		Timezone:  cfg.Timezone,
		MaxChars:  cfg.Prompt.BriefMaxChars,
		Spacing:   rhythmSpacing(cfg),
	}
}

func rhythmSpacing(cfg Config) time.Duration {
	return time.Duration(spacingSetting.value(cfg)) * time.Second
}

func (a *App) wireCognitive(bgCtx context.Context, anchorer *witness.Anchorer, door *ledgerAdapter, cfg Config) error {
	stAdapt := a.store
	llmAdapt := facilityLLM{a.llmSwap, a}

	ringWriter := cognitive.NewPersistingRingWriter(a.rings, a.store)
	briefWriter := cognitive.NewPersistingBriefWriter(a.rings, a.store)

	if a.timeFac == nil {
		a.wakeMu.Lock()
		a.timeFac = cognitive.NewTIME(stAdapt, stAdapt)
		a.wakeMu.Unlock()
		a.timeFac.SetSafeSource(func() bool { _, s := a.SafeMode(); return s })
	}

	teller := &cognitive.Teller{Report: a.noteCondition, Door: door, Exists: a.store.EntityExists, Room: a.facilityRoom}
	dreamFac := cognitive.NewDream(stAdapt, llmAdapt, door, ringWriter, dreamConfig(cfg))
	dreamFac.SetAuthority(ringAuthority{a.promptGate, a.store})
	dreamFac.SetTeller(teller)
	dreamFac.SetConversation(stAdapt)
	dreamFac.SetTensions(stAdapt)
	a.timeFac.RegisterOwner(dreamFac)
	consolidateFac := cognitive.NewConsolidate(stAdapt, llmAdapt, door, ringWriter, consolidateConfig(cfg))
	consolidateFac.SetAuthority(ringAuthority{a.promptGate, a.store})
	consolidateFac.SetTensions(stAdapt)
	consolidateFac.SetOutcomes(stAdapt)
	consolidateFac.SetDecisionLog(a.store)
	consolidateFac.SetTeller(teller)
	dreamFac.SetHeld(consolidateFac)
	a.timeFac.RegisterOwner(consolidateFac)
	a.cognition.consolidateFacility.Store(consolidateFac)
	a.reconcileRing3(consolidateFac)
	selfModelFac := cognitive.NewSelfModel(stAdapt, llmAdapt, selfModelCommitter{engine: a.engine})
	selfModelFac.Reconfigure(selfModelConfig(cfg))
	selfModelFac.SetAuthority(ringAuthority{a.promptGate, a.store})
	selfModelFac.SetTeller(teller)
	a.timeFac.RegisterOwner(selfModelFac)
	reviewFac := cognitive.NewIdentityReview(stAdapt)
	a.timeFac.RegisterOwner(reviewFac)

	a.cognition.reviewFacility.Store(reviewFac)
	a.briefFacility = cognitive.NewMorningBrief(stAdapt, llmAdapt, briefWriter, briefConfig(cfg))
	a.briefFacility.SetAuthority(ringAuthority{a.promptGate, a.store})
	a.briefFacility.SetTeller(teller)
	a.briefFacility.SetTurnGate(facilityGate{a})
	a.briefFacility.SetAttention(func(ctx context.Context) ([]memory.AttentionItem, error) {
		return a.engine.Instruments().Attention(ctx, time.Now(), attention.CostLow)
	})
	a.timeFac.RegisterOwner(a.briefFacility)

	selfModelFac.SetDoor(door)
	rhythmFac := cognitive.NewRhythm(stAdapt, facilityGate{a}, dreamFac, consolidateFac, selfModelFac, reviewFac)
	rhythmFac.SetSpacing(rhythmSpacing(cfg))
	rhythmFac.SetOutcomes(consolidateFac)
	rhythmFac.SetConversation(dreamFac)
	rhythmFac.SetDecisionLog(func(facility, decision, reason string) {
		if err := a.store.RecordMemoryDecision(store.MemoryDecision{Kind: "rhythm", Facility: facility, Decision: decision, Record: map[string]any{"reason": reason}}); err != nil {
			logsink.Warn("rhythm.error", "decision not logged: %v", err)
		}
	})
	rhythmFac.SetAttention(a.store, door, func(id, content string) {
		if _, err := a.store.AddOutboxNoticeOnce(id, "operator", "", content, nil); err != nil {
			logsink.Warn("rhythm.error", "attention outbox: %v", err)
		}
	})
	a.timeFac.RegisterOwner(rhythmFac)

	a.cognition.rhythm.Store(&rhythmSet{dream: dreamFac, consolidate: consolidateFac, selfModel: selfModelFac, brief: a.briefFacility, rhythm: rhythmFac,
		pulseSeconds: pulseSeconds(cfg)})

	pulseInterval := time.Duration(pulseSeconds(cfg)) * time.Second
	a.pulseSource = &dashboardPulse{live: a.dashboard.SessionLive, active: a.dashboard.OperatorActiveWithin, interval: pulseInterval}
	a.timeFac.StartHeartbeat(a.pulseSource)

	if cfg.Witness.URL != "" {
		a.timeFac.Every("witness", 30*time.Second, func() { a.witnessPass(bgCtx, anchorer) })
	}

	if err := a.timeFac.EvaluateAll(bgCtx); err != nil {
		return fmt.Errorf("TIME boot catch-up: %w", err)
	}

	if err := a.armFacilityAlarms(cfg); err != nil {
		return err
	}

	a.timeFac.SetQuiesceGate(a.gate)
	a.timeFac.Start(bgCtx)
	return nil
}

func (a *App) witnessPass(ctx context.Context, anchorer *witness.Anchorer) {
	if ctx.Err() != nil {
		return
	}
	if a.currentMode() == ModeSafe {
		return
	}
	st, err := a.witnessProbe.Status()
	if err != nil {
		a.witnessAttempt(err)
		if fault := (*tlsfault.Fault)(nil); errors.As(err, &fault) {
			logsink.Warn("witness.error", "Witness certificate refused: %s", tlsfault.ForTheLog(fault))
		} else {
			logsink.Warn("witness.error", "Witness unreachable: %v", err)
		}
		return
	}
	a.witnessAttempt(nil)
	if err := anchorer.AnchorPass(ctx, st); err != nil {
		logsink.Warn("witness.error", "anchor failed (health OK): %v", err)
	}
}

func (a *App) armRhythm(seconds int) error {
	every := int64(seconds) * 1000
	return a.timeFac.SetAlarm("rhythm", "rhythm", "wall", time.Now().UTC().UnixMilli()+every, &every, "")
}

func (a *App) armFacilityAlarms(cfg Config) error {

	if err := a.armRhythm(cfg.Agency.RhythmSeconds); err != nil {
		return fmt.Errorf("arm cognitive rhythm: %w", err)
	}

	selfModelEvery, reviewEvery, err := a.armReflection(cfg)
	if err != nil {
		return err
	}
	if a.briefFacility != nil {
		brief, err := a.armBriefAtStart(rhythmSpacing(cfg))
		if err != nil {
			return err
		}
		logsink.Info("rhythm.start", "Cognitive rhythm armed: metabolism every %ds wall-clock, capacity-gated, consolidation at most every %s; reflection on lived time (self-model every %d pulses, review every %d — a pulse counts only with operator interaction); morning_brief(%s)",
			cfg.Agency.RhythmSeconds, rhythmSpacing(cfg), selfModelEvery, reviewEvery, brief)
	}

	a.continuity.EnsureSnapshotKey(continuitySettingsOf(cfg))
	a.continuity.SweepSnapshotDebrisAtBoot(continuitySettingsOf(cfg))

	if err := continuity.ArmMaintenanceAlarm(a.timeFac, time.Now()); err != nil {
		return fmt.Errorf("arm maintenance: %w", err)
	}

	if err := a.armMemoryBackfill(); err != nil {
		return fmt.Errorf("arm memory backfill: %w", err)
	}
	return nil
}

func (a *App) goLive(started Config) {
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	a.live.Store(true)
	a.applyAgency(a.cfg.Agency)
	if a.cfg.Agency.RhythmSeconds != started.Agency.RhythmSeconds && a.timeFac != nil {
		if err := a.armRhythm(a.cfg.Agency.RhythmSeconds); err != nil {
			logsink.Warn("config.refusal", "metabolism rhythm not re-armed, it keeps its cadence until the next boot: %v", err)
		}
	}
	a.applyCognition(*a.cfg)
}

func (a *App) armReflection(cfg Config) (selfModelEvery, reviewEvery int64, err error) {
	life := a.timeFac.LifeClock()
	pulse := pulseSeconds(cfg)
	selfModelEvery = reflectionPulses(cognitionValue(cfg, "cognition.self_model_every_minutes"), pulse)
	reviewEvery = reflectionPulses(cognitionValue(cfg, "cognition.identity_review_every_minutes"), pulse)
	if err := a.timeFac.EnsureAlarm(cognitive.ReflectSelfModelAlarm, "rhythm", "life", life+selfModelEvery, &selfModelEvery, ""); err != nil {
		return 0, 0, fmt.Errorf("arm self-model reflection: %w", err)
	}
	if err := a.timeFac.EnsureAlarm(cognitive.ReflectIdentityReviewAlarm, "rhythm", "life", life+reviewEvery, &reviewEvery, ""); err != nil {
		return 0, 0, fmt.Errorf("arm identity review: %w", err)
	}
	return selfModelEvery, reviewEvery, nil
}

func (a *App) armBriefAtStart(spacing time.Duration) (string, error) {
	if !a.briefFacility.Off() {
		owed := min(a.briefFacility.NextDeadline(), time.Now().Add(spacing).UnixMilli())
		switch due, stands, err := a.store.AlarmDeadline("morning_brief", "morning_brief"); {
		case err != nil:
			return "", fmt.Errorf("arm morning brief: %w", err)
		case stands && due <= owed:
			return time.UnixMilli(due).UTC().Format("15:04 UTC") + ", left as it stood: a brief still owed", nil
		}
	}
	return a.armBrief()
}

func (a *App) armBrief() (string, error) {

	deadline := a.briefFacility.NextDeadline()
	if err := a.timeFac.SetAlarm("morning_brief", "morning_brief", "wall", deadline, nil, ""); err != nil {
		return "", fmt.Errorf("arm morning brief: %w", err)
	}
	if a.briefFacility.Off() {
		return "off", nil
	}
	return time.UnixMilli(deadline).UTC().Format("15:04 UTC"), nil
}

func cognitionValue(cfg Config, key string) int {
	s, ok := cognitionSettingFor(key)
	if !ok {
		panic("cognitionValue: no setting " + key)
	}
	return s.value(cfg)
}

func (a *App) applyCognition(cfg Config) {
	if a.store != nil {
		a.store.SetSelfModelMaxTokens(cfg.Prompt.SelfModelMaxTokens)
	}
	set := a.cognition.rhythm.Load()
	if set == nil {
		return
	}

	cfg.Prompt.PulseIntervalSeconds = set.pulseSeconds
	set.dream.Reconfigure(dreamConfig(cfg))
	set.consolidate.Reconfigure(consolidateConfig(cfg))
	set.selfModel.Reconfigure(selfModelConfig(cfg))
	set.rhythm.SetSpacing(rhythmSpacing(cfg))
	if a.timeFac == nil {
		return
	}
	if _, _, err := a.armReflection(cfg); err != nil {
		logsink.Warn("rhythm.refusal", "reflective cadences not re-armed; they keep their cadence until the next boot: %v", err)
	}
	if set.brief != nil {

		if set.brief.Reconfigure(briefConfig(cfg)) {
			if _, err := a.armBrief(); err != nil {
				logsink.Warn("rhythm.refusal", "the morning brief's alarm not re-armed: %v", err)
			}
		}
	}
}

func (a *App) cognitionReconsider() {
	if set := a.cognition.rhythm.Load(); set != nil {
		set.consolidate.Reconsider()
	}
}

func (a *App) facilityRoom() string {
	if _, source := a.currentFacilityBudget(); source.window() {
		return cognitive.WiderWindow
	}
	return "a context_length declared for the model on its provider entry (Settings → Providers) or a higher prompt.max_tokens"
}

func (a *App) noteCondition(c cognitive.Condition) {
	a.cognition.condMu.Lock()
	if a.cognition.conditions == nil {
		a.cognition.conditions = map[string]cognitionCondition{}
	}
	prev, had := a.cognition.conditions[c.Facility]
	changed := !had || prev.Kind != c.Kind || prev.Said != c.Said
	if changed {
		a.cognition.conditions[c.Facility] = cognitionCondition{Condition: c, at: time.Now().UTC()}
	}
	a.cognition.condMu.Unlock()
	if changed && a.dashboard != nil {
		a.dashboard.BroadcastConfig()
	}
}

func (a *App) cognitionConditions() []dashboard.CognitionConditionState {
	a.cognition.condMu.Lock()
	defer a.cognition.condMu.Unlock()
	out := []dashboard.CognitionConditionState{}
	for _, c := range a.cognition.conditions {
		if c.Kind == cognitive.ConditionWhole {
			continue
		}
		out = append(out, dashboard.CognitionConditionState{Facility: c.Facility, Kind: string(c.Kind), Said: c.Said, At: c.at.Format(time.RFC3339)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Facility < out[j].Facility })
	return out
}

type dashboardPulse struct {
	live     func() bool
	interval time.Duration

	active func(d time.Duration) bool

	ovMu     sync.Mutex
	override bool
}

func (d *dashboardPulse) setOverride(v bool) {
	d.ovMu.Lock()
	d.override = v
	d.ovMu.Unlock()
}

func (d *dashboardPulse) Interval() time.Duration { return d.interval }
func (d *dashboardPulse) Live() bool {
	d.ovMu.Lock()
	ov := d.override
	d.ovMu.Unlock()
	if !(ov || d.live()) {
		return false
	}
	if d.active == nil {
		return true
	}
	window := d.interval
	if window <= 0 {
		window = cognitive.DefaultPulseInterval
	}
	return d.active(window)
}
