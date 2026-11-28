package main

// Shared scene: turns snapshots into draw calls on a canvas.
//
// The canvas is implemented per platform:
//   windows: layered, click-through, always-on-top overlay (Win32 + GDI)
//   darwin:  transparent click-through overlay (AppKit + Core Graphics)

import (
	"fmt"
	"image/color"
	"math"
	"sync/atomic"
	"time"
)

var (
	colorT         = color.RGBA{235, 150, 50, 255}
	colorCT        = color.RGBA{80, 160, 255, 255}
	colorDim       = color.RGBA{120, 125, 140, 255}
	colorBomb      = color.RGBA{255, 210, 60, 255}
	colorHealth    = color.RGBA{80, 220, 120, 255}
	colorHealthLow = color.RGBA{235, 90, 80, 255}
)

type canvas interface {
	// Begin prepares a frame and returns the drawable size.
	Begin() (width, height float32)
	Clear()
	Rect(x, y, w, h float32, c color.RGBA)
	Line(x1, y1, x2, y2 float32, c color.RGBA)
	Text(x, y float32, s string, c color.RGBA)
	Present()

	// Opaque is true for a window with its own background. Overlays are
	// transparent, so they skip the background grid.
	Opaque() bool
}

// Overridden per platform: the Windows overlay is click-through, so it cannot
// receive keys and uses global hotkeys; macOS reopens the launcher instead.
var controlsHint = "F8 = next player    END = quit"

var nextPlayerRequested atomic.Bool

func requestNextPlayer() { nextPlayerRequested.Store(true) }

func consumeNextPlayer() bool { return nextPlayerRequested.Swap(false) }

type displaySettings struct {
	Boxes     bool
	Health    bool
	Skeleton  bool
	Names     bool
	Teammates bool
}

func defaultDisplaySettings() displaySettings {
	return displaySettings{Boxes: true, Health: true, Skeleton: true, Names: true, Teammates: true}
}

type playerPalette struct {
	T, CT, Health, HealthLow, Name color.RGBA
}

func defaultPlayerPalette() playerPalette {
	return playerPalette{T: colorT, CT: colorCT, Health: colorHealth,
		HealthLow: colorHealthLow, Name: color.RGBA{235, 235, 240, 255}}
}

type scene struct {
	relay         *Relay
	opts          options
	selected      int8 // -1 = auto
	useHostMatrix bool
	display       displaySettings
	palette       playerPalette
	viewKey       string
	delayMS       int
}

func newScene(relay *Relay, o options) *scene {
	return &scene{relay: relay, opts: o, selected: -1, display: defaultDisplaySettings(), palette: defaultPlayerPalette(), delayMS: defaultDisplayDelayMS}
}

func (s *scene) update() {
	if !consumeNextPlayer() {
		return
	}

	snap := s.relay.Snapshot()
	if snap.Frame == nil {
		return
	}

	if next := nextPlayerIndex(snap.Frame.Players, s.currentPlayer(snap.Frame)); next >= 0 {
		s.selected = next
	}
}

// nextPlayerIndex returns the player after current, wrapping around the list.
// Dead players are skipped while at least one player is alive.
func nextPlayerIndex(players []Player, current *Player) int8 {
	if len(players) == 0 {
		return -1
	}

	order := make([]int, 0, len(players))
	for i := range players {
		if players[i].Alive() {
			order = append(order, i)
		}
	}
	if len(order) == 0 {
		for i := range players {
			order = append(order, i)
		}
	}

	position := -1
	if current != nil {
		for k, i := range order {
			if players[i].Index == current.Index {
				position = k
				break
			}
		}
	}

	return players[order[(position+1)%len(order)]].Index
}

// hiddenPlayer is the optional player to leave out of the render (usually the
// viewer's own pawn when they are in the same match).
func (s *scene) hiddenPlayer(f *Frame) *Player {
	if s.selected >= 0 {
		if p := f.PlayerByIndex(int(s.selected)); p != nil {
			return p
		}
	}
	return nil
}

func (s *scene) currentPlayer(f *Frame) *Player {
	if s.selected >= 0 {
		if p := f.PlayerByIndex(int(s.selected)); p != nil {
			return p
		}
	}
	return selectPlayer(f, s.opts)
}

func (s *scene) draw(c canvas) {
	w, h := c.Begin()

	snap := s.relay.DisplaySnapshot(s.delayMS)

	if snap.Frame == nil {
		c.Text(16, 16, "waiting for host...", color.RGBA{230, 230, 235, 255})
		c.Text(16, 32, "room "+s.opts.room, colorDim)
		if snap.Status.Message != "" {
			c.Text(16, 48, truncate(snap.Status.Message, 90), colorHealthLow)
		}
		return
	}

	f := snap.Frame

	m, host, available := s.cameraForFrame(f, w/h)
	if !available {
		c.Text(16, 16, "Selected player is unavailable. Choose a viewpoint in controls.", colorBomb)
		return
	}
	hidden := s.hiddenPlayer(f)
	reference := host
	if hidden != nil {
		reference = hidden
	}

	if c.Opaque() {
		s.drawGrid(c, w, h)
	}

	for i := range f.Players {
		p := &f.Players[i]
		if !p.Alive() {
			continue
		}
		if host != nil && p.Index == host.Index {
			continue // the camera's own player
		}
		if hidden != nil && p.Index == hidden.Index {
			continue
		}
		mate := reference != nil && p.Team == reference.Team
		if mate && !s.display.Teammates {
			continue
		}
		s.drawPlayer(c, m, w, h, p, mate)
	}

	s.drawBomb(c, m, w, h, f)

	viewer := "host"
	if host != nil {
		viewer = host.Name
	}
	hud := fmt.Sprintf("room %s   seq %d   %s ago   players %d   view: %s",
		snap.Status.Room, f.Seq, ageOf(snap.FrameAt), len(f.Players), viewer)
	c.Text(16, 16, hud, color.RGBA{200, 205, 215, 255})

	c.Text(16, h-24, controlsHint, colorDim)

	if !snap.Status.Connected || !snap.Status.HostOnline || snap.Status.Message != "" || time.Since(snap.FrameAt) > 3*time.Second {
		c.Text(16, 34, StatusText(snap, len(f.Players)), colorBomb)
	}
	if snap.Gaps > 0 {
		c.Text(16, 50, fmt.Sprintf("gaps: %d", snap.Gaps), colorDim)
	}
}

func (s *scene) drawPlayer(c canvas, m [16]float32, w, h float32, p *Player, mate bool) {
	boxColor := s.palette.CT
	if p.Team == 2 {
		boxColor = s.palette.T
	}

	minX, minY := float32(math.MaxFloat32), float32(math.MaxFloat32)
	maxX, maxY := float32(-math.MaxFloat32), float32(-math.MaxFloat32)
	any := false

	projected := make(map[int]Vec2, 24)

	for i := 0; i < 32; i++ {
		if p.BoneMask&(1<<uint(i)) == 0 {
			continue
		}
		bone, ok := p.Bone(i)
		if !ok {
			continue
		}
		sp, ok := Project(m, bone, w, h)
		if !ok {
			continue
		}
		projected[i] = sp
		any = true
		if sp.X < minX {
			minX = sp.X
		}
		if sp.X > maxX {
			maxX = sp.X
		}
		if sp.Y < minY {
			minY = sp.Y
		}
		if sp.Y > maxY {
			maxY = sp.Y
		}
	}

	if !any {
		return
	}

	pad := (maxX - minX) * 0.12
	if pad < 2 {
		pad = 2
	}
	minX -= pad
	maxX += pad
	minY -= pad * 2
	maxY += pad

	if s.display.Boxes {
		// box
		c.Rect(minX, minY, maxX-minX, 1.5, boxColor)
		c.Rect(minX, maxY-1.5, maxX-minX, 1.5, boxColor)
		c.Rect(minX, minY, 1.5, maxY-minY, boxColor)
		c.Rect(maxX-1.5, minY, 1.5, maxY-minY, boxColor)

	}

	if s.display.Skeleton {
		// skeleton
		skeletonColor := color.RGBA{boxColor.R, boxColor.G, boxColor.B, 160}
		for _, conn := range skeletonConnections {
			a, okA := projected[conn[0]]
			b, okB := projected[conn[1]]
			if !okA || !okB {
				continue
			}
			c.Line(a.X, a.Y, b.X, b.Y, skeletonColor)
		}

	}

	if s.display.Health {
		// health
		health := float32(p.Health)
		if health > 100 {
			health = 100
		}
		barH := maxY - minY
		barColor := s.palette.Health
		if health <= 35 {
			barColor = s.palette.HealthLow
		}
		c.Rect(minX-6, minY, 3, barH, color.RGBA{40, 42, 48, 255})
		c.Rect(minX-6, maxY-barH*(health/100), 3, barH*(health/100), barColor)

	}

	if !s.display.Names {
		return
	}

	// name + flags
	label := p.Name
	if p.Bot() {
		label = "[bot] " + label
	}
	c.Text(minX, minY-14, truncate(label, 24), s.palette.Name)

	flags := ""
	if p.Scoped() {
		flags += "S"
	}
	if p.Flashed() {
		flags += "F"
	}
	if p.Defusing() {
		flags += "D"
	}
	if p.HasC4() {
		flags += "C4"
	}
	if p.Reloading() {
		flags += "R"
	}
	if flags != "" {
		c.Text(maxX+4, minY, flags, colorDim)
	}

	if mate {
		c.Text(minX, maxY+2, "mate", colorDim)
	}
}

func (s *scene) drawBomb(c canvas, m [16]float32, w, h float32, f *Frame) {
	if !f.Bomb.IsPlanted {
		return
	}
	sp, ok := Project(m, f.Bomb.Pos, w, h)
	if !ok {
		return
	}
	c.Rect(sp.X-4, sp.Y-4, 8, 8, colorBomb)
	c.Text(sp.X+8, sp.Y-6, fmt.Sprintf("C4 %.1fs", f.Bomb.TimeLeft), colorBomb)
}

func (s *scene) drawGrid(c canvas, w, h float32) {
	grid := color.RGBA{28, 30, 38, 255}
	for x := float32(0); x < w; x += 64 {
		c.Rect(x, 0, 1, h, grid)
	}
	for y := float32(0); y < h; y += 64 {
		c.Rect(0, y, w, 1, grid)
	}
}

func ageOf(t time.Time) string {
	if t.IsZero() {
		return "n/a"
	}
	return time.Since(t).Round(time.Millisecond).String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
