package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/memory"
)

const (
	memoryBackfillAlarmID = "memory.backfill"
	memoryOwnerName       = "memory"

	memoryBackfillFirstDelay = 30 * time.Second
	memoryBackfillEvery      = 10 * time.Minute

	memoryLocalPass = 2 * time.Minute
)

type meaningLimits struct {
	every, localPass, cue, memory, checkRetry, replyHold time.Duration
	rows                                                 int
}

func limitsOf(m MemoryConfig) meaningLimits {
	sec := func(n int) time.Duration { return time.Duration(n) * time.Second }
	return meaningLimits{
		every: sec(m.BackfillSeconds), localPass: sec(m.BackfillLocalSeconds), rows: m.BackfillRows,
		cue: sec(m.EmbedCueSeconds), memory: sec(m.EmbedMemorySeconds),
		checkRetry: sec(m.SourceCheckRetrySeconds), replyHold: sec(m.ReplyHoldSeconds),
	}
}

func orDefault(d, otherwise time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return otherwise
}

func (l meaningLimits) cadence() time.Duration   { return orDefault(l.every, memoryBackfillEvery) }
func (l meaningLimits) localWork() time.Duration { return orDefault(l.localPass, memoryLocalPass) }
func (l meaningLimits) cueCall() time.Duration   { return orDefault(l.cue, embedCueDeadline) }
func (l meaningLimits) memoryCall() time.Duration {
	return orDefault(l.memory, embedMemoryDeadline)
}
func (l meaningLimits) providerRows() int {
	if l.rows > 0 {
		return l.rows
	}
	return memory.BackfillBudget
}

type memoryEmbedder struct{ a *App }

func (e memoryEmbedder) Basis() (string, error) {
	return providerBasis(e.a.currentProvider())
}

func providerBasis(entry providerEntry) (string, error) {
	if entry.EmbeddingsModel == "" {
		return "", fmt.Errorf("provider %q names no embeddings_model (Settings → Providers)", entry.Name)
	}
	return basisPart(entry.Name) + "/" + entry.EmbeddingsModel, nil
}

func basisPart(s string) string { return basisEscaper.Replace(s) }

var basisEscaper = strings.NewReplacer("%", "%25", "/", "%2F")

func (e memoryEmbedder) Floor() float64 { return memory.ProviderFloor }

func (e memoryEmbedder) Embed(ctx context.Context, _ memory.Kind, inputs []string) (string, [][]float32, error) {

	entry, client := e.a.activeRuntime()
	basis, err := providerBasis(entry)
	if err != nil {
		return "", nil, err
	}
	if client == nil {
		return basis, nil, fmt.Errorf("no model client is active")
	}

	if reason := e.a.handOver(meaningChoiceProvider); reason != nil {
		return basis, nil, reason
	}
	vectors, err := client.Embed(ctx, entry.EmbeddingsModel, inputs)
	return basis, vectors, ofTheRequest(err)
}

func ofTheRequest(err error) error {
	var refusal *llm.HTTPError

	if errors.As(err, &refusal) && !refusal.AuthFailure() {
		switch refusal.StatusCode {
		case http.StatusBadRequest, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity:
			return textFault{error: err}
		}
	}
	return err
}

type memoryOwner struct{ a *App }

func (o memoryOwner) Name() string { return memoryOwnerName }

func (o memoryOwner) OnAlarm(ctx context.Context, _ string, _ string, _ int64, _ string) cognitive.AlarmResult {
	if _, safe := o.a.SafeMode(); safe || o.a.engine == nil {
		return cognitive.AlarmResult{Accepted: true}
	}

	if o.a.backfillHeld() {
		return cognitive.AlarmResult{Accepted: true}
	}
	rep, err := o.a.backfillPass(ctx, o.a.engine.Instruments())
	o.a.rememberBackfill(rep, err)
	switch {
	case err != nil:
		logsink.Warn("memory.refusal", "meaning backfill stopped — %v (what landed stays; the next pass continues)", err)
	case rep.Unavailable != "":
	case rep.Embedded > 0 || rep.Dropped > 0 || rep.Pruned > 0:
		logsink.Info("memory.end", "%s", logsink.Preview(rep.Line()))
	}
	return cognitive.AlarmResult{Accepted: true}
}

func (a *App) backfillPass(ctx context.Context, f *memory.Facility) (memory.BackfillReport, error) {
	return f.Backfill(ctx, a.memory.limits.providerRows())
}

func (a *App) backfillHeld() bool {
	y, yields := a.pickMeaningSource().source.(memory.Yielder)
	return yields && !y.MayContinue()
}

func (a *App) armMemoryBackfill() error {
	every := a.memory.limits.cadence().Milliseconds()
	first := time.Now().UTC().Add(memoryBackfillFirstDelay).UnixMilli()
	return a.timeFac.SetAlarm(memoryBackfillAlarmID, memoryOwnerName, "wall", first, &every, "")
}

type memoryState struct {
	lastBackfill atomic.Pointer[backfillPass]

	checks sync.Map

	gates sync.Map

	checkRetry time.Duration

	replies    sync.Map
	replyLapse time.Duration

	limits meaningLimits
}

type backfillPass struct {
	at       time.Time
	embedded int
	stopped  string
}

func (a *App) rememberBackfill(rep memory.BackfillReport, err error) {
	if err == nil && rep.Unavailable != "" {
		return
	}
	pass := &backfillPass{at: time.Now().UTC(), embedded: rep.Embedded}
	switch {
	case err != nil:
		pass.stopped = err.Error()
	case rep.Yielded:

		pass.stopped = "voice was at work; the rest waits until no session is listening and no reply is being spoken"
	}
	a.memory.lastBackfill.Store(pass)
}

func (a *App) meaningState(ctx context.Context, f *memory.Facility, pick meaningPick) (*dashboard.MeaningState, error) {
	state := &dashboard.MeaningState{
		Choice: a.configSnapshot().Memory.MeaningSource, Plugin: pick.plugin, Question: pick.question,
		ProviderModel: a.currentProvider().EmbeddingsModel != "",
	}
	for _, ap := range a.embeddingsPlugins() {
		state.Plugins = append(state.Plugins, ap.ID)
	}
	if pick.off != nil {
		state.Unavailable, state.Why = pick.off.Detail, pick.off.Kind
		return state, nil
	}
	basis, cov, unavailable, err := f.VectorCoverage(ctx)
	if err != nil {
		return nil, err
	}
	if unavailable != "" {

		state.Unavailable, state.Why = unavailable, meaningOffNoSource
		return state, nil
	}
	state.Source, state.Basis, state.Floor = pick.kind, basis, pick.source.Floor()
	if y, yields := pick.source.(memory.Yielder); yields {
		state.Waiting = !y.MayContinue()
	}
	state.SetAside, state.SetAsideWhy = f.SetAside()
	for _, c := range cov {
		state.Coverage = append(state.Coverage, dashboard.MeaningCoverage{Store: c.Store, Rows: c.Rows, Embedded: c.Embedded, Beyond: c.Beyond, BeyondEmbedded: c.BeyondEmbedded})
	}
	if pass := a.memory.lastBackfill.Load(); pass != nil {
		state.LastPass = &dashboard.MeaningPass{At: pass.at.Format(time.RFC3339), Embedded: pass.embedded, Stopped: pass.stopped}
	}
	return state, nil
}
