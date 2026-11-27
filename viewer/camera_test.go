package main

import (
	"math"
	"testing"
)

func near(t *testing.T, got, want, eps float32, what string) {
	t.Helper()
	if float32(math.Abs(float64(got-want))) > eps {
		t.Fatalf("%s: got %v want %v", what, got, want)
	}
}

func TestProjectionCentre(t *testing.T) {
	m := BuildViewMatrix(Vec3{0, 0, 0}, Vec2{0, 0}, 90, 4.0/3.0, fovHorizontal)

	sp, ok := Project(m, Vec3{100, 0, 0}, 1920, 1080)
	if !ok {
		t.Fatal("point in front rejected")
	}
	near(t, sp.X, 960, 0.01, "centre x")
	near(t, sp.Y, 540, 0.01, "centre y")
}

func TestProjectionEdges(t *testing.T) {
	m := BuildViewMatrix(Vec3{0, 0, 0}, Vec2{0, 0}, 90, 4.0/3.0, fovHorizontal)

	// 45 degrees to the right with a 90 degree horizontal FOV.
	d := float32(70.710678)
	sp, ok := Project(m, Vec3{d, -d, 0}, 1920, 1080)
	if !ok {
		t.Fatal("45deg point rejected")
	}
	near(t, sp.X, 1920, 0.5, "45deg right edge")

	// Half the vertical FOV up: tan(36.87) = 0.75.
	sp, ok = Project(m, Vec3{80, 0, 60}, 1920, 1080)
	if !ok {
		t.Fatal("vfov point rejected")
	}
	near(t, sp.Y, 0, 0.5, "top edge")
}

func TestHorPlusWidensAt169(t *testing.T) {
	m := BuildViewMatrix(Vec3{0, 0, 0}, Vec2{0, 0}, 90, 16.0/9.0, fovSourceHorPlus)

	near(t, MatrixXScale(m), 0.75, 1e-5, "hor+ x scale")
	near(t, MatrixYScale(m), 0.75*16.0/9.0, 1e-5, "y scale")

	a := float32(53.130102) * float32(math.Pi/180)
	sp, ok := Project(m, Vec3{float32(math.Cos(float64(a))) * 100, -float32(math.Sin(float64(a))) * 100, 0}, 2560, 1440)
	if !ok {
		t.Fatal("hor+ edge rejected")
	}
	near(t, sp.X, 2560, 0.5, "hor+ edge")
}

func TestTranslation(t *testing.T) {
	eye := Vec3{100, 200, 50}
	m := BuildViewMatrix(eye, Vec2{0, 0}, 90, 16.0/9.0, fovSourceHorPlus)

	sp, ok := Project(m, Vec3{eye.X + 300, eye.Y, eye.Z}, 1920, 1080)
	if !ok {
		t.Fatal("eye-relative point rejected")
	}
	near(t, sp.X, 960, 0.01, "eye-relative x")
	near(t, sp.Y, 540, 0.01, "eye-relative y")
}

func TestHandComputedMatrix(t *testing.T) {
	m := BuildViewMatrix(Vec3{10, 20, 30}, Vec2{0, 90}, 90, 1, fovHorizontal)

	near(t, m[0], 1, 1e-6, "m00")
	near(t, m[3], -10, 1e-6, "m03")
	near(t, m[6], 1, 1e-6, "m22")
	near(t, m[7], -30, 1e-6, "m23")
	near(t, m[13], 1, 1e-6, "m31")
	near(t, m[15], -20, 1e-6, "m33")

	sp, ok := Project(m, Vec3{10, 120, 30}, 800, 600)
	if !ok {
		t.Fatal("hand case rejected")
	}
	near(t, sp.X, 400, 0.01, "hand case x")
	near(t, sp.Y, 300, 0.01, "hand case y")
}

func TestDetectFovMode(t *testing.T) {
	aspects := []float32{16.0 / 9.0, 16.0 / 10.0, 4.0 / 3.0, 21.0 / 9.0}

	for _, aspect := range aspects {
		for _, mode := range []uint8{fovSourceHorPlus, fovHorizontal} {
			m := BuildViewMatrix(Vec3{}, Vec2{}, 90, aspect, mode)
			d := DetectFovMode(90, aspect, m)

			if aspect == 4.0/3.0 {
				if d.Mode != fovSourceHorPlus {
					t.Fatalf("4:3 should be ambiguous and default to hor+")
				}
				continue
			}
			if d.Mode != mode {
				t.Fatalf("aspect %v mode %d detected as %d", aspect, mode, d.Mode)
			}
		}
	}
}

func TestBehindCameraRejected(t *testing.T) {
	m := BuildViewMatrix(Vec3{}, Vec2{}, 90, 16.0/9.0, fovSourceHorPlus)
	if _, ok := Project(m, Vec3{-50, 0, 0}, 1920, 1080); ok {
		t.Fatal("point behind camera accepted")
	}
}

// TestCameraMatchesCPP locks the Go implementation to the values the C++
// tests assert, so the two renderers cannot drift.
func TestCameraMatchesCPP(t *testing.T) {
	m := BuildViewMatrix(Vec3{0, 0, 0}, Vec2{0, 0}, 90, 4.0/3.0, fovHorizontal)

	// C++ test: 45 degrees right lands on the right edge, half vfov on top.
	cases := []struct {
		p    Vec3
		x, y float32
	}{
		{Vec3{70.710678, -70.710678, 0}, 1920, 540},
		{Vec3{80, 0, 60}, 960, 0},
	}
	for _, c := range cases {
		sp, ok := Project(m, c.p, 1920, 1080)
		if !ok {
			t.Fatalf("project %v failed", c.p)
		}
		near(t, sp.X, c.x, 0.5, "cpp parity x")
		near(t, sp.Y, c.y, 0.5, "cpp parity y")
	}
}
