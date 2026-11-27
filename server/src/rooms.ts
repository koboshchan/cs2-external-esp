import { randomBytes } from 'node:crypto';
import type { WebSocket } from 'ws';

/** Unambiguous alphabet: no 0/O, 1/I/L. */
const ROOM_ALPHABET = '23456789ABCDEFGHJKMNPQRSTUVWXYZ';
const ROOM_LENGTH = 6;

export function randomToken(bytes = 24): string {
  return randomBytes(bytes).toString('base64url');
}

export function randomRoomId(): string {
  const raw = randomBytes(ROOM_LENGTH);
  let out = '';
  for (let i = 0; i < ROOM_LENGTH; i++) {
    out += ROOM_ALPHABET[raw[i]! % ROOM_ALPHABET.length];
  }
  return out;
}

export interface Room {
  id: string;
  hostToken: string;
  viewerKey: string;
  maxViewers: number;
  createdAt: number;

  host: WebSocket | null;
  hostSeenAt: number;
  hostOnline: boolean;

  viewers: Map<WebSocket, number>;

  lastFrame: Buffer | null;
  lastFrameAt: number;
  lastSeq: number;

  /** Last time anything happened, used for room garbage collection. */
  touchedAt: number;
}

export class RoomRegistry {
  private readonly rooms = new Map<string, Room>();
  private readonly maxViewers: number;

  constructor(maxViewers: number) {
    this.maxViewers = maxViewers;
  }

  create(overrides?: { maxViewers?: number }): Room {
    let id = randomRoomId();
    while (this.rooms.has(id)) id = randomRoomId();

    const now = Date.now();
    const room: Room = {
      id,
      hostToken: randomToken(),
      viewerKey: randomToken(12),
      maxViewers: Math.max(1, Math.min(overrides?.maxViewers ?? this.maxViewers, 32)),
      createdAt: now,
      host: null,
      hostSeenAt: 0,
      hostOnline: false,
      viewers: new Map(),
      lastFrame: null,
      lastFrameAt: 0,
      lastSeq: 0,
      touchedAt: now,
    };

    this.rooms.set(id, room);
    return room;
  }

  get(id: string): Room | undefined {
    return this.rooms.get(id.toUpperCase());
  }

  delete(id: string): void {
    this.rooms.delete(id);
  }

  all(): IterableIterator<Room> {
    return this.rooms.values();
  }

  get size(): number {
    return this.rooms.size;
  }

  viewerCount(): number {
    let n = 0;
    for (const room of this.rooms.values()) n += room.viewers.size;
    return n;
  }

  /** Drops rooms that have been idle long enough to be abandoned. */
  sweep(idleMs: number, now = Date.now()): number {
    let removed = 0;
    for (const [id, room] of this.rooms) {
      const empty = !room.host && room.viewers.size === 0;
      if (empty && now - room.touchedAt > idleMs) {
        this.rooms.delete(id);
        removed++;
      }
    }
    return removed;
  }
}

export function hostIsStale(room: Room, now: number, timeoutMs: number): boolean {
  return !room.host || now - room.hostSeenAt > timeoutMs;
}
