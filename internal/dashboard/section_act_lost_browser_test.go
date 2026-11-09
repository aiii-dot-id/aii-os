//go:build !windows

package dashboard

import "testing"

/*
A SECTION'S ACT THAT WAITS WHEN THE CONNECTION IS LOST IS REFUSED, AND SAYS SO,
AND THE SECTION IS TOLD THE CONNECTION WENT AND CAME BACK.

	A section's act waits for the host's own answer (bridge.js). When the
	socket that carried it is lost, that answer will never come: the REAL
	ws.js tells the REAL sections.js, which refuses every act still
	waiting, so the section hears that the connection was lost instead of
	waiting for ever.

	The section's frame is served this same document. Inside the frame it
	speaks for a section as section-api.js does, by the bridge's own
	messages: hello, then one act on the port it is given.
*/
const sectionActLostPage = `<!doctype html>
<div id="nav"></div><div id="views"></div>
<div id="slot-rail"></div><div id="slot-panel"></div><div id="slot-dock"></div><div id="slot-overlay"></div>
<button id="send-btn" disabled></button><button id="mic" disabled></button>
<script type="module">
if (window.top !== window) {
  window.addEventListener('message', e => {
    if (!e.data || e.data.type !== 'aii-section-connect' || !e.ports || !e.ports[0]) return;
    const port = e.ports[0];
    port.onmessage = m => {
      if (m.data && m.data.kind === 'reply' && m.data.id === 1) parent.postMessage({ type: 'act-answered', ok: m.data.ok, error: m.data.error || '' }, '*');
      if (m.data && m.data.kind === 'topic' && m.data.topic === 'connection') parent.postMessage({ type: 'connection', on: m.data.data }, '*');
    };
    port.postMessage({ kind: 'subscribe', id: 2, topic: 'connection' });
    port.postMessage({ kind: 'act', id: 1, command: 'project.select', args: { id: 'p-one' } });
  });
  parent.postMessage({ type: 'aii-section-hello', v: 1 }, '*');
} else {
  const { run, until } = await import('/__harness.js');
  const { S } = await import('/state.js');
  const sockets = [];
  class Socket {
    constructor(url) { this.url = url; this.readyState = 0; this.sent = []; sockets.push(this); }
    send(m) { this.sent.push(JSON.parse(m)); }
    open() { this.readyState = 1; this.onopen?.({}); }
    close() { this.readyState = 3; }
    drop() { this.readyState = 3; this.onclose?.({}); }
    recv(m) { this.onmessage?.({ data: JSON.stringify(m) }); }
  }
  Socket.CONNECTING = 0; Socket.OPEN = 1; Socket.CLOSING = 2; Socket.CLOSED = 3;
  window.WebSocket = Socket;
  const ws = await import('/ws.js');
  const { initSections } = await import('/sections.js');
  let answered = null, connection = null;
  window.addEventListener('message', e => {
    if (e.data && e.data.type === 'act-answered') answered = e.data;
    if (e.data && e.data.type === 'connection') connection = e.data.on;
  });
  run(async (assert) => {
    S.view = 'chat';
    initSections();
    ws.connect(); sockets[0].open();
    sockets[0].recv({ type: 'sections', sections: [{ id: 'waiting', slot: 'rail', title: 'Waiting', entry: 'waiting.html', built_in: true, commands: ['project.select'], topics: ['connection'] }] });
    sockets[0].recv({ type: 'layout', layout: { profiles: { desktop: { rail: ['waiting'] }, mobile: { rail: ['waiting'] } } } });
    await until(() => sockets[0].sent.some(f => f.type === 'project'));
    assert(sockets[0].sent.some(f => f.type === 'project' && f.project && f.project.id === 'p-one'), 'rig: the section\'s act never reached the socket');
    assert(answered === null, 'the act was answered before the host answered it: ' + JSON.stringify(answered));
    sockets[0].drop();
    await until(() => answered);
    assert(answered && !answered.ok && /connection was lost/.test(answered.error), 'a lost connection did not refuse the act that waited: ' + JSON.stringify(answered));
    await until(() => connection === false);
    assert(connection === false, 'the section was not told the connection was lost: ' + connection);
    await until(() => sockets.length > 1, 8000);
    sockets[sockets.length - 1].open();
    sockets[sockets.length - 1].recv({ type: 'status', stats: { name: 'Fixture' } });
    await until(() => connection === true);
    assert(connection === true, 'the section was not told the connection came back: ' + connection);
  });
}
</script>`

// THE TURN REACHES A SECTION FROM THE SOCKET'S OWN FRAMES: the REAL ws.js tells the
// REAL sections.js when a turn ends, by its reply and by its cancelling, and a
// built-in section that reads the turn hears each.
const sectionTurnPage = `<!doctype html>
<div id="nav"></div><div id="views"></div>
<div id="slot-rail"></div><div id="slot-panel"></div><div id="slot-dock"></div><div id="slot-overlay"></div>
<button id="send-btn" disabled></button><button id="mic" disabled></button>
<script type="module">
if (window.top !== window) {
  window.addEventListener('message', e => {
    if (!e.data || e.data.type !== 'aii-section-connect' || !e.ports || !e.ports[0]) return;
    const port = e.ports[0];
    port.onmessage = m => { if (m.data && m.data.kind === 'topic') parent.postMessage({ type: 'heard', data: m.data.data }, '*'); };
    port.postMessage({ kind: 'subscribe', id: 1, topic: 'turn' });
  });
  parent.postMessage({ type: 'aii-section-hello', v: 1 }, '*');
} else {
  const { run, until } = await import('/__harness.js');
  const { S } = await import('/state.js');
  const sockets = [];
  class Socket {
    constructor(url) { this.url = url; this.readyState = 0; this.sent = []; sockets.push(this); }
    send(m) { this.sent.push(JSON.parse(m)); }
    open() { this.readyState = 1; this.onopen?.({}); }
    close() { this.readyState = 3; }
    recv(m) { this.onmessage?.({ data: JSON.stringify(m) }); }
  }
  Socket.CONNECTING = 0; Socket.OPEN = 1; Socket.CLOSING = 2; Socket.CLOSED = 3;
  window.WebSocket = Socket;
  const ws = await import('/ws.js');
  await import('/sections.js');
  const heard = [];
  window.addEventListener('message', e => { if (e.data && e.data.type === 'heard') heard.push(e.data.data); });
  run(async (assert) => {
    S.view = 'chat';
    ws.connect(); sockets[0].open();
    sockets[0].recv({ type: 'sections', sections: [{ id: 'turns', slot: 'rail', title: 'Turns', entry: 'turns.html', built_in: true, topics: ['turn'] }] });
    sockets[0].recv({ type: 'layout', layout: { profiles: { desktop: { rail: ['turns'] }, mobile: { rail: ['turns'] } } } });
    await until(() => document.querySelector('iframe.section-frame'));
    await until(() => heard.length);
    const ended = () => heard.at(-1).ended;
    const was = ended();
    sockets[0].recv({ type: 'cancelled' });
    await until(() => ended() === was + 1);
    assert(ended() === was + 1, 'a cancelled turn was not heard as ended: ' + JSON.stringify(heard));
    sockets[0].recv({ type: 'response', role: 'identity', message: 'done' });
    await until(() => ended() === was + 2);
    assert(ended() === was + 2, 'a reply was not heard as a turn ended: ' + JSON.stringify(heard));
  });
}
</script>`

func TestTheTurnReachesASectionFromTheSocketsOwnFrames(t *testing.T) {
	wsJS, err := staticFS.ReadFile("static/ws.js")
	if err != nil {
		t.Fatal(err)
	}
	modules := wsPageModules(wsJS, []byte(`export const S = { connected: false, reconnectTimer: null, identityExists: false, view: 'home', tokenPrompted: false, wsEverOpened: false };`))
	for module, name := range map[string]string{"/sections.js": "static/sections.js", "/bridge.js": "static/bridge.js", "/sections-frames.js": "static/sections-frames.js"} {
		b, err := staticFS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		modules[module] = b
	}
	runPageInEngines(t, sectionTurnPage, modules)
}

func TestASectionsActWaitingWhenTheConnectionIsLostIsRefused(t *testing.T) {
	wsJS, err := staticFS.ReadFile("static/ws.js")
	if err != nil {
		t.Fatal(err)
	}
	sectionsJS, err := staticFS.ReadFile("static/sections.js")
	if err != nil {
		t.Fatal(err)
	}
	stateJS := []byte(`export const S = { connected: false, reconnectTimer: null, identityExists: false, view: 'home', tokenPrompted: false, wsEverOpened: false };`)
	modules := wsPageModules(wsJS, stateJS)
	modules["/sections.js"] = sectionsJS
	framesJS, err := staticFS.ReadFile("static/sections-frames.js")
	if err != nil {
		t.Fatal(err)
	}
	modules["/sections-frames.js"] = framesJS
	bridgeJS, err := staticFS.ReadFile("static/bridge.js")
	if err != nil {
		t.Fatal(err)
	}
	modules["/bridge.js"] = bridgeJS
	runPageInEngines(t, sectionActLostPage, modules)
}
