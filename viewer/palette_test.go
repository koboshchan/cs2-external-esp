package main

import (
	"image/color"
	"testing"
)

type paletteCanvas struct {
	countingCanvas
	rectColors []color.RGBA
	lineColors []color.RGBA
	textColors []color.RGBA
}

func (c *paletteCanvas) Rect(_, _, _, _ float32, col color.RGBA) {
	c.rectColors = append(c.rectColors, col)
}

func (c *paletteCanvas) Line(_, _, _, _ float32, col color.RGBA) {
	c.lineColors = append(c.lineColors, col)
}

func (c *paletteCanvas) Text(_, _ float32, _ string, col color.RGBA) {
	c.textColors = append(c.textColors, col)
}

func TestPlayerPaletteReachesDrawing(t *testing.T) {
	s := newScene(&Relay{}, options{})
	s.palette = playerPalette{
		T: color.RGBA{10, 20, 30, 255}, CT: color.RGBA{40, 50, 60, 255},
		Health: color.RGBA{70, 80, 90, 255}, HealthLow: color.RGBA{100, 110, 120, 255},
		Name: color.RGBA{130, 140, 150, 255},
	}
	p := &Player{Team: 2, Health: 100, Name: "B", BoneMask: (1 << 6) | (1 << 7),
		Bones: []Vec3{{100, -10, 0}, {100, 10, 20}}}
	m := BuildViewMatrix(Vec3{}, Vec2{}, 90, 16.0/9, fovSourceHorPlus)
	c := &paletteCanvas{}
	s.drawPlayer(c, m, 1920, 1080, p, false)
	if len(c.rectColors) != 6 || len(c.lineColors) != 1 || len(c.textColors) != 1 {
		t.Fatalf("unexpected draw counts: %d %d %d", len(c.rectColors), len(c.lineColors), len(c.textColors))
	}
	if c.rectColors[0] != s.palette.T || c.rectColors[5] != s.palette.Health || c.textColors[0] != s.palette.Name {
		t.Fatalf("custom T, health or name color was ignored: %+v", c)
	}
	if c.lineColors[0] != (color.RGBA{10, 20, 30, 160}) {
		t.Fatalf("skeleton did not inherit T color: %v", c.lineColors[0])
	}
	p.Team, p.Health = 3, 20
	c = &paletteCanvas{}
	s.drawPlayer(c, m, 1920, 1080, p, false)
	if c.rectColors[0] != s.palette.CT || c.rectColors[5] != s.palette.HealthLow {
		t.Fatalf("custom CT or low-health color was ignored: %+v", c)
	}
}
