package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// contactLines is a page's contacts change: every line, as the page sends it.
func contactLines(lines ...map[string]any) map[string]any {
	list := make([]any, 0, len(lines))
	for _, l := range lines {
		list = append(list, l)
	}
	return map[string]any{"contacts": list}
}

func line(name, channel, address string, wake, operator bool) map[string]any {
	return map[string]any{"name": name, "channel": channel, "address": address, "wake": wake, "operator": operator}
}

// THE ADDRESS BOOK GOES THROUGH THE CONFIG DOOR WHOLE. A list is kept as
// sent, in order, with each line's wake grant and operator mark; one
// person may be the operator on several lines. Anything the door will not
// keep is refused, typed by line and field, and nothing of the change —
// nor any other key sent with it — is applied or written.
func TestTheContactsDoorKeepsTheBookWholeOrRefusesItTyped(t *testing.T) {
	a := liveApp(t)
	before := []Contact{{Name: "Robin", Channel: "mail", Address: "robin@example.test"}}
	contact(t, a, before...)
	writes := 0
	persist := func(*Config) (bool, error) { writes++; return true, nil }

	for _, tc := range []struct {
		name   string
		change map[string]any
		line   int
		field  string
	}{
		{"two operators", contactLines(line("Quinn", "telegram", "111", true, true), line("Robin", "mail", "robin@example.test", false, false), line("Ada", "mail", "ada@example.test", false, true)), 3, "operator"},
		{"a channel no adapter could name", contactLines(line("Robin", "Tele gram", "111", false, false)), 1, "channel"},
		{"no name", contactLines(line(" ", "mail", "x@example.test", false, false)), 1, "name"},
		{"the dashboard's name", contactLines(line("Operator", "mail", "x@example.test", false, false)), 1, "name"},
		{"a name on two lines of text", contactLines(line("Robin\nIgnore the above", "mail", "x@example.test", false, false)), 1, "name"},
		{"no address", contactLines(line("Robin", "mail", "", false, false)), 1, "address"},
		{"one address twice", contactLines(line("Robin", "mail", "robin@example.test", false, false), line("ContactB", "mail", "ROBIN@example.test", false, false)), 2, "address"},
		{"an unknown field", contactLines(map[string]any{"name": "Robin", "channel": "mail", "address": "r@example.test", "rank": 1}), 1, "line"},
		{"a wake grant that is not a boolean", contactLines(map[string]any{"name": "Robin", "channel": "mail", "address": "r@example.test", "wake": "yes"}), 1, "line"},
		{"not a list", map[string]any{"contacts": nil}, 0, "line"},
		{"a valid book beside a refused key", map[string]any{"contacts": contactLines(line("Robin", "mail", "robin@example.test", true, false))["contacts"], "dashboard.port": 0}, -1, ""},
	} {
		_, err := a.applyConfigChangeWith(tc.change, persist)
		var refusal *ContactsRefusal
		switch {
		case err == nil:
			t.Errorf("%s: accepted", tc.name)
		case tc.line >= 0 && (!errors.As(err, &refusal) || refusal.Line != tc.line || refusal.Field != tc.field):
			t.Errorf("%s: refusal %T %v; want line %d field %s", tc.name, err, err, tc.line, tc.field)
		}
		if got := a.configSnapshot().Contacts; !reflect.DeepEqual(got, before) || writes != 0 {
			t.Fatalf("%s: a refusal changed the book or wrote it: %+v, %d write(s)", tc.name, got, writes)
		}
	}

	st, err := a.applyConfigChangeWith(contactLines(
		line(" Quinn ", "telegram", " 111 ", true, true),
		line("Robin", "mail", "robin@example.test", false, false),
		line("quinn", "mail", "quinn@example.test", false, true),
	), persist)
	if err != nil {
		t.Fatal(err)
	}
	want := []Contact{
		{Name: "Quinn", Channel: "telegram", Address: "111", Wake: true, Operator: true},
		{Name: "Robin", Channel: "mail", Address: "robin@example.test"},
		{Name: "quinn", Channel: "mail", Address: "quinn@example.test", Operator: true},
	}
	if got := a.configSnapshot().Contacts; !reflect.DeepEqual(got, want) || writes != 1 {
		t.Fatalf("kept %+v with %d write(s); want %+v once", got, writes, want)
	}
	if len(st.Contacts) != 3 || !st.Contacts[0].Operator || st.Contacts[0].Address != "111" || st.Contacts[1].Operator {
		t.Fatalf("the readback is not the book: %+v", st.Contacts)
	}
	if name, wake := a.whoIs("telegram", "111"); name != "Quinn" || !wake {
		t.Fatalf("the saved book is not in force: %q %v", name, wake)
	}

	if _, err := a.applyConfigChangeWith(map[string]any{"contacts": []any{}}, persist); err != nil {
		t.Fatal(err)
	}
	if got := a.configSnapshot(); len(got.Contacts) != 0 || !got.contactsPresent {
		t.Fatalf("an empty book is an explicit one: %+v present=%v", got.Contacts, got.contactsPresent)
	}
	if st := a.configState(); st.Contacts == nil {
		t.Fatal("an empty book reads back as null")
	}
}

// The file the door writes carries the mark on the lines that have it and
// on no other, so a book with nobody marked keeps the shape it had.
func TestAContactsSaveWritesTheMarkOnlyWhereItIs(t *testing.T) {
	a := liveApp(t)
	if _, err := a.applyConfigChange(contactLines(line("Quinn", "telegram", "111", true, true), line("Robin", "mail", "robin@example.test", false, false))); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(a.configSnapshot().SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Contacts []map[string]any `json:"contacts"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Contacts) != 2 || file.Contacts[0]["operator"] != true {
		t.Fatalf("the file's contacts: %v", file.Contacts)
	}
	if _, marked := file.Contacts[1]["operator"]; marked {
		t.Fatalf("an unmarked line was written with a mark: %v", file.Contacts[1])
	}
	var loaded Config // the one decoder a boot reads the file with
	if err := json.Unmarshal(raw, &loaded); err != nil {
		t.Fatal(err)
	}
	if !loaded.Contacts[0].Operator || loaded.Contacts[1].Operator {
		t.Fatalf("the mark did not survive a load: %+v", loaded.Contacts)
	}
}

// peerRow queues a message to a person and sets its delivery record as
// the test says.
func peerRow(t *testing.T, a *App, id, to string, set string, args ...any) {
	t.Helper()
	if err := a.store.AddOutboxMessage(id, "peer", to, "body of "+id, nil); err != nil {
		t.Fatal(err)
	}
	if set != "" {
		if _, err := a.store.DB().Exec(`UPDATE outbox SET `+set+` WHERE id = ?`, append(args, id)...); err != nil {
			t.Fatal(err)
		}
	}
}

func arrivalAt(t *testing.T, a *App, id, channel, address string, ms int64) {
	t.Helper()
	if _, err := a.store.DB().Exec(`INSERT INTO inbound (id, channel, address, body, received_ms) VALUES (?, ?, ?, ?, ?)`, id, channel, address, "words of "+id, ms); err != nil {
		t.Fatal(err)
	}
}

func activityIDs(row dashboard.SendRow) string {
	if row.Activity == nil {
		return "<none>"
	}
	var ids []string
	for _, m := range row.Activity.Latest {
		ids = append(ids, m.ID)
	}
	return fmt.Sprintf("%d:%s", row.Activity.Count, strings.Join(ids, ","))
}

func deliveredRow(t *testing.T, st dashboard.MessagesState, id string) dashboard.SendRow {
	t.Helper()
	for _, r := range st.Delivered {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("%s is not among the delivered", id)
	return dashboard.SendRow{}
}

// WROTE SINCE SENT COUNTS FROM THE DISPATCH CLAIM, INCLUSIVE (C4). Under
// each delivered send is every arrival from any of its person's lines
// received at or after the delivering attempt's claim: one that came
// before the local acknowledgement was stamped counts, one that came
// after the message was written but before it left does not, and two
// sends to the same person both show what came after both. A stranger,
// another person and a relayed event are nobody's activity here. Where
// the claim or the person's lines are gone, the page says it cannot tell.
func TestWroteSinceSentCountsFromTheDispatchClaim(t *testing.T) {
	a := liveApp(t)
	contact(t, a,
		Contact{Name: "Robin", Channel: "telegram", Address: "111"},
		Contact{Name: "Robin", Channel: "mail", Address: "robin@example.test"},
		Contact{Name: "ContactB", Channel: "telegram", Address: "222"})
	const deliveredSet = `delivered = 1, effect = 'performed', delivered_via = 'org.example.telegram', attempts = 1, created_ms = ?, dispatched_ms = ?, last_attempt_ms = ?, delivered_at = ?`
	stamp := func(ms int64) string { return time.UnixMilli(ms).UTC().Format(time.RFC3339Nano) }
	peerRow(t, a, "msg_a", "Robin", deliveredSet, 1000, 2000, 2100, stamp(2100))
	peerRow(t, a, "msg_b", "robin", deliveredSet, 1500, 3000, 3100, stamp(3100))
	peerRow(t, a, "msg_contact_b", "ContactB", deliveredSet, 2300, 2400, 2450, stamp(2450))
	peerRow(t, a, "msg_legacy", "Robin", `delivered = 1, effect = 'performed', delivered_via = 'dashboard', attempts = 1, last_attempt_ms = 900`)
	peerRow(t, a, "msg_gone", "Kim", deliveredSet, 100, 200, 300, stamp(300))

	arrivalAt(t, a, "r0_before_dispatch", "telegram", "111", 1800)
	arrivalAt(t, a, "r1_at_dispatch", "telegram", "111", 2000)
	arrivalAt(t, a, "r2_before_ack", "mail", "Robin@Example.test", 2050)
	arrivalAt(t, a, "r3_contact_b", "telegram", "222", 2500)
	arrivalAt(t, a, "r4_after_both", "telegram", "111", 3050)
	arrivalAt(t, a, "r5_relayed", "hook:org.example.relay", "111", 3200)
	arrivalAt(t, a, "r6_stranger", "telegram", "999", 3300)

	st, err := a.messagesPage(t.Context(), dashboard.MessagesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{
		"msg_a":         "3:r4_after_both,r2_before_ack,r1_at_dispatch",
		"msg_b":         "1:r4_after_both",
		"msg_contact_b": "1:r3_contact_b",
	} {
		if got := activityIDs(deliveredRow(t, st, id)); got != want {
			t.Errorf("%s wrote since sent: %s; want %s", id, got, want)
		}
	}
	for id, why := range map[string]string{"msg_legacy": "was not recorded", "msg_gone": "not in your contacts"} {
		act := deliveredRow(t, st, id).Activity
		if act == nil || act.Count != 0 || !strings.Contains(act.Unavailable, why) {
			t.Errorf("%s: %+v; want it said that activity cannot be told (%s)", id, act, why)
		}
	}
	for _, m := range deliveredRow(t, st, "msg_a").Activity.Latest {
		if m.From != "Robin" {
			t.Errorf("activity %s is not named as Robin's: %q", m.ID, m.From)
		}
	}
}

// A REPLY THAT COMES WHILE THE ADAPTER IS STILL ANSWERING IS COUNTED. The
// send is claimed, the person's answer arrives before the adapter returns
// and before the delivery is stamped, and the page counts it under the
// send — through the real walk, not stamped rows.
func TestAReplyBeforeTheLocalAcknowledgementIsCounted(t *testing.T) {
	a := liveApp(t)
	contact(t, a, Contact{Name: "Robin", Channel: "telegram", Address: "111"})
	installScriptedAdapter(t, a, "org.example.telegram", "telegram", func(context.Context) (tools.Result, error) {
		if _, err := a.store.RecordInbound("fast_reply", "telegram", "111", "got it"); err != nil {
			return tools.Result{}, err
		}
		time.Sleep(5 * time.Millisecond) // the stamp is strictly later than the reply
		return tools.Result{Output: `{"ok":true}`}, nil
	})
	if err := a.store.AddOutboxMessage("msg_fast", "peer", "Robin", "are you there?", nil); err != nil {
		t.Fatal(err)
	}
	if n, err := a.deliverOutbox(t.Context()); err != nil || n != 1 {
		t.Fatalf("delivered %d (%v)", n, err)
	}
	st, err := a.messagesPage(t.Context(), dashboard.MessagesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	row := deliveredRow(t, st, "msg_fast")
	if got := activityIDs(row); got != "1:fast_reply" {
		t.Fatalf("wrote since sent: %s; want the reply that came before the acknowledgement", got)
	}
	if row.Via != "org.example.telegram" || row.SentMs == 0 {
		t.Fatalf("delivered row: %+v", row)
	}
}

// EACH SEND IS SHOWN IN ITS STATE, with the host's account beside the
// adapter's own words: waiting (and why, derived as the identity is told),
// refused and still queued, being sent, parked, and of unknown effect.
func TestTheMessagesPageShowsEachSendInItsState(t *testing.T) {
	a := liveApp(t)
	contact(t, a, Contact{Name: "Robin", Channel: "telegram", Address: "111"})
	installScriptedAdapter(t, a, "org.example.telegram", "telegram", func(context.Context) (tools.Result, error) {
		return tools.Result{Output: `{"ok":true}`}, nil
	})
	peerRow(t, a, "w_not_tried", "Robin", "created_ms = 1")
	peerRow(t, a, "w_no_contact", "Kim", "created_ms = 2")
	peerRow(t, a, "w_refused", "Robin", "created_ms = 3, attempts = 2, last_attempt_ms = 50, dispatched_ms = 50, last_error = 'telegram(<b>429</b>)'")
	peerRow(t, a, "s_in_flight", "Robin", "created_ms = 4, attempts = 1, last_attempt_ms = 60, dispatched_ms = 70")
	peerRow(t, a, "p_parked", "Robin", "created_ms = 5, attempts = 8, parked = 1, last_attempt_ms = 80, dispatched_ms = 80, last_error = 'telegram(address refused)'")
	peerRow(t, a, "u_unknown", "Robin", "created_ms = 6, attempts = 1, parked = 1, effect = 'unknown', delivered_via = 'org.example.telegram', last_attempt_ms = 90, dispatched_ms = 90, last_error = 'NET_EFFECT_UNKNOWN'")
	peerRow(t, a, "u_stopped", "Robin", "created_ms = 7, attempts = 1, parked = 1, effect = 'unknown', last_attempt_ms = 95, dispatched_ms = 95, last_error = 'the process stopped'")

	st, err := a.messagesPage(t.Context(), dashboard.MessagesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]dashboard.SendRow{}
	var order []string
	for _, r := range st.Open {
		got[r.ID] = r
		order = append(order, r.ID)
	}
	if strings.Join(order, ",") != "w_not_tried,w_no_contact,w_refused,s_in_flight,p_parked,u_unknown,u_stopped" || st.OpenTotal != 7 {
		t.Fatalf("open rows %v of %d", order, st.OpenTotal)
	}
	for id, want := range map[string]struct{ state, note, answer string }{
		"w_not_tried":  {"waiting", "Queued; not yet tried.", ""},
		"w_no_contact": {"waiting", "Kim is not in your contacts", ""},
		"w_refused":    {"waiting", "Refused 2 time(s); still queued, and parked after 8.", "telegram(<b>429</b>)"},
		"s_in_flight":  {"sending", "its answer is not in yet", ""},
		"p_parked":     {"parked", "Not delivered after 8 attempt(s)", "telegram(address refused)"},
		"u_unknown":    {"unknown", "It may have left via org.example.telegram", "NET_EFFECT_UNKNOWN"},
		"u_stopped":    {"unknown", "The process stopped while it was being sent", "the process stopped"},
	} {
		r := got[id]
		if r.State != want.state || !strings.Contains(r.Note, want.note) || r.Answer != want.answer {
			t.Errorf("%s: state %q note %q answer %q; want %q, a note with %q, answer %q", id, r.State, r.Note, r.Answer, want.state, want.note, want.answer)
		}
	}
	if len(st.Channels) != 1 || st.Channels[0] != "telegram" {
		t.Fatalf("installed channels: %v", st.Channels)
	}
	a.enterSafe("test: the record is frozen")
	st, err = a.messagesPage(t.Context(), dashboard.MessagesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st.Open[0].Note, "nothing leaves in safe mode") {
		t.Fatalf("under SAFE a waiting send says: %q", st.Open[0].Note)
	}
}

// WHETHER AN ARRIVAL WOKE THE IDENTITY, through the paths that record it:
// a contact granted wake starts a turn; a message that comes while a turn
// runs joins it; one from a contact without the grant waits for the next
// turn to carry it; and one nobody has read yet says so.
func TestTheMessagesPageSaysWhetherAnArrivalWokeTheIdentity(t *testing.T) {
	a, _ := promptCacheApp(t)
	contact(t, a,
		Contact{Name: "Robin", Channel: "mail", Address: "robin@example.test", Wake: true},
		Contact{Name: "ContactB", Channel: "mail", Address: "contact-b@example.test"})

	if _, err := a.store.RecordInbound("in_woke", "mail", "robin@example.test", "wake up"); err != nil {
		t.Fatal(err)
	}
	a.carryInbound("in_woke", channelRoute{Channel: "mail"}, arrival{ID: "woke", From: "robin@example.test", Body: "wake up"})
	waitUntil(t, func() bool { return !a.TurnActive() }, "the arrival's wake turn to finish")

	if _, err := a.store.RecordInbound("in_later", "mail", "contact-b@example.test", "no hurry"); err != nil {
		t.Fatal(err)
	}
	a.carryInbound("in_later", channelRoute{Channel: "mail"}, arrival{ID: "later", From: "contact-b@example.test", Body: "no hurry"})
	if a.TurnActive() {
		t.Fatal("a contact without the wake grant started a turn")
	}

	steered := make(chan error, 1)
	fixtureModel(t, a, func(n int, w http.ResponseWriter) {
		if n == 1 {
			if _, err := a.store.RecordInbound("in_steered", "mail", "contact-b@example.test", "and another thing"); err != nil {
				steered <- err
			} else {
				ok, err := a.steer(roleParticipant, "and another thing", nil, context.WithValue(t.Context(), inboundRecordKey{}, "in_steered"))
				if err == nil && !ok {
					err = fmt.Errorf("the steer was not queued")
				}
				steered <- err
			}
			fmt.Fprint(w, callReply("", toolCall("r1", "ls", `{"path":"."}`)))
			return
		}
		fmt.Fprint(w, textReply("noted"))
	})
	operatorTurn(t, a, "anything new?")
	if err := <-steered; err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.RecordInbound("in_unread", "mail", "stranger@example.test", "<script>alert(1)</script>"); err != nil {
		t.Fatal(err)
	}

	st, err := a.messagesPage(t.Context(), dashboard.MessagesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]dashboard.ArrivalRow{}
	for _, r := range st.Arrivals {
		got[r.ID] = r
	}
	for id, want := range map[string]struct {
		from       string
		woke, read bool
	}{
		"in_woke":    {"Robin", true, true},
		"in_later":   {"ContactB", false, true},
		"in_steered": {"ContactB", false, true},
		"in_unread":  {"", false, false},
	} {
		r, ok := got[id]
		if !ok || r.From != want.from || r.Woke != want.woke || r.Read != want.read {
			t.Errorf("%s: from %q woke=%v read=%v (listed %v); want from %q woke=%v read=%v", id, r.From, r.Woke, r.Read, ok, want.from, want.woke, want.read)
		}
	}
	if st.ArrivalsTotal != 4 || got["in_unread"].Body != "<script>alert(1)</script>" {
		t.Fatalf("total %d; the stranger's words must come back as they were written: %q", st.ArrivalsTotal, got["in_unread"].Body)
	}
}
