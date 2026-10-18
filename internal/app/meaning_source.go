package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/memory"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

const (
	meaningSourceKey = "memory.meaning_source"

	meaningChoiceNone     = "none"
	meaningChoiceProvider = "provider"
	meaningChoicePlugin   = "plugin:"

	embedCueDeadline    = 5 * time.Second
	embedMemoryDeadline = 15 * time.Second

	gateMargin = 2 * time.Second

	sourceCheckBound = 2 * time.Minute
	sourceCheckRetry = 30 * time.Second
)

const (
	meaningOffNoSource   = "no_source"
	meaningOffTurnedOff  = "turned_off"
	meaningOffNotRunning = "not_running"
	meaningOffUnchosen   = "unchosen"
	meaningOffSafe       = "safe"
	meaningOffGrant      = "operator_grant"
	meaningOffUnread     = "unread"
	meaningOffChanged    = "changed"
	meaningOffUnrecorded = "unrecorded"
	meaningOffChecking   = "checking"
	meaningOffUnanswered = "check_unanswered"
	meaningOffProbe      = "probe_failed"
)

type meaningOff struct {
	Kind   string
	Detail string
}

func (e *meaningOff) Error() string { return e.Detail }

func validMeaningChoice(choice string) error {
	switch {
	case choice == "", choice == meaningChoiceNone, choice == meaningChoiceProvider:
		return nil
	case strings.HasPrefix(choice, meaningChoicePlugin) && packagefmt.ValidManifestID(strings.TrimPrefix(choice, meaningChoicePlugin)):
		return nil
	}
	return fmt.Errorf("want %q, %q, %q<package id>, or nothing for no choice", meaningChoiceNone, meaningChoiceProvider, meaningChoicePlugin)
}

type meaningPick struct {
	source memory.Embedder

	kind   string
	plugin string
	off    *meaningOff

	question string

	derived string
}

func (a *App) embeddingsPlugins() []*pluginhost.ActivePlugin {
	a.pluginMu.RLock()
	defer a.pluginMu.RUnlock()
	var out []*pluginhost.ActivePlugin
	for _, p := range a.plugins {
		if p != nil && p.Embeddings != nil {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

const bothThere = "A plugin that makes vectors on this machine is running, and the provider names an embeddings model. Which of them reads the memories is chosen here."

func (a *App) pickMeaningSource() meaningPick {
	cfg := a.configSnapshot()
	offered := a.embeddingsPlugins()
	entry := a.currentProvider()
	named := entry.EmbeddingsModel != ""
	pick := a.pickFor(&cfg, offered, entry)
	if named && len(offered) > 0 && pick.question == "" {
		pick.question = bothThere
	}
	return pick
}

func (a *App) pickFor(cfg *Config, offered []*pluginhost.ActivePlugin, entry providerEntry) meaningPick {
	choice := cfg.Memory.MeaningSource
	_, safe := a.SafeMode()
	named := entry.EmbeddingsModel != ""
	provider := meaningPick{source: memoryEmbedder{a}, kind: "provider"}
	off := func(kind, format string, args ...any) meaningPick {
		reason := &meaningOff{Kind: kind, Detail: fmt.Sprintf(format, args...)}
		return meaningPick{source: noSource{reason}, off: reason}
	}
	switch {
	case choice == meaningChoiceNone:
		return off(meaningOffTurnedOff, "the meaning layer is turned off (the dashboard's Memory card)")
	case choice == meaningChoiceProvider:
		return provider
	case strings.HasPrefix(choice, meaningChoicePlugin):
		id := strings.TrimPrefix(choice, meaningChoicePlugin)
		for _, ap := range offered {
			if ap.ID == id {
				return a.pickPlugin(cfg, ap)
			}
		}
		var gone meaningPick
		if safe {
			gone = off(meaningOffSafe, "the vectors are made by a plugin (%s), and no plugin is asked while the identity is in SAFE", id)
		} else {
			gone = off(meaningOffNotRunning, "the plugin chosen to make the vectors, %s, is not running (the dashboard's Plugins view), and nothing else is asked in its place", id)
		}
		gone.plugin = id
		return gone
	}

	kept, err := a.keptVectorBases()
	if err != nil {

		logsink.Warn("memory.refusal", "which source of vectors is in use could not be read from the record: %v", err)
		return off(meaningOffUnread, "which source of vectors is in use could not be read from the record")
	}

	running := make(map[string]*pluginhost.ActivePlugin, len(offered))
	for _, ap := range offered {
		running[ap.ID] = ap
	}
	var inUse []*pluginhost.ActivePlugin
	var stopped []string
	ownKept := false
	seen := map[string]bool{}
	for _, basis := range kept {
		id, plugin := pluginhost.SourceOfBasis(basis)
		switch {
		case !plugin:
			ownKept = ownKept || strings.HasPrefix(basis, basisPart(entry.Name)+"/")
		case seen[id]:
		case running[id] != nil:
			seen[id] = true
			inUse = append(inUse, running[id])
		default:
			seen[id] = true
			stopped = append(stopped, id)
		}
	}
	var pick meaningPick
	switch {
	case len(inUse) == 1:
		pick = a.pickPlugin(cfg, inUse[0])
		pick.derived = meaningChoicePlugin + inUse[0].ID
	case len(inUse) > 1:
		pick = off(meaningOffUnchosen, "more than one running plugin has made the vectors kept and none is chosen (the dashboard's Memory card)")
		pick.question = "More than one running plugin makes vectors. Which of them reads the memories is the operator's to choose."
	case len(stopped) > 0:

		if safe {
			pick = off(meaningOffSafe, "the vectors are made by a plugin (%s), and no plugin is asked while the identity is in SAFE", stopped[0])
		} else {
			pick = off(meaningOffNotRunning, "the plugin that made the vectors kept, %s, is not running (the dashboard's Plugins view), and nothing else is asked in its place", stopped[0])
		}
		pick.plugin = stopped[0]
		pick.question = fmt.Sprintf("The vectors kept were made by the plugin %s, which is not running. Start it again in Plugins, or choose here what reads the memories.", stopped[0])
	case named && ownKept:
		pick = provider
		pick.derived = meaningChoiceProvider
	case len(offered) == 1:
		pick = a.pickPlugin(cfg, offered[0])
		pick.derived = meaningChoicePlugin + offered[0].ID
	case len(offered) > 1:
		pick = off(meaningOffUnchosen, "more than one running plugin makes vectors and none is chosen (the dashboard's Memory card)")
		pick.question = "More than one running plugin makes vectors. Which of them reads the memories is the operator's to choose."
	case named:

		pick = provider
		pick.derived = meaningChoiceProvider
	case safe:
		pick = off(meaningOffSafe, "no provider's embeddings model is named, and a plugin that makes vectors does not run while the identity is in SAFE")
	default:
		pick = off(meaningOffNoSource, "%s", memory.NoSource)
	}
	return pick
}

var errChoiceTaken = errors.New("another source of vectors is recorded as the choice")

func (a *App) recordMeaningChoice(choice string) error {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		switch stored := a.configSnapshot().Memory.MeaningSource; {
		case stored == choice:
			return nil
		case stored != "":
			return errChoiceTaken
		}
		if _, err = a.applyConfigChange(map[string]any{meaningSourceKey: choice}); err == nil {
			logsink.Info("memory.decision", "the source of vectors is recorded as %s: it is what reads the memories until the operator chooses otherwise (the dashboard's Memory card)", choice)
			return nil
		}
		if !errors.Is(err, errConfigMoved) {
			return err
		}
	}
	return err
}

func (a *App) handOver(choice string) *meaningOff {
	err := a.recordMeaningChoice(choice)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, errChoiceTaken):
		return &meaningOff{Kind: meaningOffChanged, Detail: "another source of vectors is the choice now, and this one is handed nothing more"}
	}
	logsink.Warn("memory.refusal", "what reads the memories could not be recorded (%s): %v", choice, err)
	return &meaningOff{Kind: meaningOffUnrecorded, Detail: "what reads the memories could not be recorded, and nothing is handed over until it is"}
}

func (a *App) chooseByInstalling(ap *pluginhost.ActivePlugin) {
	if a.configSnapshot().Memory.MeaningSource != "" {
		return
	}
	if a.pickMeaningSource().derived != meaningChoicePlugin+ap.ID {
		return
	}
	if err := a.recordMeaningChoice(meaningChoicePlugin + ap.ID); err != nil && !errors.Is(err, errChoiceTaken) {
		logsink.Warn("memory.refusal", "the plugin %s could not be recorded as what reads the memories: %v", ap.ID, err)
	}
}

func (a *App) keptVectorBases() ([]string, error) {
	if a.store == nil {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(a.lifetime(), meaningStateTimeout)
	defer cancel()
	return a.store.MemoryVectorBases(ctx)
}

func (a *App) pickPlugin(cfg *Config, ap *pluginhost.ActivePlugin) meaningPick {
	if reason := a.pluginSourceRefused(cfg, ap); reason != nil {
		return meaningPick{source: noSource{reason}, off: reason, plugin: ap.ID}
	}
	check := a.sourceCheck(ap)
	if !check.over() {
		reason := &meaningOff{Kind: meaningOffChecking, Detail: fmt.Sprintf("the plugin that makes the vectors, %s, is being checked against its publisher's vectors; ask again in a moment", ap.ID)}
		return meaningPick{source: noSource{reason}, off: reason, plugin: ap.ID}
	}
	switch {
	case check.err != nil && check.unanswered:
		reason := &meaningOff{Kind: meaningOffUnanswered, Detail: fmt.Sprintf("the plugin that makes the vectors did not answer its check and is asked again shortly: %v", check.err)}
		return meaningPick{source: noSource{reason}, off: reason, plugin: ap.ID}
	case check.err != nil:
		reason := &meaningOff{Kind: meaningOffProbe, Detail: fmt.Sprintf("the plugin that makes the vectors did not pass its check and is not used until it starts again: %v", check.err)}
		return meaningPick{source: noSource{reason}, off: reason, plugin: ap.ID}
	}
	return meaningPick{source: a.pluginSourceOf(ap), kind: "plugin", plugin: ap.ID}
}

func (a *App) pluginSourceRefused(cfg *Config, ap *pluginhost.ActivePlugin) *meaningOff {
	if _, safe := a.SafeMode(); safe {
		return &meaningOff{Kind: meaningOffSafe, Detail: fmt.Sprintf("the vectors are made by a plugin (%s), and no plugin is asked while the identity is in SAFE", ap.ID)}
	}

	if g, granted := cfg.Plugins.Grants[ap.ID]; granted && !grantEmpty(g) {
		return &meaningOff{Kind: meaningOffGrant, Detail: fmt.Sprintf(
			"the plugin that makes the vectors, %s, holds a grant from the operator (%s), and a package that is handed the memories is asked only while it holds none (the dashboard's Plugins view)",
			ap.ID, strings.Join(grantNames(g), ", "))}
	}
	return nil
}

func grantNames(g broker.Grant) []string {
	var out []string
	add := func(on bool, name string) {
		if on {
			out = append(out, name)
		}
	}
	add(g.KV, "kv")
	add(g.Memory, "memory")
	add(g.Interactions, "interactions")
	add(g.Voice, "voice")
	add(g.Embeddings, "embeddings")
	add(g.Tools, "tools")
	add(g.Files, "files")
	add(g.ReadOnly, "read_only")
	add(g.PlaintextCredentials, "plaintext_credentials")
	add(len(g.Hosts) > 0, "hosts")
	add(len(g.Local) > 0, "local")
	add(len(g.CredentialHandles) > 0, "credential_handles")
	add(len(g.AutoConfirm) > 0, "auto_confirm")
	return out
}

type sourceCheck struct {
	done chan struct{}

	at  time.Time
	err error

	unanswered bool
	again      time.Time
}

func (c *sourceCheck) over() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

func (a *App) sourceCheck(ap *pluginhost.ActivePlugin) *sourceCheck {
	for {
		fresh := &sourceCheck{done: make(chan struct{})}
		held, loaded := a.memory.checks.LoadOrStore(ap, fresh)
		if !loaded {
			a.startSourceCheck(ap, fresh)
			return fresh
		}
		check := held.(*sourceCheck)
		if !check.over() || !check.unanswered || time.Now().Before(check.again) {
			return check
		}
		if a.memory.checks.CompareAndSwap(ap, check, fresh) {
			a.startSourceCheck(ap, fresh)
			return fresh
		}
	}
}

func (a *App) startSourceCheck(ap *pluginhost.ActivePlugin, check *sourceCheck) {

	for _, kept := range []*sync.Map{&a.memory.checks, &a.memory.gates} {
		kept.Range(func(k, _ any) bool {
			if other := k.(*pluginhost.ActivePlugin); other != ap && other.ID == ap.ID {
				kept.Delete(k)
			}
			return true
		})
	}
	retry := a.memory.checkRetry
	if retry <= 0 {
		retry = orDefault(a.memory.limits.checkRetry, sourceCheckRetry)
	}
	run := func() {

		defer a.pluginsChanged()
		defer func() {
			check.at = time.Now().UTC()
			close(check.done)
		}()

		a.chooseByInstalling(ap)

		if ap.DeclaresChecks() {
			check.err = declaredChecks(ap)
			if check.err == nil {
				logsink.Info("memory.decision", "the source of vectors %s passed its publisher's checks on this machine at its start (basis %s, floor %.2f)",
					ap.ID, ap.Embeddings.Basis(), ap.Embeddings.Decl.Floor)
				return
			}
			logsink.Warn("memory.refusal", "the source of vectors %s is not used until it starts again: %v", ap.ID, check.err)
			return
		}

		ctx, cancel := context.WithTimeout(a.lifetime(), sourceCheckBound)
		defer cancel()
		source := a.pluginSourceOf(ap)
		check.err = ap.Embeddings.Probe(ctx, func(ctx context.Context, kind, text string) ([]float32, error) {
			return source.turn(ctx, kind, text, false)
		})
		if check.err == nil {
			logsink.Info("memory.decision", "the source of vectors %s returned its publisher's vectors for all %d probes (basis %s, floor %.2f)",
				ap.ID, len(ap.Embeddings.Decl.Probes), ap.Embeddings.Basis(), ap.Embeddings.Decl.Floor)
			return
		}

		var miss *pluginhost.ProbeError
		if errors.As(check.err, &miss) && miss.Outcome != pluginfacility.CheckWrong {
			check.unanswered, check.again = true, time.Now().Add(retry)
			logsink.Warn("memory.refusal", "the source of vectors %s did not answer its check and is asked again: %v", ap.ID, check.err)
			return
		}
		logsink.Warn("memory.refusal", "the source of vectors %s is not used until it starts again: %v", ap.ID, check.err)
	}
	if !a.runBackground(run) {
		check.err, check.unanswered = errors.New("the identity is stopping"), true
		check.again = time.Now().Add(retry)
		close(check.done)
	}
}

func (a *App) probesView(ap *pluginhost.ActivePlugin) *dashboard.PluginProbesView {
	if ap == nil || ap.Embeddings == nil || ap.DeclaresChecks() {
		return nil
	}
	held, ok := a.memory.checks.Load(ap)
	if !ok {
		return &dashboard.PluginProbesView{State: "unchecked"}
	}
	check := held.(*sourceCheck)
	if !check.over() {
		return &dashboard.PluginProbesView{State: "checking"}
	}
	out := &dashboard.PluginProbesView{At: check.at.Format(time.RFC3339)}
	switch {
	case check.err == nil:
		out.State = "passed"
	case check.unanswered:
		out.State, out.At, out.Words = "pending", "", check.err.Error()
	default:
		out.State, out.Words = "failed", check.err.Error()
	}
	return out
}

func declaredChecks(ap *pluginhost.ActivePlugin) error {
	c := ap.Checked()
	switch {
	case c == nil:
		return fmt.Errorf("plugin %s: its publisher's checks did not run at its start", ap.ID)
	case c.Failed != nil:
		return fmt.Errorf("plugin %s: its publisher's checks have not yet passed on this machine (%q %s: %s); they run again at its next start",
			ap.ID, c.Failed.Check, c.Failed.Outcome, c.Failed.Words)
	}
	return nil
}

func (a *App) checkSourceAtAdoption(ap *pluginhost.ActivePlugin) {
	if ap == nil || ap.Embeddings == nil {
		return
	}
	if _, safe := a.SafeMode(); safe {
		return
	}
	a.sourceCheck(ap)
}

type sourceEmbedder struct{ a *App }

func (e sourceEmbedder) Choose() memory.Embedder { return e.a.pickMeaningSource().source }

func (e sourceEmbedder) Basis() (string, error) { return e.Choose().Basis() }
func (e sourceEmbedder) Floor() float64         { return e.Choose().Floor() }
func (e sourceEmbedder) Embed(ctx context.Context, kind memory.Kind, inputs []string) (string, [][]float32, error) {
	return e.Choose().Embed(ctx, kind, inputs)
}

type noSource struct{ why *meaningOff }

func (s noSource) Basis() (string, error) { return "", s.why }
func (noSource) Floor() float64           { return 0 }
func (s noSource) Embed(context.Context, memory.Kind, []string) (string, [][]float32, error) {
	return "", nil, s.why
}

type pluginSource struct {
	a  *App
	ap *pluginhost.ActivePlugin

	cue, memory, work time.Duration
}

func (a *App) pluginSourceOf(ap *pluginhost.ActivePlugin) pluginSource {
	l := a.memory.limits
	return pluginSource{a: a, ap: ap, cue: l.cueCall(), memory: l.memoryCall(), work: l.localWork()}
}

func (s pluginSource) PassWork() time.Duration { return s.work }

func (s pluginSource) Basis() (string, error) { return s.ap.Embeddings.Basis(), nil }

func (s pluginSource) Floor() float64 { return s.ap.Embeddings.Decl.Floor }

func (pluginSource) Batch() int { return 1 }

func (s pluginSource) Epoch() any { return s.ap }

func (s pluginSource) MayContinue() bool { return !s.a.voiceHoldsBackfill() }

func (s pluginSource) Embed(ctx context.Context, kind memory.Kind, inputs []string) (string, [][]float32, error) {
	basis := s.ap.Embeddings.Basis()
	arg := pluginhost.EmbedKindMemory
	if kind == memory.KindCue {
		arg = pluginhost.EmbedKindCue
	}
	vectors := make([][]float32, 0, len(inputs))
	for _, text := range inputs {
		if err := s.admitted(); err != nil {
			return basis, nil, err
		}
		v, err := s.turn(ctx, arg, text, true)
		if err != nil {
			return basis, nil, err
		}
		vectors = append(vectors, v)
	}
	return basis, vectors, nil
}

func (s pluginSource) deadline(kind string) time.Duration {
	if kind == pluginhost.EmbedKindCue {
		return s.cue
	}
	return s.memory
}

func (s pluginSource) admitted() error {
	cfg := s.a.configSnapshot()
	if reason := s.a.pluginSourceRefused(&cfg, s.ap); reason != nil {
		return reason
	}
	if reason := s.a.handOver(meaningChoicePlugin + s.ap.ID); reason != nil {
		return reason
	}
	return nil
}

func (s pluginSource) turn(ctx context.Context, kind, text string, own bool) ([]float32, error) {
	g := s.a.gateOf(s.ap)
	if err := g.take(ctx, kind == pluginhost.EmbedKindCue, s.deadline(kind)); err != nil {
		return nil, &waitedBehind{plugin: s.ap.ID, kind: kind, err: err}
	}
	defer g.release()
	if own {
		if err := s.admitted(); err != nil {
			return nil, err
		}
	}
	v, err := s.ask(ctx, kind, text)
	if err != nil && own {
		v, err = s.whose(ctx, kind, text, err)
	}
	return v, err
}

func (a *App) gateOf(ap *pluginhost.ActivePlugin) *callGate {
	if g, ok := a.memory.gates.Load(ap); ok {
		return g.(*callGate)
	}
	g, _ := a.memory.gates.LoadOrStore(ap, &callGate{})
	return g.(*callGate)
}

type callGate struct {
	mu sync.Mutex

	held bool
	ends time.Time

	cues, memories []*waiting
}

type waiting struct {
	handed   chan struct{}
	deadline time.Duration
}

var errBehind = errors.New("waited behind the source's other call past that call's own deadline")

func (g *callGate) take(ctx context.Context, cue bool, deadline time.Duration) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.held {
		g.held, g.ends = true, time.Now().Add(deadline)
		return nil
	}
	w := &waiting{handed: make(chan struct{}), deadline: deadline}
	queue := &g.memories
	if cue {
		queue = &g.cues
	}
	*queue = append(*queue, w)
	for {

		bound := time.NewTimer(time.Until(g.ends.Add(gateMargin)))
		g.mu.Unlock()
		select {
		case <-w.handed:
		case <-ctx.Done():
		case <-bound.C:
		}
		bound.Stop()
		g.mu.Lock()
		select {
		case <-w.handed:
			if err := ctx.Err(); err != nil {

				g.handOn()
				return err
			}
			return nil
		default:
		}
		var left error
		switch {
		case ctx.Err() != nil:
			left = ctx.Err()
		case !time.Now().Before(g.ends.Add(gateMargin)):
			left = errBehind
		default:
			continue
		}
		for i, q := range *queue {
			if q == w {
				*queue = append((*queue)[:i], (*queue)[i+1:]...)
				break
			}
		}
		return left
	}
}

func (g *callGate) sent(deadline time.Duration) {
	g.mu.Lock()
	g.ends = time.Now().Add(deadline)
	g.mu.Unlock()
}

func (g *callGate) release() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.handOn()
}

func (g *callGate) handOn() {
	var w *waiting
	switch {
	case len(g.cues) > 0:
		w, g.cues = g.cues[0], g.cues[1:]
	case len(g.memories) > 0:
		w, g.memories = g.memories[0], g.memories[1:]
	default:
		g.held, g.ends = false, time.Time{}
		return
	}
	g.ends = time.Now().Add(w.deadline)
	close(w.handed)
}

type waitedBehind struct {
	plugin, kind string
	err          error
}

func (e *waitedBehind) Error() string {
	what := "waited behind the source's other call, which ran past its own deadline"
	if !errors.Is(e.err, errBehind) {
		what = "was given up while it waited behind the source's other call"
	}
	return fmt.Sprintf("plugin %s gave no vector: the %s %s, and was handed nothing", e.plugin, e.kind, what)
}

func (e *waitedBehind) Unwrap() error { return e.err }

func (s pluginSource) whose(ctx context.Context, kind, text string, failed error) ([]float32, error) {
	fault := ofTheText(failed)
	var own textFault
	if !errors.As(fault, &own) || own.ends {
		return nil, fault
	}
	probe, ok := s.probe(ctx, kind)
	if !ok {
		return nil, fault
	}
	if probe.Outcome != pluginfacility.CheckPassed {
		logsink.Warn("memory.refusal", "the source of vectors %s failed a %s and then its own %s (%s): the failure is the source's, and nothing is set aside", s.ap.ID, kind, probe.Name, probe.Outcome)
		return nil, fmt.Errorf("%w; its own %s, asked after it, %s, so the failure is the source's and nothing is set aside", failed, probe.Name, probe.Words)
	}
	v, err := s.ask(ctx, kind, text)
	if err != nil {
		return nil, ofTheText(err)
	}
	return v, nil
}

func (s pluginSource) probe(ctx context.Context, kind string) (pluginhost.CheckResult, bool) {
	for _, c := range s.ap.Embeddings.ProbeChecks() {
		if c.Kind == kind {
			lanes := pluginhost.CheckLanes{Embed: s.ask}
			return pluginhost.RunChecks(ctx, []packagefmt.Check{c}, lanes, nil)[0], true
		}
	}
	return pluginhost.CheckResult{}, false
}

type textFault struct {
	error
	ends bool
}

func (f textFault) Unwrap() error     { return f.error }
func (textFault) OfTheText() bool     { return true }
func (f textFault) EndsThePass() bool { return f.ends }

func ofTheText(err error) error {
	var reply *pluginhost.EmbedReplyError
	var call *pluginhost.EmbedCallError
	switch {
	case errors.As(err, &reply):
		return textFault{error: err}
	case errors.As(err, &call) && call.Kind == pluginhost.EmbedPluginError:
		return textFault{error: err}
	case errors.As(err, &call) && call.Kind == pluginhost.EmbedTimedOut:
		return textFault{error: err, ends: true}
	}
	return err
}

func (s pluginSource) ask(ctx context.Context, kind, text string) ([]float32, error) {
	deadline := s.deadline(kind)
	s.a.gateOf(s.ap).sent(deadline)
	ctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	v, err := s.ap.Embeddings.Ask(ctx, s.a.toolReg, s.ap, kind, text)
	var failed *pluginhost.EmbedCallError
	if errors.As(err, &failed) && failed.Said != "" {
		logsink.Warn("memory.refusal", "the source of vectors %s gave no vector for a %s (%s); it said: %s", s.ap.ID, kind, failed.Kind, logsink.Preview(failed.Said))
	}
	return v, err
}

const replyLapse = 10 * time.Minute

type replySeen struct {
	id    string
	since time.Time
}

func (a *App) voiceHoldsBackfill() bool {
	lapse := a.memory.replyLapse
	if lapse <= 0 {
		lapse = orDefault(a.memory.limits.replyHold, replyLapse)
	}
	now := time.Now()
	held := false
	live := map[string]bool{}
	a.voiceSessions.Range(func(_, val any) bool {
		h := val.(*voiceHandle)
		select {
		case <-h.done:
			return true
		default:
		}
		live[h.id] = true
		if h.b != nil && h.b.HasInput() {
			held = true
			return true
		}
		reply := h.loadInflight().id
		if reply == "" {
			return true
		}

		seen, loaded := a.memory.replies.LoadOrStore(h.id, replySeen{reply, now})
		switch first := seen.(replySeen); {
		case !loaded:
			held = true
		case first.id != reply:
			a.memory.replies.Store(h.id, replySeen{reply, now})
			held = true
		case now.Sub(first.since) < lapse:
			held = true
		}
		return true
	})
	a.memory.replies.Range(func(k, _ any) bool {
		if !live[k.(string)] {
			a.memory.replies.Delete(k)
		}
		return true
	})
	return held
}
