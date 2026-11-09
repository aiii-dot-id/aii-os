import { S } from '../state.js';
import { V } from './state.js';

export const MODE_PAIRS = {
  interactive: { listen: 'interactive', speak: 'on' },
  earbuds: { listen: 'off', speak: 'on' },
  off: { listen: 'off', speak: 'off' },
  meeting: { listen: 'meeting', speak: 'off' },
};

let modeHeld = { listen: 'interactive', speak: 'auto' };

export function voiceMode() {
  const st = S.stats || {};
  const rev = typeof st.voice_mode_revision === 'number' ? st.voice_mode_revision : 0;
  if (rev < V.modeRev) return { listen: modeHeld.listen, speak: modeHeld.speak, revision: V.modeRev };
  const listen = st.voice_listen === 'off' || st.voice_listen === 'meeting' ? st.voice_listen : 'interactive';
  const speak = st.voice_speak === 'on' || st.voice_speak === 'off' ? st.voice_speak : 'auto';
  V.modeRev = rev; modeHeld = { listen: listen, speak: speak };
  if (listen === 'off' && rev > V.offSeenRev) V.offSeenRev = rev;
  return { listen: listen, speak: speak, revision: rev };
}

export function modeName(listen, speak) {
  const spoken = speak !== 'off';
  if (listen === 'interactive') return spoken ? 'interactive' : 'interactive, text replies';
  if (listen === 'meeting') return spoken ? 'meeting, spoken replies' : 'meeting';
  return spoken ? 'earbuds' : 'off';
}

export function nextMode(m) {
  if (m.listen === 'meeting') return 'interactive';
  if (m.listen === 'off') return m.speak === 'off' ? 'interactive' : 'off';
  return 'earbuds';
}

export function laneMode(listen) { return listen === 'meeting' ? 'meeting' : 'conversation'; }
