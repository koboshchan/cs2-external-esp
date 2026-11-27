/**
 * Wire constants shared with src/net/protocol.hpp.
 *
 * The relay deliberately does not understand player data. It only validates
 * the 12-byte prefix so a broken or hostile host cannot poison viewers with
 * frames of the wrong shape or version.
 */

export const PROTO_VERSION = 1;

/** 'CS2R' little-endian */
export const MAGIC = 0x52325343;

export const MAX_FRAME_BYTES = 64 * 1024;
export const MIN_FRAME_BYTES = 12;
export const MAX_PLAYERS = 64;

export const FRAME_FLAG_HOST_ALIVE = 1 << 0;
export const FRAME_FLAG_HOST_SPECTATING = 1 << 1;

export interface FramePrefix {
  version: number;
  flags: number;
  seq: number;
}

/**
 * Validates the fixed frame prefix: magic, protocol version and size.
 * Returns null when the frame must not be relayed.
 */
export function parseFramePrefix(buf: Buffer): FramePrefix | null {
  if (buf.length < MIN_FRAME_BYTES || buf.length > MAX_FRAME_BYTES) return null;
  if (buf.readUInt32LE(0) !== MAGIC) return null;

  const version = buf.readUInt16LE(4);
  if (version !== PROTO_VERSION) return null;

  return {
    version,
    flags: buf.readUInt16LE(6),
    seq: buf.readUInt32LE(8),
  };
}

export type Role = 'host' | 'viewer';

export interface HelloMessage {
  t: 'hello';
  role: Role;
  room: string;
  token: string;
  proto: number;
}

export interface ClientMessage {
  t: string;
  [key: string]: unknown;
}

export interface ErrorMessage {
  t: 'error';
  code: string;
  message: string;
}

export const ERR = {
  badMessage: 'bad_message',
  protoMismatch: 'proto_mismatch',
  roomNotFound: 'room_not_found',
  badToken: 'bad_token',
  roomBusy: 'room_busy',
  tooManyViewers: 'too_many_viewers',
  notHost: 'not_host',
  frameTooLarge: 'frame_too_large',
  rateLimited: 'rate_limited',
} as const;
