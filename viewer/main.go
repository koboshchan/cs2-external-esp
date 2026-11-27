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
	"strconv"
	"strings"
	"syscall"
	"time"
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
	headless bool
	seconds  int
}

func parseFlags() options {
	var o options

	flag.StringVar(&o.server, "server", "", "relay URL (default "+defaultServer+", overridden by CS2ESPSERVER)")
	flag.StringVar(&o.room, "room", "", "room code from the host")
	flag.StringVar(&o.key, "key", "", "viewer key from the host")
	flag.StringVar(&o.me, "me", "", "your steam id or player name")
	flag.IntVar(&o.meIndex, "me-index", -1, "your player index (fallback when ids are hidden)")
	flag.BoolVar(&o.headless, "headless", false, "connect and print stats without a window")
	flag.IntVar(&o.seconds, "seconds", 0, "exit after N seconds (headless default 10)")
	flag.Parse()

	o.server = resolveServerURL(o.server)
	return o
}

func main() {
	o := parseFlags()

	if o.room == "" {
		fmt.Fprintln(os.Stderr, "error: --room is required (the host shows it in the Host tab)")
		os.Exit(2)
	}
	if o.key == "" {
		fmt.Fprintln(os.Stderr, "error: --key is required (the host shows the viewer key)")
		os.Exit(2)
	}

	relay := &Relay{URL: o.server, Room: o.room, Key: o.key}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	go relay.Run(ctx)

	if o.headless {
		runHeadless(ctx, relay, o)
		return
	}

	if err := runUI(ctx, relay, o); err != nil {
		fmt.Fprintln(os.Stderr, "viewer stopped:", err)
		os.Exit(1)
	}
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

func runHeadless(ctx context.Context, relay *Relay, o options) {
	seconds := o.seconds
	if seconds <= 0 {
		seconds = 10
	}

	deadline := time.After(time.Duration(seconds) * time.Second)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	var last RelaySnapshot
	var projected, visible int
	var maxCameraError float32

	for {
		select {
		case <-ctx.Done():
			report(last, projected, visible, maxCameraError)
			return
		case <-deadline:
			report(last, projected, visible, maxCameraError)
			return
		case <-ticker.C:
			last = relay.Snapshot()
			if last.Frame != nil {
				p, v, err := projectionStats(last.Frame, o)
				projected, visible = p, v
				if err > maxCameraError {
					maxCameraError = err
				}
			}
			fmt.Printf("connected=%v host=%v frames=%d bytes=%d gaps=%d age=%s\n",
				last.Status.Connected, last.Status.HostOnline, last.Frames, last.Bytes, last.Gaps, ageOf(last.FrameAt))
		}
	}
}

func ageOf(t time.Time) string {
	if t.IsZero() {
		return "n/a"
	}
	return time.Since(t).Round(time.Millisecond).String()
}

func report(s RelaySnapshot, projected, visible int, maxErr float32) {
	fmt.Printf("\nsummary: frames=%d bytes=%d gaps=%d players=%d projected=%d on_screen=%d camera_error=%.6f\n",
		s.Frames, s.Bytes, s.Gaps, playerCount(s.Frame), projected, visible, maxErr)
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
		if _, ok := Project(m, p.EyePos, width, height); ok {
			visible++
		}
	}

	// Calibration: rebuild the host's matrix from its camera block.
	if host := f.PlayerByIndex(int(f.Camera.HostIndex)); host != nil && f.Camera.HostFov > 1 {
		built := BuildViewMatrix(f.Camera.HostEyePos, f.Camera.HostEyeAngles, f.Camera.HostFov, aspect, f.Camera.FovMode)
		for _, row := range [3][2]int{{0, 3}, {4, 7}, {12, 15}} {
			for i := row[0]; i <= row[1]; i++ {
				d := built[i] - f.Camera.ViewMatrix[i]
				if d < 0 {
					d = -d
				}
				if d > maxCameraError {
					maxCameraError = d
				}
			}
		}
	}

	return projected, visible, maxCameraError
}
