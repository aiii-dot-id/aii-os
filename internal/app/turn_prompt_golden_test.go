package app

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/updates"
)

// platform line, the fixture's temporary directory, and the names the
// floor gives this test binary's machine directory and program (a
// temporary name, and app.test or app.test.exe) are the only bytes
// normalised, so the goldens hold on every host the gate runs on. Ring 0
// and Ring 5 are the platform's own words, fetched live and never stored:
// where they stand a golden holds a marker (withoutPlatformText), so it
// pins every byte around them and does not move when the platform
// republishes them. When a change to the prompt is intended, regenerate
// every prompt golden with
//	go test ./internal/app -run 'TestEachTurnPathSendsItsPinnedPrompt|TestEachSafePostureSendsItsPinnedPrompt|TestEveryRequestShape' -update-prompt-golden

var updatePromptGolden = flag.Bool("update-prompt-golden", false, "rewrite the prompt goldens (testdata/turn_prompt, testdata/whole_prompt with this platform's tool sets, testdata/prompt_sizes.txt) from the requests the host sends")

var (
	turnGoldenNow      = regexp.MustCompile(`### Now\n[^\n]*`)
	turnGoldenOperator = regexp.MustCompile(`### Your operator\n[^\n]*`)
	turnGoldenDate     = regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}\b`)
	turnGoldenPlatform = regexp.MustCompile(`You are running on [^.]*\.`)
)

// turnPath is one way a turn opens, run on an app the golden fixtures made.
type turnPath struct {
	name string
	run  func(t *testing.T, a *App)
}

// turnPaths are the ways a turn opens that TestEachTurnPathSendsItsPinnedPrompt
// pins; the whole-request goldens open the same turns on a realistic identity.
func turnPaths() []turnPath {
	return []turnPath{
		{"operator", func(t *testing.T, a *App) {
			ctx := goldenTurnCtx(t)
			if err := a.acquireTurn(ctx); err != nil {
				t.Fatal(err)
			}
			if reply, err := a.observeChat(ctx, "GOLDEN_OPERATOR the typed words", func(kind, name, args string) {}); err != nil || reply != "noted" {
				t.Fatalf("operator turn = %q, %v", reply, err)
			}
		}},
		{"spoken", func(t *testing.T, a *App) {
			ctx := goldenTurnCtx(t)
			if err := a.acquireTurn(ctx); err != nil {
				t.Fatal(err)
			}
			defer a.releaseTurn()
			if reply, err := a.wake(ctx, roleOperator, voiceMarker+"GOLDEN_SPOKEN the spoken words"); err != nil || reply != "noted" {
				t.Fatalf("spoken operator turn = %q, %v", reply, err)
			}
		}},
		{"leftover", func(t *testing.T, a *App) {
			// The words' turn is given the token by the release that kept
			// it for them (releaseTurn) and never takes it itself: held
			// here as that release leaves it.
			<-a.turnGate
			a.runLeftoverSteerTurn([]steerEntry{
				{role: roleOperator, content: "GOLDEN_LEFTOVER_ONE the operator's queued words", recordCtx: context.Background()},
				{role: roleParticipant, content: "GOLDEN_LEFTOVER_TWO a participant's queued words", recordCtx: context.Background()},
			})
		}},
		{"system", func(t *testing.T, a *App) {
			ctx := goldenTurnCtx(t)
			if err := a.acquireTurn(ctx); err != nil {
				t.Fatal(err)
			}
			defer a.releaseTurn()
			if reply, err := a.wake(withTurnSource(ctx, turnSourceHarvest), string(interaction.System), "[GOLDEN_SYSTEM a fact the substrate states]"); err != nil || reply != "noted" {
				t.Fatalf("system wake = %q, %v", reply, err)
			}
		}},
		{"timer", func(t *testing.T, a *App) {
			ctx := goldenTurnCtx(t)
			if err := a.acquireTurn(ctx); err != nil {
				t.Fatal(err)
			}
			defer a.releaseTurn()
			// The firing that woke this turn is a pending timer event of
			// its own; the turn's facts leave it out.
			if err := a.store.AddOutboxMessage("timer_golden_1", "operator", "", "GOLDEN_TIMER_NOTICE the firing itself", nil); err != nil {
				t.Fatal(err)
			}
			ctx = context.WithValue(withTurnSource(ctx, turnSourceTimer), timerRecordKey{}, "timer_golden_1")
			if reply, err := a.wake(ctx, string(interaction.System), "[GOLDEN_TIMER the alarm] — your own alarm woke you. Respond to your operator in your own words."); err != nil || reply != "noted" {
				t.Fatalf("timer wake = %q, %v", reply, err)
			}
		}},
		{"chartered", func(t *testing.T, a *App) {
			goldenRelationship(t, a)
			ctx := goldenTurnCtx(t)
			if err := a.acquireTurn(ctx); err != nil {
				t.Fatal(err)
			}
			if reply, err := a.observeChat(ctx, "GOLDEN_CHARTERED the typed words", func(kind, name, args string) {}); err != nil || reply != "noted" {
				t.Fatalf("chartered operator turn = %q, %v", reply, err)
			}
		}},
		{"participant", func(t *testing.T, a *App) {
			ctx := goldenTurnCtx(t)
			if err := a.acquireTurn(ctx); err != nil {
				t.Fatal(err)
			}
			defer a.releaseTurn()
			if reply, err := a.wake(withTurnSource(ctx, turnSourceArrival), string(interaction.Participant), "[messages] GOLDEN_PARTICIPANT a contact wrote"); err != nil || reply != "noted" {
				t.Fatalf("participant wake = %q, %v", reply, err)
			}
		}},
	}
}

func TestEachTurnPathSendsItsPinnedPrompt(t *testing.T) {
	for _, p := range turnPaths() {
		t.Run(p.name, func(t *testing.T) {
			a, dir := goldenTurnApp(t)
			obs := fixtureModel(t, a, func(_ int, w http.ResponseWriter) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(textReply("noted")))
			})
			p.run(t, a)
			waitUntil(t, func() bool { return !a.TurnActive() }, "the turn to end")
			reqs := obs.snapshot()
			if len(reqs) != 1 {
				t.Fatalf("the %s path sent %d requests, want 1", p.name, len(reqs))
			}
			got := goldenRequest(t, reqs[0], dir)
			file := filepath.Join("testdata", "turn_prompt", p.name+".golden")
			if *updatePromptGolden {
				if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(file, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("%v (generate it with -update-prompt-golden)", err)
			}
			if got != string(want) {
				t.Fatalf("the %s path's request moved from %s.\n%s", p.name, file, goldenDiff(string(want), got))
			}
		})
	}
}

// event of each kind (goldenFixtures).
func goldenTurnApp(t *testing.T) (*App, string) {
	t.Helper()
	a, dir := dispatchApp(t)
	t.Cleanup(func() { stopApp(t, a) })
	goldenFixtures(t, a)
	return a, dir
}

// goldenFixtures are the golden app's fixed inputs, given to a booted
// identity.
func goldenFixtures(t *testing.T, a *App) {
	t.Helper()
	if err := a.store.SetStandingState("GOLDEN_STANDING waiting on the review"); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	if err := a.recordConversation(ctx, string(interaction.Operator), "GOLDEN_EARLIER the operator asked before"); err != nil {
		t.Fatal(err)
	}
	if err := a.recordConversation(ctx, string(interaction.Resident), "GOLDEN_EARLIER_REPLY the identity answered"); err != nil {
		t.Fatal(err)
	}
	// One pending event of each kind, a few milliseconds apart: they are
	// shown in the order they happened, and a tie would be decided by id.
	if _, err := a.store.RecordInbound("in_mail_golden", "mail", "friend", "GOLDEN_ARRIVAL a message waiting"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * time.Millisecond)
	if err := a.store.AddOutboxMessage("timer_golden_other", "operator", "", "GOLDEN_OTHER_TIMER a firing no turn took", nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * time.Millisecond)
	if err := a.store.AddOutboxMessage("msg_golden_outcome", "peer", "friend", "sent earlier", nil); err != nil {
		t.Fatal(err)
	}
	if err := a.store.MarkEffectUnknown(claimRow(t, a, "msg_golden_outcome"), "mail", "fixture response lost"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * time.Millisecond)
}

// goldenRelationship gives the golden identity a charter its operator
// affirmed, one belief about them (SHAPED_BY the relationship, from an
// experience of its own) and both parts of Ring 3, through the live door.
func goldenRelationship(t *testing.T, a *App) {
	t.Helper()
	ctx := interaction.WithSaid(t.Context(), interaction.SaidTyped) // as the chat door marks what the operator types
	if err := a.recordConversation(ctx, string(interaction.Operator), "GOLDEN_YES that is our charter"); err != nil {
		t.Fatal(err)
	}
	turn, err := a.store.GetLatestOperatorTurn()
	if err != nil || turn == nil {
		t.Fatalf("operator turn = %+v, %v", turn, err)
	}
	for _, e := range []struct {
		typ     ledger.EventType
		ring    int
		payload map[string]any
	}{
		{ledger.EventRelationshipUpsert, 1, map[string]any{
			"id": "rel_golden", "counterpart_name": "Quinn", "counterpart_role": "operator",
			"relationship_type": "founding_operator", "charter_text": "GOLDEN_CHARTER Quinn and I build carefully.",
			"operator_approval_excerpt": turn.Content, "operator_approval_turn": turn.TurnSeq, "approval_basis": "conversation_turn",
		}},
		{ledger.EventExperienceCreate, 3, map[string]any{"id": "exp_golden_asks", "content": "Quinn asked what I think first", "category": "observation", "provenance": "self"}},
		{ledger.EventBeliefUpsert, 3, map[string]any{"id": "b_golden_asks", "statement": "GOLDEN_ABOUT Quinn asks what you think first", "ring": 3, "confidence": 0.7,
			"origin_refs": []map[string]string{{"kind": "experience", "id": "exp_golden_asks"}}}},
		{ledger.EventEdgeCreate, 3, map[string]any{"id": "edge_golden_about", "from_id": "rel_golden", "to_id": "b_golden_asks", "edge_type": "SHAPED_BY"}},
	} {
		if _, err := a.door.Append(e.typ, e.ring, e.payload, ""); err != nil {
			t.Fatalf("%s: %v", e.typ, err)
		}
	}
	if err := a.rings.SetSection(ring.Ring3, "surfacing", "GOLDEN_SURFACING You may be noticing a pattern."); err != nil {
		t.Fatal(err)
	}
	if err := a.rings.SetSection(ring.Ring3, "working_truth", "GOLDEN_WORKING_TRUTH You believe the anchor held."); err != nil {
		t.Fatal(err)
	}
}

func goldenTurnCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// goldenRequest is one request's messages, role by role, as a golden pins
// them (pinnedText).
func goldenRequest(t *testing.T, body, dir string) string {
	t.Helper()
	var b strings.Builder
	for i, m := range wireMessages(t, body) {
		fmt.Fprintf(&b, "=== message %d (%s) ===\n", i, m.Role)
		b.WriteString(m.Content)
		b.WriteString("\n")
	}
	return pinnedText(t, b.String(), dir)
}

// pinnedText is text a request carried as a golden pins it: the platform's
// own words replaced by their markers (withoutPlatformText), and the clock,
// the platform, the fixture's directory and the names the floor gives this
// test binary's machine directory and program normalised.
func pinnedText(t *testing.T, text, dir string) string {
	t.Helper()
	out := withoutPlatformText(t, text)
	if dir != "" {
		out = strings.ReplaceAll(out, dir, "<dir>")
	}
	if machineDir, err := accountMachineDir(); err == nil {
		out = strings.ReplaceAll(out, "**"+filepath.Base(machineDir)+"**", "**<machine>**")
	}
	if program, err := updates.Program(); err == nil {
		out = strings.ReplaceAll(out, "**"+filepath.Base(program)+"**", "**<program>**")
	}
	out = turnGoldenNow.ReplaceAllString(out, "### Now\n<now>")
	out = turnGoldenOperator.ReplaceAllString(out, "### Your operator\n<operator>")
	out = turnGoldenPlatform.ReplaceAllString(out, "You are running on <platform>.")
	return turnGoldenDate.ReplaceAllString(out, "<date>")
}

const (
	ring0Marker     = "<<Ring 0: fetched live from the platform; not pinned here>>"
	ring5Marker     = "<<Ring 5: fetched live from the platform; not pinned here>>"
	bootstrapMarker = "<<Bootstrap packet: fetched live from the platform; not pinned here>>"
)

// livePlatformText is each text the platform serves that a request may
// carry, with the marker that stands in its place: what this test binary
// fetched (genesistest), so the replacement is exact.
func livePlatformText(t *testing.T) []struct{ text, marker string } {
	t.Helper()
	root := genesistest.NewRoot(t)
	return []struct{ text, marker string }{
		{root.Constitution(t), ring0Marker},
		{root.Ring5(t), ring5Marker},
		{root.BootstrapPrompt(t), bootstrapMarker},
	}
}

func withoutPlatformText(t *testing.T, s string) string {
	t.Helper()
	live := livePlatformText(t)
	for _, p := range live {
		s = strings.ReplaceAll(s, p.text, p.marker)
	}
	for _, p := range live {
		for _, line := range strings.Split(p.text, "\n") {
			if len(strings.TrimSpace(line)) >= 20 && strings.Contains(s, line) {
				t.Fatalf("a line of the platform's own text (%s) is still in the text a golden would store: %q", p.marker, line)
			}
		}
	}
	return s
}

// goldenDiff names the first line where two goldens part.
func goldenDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(w) || i < len(g); i++ {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if wl != gl {
			return fmt.Sprintf("first difference at line %d:\nwant: %s\ngot:  %s", i+1, wl, gl)
		}
	}
	return "(no line differs)"
}
