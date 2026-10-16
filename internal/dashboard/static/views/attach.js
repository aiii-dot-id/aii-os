import { $ } from '../util.js';
import { S } from '../state.js';
import { toast } from '../app.js';

const DRAWN = /\.(png|jpe?g|gif|webp|svg)$/i;
const isPath = t => !!t && !/^[a-z][a-z0-9+.-]*:(?![\\/])/i.test(t) && !/^[a-z][a-z0-9+.-]+:/i.test(t) && !t.startsWith('//');
const fileURL = (path, download) => '/w?p=' + encodeURIComponent(path) + (download ? '&download=1' : '');
const baseName = path => String(path).split(/[\\/]/).pop();
const sizeText = n => n >= 1 << 20 ? (n / (1 << 20)).toFixed(1) + ' MB' : n >= 1024 ? Math.round(n / 1024) + ' KB' : n + ' bytes';

let chips = [];
function pictureNote(chip, sent) {
  const llm = S.config?.llm || {};
  if (llm.images_shown !== 'yes') return ' · to the folder only: this model is not shown images';
  if (S.thinking) return ' · to the folder only while a turn is running: the identity is told of the file and can read it';
  const why = llm.image_max_bytes && chip.bytes > llm.image_max_bytes ? 'larger than one picture this model is shown (' + sizeText(llm.image_max_bytes) + ')'
    : llm.image_max_side && chip.side > llm.image_max_side ? 'more than the ' + llm.image_max_side + ' pixels a side this model is shown'
    : llm.images_max_count && sent.count >= llm.images_max_count ? 'this message already has the ' + llm.images_max_count + ' pictures one request takes'
    : llm.images_max_bytes && sent.bytes + chip.bytes > llm.images_max_bytes ? 'with it this message\'s pictures pass what one request carries (' + sizeText(llm.images_max_bytes) + ')' : '';
  if (why) return ' · to the folder only: ' + why;
  sent.count++; sent.bytes += chip.bytes;
  return ' · the model will be shown it';
}
function renderChips() {
  const host = $('attach-chips'); if (!host) return;
  host.hidden = !chips.length;
  host.textContent = '';
  const sent = { count: 0, bytes: 0 };
  for (const chip of chips) {
    const el = document.createElement('span'); el.className = 'attach-chip' + (chip.error ? ' error' : chip.path ? '' : ' sending');
    const name = document.createElement('span'); name.className = 'attach-name'; name.textContent = chip.name;
    const note = document.createElement('span'); note.className = 'attach-note';
    const kept = chip.given && chip.given !== chip.name ? ' · kept as ' + chip.name + ', not ' + chip.given : '';
    const shown = chip.path && DRAWN.test(chip.path) && !/\.svg$/i.test(chip.path) ? pictureNote(chip, sent) : '';
    note.textContent = chip.error ? chip.error : chip.path ? sizeText(chip.bytes) + kept + shown : 'sending…';
    const drop = document.createElement('button'); drop.type = 'button'; drop.className = 'attach-drop'; drop.textContent = '×';
    drop.setAttribute('aria-label', 'Remove ' + chip.name);
    drop.title = chip.path ? 'Remove from this message. The file stays in the identity\'s folder at ' + chip.path : 'Remove';
    drop.onclick = () => { chips = chips.filter(c => c !== chip); renderChips(); };
    el.append(name, note, drop); host.appendChild(el);
  }
}
async function take(file) {
  const chip = { name: file.name, bytes: file.size, path: '', error: '', side: 0 };
  chips.push(chip); renderChips();
  if (/^image\//.test(file.type || '') && window.createImageBitmap) createImageBitmap(file).then(b => { chip.side = Math.max(b.width, b.height); if (b.close) b.close(); if (chips.includes(chip)) renderChips(); }).catch(() => {});
  try {
    const res = await fetch('/upload?name=' + encodeURIComponent(file.name), { method: 'POST', body: file });
    if (!res.ok) chip.error = (await res.text()).trim() || 'not taken (HTTP ' + res.status + ')';
    else { const reply = await res.json(); chip.path = reply.path; chip.given = chip.name; chip.name = reply.name; chip.bytes = reply.bytes; }
  } catch (e) {
    chip.error = 'not sent: ' + (e && e.message || e);
  }
  if (chips.includes(chip)) renderChips();
}
export function takeFiles(files) { for (const f of files || []) take(f); }
export function attachmentsForSend(text) {
  if (!chips.length) return [];
  if (chips.some(c => !c.path && !c.error)) { toast('A file is still being sent. Send the message when it is taken.'); return null; }
  if (chips.some(c => c.error)) { toast('A file was not taken. Remove it from the message, or attach it again.'); return null; }
  if (!text) { toast('Say what to do with the file, then send.'); return null; }
  return chips.map(c => c.path);
}
export function attachmentsSent() { chips = []; renderChips(); }
export function wireAttach() {
  const btn = $('attach-btn'), input = $('attach-input'), box = $('msg-input');
  if (!btn || !input) return;
  S.chipsRedraw = renderChips;
  btn.onclick = () => input.click();
  input.onchange = () => { takeFiles(input.files); input.value = ''; };
  const wrap = $('composer-wrap') || box;
  if (wrap) {
    wrap.addEventListener('dragover', e => { if (e.dataTransfer && [...e.dataTransfer.types].includes('Files')) e.preventDefault(); });
    wrap.addEventListener('drop', e => { if (e.dataTransfer && e.dataTransfer.files.length) { e.preventDefault(); takeFiles(e.dataTransfer.files); } });
  }
  if (box) box.addEventListener('paste', e => { if (e.clipboardData && e.clipboardData.files.length) { e.preventDefault(); takeFiles(e.clipboardData.files); } });
}

const SHOWN = /^\s*(!?)\[([^\]\n]*)\]\(((?:[^()\n]|\([^()\n]*\)|[()](?=[^\s()]))+)\)\s*$/;
export function shownLine(line) {
  const m = SHOWN.exec(line); if (!m) return null;
  const path = m[3].trim(); if (!isPath(path)) return null;
  return { image: m[1] === '!', label: m[2].trim() || baseName(path), path };
}
function fileChip(path, label) {
  const a = document.createElement('a'); a.className = 'file-chip'; a.href = fileURL(path, true); a.download = baseName(path);
  a.textContent = label || baseName(path); a.title = 'Download ' + path;
  a.onclick = async e => {
    e.preventDefault();
    let why = '';
    try {
      const res = await fetch(a.href);
      if (res.ok) {
        const url = URL.createObjectURL(await res.blob()), save = document.createElement('a');
        save.href = url; save.download = baseName(path); save.click();
        setTimeout(() => URL.revokeObjectURL(url), 60000);
      } else why = res.status === 404 ? 'not available at ' + path : (await res.text()).trim() || 'not handed over (HTTP ' + res.status + ')';
    } catch (err) { why = 'not handed over: ' + (err && err.message || err); }
    let note = a.nextElementSibling;
    if (!note || !note.classList.contains('shown-missing')) { note = document.createElement('span'); note.className = 'shown-missing'; a.after(note); }
    note.textContent = why ? ' ' + why : '';
  };
  return a;
}
function shownImage(path, label) {
  const fig = document.createElement('figure'); fig.className = 'shown-image';
  const link = document.createElement('a'); link.href = fileURL(path, false); link.target = '_blank'; link.rel = 'noopener noreferrer';
  const img = document.createElement('img'); img.alt = label; img.loading = 'lazy'; img.src = fileURL(path, false);
  img.onerror = () => { fig.textContent = ''; const gone = document.createElement('span'); gone.className = 'shown-missing'; gone.textContent = label + ' (image not available at ' + path + ')'; fig.appendChild(gone); };
  link.appendChild(img); fig.appendChild(link);
  if (label) { const cap = document.createElement('figcaption'); cap.textContent = label; fig.appendChild(cap); }
  return fig;
}
export function drawReply(body, content) {
  const lines = String(content || '').split('\n');
  if (!lines.some(l => shownLine(l))) { if (body.dataset.shown) { delete body.dataset.shown; body.textContent = ''; } return false; }
  if (body.dataset.shown === content) return true;
  body.dataset.shown = content; body.textContent = '';
  let text = [];
  const flush = () => { if (text.length) { body.appendChild(document.createTextNode(text.join('\n') + '\n')); text = []; } };
  for (const line of lines) {
    const shown = shownLine(line);
    if (!shown) { text.push(line); continue; }
    flush();
    body.appendChild(shown.image && DRAWN.test(shown.path) ? shownImage(shown.path, shown.label) : fileChip(shown.path, shown.label));
  }
  flush();
  return true;
}
export function drawAttached(label, path) {
  if (label.dataset.attached === path) return;
  label.dataset.attached = path; label.textContent = '';
  if (DRAWN.test(path)) { const img = document.createElement('img'); img.className = 'attached-thumb'; img.alt = ''; img.loading = 'lazy'; img.src = fileURL(path, false); img.onerror = () => img.remove(); label.appendChild(img); }
  label.appendChild(fileChip(path, baseName(path)));
}
