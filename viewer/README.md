# cs2-remote-viewer

Standalone viewer for the remote ESP. It renders an overlay from a host's
snapshots and **never opens a handle to the game** — no offsets, no memory
reads, no injection.

On Windows it is a real overlay: a layered, click-through, always-on-top
window with per-pixel alpha, sized to the CS2 client area. On macOS it is a
normal window (CS2 does not run there; that build is for previewing).

The host is the C++ application (`cs2-external-esp.exe`, Host tab). This binary
is the viewer.

## Run (Windows)

Start it with no arguments. A launcher window opens:

1. **Server / Room code / Viewer key** — paste the two values from the host's
   Host tab (each field has a **Paste** button; `Ctrl+V` works too).
2. **Connect**.
3. **You are** — pick your own player from the live list.
4. **Overlay window** — pick the window to attach to (press **Refresh** if the
   game is not listed).
5. **Start overlay**.

The overlay is click-through, always on top, and follows that window's client
area. The camera is always the **host's**: what the host is facing is what you
see, and nothing on the viewer side can change the view. `F9` returns to the launcher, `F8` cycles players, `END` quits.

Settings are saved, so later launches only need Connect → player → window →
Start.

> The game must be in **Fullscreen Windowed** (borderless). Exclusive
> fullscreen cannot be overlaid.

## Run (macOS)

Same flow, native AppKit controls:

1. **Server / Room code / Viewer key** — `Cmd+V` pastes.
2. **Connect**.
3. **Hide player** — optional; hides one player from the overlay.
4. **Overlay window** — pick any window (a stream, a video, anything).
5. **Start overlay**.

The overlay is a borderless, transparent, click-through window at
`NSScreenSaverWindowLevel`. Its frame is re-read from the chosen window every
frame, so it follows moves and resizes live (a brief lookup miss keeps the
last rect instead of flashing full screen). A **CS2** menu bar
item brings the launcher back or quits.

CS2 does not run on macOS, so this overlays other windows (streams, captures);
the Windows build is the one that overlays the game.

On Windows `F9` reopens the connect window (to change room), `END` quits.
The overlay itself is click-through, which is why the connect window exists.

Command-line flags are optional, for scripting:

```sh
./cs2-remote-viewer --room ABC234 --key <viewerKey>          # skip the window
./cs2-remote-viewer --room ... --key ... --me 76561198000000000
./cs2-remote-viewer --room ... --key ... --me "player name"
./cs2-remote-viewer --room ... --key ... --me-index 4
./cs2-remote-viewer --pick 3            # testing: auto-select the 4th player
```

Saved settings live in:

- Windows: `%APPDATA%\cs2-remote-viewer\config.json`
- macOS: `~/Library/Application Support/cs2-remote-viewer/config.json`

Controls:

| | Windows overlay | macOS window |
|---|---|---|
| next player | `F8` | reopen the launcher |
| quit | `END` | CS2 menu bar item |

On Windows the overlay follows the CS2 window automatically. If detection
fails (windowed mode with an unusual class), pin it manually:

```sh
./cs2-remote-viewer --room ... --key ... --rect 0,0,1920,1080
```

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
protocol.go        CS2R v2 parser, mirrors src/net/protocol.hpp
camera.go          view matrix + projection, mirrors src/net/camera.hpp
relay.go           websocket client, reconnect, latest-frame store
scene.go           drawing logic shared by both canvases
canvas_windows.go  layered click-through overlay (Win32 + GDI, no cgo)
canvas_darwin.go   AppKit launcher + Core Graphics overlay (cgo)
ui_darwin.m        Objective-C bridge for the above
main.go            flags, player selection, summary
*_test.go          golden frame, truncation, mutation fuzz, camera parity
```

## Notes

- The Windows overlay draws into a 32-bit DIB and presents it with
  `UpdateLayeredWindow`. GDI text is drawn on black, then its alpha is derived
  from brightness, so glyphs composite correctly over the game.
- Everything received from the relay is treated as hostile: sizes, counts,
  bone masks and floats are validated before use.
