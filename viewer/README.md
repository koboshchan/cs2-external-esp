# cs2-remote-viewer

Standalone viewer for the remote ESP. It renders an overlay from a host's
snapshots and **never opens a handle to the game** — no offsets, no memory
reads, no injection.

The host is the C++ application (`cs2-external-esp.exe`, Host tab). This binary
is the viewer.

## Run

```sh
# the host shows these in its Host tab
./cs2-remote-viewer --room ABC234 --key <viewerKey>

# your own player (pick one)
./cs2-remote-viewer --room ABC234 --key ... --me 76561198000000000
./cs2-remote-viewer --room ABC234 --key ... --me "player name"
./cs2-remote-viewer --room ABC234 --key ... --me-index 4
```

In the window: `TAB` cycles the selected player, `ESC` quits.

Relay URL defaults to `wss://cs2.kjt.lol/ws`; set `CS2ESPSERVER` or pass
`--server` to override:

```sh
CS2ESPSERVER=ws://192.168.1.110:8080/ws ./cs2-remote-viewer --room ... --key ...
```

## Smoke test

`--seconds N` quits on its own and prints counters plus a projection summary:

```sh
./cs2-remote-viewer --seconds 10 --room ABC234 --key ...
```

```
summary: frames=150 bytes=283800 gaps=0 players=10 you="selftest0" projected=9 on_screen=6 camera_error=0.000000
```

`camera_error` compares the matrix built from the host's camera block with the
host's real `view_matrix` (rows 0, 1 and 3). It should be 0 when the host is
the C++ implementation.

## Build

```sh
./build.sh
# or
go build -o cs2-remote-viewer .
```

Cross-compiles:

```sh
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o cs2-remote-viewer.exe .
GOOS=darwin  GOARCH=arm64 go build -o cs2-remote-viewer-macos-arm64 .
```

`darwin/amd64` needs an Intel Mac (or matching cgo flags): Ebiten's macOS
OpenGL backend does not cross-compile from arm64.

## Layout

```
protocol.go      CS2R v2 parser, mirrors src/net/protocol.hpp
camera.go        view matrix + projection, mirrors src/net/camera.hpp
relay.go         websocket client, reconnect, latest-frame store
render.go        Ebiten overlay: boxes, skeleton, health, flags, bomb
main.go          flags, player selection, summary
*_test.go        golden frame, truncation, mutation fuzz, camera parity
```

## Notes

- The window is a normal window today. A transparent, click-through overlay
  over the game needs a layered Win32 window, which Ebiten cannot do; that is
  the next step for a true in-game overlay.
- Everything received from the relay is treated as hostile: sizes, counts,
  bone masks and floats are validated before use.
