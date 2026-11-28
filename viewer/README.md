# cs2-remote-viewer

Standalone viewer for the remote ESP. It renders an overlay from a host's
snapshots and **never opens a handle to the game** — no offsets, no memory
reads, no injection.

On Windows it uses a layered, click-through, always-on-top window with
per-pixel alpha. On macOS it uses a transparent AppKit overlay. Both platforms
can follow the native GeForce NOW client or a manually selected window.

The host is the C++ application (`cs2-external-esp.exe`, Host tab). This binary
is the viewer.

## Run (Windows, GeForce NOW viewer 0.6.2)

Build for Windows 10/11 x64 with `GOOS=windows GOARCH=amd64 CGO_ENABLED=0`.
The native GeForce NOW application is the automatic overlay target.

1. Enter **Server**, **Room code** and **Viewer key** from the Windows host.
2. Click **Connect**, choose the camera player, then **Start overlay**.
3. Automatic GeForce NOW detection is enabled by default. The mapping session
   waits while its target is missing, minimized or on another virtual desktop.
4. **Ctrl+P** opens display controls. Choose the camera player in the persistent
   list, toggle boxes/health/skeleton/names/teammates, set opacity and edit RGB colors.
5. **F8** cycles camera players. **F9** restores the launcher. **End** exits.

The minimized launcher remains available in the Windows taskbar. Restoring it
returns to connection and target settings. Its **显示控制台** button provides
access when a shortcut is occupied. Closing the launcher exits the viewer.

Camera selection uses the chosen player's EyePos, EyeAngles and Fov from
received snapshots. The selection follows SteamID across slot changes.
Missing players retain a waiting selection until they return.

The overlay follows the target client rectangle and centered aspect-fit stream.
Choose an explicit aspect ratio or top inset when the stream uses different
cropping. Changes to target and ratio settings apply on **Start overlay**.
Uncheck automatic targeting to use the manual window list and **Refresh**.

Appearance is saved to `%APPDATA%\cs2-remote-viewer\appearance.json`.
The existing connection settings continue to use `config.json` in that folder.
Colors accept six-digit RGB values such as `#50A0FF`. Click **应用颜色** to apply.
Opacity changes apply immediately and are saved after dragging pauses.
The **隐藏映射 / 显示映射** button toggles the entire overlay. Controls remain
available while the overlay is hidden. The toggle state lasts for the current run.
**F6** is the default global overlay master shortcut. In the controls panel,
click the **映射总开关键** field, press the desired key combination, and click
**保存按键**. The binding is saved to `hotkeys.json` and restored on the next run.
Occupied or reserved shortcuts show an error. Holding the shortcut toggles once.

On macOS, open controls with **Command+P** and click the shortcut button beside
**映射总开关键**. Press the desired combination to save it, or **Escape** to cancel.
The macOS default is also **F6**, which may require **fn+F6** depending on keyboard
settings. Its binding is stored in the app's preferences.

The Windows EXE is cross-compiled on macOS. Shared tests and static checks were
run locally. Windows-native mouse, hotkey, virtual-desktop, multi-monitor and
GeForce NOW fullscreen behavior still require Windows runtime verification.

## Run (macOS)

Same flow, native AppKit controls:

1. **Server / Room code / Viewer key** — `Cmd+V` pastes.
2. **Connect**.
3. **Camera player** — choose the player's viewpoint to render.
4. Automatic GeForce NOW targeting is enabled by default. Disable it to select
   an overlay window manually.
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
| next player | `F8` | select in the Command+P controls panel |
| quit | `END` | CS2 menu bar item |

On Windows the overlay targets GeForce NOW automatically. The launcher can
switch to manual targeting, and `--rect` selects a fixed rectangle:

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

`darwin/amd64` needs an Intel Mac or a matching cgo cross-compilation setup
for the native AppKit bridge.

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
