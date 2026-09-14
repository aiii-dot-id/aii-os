

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
  return slot;
}
