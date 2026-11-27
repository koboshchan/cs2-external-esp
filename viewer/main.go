package main

// Remote ESP viewer: renders an overlay from a host's snapshots.
//
// It never opens a handle to the game. The host publishes world-space data and
// the camera of every player; the viewer picks which player it is, builds that
// player's view matrix, and projects everyone else into its own window.

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

const defaultServer = "wss://cs2.kjt.lol/ws"

func normalizeWSURL(raw string) string {
	scheme := "wss://"
	rest := raw

	switch {
	case strings.HasPrefix(raw, "wss://"):
		rest = raw[len("wss://"):]
	case strings.HasPrefix(raw, "ws://"):
		scheme = "ws://"
		rest = raw[len("ws://"):]
	case strings.HasPrefix(raw, "https://"):
		rest = raw[len("https://"):]
	case strings.HasPrefix(raw, "http://"):
		scheme = "ws://"
		rest = raw[len("http://"):]
	}

	rest = strings.TrimRight(rest, "/")

	if i := strings.Index(rest, "/"); i < 0 {
		rest += "/ws"
	} else if i == len(rest)-1 {
		rest += "ws"
	}

	return scheme + rest
}

func resolveServerURL(configured string) string {
	raw := configured
	if env := os.Getenv("CS2ESPSERVER"); env != "" {
		raw = env
	}
	if raw == "" {
		raw = defaultServer
	}
	return normalizeWSURL(raw)
}

type options struct {
	server   string
	room     string
	key      string
	me       string
	meIndex  int
	seconds  int
	rect     string
	autostart bool
	dump      string
	pick      int
	uiTest    bool
}

func parseFlags() options {
	var o options

	flag.StringVar(&o.server, "server", "", "relay URL (default "+defaultServer+", overridden by CS2ESPSERVER)")
	flag.StringVar(&o.room, "room", "", "room code from the host")
	flag.StringVar(&o.key, "key", "", "viewer key from the host")
	flag.StringVar(&o.me, "me", "", "your steam id or player name")
	flag.IntVar(&o.meIndex, "me-index", -1, "your player index (fallback when ids are hidden)")
	flag.IntVar(&o.seconds, "seconds", 0, "quit after N seconds (0 = run until ESC)")
	flag.StringVar(&o.rect, "rect", "", "overlay rect as x,y,w,h (default: the CS2 window)")
	flag.BoolVar(&o.autostart, "autostart", false, "skip the launcher and bind to the CS2 window as soon as frames arrive")
	flag.StringVar(&o.dump, "dump", "", "write the overlay bitmap to a PNG on exit (macOS, testing)")
	flag.IntVar(&o.pick, "pick", -1, "select the Nth player in the launcher automatically (testing)")
	flag.BoolVar(&o.uiTest, "ui-test", false, "open the player dropdown, verify a menu window appears, then close it (testing)")
	flag.Parse()

	return o
}

func main() {
	// AppKit (macOS) requires windows to be created on the OS main thread.
	runtime.LockOSThread()

	o := parseFlags()

	// Saved settings fill anything the command line did not provide, so the
	// app works by double-clicking with no arguments at all.
	cfg := loadConfig()
	if o.server == "" {
		o.server = cfg.Server
	}
	if o.room == "" {
		o.room = cfg.Room
	}
	if o.key == "" {
		o.key = cfg.Key
	}
	if o.me == "" {
		o.me = cfg.Me
	}
	o.server = resolveServerURL(o.server)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	relay, err := runUI(ctx, o)
	if err != nil {
		fmt.Fprintln(os.Stderr, "viewer stopped:", err)
		os.Exit(1)
	}

	if relay != nil {
		printSummary(relay, o)
	}
}

// startRelay connects in the background; cancel stops it.
func startRelay(ctx context.Context, o options) (*Relay, context.CancelFunc) {
	relayCtx, cancel := context.WithCancel(ctx)

	relay := &Relay{URL: o.server, Room: o.room, Key: o.key}
	go relay.Run(relayCtx)

	return relay, cancel
}

// selectedPlayerIndex is set by the launcher when the overlay starts, so the
// summary reports the player the user actually picked.
var selectedPlayerIndex int8 = -1

func printSummary(relay *Relay, o options) {
	if selectedPlayerIndex >= 0 {
		o.meIndex = int(selectedPlayerIndex)
	}

	snap := relay.Snapshot()

	me := ""
	projected, visible := 0, 0
	var cameraError float32

	if snap.Frame != nil {
		if p := selectPlayer(snap.Frame, o); p != nil {
			me = p.Name
		}
		projected, visible, cameraError = projectionStats(snap.Frame, o)
	}

	fmt.Printf("\nsummary: frames=%d bytes=%d gaps=%d players=%d you=%q projected=%d on_screen=%d camera_error=%.6f\n",
		snap.Frames, snap.Bytes, snap.Gaps, playerCount(snap.Frame), me, projected, visible, cameraError)
}

func selectPlayer(f *Frame, o options) *Player {
	if f == nil {
		return nil
	}
	if o.meIndex >= 0 {
		if p := f.PlayerByIndex(o.meIndex); p != nil {
			return p
		}
	}
	if o.me != "" {
		if id, err := strconv.ParseUint(o.me, 10, 64); err == nil {
			if p := f.PlayerBySteamID(id); p != nil {
				return p
			}
		}
		if p := f.PlayerByName(o.me); p != nil {
			return p
		}
	}
	// Fall back to the host's own player so something sensible renders.
	for i := range f.Players {
		if f.Players[i].Local() {
			return &f.Players[i]
		}
	}
	return nil
}

func playerCount(f *Frame) int {
	if f == nil {
		return 0
	}
	return len(f.Players)
}

// projectionStats builds the selected player's camera and projects everyone
// else, which is exactly what the renderer does. It also compares the built
// matrix with the host's real matrix for the host's own player.
func projectionStats(f *Frame, o options) (projected, visible int, maxCameraError float32) {
	me := selectPlayer(f, o)
	if me == nil {
		return 0, 0, 0
	}

	const width, height = 1920.0, 1080.0
	aspect := float32(width / height)

	m := BuildViewMatrix(me.EyePos, me.EyeAngles, me.Fov, aspect, f.Camera.FovMode)

	for i := range f.Players {
		p := &f.Players[i]
		if p.Index == me.Index || !p.Alive() {
			continue
		}
		projected++
		if sp, ok := Project(m, p.EyePos, width, height); ok &&
			sp.X >= 0 && sp.X <= width && sp.Y >= 0 && sp.Y <= height {
			visible++
		}
	}

	// Calibration: rebuild the host's matrix from its camera block. The host's
	// own aspect ratio comes from its matrix, not from our window. A zeroed
	// matrix (host between rounds) carries no information, so skip it.
	if host := f.PlayerByIndex(int(f.Camera.HostIndex)); host != nil && f.Camera.HostFov > 1 && MatrixXScale(f.Camera.ViewMatrix) > 0.01 {
		hostAspect := aspect
		if ys := MatrixYScale(f.Camera.ViewMatrix); ys > 0 {
			hostAspect = ys / MatrixXScale(f.Camera.ViewMatrix)
		}

		built := BuildViewMatrix(f.Camera.HostEyePos, f.Camera.HostEyeAngles, f.Camera.HostFov, hostAspect, f.Camera.FovMode)
		// Compare the projection basis only (right/up/forward scales and
		// directions). Translation carries view bob and punch, which are
		// expected to differ slightly from the pawn-derived eye position.
		for _, i := range []int{0, 1, 2, 4, 5, 6, 12, 13, 14} {
			d := built[i] - f.Camera.ViewMatrix[i]
			if d < 0 {
				d = -d
			}
			if d > maxCameraError {
				maxCameraError = d
			}
		}
	}

	return projected, visible, maxCameraError
}
