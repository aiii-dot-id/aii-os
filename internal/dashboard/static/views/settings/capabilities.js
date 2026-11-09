import { esc } from '../../util.js';

const CAP_KIND = { image: 'image', pdf: 'PDF', audio: 'audio', video: 'video', text: 'text', tools: 'tools' };
function capabilityLine(l) {
  return '<div data-cap-line="' + esc(l.kind) + '">' + esc(CAP_KIND[l.kind] || l.kind) + ' — ' + esc(l.state) + ' · ' + esc(l.from) +
    (l.not_sent ? ' · this host does not send it' : '') + '</div>';
}
function capabilityFigure(name, f) {
  f = f || {};
  return '<div data-cap-figure="' + name + '">' + name + ' — ' + (f.tokens ? Number(f.tokens).toLocaleString('en-US') + ' tokens' : 'unknown') + ' · ' + esc(f.from || 'nobody has said') + '</div>';
}
export function capabilitiesHTML(c) {
  if (!c) return '';
  const head = (t) => '<div style="color:var(--dim);margin-top:4px">' + t + '</div>';
  return '<div data-capabilities="' + esc(c.model) + '" style="font-size:11.5px;color:var(--faint);margin-top:8px;line-height:1.6;overflow-wrap:anywhere">' +
    '<div style="font-family:var(--mono)">' + esc(c.model) + '</div>' +
    head('Takes') + (c.takes || []).map(capabilityLine).join('') +
    head('Gives') + (c.gives || []).map(capabilityLine).join('') +
    capabilityLine(Object.assign({}, c.tools, { kind: 'tools' })) +
    capabilityFigure('window', c.window) + capabilityFigure('output limit', c.output_limit) + '</div>';
}
