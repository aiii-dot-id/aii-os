//go:build !windows

package app

import (
	"math"
	"os"
	"strings"
	"testing"
	"time"
)

// THE COGNITION CARD, END TO END, IN A REAL BROWSER: a born
// identity's own dashboard in Chrome, opened at Technical → Reflection. The
// card shows what the host holds — a batch set on the host, the defaults
// of everything else with the host's bounds. A changed number saved there
// is kept by the host, written to config.json and in force at once; a
// number outside its bounds is refused, the door's words with the bounds
// standing beside the card's Save, and the host keeps what it had; and
// the brief turned off is off in the host, its alarm's row still its own.
//
// COVERAGE LIMIT, NAMED: Blink only, as the other journeys; the card's
// rendering in Gecko is the dashboard's fixture test's.
func TestTheCognitionCardInARealBrowser(t *testing.T) {
	chrome := needChrome(t)
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Journey")
	a := New(safebootConfig(t, dir, "Journey", keyPath, ledgerPath, dbPath))
	if err := startLiveForTest(a); err != nil {
		t.Fatalf("startLive: %v", err)
	}
	t.Cleanup(func() { stopApp(t, a) })
	if _, err := a.applyConfigChange(map[string]any{"cognition.consolidate_batch": float64(25)}); err != nil {
		t.Fatal(err)
	}

	page := openInChrome(t, chrome, a.dashboard.Origin(), "/#/settings/technical/reflection")
	await := func(what, predicate string) {
		t.Helper()
		got, err := page.evalRetry(awaitJS(predicate, 20000), true, 30*time.Second, 5)
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		if got != "ok" {
			t.Fatalf("%s: %s", what, got)
		}
	}
	run := func(js string) {
		t.Helper()
		if _, err := page.eval(`(() => { `+js+` ; return 'done'; })()`, false, 10*time.Second); err != nil {
			t.Fatal(err)
		}
	}
	const field = `const field = key => document.querySelector('[data-technical-form="cognition"] [data-cog-key="' + key + '"]');`

	await("the card shows what the host holds", field+`
    const card = document.querySelector('[data-technical-form="cognition"]');
    last = card ? card.textContent.slice(0, 200) : 'no card';
    if (!card || !field('cognition.consolidate_batch')) return null;
    const batch = field('cognition.consolidate_batch'), ops = field('cognition.consolidate_max_ops'), spacing = field('cognition.consolidate_spacing_seconds');
    last = 'batch=' + batch.value + ' ops=' + ops.value + '/' + ops.placeholder + ' spacing=' + spacing.placeholder;
    if (batch.value !== '25' || batch.min !== '1' || batch.max !== '200') return null;
    if (ops.value !== '' || !/ 32$/.test(ops.placeholder) || !/ 30$/.test(spacing.placeholder)) return null;
    return document.getElementById('cog-brief-at').placeholder === '07:00' ? 'ok' : null;`)

	// A change the host keeps, writes and puts in force.
	run(field + `
    const b = field('cognition.consolidate_batch'); b.value = '12'; b.dispatchEvent(new Event('input', { bubbles: true }));
    const s = field('cognition.consolidate_spacing_seconds'); s.value = '40'; s.dispatchEvent(new Event('input', { bubbles: true }));
    document.querySelector('[data-save="technical"]').click()`)
	await("the save is said beside the card's Save", `
    const said = document.querySelector('[data-said="technical"]');
    last = said ? said.textContent : 'nothing said';
    return said && /next pass/.test(said.textContent) ? 'ok' : null;`)
	waitUntil(t, func() bool {
		c := a.configSnapshot()
		return c.Cognition.ConsolidateBatch == 12 && c.Cognition.ConsolidateSpacingSeconds == 2400
	}, "the host kept the batch and the spacing")
	raw, err := os.ReadFile(a.configSnapshot().SourcePath)
	if err != nil || !strings.Contains(string(raw), `"consolidate_batch": 12`) || !strings.Contains(string(raw), `"consolidate_spacing_seconds": 2400`) {
		t.Fatalf("config.json does not carry the card's change (%v):\n%s", err, raw)
	}
	if got := a.cognition.rhythm.Load().rhythm.Spacing(); got != 40*time.Minute {
		t.Fatalf("the spacing saved on the card is not in force: %s", got)
	}

	// A number outside its bounds: refused, with them, and nothing moves.
	run(field + `
    const b = field('cognition.consolidate_batch'); b.value = '500'; b.dispatchEvent(new Event('input', { bubbles: true }));
    document.querySelector('[data-save="technical"]').click()`)
	await("the field says its bounds, and the Save stops there", `
    const said = document.querySelector('[data-cog-key="cognition.consolidate_batch"]').closest('.technical-form__field').querySelector('[data-range-said]');
    last = said.textContent;
    return said.textContent === 'Enter a number from 1 to 200.' ? 'ok' : null;`)
	if got := a.configSnapshot().Cognition.ConsolidateBatch; got != 12 {
		t.Fatalf("a refused batch moved the host's: %d", got)
	}

	// No brief: its alarm stands, and holds its name, and writes nothing.
	run(field + `
    const b = field('cognition.consolidate_batch'); b.value = '12'; b.dispatchEvent(new Event('input', { bubbles: true }));
    document.getElementById('cog-brief-off').click();
    document.querySelector('[data-save="technical"]').click()`)
	waitUntil(t, func() bool { return a.configSnapshot().Cognition.MorningBriefAt == "off" }, "the host kept the brief off")
	rows, err := a.store.DueAlarms("wall", math.MaxInt64, 100)
	if err != nil {
		t.Fatal(err)
	}
	held := false
	for _, r := range rows {
		held = held || (r.AlarmID == "morning_brief" && r.OwnerName == "morning_brief")
	}
	if !held || !a.briefFacility.Off() {
		t.Fatalf("the brief was turned off on the card: off in force %v, its alarm's row still its own %v (the row is what keeps the name from a timer)", a.briefFacility.Off(), held)
	}
}

// A HALF-TYPED NUMBER STOPS THE ONE SAVE. Typed on the real keyboard into a field
// that starts blank, an unfinished number reads as empty: it is a draft all the
// same, so the Save does not go past it, here or from another panel, and nothing
// else goes with it until it is right.
func TestAHalfTypedNumberStopsTheOneSave(t *testing.T) {
	chrome := needChrome(t)
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Journey")
	a := New(safebootConfig(t, dir, "Journey", keyPath, ledgerPath, dbPath))
	if err := startLiveForTest(a); err != nil {
		t.Fatalf("startLive: %v", err)
	}
	t.Cleanup(func() { stopApp(t, a) })
	page := openInChrome(t, chrome, a.dashboard.Origin(), "/#/settings/technical/limits")
	await := func(what, predicate string) {
		t.Helper()
		got, err := page.evalRetry(awaitJS(predicate, 20000), true, 30*time.Second, 5)
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		if got != "ok" {
			t.Fatalf("%s: %s", what, got)
		}
	}
	run := func(js string) {
		t.Helper()
		if _, err := page.eval(`(() => { `+js+` ; return 'done'; })()`, false, 10*time.Second); err != nil {
			t.Fatal(err)
		}
	}
	await("the panel opens with its blank budget", `
    const el = document.getElementById('cfg-ptokens');
    last = el ? 'value=' + el.value : 'no field';
    return el && el.value === '' ? 'ok' : null;`)
	turns := a.configSnapshot().Prompt.RecentTurns

	run(`document.getElementById('cfg-ptokens').focus()`)
	for _, key := range []string{"1", "e"} {
		for _, kind := range []string{"keyDown", "keyUp"} {
			params := map[string]any{"type": kind, "key": key}
			if kind == "keyDown" {
				params["text"] = key
			}
			if err := page.call("Input.dispatchKeyEvent", params, 10*time.Second); err != nil {
				t.Fatal(err)
			}
		}
	}
	await("the browser holds a bad number, and the bar counts it", `
    const el = document.getElementById('cfg-ptokens'), said = document.querySelector('.technical-savebar__said');
    last = 'bad=' + el.validity.badInput + ' value=' + el.value + ' invalid=' + el.getAttribute('aria-invalid') + ' bar=' + (said && said.textContent);
    return el.validity.badInput && el.value === '' && el.getAttribute('aria-invalid') === 'true' && said.textContent === '1 change' ? 'ok' : null;`)

	// With another change on the panel, the Save stops at it.
	run(`const other = document.getElementById('cfg-pturns'); other.value = String(Number(other.value) + 1); other.dispatchEvent(new Event('input', { bubbles: true }));
    document.querySelector('[data-save="technical"]').click()`)
	await("the Save stops at the field and says why", `
    const el = document.getElementById('cfg-ptokens'), said = el.closest('.technical-form__field').querySelector('[data-range-said]');
    last = 'focus=' + document.activeElement.id + ' said=' + said.textContent;
    return document.activeElement === el && !said.hidden && /whole number/.test(said.textContent) ? 'ok' : null;`)
	if got := a.configSnapshot().Prompt.RecentTurns; got != turns {
		t.Fatalf("a Save went past a half-typed number: recent turns %d, was %d", got, turns)
	}

	// From another panel, the Save goes to it.
	run(`location.hash = '#/settings/technical/verification'`)
	await("Verification opens", `last = location.hash; return document.getElementById('cfg-wurl') ? 'ok' : null;`)
	run(`document.querySelector('[data-save="technical"]').click()`)
	await("the Save goes back to the field", `
    const el = document.getElementById('cfg-ptokens');
    last = location.hash + ' focus=' + document.activeElement.id;
    return el && document.activeElement === el && el.validity.badInput ? 'ok' : null;`)
	if got := a.configSnapshot().Prompt.RecentTurns; got != turns {
		t.Fatalf("a Save from another panel went past a half-typed number: recent turns %d, was %d", got, turns)
	}

	// Cleared, the rest goes.
	run(`document.getElementById('cfg-ptokens').focus(); document.execCommand('selectAll'); document.execCommand('delete');`)
	await("the field is clear of its bad number", `
    const el = document.getElementById('cfg-ptokens');
    last = 'bad=' + el.validity.badInput + ' invalid=' + el.getAttribute('aria-invalid');
    return !el.validity.badInput && !el.hasAttribute('aria-invalid') ? 'ok' : null;`)
	run(`document.querySelector('[data-save="technical"]').click()`)
	waitUntil(t, func() bool { return a.configSnapshot().Prompt.RecentTurns == turns+1 }, "the other change was saved once the number was cleared")
}

// AN EMPTY LIST AT ITS DEFAULT IS NOT CHANGED: the shipped contacts: [] through the
// running program's own rows to the real page, with the file stating it and with the
// file unread. The count and the filter leave it out, and no default is named beside it.
func TestAnEmptyListAtItsDefaultIsNotCalledChanged(t *testing.T) {
	chrome := needChrome(t)
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Journey")
	cfg := safebootConfig(t, dir, "Journey", keyPath, ledgerPath, dbPath)
	if err := os.WriteFile(cfg.SourcePath, []byte(`{"contacts":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatalf("startLive: %v", err)
	}
	t.Cleanup(func() { stopApp(t, a) })
	page := openInChrome(t, chrome, a.dashboard.Origin(), "/#/settings/technical/inforce")
	await := func(what, predicate string) {
		t.Helper()
		got, err := page.evalRetry(awaitJS(predicate, 20000), true, 30*time.Second, 5)
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		if got != "ok" {
			t.Fatalf("%s: %s", what, got)
		}
	}
	const seen = `
    const row = document.querySelector('[data-force-row="contacts"]');
    const chip = document.querySelector('[data-force-filter="changed"]');
    if (!row || !chip) return null;
    const by = row.querySelector('.technical-force__by').textContent, value = row.querySelector('.technical-force__value').textContent;`
	await("the file states the empty list, and it is not changed", seen+`
    last = by + ' | ' + value + ' | ' + chip.textContent;
    return by === 'config.json' && value === '[]' && chip.textContent === 'Changed from default 0' ? 'ok' : null;`)
	const filtered = `
    document.querySelector('[data-force-filter="changed"]').click();
    const count = Number(document.querySelector('[data-force-filter="changed"]').textContent.replace(/\D/g, ''));
    const rows = [...document.querySelectorAll('[data-force-row]')].map(r => r.dataset.forceRow);
    last = count + ' | ' + rows.join();
    return rows.length === count && !rows.includes('contacts') ? 'ok' : null;`
	await("the filter leaves it out", filtered)
	if err := os.WriteFile(cfg.SourcePath, []byte(`{ not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := page.eval(`(() => { location.reload(); return 'done'; })()`, false, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	await("with the file unread it is said, and who set it is not", seen+`
    const said = document.querySelector('[data-force-unread]');
    last = (said && said.textContent) + ' | ' + by + ' | ' + value;
    return said && !said.hidden && /^config\.json could not be read/.test(said.textContent) && by === '' && value === '[]' ? 'ok' : null;`)
	await("the filter still leaves it out", filtered)
}
