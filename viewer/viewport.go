package main

import "math"

// fitViewport returns a centered aspect-fit rectangle in logical screen points.
func fitViewport(x, y, width, height, aspect float64) (float64, float64, float64, float64) {
	if width <= 0 || height <= 0 || aspect <= 0 || math.IsNaN(aspect) || math.IsInf(aspect, 0) {
		return x, y, width, height
	}
	if width/height > aspect {
		fitted := height * aspect
		return x + (width-fitted)/2, y, fitted, height
	}
	fitted := width / aspect
	return x, y + (height-fitted)/2, width, fitted
}

func streamAspect(mode int, frame *Frame) float64 {
	switch mode {
	case 1:
		return 16.0 / 9
	case 2:
		return 16.0 / 10
	case 3:
		return 21.0 / 9
	case 4:
		return 4.0 / 3
	case 5:
		return 0
	}
	if frame != nil {
		x := float64(MatrixXScale(frame.Camera.ViewMatrix))
		y := float64(MatrixYScale(frame.Camera.ViewMatrix))
		aspect := y / x
		if aspect >= 0.5 && aspect <= 5 && !math.IsInf(aspect, 0) {
			return aspect
		}
	}
	return 16.0 / 9
}

// Liang–Barsky clipping bounds the software rasterizer's work to the viewport
// even when a projected bone lies far outside the screen.
func clipViewportLine(x1, y1, x2, y2, width, height float32) (Vec2, Vec2, bool) {
	if width < 1 || height < 1 || !finite(x1) || !finite(y1) || !finite(x2) || !finite(y2) {
		return Vec2{}, Vec2{}, false
	}
	x, y := float64(x1), float64(y1)
	dx, dy := float64(x2)-x, float64(y2)-y
	start, end := float64(0), float64(1)
	for _, edge := range [4][2]float64{
		{-dx, x}, {dx, float64(width-1) - x}, {-dy, y}, {dy, float64(height-1) - y},
	} {
		p, q := edge[0], edge[1]
		if p == 0 {
			if q < 0 {
				return Vec2{}, Vec2{}, false
			}
			continue
		}
		ratio := q / p
		if p < 0 {
			start = max(start, ratio)
		} else {
			end = min(end, ratio)
		}
		if start > end {
			return Vec2{}, Vec2{}, false
		}
	}
	a := Vec2{float32(x + start*dx), float32(y + start*dy)}
	b := Vec2{float32(x + end*dx), float32(y + end*dy)}
	a.X, a.Y = min(width-1, max(0, a.X)), min(height-1, max(0, a.Y))
	b.X, b.Y = min(width-1, max(0, b.X)), min(height-1, max(0, b.Y))
	return a, b, true
}
