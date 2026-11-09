import { pageKey } from '../pagekey.js';

export function hello(socket) {
  try { socket.send(JSON.stringify({ type: 'hello', page_key: pageKey() })); } catch (_) { }
}

const PIECE_BYTES = 16 * 1024, BINARY = 1, LAST = 2;
const QUEUE_HIGH = 1024 * 1024, QUEUE_LOW = 256 * 1024;
const encoder = new TextEncoder(), decoder = new TextDecoder();

export function channelWanted(protocol, hostname, hasPeerConnection) {
  const here = ['127.0.0.1', 'localhost', '[::1]', '::1'].includes(hostname);
  return protocol === 'https:' && !here && !!hasPeerConnection;
}

export function cutIntoPieces(data, pieceBytes) {
  let bytes, head = 0;
  if (typeof data === 'string') bytes = encoder.encode(data);
  else {
    bytes = data instanceof ArrayBuffer ? new Uint8Array(data) : new Uint8Array(data.buffer, data.byteOffset, data.byteLength);
    head = BINARY;
  }
  const room = Math.max(1, pieceBytes - 1), pieces = [];
  let at = 0;
  do {
    const n = Math.min(room, bytes.length - at);
    const piece = new Uint8Array(1 + n);
    piece[0] = head | (at + n === bytes.length ? LAST : 0);
    piece.set(bytes.subarray(at, at + n), 1);
    pieces.push(piece);
    at += n;
  } while (at < bytes.length);
  return pieces;
}

export function pieceJoiner() {
  let parts = [], size = 0, binary = false, started = false;
  return buffer => {
    const piece = new Uint8Array(buffer);
    if (!piece.length) return false;
    const isBinary = !!(piece[0] & BINARY);
    if (started && isBinary !== binary) return false;
    started = true; binary = isBinary;
    parts.push(piece.subarray(1)); size += piece.length - 1;
    if (!(piece[0] & LAST)) return null;
    const whole = new Uint8Array(size);
    let at = 0;
    for (const part of parts) { whole.set(part, at); at += part.length; }
    const message = { data: binary ? whole.buffer : decoder.decode(whole) };
    parts = []; size = 0; started = false;
    return message;
  };
}

class ChannelSocket {
  constructor(pc, dc) {
    this.pc = pc; this.dc = dc;
    this.readyState = 0; this.binaryType = 'arraybuffer';
    this.onmessage = null; this.onclose = null;
    this.queue = []; this.join = pieceJoiner();
    dc.binaryType = 'arraybuffer';
    dc.bufferedAmountLowThreshold = QUEUE_LOW;
    dc.onbufferedamountlow = () => this.pump();
    dc.onopen = () => { if (this.readyState === 0) this.readyState = 1; };
    dc.onclose = () => this.end();
    dc.onmessage = e => {
      const message = this.join(e.data);
      if (message === false) { this.close(); return; }
      if (message && this.onmessage) this.onmessage(message);
    };
    pc.onconnectionstatechange = () => { if (pc.connectionState === 'failed' || pc.connectionState === 'closed') this.end(); };
    this.left = () => this.end();
    window.addEventListener('pagehide', this.left);
  }
  pieceBytes() {
    const stated = this.pc.sctp && this.pc.sctp.maxMessageSize;
    return stated > 1 && stated < PIECE_BYTES ? stated : PIECE_BYTES;
  }
  send(data) {
    if (this.readyState !== 1) throw new Error('the channel is not open');
    for (const piece of cutIntoPieces(data, this.pieceBytes())) this.queue.push(piece);
    this.pump();
  }
  pump() {
    try {
      while (this.queue.length && this.dc.readyState === 'open' && this.dc.bufferedAmount < QUEUE_HIGH) this.dc.send(this.queue.shift());
    } catch (_) { this.close(); }
  }
  close() { this.end(); }
  end() {
    if (this.readyState === 3) return;
    this.readyState = 3; this.queue = [];
    window.removeEventListener('pagehide', this.left);
    try { this.dc.close(); } catch (_) { }
    try { this.pc.close(); } catch (_) { }
    if (this.onclose) this.onclose({});
  }
}

export function settle(socket, adopt, lost, makePeer) {
  let pc = null, channel = null, timer = null, over = false, negotiating = false;
  let offered = false, answered = false, answering = false;
  let readyHere = false, socketClosed = false;
  let localCandidates = [], remoteCandidates = [];
  const finish = () => { over = true; clearTimeout(timer); localCandidates = []; remoteCandidates = []; };
  const drop = () => {
    if (channel) { channel.onclose = null; channel.end(); }
    if (pc) { try { pc.close(); } catch (_) { } }
  };
  const abandon = () => {
    try { socket.close(); } catch (_) { }
    drop();
  };
  const fail = code => { if (over) return; finish(); abandon(); lost(code); };
  const onSocket = () => {
    finish();
    drop();
    adopt(socket);
  };
  const take = () => { if (channel.readyState !== 1) { fail(); return; } finish(); adopt(channel); };
  const say = message => { if (!over && socket.readyState === 1) socket.send(JSON.stringify(message)); };

  const begin = setup => {
    negotiating = true;
    timer = setTimeout(fail, setup.ready_within_ms);
    pc = makePeer({ iceServers: (setup.ice_servers || []).map(s => ({ urls: s.urls, username: s.username, credential: s.credential })) });
    channel = new ChannelSocket(pc, pc.createDataChannel('dashboard', { ordered: true }));
    channel.onmessage = e => {
      if (over) return;
      if (readyHere) { take(); if (channel.onmessage) channel.onmessage(e); return; }
      let message = null;
      try { message = JSON.parse(e.data); } catch (_) { }
      if (!message || message.type !== 'transport_ready') return;
      try { channel.send(JSON.stringify({ type: 'transport_ready', carrier: 'channel' })); } catch (_) { return; }
      readyHere = true;
      if (socketClosed) take();
    };
    pc.onicecandidate = e => {
      if (over) return;
      const message = e.candidate
        ? { type: 'transport_candidate', candidate: e.candidate.candidate, sdp_mid: e.candidate.sdpMid, sdp_mline_index: e.candidate.sdpMLineIndex }
        : { type: 'transport_candidates_done' };
      if (offered) say(message);
      else localCandidates.push(message);
    };
    pc.createOffer()
      .then(offer => pc.setLocalDescription(offer).then(() => {
        if (over) return;
        say({ type: 'transport_offer', sdp: offer.sdp });
        offered = true;
        for (const candidate of localCandidates) say(candidate);
        localCandidates = [];
      }))
      .catch(() => { });
  };

  socket.onopen = () => hello(socket);
  socket.onclose = e => {
    if (over) return;
    socketClosed = true;
    if (!negotiating) fail(e && e.code);
    else if (readyHere) take();
  };
  socket.onmessage = e => {
    if (over) return;
    let message = null;
    if (typeof e.data === 'string') { try { message = JSON.parse(e.data); } catch (_) { } }
    if (!negotiating) {
      if (message && message.type === 'transport_setup' && message.ready_within_ms > 0) { begin(message); return; }
      finish();
      adopt(socket);
      if (socket.onmessage) socket.onmessage(e);
      return;
    }
    if (!message) return;
    if (message.type === 'transport_answer' && !answering) {
      answering = true;
      pc.setRemoteDescription({ type: 'answer', sdp: message.sdp }).then(() => {
        if (over) return;
        answered = true;
        for (const candidate of remoteCandidates) pc.addIceCandidate(candidate).catch(() => {});
        remoteCandidates = [];
      }).catch(() => {});
    } else if (message.type === 'transport_candidate') {
      const candidate = { candidate: message.candidate, sdpMid: message.sdp_mid, sdpMLineIndex: message.sdp_mline_index };
      if (answered) pc.addIceCandidate(candidate).catch(() => {});
      else remoteCandidates.push(candidate);
    }
    else if (message.type === 'transport_ready') onSocket();
  };
  return { readyState: 0, close: () => { if (over) return; finish(); abandon(); } };
}
