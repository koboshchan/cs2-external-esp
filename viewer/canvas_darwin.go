//go:build darwin

package main

// macOS client: native launcher + transparent click-through overlay, both
// driven from Go through the Objective-C bridge in ui_darwin.m.

/*
#cgo CFLAGS: -fobjc-arc
#cgo LDFLAGS: -framework Cocoa -framework CoreGraphics -framework CoreText -framework QuartzCore -framework ImageIO

#include <stdlib.h>

void ov_init(void);
void ov_pump(void);

void ov_launcher_create(const char* server, const char* room, const char* key);
void ov_launcher_show(void);
void ov_launcher_hide(void);
int  ov_launcher_visible(void);
int  ov_launcher_take_connect(void);
int  ov_launcher_take_start(void);
int  ov_launcher_take_refresh(void);
int  ov_take_quit(void);
int  ov_take_show_launcher(void);
void ov_launcher_get(int field, char* buffer, int length);
void ov_launcher_set_status(const char* text);
void ov_launcher_set_players(const char* newline_separated);
int  ov_launcher_player_index(void);
void ov_launcher_set_windows(const char* newline_separated);
int  ov_launcher_window_index(void);
void ov_launcher_pick(int index);
void ov_ui_test(void);
void ov_launcher_force_start(void);

const char* ov_windows_list(void);
int  ov_window_count(void);
int  ov_window_bounds(int index, double* x, double* y, double* w, double* h);
void ov_screen_size(double* w, double* h);

void ov_overlay_create(void);
void ov_overlay_show(void);
void ov_overlay_hide(void);
void ov_overlay_begin(double x, double y, double w, double h);
void ov_overlay_fill(double x, double y, double w, double h,
                     double r, double g, double b, double a);
void ov_overlay_line(double x1, double y1, double x2, double y2,
                     double r, double g, double b, double a);
void ov_overlay_text(double x, double y, const char* utf8,
                     double r, double g, double b, double a);
void ov_overlay_present(void);
int  ov_overlay_dump(const char* path);
int  ov_selftest_dump(const char* path);
*/
import "C"

import (
	"context"
	"fmt"
	"image/color"
	"strings"
	"time"
	"unsafe"
)

type overlayCanvas struct {
	windowIndex int
	width       float32
	height      float32
}

func cstr(s string) *C.char {
	return C.CString(s)
}

func freeCString(p *C.char) {
	if p != nil {
		C.free(unsafe.Pointer(p))
	}
}

func (c *overlayCanvas) Begin() (float32, float32) {
	var x, y, w, h C.double

	if C.ov_window_bounds(C.int(c.windowIndex), &x, &y, &w, &h) == 0 {
		// The bound window is gone: fall back to the whole screen.
		C.ov_screen_size(&w, &h)
		x, y = 0, 0
	}

	c.width = float32(w)
	c.height = float32(h)

	C.ov_overlay_begin(x, y, w, h)
	return c.width, c.height
}

func (c *overlayCanvas) Clear() {}

func (c *overlayCanvas) Rect(x, y, w, h float32, col color.RGBA) {
	if col.A == 0 || w <= 0 || h <= 0 {
		return
	}
	C.ov_overlay_fill(
		C.double(x), C.double(y), C.double(w), C.double(h),
		C.double(col.R)/255, C.double(col.G)/255, C.double(col.B)/255, C.double(col.A)/255,
	)
}

func (c *overlayCanvas) Line(x1, y1, x2, y2 float32, col color.RGBA) {
	C.ov_overlay_line(
		C.double(x1), C.double(y1), C.double(x2), C.double(y2),
		C.double(col.R)/255, C.double(col.G)/255, C.double(col.B)/255, C.double(col.A)/255,
	)
}

func (c *overlayCanvas) Text(x, y float32, s string, col color.RGBA) {
	if s == "" {
		return
	}

	text := cstr(s)
	defer freeCString(text)

	C.ov_overlay_text(
		C.double(x), C.double(y), text,
		C.double(col.R)/255, C.double(col.G)/255, C.double(col.B)/255, C.double(col.A)/255,
	)
}

func (c *overlayCanvas) Present() {
	C.ov_overlay_present()
}

func (c *overlayCanvas) Opaque() bool { return false }

func launcherGet(field int) string {
	buffer := make([]byte, 512)
	C.ov_launcher_get(C.int(field), (*C.char)(unsafe.Pointer(&buffer[0])), C.int(len(buffer)))
	return strings.TrimSpace(string(buffer))
}

func launcherStatus(text string) {
	ptr := cstr(text)
	defer freeCString(ptr)
	C.ov_launcher_set_status(ptr)
}

func refreshWindowList() {
	list := C.GoString(C.ov_windows_list())
	ptr := cstr(list)
	defer freeCString(ptr)
	C.ov_launcher_set_windows(ptr)
}

// largestWindow picks the biggest on-screen window, used by --autostart.
func largestWindow() int {
	count := int(C.ov_window_count())
	best, bestArea := 0, 0.0

	for i := 0; i < count; i++ {
		var x, y, w, h C.double
		if C.ov_window_bounds(C.int(i), &x, &y, &w, &h) == 0 {
			continue
		}
		area := float64(w) * float64(h)
		if area > bestArea {
			best, bestArea = i, area
		}
	}

	return best
}

func runUI(ctx context.Context, o options) (*Relay, error) {
	C.ov_init()
	C.ov_launcher_create(cstr(o.server), cstr(o.room), cstr(o.key))

	var (
		relay     *Relay
		cancel    context.CancelFunc
		canvas    *overlayCanvas
		sc        *scene
		started   bool
		players   []Player
		playerSig string
	)

	defer func() {
		if cancel != nil {
			cancel()
		}
		C.ov_overlay_hide()
	}()

	startHost := func(server, room, key string) {
		o.server = resolveServerURL(server)
		o.room = room
		o.key = key
		saveConfig(viewerConfig{Server: o.server, Room: o.room, Key: o.key, Me: o.me})

		if cancel != nil {
			cancel()
		}
		relay, cancel = startRelay(ctx, o)

		refreshWindowList()
		launcherStatus("Connected. Pick your player and the window to overlay.")
	}

	if o.room != "" && o.key != "" {
		startHost(o.server, o.room, o.key)
	}

	start := time.Now()
	lastLog := time.Now()

	for {
		if ctx.Err() != nil {
			break
		}
		if C.ov_take_quit() == 1 {
			break
		}

		if C.ov_take_show_launcher() == 1 {
			started = false
			C.ov_overlay_hide()
		}
		if o.seconds > 0 && time.Since(start) > time.Duration(o.seconds)*time.Second {
			break
		}

		C.ov_pump()

		if C.ov_launcher_take_connect() == 1 {
			server := launcherGet(0)
			room := launcherGet(1)
			key := launcherGet(2)

			if room == "" || key == "" {
				launcherStatus("Room code and viewer key are required.")
			} else {
				startHost(server, room, key)
			}
		}

		if C.ov_launcher_take_refresh() == 1 {
			refreshWindowList()
		}

		if relay != nil && !started {
			snap := relay.Snapshot()
			if snap.Frame != nil {
				players = snap.Frame.Players

				var sig strings.Builder
				for _, p := range players {
					fmt.Fprintf(&sig, "%d:%s;", p.Index, p.Name)
				}

				if sig.String() != playerSig {
					playerSig = sig.String()

					lines := make([]string, 0, len(players))
					for _, p := range players {
						team := "T"
						if p.Team == 3 {
							team = "CT"
						}
						lines = append(lines, fmt.Sprintf("%d   %-2s   %-20s   %d hp", p.Index, team, p.Name, p.Health))
					}

					ptr := cstr(strings.Join(lines, "\n"))
					C.ov_launcher_set_players(ptr)
					freeCString(ptr)
				}
			}
		}

		if o.autostart && !started && relay != nil && len(players) > 0 {
			index := players[0].Index
			o.meIndex = int(index)

			if canvas == nil {
				C.ov_overlay_create()
				canvas = &overlayCanvas{}
			}

			canvas.windowIndex = largestWindow()
			selectedPlayerIndex = index
			sc = newScene(relay, o)
			sc.selected = index

			started = true
			C.ov_launcher_hide()
			C.ov_overlay_show()
		}

		if o.uiTest && !started && len(players) > 0 {
			o.uiTest = false
			C.ov_ui_test()
		}

		if o.pick >= 0 && relay != nil && !started && len(players) > 0 {
			C.ov_launcher_pick(C.int(o.pick))
			C.ov_launcher_force_start()
			o.pick = -1
		}

		if C.ov_launcher_take_start() == 1 {
			playerIndex := int(C.ov_launcher_player_index())
			windowIndex := int(C.ov_launcher_window_index())

			switch {
			case relay == nil:
				launcherStatus("Connect first.")
			case windowIndex < 0:
				launcherStatus("Pick the window the overlay should attach to.")
			case playerIndex < 0 || playerIndex >= len(players):
				launcherStatus("Pick which player you are.")
			default:
				index := players[playerIndex].Index
				o.meIndex = int(index)

				if canvas == nil {
					C.ov_overlay_create()
					canvas = &overlayCanvas{}
				}

				canvas.windowIndex = windowIndex
				selectedPlayerIndex = index
				sc = newScene(relay, o)
				sc.selected = index

				started = true
				C.ov_launcher_hide()
				C.ov_overlay_show()
			}
		}

		if started && canvas != nil {
			sc.update()
			sc.draw(canvas)
			canvas.Present()

			if time.Since(lastLog) > 2*time.Second {
				lastLog = time.Now()
				snap := relay.Snapshot()
				fmt.Printf("connected=%v host=%v frames=%d bytes=%d gaps=%d age=%s rect=%.0fx%.0f\n",
					snap.Status.Connected, snap.Status.HostOnline, snap.Frames, snap.Bytes, snap.Gaps,
					ageOf(snap.FrameAt), canvas.width, canvas.height)
			}
		}

		time.Sleep(16 * time.Millisecond)
	}

	if o.dump != "" && canvas != nil {
		path := cstr(o.dump)
		ok := C.ov_overlay_dump(path)
		freeCString(path)
		fmt.Printf("overlay dump: %s (ok=%d)\n", o.dump, int(ok))
	}

	return relay, nil
}
