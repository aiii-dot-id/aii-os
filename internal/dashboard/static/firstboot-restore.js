import { S } from './state.js';
import { $, esc } from './util.js';
import { send } from './ws.js';

// First boot's second door: restore an identity on a machine that holds
// none. Three things from the person — the model it will run on (the birth
// form's own fields), the escrow file with its passphrase, and the snapshot
// that left the old machine — and then the identity's own page.
//
// The passphrase is read from its field when the keys are asked for and is
// not kept: the snapshot is opened by the keys once they are in place.

let step = 'closed';   // closed | keys | snapshot | restoring | done
let busy = false;
let said = null;       // { kind, text }
let keys = null;       // { identity, holds_snapshot_key }
let snapshot = '';     // the snapshot's name, once it has arrived
let progress = '';
let secretsOK = true;

const body = () => $('fb-restore-body');
function say(kind, text) { said = { kind, text }; render(); }

export function renderRestoreDoor() {
  const open = $('fb-restore-open');
  if (!open) return;
  open.hidden = step !== 'closed' || !!S.identityExists;
  open.onclick = () => { step = 'keys'; said = null; send({ type: 'restore_new', restore_new: { action: 'hello' } }); render(); };
  render();
}

function render() {
  const el = body();
  if (!el) return;
  const open = $('fb-restore-open');
  if (open) open.hidden = step !== 'closed' || !!S.identityExists;
  el.hidden = step === 'closed' || !!S.identityExists;
  if (el.hidden) { el.innerHTML = ''; return; }
  let html = '<h2 style="margin-top:6px">Restore an identity</h2>' +
    '<p class="lead">For a machine that has never held this identity. You need the two things that left the old machine — the <b>escrow file</b> with its passphrase, and a <b>snapshot</b> — and the provider, model and key above, because a restored identity needs a mind to run on.</p>';
  if (!secretsOK) {
    html += '<div class="err" data-nosecrets>This connection is not private: a passphrase, or an identity in the clear, would cross the network. Open this page on the machine itself, or serve the dashboard with TLS, and come back.</div>';
    el.innerHTML = html; return;
  }
  // 1 — the keys
  html += '<div data-step="keys"><label class="f">1 · THE ESCROW FILE</label>';
  if (keys) {
    html += '<div class="ok" data-keys-ok>The keys of identity <span style="font-family:var(--mono)">' + esc(keys.identity.slice(0, 16)) + '…</span> are in place on this machine; the key signs and verifies.</div>';
  } else {
    html += '<input type="file" id="fbr-escrow" accept=".age">' +
      '<label class="f">ITS PASSPHRASE</label><input type="password" id="fbr-pass" autocomplete="off">' +
      '<div style="margin-top:10px"><button class="btn" id="fbr-keys"' + (busy ? ' disabled' : '') + '>' + (busy ? 'Opening…' : 'Open it') + '</button></div>';
  }
  html += '</div>';
  // 2 — the snapshot
  if (keys) {
    html += '<div data-step="snapshot" style="margin-top:16px"><label class="f">2 · THE SNAPSHOT</label>';
    if (snapshot) {
      html += '<div class="ok" data-snapshot-ok>' + esc(snapshot) + ' is on this machine.</div>';
    } else {
      html += (keys.holds_snapshot_key
        ? '<div class="fb-hint">The encrypted snapshot file, named <span style="font-family:var(--mono)">ledger-&lt;date&gt;-seq&lt;N&gt;.tar.age</span>. Do not rename it.</div><input type="file" id="fbr-snap" accept=".age">'
        : '<div class="fb-hint">This escrow was made before snapshots were encrypted, so pick a plaintext snapshot\'s FOLDER, named <span style="font-family:var(--mono)">ledger-&lt;date&gt;-seq&lt;N&gt;</span>.</div>') +
        '<div class="fb-hint" style="margin-top:8px">Or a plaintext snapshot\'s folder:</div><input type="file" id="fbr-folder" webkitdirectory>' +
        '<div style="margin-top:10px"><button class="btn" id="fbr-send"' + (busy ? ' disabled' : '') + '>' + (busy ? esc(progress || 'Sending…') : 'Send it to this machine') + '</button></div>';
    }
    html += '</div>';
  }
  // 3 — restore
  if (keys && snapshot) {
    html += '<div data-step="restore" style="margin-top:16px"><label class="f">3 · RESTORE</label>' +
      '<div class="fb-hint" data-restore-says>The snapshot is proved before anything is put in place, and held to the keys above: another identity\'s snapshot is refused. The identity will be as it was when that snapshot was made — what it lived afterwards on the old machine is not in it. If the witness attested records this snapshot does not hold, the next anchor is refused as a rollback and the identity enters SAFE until you deal with it — see Backups &amp; Keys after the restore.</div>' +
      '<div style="margin-top:10px"><button class="btn" id="fbr-restore" style="width:100%"' + (busy ? ' disabled' : '') + '>' + (step === 'restoring' ? 'Proving and restoring…' : 'Restore') + '</button></div></div>';
  }
  if (said) html += '<div class="' + (said.kind === 'ok' ? 'ok' : 'err') + '" data-restore-said style="margin-top:12px">' + esc(said.text) + '</div>';
  html += '<div style="margin-top:14px"><button class="btn ghost" id="fbr-close" type="button"' + (busy ? ' disabled' : '') + '>Back to Birth</button></div>';
  el.innerHTML = html;
  wire();
}

function readB64(file, then) {
  const r = new FileReader();
  r.onload = () => { const b = new Uint8Array(r.result); let s = ''; for (let i = 0; i < b.length; i++) s += String.fromCharCode(b[i]); then(btoa(s)); };
  r.onerror = () => { busy = false; say('err', 'That file could not be read.'); };
  r.readAsArrayBuffer(file);
}

async function put(name, member, file) {
  const q = '/restore/upload?snapshot=' + encodeURIComponent(name) + (member ? '&member=' + encodeURIComponent(member) : '');
  const res = await fetch(q, { method: 'POST', body: file, credentials: 'same-origin' });
  if (!res.ok) throw new Error((await res.text()).trim() || 'the upload was refused');
}

function wire() {
  const k = $('fbr-keys');
  if (k) k.onclick = () => {
    const f = $('fbr-escrow').files[0], pass = $('fbr-pass').value;
    if (!f) return say('err', 'Pick the escrow file first.');
    if (f.size > 65536) return say('err', 'That file is larger than any escrow file.');
    if (!pass) return say('err', 'Type the escrow file\'s passphrase.');
    busy = true; said = null; render();
    readB64(f, b64 => { if (!send({ type: 'restore_new', restore_new: { action: 'keys', file_b64: b64, passphrase: pass } })) { busy = false; say('err', 'Not connected — nothing was sent.'); } });
  };
  const s = $('fbr-send');
  if (s) s.onclick = async () => {
    const one = $('fbr-snap') && $('fbr-snap').files[0];
    const many = $('fbr-folder') ? Array.from($('fbr-folder').files) : [];
    if (!one && !many.length) return say('err', 'Pick the snapshot first.');
    busy = true; said = null; progress = 'Sending…'; render();
    try {
      let name;
      if (one) {
        name = one.name;
        await put(name, '', one);
      } else {
        name = (many[0].webkitRelativePath || '').split('/')[0];
        let n = 0;
        for (const f of many) {
          const rel = (f.webkitRelativePath || f.name).split('/').slice(1).join('/');
          if (!rel) continue;
          await put(name, rel, f);
          progress = 'Sent ' + (++n) + ' of ' + many.length + '…'; render();
        }
      }
      snapshot = name; busy = false; progress = ''; render();
    } catch (e) { busy = false; progress = ''; say('err', String(e.message || e)); }
  };
  const r = $('fbr-restore');
  if (r) r.onclick = () => {
    const p = S.providers[parseInt(($('fb-provider') || {}).value, 10)];
    if (!p) return say('err', 'Choose the provider and model above first: a restored identity needs a mind to run on.');
    busy = true; step = 'restoring'; said = null; render();
    const provider = { provider: p.name, model: $('fb-model').value, api_key: $('fb-apikey').value.trim(), endpoint: p.endpoint };
    if (!send({ type: 'restore_new', restore_new: { action: 'restore', snapshot, provider } })) { busy = false; step = 'snapshot'; say('err', 'Not connected — nothing was sent.'); }
  };
  const c = $('fbr-close');
  if (c) c.onclick = () => { step = 'closed'; said = null; render(); };
}

// applyRestoreNew takes the server's answers.
export function applyRestoreNew(reply) {
  if (!reply) return;
  secretsOK = reply.secrets_ok !== false;
  if (reply.action === 'hello') { render(); return; }
  busy = false;
  if (reply.error) { if (step === 'restoring') step = 'snapshot'; say('err', reply.error); return; }
  if (reply.action === 'keys' && reply.keys) { keys = reply.keys; step = 'snapshot'; said = null; render(); return; }
  if (reply.action === 'restore' && reply.done) {
    step = 'done';
    say('ok', 'Restored through record ' + reply.done.restored_to + '. Opening the identity\'s page…');
    setTimeout(() => location.reload(), 1200);
  }
}

// For the tests.
export function restoreDoorState() { return { step, busy, keys, snapshot, secretsOK }; }
