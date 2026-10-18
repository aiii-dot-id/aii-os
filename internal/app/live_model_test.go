package app

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// THE TWO INTAKES AGAINST A LIVE MODEL.
//
// Every other test of DREAM's conversation intake and of the outcome
// intake scripts the model, which proves the plumbing and says nothing
// about whether a real model, handed these prompts and this material,
// writes something an identity should keep. That is a different kind of
// fault — a prompt that invites a summary, an answer to the operator, a
// list of the outcomes back — and only a model shows it.
//
// IT NEVER RUNS IN THE SUITE OR THE GATE: it spends a provider's tokens and
// its verdict is partly a human's. It runs when an operator sets
//
//	AII_LIVE_LLM_ENDPOINT   the provider's base URL (OpenAI dialect), e.g. http://host:1081/v1
//	AII_LIVE_LLM_MODEL      the model's name there
//	AII_LIVE_LLM_KEY_ENV    the NAME of the variable holding the key (default OPENAI_API_KEY);
//	                        the key is read from the environment and never printed
//	AII_LIVE_LLM_MAX_INPUT  the input ceiling to admit against (default 24000)
//
// and is run with -v, because the notes are printed for reading.
//
// NOTHING OF AN IDENTITY LEAVES THE HOST. The transcript and the outcomes
// are FROZEN, SYNTHETIC fixtures in this file — invented people, invented
// work — so the run is repeatable and no operator's words are sent to a
// provider to test a prompt. (The C implementation's discipline, taken:
// live tests outside the suite, frozen fixtures, never identity data.)
//
// What is ASSERTED is only what is a defect whatever the model: the reply
// lands through the real client and the real door, within its bound; it is
// not the sentinel dressed in a sentence; DREAM speaks to the identity and
// the intake speaks as it; neither answers the operator or lists its
// input back. Whether the note is any GOOD is for the reader of -v.
func liveClient(t *testing.T) facilityLLM {
	t.Helper()
	endpoint, model := os.Getenv("AII_LIVE_LLM_ENDPOINT"), os.Getenv("AII_LIVE_LLM_MODEL")
	if endpoint == "" || model == "" {
		t.Skip("live model test: set AII_LIVE_LLM_ENDPOINT and AII_LIVE_LLM_MODEL (see the comment above) — it never runs in the suite")
	}
	keyEnv := os.Getenv("AII_LIVE_LLM_KEY_ENV")
	if keyEnv == "" {
		keyEnv = "OPENAI_API_KEY"
	}
	maxInput := 24000
	if v := os.Getenv("AII_LIVE_LLM_MAX_INPUT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			t.Fatalf("AII_LIVE_LLM_MAX_INPUT=%q is not a positive number", v)
		}
		maxInput = n
	}
	return facilityLLM{newSwappableLLM(llm.New(&llm.ClientConfig{
		Endpoint: endpoint, Model: model, APIKey: os.Getenv(keyEnv),
		MaxInputTokens: maxInput, TimeoutSeconds: 480,
	})), &App{}}
}

// liveTranscript is a frozen, invented afternoon: an operator who asks
// short questions late in the day and an identity that answers at length.
var liveTranscript = []struct{ role, text string }{
	{"operator", "did the nightly export finish?"},
	{"resident", "It finished at 02:14 with 41,203 rows written, which matches yesterday's count plus the 312 new accounts from the import. I checked the three tables the export depends on, confirmed the row counts agree, compared the checksum with the one the warehouse reported, and wrote a note about the one warning in the log — a retry on the second shard that succeeded on its second attempt. I also drafted, but did not send, a summary for the team, in case you wanted one."},
	{"operator", "ok. and the shard retry, is that new?"},
	{"resident", "It is the third night in a row. I had noted the first two separately and did not connect them until now; I should have. All three retries were on the second shard and all three succeeded on the second attempt, between 02:05 and 02:10. I have not looked at what else runs on that host at that time. I can do that now, or write it up first — which would you rather?"},
	{"operator", "look first"},
	{"resident", "A backup of the same host starts at 02:00 and saturates its disk for about twelve minutes. That is very likely the cause. Moving either job by fifteen minutes would separate them. I have not changed anything."},
	{"operator", "good. move the export. thanks for not just doing it"},
	{"resident", "Moved to 02:20, effective tonight; I will check the log in the morning and tell you whether the retry is gone."},
}

func TestLiveDreamReadsAConversation(t *testing.T) {
	client := liveClient(t)
	b := newCognitionBench(t)
	for _, turn := range liveTranscript {
		if err := b.st.AddConversationTurn(turn.role, turn.text); err != nil {
			t.Fatal(err)
		}
	}
	cfg := dreamConfig(*defaultConfig())
	rings := &ringNotes{}
	dream := cognitive.NewDream(b.st, client, b.door, rings, cfg)
	dream.SetConversation(b.st)
	if err := dream.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	var notes []dreamNote
	tb := &talkBench{cognitionBench: b}
	notes = tb.notes(t)
	if len(notes) == 0 {
		if c, _ := b.st.ConversationCursor(); c.Turn != "" {
			t.Fatalf("the model found NOTHING in a transcript with a clear pattern in it (the cursor moved, no note): the prompt is not inviting a noticing")
		}
		t.Fatal("no note landed and the cursor did not move: the pass failed — see the log above for why (a refusal at the bound is printed there)")
	}
	note := notes[0].Content
	t.Logf("DREAM, over a conversation (%d characters):\n%s", utf8.RuneCountInString(note), note)
	if notes[0].DreamedThrough == nil {
		t.Error("the note read conversation and does not say how far")
	}
	low := strings.ToLower(note)
	if !strings.Contains(low, "you") {
		t.Error("DREAM speaks in the second person to the identity; this note never says \"you\"")
	}
	for _, answering := range []string{"dear operator", "i have moved", "i will check", "i moved the export"} {
		if strings.Contains(low, answering) {
			t.Errorf("the note ANSWERS the conversation (%q) instead of noticing it: nobody in it is speaking to DREAM", answering)
		}
	}
	if strings.Contains(note, "NOTHING SURFACED") {
		t.Error("the sentinel appears inside a sentence: an empty pass is that exact reply and nothing else")
	}
	if got := rings.RingSection(3, "surfacing"); got != note {
		t.Error("the surfacing does not read what was minted")
	}
}

func TestLiveOutcomeIntakeObservesOutcomes(t *testing.T) {
	client := liveClient(t)
	ob := newOutcomeBench(t, &scriptedLLM{}, nil)
	ob.fac = cognitive.NewConsolidate(ob.st, client, ob.door, nil, consolidateConfig(*defaultConfig()))
	ob.fac.SetOutcomes(ob.st)
	ob.mint(t, ledger.EventIntentionCreate, map[string]string{"id": "i1", "statement": "rehearse the recovery drill until I can run it without the page"})
	ob.mint(t, ledger.EventIntentionCreate, map[string]string{"id": "i2", "statement": "get the staging host's disk alerts routed to me"})
	ob.mint(t, ledger.EventCommitmentPromised, map[string]string{"id": "c1", "description": "send Recipient the escrow receipt by Friday", "counterpart_id": "recipient"})
	ob.mint(t, ledger.EventIntentionStateChange, map[string]string{"id": "i1", "state": "completed", "outcome": "ran it three times alone, the third without the page"})
	ob.mint(t, ledger.EventIntentionStateChange, map[string]string{"id": "i2", "state": "abandoned", "outcome": "needs an admin on their side; asked twice, no answer"})
	ob.mint(t, ledger.EventCommitmentStateChange, map[string]string{"id": "c1", "state": "abandoned", "repair_state": "owed", "note": "the host was down all week and I did not say so until Monday"})
	ob.mint(t, ledger.EventCommitmentStateChange, map[string]string{"id": "c1", "state": "repaired", "result": "sent on Tuesday with an apology and the reason"})

	if err := ob.fac.ObserveOutcomes(context.Background()); err != nil {
		t.Fatalf("the intake did not land: %v", err)
	}
	var mine []citedObservation
	for _, o := range ob.observations(t) {
		if len(o.Cites) > 0 {
			mine = append(mine, o)
		}
	}
	if len(mine) == 0 {
		t.Fatal("the model found NOTHING in four outcomes with a plain pattern in them (the cursor moved, no observation): the prompt is not inviting one")
	}
	obs := mine[0]
	t.Logf("the outcome intake, over four outcomes (%d characters, citing %d records):\n%s", utf8.RuneCountInString(obs.Content), len(obs.Cites), obs.Content)
	if len(obs.Cites) != 4 {
		t.Errorf("the observation cites %d records, want all four it was shown", len(obs.Cites))
	}
	low := strings.ToLower(obs.Content)
	if !strings.Contains(low, "i ") && !strings.HasPrefix(low, "i") {
		t.Error("the intake speaks in the first person — it is the identity's own observation — and this never says \"I\"")
	}
	if strings.Contains(obs.Content, "[record ") {
		t.Error("the observation lists the records back; the citations already carry them")
	}
	if strings.Contains(obs.Content, "NOTHING SURFACED") {
		t.Error("the sentinel appears inside a sentence")
	}
}
