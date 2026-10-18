//go:build !windows

package app

import (
	"math"
	"os"
	"strings"
	"testing"
	"time"
)

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

	page := openInChrome(t, chrome, a.dashboard.Origin(), "/#/settings/identity")
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
	const field = `const field = key => document.querySelector('[data-cognition] [data-cog-key="' + key + '"]');`

	await("the card shows what the host holds", field+`
    const card = document.querySelector('[data-cognition]');
    last = card ? card.textContent.slice(0, 200) : 'no card';
    if (!card || !field('cognition.consolidate_batch')) return null;
    const batch = field('cognition.consolidate_batch'), ops = field('cognition.consolidate_max_ops'), spacing = field('cognition.consolidate_spacing_seconds');
    last = 'batch=' + batch.value + ' ops=' + ops.value + '/' + ops.placeholder + ' spacing=' + spacing.placeholder;
    if (batch.value !== '25' || batch.min !== '1' || batch.max !== '200') return null;
    if (ops.value !== '' || ops.placeholder.indexOf('32') !== 0 || spacing.placeholder.indexOf('1800') !== 0) return null;
    return document.getElementById('cog-brief-at').placeholder === '07:00' ? 'ok' : null;`)

	// A change the host keeps, writes and puts in force.
	run(field + `
    const b = field('cognition.consolidate_batch'); b.value = '12'; b.dispatchEvent(new Event('input', { bubbles: true }));
    const s = field('cognition.consolidate_spacing_seconds'); s.value = '2400'; s.dispatchEvent(new Event('input', { bubbles: true }));
    document.querySelector('[data-cognition] [data-save="cognition"]').click()`)
	await("the save is said beside the card's Save", `
    const said = document.querySelector('[data-cognition] [data-said="cognition"]');
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
    document.querySelector('[data-cognition] [data-save="cognition"]').click()`)
	await("the refusal and its bounds stand beside the card's Save", `
    const said = document.querySelector('[data-cognition] [data-said="cognition"]');
    last = said ? said.textContent : 'nothing said';
    return said && /cognition\.consolidate_batch: 500 is out of bounds/.test(said.textContent) && /between 1 and 200/.test(said.textContent) ? 'ok' : null;`)
	if got := a.configSnapshot().Cognition.ConsolidateBatch; got != 12 {
		t.Fatalf("a refused batch moved the host's: %d", got)
	}

	// No brief: its alarm stands, and holds its name, and writes nothing.
	run(field + `
    const b = field('cognition.consolidate_batch'); b.value = '12'; b.dispatchEvent(new Event('input', { bubbles: true }));
    document.getElementById('cog-brief-off').click();
    document.querySelector('[data-cognition] [data-save="cognition"]').click()`)
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
