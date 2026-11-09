import { pendingSlot } from '../../pending.js';
export const config = pendingSlot();
export const edits = new Map();
export const draft = { stt: null, tts: null, sttLeft: false };
export const asked = {};
