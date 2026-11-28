package main

import (
	"image/color"
	"math"
	"testing"
	"time"
)

func TestSelectedPlayerDefinesCamera(t *testing.T) {
	f := &Frame{Players: []Player{
		{Index: 0, SteamID: 10, Name: "A", EyePos: Vec3{0, 0, 64}, EyeAngles: Vec2{}, Fov: 90},
		{Index: 1, SteamID: 20, Name: "B", EyePos: Vec3{100, 100, 64}, EyeAngles: Vec2{0, 90}, Fov: 90},
	}}
	f.Camera.HostIndex = 0
	f.Camera.HostEyePos = f.Players[0].EyePos
	f.Camera.HostFov = 90
	s := newScene(&Relay{}, options{})
	s.viewKey = playerViewKey(&f.Players[1])
	m, player, available := s.cameraForFrame(f, 16.0/9)
	if !available || player.SteamID != 20 {
		t.Fatal("B was not selected")
	}
	point, visible := Project(m, Vec3{100, 200, 64}, 1920, 1080)
	if !visible || math.Abs(float64(point.X-960)) > 0.01 || math.Abs(float64(point.Y-540)) > 0.01 {
		t.Fatalf("B's forward direction should be centered, got %+v", point)
	}
	s.viewKey = ""
	a, _, _ := s.cameraForFrame(f, 16.0/9)
	other, _ := Project(a, Vec3{100, 200, 64}, 1920, 1080)
	if math.Abs(float64(other.X-point.X)) < 1 {
		t.Fatal("camera still used A's viewpoint")
	}
	s.viewKey = playerViewKey(&f.Players[1])
	f.Players[1].Index = 8
	_, player, available = s.cameraForFrame(f, 16.0/9)
	if !available || player.Index != 8 {
		t.Fatal("selection did not survive a slot change")
	}
	f.Players = f.Players[:1]
	if _, _, ok := s.cameraForFrame(f, 16.0/9); ok {
		t.Fatal("missing B silently became A")
	}
}

type countingCanvas struct{ rectangles, lines, texts int }

func (c *countingCanvas) Begin() (float32, float32)                           { return 1920, 1080 }
func (c *countingCanvas) Clear()                                              {}
func (c *countingCanvas) Rect(float32, float32, float32, float32, color.RGBA) { c.rectangles++ }
func (c *countingCanvas) Line(float32, float32, float32, float32, color.RGBA) { c.lines++ }
func (c *countingCanvas) Text(float32, float32, string, color.RGBA)           { c.texts++ }
func (c *countingCanvas) Present()                                            {}
func (c *countingCanvas) Opaque() bool                                        { return false }

func TestDisplaySwitches(t *testing.T) {
	p := &Player{Health: 100, Name: "B", BoneMask: (1 << 1) | (1 << 6) | (1 << 7),
		Bones: []Vec3{{100, -10, 0}, {100, 0, 10}, {100, 10, 20}}}
	m := BuildViewMatrix(Vec3{}, Vec2{}, 90, 16.0/9, fovSourceHorPlus)
	s := newScene(&Relay{}, options{})
	c := &countingCanvas{}
	s.drawPlayer(c, m, 1920, 1080, p, false)
	if c.rectangles != 6 || c.lines == 0 || c.texts == 0 {
		t.Fatalf("unexpected default drawing %+v", c)
	}
	s.display = displaySettings{}
	c = &countingCanvas{}
	s.drawPlayer(c, m, 1920, 1080, p, false)
	if *c != (countingCanvas{}) {
		t.Fatalf("disabled controls still drew %+v", c)
	}
	s.display.Health = true
	s.drawPlayer(c, m, 1920, 1080, p, false)
	if c.rectangles != 2 || c.lines != 0 || c.texts != 0 {
		t.Fatalf("health-only drew %+v", c)
	}
}

func TestTeammatesUseSelectedViewpoint(t *testing.T) {
	f := &Frame{}
	for i, team := range []uint8{2, 3, 3, 2} {
		f.Players = append(f.Players, Player{Index: int8(i), SteamID: uint64(i + 1), Team: team,
			Flags: playerAlive, Health: 100, EyePos: Vec3{100, 100, 64}, EyeAngles: Vec2{0, 90}, Fov: 90,
			BoneMask: (1 << 6) | (1 << 7), Bones: []Vec3{{100, 200, 64}, {101, 200, 80}}})
	}
	f.Camera.HostIndex = 0
	r := &Relay{latest: f, latestAt: time.Now(), status: RelayStatus{Connected: true, HostOnline: true}}
	s := newScene(r, options{})
	s.viewKey = playerViewKey(&f.Players[1])
	s.display = displaySettings{Boxes: true}
	c := &countingCanvas{}
	s.draw(c)
	if c.rectangles != 8 {
		t.Fatalf("expected A and D, B's two opponents, got %d rectangles", c.rectangles)
	}
}
