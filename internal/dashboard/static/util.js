/* util.js — tiny shared DOM/string helpers, no state, no wiring.
   Owns: $ (id lookup), esc (HTML escaping — every rendered string
   passes through it), hueOf (stable per-id hue). R66: stays in
   frame; sections get their own copy via the section kit, so this
   file never becomes a cross-boundary API. */

export const $ = id => document.getElementById(id);
export const esc = s => String(s == null ? '' : s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
export const hueOf = id => { let h = 0; for (let i = 0; i < id.length; i++) h = (h * 31 + id.charCodeAt(i)) >>> 0; return h % 360; };
