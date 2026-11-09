import { act } from './ws.js';

const COMMANDS = { '/record': 'record.read', '/record/payload': 'record.payload' };

export async function signedFetch(address) {
  const [path, query] = address.split('?');
  try {
    const body = await act(COMMANDS[path], Object.fromEntries(new URLSearchParams(query)));
    return { ok: true, json: async () => body, arrayBuffer: async () => body };
  } catch (err) {
    return { ok: false, status: 0, text: async () => err.message };
  }
}
