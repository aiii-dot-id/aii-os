const KEY = 'aii.visitArrival';
export const reloaded = () => {
  try { return performance.getEntriesByType('navigation')[0]?.type === 'reload'; } catch (err) { return false; }
};
export function arrivalOfThisVisit() {
  try {
    const held = reloaded() ? sessionStorage.getItem(KEY) : null;
    if (held) return JSON.parse(held);
    const arrival = { at: Number(localStorage.getItem('aii.lastSeenAt')) || 0, seq: Number(localStorage.getItem('aii.lastSeenSeq')) || 0 };
    sessionStorage.setItem(KEY, JSON.stringify(arrival));
    return arrival;
  } catch (err) { return { at: 0, seq: 0 }; }
}
