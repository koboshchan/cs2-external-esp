package main

// Camera math mirroring src/net/camera.hpp.
//
// The matrix uses the row-vector convention the game uses:
//
//	clip.x = row0 . (x, y, z, 1)
//	clip.y = row1 . (x, y, z, 1)
//	clip.w = row3 . (x, y, z, 1)
//
// Rows 0, 1 and 3 match the game's world-to-clip matrix; row 2 is depth and is
// not used for the ESP.

import "math"

func degToRad(deg float32) float32 {
	return deg * 0.017453292519943295
}

// XScaleFromFov converts a horizontal FOV into the projection scale.
//
// fovSourceHorPlus: the game keeps a 4:3 horizontal FOV and widens it on
// wider screens (Source behaviour).
// fovHorizontal: the FOV already refers to the current aspect ratio.
func XScaleFromFov(fov, aspect float32, mode uint8) float32 {
	tanHalf := float32(math.Tan(float64(degToRad(fov)) * 0.5))
	if tanHalf <= 0 {
		return 0
	}
	if mode == fovHorizontal {
		return 1 / tanHalf
	}
	if aspect <= 0 {
		return 0
	}
	return (4.0 / 3.0) / (aspect * tanHalf)
}

func MatrixXScale(m [16]float32) float32 {
	return float32(math.Sqrt(float64(m[0]*m[0] + m[1]*m[1] + m[2]*m[2])))
}

func MatrixYScale(m [16]float32) float32 {
	return float32(math.Sqrt(float64(m[4]*m[4] + m[5]*m[5] + m[6]*m[6])))
}

// BuildViewMatrix builds the world-to-clip matrix for a camera.
func BuildViewMatrix(eye Vec3, angles Vec2, fov, aspect float32, mode uint8) [16]float32 {
	pitch := degToRad(angles.X)
	yaw := degToRad(angles.Y)

	cp, sp := float32(math.Cos(float64(pitch))), float32(math.Sin(float64(pitch)))
	cy, sy := float32(math.Cos(float64(yaw))), float32(math.Sin(float64(yaw)))

	// Source engine basis: +X forward, +Y left, +Z up.
	fwd := Vec3{cp * cy, cp * sy, -sp}
	right := Vec3{sy, -cy, 0}
	up := Vec3{sp * cy, sp * sy, cp}

	xs := XScaleFromFov(fov, aspect, mode)
	ys := xs * aspect
	if aspect <= 0 {
		ys = xs
	}

	dr := right.X*eye.X + right.Y*eye.Y + right.Z*eye.Z
	du := up.X*eye.X + up.Y*eye.Y + up.Z*eye.Z
	df := fwd.X*eye.X + fwd.Y*eye.Y + fwd.Z*eye.Z

	var m [16]float32

	// row 0: x
	m[0], m[1], m[2], m[3] = xs*right.X, xs*right.Y, xs*right.Z, -xs*dr
	// row 1: y
	m[4], m[5], m[6], m[7] = ys*up.X, ys*up.Y, ys*up.Z, -ys*du
	// row 2: depth (D3D style, near 1, far 10000); unused by the ESP
	const near, far = 1.0, 10000.0
	a := float32(far / (far - near))
	b := float32(-(far * near) / (far - near))
	m[8], m[9], m[10], m[11] = a*fwd.X, a*fwd.Y, a*fwd.Z, a*(-df)+b
	// row 3: w
	m[12], m[13], m[14], m[15] = fwd.X, fwd.Y, fwd.Z, -df

	return m
}

type FovDetection struct {
	Mode           uint8
	ErrorHorPlus   float32
	ErrorHorizontal float32
}

// DetectFovMode picks the FOV interpretation that reproduces a real matrix.
func DetectFovMode(fov, aspect float32, m [16]float32) FovDetection {
	var d FovDetection
	measured := MatrixXScale(m)
	if measured <= 0 || fov <= 0 || aspect <= 0 {
		return d
	}

	a := XScaleFromFov(fov, aspect, fovSourceHorPlus)
	b := XScaleFromFov(fov, aspect, fovHorizontal)

	d.ErrorHorPlus = float32(math.Abs(float64(a-measured))) / measured
	d.ErrorHorizontal = float32(math.Abs(float64(b-measured))) / measured
	if d.ErrorHorPlus <= d.ErrorHorizontal {
		d.Mode = fovSourceHorPlus
	} else {
		d.Mode = fovHorizontal
	}
	return d
}

// Project maps a world position to screen pixels. Mirrors view_matrix_t::wts.
func Project(m [16]float32, p Vec3, width, height float32) (Vec2, bool) {
	w := m[12]*p.X + m[13]*p.Y + m[14]*p.Z + m[15]
	if w <= 0.01 {
		return Vec2{}, false
	}

	cx := m[0]*p.X + m[1]*p.Y + m[2]*p.Z + m[3]
	cy := m[4]*p.X + m[5]*p.Y + m[6]*p.Z + m[7]

	halfW := width * 0.5
	halfH := height * 0.5

	return Vec2{
		X: halfW + (cx/w)*halfW,
		Y: halfH - (cy/w)*halfH,
	}, true
}
