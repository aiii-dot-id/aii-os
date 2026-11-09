//go:build !windows

package dashboard

import (
	"sort"
	"strings"
	"testing"
)

// settingsColdModules is Settings as its code is split, each module in the
// order it is entered first below. TestTheSettingsColdListIsTheWholeGroup
// keeps it whole.
var settingsColdModules = []string{
	"views/settings.js",
	"views/settings/parts.js",
	"views/settings/state.js",
	"views/settings/save.js",
	"views/settings/applies.js",
	"views/settings/modelwords.js",
	"views/settings/keep.js",
	"views/settings/access.js",
	"views/settings/access-routes.js",
	"views/settings/people.js",
	"views/settings/people-say.js",
	"views/settings/permissions.js",
	"views/settings/model.js",
	"views/settings/local.js",
	"views/settings/local-rule.js",
	"views/settings/capabilities.js",
	"views/settings/voice.js",
	"views/settings/voice-speakers.js",
	"views/settings/app.js",
	"views/settings/phone.js",
}

// TestTheSettingsColdListIsTheWholeGroup keeps the list above whole: a
// module of Settings the list leaves out would never be entered first, and
// the test below would pass without it. The group is settingsGroup's walk,
// which follows imports through every module of Settings, not only the
// index's own, so a module reached only through another is held too.
func TestTheSettingsColdListIsTheWholeGroup(t *testing.T) {
	want := map[string]bool{}
	for _, p := range settingsGroup(t) {
		want[strings.TrimPrefix(p, "static/")] = true
	}
	have := map[string]bool{}
	for _, m := range settingsColdModules {
		have[m] = true
	}
	var missing, extra []string
	for m := range want {
		if !have[m] {
			missing = append(missing, m)
		}
	}
	for m := range have {
		if !want[m] {
			extra = append(extra, m)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 || len(extra) > 0 {
		t.Errorf("settingsColdModules is not the group Settings' imports reach: missing %v, not reached %v", missing, extra)
	}
}

// settingsColdJS enters Settings through entry on the shipped page, once its
// shell is running on the conversation and no module of the group has been
// fetched (the conversation fetches none of it),
// then loads the rest of the group, opens Settings, and calls the forwarder in
// parts.js with the stack composing, which the index's renderSettings
// answers by marking a refresh due.
func settingsColdJS(entry string) string {
	list := "'/" + strings.Join(settingsColdModules, "', '/") + "'"
	return `import { report, until } from '/__harness.js';
import { S } from '/state.js';
const sockets = window.__sockets;
const problems = [];
const thrown = [];
addEventListener('error', (e) => thrown.push('an error reached the page: ' + (e.message || e.error)));
addEventListener('unhandledrejection', (e) => thrown.push('a rejection reached the page: ' + ((e.reason && e.reason.message) || e.reason)));
const entry = '/` + entry + `';
const group = [` + list + `];
const settingsFetched = () => performance.getEntriesByType('resource').map(e => new URL(e.name).pathname).filter(p => group.includes(p));
async function run() {
  await until(() => sockets.length > 0);
  sockets[0].open();
  sockets[0].say({ type: 'status', stats: { name: 'Mira', version: 'test', build: 'test', ledger_seq: 9, lifetime_ticks: 1, belief_count: 0, intention_count: 0, experience_count: 0, reflection_count: 0 } });
  await until(() => S.view === 'chat', 4000);
  // waits by design: an absence is read, after the conversation has had the time to
  // fetch what it would.
  await new Promise(r => setTimeout(r, 300));
  const early = settingsFetched();
  if (S.view !== 'chat' || early.length) {
    report('FAIL: not a cold entry: the view is ' + S.view + ' and the shell had fetched ' + (early.join(', ') || 'nothing of Settings'));
    return;
  }
  try { await import(entry); }
  catch (e) { problems.push('a cold entry through ' + entry + ' threw ' + (e && e.name) + ': ' + (e && e.message)); }
  for (const m of [` + list + `]) {
    try { await import(m); }
    catch (e) { problems.push(m + ' did not load after a cold entry through ' + entry + ': ' + (e && e.name) + ': ' + (e && e.message)); }
  }
  if (problems.length === 0) {
    S.go('settings');
    await until(() => { const s = document.getElementById('settings-stack'); return !!(s && s.children.length); }, 4000);
    const st = document.getElementById('settings-stack');
    if (!st || !st.children.length) {
      problems.push('after a cold entry through ' + entry + ', Settings did not draw when it was opened');
    } else {
      const parts = await import('/views/settings/parts.js');
      st.settingsComposing = true;
      st.settingsRefreshDue = false;
      try { parts.renderSettings(); }
      catch (e) { problems.push('after a cold entry through ' + entry + ', the forwarder in parts.js threw ' + e.name + ': ' + e.message); }
      if (st.settingsRefreshDue !== true) problems.push('after a cold entry through ' + entry + ", the forwarder in parts.js did not reach the index's renderSettings");
      st.settingsComposing = false;
      st.settingsRefreshDue = false;
    }
  }
  problems.push(...thrown);
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e) + (problems.length ? '\n  - ' + problems.join('\n  - ') : '')));
`
}

// TestEachSettingsModuleLoadsColdWithNoBindingReadBeforeItsModuleRuns enters
// Settings through each of its modules in turn, on the shipped page with its
// shell running and Settings not yet fetched. (On a bare page the index's
// imports reach the shell's own modules out of their order and stop on the
// socket's binding before it is initialized, before the split as well, so
// the entry is made where the page makes it: after the shell.) The modules
// import one another in cycles through the index, which is safe only while
// no module's top level reads a binding of a module that has not run yet,
// and the module a page reaches first decides that order, so each one is
// tried first. Then Settings opens, and the forwarder in parts.js must reach
// the index's renderSettings: the index registers it while parts.js may not
// have run, so a holder declared with let throws on an entry through
// parts.js, and a holder given a default is reset after the registration and
// forwards to nothing.
func TestEachSettingsModuleLoadsColdWithNoBindingReadBeforeItsModuleRuns(t *testing.T) {
	for _, entry := range settingsColdModules {
		entry := entry
		name := strings.TrimSuffix(strings.ReplaceAll(strings.TrimPrefix(entry, "views/"), "/", "-"), ".js")
		t.Run(name, func(t *testing.T) {
			runRealPage(t, statusFakeSocketJS, settingsColdJS(entry), homeStyles)
		})
	}
}
