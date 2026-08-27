/* pending.js — a request the operator is waiting on.

   Five surfaces arm a request id, wait for the answer wearing it, and
   must forget it when the socket dies: the substrate control, the two
   settings saves, and the project create and focus save. Each grew its
   own copy of that, and each copy grew its own bug — the substrate
   banner that said "Checking real inference" forever (2026-08-23), the
   settings banner that did the same on a field the readback could not
   carry, and the create button left inert until a page reload because
   the disconnect sweep listed four pendings and not that one.

   All three are the same two rules, so they live here once:

     CLAIMING ALWAYS CLEARS. The frame wearing our request id IS our
     answer. Deciding it says something we did not want is a rendering
     question; the WAIT is over either way. Every wedge above was an
     early return that left the slot armed with no error and no timeout.

     A SLOT CANNOT BE LEFT OUT OF THE SWEEP. Creating one registers it,
     so dropAllPending() reaches every slot that exists rather than the
     ones a disconnect handler remembered to name. */

const slots = [];

export function pendingSlot() {
  const slot = {
    held: null,
    /* arm returns whether we are now waiting: a send on a closed socket
       yields no request id, and there is nothing to wait for. */
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
  slots.push(slot);
  return slot;
}

/* Every slot, dropped — the disconnect backstop. Surfaces that want to
   SAY something about the loss still do it in their own connectionLost;
   this is what makes forgetting one impossible. */
export function dropAllPending() {
  let dropped = 0;
  for (const slot of slots) if (slot.drop()) dropped++;
  return dropped;
}
