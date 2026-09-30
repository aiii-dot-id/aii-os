// Device processing belongs to this browser, like its microphone selection.
// Persist only on Save; a live capture keeps the mode it opened with.
export const echoModes = [
  ['browser', 'Browser cancellation'],
  ['combined', 'Browser + native cancellation'],
  ['native', 'Native cancellation only'],
  ['neither', 'Neither (test control)'],
];
const storageKey = 'aii.voice.echo-mode';
export function echoMode() {
  try {
    const saved = localStorage.getItem(storageKey);
    if (echoModes.some(([key]) => key === saved)) return saved;
  } catch (e) { /* Reading unavailable storage keeps the safe default. */ }
  return 'browser';
}
export function setEchoMode(value) {
  if (!echoModes.some(([key]) => key === value)) throw new Error('Unknown echo processing mode');
  localStorage.setItem(storageKey, value);
  if (localStorage.getItem(storageKey) !== value) throw new Error('Browser storage did not retain echo processing');
}
export function nativeEcho(value = echoMode()) { return value === 'native' || value === 'combined'; }
export function browserEcho(value = echoMode()) { return value === 'browser' || value === 'combined'; }
export const referenceRoles = ['capture', 'playback_reference'];
