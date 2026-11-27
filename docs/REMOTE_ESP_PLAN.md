# Remote ESP — host / relay / viewer

Status: design draft
Target: cs2-external-esp (fork of IMXNOOBX/cs2-external-esp), Windows x64, MSVC v145

## 1. Goal

Let a *viewer* render the ESP overlay from another player's memory reads.

```
+-----------+   binary snapshots    +-----------+   fan-out    +-----------+
|   HOST    | --------------------> |  RELAY    | -----------> |  VIEWER   |
| (in game, |    wss:// 30 Hz        | (Node/TS, |   wss://     | (in same  |
|  reads    | <-------------------- |  VPS)     |              |  match,   |
|  memory)  |    control msgs       |           |              |  no memory|
+-----------+                       +-----------+              |  reads)   |
                                                               +-----------+
```

Three roles:

| Role   | Reads memory | Writes memory | Renders | Talks to relay |
|--------|--------------|---------------|---------|----------------|
| local  | yes          | yes           | yes     | no             |
| host   | yes          | yes           | yes     | publishes      |
| viewer | **no**       | **no**        | yes     | subscribes     |

The viewer is a normal player in the same match. It does not know its own
camera, because it does not read memory — so the **host** sends it.

That is the core trick:

1. Viewer picks "I am player X" from the host's player list (by steam id).
2. Host includes, for every player, the camera parameters it can read:
   `eye position`, `eye angles`, `fov`, and the observer target if spectating.
3. Viewer builds a view matrix from **player X's** camera and its **own**
   window aspect ratio, then projects all other players into its own overlay.

Result: correct ESP in the viewer's own game window, with zero memory access
on the viewer side. No radar, no minimap, no web page — same overlay the host
sees, just computed from remote data.

## 2. Why not a radar

A 2D radar needs a map projection, a map image per map, and a separate
"where is T / where is CT" layout. World-space data + the viewer's own camera
gives the real thing (boxes, skeletons, health, flags) aligned to the game,
which is what the existing renderer already does.

A radar can be derived later from the same payload without protocol changes.

## 3. Data model

The wire payload is a **SnapshotV1**: everything `Esp`/`Overlays` already read
from `Snapshot`, minus anything machine-local.

```
FrameHeader
  u32  magic            'CS2R'
  u16  proto_version    1
  u16  flags            bit0 = host is alive, bit1 = host is spectating
  u32  seq              monotonically increasing, wraps
  u64  host_time_ms     steady clock, for interpolation
  u32  match_time       in-game clock (optional, for bomb timer)
  u8   map_len + char map_name[]
  u8   player_count     <= 64

CameraBlock (one per frame, for the viewer's chosen player)
  i8   camera_index     player index, or the observer target when spectating
  u8   observer_mode    0 alive, 2 first, 3 third, 4 free
  f32  eye_pos[3]       origin + view offset (host computes it, no drift)
  f32  eye_angles[2]    pitch, yaw
  f32  fov              horizontal FOV in degrees (m_iFov, 90 default)
  f32  host_view_matrix[16]   only for self-test / calibration

PlayerRecord (player_count times)
  i8   index
  u8   team
  u16  health
  u16  armor
  i32  money
  u32  flags            alive, scoped, flashed, defusing, spotted,
                        has_c4, local, bot, reloading
  u64  steam_id
  u8   name_len + char name[]
  u16  weapon_id
  u16  ammo
  f32  pos[3]
  f32  vel[3]
  f32  eye_angles[2]
  u32  bone_mask        bit i set => bone i present
  f32  bones[popcount][3]

BombBlock
  f32  pos[3]
  f32  time_left
  u8   is_planted
  i8   site
```

Size: ~120 B fixed per player + ~228 B of bones (19 bones) = ~350 B.
10 players ≈ 3.5 KB/frame. At 30 Hz ≈ 105 KB/s per viewer, so 8 viewers is
under 1 MB/s egress. That is fine for a VPS; if it is not, the knobs are:
20 Hz, bone subset, or per-frame delta + zstd.

Bones are sent as world positions, not screen positions, so the viewer can
resize, change aspect ratio, and interpolate freely.

## 4. Camera math (the risky part)

`Esp` calls `matrix.wts(pos, io.DisplaySize, out)` with `io.DisplaySize` being
the viewer's own window, so the viewer only has to produce a correct
`view_matrix_t` in `Snapshot.game.view_matrix`.

Build it from `eye_pos`, `eye_angles`, `fov`, and the viewer's aspect:

```
yaw = eye_angles.y, pitch = eye_angles.x, roll = 0
forward = ( cos(pitch)*cos(yaw),  cos(pitch)*sin(yaw), -sin(pitch) )
right   = ( sin(yaw),           -cos(yaw),              0          )
up      = right x forward
```

then a row-vector world-to-view matrix (Source convention, camera looks down
`-Z`), followed by a projection with `tan(fov/2)` and the viewer's aspect.

Do not trust the signs. Calibrate:

* the host includes its own `host_view_matrix` and its own camera block;
* the viewer builds a matrix for the host's player and diffs it against
  `host_view_matrix` element by element;
* the viewer also projects a known point (its own player's head) and checks it
  lands on the crosshair when the host is looking at it.

Acceptance: max element error < 1e-3, and projected head positions within a
couple of pixels of the host's own ESP output. This is a Phase 2 gate — do not
build the rest on an unvalidated matrix.

`fov` needs `CPlayer_CameraServices::m_iFov` (add to the runtime-updatable
offsets, same mechanism as `m_vecCsViewPunchAngle`). Fall back to 90 when the
field is missing, and note that scoped weapons change it.

## 5. Roles and binaries

Two programs, no shared process:

* **Host** — this C++ application. New **Host** menu tab: pick the server,
  press *Create room*, and it shows a room code plus a viewer key with copy
  buttons, then publishes at the configured rate.
* **Viewer** — a separate Go binary (`viewer/`), cross-built for macOS and
  Windows. It never opens a handle to `cs2.exe`; it only speaks to the relay.

Relay default: `wss://cs2.kjt.lol/ws`. The `CS2ESPSERVER` environment
variable overrides it (accepts `host`, `host:port`, `ws://…`, `http://…`);
command-line `--url` overrides both. Room codes are per session and are not
written to `config.json`, so a restart never retries a dead room.

## 6. Transport

**WebSocket (`ws://` / `wss://`)** with binary frames and small JSON control
messages.

The host uses **WinHTTP** (built into Windows) rather than the vendored
libcurl: the bundled libcurl 8.4 build has no `ws`/`wss` protocols, and
WinHTTP provides the upgrade plus Schannel TLS with no extra dependency. The
Go viewer uses a normal WebSocket library.

```
client -> server   hello   { role, room, token, proto }
                   ping    {}
                   select  { steam_id }        (viewer only)

server -> client   welcome { room, host_online, viewers, proto }
                   snapshot <binary>           (relayed untouched)
                   host_offline
                   error   { code, message }
```

The relay never parses `snapshot`. It validates size and version from the
12-byte prefix, caches the latest frame per room for instant join, and fans out.

## 7. Relay server (Node/TypeScript)

Single service, no database.

```
server/
  package.json  tsconfig.json  Dockerfile  README.md
  src/index.ts        bootstrap, TLS, health endpoint
  src/rooms.ts        room registry, host slot, viewer slots, latest frame
  src/protocol.ts     frame prefix parse, message types (shared with C++)
  src/limits.ts       rate limits, size caps
```

Behaviour:

* `POST /rooms` → `{ room, host_token, viewer_key }`; host keeps `host_token`,
  shares `room` + `viewer_key`.
* One host per room; viewers join with `room` + `viewer_key`.
* `maxViewers` per room (default 8), `maxFrameBytes` (default 64 KiB),
  per-IP connection cap, 30 msg/s per connection.
* Latest frame cached; new viewer gets `welcome` + last snapshot immediately.
* Host heartbeat timeout 2 s → `host_offline` to viewers, room stays joinable.
* Slow viewer: drop frames, never queue (backpressure = drop oldest).
* Structured logs with room id only, no player data.

Deploy: systemd unit + Caddy for TLS, or Docker. Health endpoint for uptime
checks. No secrets in the repo; tokens are per-room and generated at runtime.

## 8. Host changes

New module `src/net/`:

```
protocol.hpp        structs above + serialize/deserialize (header-only)
ws_client.cpp/.hpp  libcurl WebSocket wrapper (connect, send, recv, close)
publisher.cpp/.hpp  host side: snapshot -> bytes -> relay
remote_source.cpp/.hpp  viewer side: relay -> Snapshot
```

Publisher:

* dedicated thread, 33 ms tick, `Cache::CopySnapshot()` (already mutex-guarded);
* serialize → `curl_ws_send`; on failure, drop the frame and reconnect with
  exponential backoff (1 s → 30 s);
* never blocks the engine thread; if the socket stalls, frames are dropped;
* privacy filters in config: `publish_names`, `publish_steam_ids`.

Integration point: `Engine::Thread()` gains one line, or `main` starts the
publisher after `Engine::Init()` when `--mode=host`.

## 9. Viewer changes

The cleanest seam is the cache. Today `Esp` calls `Cache::CopySnapshot()`.

```
src/core/engine/cache/IDataSource.hpp
    struct IDataSource {
        virtual ~IDataSource() = default;
        virtual bool Refresh() = 0;
        virtual Snapshot Copy() = 0;
    };
```

* `LocalDataSource` — today's `Cache::RefreshImpl` (memory).
* `RemoteDataSource` — WebSocket client + latest/interpolated frame.
* `Cache` becomes a thin facade: `Cache::Use(source)`, `Refresh()`, `Copy()`.

`Esp`, `Overlays`, `Menu` keep calling `Cache::CopySnapshot()` unchanged.

Viewer-specific work:

| Area | Change |
|------|--------|
| `main.cpp` | `--mode=viewer` skips `Engine::Init()` and `Updater::Process()` |
| `Engine` | not started; `GetProcess()` stays null |
| `Renderer::InitImpl` | skip `SetForegroundWindow(Engine::GetProcess()->hwnd_)` |
| `Renderer::HandleWindowOrder` | viewer variant: find local `cs2.exe` window by class/process, no memory read; hide when unfocused |
| `Scripting` | disabled in viewer mode (it calls `Engine::GetProcess()->read`) |
| features | aimbot/triggerbot/bhop/antiflash disabled in viewer mode (they write memory) |
| menu | new **Remote** tab: URL, room, viewer key, connect/disconnect, status, latency, player list to pick "I am" |
| config | `remote` namespace: url, room, key, steam_id, publish_* |

Identity selection:

* menu lists players (name, team, health) → pick one, persisted as `steam_id`;
* optional auto-detect from Steam's `config/loginusers.vdf`
  (`MostRecent=1`) — a file read, not a memory read;
* if the chosen player dies and spectates, the host's `camera_index` follows
  the observer target, so the ESP keeps working in spectator mode.

## 10. Latency and smoothness

* Host 30 Hz, viewer renders 60+ fps.
* Keep the last two frames, interpolate positions/angles by `host_time_ms`
  with ~1 frame of delay. Pair players by `steam_id` (stable), not index.
* Show `seq` gaps and age in the Remote tab; if age > 500 ms, dim the ESP and
  show "stale".
* No lag compensation for aim — the viewer is display-only.

## 11. Trust boundary

The viewer consumes data from a server and a host it does not control. Treat
every byte as hostile:

* reject `player_count > 64`, `name_len > 32`, `bone_mask` bits outside the
  known bone set, `popcount` mismatches, and frames above 64 KiB;
* clamp/validate every float (`std::isfinite`), reject NaN/Inf positions;
* bound `bone_mask` lookups before indexing;
* never `memcpy` into fixed arrays without a length check;
* cap frames per second accepted from the relay;
* the viewer must remain fully functional (menu, config) if the relay lies.

## 12. Build and repo layout

```
src/net/                    host transport + protocol (WinHTTP websocket)
src/net/protocol.hpp        wire format, shared with the Go viewer
src/net/camera.hpp          matrix construction, mirrored in Go
src/net/publisher.*         host publishing thread
server/                     Node/TS relay (host + viewer fan-out)
viewer/                     Go viewer, cross-built for macOS + Windows (phase 5)
tests/                      protocol + camera tests, golden frame
docs/REMOTE_ESP_PLAN.md     this file
```

* `src.vcxproj`: `net\ws_client.cpp` and `net\publisher.cpp` added;
  `winhttp.lib` is linked via `#pragma comment`.
* `scripts/build.bat`: the new `.cpp` files are in `SRCS`.
* `main.cpp`: `--mode=local|host` (default `local`), plus
  `--net-selftest[=SECONDS]` to publish synthetic frames without the game.
* CI: a second workflow builds `server/` (`npm ci && tsc`) — no MSVC needed.

## 13. Milestones

| # | Deliverable | Acceptance | Est. | Status |
|---|-------------|------------|------|--------|
| 0 | Spike: C++ ws client ↔ Node relay echo | round-trips 1 KB binary, `wss` with Schannel | 0.5 d | done (WinHTTP) |
| 1 | `protocol.hpp` + serializer + golden-vector tests | serialize→deserialize→serialize is byte-identical | 1–2 d | done |
| 2 | Camera matrix + calibration harness | matches `host_view_matrix` < 1e-3, head lands on crosshair | 1–2 d | done (live check pending) |
| 3 | Node relay: rooms, auth, fan-out, limits | 1 host + 4 viewers, 30 Hz, no queue growth | 1–2 d | done (7 tests) |
| 4 | Host publisher thread + `--mode=host` | 10 min session, no frame drops on engine thread | 1 d | done |
| 5 | **Go viewer** + `--mode=viewer` equivalent | ESP renders in viewer's own window, no `cs2.exe` handle | 2–3 d | done (headless verified on macOS + Windows) |
| 6 | Remote tab, identity pick, config, status | restart restores room + identity | 1 d | host tab done; viewer picker done |
| 7 | Interpolation, reconnect, stale handling | 60 fps smooth at 30 Hz, survives relay restart | 1–2 d | not started |
| 8 | Hardening, deploy docs, CI | fuzz-ish malformed frames do not crash viewer | 1–2 d | not started |

Phase 5 is a **separate Go program** (`viewer/`), cross-compiled for macOS
and Windows, so the C++ binary stays Windows-only and the viewer can run
anywhere.

Total ≈ 10–14 dev days. Phases 2 and 5 are the schedule risks.

## 14. Risks

| Risk | Mitigation |
|------|------------|
| View matrix convention wrong (handedness, row/col) | Phase 2 gate with host matrix diff before anything else |
| FOV differs per player / scoped | send `fov` per player from `m_iFov`, fall back to 90 |
| Aspect ratio differs from host | viewer builds its own projection, host data is world-space |
| libcurl ws quirks (fragmentation, close frames) | spike in Phase 0, wrap in `ws_client` with a 1 MB cap |
| Relay abuse on a public VPS | room tokens, per-IP caps, frame size cap, rate limit |
| Data privacy (names, steam ids) | `publish_names` / `publish_steam_ids` toggles |
| Viewer still detectable | no memory access, but an overlay + relay connection is still a cheat; document the risk, no promises |

## 15. Open questions

1. Should the host also be able to publish while playing normally (yes, by
   default), or only in a dedicated "host" mode?
2. Do viewers get the host's team only, or all players? (Protocol supports
   both; default: all, filtered by the existing ESP team setting.)
3. Is one host per room enough, or do we need multiple hosts in one room
   (e.g., both teams) merged server-side? Deferred.
4. Recording/replay of snapshots to disk for later viewing — out of scope for
   v1, but the frame format makes it trivial.
