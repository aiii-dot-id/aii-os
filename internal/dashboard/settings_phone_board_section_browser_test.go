//go:build !windows

// Runs only through the unix browser-engine rig (browserengine_test.go,
// same tag) — this file calls its helpers, so it shares its build row.

package dashboard

import (
	"net/http"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/sections"
)

// THE BOARD HOLDS WITH STATUS'S BUILT-IN SECTION REGISTERED, the page an
// identity's person sees (boot.go, safeboot.go register it): there the page's
// own Status bundle never loads, so the board is the page's (app.js,
// layout.css), not Status's. At a phone's width: #/settings is the list,
// Status's row says "Status" and nothing invented, the row opens the section
// at #/settings/status with the page's own Status hidden, and the bar's
// Settings leads back to the list as that address settles. Run with the
// sections list there first (the page holds Status's entry in its sections
// list before its first visit to Settings: registered before launch is not
// enough, the list comes over the socket after the page has loaded), and with
// it coming once the page has drawn the board on its own (host 1821; codex
// 2206; host 2215).
const boardSectionDriver = boardDriver + `
let held = null;
window.addEventListener('message', e => { if (e.data && e.data.type === 'section-holds') held = e.data; });
const box = () => document.querySelector('[id="view-section:status"]');
const visible = el => !!el && getComputedStyle(el).visibility !== 'hidden' && shown(el);
// Read in the address's own hashchange, after the page's own listeners
// (registered when it loaded, so they run first): not later, when a frame's
// redraw could draw the list instead.
const settled = act => new Promise(r => { addEventListener('hashchange', () => r(), { once: true }); act(); });
(async () => {
  await start();
  if (__LATE__) {
    await go('#/settings', onBoard);
    if (!onBoard()) return fail('rig: before the sections list came, #/settings at 390 was not the list');
    await fetch('/__release', { method: 'POST' });
    await until(() => box());
    if (!box()) return fail('rig: the sections list came and Status\'s section never took the place');
  } else {
    // The list first (host 2215): the page's sections list starts empty
    // (sections.js) and its one writer is onSections, called from the real
    // socket, so Status's entry in it is the list received and consumed. The
    // mount is lazy (a section's frame mounts only where it covers the view,
    // and / is not Status's), so it is checked after the visit, below.
    if (location.hash.startsWith('#/settings')) return fail('rig: the page opened at ' + location.hash + ', not before Settings');
    const { page } = await import('/sections.js');
    const holdsStatus = () => page.sectionsList.some(s => s.id === 'status' && s.built_in && Array.isArray(s.views) && s.views.includes('status'));
    await until(holdsStatus);
    if (!holdsStatus()) return fail('rig: the sections list had not reached the page before its first visit to #/settings');
    if (location.hash.startsWith('#/settings')) return fail('rig: the page was at ' + location.hash + ' before the sections list came');
  }
  await go('#/settings', onBoard);
  if (!onBoard()) return fail('#/settings at 390 with Status\'s section is not the list: list shown ' + shown(tabs()) + ', page shown ' + shown(views()));
  const all = rows().filter(shown);
  const names = all.map(rowName).join('|');
  if (names !== ['Status'].concat(GROUPS).join('|')) return fail('the rows with Status\'s section are ' + names);
  const st = all[0];
  if (/Loading/.test(st.textContent)) return fail('Status\'s row waits on what the page does not hold: ' + st.textContent);
  // The page may hold Status's answer too (this rig serves it): then the row
  // carries it as a glance. Otherwise it says "Status" and nothing more.
  const stg = st.querySelector('.status__glance');
  if (stg && !stg.textContent.trim()) return fail('Status\'s row carries an empty glance');
  if (st.textContent.replace(stg ? stg.textContent : '', '') !== 'Status') return fail('Status\'s row says ' + JSON.stringify(st.textContent) + ', not "Status" with at most a glance');
  st.click();
  // The wait also holds until the page's own Status is hidden, so the check
  // below reads where the page settles rather than one frame of it. Once in
  // 18 engine runs the own Status still stood beside the section here.
  // Why it was on is not proven: stretching
  // its .16 s fade did not reproduce it. A page that never hides it still
  // fails below in its own words, since until() returns quietly at 5 s.
  const ownHidden = () => { const o = document.getElementById('view-status'); return !o || getComputedStyle(o).visibility === 'hidden'; };
  await until(() => location.hash === '#/settings/status' && !onBoard() && visible(box()) && held && /Everything is working/.test(held.text) && ownHidden());
  if (location.hash !== '#/settings/status') return fail('Status\'s row went to ' + location.hash);
  if (onBoard()) return fail('#/settings/status with Status\'s section still shows the list');
  if (!visible(box())) return fail('#/settings/status does not show Status\'s section');
  if (!held || !/Everything is working/.test(held.text)) return fail('Status\'s section does not hold Status: ' + (held ? held.text : 'nothing said'));
  const own = document.getElementById('view-status');
  if (own && getComputedStyle(own).visibility !== 'hidden') return fail('the page\'s own Status stands beside its section');
  await settled(() => document.querySelector('#nav a[data-view="settings"]').click());
  if (location.hash !== '#/settings') return fail('the bar\'s Settings from Status\'s section went to ' + location.hash);
  if (!onBoard()) return fail('the bar\'s Settings from Status\'s section did not draw the list: list shown ' + shown(tabs()) + ', page shown ' + shown(views()));
  report('OK');
})().catch(e => fail((e && e.message) || String(e)));
`

func boardSection(t *testing.T) *sections.Section {
	t.Helper()
	tree, err := builtInStatusTree()
	if err != nil {
		t.Fatal(err)
	}
	built := BuiltInSections()
	built[0].FS = reportingTree{tree}
	return built[0]
}

func TestSettingsAtPhoneWidthIsItsListWithStatussSection(t *testing.T) {
	pressed := 0
	runDashboardSized(t, func(t *testing.T) *Server {
		reg := sections.NewRegistry()
		if err := reg.Register(boardSection(t)); err != nil {
			t.Fatal(err)
		}
		s := New("127.0.0.1", 0, statusSectionHandler(&pressed))
		s.SetSections(reg)
		return s
	}, "/", strings.ReplaceAll(boardSectionDriver, "__LATE__", "false"), 390, 844)
}

func TestSettingsAtPhoneWidthIsItsListWhenStatussSectionComesLate(t *testing.T) {
	pressed := 0
	runDashboardSized(t, func(t *testing.T) *Server {
		reg := sections.NewRegistry()
		s := New("127.0.0.1", 0, statusSectionHandler(&pressed))
		s.SetSections(reg)
		next := s.server.Handler
		s.server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/__release" {
				if err := reg.Register(boardSection(t)); err != nil {
					t.Error(err)
				}
				s.BroadcastSections()
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
		return s
	}, "/", strings.ReplaceAll(boardSectionDriver, "__LATE__", "true"), 390, 844)
}
