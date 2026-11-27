package main

// Ebiten window: draws boxes, skeletons, health and flags for the selected
// player's camera.

import (
	"context"
	"fmt"
	"image/color"
	"math"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
)

var (
	colorT       = color.RGBA{235, 150, 50, 255}
	colorCT      = color.RGBA{80, 160, 255, 255}
	colorHUD     = color.RGBA{200, 205, 215, 255}
	colorDim     = color.RGBA{120, 125, 140, 255}
	colorBomb    = color.RGBA{255, 210, 60, 255}
	colorHealth  = color.RGBA{80, 220, 120, 255}
	colorHealthLow = color.RGBA{235, 90, 80, 255}
)

type uiGame struct {
	relay    *Relay
	opts     options
	selected int8 // -1 = auto
}

func runUI(ctx context.Context, relay *Relay, o options) error {
	g := &uiGame{relay: relay, opts: o, selected: -1}

	ebiten.SetWindowTitle("cs2 remote viewer")
	ebiten.SetWindowSize(1280, 720)
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)

	return ebiten.RunGame(g)
}

func (g *uiGame) Update() error {
	if ebiten.IsKeyPressed(ebiten.KeyEscape) {
		return ebiten.Termination
	}

	if inpututil.IsKeyJustPressed(ebiten.KeyTab) {
		snap := g.relay.Snapshot()
		if snap.Frame != nil && len(snap.Frame.Players) > 0 {
			current := g.currentPlayer(snap.Frame)
			next := int8(0)
			if current != nil {
				next = current.Index + 1
			}
			// Skip to the next existing index.
			for i := 0; i < len(snap.Frame.Players); i++ {
				if p := snap.Frame.PlayerByIndex(int(next)); p != nil {
					g.selected = p.Index
					return nil
				}
				next++
			}
		}
	}

	return nil
}

func (g *uiGame) Layout(outsideWidth, outsideHeight int) (int, int) {
	return outsideWidth, outsideHeight
}

func (g *uiGame) currentPlayer(f *Frame) *Player {
	if g.selected >= 0 {
		if p := f.PlayerByIndex(int(g.selected)); p != nil {
			return p
		}
	}
	return selectPlayer(f, g.opts)
}

func (g *uiGame) Draw(screen *ebiten.Image) {
	screen.Fill(color.RGBA{9, 10, 14, 255})

	w := float32(screen.Bounds().Dx())
	h := float32(screen.Bounds().Dy())

	snap := g.relay.Snapshot()

	if snap.Frame == nil {
		ebitenutil.DebugPrintAt(screen, "waiting for host...", 16, 16)
		ebitenutil.DebugPrintAt(screen, "room "+g.opts.room, 16, 32)
		if snap.Status.Message != "" {
			ebitenutil.DebugPrintAt(screen, truncate(snap.Status.Message, 90), 16, 48)
		}
		return
	}

	f := snap.Frame
	me := g.currentPlayer(f)

	if me == nil {
		ebitenutil.DebugPrintAt(screen, "no players in snapshot", 16, 16)
		return
	}

	aspect := w / h
	m := BuildViewMatrix(me.EyePos, me.EyeAngles, me.Fov, aspect, f.Camera.FovMode)

	drawGrid(screen, w, h)

	for i := range f.Players {
		p := &f.Players[i]
		if p.Index == me.Index || !p.Alive() {
			continue
		}
		g.drawPlayer(screen, m, w, h, p, p.Team == me.Team)
	}

	g.drawBomb(screen, m, w, h, f)

	// HUD
	hud := fmt.Sprintf("room %s   seq %d   %s ago   %d fps   players %d   you: %s (%d)",
		snap.Status.Room, f.Seq, ageOf(snap.FrameAt), int(ebiten.ActualFPS()),
		len(f.Players), me.Name, me.Index)
	ebitenutil.DebugPrintAt(screen, hud, 16, 16)

	hint := "TAB = next player    ESC = quit"
	ebitenutil.DebugPrintAt(screen, hint, 16, int(h)-28)

	if !snap.Status.HostOnline {
		ebitenutil.DebugPrintAt(screen, "host offline - last frame shown", 16, 34)
	}
	if snap.Gaps > 0 {
		ebitenutil.DebugPrintAt(screen, fmt.Sprintf("gaps: %d", snap.Gaps), 16, 50)
	}
}

func (g *uiGame) drawPlayer(screen *ebiten.Image, m [16]float32, w, h float32, p *Player, mate bool) {
	boxColor := colorCT
	if p.Team == 2 {
		boxColor = colorT
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

	drawRectOutline(screen, minX, minY, maxX-minX, maxY-minY, boxColor)

	// Skeleton
	for _, c := range skeletonConnections {
		a, okA := projected[c[0]]
		b, okB := projected[c[1]]
		if !okA || !okB {
			continue
		}
		ebitenutil.DrawLine(screen, float64(a.X), float64(a.Y), float64(b.X), float64(b.Y), color.RGBA{boxColor.R, boxColor.G, boxColor.B, 160})
	}

	// Health bar
	health := float32(p.Health)
	if health > 100 {
		health = 100
	}
	barH := maxY - minY
	barColor := colorHealth
	if health <= 35 {
		barColor = colorHealthLow
	}
	ebitenutil.DrawRect(screen, float64(minX-6), float64(maxY-barH*(health/100)), 3, float64(barH*(health/100)), barColor)
	ebitenutil.DrawRect(screen, float64(minX-6), float64(minY), 3, float64(barH), color.RGBA{40, 42, 48, 255})

	// Name + flags
	label := p.Name
	if p.Bot() {
		label = "[bot] " + label
	}
	ebitenutil.DebugPrintAt(screen, truncate(label, 24), int(minX), int(minY)-14)

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
		ebitenutil.DebugPrintAt(screen, flags, int(maxX)+4, int(minY))
	}

	if mate {
		ebitenutil.DebugPrintAt(screen, "mate", int(minX), int(maxY)+2)
	}
}

func (g *uiGame) drawBomb(screen *ebiten.Image, m [16]float32, w, h float32, f *Frame) {
	if !f.Bomb.IsPlanted {
		return
	}
	sp, ok := Project(m, f.Bomb.Pos, w, h)
	if !ok {
		return
	}
	ebitenutil.DrawRect(screen, float64(sp.X)-4, float64(sp.Y)-4, 8, 8, colorBomb)
	ebitenutil.DebugPrintAt(screen, fmt.Sprintf("C4 %.1fs", f.Bomb.TimeLeft), int(sp.X)+8, int(sp.Y)-6)
}

func drawRectOutline(dst *ebiten.Image, x, y, w, h float32, clr color.Color) {
	ebitenutil.DrawRect(dst, float64(x), float64(y), float64(w), 1.5, clr)
	ebitenutil.DrawRect(dst, float64(x), float64(y+h-1.5), float64(w), 1.5, clr)
	ebitenutil.DrawRect(dst, float64(x), float64(y), 1.5, float64(h), clr)
	ebitenutil.DrawRect(dst, float64(x+w-1.5), float64(y), 1.5, float64(h), clr)
}

func drawGrid(dst *ebiten.Image, w, h float32) {
	grid := color.RGBA{22, 24, 30, 255}
	for x := float32(0); x < w; x += 64 {
		ebitenutil.DrawRect(dst, float64(x), 0, 1, float64(h), grid)
	}
	for y := float32(0); y < h; y += 64 {
		ebitenutil.DrawRect(dst, 0, float64(y), float64(w), 1, grid)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
