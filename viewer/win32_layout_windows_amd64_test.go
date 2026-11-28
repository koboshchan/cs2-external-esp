//go:build windows && amd64

package main

import "unsafe"

// Both subtraction directions must compile, so each Win32 ABI size is exact.
var (
	_ [unsafe.Sizeof(wndClassEx{}) - 80]byte
	_ [80 - unsafe.Sizeof(wndClassEx{})]byte
	_ [unsafe.Sizeof(msg{}) - 48]byte
	_ [48 - unsafe.Sizeof(msg{})]byte
	_ [unsafe.Sizeof(windowPlacement{}) - 44]byte
	_ [44 - unsafe.Sizeof(windowPlacement{})]byte
	_ [unsafe.Sizeof(bitmapInfoHeader{}) - 40]byte
	_ [40 - unsafe.Sizeof(bitmapInfoHeader{})]byte
	_ [unsafe.Sizeof(blendFunction{}) - 4]byte
	_ [4 - unsafe.Sizeof(blendFunction{})]byte
)
