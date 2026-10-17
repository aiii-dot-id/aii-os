//go:build !windows

package app

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// SETTINGS → MESSAGES, END TO END: a born identity's own outbox and
// arrivals, read over the live socket by a real Chrome at the page's
// address; what each send's person wrote since it left counted from the
// dispatch claim and labelled activity; and the address book edited in
// the page and kept — or refused — by the configuration door itself.
//
// COVERAGE LIMIT, NAMED: Blink only, as the projects journey; the page's
// rendering in Gecko is the dashboard's fixture test's.
func TestTheMessagesJourneyInARealBrowser(t *testing.T) {
	chrome := needChrome(t)
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Journey")
	a := New(safebootConfig(t, dir, "Journey", keyPath, ledgerPath, dbPath))
	if err := startLiveForTest(a); err != nil {
		t.Fatalf("startLive: %v", err)
	}
	t.Cleanup(func() { stopApp(t, a) })
	if _, err := a.applyConfigChange(contactLines(
		line("Robin", "telegram", "111", true, false),
		line("Robin", "mail", "robin@example.test", false, false),
		line("Quinn", "telegram", "42", false, false),
	)); err != nil {
		t.Fatal(err)
	}
	const delivered = `delivered = 1, effect = 'performed', delivered_via = 'org.example.telegram', attempts = 1, created_ms = ?, dispatched_ms = ?, last_attempt_ms = ?, delivered_at = ?`
	stamp := func(ms int64) string { return time.UnixMilli(ms).UTC().Format(time.RFC3339Nano) }
	peerRow(t, a, "msg_first", "Robin", delivered, 1000, 2000, 2100, stamp(2100))
	peerRow(t, a, "msg_second", "Robin", delivered, 1500, 3000, 3100, stamp(3100))
	peerRow(t, a, "msg_kim", "Kim", "created_ms = 10")
	peerRow(t, a, "msg_parked", "Robin", "created_ms = 11, attempts = 8, parked = 1, last_attempt_ms = 80, dispatched_ms = 80, last_error = 'telegram(address refused)'")
	peerRow(t, a, "msg_unknown", "Robin", "created_ms = 12, attempts = 1, parked = 1, effect = 'unknown', delivered_via = 'org.example.telegram', last_attempt_ms = 90, dispatched_ms = 90, last_error = 'NET_EFFECT_UNKNOWN'")
	arrivalAt(t, a, "in_before", "telegram", "111", 1800)
	arrivalAt(t, a, "in_early_reply", "mail", "robin@example.test", 2050)
	arrivalAt(t, a, "in_unrelated", "telegram", "111", 2500)
	arrivalAt(t, a, "in_after_both", "telegram", "111", 3050)
	if _, err := a.store.DB().Exec(`INSERT INTO inbound (id, channel, address, body, received_ms) VALUES ('in_hostile', 'telegram', '<b>999</b>', '<img src=x onerror="document.title=''owned''">', 3300)`); err != nil {
		t.Fatal(err)
	}

	origin := a.dashboard.Origin()
	page := openInChrome(t, chrome, origin, "/#/settings/messages")
	await := func(what, predicate string) string {
		t.Helper()
		got, err := page.evalRetry(awaitJS(predicate, 20000), true, 30*time.Second, 5)
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		if strings.HasPrefix(got, "TIMEOUT") {
			t.Fatalf("%s: %s", what, got)
		}
		return got
	}

	// ---- Rows by state, and what came since each delivered send.
	await("the lists", `
    const s = document.getElementById('settings-stack');
    const has = sel => !!s.querySelector(sel);
    const act = (send, id) => has('[data-send="' + send + '"] [data-activity-item="' + id + '"]');
    last = 'stack: ' + (s.textContent || '').slice(0, 400);
    if (!has('[data-send-state="waiting"] [data-send="msg_kim"]') || !has('[data-send-state="parked"] [data-send="msg_parked"]') ||
        !has('[data-send-state="unknown"] [data-send="msg_unknown"]')) return null;
    last = 'first: ' + s.querySelector('[data-send="msg_first"] [data-activity]')?.textContent;
    if (!act('msg_first', 'in_early_reply') || !act('msg_first', 'in_unrelated') || !act('msg_first', 'in_after_both') || act('msg_first', 'in_before')) return null;
    if (!act('msg_second', 'in_after_both') || act('msg_second', 'in_early_reply')) return null;
    const first = s.querySelector('[data-send="msg_first"] [data-activity]').textContent;
    if (!first.includes('3 messages from Robin') || !first.includes('This is activity, not an answer')) return null;
    if (!s.querySelector('[data-send="msg_kim"]').textContent.includes('Kim is not in your contacts')) return null;
    return 'ok';
  `)
	await("untrusted text stays text", `
    const s = document.getElementById('settings-stack');
    const row = s.querySelector('[data-arrival="in_hostile"]');
    if (!row) return null;
    last = row.innerHTML;
    if (s.querySelector('img') || row.querySelector('b') || document.title === 'owned') return 'FAIL markup: ' + last;
    if (!row.textContent.includes('<img src=x') || !row.textContent.includes('Not in your contacts: <b>999</b>')) return null;
    const words = s.querySelector('[data-delivered-sends]').textContent + s.querySelector('[data-open-sends]').textContent;
    return /repl/i.test(words) ? 'FAIL reply: ' + words : 'ok';
  `)

	// ---- The book, edited on the page and kept by the door.
	if _, err := page.eval(`(() => {
    const s = document.getElementById('settings-stack');
    const line = s.querySelector('[data-contact-line="2"]');
    line.querySelector('[data-contact-field="address"]').value = '4242';
    const me = line.querySelector('[data-contact-field="operator"]');
    me.checked = true;
    me.dispatchEvent(new Event('input', { bubbles: true }));
    s.querySelector('[data-save="contacts"]').click();
    return 'saved';
  })()`, false, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	await("the door's answer", `
    const said = document.querySelector('#settings-stack [data-said="contacts"]');
    last = said ? said.textContent : 'no answer yet';
    return said && said.textContent === 'Saved — the contacts are in force now.' ? 'ok' : null;
  `)
	got := a.configSnapshot().Contacts
	if len(got) != 3 || !got[2].Operator || got[2].Address != "4242" || got[0].Operator || !got[0].Wake {
		t.Fatalf("the door kept %+v", got)
	}
	raw, err := os.ReadFile(a.configSnapshot().SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Contacts []Contact `json:"contacts"`
	}
	if err := json.Unmarshal(raw, &file); err != nil || len(file.Contacts) != 3 || !file.Contacts[2].Operator {
		t.Fatalf("the file holds %+v (%v)", file.Contacts, err)
	}

	// ---- Two people marked as the operator: the door refuses, the page
	// says so beside Save, and nothing changes.
	if _, err := page.eval(`(() => {
    const s = document.getElementById('settings-stack');
    const me = s.querySelector('[data-contact-line="0"] [data-contact-field="operator"]');
    me.checked = true;
    me.dispatchEvent(new Event('input', { bubbles: true }));
    s.querySelector('[data-save="contacts"]').click();
    return 'saved';
  })()`, false, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	await("the door's refusal", `
    const s = document.getElementById('settings-stack');
    const said = s.querySelector('[data-said="contacts"]');
    last = said ? said.textContent : 'no answer yet';
    const kept = s.querySelector('[data-contact-line="0"] [data-contact-field="operator"]').checked;
    return said && said.textContent.includes('the operator is one person') && kept ? 'ok' : null;
  `)
	if got := a.configSnapshot().Contacts; got[0].Operator || !got[2].Operator {
		t.Fatalf("a refused book changed the contacts: %+v", got)
	}
}
