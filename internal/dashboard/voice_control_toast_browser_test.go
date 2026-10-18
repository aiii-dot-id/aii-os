//go:build !windows

package dashboard

import (
	"encoding/json"
	"strings"
	"testing"
)

const voiceControlToastPage = `<!doctype html>
<button id="send-btn" disabled></button>
<button id="mic" disabled></button>
<script type="module">
import { run } from '/__harness.js';
import { S } from '/state.js';
const sockets = [];
class Socket {
  constructor(url) { this.url = url; this.readyState = 0; sockets.push(this); }
  send() {}
  open() { this.readyState = 1; this.onopen?.({}); }
  close() { this.readyState = 3; }
  recv(m) { this.onmessage?.({ data: JSON.stringify(m) }); }
}
window.WebSocket = Socket;
const ws = await import('/ws.js');
const sent = __SENT__, want = __WANT__;
run(assert => {
  ws.connect(); sockets[0].open();
  assert(sent.length === want.length && sent.length >= 4, 'the socket gave the page ' + sent.length + ' control failures');
  for (const [view, said] of [['home', () => window.__toasts || []], ['chat', () => window.__chatLines || []]]) {
    S.view = view;
    const before = said().length;
    for (const m of sent) sockets[0].recv(m);
    const shown = said().slice(before);
    assert(shown.length === want.length, view + ': ' + shown.length + ' of ' + want.length + ' failures were shown: ' + JSON.stringify(shown));
    shown.forEach((text, i) => {
      assert(!text.includes('{') && !text.includes('-320'), view + ': a control failure showed protocol text: ' + text);
      assert(text === want[i], view + ': failure ' + i + ' showed ' + JSON.stringify(text) + ', want ' + JSON.stringify(want[i]));
    });
  }
});
</script>`

func TestAVoiceControlFailureShowsOnlyItsSentenceInBrowser(t *testing.T) {
	sent, want := voiceControlFailures(t)
	sentJSON, err := json.Marshal(sent)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, _ := json.Marshal(want)
	page := strings.NewReplacer("__SENT__", string(sentJSON), "__WANT__", string(wantJSON)).Replace(voiceControlToastPage)
	wsJS, err := staticFS.ReadFile("static/ws.js")
	if err != nil {
		t.Fatal(err)
	}
	modules := wsPageModules(wsJS, []byte(`export const S = { connected:false, reconnectTimer:null, identityExists:false, view:'home', tokenPrompted:false, wsEverOpened:false, thinking:false };`))
	modules["/app.js"] = []byte(`export const toast = s => { (window.__toasts ||= []).push(s); }; export const go = () => {}; export const renderFirstbootVisibility = () => {};`)
	runPageInEngines(t, page, modules)
}

func TestARefusedOpenShowsOnlyTheHostsWordsInBrowser(t *testing.T) {
	all, allWant := voiceOpenRefusals(t)
	var sent []*VoiceSessionState
	var want []string
	for i, m := range all {
		if !strings.Contains(allWant[i], "speaking") {
			sent, want = append(sent, m.VoiceSession), append(want, allWant[i])
		}
	}
	sentJSON, err := json.Marshal(sent)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, _ := json.Marshal(want)
	page := micMarkup + `<script type="module">
import { sessionState } from './voice.js';
import { run } from './__harness.js';
const sent = ` + string(sentJSON) + `, want = ` + string(wantJSON) + `;
run(assert => {
  assert(sent.length === 3, 'the socket gave the page ' + sent.length + ' refused opens');
  for (const st of sent) sessionState(st);
  const shown = globalThis.toasts || [];
  assert(shown.length === want.length, shown.length + ' of ' + want.length + ' refusals were toasted: ' + JSON.stringify(shown));
  shown.forEach((text, i) => {
    assert(!text.includes('{') && !text.includes('-320'), 'a refused open showed protocol text: ' + text);
    assert(text === want[i], 'refusal ' + i + ' showed ' + JSON.stringify(text) + ', want ' + JSON.stringify(want[i]));
  });
});
</script>`
	runPageInEngines(t, page, micModules)
}
