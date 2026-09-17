//go:build !windows

package dashboard

import "testing"

// .
// .
// .
const speakersSettingsPage = substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { frames } from './ws.js';
import { renderSettings, saveConfigSection, speakersReadback } from './views/settings.js';
import { assert, run } from './__harness.js';

run(() => {
  const byId = id => document.getElementById(id);
  const sets = () => frames.filter(f => f.type === 'config_set').map(f => f.config);
  S.identityExists = true;
  S.providersLoaded = true;
  S.providers = [];
  S.config = { llm: { provider: 'OpenAI', model: 'm', resolved_provider: 'OpenAI', resolved_model: 'm', timeout_seconds: 120 },
    speech: { stt: { provider: '' }, tts: { provider: '' }, services: [],
      speakers: { mode: 'ignore', uids: ['visitor'], unidentified: 'withhold', revision: 4, withheld_finals: 2, withheld_partials: 7 } } };
  S.openSettings('speech');
  const card = byId('sp-speakers');
  assert(card, 'the Speakers card is on the speech page');
  assert(byId('sp-speakers-mode').value === 'ignore' && byId('sp-speakers-uids').value === 'visitor' && byId('sp-speakers-unid').value === 'withhold', 'the policy in force is drawn');
  const rb = byId('sp-speakers-readback').textContent;
  assert(rb.includes('Everyone but 1 listed speaker') && rb.includes('revision 4') && rb.includes('2 finals, 7 partials'), 'the readback names the rule, the revision and the counts: ' + rb);
  assert(!byId('sp-speakers-list-row').hidden && !byId('sp-speakers-unid-row').hidden, 'ignore shows the list and the unidentified rule');
  // The mode decides the rows.
  byId('sp-speakers-mode').value = 'all'; byId('sp-speakers-mode').onchange();
  assert(byId('sp-speakers-list-row').hidden && byId('sp-speakers-unid-row').hidden, 'all hides both rows');
  byId('sp-speakers-mode').value = 'only'; byId('sp-speakers-mode').onchange();
  assert(!byId('sp-speakers-list-row').hidden && byId('sp-speakers-unid-row').hidden, 'only shows the list, not the unidentified rule');
  // The save sends the whole policy as one object.
  byId('sp-speakers-uids').value = 'james-one, ada.2  visitor';
  saveConfigSection('speech_speakers');
  const sent = sets();
  assert(sent.length === 1 && sent[0]['speech.speakers'], 'one config_set with the policy: ' + JSON.stringify(sent));
  const pol = sent[0]['speech.speakers'];
  assert(pol.mode === 'only' && JSON.stringify(pol.uids) === '["james-one","ada.2","visitor"]' && !('unidentified' in pol), 'the object is the mode and the ids, the unidentified rule only under ignore: ' + JSON.stringify(pol));
  assert(speakersReadback({ mode: 'all', uids: [], revision: 0 }).startsWith('Everyone is heard'), 'all reads back as everyone');
});
</script>`

func TestTheSpeakersCardEditsThePolicyWhole(t *testing.T) {
	runPageInEngines(t, speakersSettingsPage, substrateModules(t))
}

// .
// .
const voiceFilterPage = `<!doctype html>
<div id="voice-transcript" hidden></div>
<div id="voice-filter" hidden></div>
<button class="converse" id="converse" disabled hidden aria-pressed="false">&#127908;</button>
<script type="module">
import { converseForTest, speakerFilterLine, voiceEvent } from './voice.js';
import { S } from './state.js';
import { assert, run } from './__harness.js';
run(() => {
  S.stats = { voice_engine: true, voice_state: 'plugin', speakers: { mode: 'only', uids: ['james-one'], revision: 2, withheld_finals: 3 } }; S.connected = true;
  const fl = document.getElementById('voice-filter');
  converseForTest('idle');
  assert(fl.hidden, 'nothing is said while nobody listens');
  converseForTest('live');
  assert(!fl.hidden && fl.textContent === 'Hearing only 1 listed speaker · 3 withheld', 'the restriction is named while listening: ' + fl.textContent);
  assert(speakerFilterLine({ mode: 'ignore', uids: ['a', 'b'], withheld_finals: 0 }) === 'Ignoring 2 listed speakers · 0 withheld', 'ignore reads as ignoring');
  voiceEvent({ type: 'transcript_withheld', session_id: 'vs-1', sequence: 8, reason: 'only the listed speakers are heard; this voice was not identified' });
  const tr = document.getElementById('voice-transcript');
  assert(!tr.hidden && tr.textContent.startsWith('A voice was withheld') && !tr.textContent.includes('secret'), 'a withheld voice is announced without its words: ' + tr.textContent);
  S.stats.speakers = { mode: 'all', uids: [] };
  converseForTest('live');
  assert(fl.hidden, 'everyone heard says nothing');
});
</script>`

func TestTheVoicePanelSaysWhoIsHeard(t *testing.T) {
	runPageInEngines(t, voiceFilterPage, map[string][]byte{
		"/app.js":   []byte("export function toast(m) {}\n"),
		"/state.js": []byte("export const S = { stats: null, connected: false };\n"),
	})
}
