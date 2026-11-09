//go:build !windows

// Runs only through the unix browser-engine rig (browserengine_test.go,
// same tag) — this file calls its helpers, so it shares its build row.

package dashboard

import "testing"

func TestEverySettingsCardSaysWhatItDoes(t *testing.T) {
	page := `<!doctype html><html><body><div id="settings-stack"></div>
<script type="module">
import { S } from '/state.js';
import { renderSettings } from '/views/settings.js';
import { acceptMessages } from '/views/messages.js';
import { frames } from '/ws.js';
import { run } from '/__harness.js';
run(async (assert) => {
  S.view = 'settings'; S.connected = true; S.identityExists = true; S.providersLoaded = true; S.providers = [];
  S.tools = [{ name: 'read', description: 'Reads a file.', enabled: true }];
  S.sandbox = { root: '/home/id', extra_roots: [] };
  S.logsList = [{ name: 'aii.log', size: '1 KB' }];
  S.update = { enabled: true, current_version: '0.2.0', checked_at: 'today' };
  S.stats = { uninstall_offered: true, expert: true };
  S.backups = { secrets_ok: true, enabled: true, encrypting: true, backup_keep: 7,
    snapshots: [{ name: 'snap-1', at: '2026-10-14T04:00:00Z', record: 3, encrypted: true }],
    set_aside: [{ name: 'aside-1', set_aside_at: '2026-10-13T04:00:00Z', was_through: 5, bytes: 10, restored_to: 3, path: '/aside' }] };
  S.config = {
    llm: { provider: '', model: '', timeout_seconds: 120 },
    speech: { stt: {}, tts: {}, services: [] },
    dashboard: { host: '127.0.0.1', port: 8181, tls: true, require_token: true },
    secrets_ok: true,
    public_name: { name: 'vega', status: 'issued', origin: 'https://vega.example', route_mode: 'direct', relays: [] },
    witness: { url: '', interval_events: 0 }, prompt: { max_tokens: 0, recent_turns: 0 },
    agency: {}, cognition: { settings: [] }, logs: { dir: 'logs', max_backups: 0, compress_days: 0 },
    database: { active: 'sqlite' }, contacts: [], channels: [],
  };
  const st = document.getElementById('settings-stack');
  renderSettings();
  const ids = ['substrate', 'providers', 'speech', 'dashboard', 'backups', 'sandbox', 'tools', 'messages', 'updates'];
  const seen = new Map();
  for (const id of ids) {
    S.settingsSection(id); S.requestSettingsSection(); renderSettings();
    if (id === 'messages') {
      const asked = frames.filter(f => f.query === 'messages').at(-1);
      assert(asked, 'opening Messages did not ask for them');
      acceptMessages({ request_id: asked.request_id, messages: { arrivals: [], open: [], delivered: [], notices: [], read_ms: 1, limit: 10, max_limit: 100 } });
    }
    const heads = [...st.querySelectorAll('h3')];
    assert(heads.length > 0, id + ' draws no card');
    for (const h of heads) {
      // A card says it in its heading's title; a part drawn from a board (App,
      // step 18) says it in the visible hint the part names (aria-describedby).
      const part = h.closest('[aria-describedby]');
      const hinted = part && document.getElementById(part.getAttribute('aria-describedby'));
      const tip = (h.title || (hinted ? hinted.textContent : '')).trim(), label = h.textContent.trim();
      assert(tip.length > 20 && tip.endsWith('.'), id + ': the card ' + label + ' has no sentence of its own: "' + tip + '"');
      assert(tip.toLowerCase() !== label.toLowerCase(), id + ': the card ' + label + ' is explained by its own label');
      assert(!seen.has(tip) || seen.get(tip) === label, id + ': the cards ' + label + ' and ' + seen.get(tip) + ' share one sentence: "' + tip + '"');
      seen.set(tip, label);
    }
  }
  const labels = new Set(seen.values());
  for (const drawn of ['The model', 'Talk', 'Hearing you', "your identity's voice", 'Who can open this page', 'Address for other devices', 'Appearance', 'Notifications', 'Updates', 'What is kept', 'Your way back · the escrow file', 'What a restore does', 'Folders their tools may reach', 'Your contacts', 'Your notices', 'Messages', 'Remove AII OS from this computer']) {
    assert(labels.has(drawn), 'the card ' + drawn + ' was not drawn, so its sentence was not read');
  }
});
</script></body></html>`
	modules := map[string][]byte{
		"/state.js": []byte(`export const S = { providers: [], config: null, providersLoaded: false };`),
		"/ws.js": []byte(`let n = 0; export const frames = [];
export function send(frame) { frame.request_id ||= 'req-' + (++n); frames.push(frame); return frame.request_id; }
export function query(name, extra) { return send(Object.assign({ type: 'query', query: name }, extra || {})); }`),
	}
	runPageInEngines(t, page, modules)
}
