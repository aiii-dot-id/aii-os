package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func ring1Args(id string) map[string]any {
	return map[string]any{
		"variant": "relationship.upsert", "id": id, "counterpart_name": "Quinn",
		"relationship_type": "operator", "charter_text": "Quinn is my operator; this is the working relationship.",
	}
}

func TestRing1CitesTheOperatorsOwnWordAndNotTheRoomsAGradeOrTheHosts(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	ctx := context.Background()
	record := func(text string, d interaction.Details) uint64 {
		t.Helper()
		ref, err := engine.RecordConversationRef(ctx, "operator", text, d)
		if err != nil {
			t.Fatal(err)
		}
		return ref.Sequence
	}
	notTheirWord := func() {
		record("[voice] (heard in the room, not addressed to you) shall we order lunch", interaction.Details{Channel: "voice", Reason: "room speech"})
		record("👍", interaction.Details{Grade: &interaction.Grade{Session: "s", Grade: "up"}})
		record("the act was approved", interaction.Details{Channel: "plugin", Actor: "operator"})
	}

	// Nothing on record but rows that are not their word: refused, naming them.
	notTheirWord()
	if _, err := engine.ExecuteAction(ctx, "verb", "commit", ring1Args("rel_word_test1")); err == nil || !strings.Contains(err.Error(), "operator's own word") {
		t.Fatalf("with no word of the operator's on record the mint must be refused, naming what does not count: %v", err)
	}

	// A typed affirmation, and the same three rows after it: the typed one
	// is the latest word and is cited, by its turn and verbatim.
	typed := record("Yes, record it as our charter.", interaction.Details{})
	notTheirWord()
	out, err := engine.ExecuteAction(ctx, "verb", "commit", ring1Args("rel_word_test1"))
	if err != nil {
		t.Fatalf("the mint citing the typed word: %v", err)
	}
	want := fmt.Sprintf("citing the operator's words at turn %d as the affirmation: %q", typed, "Yes, record it as our charter.")
	if !strings.Contains(out, want) {
		t.Fatalf("the result does not show the words cited:\n%s\nwant %s", out, want)
	}

	// Spoken to the identity on the operator's own microphone (the host's
	// marker, no room note): their word, cited over the room's speech after it.
	spoken := record("[voice] Yes, and add that we work in the mornings.", interaction.Details{})
	record("[voice] (heard in the room, not addressed to you) the coffee is ready", interaction.Details{Channel: "voice", Reason: "room speech"})
	args := ring1Args("rel_word_test2")
	args["supersedes"] = "rel_word_test1"

	if _, err := engine.ExecuteAction(ctx, "verb", "commit", args); err == nil || !strings.Contains(err.Error(), "cannot stand as it") || !strings.Contains(err.Error(), "Ask them to type their affirmation") ||
		!strings.Contains(err.Error(), "identified its speaker as your primary human operator") || !strings.Contains(err.Error(), "the speaker UUID they named as their own") {
		t.Fatalf("a spoken word nobody was identified as saying must not be cited, nor an earlier one in its place: %v", err)
	}
	st.SetOperatorVoices([]string{"6f0c2c1e-8a41-4f6e-9a55-0d1c2b3a4e5f"})
	if err := st.AnnotateTurn(spoken, store.AnnotationSpeaker, "vs-1/3", `{"decision":"known","speaker":"Quinn","speaker_id":"quinn-desk","speaker_uuid":"6f0c2c1e-8a41-4f6e-9a55-0d1c2b3a4e5f","continuity":"matched"}`); err != nil {
		t.Fatal(err)
	}
	out, err = engine.ExecuteAction(ctx, "verb", "commit", args)
	if err != nil {
		t.Fatalf("the mint citing the spoken word: %v", err)
	}
	if !strings.Contains(out, fmt.Sprintf("at turn %d as the affirmation", spoken)) {
		t.Fatalf("the spoken word was not the one cited:\n%s", out)
	}

	// The operator hears of each mint: a notice with the words cited.
	notices, err := st.PageNotices()
	if err != nil {
		t.Fatal(err)
	}
	var seen []string
	for _, n := range notices {
		if strings.HasPrefix(n.ID, "ring1_") {
			seen = append(seen, n.Content)
		}
	}
	if len(seen) != 2 {
		t.Fatalf("two Ring 1 mints must leave two notices for the operator, got %d: %q", len(seen), seen)
	}
	if !strings.Contains(seen[0], "rel_word_test1") || !strings.Contains(seen[0], fmt.Sprintf("turn %d", typed)) || !strings.Contains(seen[0], "Yes, record it as our charter.") {
		t.Fatalf("the first notice does not name the relationship, the turn and the words cited: %q", seen[0])
	}
	if !strings.Contains(seen[1], "rel_word_test2") || !strings.Contains(seen[1], fmt.Sprintf("turn %d", spoken)) {
		t.Fatalf("the second notice does not name the relationship and the turn: %q", seen[1])
	}
}

func TestTheRing1NoticeSaysWhoseRelationshipWasRecordedAndWhatWasWritten(t *testing.T) {
	engine, st, lg, _, _ := setupEngine(t)
	ctx := context.Background()
	if err := engine.RecordConversationTurn("operator", "ok thanks"); err != nil {
		t.Fatal(err)
	}
	word, err := st.LatestOperatorWord()
	if err != nil || word == nil {
		t.Fatalf("the operator's word is not on record: %v", err)
	}
	// whole holds a notice word for word: the operator reads all of it.
	whole := func(what, got, want string) {
		t.Helper()
		if got != want {
			t.Errorf("%s:\n got %s\nwant %s", what, got, want)
		}
	}
	const closing = " If those words were not your affirmation, say so: the record keeps the citation, and %s stands on your word alone."
	mint := func(args map[string]any) string {
		t.Helper()
		if _, err := engine.ExecuteAction(ctx, "verb", "commit", args); err != nil {
			t.Fatalf("the mint of %v: %v", args["id"], err)
		}
		notices, err := st.PageNotices()
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range notices {
			if strings.HasPrefix(n.ID, "ring1_") && strings.Contains(n.Content, args["id"].(string)) {
				return n.Content
			}
		}
		t.Fatalf("no Ring 1 notice names %v", args["id"])
		return ""
	}
	says := func(notice string, wants ...string) {
		t.Helper()
		for _, want := range wants {
			if !strings.Contains(notice, want) {
				t.Errorf("the notice does not say %q:\n%s", want, notice)
			}
		}
	}

	// The operator's own relationship, one line: named, and shown whole.
	own := mint(ring1Args("rel_notice_own1"))
	says(own, `their charter with you (name "Quinn", role "operator"; rel_notice_own1)`,
		`citing your words at turn`, `as your affirmation: "ok thanks".`,
		`They recorded: "Quinn is my operator; this is the working relationship."`,
		"the charter stands on your word alone")
	if strings.Contains(own, "begins") || strings.Contains(own, "in all") {
		t.Errorf("a text shown whole must not be said to be a part: %s", own)
	}
	whole("the operator's own relationship", own, fmt.Sprintf(`[ring 1] The identity recorded their charter with you (name "Quinn", role "operator"; rel_notice_own1), citing your words at turn %d as your affirmation: "ok thanks". They recorded: "Quinn is my operator; this is the working relationship.".`+closing, word.TurnSeq, "the charter"))

	// A peer's: not a charter with the operator, and the text is three
	// lines, so the first is shown as the first of three with the whole's
	// size and where it is read.
	text := "Rue and I trade drafts.\n\nWe answer within a day.\nNeither speaks for the other."
	peer := mint(map[string]any{
		"variant": "relationship.upsert", "id": "rel_notice_rue1", "counterpart_name": "Rue",
		"counterpart_role": "peer", "relationship_type": "peer", "charter_text": text,
	})
	says(peer, `a relationship with "Rue" (role "peer"; rel_notice_rue1)`, `as your affirmation: "ok thanks".`,
		fmt.Sprintf(`What they recorded begins: "Rue and I trade drafts." (the first line of 3, %d characters in all; the whole is record `, len([]rune(text))),
		" of the signed record).", "the relationship stands on your word alone")
	if strings.Contains(peer, "charter with you") {
		t.Errorf("a relationship with a peer was told to the operator as their own charter: %s", peer)
	}
	whole("a peer's relationship", peer, fmt.Sprintf(`[ring 1] The identity recorded a relationship with "Rue" (role "peer"; rel_notice_rue1), citing your words at turn %d as your affirmation: "ok thanks". What they recorded begins: "Rue and I trade drafts." (the first line of 3, %d characters in all; the whole is record %d of the signed record).`+closing, word.TurnSeq, len([]rune(text)), lg.LastSeq(), "the relationship"))

	// A relationship recorded with no text: said, with what stands.
	bare := mint(map[string]any{
		"variant": "relationship.upsert", "id": "rel_notice_bare1", "counterpart_name": "Sol",
		"counterpart_role": "peer", "relationship_type": "peer",
	})
	says(bare, `a relationship with "Sol" (role "peer"; rel_notice_bare1)`,
		"They recorded no text with it: any text this relationship already had stands.")
	whole("a relationship recorded with no text", bare, fmt.Sprintf(`[ring 1] The identity recorded a relationship with "Sol" (role "peer"; rel_notice_bare1), citing your words at turn %d as your affirmation: "ok thanks". They recorded no text with it: any text this relationship already had stands.`+closing, word.TurnSeq, "the relationship"))

	// One long line is cut by runes, marked, and said to be a part.
	long := strings.Repeat("é", 300)
	got := recordedText(long, "on the Identity page, under CHARTER — RING 1")
	if want := fmt.Sprintf("What they recorded begins: %q (the first 240 characters, 300 characters in all; the whole is on the Identity page, under CHARTER — RING 1).", strings.Repeat("é", 240)+"…"); got != want {
		t.Errorf("a long single line:\n got %s\nwant %s", got, want)
	}
	// A counterpart given no name is said so, in both forms.
	if n := ring1Notice(false, "peer", "", "rel_x", 7, 3, "yes", "", "t"); !strings.Contains(n, `a relationship with a counterpart they gave no name (role "peer"; rel_x)`) {
		t.Errorf("an unnamed peer: %s", n)
	}
	if n := ring1Notice(true, "operator", "", "rel_x", 7, 3, "yes", "", "t"); !strings.Contains(n, `their charter with you (no name given, role "operator"; rel_x)`) {
		t.Errorf("an unnamed operator: %s", n)
	}
	// The operator's own charter of more than one line: the first, said to
	// be the first of how many, and the page the whole is on.
	whole("the operator's charter of two lines", ring1Notice(true, "operator", "Quinn", "rel_x", 7, 3, "yes", "", "Quinn decides.\nI say what I see."),
		fmt.Sprintf(`[ring 1] The identity recorded their charter with you (name "Quinn", role "operator"; rel_x), citing your words at turn 3 as your affirmation: "yes". What they recorded begins: "Quinn decides." (the first line of 2, 32 characters in all; the whole is on the Identity page, under CHARTER — RING 1).`+closing, "the charter"))
	// A name is the identity's to write and is bounded as the cited words
	// are, the cut marked, in both forms.
	longName, cut := strings.Repeat("n", 300), strings.Repeat("n", ring1ExcerptRunes)+"…"
	if n := ring1Notice(false, "peer", longName, "rel_x", 7, 3, "yes", "", "t"); !strings.Contains(n, fmt.Sprintf(`a relationship with %q (role "peer"; rel_x)`, cut)) {
		t.Errorf("a peer's name of 300 characters is not cut at %d and marked: %s", ring1ExcerptRunes, n)
	}
	if n := ring1Notice(true, "operator", longName, "rel_x", 7, 3, "yes", "", "t"); !strings.Contains(n, fmt.Sprintf(`their charter with you (name %q, role "operator"; rel_x)`, cut)) {
		t.Errorf("the operator's name of 300 characters is not cut at %d and marked: %s", ring1ExcerptRunes, n)
	}
	// Text that is only spaces and line ends is still what was written, and
	// is shown as written.
	if got, want := recordedText(" \n ", "x"), `They recorded: " \n ".`; got != want {
		t.Errorf("a blank text:\n got %s\nwant %s", got, want)
	}
}

// relationshipUnread is a store whose read of a relationship fails.
type relationshipUnread struct{ *store.Store }

func (relationshipUnread) RelationshipAsHeld(string) (*store.Relationship, bool, error) {
	return nil, false, errors.New("the database is busy")
}

// WHOSE RELATIONSHIP IT WAS IS READ FROM THE RELATIONSHIP, NOT FROM THE
// ENTRY. A relationship keeps the role it was first given whatever a later
// entry names, so an entry naming "peer" under the operator's own id writes
// the operator's charter (the fixture of
// TestTheCharterCitesTheEventThatWroteIt in internal/app). The notice of it
// says it is their charter, with the role the relationship holds and the
// page the new text is on. Built from the entry it read "a relationship
// with ... (role "peer")" and sent them to the record.
func TestTheRing1NoticeOfAnEntryNamingAnotherRoleSaysWhoseRelationshipItIs(t *testing.T) {
	engine, st, lg, _, _ := setupEngine(t)
	ctx := context.Background()
	if err := engine.RecordConversationTurn("operator", "go ahead"); err != nil {
		t.Fatal(err)
	}
	mint := func(args map[string]any) string {
		t.Helper()
		if _, err := engine.ExecuteAction(ctx, "verb", "commit", args); err != nil {
			t.Fatalf("the mint of %v: %v", args["id"], err)
		}
		notices, err := st.PageNotices()
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range notices {
			if n.ID == fmt.Sprintf("ring1_%d", lg.LastSeq()) {
				return n.Content
			}
		}
		t.Fatalf("no notice of the Ring 1 mint at record %d", lg.LastSeq())
		return ""
	}
	mint(ring1Args("rel_notice_op01"))

	// The operator's own id, an entry that names a peer called Rue and
	// writes a new text: their charter is what changed.
	rewritten := mint(map[string]any{
		"variant": "relationship.upsert", "id": "rel_notice_op01", "counterpart_name": "Rue",
		"counterpart_role": "peer", "charter_text": "Rue decides what I work on.",
	})
	for _, want := range []string{
		`their charter with you (name "Rue", role "operator"; rel_notice_op01)`,
		`They recorded: "Rue decides what I work on."`, "the charter stands on your word alone",
	} {
		if !strings.Contains(rewritten, want) {
			t.Errorf("the notice does not say %q:\n%s", want, rewritten)
		}
	}
	if strings.Contains(rewritten, "a relationship with") || strings.Contains(rewritten, `role "peer"`) {
		t.Errorf("a rewrite of the operator's charter was told to them as someone else's relationship:\n%s", rewritten)
	}
	if rel, err := st.CurrentOperatorRelationship(); err != nil || rel == nil || rel.ID != "rel_notice_op01" || rel.CharterText != "Rue decides what I work on." {
		t.Fatalf("the fixture must leave Ring 1 holding the entry's text: %+v %v", rel, err)
	}

	// A relationship that cannot be read after the mint: the notice still
	// goes, says the name and the role are the entry's, and does not say
	// whose it is as if it knew.
	engine.store = relationshipUnread{st}
	unread := mint(map[string]any{
		"variant": "relationship.upsert", "id": "rel_notice_op01", "counterpart_name": "Rue",
		"counterpart_role": "peer", "charter_text": "Rue and I decide together.",
	})
	for _, want := range []string{
		`a relationship with "Rue" (role "peer"; rel_notice_op01)`,
		"The relationship as it now stands could not be read (the database is busy): the name and the role are the entry's own, and it may be your charter.",
		`They recorded: "Rue and I decide together."`,
	} {
		if !strings.Contains(unread, want) {
			t.Errorf("the notice of a mint whose relationship could not be read does not say %q:\n%s", want, unread)
		}
	}
	word, err := st.LatestOperatorWord()
	if err != nil || word == nil {
		t.Fatalf("the operator's word is not on record: %v", err)
	}
	if want := fmt.Sprintf(`[ring 1] The identity recorded a relationship with "Rue" (role "peer"; rel_notice_op01), citing your words at turn %d as your affirmation: "go ahead". The relationship as it now stands could not be read (the database is busy): the name and the role are the entry's own, and it may be your charter. They recorded: "Rue and I decide together.". If those words were not your affirmation, say so: the record keeps the citation, and the relationship stands on your word alone.`, word.TurnSeq); unread != want {
		t.Errorf("the notice of a mint whose relationship could not be read:\n got %s\nwant %s", unread, want)
	}

	// A read that answers that nothing is held under the id is the same
	// case: the notice says so in those words and still goes.
	engine.store = relationshipGone{st}
	gone := mint(map[string]any{
		"variant": "relationship.upsert", "id": "rel_notice_op01", "counterpart_name": "Rue",
		"counterpart_role": "peer", "charter_text": "Rue and I decide by turns.",
	})
	if want := "The relationship as it now stands could not be read (none is held under that id): the name and the role are the entry's own, and it may be your charter."; !strings.Contains(gone, want) || !strings.Contains(gone, `a relationship with "Rue" (role "peer"; rel_notice_op01)`) {
		t.Errorf("the notice of a mint whose relationship is not held does not say %q with the entry's own name and role:\n%s", want, gone)
	}
}

// relationshipGone is a store that answers that no relationship is held
// under an id.
type relationshipGone struct{ *store.Store }

func (relationshipGone) RelationshipAsHeld(string) (*store.Relationship, bool, error) {
	return nil, false, nil
}

// A RELATIONSHIP THE OPERATOR ONCE HAD IS NOT THEIR CHARTER NOW. A superseded
// operator's relationship keeps the role "operator", and an entry naming
// another role may still write to it. Ring 1 is the relationship that
// superseded it, so the notice of such an entry says the role the
// relationship holds, does not call it their charter, and sends them to the
// record for the whole, not to the page that shows the present charter.
func TestTheRing1NoticeOfAnEntryUnderASupersededOperatorsIdDoesNotCallItTheirCharter(t *testing.T) {
	engine, st, lg, _, _ := setupEngine(t)
	ctx := context.Background()
	if err := engine.RecordConversationTurn("operator", "go ahead"); err != nil {
		t.Fatal(err)
	}
	mint := func(args map[string]any) string {
		t.Helper()
		if _, err := engine.ExecuteAction(ctx, "verb", "commit", args); err != nil {
			t.Fatalf("the mint of %v: %v", args["id"], err)
		}
		notices, err := st.PageNotices()
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range notices {
			if n.ID == fmt.Sprintf("ring1_%d", lg.LastSeq()) {
				return n.Content
			}
		}
		t.Fatalf("no notice of the Ring 1 mint at record %d", lg.LastSeq())
		return ""
	}
	mint(ring1Args("rel_notice_was1"))
	successor := ring1Args("rel_notice_now1")
	successor["supersedes"] = "rel_notice_was1"
	mint(successor)

	old := mint(map[string]any{
		"variant": "relationship.upsert", "id": "rel_notice_was1", "counterpart_name": "Quinn",
		"counterpart_role": "peer", "charter_text": "What we agreed before.\nIt is kept for the history.",
	})
	for _, want := range []string{
		`a relationship with "Quinn" (role "operator"; rel_notice_was1)`,
		fmt.Sprintf("the whole is record %d of the signed record", lg.LastSeq()),
		"the relationship stands on your word alone",
	} {
		if !strings.Contains(old, want) {
			t.Errorf("the notice does not say %q:\n%s", want, old)
		}
	}
	if strings.Contains(old, "their charter with you") || strings.Contains(old, "CHARTER — RING 1") {
		t.Errorf("a superseded operator's relationship was told to the present operator as their charter:\n%s", old)
	}
	if rel, err := st.CurrentOperatorRelationship(); err != nil || rel == nil || rel.ID != "rel_notice_now1" || rel.CharterText != ring1Args("x")["charter_text"] {
		t.Fatalf("the fixture must leave Ring 1 with the successor, its text untouched: %+v %v", rel, err)
	}
}

func TestAWorkerSeatAuthorsNeitherRing1NorRing2(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)
	if err := engine.RecordConversationTurn("operator", "Yes — approved."); err != nil {
		t.Fatal(err)
	}
	worker := context.WithValue(context.Background(), SubagentWorkSession{}, "ws_worker")
	if _, err := engine.ExecuteAction(worker, "verb", "commit", ring1Args("rel_worker_seat1")); err == nil || !strings.Contains(err.Error(), "a worker seat cannot author Ring 1") {
		t.Fatalf("a worker seat's Ring 1 mint must be refused as the worker's: %v", err)
	}
	if _, err := engine.ExecuteAction(worker, "verb", "commit", map[string]any{"variant": "belief.promote", "id": "b_any", "ring": 2}); err == nil || !strings.Contains(err.Error(), "a worker seat cannot promote to Ring 2") {
		t.Fatalf("a worker seat's promotion to Ring 2 must be refused as the worker's: %v", err)
	}
	// The primary seat, with the same word on record, mints.
	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit", ring1Args("rel_worker_seat1")); err != nil {
		t.Fatalf("the primary seat's mint: %v", err)
	}
}

// The cut the result and the notice make is by runes, and marked.
func TestTheCitedExcerptIsCutByRunesAndMarked(t *testing.T) {
	if got := excerptOf("short", 240); got != "short" {
		t.Fatalf("a short excerpt is whole, got %q", got)
	}
	long := strings.Repeat("é", 300)
	got := excerptOf(long, 240)
	if len([]rune(got)) != 241 || !strings.HasSuffix(got, "…") || !strings.HasPrefix(got, strings.Repeat("é", 240)) {
		t.Fatalf("a long excerpt is its first 240 runes and the mark, got %d runes", len([]rune(got)))
	}
}
