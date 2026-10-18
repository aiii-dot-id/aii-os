package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

type charterIdentity struct {
	t   *testing.T
	dir string
	app *App
}

func newCharterIdentity(t *testing.T) *charterIdentity {
	t.Helper()
	dir := t.TempDir()
	genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name: "Charter", KeyPath: filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
	}).Ledger.Close()
	c := &charterIdentity{t: t, dir: dir}
	c.boot()
	return c
}

func (c *charterIdentity) config() *Config {
	cfg := defaultConfig()
	cfg.Identity = IdentityConfig{KeyPath: filepath.Join(c.dir, "identity.sec"),
		LedgerPath: filepath.Join(c.dir, "ledger.jsonl"), DBPath: filepath.Join(c.dir, "aii.db")}
	cfg.LLM = withTestProvider(c.t, c.dir, "test", "https://127.0.0.1:1", "m", "sk-x")
	cfg.SourcePath = filepath.Join(c.dir, "config.json")
	cfg.Dashboard.Port = 0
	cfg.Tools.CWD = c.dir
	return cfg
}

// boot brings the identity up live, with a model that answers "noted".
func (c *charterIdentity) boot() {
	c.t.Helper()
	c.app = New(c.config())
	if err := startLiveForTest(c.app); err != nil {
		c.t.Fatal(err)
	}
	c.t.Cleanup(func() { stopApp(c.t, c.app) })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, textReply("noted"))
	}))
	c.t.Cleanup(srv.Close)
	c.app.llmSwap.Swap(llm.New(&llm.ClientConfig{Endpoint: srv.URL, Model: "fake", MaxOutputTokens: 4096, Retries: -1}))
}

func (c *charterIdentity) stop() {
	c.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := c.app.Shutdown(ctx); err != nil {
		c.t.Fatalf("shutdown: %v", err)
	}
}

// say is the operator speaking: one turn through the conversation. It
// returns the number the conversation gave those words, found by the words.
func (c *charterIdentity) say(words string) uint64 {
	c.t.Helper()
	operatorTurn(c.t, c.app, words)
	turns, err := c.app.store.RecentTurnsIncludingSystem(200)
	if err != nil {
		c.t.Fatal(err)
	}
	for i := len(turns) - 1; i >= 0; i-- {
		if turns[i].Role == "operator" && turns[i].Content == words {
			return turns[i].TurnSeq
		}
	}
	c.t.Fatalf("the conversation does not hold the operator's words %.60q", words)
	return 0
}

// upsert is the identity's own act: relationship.upsert through its verb,
// which stamps the latest operator turn.
func (c *charterIdentity) upsert(args map[string]any) {
	c.t.Helper()
	args["variant"] = "relationship.upsert"
	if _, err := c.app.engine.ExecuteAction(context.Background(), "verb", "commit", args); err != nil {
		c.t.Fatalf("relationship.upsert %v: %v", args["id"], err)
	}
}

// recorded writes a relationship.upsert as a record that already holds it:
// signed and materialized, not asked of admission. Admission refuses an
// entry that writes the operator's charter and cites no approval of theirs
// (store.ErrOperatorRelationshipUnapproved); a record made by another
// writer, or before that check, may hold one, and it replays. The door's
// lock keeps it in order with the identity's own appends.
func (c *charterIdentity) recorded(payload map[string]any) *ledger.Event {
	c.t.Helper()
	door := c.app.door
	door.mu.Lock()
	defer door.mu.Unlock()
	evt, err := door.Ledger.Append(ledger.EventRelationshipUpsert, 1, payload, door.kp)
	if err != nil {
		c.t.Fatal(err)
	}
	if err := c.app.store.Materialize(evt); err != nil {
		c.t.Fatal(err)
	}
	return evt
}

// read is the store read the Identity view is built from.
func (c *charterIdentity) read(app *App) (*store.Relationship, store.CharterApproval) {
	c.t.Helper()
	rel, err := app.store.CurrentOperatorRelationship()
	if err != nil || rel == nil {
		c.t.Fatalf("no current operator relationship: %v %v", rel, err)
	}
	a, err := app.store.CharterApproval(rel)
	if err != nil {
		c.t.Fatalf("charter approval: %v", err)
	}
	return rel, a
}

// wireApproval is the approval as the page reads it, by the names identity.js uses.
type wireApproval struct {
	State   string `json:"state"`
	Excerpt string `json:"excerpt"`
	Chars   int    `json:"chars"`
	Turn    uint64 `json:"turn"`
	SaidAt  string `json:"said_at"`
	Record  uint64 `json:"record"`
	Error   string `json:"error"`
}

// view is what the dashboard sends for the Identity view: the handler the
// dashboard is wired with, marshaled as the socket sends it.
func (c *charterIdentity) view(app *App) (string, *wireApproval) {
	c.t.Helper()
	st, err := app.buildLiveHandler().GetIdentity()
	if err != nil {
		c.t.Fatalf("identity view: %v", err)
	}
	raw, err := json.Marshal(st)
	if err != nil {
		c.t.Fatal(err)
	}
	var wire struct {
		Charter  string        `json:"charter"`
		Approval *wireApproval `json:"approval"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		c.t.Fatal(err)
	}
	return wire.Charter, wire.Approval
}

func (c *charterIdentity) saidAt(turn uint64) string {
	c.t.Helper()
	t, err := c.app.store.GetTurnBySeq(turn)
	if err != nil || t == nil {
		c.t.Fatalf("turn %d: %v %v", turn, t, err)
	}
	return t.CreatedAt
}

const firstCharter = "We build this together. You tell me what you see, and I say what I do not know."

func TestTheIdentityViewShowsTheOperatorsWordsBesideTheCharter(t *testing.T) {
	c := newCharterIdentity(t)
	if charter, approval := c.view(c.app); charter != "" || approval != nil {
		t.Fatalf("birth mints no Ring 1, so there is no charter and no approval to show: %q %+v", charter, approval)
	}

	// Markup and a line break in the operator's words: verbatim is verbatim.
	yes := "Yes. I approve the charter as you proposed it:\n<b>we build this together</b> & nothing is hidden."
	n1 := c.say(yes)
	c.upsert(map[string]any{"id": "rel_charter_first", "counterpart_name": "Quinn", "counterpart_role": "operator", "charter_text": firstCharter})

	rel, got := c.read(c.app)
	want := store.CharterApproval{Seq: rel.CreatedSeq, Excerpt: yes, Turn: n1, State: store.ApprovalSaid, SaidAt: c.saidAt(n1)}
	if got != want {
		t.Fatalf("the charter's approval:\n got %+v\nwant %+v", got, want)
	}
	charter, approval := c.view(c.app)
	if charter != firstCharter || approval == nil ||
		*approval != (wireApproval{State: "said", Excerpt: yes, Turn: n1, SaidAt: want.SaidAt, Record: rel.CreatedSeq}) {
		t.Fatalf("the Identity view is not sent the operator's words beside the charter: %q %+v", charter, approval)
	}

	// A full replay is f(ledger): the same read, the same answer.
	if err := c.app.store.ReplayFromFile(filepath.Join(c.dir, "ledger.jsonl")); err != nil {
		t.Fatal(err)
	}
	if _, again := c.read(c.app); again != want {
		t.Fatalf("after a full replay the approval reads\n %+v\nnot\n %+v", again, want)
	}

	// A long turn is carried cut, on a character boundary, with its length.
	long := strings.Repeat("Ωé日 ", 374) + "Ωé日!" // 1500 characters, none of them one byte
	n2 := c.say(long)
	c.upsert(map[string]any{"id": "rel_charter_first", "counterpart_name": "Quinn", "counterpart_role": "operator", "charter_text": firstCharter + " Amended."})
	if _, got := c.read(c.app); got.Excerpt != long || got.Turn != n2 || got.State != store.ApprovalSaid {
		t.Fatalf("the store read must give the whole of the words: turn %d (want %d), %s, %d characters", got.Turn, n2, got.State, utf8.RuneCountInString(got.Excerpt))
	}
	_, approval = c.view(c.app)
	if approval == nil || approval.Turn != n2 || approval.Chars != 1500 || !utf8.ValidString(approval.Excerpt) ||
		utf8.RuneCountInString(approval.Excerpt) != approvalShown || !strings.HasPrefix(long, approval.Excerpt) {
		t.Fatalf("a long approval must reach the view as its first %d characters and its whole length: %+v", approvalShown, approval)
	}
	live := *approval

	// SAFE: the record is frozen and the projection is read-only; the
	// operator's words are still beside the charter.
	c.stop()
	cfg := c.config()
	cfg.Genesis.FirewallURL = ""
	safe := New(cfg)
	if err := safe.startLive(); err != nil {
		t.Fatalf("a boot without Ring 5 must come up SAFE: %v", err)
	}
	t.Cleanup(func() { stopApp(t, safe) })
	if _, ok := safe.SafeMode(); !ok {
		t.Fatal("the boot is not SAFE")
	}
	charter, approval = c.view(safe)
	if charter != firstCharter+" Amended." || approval == nil || *approval != live {
		t.Fatalf("in SAFE the Identity view must carry the same approval:\n got %q %+v\nwant %+v", charter, approval, live)
	}
}

func TestASuccessorCharterCitesItsOwnNewerTurn(t *testing.T) {
	c := newCharterIdentity(t)
	c.say("Yes, found this with me.")
	c.upsert(map[string]any{"id": "rel_founding_one", "counterpart_name": "Quinn", "counterpart_role": "operator", "charter_text": firstCharter})
	successor := "Yes: Maria is your operator from today, and I hand over to her."
	n2 := c.say(successor)
	c.upsert(map[string]any{"id": "rel_successor_one", "counterpart_name": "Maria", "counterpart_role": "operator",
		"relationship_type": "operator", "supersedes": "rel_founding_one", "charter_text": "Maria and I work on the harbor project."})

	rel, got := c.read(c.app)
	if rel.ID != "rel_successor_one" || got.Turn != n2 || got.Excerpt != successor || got.Seq != rel.CreatedSeq {
		t.Fatalf("the successor's charter must cite its own turn %d: %s %+v", n2, rel.ID, got)
	}
	charter, approval := c.view(c.app)
	if charter != "Maria and I work on the harbor project." || approval == nil || approval.Turn != n2 || approval.Excerpt != successor {
		t.Fatalf("the Identity view must show Ring 1 — the successor's charter and its approval, turn %d: %q %+v", n2, charter, approval)
	}
}

// The words beside the charter are the ones cited by the event that WROTE
// it. An upsert through the verbs that leaves the charter alone still
// stamps the latest operator turn, and the projection keeps that newest
// excerpt: showing it would put words beside the charter that never
// approved it.
func TestTheCharterCitesTheEventThatWroteIt(t *testing.T) {
	c := newCharterIdentity(t)
	yes := "Yes, that is our charter."
	n1 := c.say(yes)
	c.upsert(map[string]any{"id": "rel_charter_op", "counterpart_name": "Quinn", "counterpart_role": "operator", "charter_text": firstCharter})
	// Another relationship's event inside this row's span of the record.
	c.say("Yes, Ola can be a peer of yours.")
	c.upsert(map[string]any{"id": "rel_peer_ola", "counterpart_name": "Ola", "counterpart_role": "peer", "charter_text": "Ola and I compare notes."})
	later := "Sure, go ahead with the timetable."
	c.say(later)
	c.upsert(map[string]any{"id": "rel_charter_op", "counterpart_name": "Quinn", "counterpart_role": "peer", "trust_level": "established"})

	var held string
	if err := c.app.store.DB().QueryRow(`SELECT operator_approval FROM relationships WHERE id = 'rel_charter_op'`).Scan(&held); err != nil || held != later {
		t.Fatalf("the fixture must leave the projection holding the later words: %q %v", held, err)
	}
	rel, got := c.read(c.app)
	if rel.CharterText != firstCharter || got.Excerpt != yes || got.Turn != n1 || got.Seq != rel.CreatedSeq || got.State != store.ApprovalSaid {
		t.Fatalf("the charter must cite the words that approved it, turn %d, not the newest the row holds: %+v", n1, got)
	}

	// A signed event that writes a charter and cites no approval is said as
	// such — not given another event's words. The door admits none: the
	// relationship is the operator's whatever role the entry names, and
	// the charter is as it was. A record that already holds one is read.
	nobody := map[string]any{
		"id": "rel_charter_op", "counterpart_name": "Quinn", "counterpart_role": "peer", "charter_text": "A charter nobody approved.",
	}
	if _, err := c.app.door.Append(ledger.EventRelationshipUpsert, 1, nobody, ""); !errors.Is(err, store.ErrOperatorRelationshipUnapproved) || !errors.Is(err, store.ErrRefused) {
		t.Fatalf("the door must refuse an entry that writes the operator's charter and cites no approval, by name: %v", err)
	}
	if rel, _ := c.read(c.app); rel.CharterText != firstCharter {
		t.Fatalf("a refused entry changed the charter: %q", rel.CharterText)
	}
	evt := c.recorded(nobody)
	if _, got := c.read(c.app); got != (store.CharterApproval{Seq: evt.Seq, State: store.ApprovalUncited}) {
		t.Fatalf("a charter whose event cites nothing must read as uncited at record %d: %+v", evt.Seq, got)
	}
	charter, approval := c.view(c.app)
	if charter != "A charter nobody approved." || approval == nil || *approval != (wireApproval{State: "uncited", Record: evt.Seq}) {
		t.Fatalf("the view must be told no approval is cited, not handed one: %q %+v", charter, approval)
	}
	// Words with no turn to find them by: uncited, the words kept.
	evt = c.recorded(map[string]any{
		"id": "rel_charter_op", "counterpart_name": "Quinn", "counterpart_role": "peer",
		"charter_text": "A charter with words and no turn.", "operator_approval_excerpt": "yes",
	})
	if _, got := c.read(c.app); got != (store.CharterApproval{Seq: evt.Seq, Excerpt: "yes", State: store.ApprovalUncited}) {
		t.Fatalf("words with no turn must read as uncited, the words kept: %+v", got)
	}

	// A projection whose charter no signed event wrote has diverged: the
	// view says the approval could not be read, and still shows the rest.
	if _, err := c.app.store.DB().Exec(`UPDATE relationships SET charter_text = 'a charter no entry wrote' WHERE id = 'rel_charter_op'`); err != nil {
		t.Fatal(err)
	}
	charter, approval = c.view(c.app)
	if charter != "a charter no entry wrote" || approval == nil || approval.State != "unreadable" || !strings.Contains(approval.Error, "disagree") || approval.Excerpt != "" {
		t.Fatalf("a diverged projection must be said, beside the charter, as unreadable: %q %+v", charter, approval)
	}
}

// The conversation is a process witness. When the projection is lost and
// rebuilt from the record, the cited turn is gone; when new turns reuse its
// number, it holds other words. Both are said — the words stay as signed.
func TestACitedTurnTheConversationNoLongerHoldsIsSaidSo(t *testing.T) {
	c := newCharterIdentity(t)
	yes := "Yes, I approve."
	n1 := c.say(yes)
	c.upsert(map[string]any{"id": "rel_charter_gone", "counterpart_name": "Quinn", "counterpart_role": "operator", "charter_text": firstCharter})
	c.stop()
	for _, f := range []string{"aii.db", "aii.db-wal", "aii.db-shm"} {
		if err := os.Remove(filepath.Join(c.dir, f)); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	c.boot() // the projection rebuilt from the record; the conversation starts again

	rel, got := c.read(c.app)
	if got != (store.CharterApproval{Seq: rel.CreatedSeq, Excerpt: yes, Turn: n1, State: store.ApprovalGone}) {
		t.Fatalf("a cited turn the conversation no longer holds must read as gone, the words as signed: %+v", got)
	}
	if _, approval := c.view(c.app); approval == nil || *approval != (wireApproval{State: "gone", Excerpt: yes, Turn: n1, Record: rel.CreatedSeq}) {
		t.Fatalf("the view must be told the turn is gone: %+v", approval)
	}

	for i := 0; ; i++ {
		var top uint64
		if err := c.app.store.DB().QueryRow(`SELECT COALESCE(MAX(turn_seq), 0) FROM conversations`).Scan(&top); err != nil {
			t.Fatal(err)
		}
		if top >= n1 {
			break
		}
		if i > int(n1) {
			t.Fatalf("the conversation never reached turn %d (at %d)", n1, top)
		}
		c.say("Something else entirely, the " + string(rune('a'+i%26)) + " time.")
	}
	if _, got := c.read(c.app); got.State != store.ApprovalDiffers || got.Excerpt != yes || got.Turn != n1 || got.SaidAt != "" {
		t.Fatalf("a turn number that now holds other words must read as differs: %+v", got)
	}
	if _, approval := c.view(c.app); approval == nil || approval.State != "differs" || approval.Excerpt != yes || approval.SaidAt != "" {
		t.Fatalf("the view must be told the turn differs: %+v", approval)
	}
}
