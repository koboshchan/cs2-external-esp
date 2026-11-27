# Relay server

Byte relay for remote ESP. A **host** publishes snapshots, any number of
**viewers** subscribe to the same room. The relay never parses player data —
it only checks the 12-byte frame prefix and fans frames out.

## Run

```sh
npm install
npm run build
PORT=8080 node dist/index.js
```

Health check:

```sh
curl http://127.0.0.1:8080/health
```

## Create a room

```sh
curl -s -X POST http://127.0.0.1:8080/rooms -d '{}'
# {"room":"ABC234","hostToken":"...","viewerKey":"...","maxViewers":8,"proto":1}
```

`hostToken` goes to the host, `viewerKey` is shared with viewers. The room id
is the only thing you share publicly.

## Environment

| Variable | Default | Meaning |
|---|---|---|
| `PORT` | `8080` | listen port |
| `BIND` | `0.0.0.0` | listen address |
| `MAX_VIEWERS` | `8` | default viewers per room (1–32) |
| `HOST_TIMEOUT_MS` | `3000` | host is offline after this without frames |
| `HELLO_TIMEOUT_MS` | `5000` | drop sockets that never send `hello` |
| `ROOM_IDLE_MS` | `600000` | delete empty rooms after this |
| `MAX_FRAME_BYTES` | `65536` | reject larger frames |
| `TLS_CERT`, `TLS_KEY` | – | enables `https://` and `wss://` |

## Protocol

JSON control messages, binary snapshots.

```jsonc
// client -> server
{"t":"hello","role":"host","room":"ABC234","token":"<hostToken>","proto":1}
{"t":"hello","role":"viewer","room":"ABC234","token":"<viewerKey>","proto":1}
{"t":"ping"}

// server -> client
{"t":"welcome","role":"host","room":"ABC234","proto":1,"viewers":1}
{"t":"welcome","role":"viewer","room":"ABC234","host_online":true,"seq":42}
{"t":"host_online"}   {"t":"host_offline"}
{"t":"error","code":"bad_token","message":"..."}
```

Binary frames are the `CS2R` snapshot from `src/net/protocol.hpp`, relayed
untouched. A late viewer receives the cached last frame on join.

Error codes: `bad_message`, `proto_mismatch`, `room_not_found`, `bad_token`,
`room_busy`, `too_many_viewers`, `not_host`, `frame_too_large`, `rate_limited`.

## Tests

```sh
npm test          # builds, then runs the relay smoke tests
node test/live-viewer.mjs ws://127.0.0.1:8080/ws ROOM VIEWER_KEY 5
```

## Docker

Multi-stage image: TypeScript is compiled in a builder stage, the runtime
image keeps only production dependencies and runs as an unprivileged user.

```sh
docker compose up -d --build
docker compose logs -f
curl http://127.0.0.1:8080/health
docker compose down
```

The compose service is `restart: unless-stopped`, `init: true`, read-only with
a tmpfs for `/tmp`, drops privileges (`no-new-privileges`), has a healthcheck
on `/health`, and caps log size.

Copy `.env.example` to `.env` to change the published port or limits:

```sh
cp .env.example .env
# PORT=9000, MAX_VIEWERS=16, ...
docker compose up -d
```

TLS (`wss://`) inside the container — mount certificates and set the env vars:

```yaml
    environment:
      TLS_CERT: /certs/fullchain.pem
      TLS_KEY: /certs/privkey.pem
    volumes:
      - ./certs:/certs:ro
```

Without compose:

```sh
docker build -t cs2-remote-esp-relay .
docker run -d --name relay -p 8080:8080 --read-only --tmpfs /tmp \
  -e MAX_VIEWERS=8 cs2-remote-esp-relay
```

## Deploy without Docker

systemd unit behind Caddy/nginx for TLS, with `TLS_CERT` / `TLS_KEY` to serve
`wss://` directly from Node.

Limits are per connection: 60 messages/second (240 burst), frames above
`MAX_FRAME_BYTES` rejected, viewers more than 256 KiB behind get frames
dropped instead of queued.
