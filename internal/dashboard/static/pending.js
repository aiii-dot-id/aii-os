

const slots = new Set();
export function anyPending() {
  for (const slot of slots) if (slot.held) return true;
  return false;
}

export function pendingSlot() {
  const slot = {
    held: null,

    arm(payload, requestID) {
      slot.held = requestID ? Object.assign({ requestID: requestID }, payload) : null;
      return !!requestID;
    },
    waiting() { return slot.held; },
    claim(requestID) {
      const held = slot.held;
      if (!held || !requestID || held.requestID !== requestID) return null;
      slot.held = null;
      return held;
    },
    drop() { const held = slot.held; slot.held = null; return held; },
  };
  slots.add(slot);
  return slot;
}

export const NOT_SENT = 'Not connected — your answer was not sent.';
export const LOST = 'Connection lost — your answer may not have arrived. If this card is still here after reconnecting, answer again.';
export const notTaken = why => 'Not taken: ' + why;

export function pendingKeyed() {
  const waits = new Map();
  const keyed = {
    get held() { return waits.size > 0; },
    sent(key, requestID) {
      if (!requestID) return false;
      waits.set(key, requestID);
      return true;
    },
    waiting(key) { return waits.has(key); },
    refused(requestID) {
      if (!requestID) return null;
      for (const [key, id] of waits) {
        if (id === requestID) { waits.delete(key); return key; }
      }
      return null;
    },
    lost() { const keys = [...waits.keys()]; waits.clear(); return keys; },
  };
  slots.add(keyed);
  return keyed;
}
