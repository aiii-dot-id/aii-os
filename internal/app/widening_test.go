package app

import (
	"context"
	"fmt"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// THE OFFER TO WIDEN APPEARS ONLY WHEN THE IDENTITY'S OWN ENDINGS ALLOW IT
// (the resident design §4): at least agency.autonomy_widening_completions
// commitments to the operator completed since their last press, none
// abandoned unrepaired; it names the next stage and carries the press that
// widens it; the work state carries the stage and the main session's
// condition for the page.
func TestTheOfferToWidenAppearsOnlyWhenTheEndingsAllowIt(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	pressStage(t, app, store.StageSupervised) // the default, pressed: the relationship the promises are to
	ctx := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn_c"), turnSourceOperator)
	promise := func(id, state string) {
		t.Helper()
		if _, err := app.engine.ExecuteAction(ctx, "verb", "commit", map[string]any{"variant": "commitment.promised", "id": id, "counterpart_id": "rel_quinn_01", "description": "the " + id}); err != nil {
			t.Fatal(err)
		}
		if _, err := app.engine.ExecuteAction(ctx, "verb", "commit", map[string]any{"variant": "commitment.state_change", "id": id, "state": state, "result": "done: " + id}); err != nil {
			t.Fatal(err)
		}
	}
	promise("cm_1", "completed")
	promise("cm_2", "completed")
	if offer := app.wideningOffer(); offer != nil {
		t.Fatalf("an offer at two completions: %+v", offer)
	}
	promise("cm_3", "completed")
	// THREE CLAIMS ARE NOT THREE COMPLETIONS (a review, 2026-11-06, finding 8):
	// a completion counts when a session that carried the promise
	// delivered with evidence of a verified class, read back.
	if offer := app.wideningOffer(); offer != nil {
		t.Fatalf("an offer on the identity's claims alone: %+v", offer)
	}
	audit := func(n int, promise, evidence, readback string) {
		t.Helper()
		id := fmt.Sprintf("ws_%d", n)
		if err := app.store.StartWorkSession(ctx, id, "keep "+promise); err != nil {
			t.Fatal(err)
		}
		p := promise
		if err := app.store.SetWorkPromise(ctx, id, &p, nil, ""); err != nil {
			t.Fatal(err)
		}
		if err := app.store.DeliverWorkSession(ctx, id, "served: "+promise, evidence, readback); err != nil {
			t.Fatal(err)
		}
	}
	audit(1, "cm_1", store.EvidenceWorkerReportOnly, "")
	audit(2, "cm_2", store.EvidenceLocallyVerified, "read back: filed")
	audit(3, "cm_3", store.EvidenceHostReceipted, "receipt in hand")
	if offer := app.wideningOffer(); offer != nil {
		t.Fatalf("an offer with a worker's report among the completions: %+v", offer)
	}
	audit(4, "cm_1", store.EvidenceLocallyVerified, "read back: sent")
	offer := app.wideningOffer()
	if offer == nil {
		t.Fatal("no offer at three audited completions")
	}
	if offer.From != store.StageSupervised || offer.To != store.StageTrusted || len(offer.Completions) != 3 || offer.Completions[2].Result != "done: cm_3" {
		t.Fatalf("the offer: %+v", offer)
	}
	for _, c := range offer.Completions {
		if !c.Audited {
			t.Fatalf("a completion in the offer is not marked audited: %+v", c)
		}
	}
	if offer.Digest != store.AutonomyApprovalDigest("rel_quinn_01", store.StageTrusted) || !store.IsApprovalPress(offer.Press, offer.Digest) {
		t.Fatalf("the offer's press does not bind the next stage: %+v", offer)
	}
	// An abandoned, unrepaired commitment withdraws it.
	promise("cm_4", "abandoned")
	if offer := app.wideningOffer(); offer != nil {
		t.Fatalf("an offer beside an abandoned commitment: %+v", offer)
	}
	// The work state for the page: the stage, and the condition when one is set.
	w, err := app.workQueueState()
	if err != nil || w.Stage != store.StageSupervised || w.Resumes != nil {
		t.Fatalf("the work state: %+v %v", w, err)
	}
	if _, err := app.engine.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "start", "description": "keep cm_3"}); err != nil {
		t.Fatal(err)
	}
	// cm_3 is completed above; a promise names an OPEN one, so the session
	// carries a fresh commitment.
	recordPromise(t, app, "cm_open_widen")
	if _, err := app.engine.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "update", "promise": "cm_open_widen", "resumes_on": "word"}); err != nil {
		t.Fatal(err)
	}
	w, err = app.workQueueState()
	if err != nil || w.Resumes == nil || w.Resumes.On != "word" || w.Resumes.Kind != "commitment" || w.Resumes.Counterpart != "Quinn" || w.NothingScheduled {
		t.Fatalf("the work state with a condition: %+v %v", w, err)
	}
}
