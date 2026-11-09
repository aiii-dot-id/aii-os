package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// A SESSION CARRIES ITS PROMISE AND WHAT RESUMES IT, AND THE TURN THAT MADE
// IT, as Ring 4: on the session row, never minted, written by the one writer.
// nil leaves a field, empty clears it; the turn is the turn of the call, not
// an argument. One notice records the change, as every change to a session is.
func TestASessionCarriesItsPromiseAndWhatResumesIt(t *testing.T) {
	s := testStore(t)
	ctx := interaction.WithTurn(context.Background(), "turn-promising")
	if err := s.StartWorkSession(ctx, "ws_p", "keep a promise"); err != nil {
		t.Fatal(err)
	}
	subagentForTest(t, s, ctx, "ws_child", "find the figures")
	promise, on := "cm_1", "delivery:ws_child"
	if err := s.SetWorkPromise(ctx, "ws_p", &promise, &on, ""); err != nil {
		t.Fatal(err)
	}
	ws, err := s.WorkSessionByID("ws_p")
	if err != nil || ws == nil {
		t.Fatalf("session: %+v %v", ws, err)
	}
	if ws.Promise != "cm_1" || ws.ResumesOn != "delivery:ws_child" || ws.PromisedTurn != "turn-promising" {
		t.Fatalf("the session does not carry its promise: promise %q resumes_on %q turn %q", ws.Promise, ws.ResumesOn, ws.PromisedTurn)
	}
	// A later change of the condition alone leaves the promise and its turn.
	later := interaction.WithTurn(context.Background(), "turn-later")
	word := "word"
	if err := s.SetWorkPromise(later, "ws_p", nil, &word, ""); err != nil {
		t.Fatal(err)
	}
	ws, _ = s.WorkSessionByID("ws_p")
	if ws.Promise != "cm_1" || ws.ResumesOn != "word" || ws.PromisedTurn != "turn-promising" {
		t.Fatalf("a condition change moved more than the condition: %+v", ws)
	}
	// Empty clears.
	none := ""
	if err := s.SetWorkPromise(later, "ws_p", &none, &none, ""); err != nil {
		t.Fatal(err)
	}
	ws, _ = s.WorkSessionByID("ws_p")
	if ws.Promise != "" || ws.ResumesOn != "" {
		t.Fatalf("empty did not clear: %+v", ws)
	}
	// Nothing to set is nothing written; an unknown session is refused.
	if err := s.SetWorkPromise(later, "ws_p", nil, nil, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SetWorkPromise(later, "ws_none", &promise, nil, ""); err == nil {
		t.Fatal("an unknown session took a promise")
	}
	// The change is a notice of the record, with the promise in its details.
	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM conversations WHERE kind='notice' AND json_extract(details,'$.work.promise')='cm_1' AND json_extract(details,'$.work.resumes_on')='delivery:ws_child'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("the promise's notice is not in the record once: %d", n)
	}
	// The ledger holds nothing of it: Ring 4 is never minted.
	var minted int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM ledger WHERE content LIKE '%cm_1%' OR content LIKE '%resumes_on%'`).Scan(&minted); err != nil {
		t.Fatal(err)
	}
	if minted != 0 {
		t.Fatalf("a promise's condition reached the ledger: %d rows", minted)
	}
}

// pressOn records the operator's press on the page for the level, as the
// page records it (said as an answer), and answers its turn.
func pressOn(t *testing.T, s *Store, counterpart, level string) uint64 {
	t.Helper()
	ref, err := s.RecordOperatorPress(context.Background(), "Quinn's identity", level, AutonomyApprovalDigest(counterpart, level))
	if err != nil {
		t.Fatal(err)
	}
	return ref.Sequence
}

// THE OPERATOR'S PRESS ON A LEVEL IS IN THE RECORD AS A DIGEST, Ring 1, beside
// their affirmation of the charter; without it the level describes only.
func TestTheOperatorsPressOnALevelIsRecordedByDigest(t *testing.T) {
	s := testStore(t)
	operatorRelationship(t, s, 1, "rel_q", "Quinn", "founding_operator", "")
	rel, err := s.CurrentOperatorRelationship()
	if err != nil || rel == nil {
		t.Fatalf("relationship: %+v %v", rel, err)
	}
	if rel.ApprovalDigest != "" {
		t.Fatalf("a level nobody pressed has a digest: %q", rel.ApprovalDigest)
	}
	if err := s.AddConversationTurn("operator", "Let them run as trusted."); err != nil {
		t.Fatal(err)
	}
	turn, err := s.GetLatestOperatorTurn()
	if err != nil || turn == nil {
		t.Fatal(err)
	}
	press := pressOn(t, s, "rel_q", "trusted")
	seqEvent(t, s, 3, ledger.EventRelationshipUpsert, map[string]any{
		"id": "rel_q", "counterpart_name": "Quinn", "counterpart_role": "operator", "relationship_type": "founding_operator",
		"charter_text": "CHARTER-TEXT Quinn and I build carefully.", "autonomy_level": "trusted",
		"operator_approval_excerpt": turn.Content, "operator_approval_turn": turn.TurnSeq, "approval_basis": "conversation_turn",
		"approval_digest": AutonomyApprovalDigest("rel_q", "trusted"), "approval_press_turn": press,
	})
	rel, err = s.CurrentOperatorRelationship()
	if err != nil || rel == nil {
		t.Fatalf("relationship: %+v %v", rel, err)
	}
	if rel.AutonomyLevel != "trusted" || rel.ApprovalDigest != AutonomyApprovalDigest("rel_q", "trusted") {
		t.Fatalf("the press is not in the record: level %q digest %q", rel.AutonomyLevel, rel.ApprovalDigest)
	}
}

// THE HOST READS A PROMISE'S RECORD, NEVER ITS MEMORY: when the condition
// last changed, whether a fresh turn has opened on it since, whether the
// promise moved since, and what to call it on the card.
func TestTheHostReadsAPromisesRecord(t *testing.T) {
	s := testStore(t)
	operatorRelationship(t, s, 1, "rel_q", "Quinn", "founding_operator", "")
	seqEvent(t, s, 2, ledger.EventCommitmentPromised, map[string]any{"id": "cm_1", "counterpart_id": "rel_q", "description": "the report by morning"})
	kind, who, found, err := s.PromiseOf("cm_1")
	if err != nil || !found || kind != "commitment" || who != "Quinn" {
		t.Fatalf("the commitment is not named for the card: %q %q %v %v", kind, who, found, err)
	}
	if _, _, found, _ := s.PromiseOf("cm_none"); found {
		t.Fatal("an unknown promise was found")
	}
	ctx := interaction.WithTurn(context.Background(), "turn-capped")
	if err := s.StartWorkSession(ctx, "ws_r", "keep a promise"); err != nil {
		t.Fatal(err)
	}
	if on, at, _, _, err := s.PromiseChangedAt("ws_r"); err != nil || on != "" || !at.IsZero() {
		t.Fatalf("a session with no condition reads %q %v %v", on, at, err)
	}
	promise, now := "cm_1", "now"
	if err := s.SetWorkPromise(ctx, "ws_r", &promise, &now, "The turn ended at a checkpoint under cm_1; a fresh turn follows"); err != nil {
		t.Fatal(err)
	}
	on, at, since, turn, err := s.PromiseChangedAt("ws_r")
	if err != nil || on != "now" || turn != "turn-capped" || at.IsZero() {
		t.Fatalf("the change is not read: %q %v %q %v", on, at, turn, err)
	}
	// The promise has not moved since the condition was set; a signed state
	// change after it is progress.
	if moved, err := s.PromiseProgressSince("cm_1", at); err != nil || moved {
		t.Fatalf("progress before any state change: %v %v", moved, err)
	}
	time.Sleep(2 * time.Millisecond)
	moved := &ledger.Event{Seq: 3, Prev: "0", Timestamp: time.Now().UTC().Format(time.RFC3339Nano), Type: ledger.EventCommitmentStateChange, Ring: 3,
		Payload: mustPayload(t, map[string]any{"id": "cm_1", "state": "in_progress"}), Content: hashOfSeq(3), Sig: "probe"}
	if err := s.Materialize(moved); err != nil {
		t.Fatal(err)
	}
	if moved, err := s.PromiseProgressSince("cm_1", at); err != nil || !moved {
		t.Fatalf("a state change is not progress: %v %v", moved, err)
	}
	if moved, _ := s.PromiseProgressSince("cm_none", at); moved {
		t.Fatal("an unknown promise made progress")
	}
	// No fresh turn has opened on it; one that did is read by its mark.
	if ran, err := s.ContinuationRanSince("ws_r", since); err != nil || ran {
		t.Fatalf("a fresh turn is read before one ran: %v %v", ran, err)
	}
	leg := interaction.WithResumeSession(interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn-leg"), "continuation"), "ws_r")
	if _, err := s.RecordRelatedConversation(leg, string(interaction.System), "[budget checkpoint — continuation 1] Work session ws_r is still active.", "turn-leg", "", interaction.Details{}); err != nil {
		t.Fatal(err)
	}
	if ran, err := s.ContinuationRanSince("ws_r", since); err != nil || !ran {
		t.Fatalf("the fresh turn that ran is not read: %v %v", ran, err)
	}
	if ran, _ := s.ContinuationRanSince("ws_other", since); ran {
		t.Fatal("another session's fresh turn was read as this one's")
	}
}

// THE BOOT SWEEP KEEPS A SESSION UNDER A CONDITION: the condition, not the
// restart, says when its work resumes; a session with none is closed as
// interrupted, as before. The kept sessions are read for the boot line.
func TestTheSweepKeepsASessionUnderACondition(t *testing.T) {
	s := testStore(t)
	ctx := interaction.WithTurn(context.Background(), "turn-1")
	if err := s.StartWorkSession(ctx, "ws_kept", "keep a promise"); err != nil {
		t.Fatal(err)
	}
	word, promise := "word", "in_1"
	if err := s.SetWorkPromise(ctx, "ws_kept", &promise, &word, ""); err != nil {
		t.Fatal(err)
	}
	swept, err := s.SweepOrphanWorkSessions()
	if err != nil || len(swept) != 0 {
		t.Fatalf("the sweep closed a session under a condition: %+v %v", swept, err)
	}
	kept, err := s.KeptPromisedSessions()
	if err != nil || len(kept) != 1 || kept[0].ID != "ws_kept" || kept[0].ResumesOn != "word" {
		t.Fatalf("the kept session is not read: %+v %v", kept, err)
	}
	// A condition keeps the session open whether or not a promise is under
	// it (work continues, promised or not); a cleared
	// condition leaves it to the sweep as any orphan.
	none := ""
	if err := s.SetWorkPromise(ctx, "ws_kept", &none, nil, ""); err != nil {
		t.Fatal(err)
	}
	swept, err = s.SweepOrphanWorkSessions()
	if err != nil || len(swept) != 0 {
		t.Fatalf("a session with a condition and no promise was closed: %+v %v", swept, err)
	}
	if err := s.SetWorkPromise(ctx, "ws_kept", nil, &none, ""); err != nil {
		t.Fatal(err)
	}
	swept, err = s.SweepOrphanWorkSessions()
	if err != nil || len(swept) != 1 || swept[0].ID != "ws_kept" {
		t.Fatalf("a session with no condition was not closed: %+v %v", swept, err)
	}
}

// A FRESH TURN IS READ BY THE RECORD'S ORDER AND THE EXACT SESSION (a review,
// 2026-11-05): never by a time string, and ws_1 is not ws_10.
func TestAFreshTurnIsReadByItsOrderAndExactSession(t *testing.T) {
	s := testStore(t)
	ctx := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn-c"), "operator")
	for _, id := range []string{"ws_1", "ws_10"} {
		if err := s.StartWorkSession(ctx, id, "keep a promise"); err != nil {
			t.Fatal(err)
		}
	}
	promise, now := "in_1", "now"
	if err := s.SetWorkPromise(ctx, "ws_1", &promise, &now, ""); err != nil {
		t.Fatal(err)
	}
	_, _, since, _, err := s.PromiseChangedAt("ws_1")
	if err != nil || since == 0 {
		t.Fatalf("the change's place: %d %v", since, err)
	}
	leg := interaction.WithResumeSession(interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn-leg10"), "continuation"), "ws_10")
	if _, err := s.RecordRelatedConversation(leg, string(interaction.System), "[budget checkpoint — continuation 1] "+ContinuationFactNames("ws_10")+"is still active.", "turn-leg10", "", interaction.Details{}); err != nil {
		t.Fatal(err)
	}
	if ran, _ := s.ContinuationRanSince("ws_1", since); ran {
		t.Fatal("ws_10's fresh turn was read as ws_1's")
	}
	leg1 := interaction.WithResumeSession(interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn-leg1"), "continuation"), "ws_1")
	if _, err := s.RecordRelatedConversation(leg1, string(interaction.System), "[budget checkpoint — continuation 1] "+ContinuationFactNames("ws_1")+"is still active.", "turn-leg1", "", interaction.Details{}); err != nil {
		t.Fatal(err)
	}
	if ran, _ := s.ContinuationRanSince("ws_1", since); !ran {
		t.Fatal("ws_1's own fresh turn was not read")
	}
	if ran, _ := s.ContinuationRanSince("ws_1", since+1000); ran {
		t.Fatal("a fresh turn before the place named was read as after it")
	}
	cases := []struct {
		words, id string
		want      bool
	}{{"in_10 is done", "in_1", false}, {"in_1 is done", "in_1", true}, {"(in_1).", "in_1", true}, {"in_1_b", "in_1", false}, {"xin_1", "in_1", false}}
	for _, c := range cases {
		if got := NamesID(c.words, c.id); got != c.want {
			t.Fatalf("NamesID(%q, %q) = %v", c.words, c.id, got)
		}
	}
}

// A LEVEL GOVERNS ONLY BY THE OPERATOR'S PRESS: the identity's upsert of a
// level carries the digest that binds it to this counterpart only when it
// cites the operator's own turn whose content is the press for that
// digest; a digest for another level, or a citation of any other word of
// theirs, is refused; a level without a digest governs nothing.
func TestALevelGovernsOnlyByTheOperatorsPress(t *testing.T) {
	s := testStore(t)
	operatorRelationship(t, s, 1, "rel_q", "Quinn", "founding_operator", "")
	if stage, err := s.ApprovedStage(); err != nil || stage != StageAutonomous {
		t.Fatalf("a level nobody pressed governs, or the default is not to work: %q %v", stage, err)
	}
	digest := AutonomyApprovalDigest("rel_q", StageTrusted)
	if err := s.AddConversationTurn("operator", "I approve the charter."); err != nil {
		t.Fatal(err)
	}
	word, err := s.GetLatestOperatorTurn()
	if err != nil || word == nil {
		t.Fatal(err)
	}
	press := pressOn(t, s, "rel_q", StageTrusted)
	upsert := func(level, withDigest string, pressTurn uint64) map[string]any {
		return map[string]any{
			"id": "rel_q", "counterpart_name": "Quinn", "counterpart_role": "operator", "relationship_type": "founding_operator",
			"charter_text": "CHARTER-TEXT Quinn and I build carefully.", "autonomy_level": level,
			"operator_approval_excerpt": "their word", "operator_approval_turn": word.TurnSeq, "approval_basis": "conversation_turn",
			"approval_digest": withDigest, "approval_press_turn": pressTurn,
		}
	}
	// The wrong digest for the level: refused before it is written.
	bad := &ledger.Event{Seq: 3, Prev: "0", Timestamp: "2026-08-16T00:00:00Z", Type: ledger.EventRelationshipUpsert, Ring: 1,
		Payload: mustPayload(t, upsert(StageSupervised, digest, press)), Content: hashOfSeq(3), Sig: "probe"}
	if err := s.Materialize(bad); err == nil || !strings.Contains(err.Error(), "does not bind") {
		t.Fatalf("a digest for another level was admitted: %v", err)
	}
	// The press itself: the level governs.
	seqEvent(t, s, 3, ledger.EventRelationshipUpsert, upsert(StageTrusted, digest, press))
	if stage, err := s.ApprovedStage(); err != nil || stage != StageTrusted {
		t.Fatalf("the pressed level does not govern: %q %v", stage, err)
	}
	// Typed words of theirs that quote the marker are not a press: refused at admission.
	if err := s.AddConversationTurn("operator", "I saw the line: "+ApprovalPressContent("Quinn's identity", StageTrusted, digest)); err != nil {
		t.Fatal(err)
	}
	quoted, _ := s.GetLatestOperatorTurn()
	if p, _ := s.LatestOperatorPress(digest); p != nil && p.TurnSeq == quoted.TurnSeq {
		t.Fatal("typed words quoting the marker read as the press")
	}
	notPress := &ledger.Event{Seq: 4, Prev: "0", Timestamp: "2026-08-16T00:00:00Z", Type: ledger.EventRelationshipUpsert, Ring: 1,
		Payload: mustPayload(t, upsert(StageTrusted, digest, quoted.TurnSeq)), Content: hashOfSeq(4), Sig: "probe"}
	s.mu.Lock()
	err = s.validateLiveOnlyLocked(notPress) // admission's own check, which replay does not run
	s.mu.Unlock()
	if err == nil || !strings.Contains(err.Error(), "not the operator's press on the page") {
		t.Fatalf("a word that is not the press carried the digest: %v", err)
	}
	// A digest with no press cited: refused in both modes.
	if err := s.Materialize(&ledger.Event{Seq: 4, Prev: "0", Timestamp: "2026-08-16T00:00:00Z", Type: ledger.EventRelationshipUpsert, Ring: 1,
		Payload: mustPayload(t, upsert(StageTrusted, digest, 0)), Content: hashOfSeq(4), Sig: "probe"}); err == nil || !strings.Contains(err.Error(), "cites no press") {
		t.Fatalf("a digest citing no press was admitted: %v", err)
	}
	// The identity's own later word on the level, with no digest: descriptive
	// only, and the pressed level stands (a review, 2026-11-05: a descriptive
	// edit must not lift what the operator pressed).
	seqEvent(t, s, 5, ledger.EventRelationshipUpsert, upsert(StageSupervised, "", 0))
	rel, _ := s.CurrentOperatorRelationship()
	if stage, err := s.ApprovedStage(); err != nil || stage != StageTrusted || rel.AutonomyLevel != StageSupervised {
		t.Fatalf("a descriptive edit moved the pressed level: stage %q level %q %v", stage, rel.AutonomyLevel, err)
	}
}

// THE LAST TURN'S DECLARATION IS READ FROM ITS ROW: the steps it declared
// and the calls it used; a facility's call is not a turn; none before any.
func TestTheLastTurnsDeclarationIsRead(t *testing.T) {
	s := testStore(t)
	if _, _, found, err := s.LastTurnDeclaration(); err != nil || found {
		t.Fatalf("a declaration before any turn: %v %v", found, err)
	}
	if err := s.InsertTurnMetric(TurnMetric{TsMs: 1000, Calls: 7, Predicted: 3, Source: "operator"}); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertTurnMetric(TurnMetric{TsMs: 2000, Calls: 1, Source: FacilitySource("consolidate")}); err != nil {
		t.Fatal(err)
	}
	declared, used, found, err := s.LastTurnDeclaration()
	if err != nil || !found || declared != 3 || used != 7 {
		t.Fatalf("the last turn's declaration reads %d %d %v %v, want 3 declared and 7 used", declared, used, found, err)
	}
}

// THE IDENTITY'S OWN ENDINGS ARE COUNTED SINCE THE OPERATOR'S LAST PRESS:
// completions to the counterpart with their results, and what it abandoned
// without repair; another counterpart's and earlier endings count nothing.
func TestTheIdentitysEndingsAreCountedSinceTheLastPress(t *testing.T) {
	s := testStore(t)
	operatorRelationship(t, s, 1, "rel_q", "Quinn", "founding_operator", "")
	seqEvent(t, s, 2, ledger.EventCommitmentPromised, map[string]any{"id": "cm_a", "counterpart_id": "rel_q", "description": "the report"})
	seqEvent(t, s, 3, ledger.EventCommitmentStateChange, map[string]any{"id": "cm_a", "state": "completed", "result": "sent at dawn"})
	// The press, after cm_a: cm_a counts for nothing after it.
	digest := AutonomyApprovalDigest("rel_q", StageTrusted)
	if err := s.AddConversationTurn("operator", "I approve."); err != nil {
		t.Fatal(err)
	}
	word, _ := s.GetLatestOperatorTurn()
	press := pressOn(t, s, "rel_q", StageTrusted)
	seqEvent(t, s, 4, ledger.EventRelationshipUpsert, map[string]any{
		"id": "rel_q", "counterpart_name": "Quinn", "counterpart_role": "operator", "relationship_type": "founding_operator",
		"charter_text": "CHARTER-TEXT Quinn and I build carefully.", "autonomy_level": StageTrusted,
		"operator_approval_excerpt": "their word", "operator_approval_turn": word.TurnSeq, "approval_basis": "conversation_turn", "approval_digest": digest, "approval_press_turn": press})
	since, err := s.LastWideningSeq("rel_q")
	if err != nil || since != 4 {
		t.Fatalf("the last press reads %d %v, want 4", since, err)
	}
	seqEvent(t, s, 5, ledger.EventCommitmentPromised, map[string]any{"id": "cm_b", "counterpart_id": "rel_q", "description": "the figures"})
	seqEvent(t, s, 6, ledger.EventCommitmentStateChange, map[string]any{"id": "cm_b", "state": "completed", "result": "filed"})
	seqEvent(t, s, 7, ledger.EventCommitmentPromised, map[string]any{"id": "cm_c", "counterpart_id": "rel_q", "description": "the call"})
	seqEvent(t, s, 8, ledger.EventCommitmentStateChange, map[string]any{"id": "cm_c", "state": "abandoned", "result": "no time"})
	seqEvent(t, s, 9, ledger.EventRelationshipUpsert, map[string]any{"id": "rel_p", "counterpart_name": "Pat", "counterpart_role": "peer", "relationship_type": "peer"})
	seqEvent(t, s, 10, ledger.EventCommitmentPromised, map[string]any{"id": "cm_p", "counterpart_id": "rel_p", "description": "a peer's"})
	seqEvent(t, s, 11, ledger.EventCommitmentStateChange, map[string]any{"id": "cm_p", "state": "completed"})
	completed, abandoned, err := s.WideningCounts("rel_q", since)
	if err != nil || len(completed) != 1 || completed[0].ID != "cm_b" || completed[0].Result != "filed" || abandoned != 1 {
		t.Fatalf("the endings since the press read %+v abandoned %d %v, want cm_b completed and one abandoned", completed, abandoned, err)
	}
	seqEvent(t, s, 12, ledger.EventCommitmentStateChange, map[string]any{"id": "cm_c", "state": "repaired", "result": "made good"})
	if _, abandoned, _ := s.WideningCounts("rel_q", since); abandoned != 0 {
		t.Fatalf("a repaired commitment still counts as abandoned: %d", abandoned)
	}
	if all, _, _ := s.WideningCounts("rel_q", 0); len(all) != 2 {
		t.Fatalf("from the beginning, %d completions, want 2", len(all))
	}
}

// ONLY THE PRESS ON THE LEVEL CONTROL IS A PRESS (a review, 2026-11-06): an
// answer on any other card, said as an answer and ending with the marker,
// carries no press mark and is refused; the fresh-turn lookup reads the
// opening's own session, never the fact's text.
func TestOnlyThePressOnTheLevelControlIsAPress(t *testing.T) {
	s := testStore(t)
	operatorRelationship(t, s, 1, "rel_q", "Quinn", "founding_operator", "")
	digest := AutonomyApprovalDigest("rel_q", StageAutonomous)
	if err := s.AddConversationTurn("operator", "I approve the charter."); err != nil {
		t.Fatal(err)
	}
	word, _ := s.GetLatestOperatorTurn()
	// A choice on an ask card, as the page records one, quoting the marker.
	ref, err := s.RecordInteraction(context.Background(), InteractionInput{ID: "ask_choice", Kind: interaction.Message, Role: interaction.Operator, SessionID: "default",
		Content: "[ask x] chose: " + ApprovalPressContent("Quinn's identity", StageAutonomous, digest), Details: interaction.Details{Said: interaction.SaidAnswer}})
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := s.LatestOperatorPress(digest); p != nil {
		t.Fatalf("a card's answer read as the press: %+v", p)
	}
	s.mu.Lock()
	err = s.pressTurnLocked(ref.Sequence, digest)
	s.mu.Unlock()
	if err == nil {
		t.Fatal("a card's answer was admitted as the press")
	}
	// The press itself, with its mark.
	press := pressOn(t, s, "rel_q", StageAutonomous)
	s.mu.Lock()
	err = s.pressTurnLocked(press, digest)
	s.mu.Unlock()
	if err != nil {
		t.Fatalf("the press was refused: %v", err)
	}
	if p, _ := s.LatestOperatorPress(digest); p == nil || p.TurnSeq != press {
		t.Fatalf("the press is not the latest press: %+v", p)
	}
	_ = word
	// The lookup is structural: A's opening whose fact quotes B counts for A only.
	ctx := interaction.WithTurn(context.Background(), "turn-c")
	for _, id := range []string{"ws_a", "ws_b"} {
		if err := s.StartWorkSession(ctx, id, "keep a promise"); err != nil {
			t.Fatal(err)
		}
	}
	promise, now := "in_1", "now"
	if err := s.SetWorkPromise(ctx, "ws_b", &promise, &now, ""); err != nil {
		t.Fatal(err)
	}
	_, _, since, _, _ := s.PromiseChangedAt("ws_b")
	legA := interaction.WithResumeSession(interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn-legA"), "continuation"), "ws_a")
	if _, err := s.RecordRelatedConversation(legA, string(interaction.System), "[budget checkpoint — continuation 1] "+ContinuationFactNames("ws_a")+"is still active. Plan: see "+ContinuationFactNames("ws_b")+"too.", "turn-legA", "", interaction.Details{}); err != nil {
		t.Fatal(err)
	}
	if ran, _ := s.ContinuationRanSince("ws_b", since); ran {
		t.Fatal("A's opening, quoting B in its fact, counted as B's fresh turn")
	}
	if ran, _ := s.ContinuationRanSince("ws_a", 0); !ran {
		t.Fatal("A's own opening was not read")
	}
}

// A DELIVERY IS CITED UNDER THE PROMISE IT WAS AWAITED UNDER (a review,
// 2026-11-06, round 4): kept or met under the promise, it grounds the words;
// awaited under another promise, or replaced since, it grounds nothing.
func TestADeliveryIsCitedUnderThePromiseItWasAwaitedUnder(t *testing.T) {
	s := testStore(t)
	ctx := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn-c"), "operator")
	start := func(id, what string) {
		t.Helper()
		if err := s.StartWorkSession(ctx, id, what); err != nil {
			t.Fatal(err)
		}
	}
	deliver := func(id string) {
		t.Helper()
		if err := s.DeliverWorkSession(ctx, id, "DELIVERED: "+id, EvidenceWorkerReportOnly, ""); err != nil {
			t.Fatal(err)
		}
	}
	since := time.Time{}
	p1, p2, word := "in_1", "in_2", "word"
	// Kept under in_1, then met by its harvest: citable both times.
	start("ws_p", "keep a promise")
	subagentForTest(t, s, ctx, "ws_c", "find the figures")
	on := "delivery:ws_c"
	if err := s.SetWorkPromise(ctx, "ws_p", &p1, &on, ""); err != nil {
		t.Fatal(err)
	}
	deliver("ws_c")
	if ok, err := s.NewerFactCited("ws_p", "in_1", "in_1: ws_c delivered the figures.", since); !ok || err != nil {
		t.Fatalf("a delivery the condition waits on was not cited: %v %v", ok, err)
	}
	if err := s.MeetWorkCondition(ctx, "ws_p", "delivery:ws_c", "A delivery from ws_c came"); err != nil {
		t.Fatal(err)
	}
	if ws, _ := s.WorkSessionByID("ws_p"); ws.ResumesOn != "" || ws.Promise != "in_1" {
		t.Fatalf("the met condition: %+v", ws)
	}
	if ok, err := s.NewerFactCited("ws_p", "in_1", "in_1: ws_c delivered the figures.", since); !ok || err != nil {
		t.Fatalf("a delivery the harvest met was not cited after it: %v %v", ok, err)
	}
	// Awaited under in_1, the promise then changed to in_2: not in_2's fact.
	start("ws_q", "keep a promise")
	subagentForTest(t, s, ctx, "ws_d", "find more")
	on = "delivery:ws_d"
	if err := s.SetWorkPromise(ctx, "ws_q", &p1, &on, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SetWorkPromise(ctx, "ws_q", &p2, nil, ""); err != nil {
		t.Fatal(err)
	}
	deliver("ws_d")
	if ok, err := s.NewerFactCited("ws_q", "in_2", "in_2: ws_d delivered.", since); ok || err != nil {
		t.Fatalf("a delivery awaited under another promise was cited: %v %v", ok, err)
	}
	// Awaited, then the condition replaced by the operator's word: not cited.
	start("ws_r", "keep a promise")
	subagentForTest(t, s, ctx, "ws_e", "find the rest")
	on = "delivery:ws_e"
	if err := s.SetWorkPromise(ctx, "ws_r", &p1, &on, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SetWorkPromise(ctx, "ws_r", nil, &word, ""); err != nil {
		t.Fatal(err)
	}
	deliver("ws_e")
	if ok, err := s.NewerFactCited("ws_r", "in_1", "in_1: ws_e delivered.", since); ok || err != nil {
		t.Fatalf("a delivery whose condition was replaced was cited: %v %v", ok, err)
	}
}

// A RESUMED TURN'S CALLS ARE ITS OWN SESSION'S (a review, 2026-11-06, round 4):
// the start row's seat is the session the turn's opening names; a turn with
// no opening keeps the newest active session, marked several.
func TestAResumedTurnsCallsAreItsOwnSessions(t *testing.T) {
	s := testStore(t)
	ctx := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn-c"), "operator")
	for _, id := range []string{"ws_old", "ws_new"} {
		if err := s.StartWorkSession(ctx, id, "work "+id); err != nil {
			t.Fatal(err)
		}
	}
	leg := interaction.WithResumeSession(interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn-leg"), "continuation"), "ws_old")
	if _, err := s.RecordRelatedConversation(leg, string(interaction.System), "[budget checkpoint — continuation 1] "+ContinuationFactNames("ws_old")+"is still active.", "turn-leg", "", interaction.Details{}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordToolStart("turn-leg", 1, "main", "model-x", "", "shell", `{"command":"ls"}`); err != nil {
		t.Fatal(err)
	}
	seat, found, err := s.ToolCallSeat("turn-leg", 1)
	if err != nil || !found || seat.Session != "ws_old" || seat.Reason != "" {
		t.Fatalf("the resumed turn's call seat: %+v %v %v", seat, found, err)
	}
	if seat, err := s.StartSeat("turn-leg", "main"); err != nil || seat.Session != "ws_old" {
		t.Fatalf("the seat outside a start row: %+v %v", seat, err)
	}
	if err := s.RecordToolStart("turn-plain", 1, "main", "model-x", "", "shell", `{"command":"ls"}`); err != nil {
		t.Fatal(err)
	}
	seat, _, err = s.ToolCallSeat("turn-plain", 1)
	if err != nil || seat.Session != "ws_new" || seat.Reason != SeveralActive {
		t.Fatalf("a plain turn's call seat: %+v %v", seat, err)
	}
}

// A CONDITION KEEPS ITS PROMISE THROUGH A CHANGE OF THE PROMISE AND ITS
// HARVEST (a review, 2026-11-06, round 5): the harvest meets it under the
// promise it was kept under, never the one in force now; the floor reads
// the latest condition notice first and its promise second, so a replaced
// condition is not revived by changing the promise back.
func TestAConditionKeepsItsPromiseThroughAPromiseChangeAndItsHarvest(t *testing.T) {
	s := testStore(t)
	ctx := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn-c"), "operator")
	start := func(id, what string) {
		t.Helper()
		if err := s.StartWorkSession(ctx, id, what); err != nil {
			t.Fatal(err)
		}
	}
	deliver := func(id string) {
		t.Helper()
		if err := s.DeliverWorkSession(ctx, id, "DELIVERED: "+id, EvidenceWorkerReportOnly, ""); err != nil {
			t.Fatal(err)
		}
	}
	since := time.Time{}
	p1, p2, word := "in_1", "in_2", "word"
	// Path 1: kept under in_1, the promise alone changed to in_2, then the
	// harvest meets the condition: under in_1, and not in_2's fact.
	start("ws_p", "keep a promise")
	subagentForTest(t, s, ctx, "ws_c", "find the figures")
	on := "delivery:ws_c"
	if err := s.SetWorkPromise(ctx, "ws_p", &p1, &on, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SetWorkPromise(ctx, "ws_p", &p2, nil, "the promise changed; the condition stays"); err != nil {
		t.Fatal(err)
	}
	if ws, _ := s.WorkSessionByID("ws_p"); ws.Promise != "in_2" || ws.ResumesOn != "delivery:ws_c" || ws.ResumesUnder != "in_1" {
		t.Fatalf("the condition after the promise changed: %+v", ws)
	}
	deliver("ws_c")
	if err := s.MeetWorkCondition(ctx, "ws_p", "delivery:ws_c", "A delivery from ws_c came"); err != nil {
		t.Fatal(err)
	}
	var under, met string
	if err := s.DB().QueryRow(`SELECT json_extract(details,'$.work.under'), json_extract(details,'$.work.met') FROM conversations WHERE kind='notice' AND json_extract(details,'$.work.session')='ws_p' AND json_extract(details,'$.work.met') IS NOT NULL ORDER BY turn_seq DESC LIMIT 1`).Scan(&under, &met); err != nil {
		t.Fatal(err)
	}
	if under != "in_1" || met != "delivery:ws_c" {
		t.Fatalf("the harvest met the condition under %q (%s), want the promise it was kept under", under, met)
	}
	if ws, _ := s.WorkSessionByID("ws_p"); ws.ResumesOn != "" || ws.ResumesUnder != "" {
		t.Fatalf("the met condition: %+v", ws)
	}
	if ok, err := s.NewerFactCited("ws_p", "in_2", "in_2: ws_c delivered the figures.", since); ok || err != nil {
		t.Fatalf("a delivery awaited under in_1 was cited as in_2's after its harvest: %v %v", ok, err)
	}
	// Path 2: kept under in_1, replaced by in_2's word, the promise changed
	// back to in_1: the replacement stands; the old condition is not revived.
	start("ws_q", "keep a promise")
	subagentForTest(t, s, ctx, "ws_d", "find more")
	on = "delivery:ws_d"
	if err := s.SetWorkPromise(ctx, "ws_q", &p1, &on, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SetWorkPromise(ctx, "ws_q", &p2, &word, "replaced"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetWorkPromise(ctx, "ws_q", &p1, nil, "the promise changed back"); err != nil {
		t.Fatal(err)
	}
	deliver("ws_d")
	if ok, err := s.NewerFactCited("ws_q", "in_1", "in_1: ws_d delivered.", since); ok || err != nil {
		t.Fatalf("a replaced condition was revived by changing the promise back: %v %v", ok, err)
	}
	// Retained: kept and met under the same promise is cited.
	start("ws_r", "keep a promise")
	subagentForTest(t, s, ctx, "ws_e", "find the rest")
	on = "delivery:ws_e"
	if err := s.SetWorkPromise(ctx, "ws_r", &p1, &on, ""); err != nil {
		t.Fatal(err)
	}
	deliver("ws_e")
	if err := s.MeetWorkCondition(ctx, "ws_r", "delivery:ws_e", "A delivery from ws_e came"); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.NewerFactCited("ws_r", "in_1", "in_1: ws_e delivered.", since); !ok || err != nil {
		t.Fatalf("a delivery kept and met under the promise was not cited: %v %v", ok, err)
	}
}

// subagentForTest makes id a sub-agent's session, as a spawn does (its
// queue row, then its session), so a wait on its delivery can be kept
// (DeliveryNeverComes).
func subagentForTest(t *testing.T, s *Store, ctx context.Context, id, what string) {
	t.Helper()
	if _, err := s.EnqueueWork(&WorkItem{ID: "sub_" + id, Kind: SubagentWorkKind, Payload: "{}", DedupKey: id}); err != nil {
		t.Fatal(err)
	}
	if err := s.StartWorkSession(ctx, id, what); err != nil {
		t.Fatal(err)
	}
}
