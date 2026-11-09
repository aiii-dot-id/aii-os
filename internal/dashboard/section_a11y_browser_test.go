//go:build !windows

package dashboard

import (
	"fmt"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/sections"
)

// THE SECTIONS ARE READ AS THE PAGE IS (step 22b): in each built-in section's frame,
// About's four tabs and Status, every control a person can reach has a role and an
// accessible name, a heading or a named region names the place, and Status has the live region its
// changes are said in before any change comes; the frame itself is named and in the
// Tab order. At a desktop's width and a phone's, in each engine. Each answer is the
// view asked for: a tab that does not come is a failure, never the last tab's answer.
// What is read is the visible markup, from inside the frame, with the component
// checks' own rules for a role and a name (componentChecks); it does not read the
// browser's accessibility tree, and nothing is spoken.
const sectionsReadDriverJS = `
const report = text => fetch('/__result', { method: 'POST', body: text, keepalive: true });
const until = async (ok, ms) => { for (let i = 0; i * 50 < (ms || 10000) && !ok(); i++) await new Promise(r => setTimeout(r, 50)); return ok(); };
const frame = title => document.querySelector('iframe.section-frame[title="' + title + '"]');
const held = {}, showing = {}, read = {}, seen = [];
window.addEventListener('message', e => {
  if (!e.data) return;
  for (const title of ['About', 'Status']) {
    if (!frame(title) || frame(title).contentWindow !== e.source) continue;
    if (e.data.type === 'section-holds') { held[title] = e.data.text; showing[title] = e.data.view; }
    if (e.data.type === 'a11y') read[title] = e.data;
  }
});
const shown = el => el && el.getClientRects().length > 0 && getComputedStyle(el).visibility !== 'hidden';
const p = [];
const check = (ok, what) => { if (!ok) p.push(what); };
(async () => {
  await until(() => document.getElementById('send-btn') && (document.getElementById('p-state') || {}).textContent);
  for (const [hash, title, view] of [['#/settings/status', 'Status', 'view-status'], ['#/identity/who', 'About', 'view-identity'],
      ['#/identity/between', 'About', 'view-between'], ['#/identity/beliefs', 'About', 'view-memory'], ['#/identity/experiences', 'About', 'view-experiences']]) {
    location.hash = hash;
    const came = await until(() => shown(frame(title)) && showing[title] === view && held[title]);
    const f = frame(title);
    if (!came) { p.push(hash + ': its section did not show ' + view + ' (it shows ' + (showing[title] || 'nothing') + ')'); continue; }
    check(f.title, hash + ': the frame has no name');
    check(f.tabIndex >= 0, hash + ': the frame is not in the Tab order: ' + f.tabIndex);
    delete read[title];
    f.contentWindow.postMessage({ type: 'a11y' }, '*');
    if (!await until(() => read[title])) { p.push(hash + ': the frame did not answer'); continue; }
    const r = read[title];
    if (r.view !== view) { p.push(hash + ': the frame answered for ' + r.view + ', not ' + view); continue; }
    check(r.controls > 0, hash + ': no control a person can reach was read');
    check(!r.unnamed.length, hash + ': a control has no name: ' + r.unnamed.join(' | '));
    check(!r.unroled.length, hash + ': a control has no role: ' + r.unroled.join(' | '));
    check(r.heading || r.region, hash + ': no heading or named region names the place');
    if (title === 'Status') check(r.live.some(l => /status__answer\[polite\]/.test(l)), hash + ': Status has no live region for its answer before its first change: ' + r.live.join(' '));
    seen.push(hash + ' at ' + innerWidth + ' (' + r.view + '): ' + r.controls + ' controls; heading "' + r.heading + '"; region "' + r.region + '"; live ' + r.live.join(' '));
  }
  report(p.length ? 'FAIL: at ' + innerWidth + ': ' + p.join('\n  - ') : 'OK\n' + seen.join('\n'));
})().catch(e => report('FAIL: the driver stopped: ' + ((e && e.message) || e)));
`

func TestTheSectionsAreReadAsThePageIs(t *testing.T) {
	for _, size := range []struct{ w, h int }{{1280, 800}, {390, 844}} {
		t.Run(fmt.Sprintf("%dx%d", size.w, size.h), func(t *testing.T) {
			about, err := builtInAboutTree()
			if err != nil {
				t.Fatal(err)
			}
			status, err := builtInStatusTree()
			if err != nil {
				t.Fatal(err)
			}
			reg := sections.NewRegistry()
			for id, tree := range map[string]builtInTree{"status": status, "about": about} {
				built := builtInSection(t, id)
				built.FS = reportingTree{tree}
				if err := reg.Register(built); err != nil {
					t.Fatal(err)
				}
			}
			s := New("127.0.0.1", 0, aboutHandler())
			s.SetSections(reg)
			runDashboardInEnginesAtSize(t, s, "/", sectionsReadDriverJS, size.w, size.h)
		})
	}
}
