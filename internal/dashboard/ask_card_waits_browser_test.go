//go:build !windows

// Runs only through the unix browser-engine rig (browserengine_test.go,
// same tag) — this file calls its helpers, so it shares its build row.

package dashboard

import "testing"

const askCardWaitsPage = substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { connect } from './ws.js';
import { anyPending } from './pending.js';
import { onOverlayChanged } from './overlay.js';
import { assert, run } from './__harness.js';
const went = []; S.go = v => went.push(v);
run(() => {
 const sockets=[];
 window.setTimeout=()=>1;window.clearTimeout=()=>{};
 // The deferred reload's re-check never runs here: what it would read is
 // anyPending(), asserted below.
 window.setInterval=()=>1;window.clearInterval=()=>{};
 window.WebSocket=class {
   constructor(){this.readyState=0;this.frames=[];sockets.push(this);}
   send(bytes){this.frames.push(JSON.parse(bytes));}
   recv(frame){this.onmessage({data:JSON.stringify(frame)});}
 };
 S.view='chat';S.identityExists=true;S.stats={name:'Fixture'};
 connect();const socket=sockets.at(-1);socket.readyState=1;socket.onopen();
 const cards = id => document.querySelectorAll('[data-ask-id="' + id + '"]');
 const card = id => cards(id)[0];
 const live = id => card(id).querySelectorAll('button:not([disabled])').length > 0;
 const note = id => card(id).querySelector('.c-ask__note').textContent;
 const press = (id, words) => [...card(id).querySelectorAll('button')].find(b => b.textContent === words).click();
 const closed = id => card(id).dataset.state === 'answered';
 const last = () => socket.frames.at(-1);
 const choose = { id: 'ask-1', kind: 'choose', from: 'identity', text: 'Aisle or window?', choices: ['aisle', 'window'] };
 const clarify = { id: 'ask-2', kind: 'clarify', from: 'identity', text: 'Which week?' };
 const connectAsk = { id: 'ask-3', kind: 'connect', from: 'identity', text: 'I need your email.', connector: 'email' };
 const act = { id: 'act-9', kind: 'confirm', from: 'org.example.uid', plugin: 'org.example.uid', operation: 'enroll', text: 'Enroll Quinn', args: { name: 'Quinn' } };
 socket.recv({ type: 'asks', asks: [choose, clarify, connectAsk, act] });
 assert(card('ask-1') && card('act-9'), 'rig: the asks did not draw their cards');

 socket.readyState = 3;
 const before = socket.frames.length;
 press('ask-1', 'window');
 press('ask-3', 'Connect, to read');
 assert(socket.frames.length === before, 'rig: an answer was sent with no connection');
 assert(live('ask-1') && note('ask-1').includes('Not connected'),
   'AN ANSWER THAT WAS NEVER SENT SETTLED ITS CARD: the page says the operator chose, and the identity is still waiting');
 assert(live('ask-3') && went.length === 0, 'a connect answer that was never sent still left for the Plugins page');
 socket.readyState = 1;

 // Refused at the press, through the socket's own error handling.
 press('ask-1', 'window');
 const chose = last();
 assert(chose.type === 'ask' && chose.ask.id === 'ask-1' && !live('ask-1') && note('ask-1') === 'Sending your answer…', 'rig: a sent answer did not quiet its card');
 // An overlay change while the answer waits holds the reload (DR08): the
 // card's wait is one of the slots the reload asks.
 onOverlayChanged(1, ['/views/home.js']);
 assert(anyPending() && document.getElementById('overlay-update'),
   'AN OVERLAY CHANGE WOULD RELOAD THE PAGE OVER AN ASK ANSWER STILL WAITING: the reload does not see the card\'s wait');
 socket.recv({ type: 'error', request_id: 'another-request', message: 'not this one' });
 assert(!live('ask-1'), 'another request\'s refusal put this card back');
 window.__think = 'untouched';
 socket.recv({ type: 'error', request_id: chose.request_id, message: 'fixture: the grant was not written' });
 assert(live('ask-1') && note('ask-1') === 'Not taken: fixture: the grant was not written', 'A REFUSED ANSWER LEFT A DEAD CARD: ' + note('ask-1'));
 assert(!anyPending(), 'a settled answer still holds the reload an overlay change asked for');

 press('act-9', 'Allow this once');
 const confirmed = last();
 assert(confirmed.type === 'plugin' && !live('act-9') && note('act-9') === 'Sending your answer…', 'rig: a confirmed act did not quiet its card');
 socket.recv({ type: 'error', request_id: confirmed.request_id, message: 'fixture: the act expired' });
 assert(live('act-9') && note('act-9').includes('the act expired'), 'a refused act left a card that says it is on its way');

 // Not recorded at the receipt, after the card closed.
 press('ask-1', 'aisle');
 const again = last(); const drawn = card('ask-1');
 socket.recv({ type: 'asks', asks: [clarify, connectAsk, act] });
 assert(closed('ask-1') && note('ask-1').startsWith('Answered '), 'rig: the answered card did not close');
 const why = 'your answer was not recorded (fixture: eight messages already wait), so the question is still open.';
 socket.recv({ type: 'error', request_id: again.request_id, message: why });
 assert(note('ask-1') === 'Not taken: ' + why, 'AN ANSWER THAT WAS NOT RECORDED WAS NOT SAID ON ITS CARD: ' + note('ask-1'));
 socket.recv({ type: 'asks', asks: [choose, clarify, connectAsk, act] });
 assert(cards('ask-1').length === 1 && card('ask-1') === drawn && !closed('ask-1') && live('ask-1') && note('ask-1') === 'Not taken: ' + why,
   'A CARD WHOSE ANSWER WAS NOT RECORDED STAYED CLOSED, or was drawn a second time: ' + cards('ask-1').length + ' ' + note('ask-1'));
 assert(window.__think === 'untouched', 'a refused answer was read as a turn\'s error');

 // A question another page answered, and which came back.
 socket.recv({ type: 'asks', asks: [choose, connectAsk, act] });
 assert(closed('ask-2') && note('ask-2') === 'Answered', 'rig: a card another page answered did not close, or said when this page answered it');
 socket.recv({ type: 'asks', asks: [choose, clarify, connectAsk, act] });
 assert(cards('ask-2').length === 1 && live('ask-2') && note('ask-2') === '', 'a question that came back is not asked again where it was');

 const inp = card('ask-2').querySelector('.c-ask__input'); inp.value = 'the week of the 21st';
 press('ask-2', 'Send');
 assert(!live('ask-2') && note('ask-2') === 'Sending your answer…', 'rig: a typed answer did not quiet its card');
 socket.readyState = 3; socket.onclose();
 assert(live('ask-2') && note('ask-2').includes('The connection dropped') && card('ask-2').querySelector('.c-ask__input').value === 'the week of the 21st',
   'A CONNECTION LOST UNDER AN ANSWER LEFT A DEAD CARD, or lost what was typed');
 assert(live('ask-1') && note('ask-1').includes('Not taken'), 'a lost connection rewrote a card that was not waiting');
 connect(); const next = sockets.at(-1); next.readyState = 1; next.onopen();
 next.recv({ type: 'asks', asks: [choose] });
 assert(closed('ask-2') && note('ask-2') === 'Answered', 'a card put back after a loss closed still saying the answer may not have arrived, or claimed this page answered it');
});
</script>`

func TestAnAskCardWaitsForTheHostAndComesBackWhenItsAnswerIsNotTaken(t *testing.T) {
	ws, err := staticFS.ReadFile("static/ws.js")
	if err != nil {
		t.Fatal(err)
	}
	modules := wsPageModules(ws, []byte(`export const S={providers:[]};`))
	// The modules the fix is in, as shipped: the socket's handling
	// (ws.js), the card (views/chat.js), its wait (pending.js, served from
	// the embedded files as every module not named here is) and the reload
	// an overlay change asks for (overlay.js).
	for _, file := range []string{"views/chat.js", "util.js", "overlay.js"} {
		data, err := staticFS.ReadFile("static/" + file)
		if err != nil {
			t.Fatal(err)
		}
		modules["/"+file] = data
	}
	modules["/presence.js"] = []byte(`import { S } from './state.js'; export const renderPresence = () => {}; export const toolPulse = () => {}; export const setThinking = on => { S.thinking = on; window.__think = on; };`)
	runPageInEngines(t, askCardWaitsPage, modules)
}
