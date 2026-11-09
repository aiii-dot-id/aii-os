import { S } from './state.js';
import { $, esc, b64 } from './util.js';
import { renderInto } from './announce.js';
import { send } from './ws.js';
import { holdReloadWhile } from './overlay.js';
import { signedFetch } from './pagekey.js';

let step = 'closed';
let back = 'keys';
let busy = false;
let act = '';
let said = null;
let begun = null;
let keys = null;
let snapshot = '';
let progress = '';
let secretsOK = true;
holdReloadWhile(() => busy || step === 'snapshot' || step === 'restoring' || !!(document.getElementById('fbr-pass') || {}).value);

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
    '<p class="lead">Moving an identity here from another computer? You need the backup file and its passphrase.</p>';
  if (!secretsOK) {
    html += '<div class="err" data-nosecrets data-announce>To restore, open this page on the computer where AII OS runs.</div>';
    renderInto(el, html); return;
  }
  html += '<div data-step="keys"><label class="f">1 · THE ESCROW FILE</label>';
  if (keys) {
    html += '<div class="ok" data-keys-ok data-announce>This identity\'s keys are on this computer.</div>';
  } else {
    if (begun) html += '<div class="err" data-begun data-announce>A restore was started here and not finished. To go on with it, open the escrow file again below; to end it, press "Withdraw these keys".</div>';
    html += '<input type="file" id="fbr-escrow" accept=".age">' +
      '<label class="f">ITS PASSPHRASE</label><input type="password" id="fbr-pass" autocomplete="off">' +
      '<div style="margin-top:10px"><button class="btn" id="fbr-keys"' + (busy ? ' disabled' : '') + '>' + (busy && act === 'opening' ? 'Opening…' : 'Open it') + '</button></div>';
  }
  html += '</div>';
  if (keys) {
    html += '<div data-step="snapshot" style="margin-top:16px"><label class="f">2 · THE SNAPSHOT</label>';
    if (snapshot) {
      html += '<div class="ok" data-snapshot-ok data-announce>This backup is already here.</div>';
    } else {
      html += (keys.holds_snapshot_key
        ? '<div class="fb-hint">The encrypted snapshot file, named <span style="font-family:var(--mono)">ledger-&lt;date&gt;-seq&lt;N&gt;.tar.age</span>. Do not rename it.</div><input type="file" id="fbr-snap" accept=".age">'
        : '<div class="fb-hint">This escrow was made before snapshots were encrypted, so pick a plaintext snapshot\'s FOLDER, named <span style="font-family:var(--mono)">ledger-&lt;date&gt;-seq&lt;N&gt;</span>.</div>') +
        '<div class="fb-hint" style="margin-top:8px">Or a plaintext snapshot\'s folder:</div><input type="file" id="fbr-folder" webkitdirectory>' +
        '<div style="margin-top:10px"><button class="btn" id="fbr-send"' + (busy ? ' disabled' : '') + '>' + (busy && act === 'sending' ? esc(progress || 'Sending…') : 'Send it here') + '</button></div>';
    }
    html += '</div>';
  }
  if (keys && snapshot) {
    html += '<div data-step="restore" style="margin-top:16px"><label class="f">3 · RESTORE</label>' +
      '<div class="fb-hint" data-restore-says>The snapshot is proved before anything is put in place, and held to the keys above: another identity\'s snapshot is refused. Anything after this backup was made is not in it. If the witness attested records this snapshot does not hold, the next anchor is refused as a rollback and the identity enters SAFE until you deal with it — see Backups after the restore.</div>' +
      '<div style="margin-top:10px"><button class="btn" id="fbr-restore" style="width:100%"' + (busy ? ' disabled' : '') + '>' + (step === 'restoring' ? 'Proving and restoring…' : 'Restore') + '</button></div></div>';
  }
  if (said) html += '<div class="' + (said.kind === 'ok' ? 'ok' : 'err') + '" data-restore-said data-announce style="margin-top:12px">' + esc(said.text) + '</div>';
  if (keys || begun) html += '<div style="margin-top:14px"><button class="btn ghost" id="fbr-withdraw" type="button"' + (busy ? ' disabled' : '') + '>' + (step === 'withdrawing' ? 'Withdrawing…' : 'Withdraw these keys') + '</button></div>';
  html += '<div style="margin-top:14px"><button class="btn ghost" id="fbr-close" type="button"' + (busy ? ' disabled' : '') + '>Back to Birth</button></div>';
  renderInto(el, html);
  wire();
}

function readB64(file, then) {
  const r = new FileReader();
  r.onload = () => then(b64(r.result));
  r.onerror = () => { busy = false; say('err', 'That file could not be read.'); };
  r.readAsArrayBuffer(file);
}

async function put(name, member, file) {
  const q = '/restore/upload?snapshot=' + encodeURIComponent(name) + (member ? '&member=' + encodeURIComponent(member) : '');
  const res = await signedFetch(q, { method: 'POST', body: file, credentials: 'same-origin' });
  if (!res.ok) throw new Error((await res.text()).trim() || 'the upload was refused');
}

function wire() {
  const k = $('fbr-keys');
  if (k) k.onclick = () => {
    const f = $('fbr-escrow').files[0], pass = $('fbr-pass').value;
    if (!f) return say('err', 'Pick the escrow file first.');
    if (f.size > 65536) return say('err', 'That file is larger than any escrow file.');
    if (!pass) return say('err', 'Type the escrow file\'s passphrase.');
    busy = true; act = 'opening'; said = null; render();
    readB64(f, b64 => { if (!send({ type: 'restore_new', restore_new: { action: 'keys', file_b64: b64, passphrase: pass } })) { busy = false; say('err', 'Not connected — nothing was sent.'); } });
  };
  const s = $('fbr-send');
  if (s) s.onclick = async () => {
    const one = $('fbr-snap') && $('fbr-snap').files[0];
    const many = $('fbr-folder') ? Array.from($('fbr-folder').files) : [];
    if (!one && !many.length) return say('err', 'Pick the snapshot first.');
    busy = true; act = 'sending'; said = null; progress = 'Sending…'; render();
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
    busy = true; act = 'restoring'; step = 'restoring'; said = null; render();
    const provider = { provider: p.name, model: $('fb-model').value, api_key: $('fb-apikey').value.trim(), endpoint: p.local ? $('fb-url').value.trim() : p.endpoint };
    if (!send({ type: 'restore_new', restore_new: { action: 'restore', snapshot, provider } })) { busy = false; step = 'snapshot'; say('err', 'Not connected — nothing was sent.'); }
  };
  const w = $('fbr-withdraw');
  if (w) w.onclick = () => {
    const got = received();
    const only = got.length === 1 ? 'Only the keys this page put here from your escrow file are removed, with the snapshot this page received (' + got[0] + '); your own copy of it is not touched.'
      : got.length ? 'Only the keys this page put here from your escrow file are removed, with the snapshots this page received (' + got.join(', ') + '); your own copies of them are not touched.'
      : 'Only the keys this page put here from your escrow file are removed.';
    if (!window.confirm('Remove this identity\'s keys from this computer? ' + only + ' You can\'t restore it here until you add them again.')) return;
    back = step;
    busy = true; act = 'withdrawing'; step = 'withdrawing'; said = null; render();
    if (!send({ type: 'restore_new', restore_new: { action: 'withdraw' } })) { busy = false; step = back; say('err', 'Not connected — nothing was sent.'); }
  };
  const c = $('fbr-close');
  if (c) c.onclick = () => { step = 'closed'; said = null; render(); };
}

function received() {
  const got = ((begun && begun.received) || []).slice();
  if (snapshot && !got.includes(snapshot)) got.push(snapshot);
  return got;
}

export function applyRestoreNew(reply) {
  if (!reply) return;
  secretsOK = reply.secrets_ok !== false;
  if (reply.action === 'hello') {
    if (secretsOK) {
      begun = reply.begun || null;
      if (keys && (!begun || begun.identity !== keys.identity)) { keys = null; if (step === 'snapshot') step = 'keys'; }
    }
    render(); return;
  }
  busy = false;
  if (reply.error) { if (step === 'restoring') step = 'snapshot'; else if (step === 'withdrawing') step = back; say('err', reply.error); return; }
  if (reply.action === 'withdraw' && reply.withdrawn) {
    begun = null; keys = null; snapshot = ''; progress = ''; said = null; step = 'closed';
    render();
    const el = $('fb-result');
    if (el) el.innerHTML = '<div class="ok" data-withdrawn>' + esc('This identity\'s keys were removed from this computer. Your escrow file still holds them. You can birth a new identity here now, or restore one.') + '</div>';
    return;
  }
  if (reply.action === 'keys' && reply.keys) { keys = reply.keys; begun = { identity: keys.identity, received: (begun && begun.received) || [] }; step = 'snapshot'; said = null; render(); return; }
  if (reply.action === 'restore' && reply.done) {
    step = 'done';
    say('ok', 'Restored through record ' + reply.done.restored_to + '. Opening the identity\'s page…');
    setTimeout(() => location.reload(), 1200);
  }
}

export function restoreDoorState() { return { step, busy, begun, keys, snapshot, secretsOK }; }
