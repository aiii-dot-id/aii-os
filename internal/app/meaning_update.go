package app

import (
	"context"
	"errors"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/memory"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

type workRate struct {
	basis     string
	perMemory time.Duration
}

func (a *App) noteWorkRate(rep memory.BackfillReport) {
	if rep.Embedded <= 0 || rep.Worked <= 0 || rep.Basis == "" {
		return
	}
	a.memory.workRate.Store(&workRate{basis: rep.Basis, perMemory: rep.Worked / time.Duration(rep.Embedded)})
}

type remake struct {
	vectors int64
	about   time.Duration
}

func newestBasis(views []pluginfacility.InstanceView) map[string]string {
	out := map[string]string{}
	for _, v := range views {
		for i := len(v.Activations) - 1; i >= 0; i-- {
			if s := v.Activations[i].Selection; s != nil {
				out[v.ID] = s.Basis
				break
			}
		}
	}
	return out
}

func (a *App) updateRemakes(f *memory.Facility, pending map[string]pendingMark, newest map[string]string) map[string]remake {
	if f == nil || len(pending) == 0 {
		return nil
	}

	var updating []*pluginhost.ActivePlugin
	for _, ap := range a.embeddingsPlugins() {
		switch pending[ap.ID].phase {
		case pluginhost.PhaseFetching, pluginhost.PhaseWaiting, pluginhost.PhaseReady, lifeStarting:
			if next := newest[ap.ID]; next != "" && next != ap.Embeddings.Basis() {
				updating = append(updating, ap)
			}
		}
	}
	if len(updating) == 0 {
		return nil
	}
	pick := a.pickMeaningSource()
	if pick.kind != "plugin" {
		return nil
	}
	var serving *pluginhost.ActivePlugin
	for _, ap := range updating {
		if ap.ID == pick.plugin {
			serving = ap
		}
	}
	if serving == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(a.lifetime(), meaningStateTimeout)
	defer cancel()
	work := a.memory.limits.localWork()
	after, unavailable, err := f.ChangeTo(ctx, basisOnly{basis: newest[serving.ID], work: work})
	if err != nil || unavailable != "" {
		return nil
	}
	now, unavailable, err := f.ChangeTo(ctx, basisOnly{basis: serving.Embeddings.Basis(), work: work})
	if err != nil || unavailable != "" {
		return nil
	}

	r := remake{vectors: after.ToEmbed - now.ToEmbed}
	if r.vectors <= 0 {
		return nil
	}
	if rate := a.memory.workRate.Load(); rate != nil && rate.basis == serving.Embeddings.Basis() {
		r.about = remakeTime(r.vectors, rate.perMemory, work, a.memory.limits.cadence())
	}
	return map[string]remake{serving.ID: r}
}

func remakeTime(n int64, perMemory, work, cadence time.Duration) time.Duration {
	total := time.Duration(n) * perMemory
	if work <= 0 || total <= work {
		return total
	}
	return time.Duration(float64(total) / float64(work) * float64(cadence))
}

type basisOnly struct {
	basis string
	work  time.Duration
}

func (b basisOnly) Basis() (string, error)  { return b.basis, nil }
func (b basisOnly) Floor() float64          { return 0 }
func (b basisOnly) PassWork() time.Duration { return b.work }
func (b basisOnly) Embed(context.Context, memory.Kind, []string) (string, [][]float32, error) {
	return "", nil, errBasisOnly
}

var errBasisOnly = errors.New("a source that is not running is asked for nothing")
