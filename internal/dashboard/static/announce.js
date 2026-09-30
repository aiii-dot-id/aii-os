// The frame's one voice for assistive technology.
//
// A screen reader is told of a change when words appear in, or change inside,
// a live region that was already there. A line that is one element for as long
// as its words change (#p-state, #toast, the Expert row) is its own region and
// needs nothing from here. A line that a view makes together with its words
// (an innerHTML rebuild of Settings, a notice that comes and goes with its
// element) is a new region carrying its words: it announces nothing, and one
// that keeps a live role says the same words again at every rebuild. So such a
// line carries no role. It says data-announce, and what it now says that it
// did not before is written, once, into #announce — the one region in the page
// that no view rebuilds (WCAG success criterion 4.1.3, status messages).

// Everything announced in one task is said, once. A lost connection leaves a
// message and a save each saying so, and the second must not replace the first
// before anything has read it; a later task replaces them both. Each
// announcement is a new text node rather than an edit of the last one's, so
// the same words in two tasks are two additions to the region and not one
// unchanged text.
let sameTask = false;
export function announce(text) {
  const region = document.getElementById('announce');
  if (!region || !text) return;
  if (sameTask) { if (!region.textContent.includes(text)) region.append(' ', text); return; }
  region.replaceChildren(text);
  sameTask = true;
  queueMicrotask(() => { sameTask = false; });
}

// A reply, or an ask, is said once as it arrives: who, then its own words.
// The identity's replies run to thousands of characters and open with a
// paragraph of about two hundred, so only the opening paragraph is said, at
// most SAID characters of it, cut between words; whatever is left unsaid is
// declared, with where it is.
const SAID = 400;
export function announceWords(who, words) {
  const whole = (words || '').replace(/\s+/g, ' ').trim();
  if (!whole) return;
  let opening = words.trim().split(/\n\s*\n/)[0].replace(/\s+/g, ' ').trim();
  if (opening.length > SAID) opening = opening.slice(0, SAID + 1).replace(/\s+\S*$/, '').slice(0, SAID);
  announce(who + ': ' + opening + (opening.length < whole.length ? '… The rest is in the conversation.' : ''));
}

const said = root => new Set([...root.querySelectorAll('[data-announce]')].map(el => el.textContent.trim()).filter(Boolean));

// renderInto replaces root's contents with html and announces the
// data-announce lines that say something new. quiet is for a rebuild that is
// navigation rather than news: opening a section is not a status.
export function renderInto(root, html, quiet = false) {
  const before = said(root);
  root.innerHTML = html;
  if (!quiet) announce([...said(root)].filter(text => !before.has(text)).join(' '));
}
