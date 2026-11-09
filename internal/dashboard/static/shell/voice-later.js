let voice = null;
let transport = null;
let asked = null;
const waiting = [];

export function bindVoice(frame, json) { transport = [frame, json]; }

export function toVoice(name, ...args) {
  if (voice) { voice[name]?.(...args); return; }
  if (name === 'connectionLost') waiting.length = 0;
  else if (name !== 'render' && name !== 'receiveFrame') waiting.push([name, args]);
  if (!asked) queueMicrotask(fetchVoice);
}

export function fetchVoice() {
  asked ||= import('../voice.js').then(mod => {
    voice = Object.assign({}, mod, (transport && mod.bindTransport?.(...transport)) || {});
    mod.wireMic?.();
    mod.render?.();
    for (const [name, args] of waiting.splice(0)) voice[name]?.(...args);
  }, () => { asked = null; });
  return asked;
}
