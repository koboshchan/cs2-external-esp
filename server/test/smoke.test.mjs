import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { setTimeout as delay } from 'node:timers/promises';
import { test, before, after } from 'node:test';
import { WebSocket } from 'ws';

import { MAGIC, PROTO_VERSION } from '../dist/protocol.js';

const PORT = 8799;
const BASE = `http://127.0.0.1:${PORT}`;
const WS_URL = `ws://127.0.0.1:${PORT}/ws`;

let child;

async function waitForServer(timeoutMs = 5000) {
  const started = Date.now();
  for (;;) {
    try {
      const res = await fetch(`${BASE}/health`);
      if (res.ok) return await res.json();
    } catch {
      // not up yet
    }
    if (Date.now() - started > timeoutMs) throw new Error('server did not start');
    await delay(50);
  }
}

async function createRoom() {
  const res = await fetch(`${BASE}/rooms`, { method: 'POST', body: '{}' });
  assert.equal(res.status, 201);
  return await res.json();
}

function frame(seq, extra = 8) {
  const buf = Buffer.alloc(12 + extra);
  buf.writeUInt32LE(MAGIC, 0);
  buf.writeUInt16LE(PROTO_VERSION, 4);
  buf.writeUInt16LE(1, 6);
  buf.writeUInt32LE(seq, 8);
  for (let i = 12; i < buf.length; i++) buf[i] = (seq + i) & 0xff;
  return buf;
}

function connect() {
  const ws = new WebSocket(WS_URL);
  const messages = [];
  const waiters = [];

  ws.on('message', (data, isBinary) => {
    messages.push({ data: Buffer.from(data), isBinary });
    for (const w of [...waiters]) w();
  });

  const waitFor = async (predicate, timeoutMs = 3000) => {
    const started = Date.now();
    for (;;) {
      const hit = messages.find(predicate);
      if (hit) return hit;
      if (Date.now() - started > timeoutMs) {
        throw new Error(`timed out waiting; got ${JSON.stringify(messages.map((m) => (m.isBinary ? 'bin' : m.data.toString())))}`);
      }
      await new Promise((resolve) => {
        waiters.push(resolve);
        setTimeout(resolve, 50);
      });
    }
  };

  return { ws, messages, waitFor };
}

async function hello(ws, role, room, token, proto = PROTO_VERSION) {
  ws.send(JSON.stringify({ t: 'hello', role, room, token, proto }));
}

async function closed(ws, timeoutMs = 3000) {
  return await Promise.race([
    once(ws, 'close'),
    delay(timeoutMs).then(() => { throw new Error('socket did not close'); }),
  ]);
}

before(async () => {
  child = spawn(process.execPath, ['dist/index.js'], {
    env: { ...process.env, PORT: String(PORT), BIND: '127.0.0.1' },
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  child.stdout.on('data', () => {});
  child.stderr.on('data', (d) => process.stderr.write(d));
  await waitForServer();
});

after(async () => {
  child?.kill('SIGTERM');
  await delay(200);
});

test('room creation returns credentials', async () => {
  const room = await createRoom();
  assert.match(room.room, /^[2-9A-HJKMNP-Z]{6}$/);
  assert.ok(room.hostToken.length >= 20);
  assert.ok(room.viewerKey.length >= 10);
  assert.equal(room.proto, PROTO_VERSION);
});

test('host publishes, viewer receives identical frames', async () => {
  const room = await createRoom();
  const host = connect();
  await once(host.ws, 'open');
  await hello(host.ws, 'host', room.room, room.hostToken);
  const welcome = await host.waitFor((m) => !m.isBinary && m.data.toString().includes('welcome'));
  assert.equal(JSON.parse(welcome.data.toString()).role, 'host');

  const viewer = connect();
  await once(viewer.ws, 'open');
  await hello(viewer.ws, 'viewer', room.room, room.viewerKey);
  await viewer.waitFor((m) => !m.isBinary && m.data.toString().includes('welcome'));

  const frames = [frame(1), frame(2), frame(3)];
  for (const f of frames) host.ws.send(f);

  await viewer.waitFor(() => viewer.messages.filter((m) => m.isBinary).length >= 3);
  const received = viewer.messages.filter((m) => m.isBinary).map((m) => m.data);
  assert.deepEqual(received.at(-1), frames.at(-1));

  host.ws.close();
  viewer.ws.close();
});

test('late viewer gets the cached frame', async () => {
  const room = await createRoom();
  const host = connect();
  await once(host.ws, 'open');
  await hello(host.ws, 'host', room.room, room.hostToken);
  await host.waitFor((m) => !m.isBinary);

  host.ws.send(frame(42));
  await delay(150);

  const viewer = connect();
  await once(viewer.ws, 'open');
  await hello(viewer.ws, 'viewer', room.room, room.viewerKey);

  const cached = await viewer.waitFor((m) => m.isBinary);
  assert.equal(cached.data.readUInt32LE(8), 42);

  host.ws.close();
  viewer.ws.close();
});

test('rejects wrong viewer key', async () => {
  const room = await createRoom();
  const viewer = connect();
  await once(viewer.ws, 'open');
  await hello(viewer.ws, 'viewer', room.room, 'nope');

  const err = await viewer.waitFor((m) => !m.isBinary && m.data.toString().includes('error'));
  assert.equal(JSON.parse(err.data.toString()).code, 'bad_token');
  await closed(viewer.ws);
});

test('rejects a second live host', async () => {
  const room = await createRoom();
  const first = connect();
  await once(first.ws, 'open');
  await hello(first.ws, 'host', room.room, room.hostToken);
  await first.waitFor((m) => !m.isBinary);

  const second = connect();
  await once(second.ws, 'open');
  await hello(second.ws, 'host', room.room, room.hostToken);

  const err = await second.waitFor((m) => !m.isBinary && m.data.toString().includes('error'));
  assert.equal(JSON.parse(err.data.toString()).code, 'room_busy');

  first.ws.close();
  second.ws.close();
});

test('rejects frames with a bad prefix', async () => {
  const room = await createRoom();
  const host = connect();
  await once(host.ws, 'open');
  await hello(host.ws, 'host', room.room, room.hostToken);
  await host.waitFor((m) => !m.isBinary);

  const bad = Buffer.alloc(32);
  bad.writeUInt32LE(0xdeadbeef, 0);
  host.ws.send(bad);

  const err = await host.waitFor((m) => !m.isBinary && m.data.toString().includes('error'));
  assert.equal(JSON.parse(err.data.toString()).code, 'bad_message');

  host.ws.close();
});

test('viewers are notified when the host disconnects', async () => {
  const room = await createRoom();
  const host = connect();
  await once(host.ws, 'open');
  await hello(host.ws, 'host', room.room, room.hostToken);
  await host.waitFor((m) => !m.isBinary);

  const viewer = connect();
  await once(viewer.ws, 'open');
  await hello(viewer.ws, 'viewer', room.room, room.viewerKey);
  await viewer.waitFor((m) => !m.isBinary && m.data.toString().includes('welcome'));

  host.ws.close();
  const offline = await viewer.waitFor((m) => !m.isBinary && m.data.toString().includes('host_offline'));
  assert.ok(offline);

  viewer.ws.close();
});
