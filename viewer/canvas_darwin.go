//go:build darwin

package main

// macOS client: an Ebiten window with a connect form, then the overlay view.
//
// CS2 does not run on macOS, so there is nothing to overlay; this build is the
// preview/development client and uses the same protocol and scene as the
// Windows overlay.

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"strings"
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

func (c *ebitenCanvas) Clear() { c.img.Clear() }

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

type formField int

const (
	fieldServer formField = iota
	fieldRoom
	fieldKey
	fieldCount
)

type connectForm struct {
	values [fieldCount]string
	focus  formField
}

func (f *connectForm) update() bool {
	for _, r := range ebiten.AppendInputChars(nil) {
		if r >= 32 && r != 127 {
			f.values[f.focus] += string(r)
		}
	}

	if inpututil.IsKeyJustPressed(ebiten.KeyBackspace) {
		v := []rune(f.values[f.focus])
		if len(v) > 0 {
			f.values[f.focus] = string(v[:len(v)-1])
		}
	}

	if inpututil.IsKeyJustPressed(ebiten.KeyTab) || inpututil.IsKeyJustPressed(ebiten.KeyDown) {
		f.focus = (f.focus + 1) % fieldCount
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyUp) {
		f.focus = (f.focus + fieldCount - 1) % fieldCount
	}

	if inpututil.IsKeyJustPressed(ebiten.KeyEnter) {
		return true
	}

	return false
}

func (f *connectForm) draw(c canvas, w, h float32) {
	white := color.RGBA{230, 230, 235, 255}
	dim := color.RGBA{140, 145, 160, 255}
	accent := color.RGBA{255, 210, 60, 255}

	c.Text(24, 24, "cs2 remote viewer", white)
	c.Text(24, 44, "Connect to a host. The room code and viewer key are in the host's Host tab.", dim)

	labels := [fieldCount]string{"Server", "Room code", "Viewer key"}
	for i := 0; i < int(fieldCount); i++ {
		y := 90 + float32(i)*44
		c.Text(24, y+6, labels[i], dim)

		box := color.RGBA{26, 28, 36, 255}
		if formField(i) == f.focus {
			box = color.RGBA{38, 42, 54, 255}
		}
		c.Rect(130, y, 330, 24, box)
		c.Rect(130, y+23, 330, 1, accent)

		value := f.values[i]
		c.Text(136, y+6, value, white)

		if formField(i) == f.focus {
			caretX := 136 + float32(len(value))*7
			c.Rect(caretX, y+5, 1, 14, white)
		}
	}

	c.Text(24, 232, "TAB next field    ENTER connect    ESC quit", dim)

	if msg := strings.TrimSpace(f.values[fieldRoom]); msg == "" {
		c.Text(24, 254, "The host shows the room code after pressing Host.", dim)
	}
}

type uiGame struct {
	ctx   context.Context
	opts  options
	form  *connectForm
	relay *Relay
	cancel context.CancelFunc
	sc    *scene
	canvas *ebitenCanvas

	viewer  bool
	start   time.Time
	lastLog time.Time
}

func (g *uiGame) Update() error {
	if ebiten.IsKeyPressed(ebiten.KeyEscape) {
		return ebiten.Termination
	}

	if g.opts.seconds > 0 && time.Since(g.start) > time.Duration(g.opts.seconds)*time.Second {
		return ebiten.Termination
	}

	if !g.viewer {
		if g.form.update() {
			server := strings.TrimSpace(g.form.values[fieldServer])
			room := strings.TrimSpace(g.form.values[fieldRoom])
			key := strings.TrimSpace(g.form.values[fieldKey])

			if room != "" && key != "" {
				g.opts.server = resolveServerURL(server)
				g.opts.room = room
				g.opts.key = key
				saveConfig(viewerConfig{Server: g.opts.server, Room: room, Key: key, Me: g.opts.me})

				g.relay, g.cancel = startRelay(g.ctx, g.opts)
				g.sc = newScene(g.relay, g.opts)
				g.viewer = true
			}
		}
		return nil
	}

	if inpututil.IsKeyJustPressed(ebiten.KeyTab) {
		requestNextPlayer()
	}
	g.sc.update()

	if time.Since(g.lastLog) > 2*time.Second {
		g.lastLog = time.Now()
		snap := g.relay.Snapshot()
		fmt.Printf("connected=%v host=%v frames=%d bytes=%d gaps=%d age=%s\n",
			snap.Status.Connected, snap.Status.HostOnline, snap.Frames, snap.Bytes, snap.Gaps, ageOf(snap.FrameAt))
	}

	return nil
}

func (g *uiGame) Draw(screen *ebiten.Image) {
	g.canvas.img = screen

	if !g.viewer {
		w, h := g.canvas.Begin()
		g.form.draw(g.canvas, w, h)
		return
	}

	g.sc.draw(g.canvas)
}

func (g *uiGame) Layout(outsideWidth, outsideHeight int) (int, int) {
	return outsideWidth, outsideHeight
}

func runUI(ctx context.Context, o options) (*Relay, error) {
	form := &connectForm{}
	form.values[fieldServer] = o.server
	form.values[fieldRoom] = o.room
	form.values[fieldKey] = o.key

	g := &uiGame{
		ctx:     ctx,
		opts:    o,
		form:    form,
		canvas:  &ebitenCanvas{},
		start:   time.Now(),
		lastLog: time.Now(),
	}

	// Saved credentials skip the form.
	if o.room != "" && o.key != "" {
		g.relay, g.cancel = startRelay(ctx, o)
		g.sc = newScene(g.relay, o)
		g.viewer = true
	}

	ebiten.SetWindowTitle("cs2 remote viewer")
	ebiten.SetWindowSize(1100, 680)
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)

	err := ebiten.RunGame(g)

	if g.cancel != nil {
		g.cancel()
	}

	return g.relay, err
}
