//go:build (linux && !android) || (darwin && !ios)

package app

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// meaning_trial_page_test.go — the trial's last step, read from a page.
//
// The run before this one judges the package by what the App reports to
// its own process. An operator sees none of that: they see the Memory
// view. So the same scratch identity's dashboard is opened in a real
// browser, signed in to the way a person signs in (the token typed into
// the page's own form; this process holds it and nothing prints it), and
// two things are read from the DOM: the meaning card (what makes the
// vectors, under which basis and floor, how much of each store holds
// one) and a recall typed into the view's box (a hit marked as found by
// meaning with its similarity, and the footer naming basis and floor).
//
// LIMITS, NAMED. Blink only, as the projects journey: the join is what
// is proved here, and render parity across engines is the fixture
// tests'. The keystrokes are events dispatched to the page's own input
// and handled by the page's own handlers; they are not the operating
// system's key events. And the card while a voice session speaks is not
// here: this identity has no voice engine, and that state is held by the
// tests that stand one in.

// meaningTrialPage drives the Memory view of a's dashboard and returns
// what it read. card is the App's own account of the meaning layer; the
// page must say the same. cue is a question whose memory, expect, the
// meaning layer brought back in this process a moment ago.
func meaningTrialPage(t *testing.T, a *App, card, cue, expect string) map[string]any {
	t.Helper()
	chrome, looked := chromeBin()
	if chrome == "" {
		t.Log("the page was not driven: no Chrome or Chromium to drive; " + looked)
		return map[string]any{"driven": false, "why": "no Chrome or Chromium on this machine"}
	}
	var want struct {
		Source   string  `json:"source"`
		Plugin   string  `json:"plugin"`
		Basis    string  `json:"basis"`
		Floor    float64 `json:"floor"`
		Coverage []struct {
			Store    string `json:"store"`
			Rows     int64  `json:"rows"`
			Embedded int64  `json:"embedded"`
		} `json:"coverage"`
	}
	if err := json.Unmarshal([]byte(card), &want); err != nil || want.Source != "plugin" || want.Basis == "" {
		t.Fatalf("the App's own account of the meaning layer is not a plugin's with a basis (%v): %s", err, card)
	}
	out := map[string]any{"driven": true, "browser": filepath.Base(chrome), "origin_scheme": strings.SplitN(a.dashboard.Origin(), ":", 2)[0]}
	page := openInChrome(t, chrome, a.dashboard.Origin(), "/#/memory")

	// ---- SIGN-IN, when the page asks: the token goes into the page's own
	// form and the form is submitted, as a person does it.
	state, err := page.evalRetry(awaitJS(`
    if (document.querySelector('#mem-meaning')) return 'in';
    const f = document.querySelector('#dashboard-signin form');
    if (f && !f.hidden) return 'asked';
    last = 'neither the meaning card nor the sign-in form is on the page';
    return null;
  `, 30000), true, 40*time.Second, 5)
	if err != nil || strings.HasPrefix(state, "TIMEOUT") {
		t.Fatalf("the Memory view never showed its card or a sign-in: %v %s", err, state)
	}
	out["sign_in_asked"] = state == "asked"
	if state == "asked" {
		token, _ := json.Marshal(a.dashboardToken())
		if _, err := page.eval(`(function(){
      const f = document.querySelector('#dashboard-signin form');
      f.querySelector('input').value = `+string(token)+`;
      f.requestSubmit();
      return 'sent';
    })()`, false, 10*time.Second); err != nil {
			t.Fatalf("the page's sign-in form could not be filled and sent: %v", err)
		}
	}

	// ---- THE CARD. Read as text, item by item; each sentence the page
	// must carry is built here from the App's own account.
	raw, err := page.evalRetry(awaitJS(`
    const c = document.querySelector('#mem-meaning');
    if (!c) {
      const s = document.querySelector('#dashboard-signin p');
      last = 'no meaning card' + (s ? '; the sign-in says: ' + s.textContent : '');
      return null;
    }
    const sel = document.querySelector('#mem-source');
    const items = Array.from(c.querySelectorAll('.item')).map(e => e.textContent);
    last = items.join(' | ');
    if (!sel || !items.some(x => x.startsWith('Source: '))) return null;
    return JSON.stringify({choice: sel.value, label: sel.options[sel.selectedIndex].textContent, items: items});
  `, 30000), true, 40*time.Second, 5)
	if err != nil || strings.HasPrefix(raw, "TIMEOUT") {
		t.Fatalf("the meaning card never named a source: %v %s", err, raw)
	}
	var shown struct {
		Choice string   `json:"choice"`
		Label  string   `json:"label"`
		Items  []string `json:"items"`
	}
	if err := json.Unmarshal([]byte(raw), &shown); err != nil {
		t.Fatalf("the card as read: %v: %s", err, raw)
	}
	out["card"] = shown.Items
	out["choice"] = shown.Label
	has := func(sentence string) bool {
		for _, item := range shown.Items {
			if item == sentence {
				return true
			}
		}
		return false
	}
	if shown.Choice != meaningChoicePlugin+want.Plugin {
		t.Fatalf("the card's choice is %q (%s), want the plugin %s", shown.Choice, shown.Label, want.Plugin)
	}
	source := fmt.Sprintf("Source: the plugin %s, on this machine basis %s · floor %.3f", want.Plugin, want.Basis, want.Floor)
	if !has(source) {
		t.Fatalf("the card does not say %q; it says: %s", source, strings.Join(shown.Items, " | "))
	}
	var rows, embedded int64
	for _, c := range want.Coverage {
		rows, embedded = rows+c.Rows, embedded+c.Embedded
		if line := fmt.Sprintf("%s %d of %d hold a vector", c.Store, c.Embedded, c.Rows); !has(line) {
			t.Fatalf("the card does not say %q; it says: %s", line, strings.Join(shown.Items, " | "))
		}
	}
	if rows == 0 {
		t.Fatal("the App counts no row in scope: the card's coverage was compared with nothing")
	}
	if rows == embedded && !has("Every row in scope holds a vector.") {
		t.Fatalf("every row holds a vector and the card does not say so; it says: %s", strings.Join(shown.Items, " | "))
	}

	// ---- A RECALL FROM THE VIEW'S BOX. The cue is typed and Enter
	// pressed; the page asks the host and draws what it answers.
	q, _ := json.Marshal(cue)
	if _, err := page.eval(`(function(){
      const box = document.querySelector('#mem-search');
      box.focus();
      box.value = `+string(q)+`;
      box.dispatchEvent(new Event('input', {bubbles: true}));
      document.querySelector('#mem-search').dispatchEvent(new KeyboardEvent('keydown', {key: 'Enter', bubbles: true, cancelable: true}));
      return 'asked';
    })()`, false, 10*time.Second); err != nil {
		t.Fatalf("the view's box could not be typed into: %v", err)
	}
	text, err := page.evalRetry(awaitJS(`
    const r = document.querySelector('#mem-recall');
    if (!r) { last = 'no recall on the page'; return null; }
    return r.textContent;
  `, 60000), true, 70*time.Second, 3)
	if err != nil || strings.HasPrefix(text, "TIMEOUT") {
		t.Fatalf("the page never showed a recall for the cue: %v %s", err, text)
	}
	out["recall"] = text
	footer := fmt.Sprintf("Meaning layer: consulted (basis %s, floor %.3f); a hit marked meaning or both carries its similarity", want.Basis, want.Floor)
	if !strings.Contains(text, footer) {
		t.Fatalf("the recall on the page does not end with %q:\n%s", footer, text)
	}
	marked := regexp.MustCompile(regexp.QuoteMeta(expect) + `, [^|\]]*\| rank \d+, (meaning, similarity \d\.\d{3}|both \([a-z_ +]*meaning \d\.\d{3}\)), score \d\.\d{5}`)
	hit := marked.FindString(text)
	if hit == "" {
		t.Fatalf("the recall on the page does not show memory %s as found by meaning with its similarity:\n%s", expect, text)
	}
	out["hit"] = hit
	return out
}
