package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
	"github.com/aiii-dot-id/aii-os/internal/supervisor"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

const (
	EmbedTimedOut = "timed_out"

	EmbedNotAsked = "not_asked"

	EmbedSafe = "safe"

	EmbedReplaced = "replaced"

	EmbedDisabled = "disabled"

	EmbedRefused = "refused"

	EmbedPluginError = "plugin_error"

	EmbedChildFailed = "child_failed"
)

type EmbedCallError struct {
	PluginID string
	Kind     string
	Code     string
	Said     string
	Err      error
}

func (e *EmbedCallError) Error() string {
	what := "the call to it failed"
	switch e.Kind {
	case EmbedTimedOut:
		what = "it did not answer in the time a call is given, was stopped, and starts again"
	case EmbedNotAsked:
		what = "it was busy for as long as a call may wait, and was handed nothing"
	case EmbedSafe:
		what = "no plugin is asked while the identity is in SAFE"
	case EmbedReplaced:
		what = "it was replaced or stopped while it was being asked"
	case EmbedDisabled:
		what = "its operation is turned off by the operator"
	case EmbedRefused:
		what = "the host refused the call (" + e.Code + ")"
	case EmbedPluginError:
		what = "it answered with an error in place of a vector"
	case EmbedChildFailed:
		what = "it stopped or is not running"
	}
	return fmt.Sprintf("plugin %s gave no vector: %s", e.PluginID, what)
}

func (e *EmbedCallError) Unwrap() error { return e.Err }

func (e *Embeddings) Ask(ctx context.Context, reg *tools.Registry, owner *ActivePlugin, kind, text string) ([]float32, error) {
	res, err := reg.Execute(tools.WithDispatchOwner(ctx, owner), e.Embed, map[string]any{"kind": kind, "text": text})
	return e.answer(ctx, res, err)
}

func (e *Embeddings) answer(ctx context.Context, res tools.Result, err error) ([]float32, error) {
	fail := func(kind, code, said string, err error) ([]float32, error) {
		return nil, &EmbedCallError{PluginID: e.PluginID, Kind: kind, Code: code, Said: said, Err: err}
	}
	if err != nil {
		var timeout *supervisor.InvokeTimeoutError
		switch {
		case errors.As(err, &timeout):
			return fail(EmbedTimedOut, "", err.Error(), err)
		case errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled):
			return fail(EmbedNotAsked, "", err.Error(), err)
		}
		return fail(EmbedChildFailed, "", err.Error(), err)
	}
	if res.Error != "" || res.ReasonCode != "" {

		if !res.Refused {

			if res.ReasonCode == ReasonCancelledBeforeDispatch && ctx.Err() != nil {
				return fail(EmbedNotAsked, res.ReasonCode, "", nil)
			}
			return fail(EmbedPluginError, "", res.Error, nil)
		}
		switch res.ReasonCode {
		case tools.ReasonSafeSuspended:
			return fail(EmbedSafe, res.ReasonCode, "", nil)
		case "HOST_OWNER_CHANGED", tools.ReasonDispatchTargetChanged, ReasonActivationWithdrawn, ReasonNotAdmitted:
			return fail(EmbedReplaced, res.ReasonCode, "", nil)
		case tools.ReasonOperatorDisabled:
			return fail(EmbedDisabled, res.ReasonCode, "", nil)
		}
		return fail(EmbedRefused, res.ReasonCode, res.Error, nil)
	}
	return e.ReadReply(res.Output)
}

type EmbedReplyError struct {
	PluginID string
	Detail   string
}

func (e *EmbedReplyError) Error() string {
	return fmt.Sprintf("plugin %s answered with something that is not a vector: %s", e.PluginID, e.Detail)
}

type embedReply struct {
	Vector ProbeVector `json:"vector"`
}

func (e *Embeddings) ReadReply(output string) ([]float32, error) {
	refuse := func(format string, args ...any) ([]float32, error) {
		return nil, &EmbedReplyError{PluginID: e.PluginID, Detail: fmt.Sprintf(format, args...)}
	}
	var reply embedReply

	if err := canonicaljson.DecodeStrict([]byte(output), &reply); err != nil {
		return refuse(`the reply is not an object whose one member is "vector", a list of numbers`)
	}
	if len(reply.Vector) != e.Decl.Dimension {
		return refuse("the reply holds %d numbers, the signed dimension is %d", len(reply.Vector), e.Decl.Dimension)
	}
	out := make([]float32, len(reply.Vector))
	var norm float64
	for i, v := range reply.Vector {
		f := float32(v)

		if math.IsNaN(float64(f)) || math.IsInf(float64(f), 0) {
			return refuse("the reply holds a number that is not finite")
		}
		out[i] = f
		norm += float64(f) * float64(f)
	}
	if norm == 0 {
		return refuse("the reply is all zeros, which no cosine can be taken against")
	}
	return out, nil
}

type ProbeError struct {
	PluginID string
	Probe    int
	Kind     string
	Cosine   float64
	Bound    float64

	Err     error
	Outcome pluginfacility.CheckOutcome
}

func (e *ProbeError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("plugin %s: probe %d (a %s) was not answered: %v", e.PluginID, e.Probe, e.Kind, e.Err)
	}
	return fmt.Sprintf("plugin %s: probe %d (a %s) came back at %.3f of the vector its publisher signed for it, under the %.2f the package asks of itself",
		e.PluginID, e.Probe, e.Kind, e.Cosine, e.Bound)
}

func (e *ProbeError) Unwrap() error { return e.Err }

func (e *Embeddings) Probe(ctx context.Context, ask func(ctx context.Context, kind, text string) ([]float32, error)) error {
	lanes := CheckLanes{Embed: ask}
	for i, c := range e.ProbeChecks() {
		r := RunChecks(ctx, []packagefmt.Check{c}, lanes, nil)[0]
		if r.Outcome == pluginfacility.CheckPassed {
			continue
		}
		miss := &ProbeError{PluginID: e.PluginID, Probe: i + 1, Kind: c.Kind, Bound: e.Decl.ProbeBound, Err: r.Err, Outcome: r.Outcome}
		if r.Cosine != nil {
			miss.Cosine = *r.Cosine
		}
		return miss
	}
	return nil
}

func (e *Embeddings) ProbeChecks() []packagefmt.Check {
	out := make([]packagefmt.Check, len(e.Decl.Probes))
	for i, p := range e.Decl.Probes {
		out[i] = packagefmt.Check{Name: fmt.Sprintf("probe %d", i+1), Sets: []string{packagefmt.AllSets}, Lane: packagefmt.LaneEmbeddings,
			Kind: p.Kind, Text: p.Text, Expect: packagefmt.Expect{Cosine: &packagefmt.CosineExpect{Reference: p.Vector, Min: e.Decl.ProbeBound}}}
	}
	return out
}
