//go:build darwin

package main

// macOS client: native launcher + transparent click-through overlay, both
// driven from Go through the Objective-C bridge in ui_darwin.m.

/*
#cgo CFLAGS: -fobjc-arc
#cgo LDFLAGS: -framework Cocoa -framework CoreGraphics -framework CoreText -framework QuartzCore -framework ImageIO -framework Carbon

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
int ov_follow_geforce(void);
int ov_stream_aspect(void);
double ov_top_inset(void);
uint32_t ov_geforce_window_id(void);
int ov_geforce_frontmost(void);
int ov_controls_enable(int enabled);
unsigned int ov_controls_flags(void);
int ov_controls_overlay_hidden(void);
uint32_t ov_controls_color(int index);
void ov_controls_set_players(const char* lines);
void ov_controls_set_view_key(const char* key);
const char* ov_controls_view_key(void);

const char* ov_windows_list(void);
int  ov_window_count(void);
uint32_t ov_launcher_window_id(int index);
int  ov_window_bounds_by_id(uint32_t wid, double* x, double* y, double* w, double* h);
int  ov_follow_test(void);
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
	windowID        uint32
	width           float32
	height          float32
	relay           *Relay
	followGeForce   bool
	aspectMode      int
	topInset        float64
	lastWindowCheck time.Time
	shown           bool

	// Last known window rect (top-left points), kept so a transient lookup
	// failure does not make the overlay jump to full screen.
	haveRect     bool
	rectX        float64
	rectY        float64
	rectW        float64
	rectH        float64
	missingSince time.Time
}

func (c *overlayCanvas) attach(windowID uint32, relay *Relay) {
	c.windowID = windowID
	c.relay = relay
	c.followGeForce = C.ov_follow_geforce() != 0
	c.aspectMode = int(C.ov_stream_aspect())
	c.topInset = float64(C.ov_top_inset())
	c.haveRect = false
	c.missingSince = time.Time{}
	c.lastWindowCheck = time.Time{}
	c.shown = true
}

func cstr(s string) *C.char {
	return C.CString(s)
}

func freeCString(p *C.char) {
	if p != nil {
		C.free(unsafe.Pointer(p))
	}
}

func colorFromRGB(value uint32) color.RGBA {
	return color.RGBA{R: uint8(value >> 16), G: uint8(value >> 8), B: uint8(value), A: 255}
}

func (c *overlayCanvas) Begin() (float32, float32) {
	var x, y, w, h C.double
	now := time.Now()
	refreshWindow := c.followGeForce && now.Sub(c.lastWindowCheck) >= 250*time.Millisecond
	if refreshWindow {
		c.windowID = uint32(C.ov_geforce_window_id())
		c.lastWindowCheck = now
	}

	// Re-read the window's frame every frame so the overlay follows moves and
	// resizes. kCGWindowListOptionIncludingWindow makes this a single-window
	// query, cheap enough for 60 Hz.
	windowAvailable := c.windowID != 0 && C.ov_window_bounds_by_id(C.uint32_t(c.windowID), &x, &y, &w, &h) != 0
	if windowAvailable {
		c.haveRect = true
		c.rectX, c.rectY, c.rectW, c.rectH = float64(x), float64(y), float64(w), float64(h)
		c.missingSince = time.Time{}
	} else {
		if c.missingSince.IsZero() {
			c.missingSince = now
		}
		if c.haveRect {
			x, y, w, h = C.double(c.rectX), C.double(c.rectY), C.double(c.rectW), C.double(c.rectH)
		} else {
			C.ov_screen_size(&w, &h)
			x, y = 0, 0
		}
	}

	left, top, width, height := float64(x), float64(y), float64(w), float64(h)
	if c.followGeForce {
		inset := min(c.topInset, max(0, height-1))
		top += inset
		height -= inset
		var frame *Frame
		if c.relay != nil {
			frame = c.relay.Snapshot().Frame
		}
		left, top, width, height = fitViewport(left, top, width, height, streamAspect(c.aspectMode, frame))
	}
	c.width = float32(width)
	c.height = float32(height)

	C.ov_overlay_begin(C.double(left), C.double(top), C.double(width), C.double(height))
	if c.followGeForce {
		show := windowAvailable && C.ov_geforce_frontmost() != 0
		// Reassert ordering after a Space transition even if the requested
		// visibility stayed true while macOS moved the streaming window.
		if show && (!c.shown || refreshWindow) {
			C.ov_overlay_show()
		} else if !show && c.shown {
			C.ov_overlay_hide()
		}
		c.shown = show
	}
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

	// The C side NUL-terminates; the rest of the buffer is padding.
	end := 0
	for end < len(buffer) && buffer[end] != 0 {
		end++
	}

	return strings.TrimSpace(string(buffer[:end]))
}

func launcherStatus(text string) {
	ptr := cstr(text)
	defer freeCString(ptr)
	C.ov_launcher_set_status(ptr)
}

func (s *scene) configureMacView(players []Player, index int8) {
	list := cstr(playerViewList(players))
	C.ov_controls_set_players(list)
	freeCString(list)
	s.viewKey = ""
	for i := range players {
		if players[i].Index == index {
			s.viewKey = playerViewKey(&players[i])
			break
		}
	}
	key := cstr(s.viewKey)
	C.ov_controls_set_view_key(key)
	freeCString(key)
	s.selected = -1
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
		id := C.ov_launcher_window_id(C.int(i))
		if id == 0 || C.ov_window_bounds_by_id(id, &x, &y, &w, &h) == 0 {
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
	controlsHint = "⌘P = 显示控制台"
	C.ov_init()
	C.ov_launcher_create(cstr(o.server), cstr(o.room), cstr(o.key))

	if o.uiTest {
		fmt.Printf("fields: server=%q room=%q key=%q\n", launcherGet(0), launcherGet(1), launcherGet(2))
	}

	if o.followTest {
		fmt.Printf("follow test: %d\n", int(C.ov_follow_test()))
		return nil, nil
	}

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
		C.ov_controls_enable(0)
		if cancel != nil {
			cancel()
		}
		C.ov_overlay_hide()
	}()

	startHost := func(server, room, key string) {
		o.server = resolveServerURL(server)
		o.room = strings.ToUpper(strings.TrimSpace(room))
		o.key = strings.TrimSpace(key)
		saveConfig(viewerConfig{Server: o.server, Room: o.room, Key: o.key, Me: o.me})

		if cancel != nil {
			cancel()
		}
		started = false
		players = nil
		playerSig = ""
		sc = nil
		C.ov_overlay_hide()
		C.ov_launcher_set_players(nil)
		relay, cancel = startRelay(ctx, o)

		refreshWindowList()
		launcherStatus("Connecting to relay and authenticating...")
	}

	if o.room != "" && o.key != "" {
		startHost(o.server, o.room, o.key)
	}

	start := time.Now()
	lastLog := time.Now()
	lastStatus := time.Now()
	lastStatusText := ""
	lastControlPlayers := ""
	lastControlPlayersCheck := time.Time{}
	geforceID := uint32(0)
	lastGeForceCheck := time.Time{}

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
			room := strings.TrimSpace(launcherGet(1))
			key := strings.TrimSpace(launcherGet(2))

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
			if snap.Frame == nil && len(players) > 0 {
				players = nil
				playerSig = ""
				C.ov_launcher_set_players(nil)
			}
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

					launcherStatus(fmt.Sprintf("Connected - %d players. Choose your player, then Start overlay.", len(players)))
				}
			}
		}

		if C.ov_follow_geforce() != 0 && !started && time.Since(lastGeForceCheck) >= 250*time.Millisecond {
			geforceID = uint32(C.ov_geforce_window_id())
			lastGeForceCheck = time.Now()
		}
		if o.autostart && !started && relay != nil && len(players) > 0 {
			index := players[0].Index
			o.meIndex = int(index)

			if canvas == nil {
				C.ov_overlay_create()
				canvas = &overlayCanvas{}
			}

			largest := largestWindow()
			windowID := uint32(C.ov_launcher_window_id(C.int(largest)))
			if C.ov_follow_geforce() != 0 {
				windowID = geforceID
			}
			canvas.attach(windowID, relay)
			selectedPlayerIndex = index
			sc = newScene(relay, o)
			sc.useHostMatrix = canvas.followGeForce
			sc.configureMacView(players, index)

			started = true
			C.ov_launcher_hide()
			C.ov_overlay_show()
		}

		if relay != nil && !started && time.Since(lastStatus) > 500*time.Millisecond {
			lastStatus = time.Now()
			text := StatusText(relay.Snapshot(), len(players))
			if text != lastStatusText {
				lastStatusText = text
				fmt.Printf("status: %s\n", text)
			}
			launcherStatus(text)
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
			case !relay.Snapshot().Status.Connected || len(players) == 0:
				launcherStatus("Wait for an authenticated connection and fresh player data.")
			case C.ov_follow_geforce() == 0 && windowIndex < 0:
				launcherStatus("Pick the window the overlay should attach to.")
			default:
				index := int8(-1)
				if playerIndex >= 0 && playerIndex < len(players) {
					index = players[playerIndex].Index
				}
				o.meIndex = int(index)

				if canvas == nil {
					C.ov_overlay_create()
					canvas = &overlayCanvas{}
				}

				windowID := uint32(C.ov_launcher_window_id(C.int(windowIndex)))
				if C.ov_follow_geforce() != 0 {
					windowID = geforceID
				}
				canvas.attach(windowID, relay)
				selectedPlayerIndex = index
				sc = newScene(relay, o)
				sc.useHostMatrix = canvas.followGeForce
				sc.configureMacView(players, index)

				started = true
				C.ov_launcher_hide()
				C.ov_overlay_show()
			}
		}

		if started && canvas != nil && !canvas.followGeForce && !canvas.missingSince.IsZero() && time.Since(canvas.missingSince) > 2*time.Second {
			// GFN keeps its mapping session while a fullscreen window is
			// replaced or temporarily leaves the visible Space.
			started = false
			C.ov_overlay_hide()
			refreshWindowList()
			C.ov_launcher_show()
			launcherStatus("The overlaid window closed.")
		}

		enableControls := C.int(0)
		if started {
			enableControls = 1
		}
		if status := C.ov_controls_enable(enableControls); status != 0 {
			fmt.Printf("Command+P registration failed (%d). Use the CS2 menu to open controls.\n", int(status))
		}
		if started && canvas != nil {
			if time.Since(lastControlPlayersCheck) >= 250*time.Millisecond {
				list := ""
				if frame := relay.Snapshot().Frame; frame != nil {
					list = playerViewList(frame.Players)
				}
				if list != lastControlPlayers {
					text := cstr(list)
					C.ov_controls_set_players(text)
					freeCString(text)
					lastControlPlayers = list
				}
				lastControlPlayersCheck = time.Now()
			}
			sc.viewKey = C.GoString(C.ov_controls_view_key())
			flags := uint32(C.ov_controls_flags())
			sc.display = displaySettings{Boxes: flags&1 != 0, Health: flags&2 != 0,
				Skeleton: flags&4 != 0, Names: flags&8 != 0, Teammates: flags&16 != 0}
			sc.palette = playerPalette{
				T:         colorFromRGB(uint32(C.ov_controls_color(0))),
				CT:        colorFromRGB(uint32(C.ov_controls_color(1))),
				Health:    colorFromRGB(uint32(C.ov_controls_color(2))),
				HealthLow: colorFromRGB(uint32(C.ov_controls_color(3))),
				Name:      colorFromRGB(uint32(C.ov_controls_color(4))),
			}
			sc.update()
			if C.ov_controls_overlay_hidden() != 0 {
				C.ov_overlay_hide()
				canvas.shown = false
			} else {
				sc.draw(canvas)
				canvas.Present()
			}

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
