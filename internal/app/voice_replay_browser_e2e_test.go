//go:build !windows

package app

import (
	"strings"
	"testing"
	"time"
)

// THE PAGE DRAWS A VOICE'S ROW AS ITS WORDS. On the real page, from a born identity's own
// store: a participant's row reloaded shows the words alone, under the label it was heard
// under, with no frame, sentinel or engine's note. Once its voice is named it is drawn as You.
// Another voice's row stays someone else's, and what was typed or written with the same
// beginning is drawn as it was written. The record does not move.
func TestThePageDrawsAVoiceRowAsItsWords(t *testing.T) {
	chrome := needChrome(t)
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Replay")
	cfg := safebootConfig(t, dir, "Replay", keyPath, ledgerPath, dbPath)
	cfg.Dashboard.Port = 0
	a := New(cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatalf("startLive: %v", err)
	}
	t.Cleanup(func() { stopApp(t, a) })
	first := voiceSays(t, a, 7, voiceOne, "the harbour at dawn")
	voiceSays(t, a, 8, voiceTwo, "the mountain at noon")
	if _, err := a.store.AddConversationTurnSeq(roleOperator, voiceMarker+"typed by hand"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.AddConversationTurnSeq("resident", voiceMarker+"my own reply"); err != nil {
		t.Fatal(err)
	}
	turn, err := a.store.GetTurnBySeq(first)
	if err != nil || turn == nil {
		t.Fatalf("turn: %+v %v", turn, err)
	}
	held := turn.Role + "\n" + turn.Content

	page := openInChrome(t, chrome, a.dashboard.Origin(), "/#/chat")
	rows := func(words string) string {
		t.Helper()
		got, err := page.evalRetry(awaitJS(`
    const msgs = [...document.querySelectorAll('.msg.record-message')];
    last = 'rows: ' + msgs.map(m => m.className + ' | ' + m.textContent).join(' || ');
    const m = msgs.find(el => el.querySelector('.body') && el.querySelector('.body').textContent.includes('`+words+`'));
    return m ? m.className + ' | ' + m.querySelector('.who').textContent + ' | ' + m.querySelector('.body').textContent : null;
  `, 20000), true, 30*time.Second, 5)
		if err != nil {
			t.Fatalf("the row of %q: %v", words, err)
		}
		if strings.HasPrefix(got, "TIMEOUT") {
			t.Fatalf("no row of %q: %s", words, got)
		}
		return got
	}
	clean := func(row string) {
		t.Helper()
		for _, gone := range []string{"authority", "EXTERNAL_UNTRUSTED", "Someone spoke", "[voice"} {
			if strings.Contains(row, gone) {
				t.Errorf("the page draws %q: %s", gone, row)
			}
		}
	}
	mine := rows("the harbour at dawn")
	clean(mine)
	if !strings.Contains(mine, "participant") || !strings.HasSuffix(mine, `| participant · "Same label" | the harbour at dawn`) {
		t.Errorf("before any name the row reads %s", mine)
	}
	// What was typed, and what the identity wrote, are drawn as they were written, whatever
	// they begin with.
	if got := rows("[voice] typed by hand"); !strings.HasSuffix(got, "| You | [voice] typed by hand") {
		t.Errorf("a typed row with the marker reads %s", got)
	}
	if got := rows("[voice] my own reply"); !strings.HasSuffix(got, "| Replay | [voice] my own reply") {
		t.Errorf("a reply with the marker reads %s", got)
	}
	other := rows("the mountain at noon")
	clean(other)

	if _, err := a.applyConfigChange(map[string]any{"speech.operator_speakers": []any{voiceOne}}); err != nil {
		t.Fatal(err)
	}
	if _, err := page.eval(`location.reload(); 'reloaded'`, false, 10*time.Second); err != nil {
		t.Fatalf("reload: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	named := rows("the harbour at dawn")
	clean(named)
	if !strings.Contains(named, "operator") || !strings.Contains(named, "| You | the harbour at dawn") {
		t.Errorf("a named voice's row reads %s", named)
	}
	if got := rows("the mountain at noon"); got != other {
		t.Errorf("another voice's row moved: %s, was %s", got, other)
	}
	if turn, err = a.store.GetTurnBySeq(first); err != nil || turn.Role+"\n"+turn.Content != held {
		t.Fatalf("the record moved: %+v %v", turn, err)
	}
}

// EVERY OPEN SCREEN FOLLOWS THE NAME. Two screens hold a voice's row, and neither reloads. A
// name given or taken back in Settings reaches both: the row's whose it is, what it says above
// its words, and whether it can be copied, on the element each screen already holds.
func TestEveryOpenScreenFollowsTheName(t *testing.T) {
	chrome := needChrome(t)
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Replay")
	cfg := safebootConfig(t, dir, "Replay", keyPath, ledgerPath, dbPath)
	cfg.Dashboard.Port = 0
	a := New(cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatalf("startLive: %v", err)
	}
	t.Cleanup(func() { stopApp(t, a) })
	voiceSays(t, a, 7, voiceOne, "the harbour at dawn")

	const (
		unnamed = `drawn as participant | participant · "Same label" | the harbour at dawn | no copy`
		named   = `drawn as operator | You | the harbour at dawn | copies the harbour at dawn`
	)
	screens := []*cdp{openInChrome(t, chrome, a.dashboard.Origin(), "/#/chat"), openInChrome(t, chrome, a.dashboard.Origin(), "/#/chat")}
	reads := func(want string) {
		t.Helper()
		for i, page := range screens {
			got, err := page.evalRetry(awaitJS(`
    const m = [...document.querySelectorAll('.msg.record-message')].find(el => el.querySelector('.body').textContent.includes('the harbour at dawn'));
    if (!m) { last = 'no row'; return null; }
    const copy = m.querySelector('.copyb');
    const row = 'drawn as ' + ['operator', 'participant'].filter(c => m.classList.contains(c)).join(' and ') + ' | ' + m.querySelector('.who').textContent + ' | ' + m.querySelector('.body').textContent + ' | ' + (copy ? 'copies ' + copy.dataset.copyText : 'no copy');
    last = row;
    return row === `+"`"+want+"`"+` ? row : null;
  `, 20000), true, 30*time.Second, 5)
			if err != nil {
				t.Fatalf("screen %d: %v", i+1, err)
			}
			if strings.HasPrefix(got, "TIMEOUT") {
				t.Errorf("screen %d: want %s; %s", i+1, want, got)
			}
		}
	}
	reads(unnamed)
	for i, page := range screens {
		if _, err := page.eval(`window.heldSinceOpening = true; 'held'`, false, 10*time.Second); err != nil {
			t.Fatalf("screen %d: %v", i+1, err)
		}
	}
	name := func(voices ...any) {
		t.Helper()
		if _, err := a.applyConfigChange(map[string]any{"speech.operator_speakers": append([]any{}, voices...)}); err != nil {
			t.Fatal(err)
		}
	}
	name(voiceOne)
	reads(named)
	name()
	reads(unnamed)
	name(voiceOne)
	reads(named)
	for i, page := range screens {
		got, err := page.eval(`String(window.heldSinceOpening)`, false, 10*time.Second)
		if err != nil || got != "true" {
			t.Errorf("screen %d reloaded: %q %v", i+1, got, err)
		}
	}
}

// A LONG VOICE ROW IS DRAWN AS ITS WORDS. On the real page a row too long to arrive whole is read
// through its reference, and shows the words alone, named or not, and copies the words.
func TestALongVoiceRowIsDrawnAsItsWords(t *testing.T) {
	chrome := needChrome(t)
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Replay")
	cfg := safebootConfig(t, dir, "Replay", keyPath, ledgerPath, dbPath)
	cfg.Dashboard.Port = 0
	a := New(cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatalf("startLive: %v", err)
	}
	t.Cleanup(func() { stopApp(t, a) })
	words := strings.TrimSpace(strings.Repeat("the harbour at dawn ", 500))
	voiceSays(t, a, 7, voiceOne, words)
	page := openInChrome(t, chrome, a.dashboard.Origin(), "/#/chat")
	reads := func(want string) {
		t.Helper()
		got, err := page.evalRetry(awaitJS(`
    const m = document.querySelector('.msg.record-message');
    if (!m) { last = 'no row'; return null; }
    const copy = m.querySelector('.copyb'), body = m.querySelector('.body').textContent;
    const row = m.querySelector('.who').textContent + ' | ' + (body === `+"`"+words+"`"+` ? 'the words' : body.slice(0, 80)) + ' | ' + (copy ? copy.hidden ? 'copy hidden' : copy.dataset.copyText === `+"`"+words+"`"+` ? 'copies the words' : 'copies ' + copy.dataset.copyText.slice(0, 80) : 'no copy');
    last = row;
    return row === `+"`"+want+"`"+` ? row : null;
  `, 20000), true, 30*time.Second, 5)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(got, "TIMEOUT") {
			t.Errorf("want %s; %s", want, got)
		}
	}
	reads("participant | the words | no copy")
	if _, err := a.applyConfigChange(map[string]any{"speech.operator_speakers": []any{voiceOne}}); err != nil {
		t.Fatal(err)
	}
	reads("You | the words | copies the words")
}
