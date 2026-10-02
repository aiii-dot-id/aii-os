

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
