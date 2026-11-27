import { readFileSync } from 'node:fs';
import http from 'node:http';
import https from 'node:https';
import type { Duplex } from 'node:stream';
import { WebSocketServer, WebSocket } from 'ws';

import { RateLimiter } from './limits.js';
import {
  ERR,
  MAX_FRAME_BYTES,
  PROTO_VERSION,
  parseFramePrefix,
  type ErrorMessage,
  type HelloMessage,
  type Role,
} from './protocol.js';
import { RoomRegistry, hostIsStale, type Room } from './rooms.js';

const PORT = Number(process.env.PORT ?? 8080);
const BIND = process.env.BIND ?? '0.0.0.0';
const MAX_VIEWERS = Number(process.env.MAX_VIEWERS ?? 8);
const HOST_TIMEOUT_MS = Number(process.env.HOST_TIMEOUT_MS ?? 3000);
const HELLO_TIMEOUT_MS = Number(process.env.HELLO_TIMEOUT_MS ?? 5000);
const ROOM_IDLE_MS = Number(process.env.ROOM_IDLE_MS ?? 10 * 60_000);
const MAX_FRAME_BYTES_ENV = Number(process.env.MAX_FRAME_BYTES ?? MAX_FRAME_BYTES);
const MAX_BUFFERED_BYTES = 256 * 1024;

const TLS_CERT = process.env.TLS_CERT;
const TLS_KEY = process.env.TLS_KEY;

const registry = new RoomRegistry(MAX_VIEWERS);
const startedAt = Date.now();

function log(event: string, fields: Record<string, unknown> = {}): void {
  process.stdout.write(JSON.stringify({ ts: new Date().toISOString(), event, ...fields }) + '\n');
}

function sendError(ws: WebSocket, code: string, message: string): void {
  const payload: ErrorMessage = { t: 'error', code, message };
  if (ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify(payload));
}

function notifyViewers(room: Room): void {
  if (!room.host || room.host.readyState !== WebSocket.OPEN) return;
  room.host.send(JSON.stringify({ t: 'viewers', count: room.viewers.size }));
}

function broadcast(room: Room, data: string | Buffer, binary: boolean): number {
  let sent = 0;
  for (const viewer of room.viewers.keys()) {
    if (viewer.readyState !== WebSocket.OPEN) continue;
    if (viewer.bufferedAmount > MAX_BUFFERED_BYTES) continue; // slow viewer: drop
    viewer.send(data, { binary });
    sent++;
  }
  return sent;
}

interface Session {
  role: Role | null;
  room: Room | null;
  limiter: RateLimiter;
  alive: boolean;
  helloTimer: NodeJS.Timeout | null;
}

function onFrame(ws: WebSocket, session: Session, data: Buffer): void {
  if (!session.room || session.role !== 'host') {
    sendError(ws, ERR.notHost, 'only a host may publish frames');
    return;
  }
  if (!session.limiter.take()) {
    sendError(ws, ERR.rateLimited, 'too many messages');
    return;
  }
  if (data.length > MAX_FRAME_BYTES_ENV) {
    sendError(ws, ERR.frameTooLarge, `frame exceeds ${MAX_FRAME_BYTES_ENV} bytes`);
    return;
  }

  const prefix = parseFramePrefix(data);
  if (!prefix) {
    log('frame_rejected', { room: session.room.id, reason: 'prefix', bytes: data.length });
    sendError(ws, ERR.badMessage, 'invalid frame prefix');
    return;
  }

  const room = session.room;
  const now = Date.now();
  const wasOffline = !room.hostOnline;

  room.lastFrame = data;
  room.lastFrameAt = now;
  room.lastSeq = prefix.seq;
  room.hostSeenAt = now;
  room.hostOnline = true;
  room.touchedAt = now;

  if (wasOffline) {
    broadcast(room, JSON.stringify({ t: 'host_online', room: room.id }), false);
    log('host_online', { room: room.id });
  }

  broadcast(room, data, true);
}

function onControl(ws: WebSocket, session: Session, msg: Record<string, unknown>): void {
  const t = msg.t;

  if (t === 'ping') {
    ws.send(JSON.stringify({ t: 'pong', ts: Date.now() }));
    return;
  }

  if (t === 'select') {
    // Viewer identity selection. Reserved for per-viewer projections later;
    // currently informational only.
    if (session.room) session.room.touchedAt = Date.now();
    return;
  }

  sendError(ws, ERR.badMessage, `unknown message type: ${String(t)}`);
}

function handleHello(ws: WebSocket, session: Session, msg: HelloMessage): void {
  if (session.role) {
    sendError(ws, ERR.badMessage, 'already joined');
    return;
  }
  if (msg.proto !== PROTO_VERSION) {
    sendError(ws, ERR.protoMismatch, `server speaks protocol ${PROTO_VERSION}`);
    ws.close(4002, 'proto_mismatch');
    return;
  }

  const room = registry.get(String(msg.room ?? ''));
  if (!room) {
    sendError(ws, ERR.roomNotFound, 'room does not exist');
    ws.close(4004, 'room_not_found');
    return;
  }

  const now = Date.now();

  if (msg.role === 'host') {
    if (String(msg.token ?? '') !== room.hostToken) {
      sendError(ws, ERR.badToken, 'invalid host token');
      ws.close(4003, 'bad_token');
      return;
    }
    if (room.host && room.host !== ws && !hostIsStale(room, now, HOST_TIMEOUT_MS)) {
      sendError(ws, ERR.roomBusy, 'room already has a host');
      ws.close(4009, 'room_busy');
      return;
    }
    if (room.host && room.host !== ws) {
      room.host.close(4001, 'replaced');
    }

    room.host = ws;
    room.hostSeenAt = now;
    room.hostOnline = true;
    room.touchedAt = now;

    session.role = 'host';
    session.room = room;

    ws.send(JSON.stringify({
      t: 'welcome',
      role: 'host',
      room: room.id,
      proto: PROTO_VERSION,
      viewers: room.viewers.size,
    }));

    broadcast(room, JSON.stringify({ t: 'host_online', room: room.id }), false);
    notifyViewers(room);
    log('host_join', { room: room.id, viewers: room.viewers.size });
    return;
  }

  if (msg.role === 'viewer') {
    if (String(msg.token ?? '') !== room.viewerKey) {
      sendError(ws, ERR.badToken, 'invalid viewer key');
      ws.close(4003, 'bad_token');
      return;
    }
    if (room.viewers.size >= room.maxViewers) {
      sendError(ws, ERR.tooManyViewers, 'room is full');
      ws.close(4008, 'too_many_viewers');
      return;
    }

    room.viewers.set(ws, now);
    room.touchedAt = now;

    session.role = 'viewer';
    session.room = room;

    ws.send(JSON.stringify({
      t: 'welcome',
      role: 'viewer',
      room: room.id,
      proto: PROTO_VERSION,
      host_online: room.hostOnline,
      viewers: room.viewers.size,
      seq: room.lastSeq,
    }));

    if (room.lastFrame) ws.send(room.lastFrame, { binary: true });

    notifyViewers(room);
    log('viewer_join', { room: room.id, viewers: room.viewers.size, host_online: room.hostOnline });
    return;
  }

  sendError(ws, ERR.badMessage, 'role must be host or viewer');
}

function onClose(ws: WebSocket, session: Session): void {
  const room = session.room;
  if (!room) return;

  if (session.role === 'host' && room.host === ws) {
    room.host = null;
    room.hostOnline = false;
    room.touchedAt = Date.now();
    broadcast(room, JSON.stringify({ t: 'host_offline', room: room.id }), false);
    log('host_leave', { room: room.id });
    return;
  }

  if (session.role === 'viewer') {
    room.viewers.delete(ws);
    room.touchedAt = Date.now();
    notifyViewers(room);
    log('viewer_leave', { room: room.id, viewers: room.viewers.size });
  }
}

function createServer(): http.Server {
  const handler = (req: http.IncomingMessage, res: http.ServerResponse): void => {
    const url = new URL(req.url ?? '/', `http://${req.headers.host ?? 'localhost'}`);

    if (req.method === 'GET' && url.pathname === '/health') {
      res.writeHead(200, { 'content-type': 'application/json' });
      res.end(JSON.stringify({
        ok: true,
        uptime_s: Math.round((Date.now() - startedAt) / 1000),
        rooms: registry.size,
        viewers: registry.viewerCount(),
        proto: PROTO_VERSION,
      }));
      return;
    }

    if (req.method === 'POST' && url.pathname === '/rooms') {
      let body = '';
      req.on('data', (chunk) => {
        body += chunk;
        if (body.length > 4096) req.destroy();
      });
      req.on('end', () => {
        let maxViewers: number | undefined;
        if (body) {
          try {
            const parsed = JSON.parse(body) as { maxViewers?: unknown };
            if (typeof parsed.maxViewers === 'number') maxViewers = parsed.maxViewers;
          } catch {
            res.writeHead(400, { 'content-type': 'application/json' });
            res.end(JSON.stringify({ error: 'invalid_json' }));
            return;
          }
        }

        const room = registry.create(maxViewers === undefined ? undefined : { maxViewers });
        log('room_create', { room: room.id, max_viewers: room.maxViewers });

        res.writeHead(201, { 'content-type': 'application/json' });
        res.end(JSON.stringify({
          room: room.id,
          hostToken: room.hostToken,
          viewerKey: room.viewerKey,
          maxViewers: room.maxViewers,
          proto: PROTO_VERSION,
        }));
      });
      return;
    }

    res.writeHead(404, { 'content-type': 'application/json' });
    res.end(JSON.stringify({ error: 'not_found' }));
    log('http_not_found', {
      method: req.method,
      path: url.pathname,
      upgrade: req.headers.upgrade ?? null,
      connection: req.headers.connection ?? null,
      websocket_key: req.headers['sec-websocket-key'] ? 'yes' : 'no',
    });
  };

  if (TLS_CERT && TLS_KEY) {
    return https.createServer({ cert: readFileSync(TLS_CERT), key: readFileSync(TLS_KEY) }, handler);
  }
  return http.createServer(handler);
}

const server = createServer();
const wss = new WebSocketServer({ noServer: true, maxPayload: MAX_FRAME_BYTES_ENV + 1024 });
const sessions = new WeakMap<WebSocket, Session>();

server.on('upgrade', (req: http.IncomingMessage, socket: Duplex, head: Buffer) => {
  const url = new URL(req.url ?? '/', `http://${req.headers.host ?? 'localhost'}`);
  if (url.pathname !== '/ws') {
    socket.write('HTTP/1.1 404 Not Found\r\n\r\n');
    socket.destroy();
    return;
  }
  wss.handleUpgrade(req, socket, head, (ws) => wss.emit('connection', ws, req));
});

wss.on('connection', (ws: WebSocket) => {
  const session: Session = {
    role: null,
    room: null,
    limiter: new RateLimiter(240, 60),
    alive: true,
    helloTimer: null,
  };
  sessions.set(ws, session);

  session.helloTimer = setTimeout(() => {
    sendError(ws, ERR.badMessage, 'hello timeout');
    ws.close(4000, 'hello_timeout');
  }, HELLO_TIMEOUT_MS);

  ws.on('pong', () => {
    session.alive = true;
  });

  ws.on('message', (data: Buffer | ArrayBuffer | Buffer[], isBinary: boolean) => {
    const buf = Array.isArray(data) ? Buffer.concat(data) : Buffer.isBuffer(data) ? data : Buffer.from(data);

    if (isBinary) {
      onFrame(ws, session, buf);
      return;
    }

    let msg: Record<string, unknown>;
    try {
      msg = JSON.parse(buf.toString('utf8')) as Record<string, unknown>;
    } catch {
      sendError(ws, ERR.badMessage, 'invalid json');
      return;
    }

    if (!session.limiter.take()) {
      sendError(ws, ERR.rateLimited, 'too many messages');
      return;
    }

    if (msg.t === 'hello') {
      if (session.helloTimer) {
        clearTimeout(session.helloTimer);
        session.helloTimer = null;
      }
      handleHello(ws, session, msg as unknown as HelloMessage);
      return;
    }

    if (!session.role) {
      sendError(ws, ERR.badMessage, 'hello required');
      return;
    }

    onControl(ws, session, msg);
  });

  ws.on('close', () => {
    if (session.helloTimer) clearTimeout(session.helloTimer);
    onClose(ws, session);
  });

  ws.on('error', (err) => {
    log('ws_error', { error: err.message, room: session.room?.id ?? null });
  });
});

const heartbeat = setInterval(() => {
  const now = Date.now();

  for (const room of registry.all()) {
    if (room.host && room.hostOnline && now - room.hostSeenAt > HOST_TIMEOUT_MS) {
      room.hostOnline = false;
      broadcast(room, JSON.stringify({ t: 'host_offline', room: room.id }), false);
      log('host_timeout', { room: room.id });
    }

    for (const viewer of room.viewers.keys()) {
      const session = sessions.get(viewer);
      if (!session) continue;
      if (!session.alive) {
        viewer.terminate();
        continue;
      }
      session.alive = false;
      viewer.ping();
    }

    // Host liveness comes from frames, so a ping is only advisory.
    if (room.host && room.host.readyState === WebSocket.OPEN) room.host.ping();
  }
}, 5000);

const sweeper = setInterval(() => {
  const removed = registry.sweep(ROOM_IDLE_MS);
  if (removed > 0) log('room_sweep', { removed });
}, 60_000);

server.listen(PORT, BIND, () => {
  const scheme = TLS_CERT && TLS_KEY ? 'wss' : 'ws';
  log('listening', { port: PORT, bind: BIND, scheme, proto: PROTO_VERSION });
});

function shutdown(signal: string): void {
  log('shutdown', { signal });
  clearInterval(heartbeat);
  clearInterval(sweeper);
  for (const room of registry.all()) {
    room.host?.close(1001, 'server_shutdown');
    for (const viewer of room.viewers.keys()) viewer.close(1001, 'server_shutdown');
  }
  wss.close();
  server.close(() => process.exit(0));
  setTimeout(() => process.exit(0), 2000).unref();
}

process.on('SIGINT', () => shutdown('SIGINT'));
process.on('SIGTERM', () => shutdown('SIGTERM'));
