package main

import (
	"math"
	"testing"
)

func TestFitStreamViewport(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		width, height, aspect float64
		x, y, w, h            float64
	}{
		{"16:9 on 16:10", 1440, 900, 16.0 / 9, 0, 45, 1440, 810},
		{"4:3 on 16:9", 1920, 1080, 4.0 / 3, 240, 0, 1440, 1080},
		{"ultrawide", 1920, 1080, 21.0 / 9, 0, 128.5714285714, 1920, 822.8571428571},
		{"fill window", 1440, 900, 0, 0, 0, 1440, 900},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x, y, w, h := fitViewport(-100, 200, tc.width, tc.height, tc.aspect)
			for i, got := range []float64{x + 100, y - 200, w, h} {
				want := []float64{tc.x, tc.y, tc.w, tc.h}[i]
				if math.Abs(got-want) > 0.0001 {
					t.Fatalf("component %d got %f want %f", i, got, want)
				}
			}
		})
	}
}

func TestStreamAspectFromCamera(t *testing.T) {
	for _, aspect := range []float32{4.0 / 3, 16.0 / 9, 16.0 / 10, 3440.0 / 1440} {
		f := &Frame{}
		f.Camera.ViewMatrix = BuildViewMatrix(Vec3{}, Vec2{17, 35}, 90, aspect, fovSourceHorPlus)
		if got := streamAspect(0, f); math.Abs(got-float64(aspect)) > 0.0001 {
			t.Fatalf("got %f want %f", got, aspect)
		}
	}
	if streamAspect(0, &Frame{}) != 16.0/9 {
		t.Fatal("invalid matrix did not use the default ratio")
	}
	if streamAspect(5, nil) != 0 {
		t.Fatal("fill mode should use the complete window")
	}
}

func TestClipViewportLine(t *testing.T) {
	for _, c := range []struct {
		name           string
		x1, y1, x2, y2 float32
		visible        bool
		a, b           Vec2
	}{
		{"inside", 1, 2, 40, 20, true, Vec2{1, 2}, Vec2{40, 20}},
		{"crossing horizontal", -1000000, 20, 1000000, 20, true, Vec2{0, 20}, Vec2{99, 20}},
		{"crossing vertical", 25, -1000000, 25, 1000000, true, Vec2{25, 0}, Vec2{25, 49}},
		{"offscreen", -500, -500, -10, -10, false, Vec2{}, Vec2{}},
		{"parallel outside", -10, -1, 110, -1, false, Vec2{}, Vec2{}},
		{"point", 10, 10, 10, 10, true, Vec2{10, 10}, Vec2{10, 10}},
		{"invalid", float32(math.Inf(1)), 0, 50, 0, false, Vec2{}, Vec2{}},
	} {
		t.Run(c.name, func(t *testing.T) {
			a, b, visible := clipViewportLine(c.x1, c.y1, c.x2, c.y2, 100, 50)
			if visible != c.visible {
				t.Fatalf("visible=%v, want %v", visible, c.visible)
			}
			if visible && (a != c.a || b != c.b) {
				t.Fatalf("got %v %v, want %v %v", a, b, c.a, c.b)
			}
		})
	}
}
