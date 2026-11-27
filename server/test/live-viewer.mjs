// Manual viewer used to verify the relay end to end.
//
//   node test/live-viewer.mjs ws://127.0.0.1:8080/ws ROOM VIEWER_KEY [seconds]
//
// Prints frame/byte counters and any sequence gaps when it exits.

import { WebSocket } from 'ws';

import { PROTO_VERSION } from '../dist/protocol.js';

const [url, room, key, seconds = '5'] = process.argv.slice(2);

if (!url || !room || !key) {
  console.error('usage: node test/live-viewer.mjs ws://host:port/ws ROOM VIEWER_KEY [seconds]');
  process.exit(2);
}

const ws = new WebSocket(url);
let frames = 0;
let bytes = 0;
let lastSeq = -1;
let gaps = 0;
let control = 0;

ws.on('open', () => {
  ws.send(JSON.stringify({ t: 'hello', role: 'viewer', room, token: key, proto: PROTO_VERSION }));
});

ws.on('message', (data, isBinary) => {
  if (!isBinary) {
    control++;
    console.log('[control]', data.toString());
    return;
  }
  frames++;
  bytes += data.length;
  const seq = data.readUInt32LE(8);
  if (lastSeq >= 0 && seq !== lastSeq + 1) gaps++;
  lastSeq = seq;
});

ws.on('error', (err) => {
  console.error('[error]', err.message);
});

setTimeout(() => {
  console.log(JSON.stringify({ frames, bytes, lastSeq, gaps, control }));
  ws.close();
  process.exit(frames > 0 ? 0 : 1);
}, Number(seconds) * 1000);
