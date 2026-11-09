import { fill } from '../../util.js';
import L from '../../strings/settings-local.js';

export function localCheckbox(local, on, roles) {
  if (local === undefined) return on ? { show: true, line: L.checking, warn: false } : { show: false };
  const usable = !!(local && local.provider && local.model && !local.problem);
  if (!usable && !on) return { show: false };
  const own = (roles || []).filter(Boolean);
  const keep = line => own.length ? line + ' ' + fill(own.length === 1 ? L.ownRouteOne : L.ownRouteMany, { roles: own.join(', ') }) : line;
  if (usable) return { show: true, line: keep(fill(on ? L.willUse : L.wouldUse, { provider: local.provider, model: local.model })), warn: false };
  if (local && local.provider && local.problem) return { show: true, line: keep(fill(L.cannotUse, { provider: local.provider, problem: local.problem })), warn: true };
  return { show: true, line: keep(L.notAnswering), warn: true };
}
