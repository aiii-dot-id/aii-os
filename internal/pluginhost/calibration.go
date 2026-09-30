package pluginhost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
	"sync"
	"sync/atomic"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

const CalibrationV1 = "calibration_v1"
const MaxCalibrationCounter uint64 = 1<<53 - 1

type ActingSession struct{ ID, Reason string }
type actingSessionKey struct{}

func WithActingSession(ctx context.Context, s ActingSession) context.Context {
	return context.WithValue(ctx, actingSessionKey{}, s)
}
func ActingSessionFrom(ctx context.Context) ActingSession {
	s, ok := ctx.Value(actingSessionKey{}).(ActingSession)
	if !ok {
		s.Reason = "no_session"
	}
	return s
}

type CalibrationSource interface {
	Snapshot(generation uint64, session ActingSession) map[string]interface{}
	Finish(generation, sequence uint64, accepted bool)
}
type calibrationSource struct{ source CalibrationSource }
type calibrationSlot struct {
	source atomic.Pointer[calibrationSource]
}

func (ap *ActivePlugin) SetCalibrationSource(s CalibrationSource) {
	if s == nil {
		ap.calibration.source.Store(nil)
	} else {
		ap.calibration.source.Store(&calibrationSource{s})
	}
}
func (ap *ActivePlugin) CalibrationOperation() string {
	for _, d := range ap.Subscriptions {
		if d.Delivery == CalibrationV1 {
			return d.Operation
		}
	}
	return ""
}
func (ap *ActivePlugin) RuntimeGeneration() uint64 {
	if ap.sup != nil {
		return ap.sup.Generation()
	}
	return 1
}

type CalibrationDelivery struct {
	Owner                *ActivePlugin
	Generation, Sequence uint64
	Stream               string
	Source               CalibrationSource
	once                 sync.Once
}
type calibrationDeliveryKey struct{}

func WithCalibrationDelivery(ctx context.Context, d *CalibrationDelivery) context.Context {
	return tools.WithDispatchOwner(context.WithValue(ctx, calibrationDeliveryKey{}, d), d.Owner)
}
func (d *CalibrationDelivery) Finish(accepted bool) {
	d.once.Do(func() { d.Source.Finish(d.Generation, d.Sequence, accepted) })
}
func calibrationDeliveryFrom(ctx context.Context) *CalibrationDelivery {
	d, _ := ctx.Value(calibrationDeliveryKey{}).(*CalibrationDelivery)
	return d
}
func (d *CalibrationDelivery) Accept(res tools.Result, err error) bool {
	if err != nil || res.Error != "" {
		return false
	}
	var ack struct {
		Accepted bool   `json:"accepted"`
		Stream   string `json:"stream_id"`
		Sequence uint64 `json:"sequence"`
	}
	raw, valid := canonicaljson.CanonicalizeV1([]byte(res.Output))
	if valid != nil {
		return false
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&ack) != nil {
		return false
	}
	return ack.Accepted && ack.Stream == d.Stream && ack.Sequence == d.Sequence
}

func (ap *ActivePlugin) validateCalibration(descs *descriptorSet) error {
	op := ap.CalibrationOperation()
	if op == "" {
		return nil
	}
	fail := func(detail string) error { return &SubscriptionsError{PluginID: ap.ID, Detail: detail} }
	if ap.Voice.Load() != nil {
		return fail("calibration does not support resident sessions")
	}
	for _, w := range ap.Webhooks {
		if w.Operation == op {
			return fail("calibration target cannot be a webhook")
		}
	}
	d := descs.ops[op]
	if d == nil || d.input == nil || d.output == nil || d.raw["additionalProperties"] != false || d.outputRaw["additionalProperties"] != false || d.effects != broker.EffectsWriteLocal || d.operatorConfirms || !d.capsDeclared || len(d.capabilities) != 1 || d.capabilities[0] != "ring4.kv" {
		return fail("calibration requires closed input/output schemas, write.local, only ring4.kv, and no confirmation")
	}
	return nil
}

func (t *operationTool) DispatchOwner() any { return t.owner }
func (t *operationTool) calibrationFrame(ctx context.Context, injected map[string]interface{}, delivery *CalibrationDelivery) (uint64, error) {
	if t.owner == nil || t.owner.CalibrationOperation() == "" {
		return 0, nil
	}
	gen := t.owner.RuntimeGeneration()
	if delivery != nil {
		if delivery.Owner != t.owner || delivery.Generation != gen || gen == 0 || delivery.Sequence == 0 || delivery.Sequence > MaxCalibrationCounter || t.operation != t.owner.CalibrationOperation() {
			return 0, fmt.Errorf("calibration generation/owner/target mismatch")
		}
	} else {
		if t.operation == t.owner.CalibrationOperation() {
			return 0, fmt.Errorf("calibration requires its host ceremony")
		}
		source := t.owner.calibration.source.Load()
		session := ActingSessionFrom(ctx)
		snap := map[string]interface{}{"version": 1, "available": false, "reason": "not_ready"}
		if session.ID != "" {
			snap["session_id"] = session.ID
		}
		if source != nil {
			snap = source.source.Snapshot(gen, session)
		}
		injected["_host_calibration"] = snap
	}
	return gen, nil
}
