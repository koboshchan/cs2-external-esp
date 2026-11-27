//go:build darwin

package main

// macOS canvas: a normal Ebiten window.
//
// CS2 does not run on macOS, so there is nothing to overlay; this build exists
// to preview the viewer and to develop against the same protocol.

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

type ebitenCanvas struct {
	img *ebiten.Image
}

func (c *ebitenCanvas) Begin() (float32, float32) {
	b := c.img.Bounds()
	c.img.Fill(color.RGBA{9, 10, 14, 255})
	return float32(b.Dx()), float32(b.Dy())
}

func (c *ebitenCanvas) Clear() {
	c.img.Clear()
}

func (c *ebitenCanvas) Rect(x, y, w, h float32, col color.RGBA) {
	if col.A == 0 || w <= 0 || h <= 0 {
		return
	}
	ebitenutil.DrawRect(c.img, float64(x), float64(y), float64(w), float64(h), col)
}

func (c *ebitenCanvas) Line(x1, y1, x2, y2 float32, col color.RGBA) {
	ebitenutil.DrawLine(c.img, float64(x1), float64(y1), float64(x2), float64(y2), col)
}

func (c *ebitenCanvas) Text(x, y float32, s string, col color.RGBA) {
	if s == "" {
		return
	}
	d := &font.Drawer{
		Dst:  c.img,
		Src:  image.NewUniform(col),
		Face: basicfont.Face7x13,
		Dot:  fixed.P(int(x), int(y)+13),
	}
	d.DrawString(s)
}

func (c *ebitenCanvas) Present() {}

func (c *ebitenCanvas) Opaque() bool { return true }

type uiGame struct {
	relay   *Relay
	opts    options
	sc      *scene
	canvas  *ebitenCanvas
	start   time.Time
	lastLog time.Time
}

func (g *uiGame) Update() error {
	if ebiten.IsKeyPressed(ebiten.KeyEscape) {
		return ebiten.Termination
	}

	if inpututil.IsKeyJustPressed(ebiten.KeyTab) {
		requestNextPlayer()
	}

	if g.opts.seconds > 0 && time.Since(g.start) > time.Duration(g.opts.seconds)*time.Second {
		return ebiten.Termination
	}

	if time.Since(g.lastLog) > 2*time.Second {
		g.lastLog = time.Now()
		snap := g.relay.Snapshot()
		fmt.Printf("connected=%v host=%v frames=%d bytes=%d gaps=%d age=%s\n",
			snap.Status.Connected, snap.Status.HostOnline, snap.Frames, snap.Bytes, snap.Gaps, ageOf(snap.FrameAt))
	}

	g.sc.update()
	return nil
}

func (g *uiGame) Draw(screen *ebiten.Image) {
	g.canvas.img = screen
	g.sc.draw(g.canvas)
}

func (g *uiGame) Layout(outsideWidth, outsideHeight int) (int, int) {
	return outsideWidth, outsideHeight
}

func runUI(ctx context.Context, relay *Relay, o options) error {
	g := &uiGame{
		relay:   relay,
		opts:    o,
		sc:      newScene(relay, o),
		canvas:  &ebitenCanvas{},
		start:   time.Now(),
		lastLog: time.Now(),
	}

	ebiten.SetWindowTitle("cs2 remote viewer")
	ebiten.SetWindowSize(1280, 720)
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)

	return ebiten.RunGame(g)
}
