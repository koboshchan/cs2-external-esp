//go:build windows

package main

// Windows overlay: a layered, click-through, always-on-top window with
// per-pixel alpha, positioned over the game's client area.
//
// Pure Go: Win32 + GDI are called through syscall, so the Windows build needs
// no cgo and no graphics library. Ebiten cannot do transparency, which is why
// this path exists.

import (
	"context"
	"fmt"
	"image/color"
	"math"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	pRegisterClassExW              = user32.NewProc("RegisterClassExW")
	pCreateWindowExW               = user32.NewProc("CreateWindowExW")
	pDefWindowProcW                = user32.NewProc("DefWindowProcW")
	pDestroyWindow                 = user32.NewProc("DestroyWindow")
	pPeekMessageW                  = user32.NewProc("PeekMessageW")
	pTranslateMessage              = user32.NewProc("TranslateMessage")
	pDispatchMessageW              = user32.NewProc("DispatchMessageW")
	pUpdateLayeredWindow           = user32.NewProc("UpdateLayeredWindow")
	pGetDC                         = user32.NewProc("GetDC")
	pReleaseDC                     = user32.NewProc("ReleaseDC")
	pGetClientRect                 = user32.NewProc("GetClientRect")
	pClientToScreen                = user32.NewProc("ClientToScreen")
	pFindWindowW                   = user32.NewProc("FindWindowW")
	pSetWindowPos                  = user32.NewProc("SetWindowPos")
	pShowWindow                    = user32.NewProc("ShowWindow")
	pGetSystemMetrics              = user32.NewProc("GetSystemMetrics")
	pRegisterHotKey                = user32.NewProc("RegisterHotKey")
	pUnregisterHotKey              = user32.NewProc("UnregisterHotKey")
	pSetProcessDPIAware            = user32.NewProc("SetProcessDPIAware")
	pSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
	pInvalidateRect                = user32.NewProc("InvalidateRect")
	pGetModuleHandleW              = kernel32.NewProc("GetModuleHandleW")

	pCreateCompatibleDC    = gdi32.NewProc("CreateCompatibleDC")
	pCreateDIBSection      = gdi32.NewProc("CreateDIBSection")
	pSelectObject          = gdi32.NewProc("SelectObject")
	pDeleteObject          = gdi32.NewProc("DeleteObject")
	pDeleteDC              = gdi32.NewProc("DeleteDC")
	pCreateFontW           = gdi32.NewProc("CreateFontW")
	pSetTextColor          = gdi32.NewProc("SetTextColor")
	pSetBkMode             = gdi32.NewProc("SetBkMode")
	pTextOutW              = gdi32.NewProc("TextOutW")
	pGetTextExtentPoint32W = gdi32.NewProc("GetTextExtentPoint32W")
	pGdiFlush              = gdi32.NewProc("GdiFlush")
)

const (
	wsExLayered     = 0x00080000
	wsExTransparent = 0x00000020
	wsExTopmost     = 0x00000008
	wsExToolWindow  = 0x00000080
	wsExNoActivate  = 0x08000000
	wsPopup         = 0x80000000

	swShowNoActivate = 4

	ulwAlpha   = 0x00000002
	acSrcOver  = 0x00
	acSrcAlpha = 0x01

	dibRGBColors = 0
	biRGB        = 0

	fwNormal          = 400
	defaultCharset    = 1
	outDefaultPrecis  = 0
	clipDefaultPrecis = 0
	antialiasedQual   = 4
	defaultPitch      = 0
	ffDontcare        = 0
	transparentBk     = 1

	wmDestroy = 0x0002
	wmHotkey  = 0x0312
	pmRemove  = 0x0001

	swpNoActivate = 0x0010

	smCxScreen        = 0
	smCyScreen        = 1
	smXVirtualScreen  = 76
	smYVirtualScreen  = 77
	smCxVirtualScreen = 78
	smCyVirtualScreen = 79

	vkF8  = 0x77
	vkEnd = 0x23

	hotkeyNextPlayer = 1
	hotkeyQuit       = 2
)

var overlayQuit atomic.Bool

type point struct{ x, y int32 }

type rect struct{ left, top, right, bottom int32 }

type size struct{ cx, cy int32 }

type blendFunction struct {
	blendOp             uint8
	blendFlags          uint8
	sourceConstantAlpha uint8
	alphaFormat         uint8
}

type bitmapInfoHeader struct {
	biSize          uint32
	biWidth         int32
	biHeight        int32
	biPlanes        uint16
	biBitCount      uint16
	biCompression   uint32
	biSizeImage     uint32
	biXPelsPerMeter int32
	biYPelsPerMeter int32
	biClrUsed       uint32
	biClrImportant  uint32
}

type bitmapInfo struct {
	bmiHeader bitmapInfoHeader
	bmiColors [1]uint32
}

type wndClassEx struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     windows.Handle
	hIcon         windows.Handle
	hCursor       windows.Handle
	hbrBackground windows.Handle
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       windows.Handle
}

type msg struct {
	hwnd     uintptr
	message  uint32
	wParam   uintptr
	lParam   uintptr
	time     uint32
	pt       point
	lPrivate uint32
}

type overlayCanvas struct {
	hwnd    uintptr
	hdcMem  uintptr
	hbm     uintptr
	oldObj  uintptr
	oldFont uintptr
	font    uintptr

	bits   []byte
	stride int32

	width   int32
	height  int32
	screenW int32
	screenH int32
	screenX int32
	screenY int32

	dirtyMinX, dirtyMinY int32
	dirtyMaxX, dirtyMaxY int32

	target          rect
	forced          *rect
	bind            uintptr
	followGeForce   bool
	aspectMode      int
	topInset        float64
	relay           *Relay
	desktop         *virtualDesktopManager
	lastWindowCheck time.Time
	ready, shown    bool
	opacity         uint8
	presentFailed   bool
}

func wndProc(hwnd, message, wParam, lParam uintptr) uintptr {
	switch message {
	case wmDestroy:
		overlayQuit.Store(true)
		return 0
	}

	r, _, _ := pDefWindowProcW.Call(hwnd, message, wParam, lParam)
	return r
}

func newOverlayCanvas(o options) (*overlayCanvas, error) {
	c := &overlayCanvas{opacity: 255}
	succeeded := false
	defer func() {
		if !succeeded {
			c.Close()
		}
	}()

	// Initial desktop bounds. The bitmap is allocated at the actual viewport
	// size, including after monitor changes and stream window replacements.
	vx, _, _ := pGetSystemMetrics.Call(smXVirtualScreen)
	vy, _, _ := pGetSystemMetrics.Call(smYVirtualScreen)
	sw, _, _ := pGetSystemMetrics.Call(smCxVirtualScreen)
	sh, _, _ := pGetSystemMetrics.Call(smCyVirtualScreen)
	c.screenX, c.screenY = int32(vx), int32(vy)
	c.screenW, c.screenH = int32(sw), int32(sh)
	if c.screenW <= 0 || c.screenH <= 0 {
		return nil, fmt.Errorf("could not read the screen size")
	}

	instanceRaw, _, _ := pGetModuleHandleW.Call(0)
	instance := windows.Handle(instanceRaw)

	className, err := windows.UTF16PtrFromString("Cs2RemoteOverlay")
	if err != nil {
		return nil, err
	}

	wc := wndClassEx{
		cbSize:        uint32(unsafe.Sizeof(wndClassEx{})),
		lpfnWndProc:   syscall.NewCallback(wndProc),
		hInstance:     instance,
		lpszClassName: className,
		hIcon:         applicationIcon(),
		hIconSm:       applicationIcon(),
	}

	if r, _, callErr := pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return nil, fmt.Errorf("RegisterClassEx: %v", callErr)
	}

	exStyle := uintptr(wsExLayered | wsExTransparent | wsExTopmost | wsExToolWindow | wsExNoActivate)

	hwnd, _, callErr := pCreateWindowExW.Call(
		exStyle,
		uintptr(unsafe.Pointer(className)),
		0,
		wsPopup,
		0, 0, uintptr(c.screenW), uintptr(c.screenH),
		0, 0, uintptr(instance), 0,
	)
	if hwnd == 0 {
		return nil, fmt.Errorf("CreateWindowEx: %v", callErr)
	}
	c.hwnd = hwnd

	screenDC, _, _ := pGetDC.Call(0)
	c.hdcMem, _, _ = pCreateCompatibleDC.Call(screenDC)
	pReleaseDC.Call(0, screenDC)
	if c.hdcMem == 0 {
		return nil, fmt.Errorf("CreateCompatibleDC failed")
	}

	fontName, _ := windows.UTF16PtrFromString("Consolas")
	font, _, _ := pCreateFontW.Call(
		uintptr(0xFFFFFFF3), // -13
		0, 0, 0,
		fwNormal, 0, 0, 0,
		defaultCharset, outDefaultPrecis, clipDefaultPrecis, antialiasedQual,
		defaultPitch|ffDontcare,
		uintptr(unsafe.Pointer(fontName)),
	)
	c.font = font
	if font != 0 {
		c.oldFont, _, _ = pSelectObject.Call(c.hdcMem, font)
	}
	pSetBkMode.Call(c.hdcMem, transparentBk)

	if o.rect != "" {
		if r, err := parseRect(o.rect); err == nil {
			c.forced = &r
		}
	}

	succeeded = true
	return c, nil
}

func (c *overlayCanvas) Opaque() bool { return false }

func (c *overlayCanvas) Show() {
	if !c.shown {
		pShowWindow.Call(c.hwnd, swShowNoActivate)
		c.shown = true
	}
}

func (c *overlayCanvas) Hide() {
	if c.shown {
		pShowWindow.Call(c.hwnd, swHide)
		c.shown = false
	}
}

func (c *overlayCanvas) Close() {
	if c.hwnd != 0 {
		pDestroyWindow.Call(c.hwnd)
		c.hwnd = 0
	}
	if c.font != 0 {
		if c.oldFont != 0 {
			pSelectObject.Call(c.hdcMem, c.oldFont)
		}
		pDeleteObject.Call(c.font)
		c.font = 0
	}
	if c.hdcMem != 0 {
		if c.oldObj != 0 {
			pSelectObject.Call(c.hdcMem, c.oldObj)
		}
		if c.hbm != 0 {
			pDeleteObject.Call(c.hbm)
		}
		pDeleteDC.Call(c.hdcMem)
		c.hdcMem = 0
	}
}

func (c *overlayCanvas) ensureSurface(w, h int32) bool {
	if w == c.width && h == c.height && c.hbm != 0 {
		return true
	}
	if w <= 0 || h <= 0 {
		return false
	}
	var bi bitmapInfo
	bi.bmiHeader.biSize = uint32(unsafe.Sizeof(bitmapInfoHeader{}))
	bi.bmiHeader.biWidth, bi.bmiHeader.biHeight = w, -h
	bi.bmiHeader.biPlanes, bi.bmiHeader.biBitCount = 1, 32
	bi.bmiHeader.biCompression = biRGB
	var bits unsafe.Pointer
	bitmap, _, _ := pCreateDIBSection.Call(c.hdcMem, uintptr(unsafe.Pointer(&bi)),
		dibRGBColors, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bitmap == 0 || bits == nil {
		if bitmap != 0 {
			pDeleteObject.Call(bitmap)
		}
		return false
	}
	previous, _, _ := pSelectObject.Call(c.hdcMem, bitmap)
	if previous == 0 || previous == ^uintptr(0) {
		pDeleteObject.Call(bitmap)
		return false
	}
	if c.oldObj == 0 {
		c.oldObj = previous
	}
	if c.hbm != 0 {
		pDeleteObject.Call(c.hbm)
	}
	c.hbm = bitmap
	c.bits = unsafe.Slice((*byte)(bits), int(w)*int(h)*4)
	c.width, c.height, c.stride = w, h, w*4
	return true
}

func (c *overlayCanvas) Begin() (float32, float32) {
	c.ready = true
	if c.followGeForce {
		if time.Since(c.lastWindowCheck) >= 250*time.Millisecond {
			c.bind = findGeForceWindow()
			c.desktop.Follow(c.bind, c.hwnd)
			c.lastWindowCheck = time.Now()
		}
		target, valid := clientScreenRect(c.bind)
		c.ready = valid && windowIsDisplayable(c.bind) && geForceForeground(c.bind)
		if c.ready {
			left, top := float64(target.left), float64(target.top)
			width, height := float64(target.right-target.left), float64(target.bottom-target.top)
			inset := min(c.topInset, max(0, height-1))
			top, height = top+inset, height-inset
			var frame *Frame
			if c.relay != nil {
				frame = c.relay.Snapshot().Frame
			}
			left, top, width, height = fitViewport(left, top, width, height, streamAspect(c.aspectMode, frame))
			c.target = rect{int32(left), int32(top), int32(left + width), int32(top + height)}
		}
	} else {
		c.target = c.gameRect()
	}
	if c.ready {
		c.ready = c.ensureSurface(c.target.right-c.target.left, c.target.bottom-c.target.top)
	}
	if !c.ready {
		c.Hide()
		return float32(max(1, c.width)), float32(max(1, c.height))
	}
	clear(c.bits)
	c.dirtyMinX, c.dirtyMinY = 1<<30, 1<<30
	c.dirtyMaxX, c.dirtyMaxY = -1, -1
	return float32(c.width), float32(c.height)
}

func (c *overlayCanvas) Clear() {
	if n := int(c.width * c.height * 4); n > 0 && n <= len(c.bits) {
		clear(c.bits[:n])
	}
}

func (c *overlayCanvas) markDirty(x0, y0, x1, y1 int32) {
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	if x1 > c.width {
		x1 = c.width
	}
	if y1 > c.height {
		y1 = c.height
	}
	if x0 >= x1 || y0 >= y1 {
		return
	}
	if x0 < c.dirtyMinX {
		c.dirtyMinX = x0
	}
	if y0 < c.dirtyMinY {
		c.dirtyMinY = y0
	}
	if x1 > c.dirtyMaxX {
		c.dirtyMaxX = x1
	}
	if y1 > c.dirtyMaxY {
		c.dirtyMaxY = y1
	}
}

func (c *overlayCanvas) Rect(x, y, w, h float32, col color.RGBA) {
	if !c.ready {
		return
	}
	if col.A == 0 || w <= 0 || h <= 0 {
		return
	}

	x0 := int32(x)
	y0 := int32(y)
	x1 := int32(x + w)
	y1 := int32(y + h)

	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	if x1 > c.width {
		x1 = c.width
	}
	if y1 > c.height {
		y1 = c.height
	}
	if x0 >= x1 || y0 >= y1 {
		return
	}

	for py := y0; py < y1; py++ {
		row := c.bits[int(py)*int(c.stride):]
		for px := x0; px < x1; px++ {
			blendPixel(row[int(px)*4:], col)
		}
	}

	c.markDirty(x0, y0, x1, y1)
}

func (c *overlayCanvas) Line(x1, y1, x2, y2 float32, col color.RGBA) {
	if !c.ready {
		return
	}
	a, b, visible := clipViewportLine(x1, y1, x2, y2, float32(c.width), float32(c.height))
	if !visible {
		return
	}
	x1, y1, x2, y2 = a.X, a.Y, b.X, b.Y
	ix1, iy1 := int32(x1), int32(y1)
	ix2, iy2 := int32(x2), int32(y2)

	dx := abs32(ix2 - ix1)
	dy := abs32(iy2 - iy1)
	sx := int32(1)
	if ix1 > ix2 {
		sx = -1
	}
	sy := int32(1)
	if iy1 > iy2 {
		sy = -1
	}
	err := dx - dy

	for {
		c.pixel(ix1, iy1, col)
		if ix1 == ix2 && iy1 == iy2 {
			break
		}
		e2 := 2 * err
		if e2 > -dy {
			err -= dy
			ix1 += sx
		}
		if e2 < dx {
			err += dx
			iy1 += sy
		}
	}

	c.markDirty(min32(int32(x1), int32(x2))-1, min32(int32(y1), int32(y2))-1,
		max32(int32(x1), int32(x2))+2, max32(int32(y1), int32(y2))+2)
}

func (c *overlayCanvas) pixel(x, y int32, col color.RGBA) {
	if x < 0 || y < 0 || x >= c.width || y >= c.height {
		return
	}
	off := int(y)*int(c.stride) + int(x)*4
	blendPixel(c.bits[off:], col)
}

func (c *overlayCanvas) Text(x, y float32, s string, col color.RGBA) {
	if !c.ready {
		return
	}
	if s == "" {
		return
	}

	utf16, err := windows.UTF16FromString(s)
	if err != nil {
		return
	}

	pSetTextColor.Call(c.hdcMem, uintptr(colorRef(col)))
	pTextOutW.Call(c.hdcMem, uintptr(int32(x)), uintptr(int32(y)), uintptr(unsafe.Pointer(&utf16[0])), uintptr(len(utf16)-1))

	extent := size{cx: int32(len(utf16)-1) * 16, cy: 24}
	pGetTextExtentPoint32W.Call(c.hdcMem, uintptr(unsafe.Pointer(&utf16[0])), uintptr(len(utf16)-1), uintptr(unsafe.Pointer(&extent)))
	c.markDirty(int32(x), int32(y), int32(x)+extent.cx, int32(y)+extent.cy)
}

func (c *overlayCanvas) Present() {
	if !c.ready {
		return
	}
	pGdiFlush.Call()
	c.fixAlpha()

	insertAfter := ^uintptr(0) // HWND_TOPMOST
	if gControls != nil && gControls.visible {
		insertAfter = gControls.hwnd
	}
	pSetWindowPos.Call(c.hwnd, insertAfter,
		uintptr(c.target.left), uintptr(c.target.top), uintptr(c.width), uintptr(c.height), swpNoActivate)

	dst := point{c.target.left, c.target.top}
	src := point{0, 0}
	sz := size{c.width, c.height}
	blend := blendFunction{blendOp: acSrcOver, sourceConstantAlpha: c.opacity, alphaFormat: acSrcAlpha}

	ok, _, err := pUpdateLayeredWindow.Call(
		c.hwnd,
		0,
		uintptr(unsafe.Pointer(&dst)),
		uintptr(unsafe.Pointer(&sz)),
		c.hdcMem,
		uintptr(unsafe.Pointer(&src)),
		0,
		uintptr(unsafe.Pointer(&blend)),
		ulwAlpha,
	)
	if ok == 0 {
		c.Hide()
		if !c.presentFailed {
			fmt.Printf("UpdateLayeredWindow failed: %v\n", err)
		}
		c.presentFailed = true
		return
	}
	c.presentFailed = false
	c.Show()
}

// fixAlpha gives GDI-drawn pixels (text) an alpha derived from their
// brightness. Everything drawn through Rect/Line already has alpha set.
func (c *overlayCanvas) fixAlpha() {
	if c.dirtyMaxX <= c.dirtyMinX || c.dirtyMaxY <= c.dirtyMinY {
		return
	}

	for y := c.dirtyMinY; y < c.dirtyMaxY; y++ {
		row := c.bits[int(y)*int(c.stride):]
		for x := c.dirtyMinX; x < c.dirtyMaxX; x++ {
			px := row[int(x)*4:]
			if px[3] != 0 {
				continue
			}
			r, g, b := px[2], px[1], px[0]
			if r == 0 && g == 0 && b == 0 {
				continue
			}
			a := r
			if g > a {
				a = g
			}
			if b > a {
				a = b
			}
			px[3] = a
		}
	}
}

func (c *overlayCanvas) Bind(hwnd uintptr) {
	c.bind = hwnd
}

func (c *overlayCanvas) BoundWindow() uintptr { return c.bind }

func clientScreenRect(hwnd uintptr) (rect, bool) {
	if hwnd == 0 {
		return rect{}, false
	}
	var cr rect
	if r, _, _ := pGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&cr))); r == 0 {
		return rect{}, false
	}

	pt := point{0, 0}
	if ok, _, _ := pClientToScreen.Call(hwnd, uintptr(unsafe.Pointer(&pt))); ok == 0 {
		return rect{}, false
	}

	w := cr.right - cr.left
	h := cr.bottom - cr.top
	if w <= 0 || h <= 0 {
		return rect{}, false
	}

	return rect{pt.x, pt.y, pt.x + w, pt.y + h}, true
}

func (c *overlayCanvas) gameRect() rect {
	if c.forced != nil {
		return *c.forced
	}

	// bind == 0 means "entire screen".
	if c.bind == 0 {
		x, _, _ := pGetSystemMetrics.Call(smXVirtualScreen)
		y, _, _ := pGetSystemMetrics.Call(smYVirtualScreen)
		w, _, _ := pGetSystemMetrics.Call(smCxVirtualScreen)
		h, _, _ := pGetSystemMetrics.Call(smCyVirtualScreen)
		c.screenX, c.screenY, c.screenW, c.screenH = int32(x), int32(y), int32(w), int32(h)
		return rect{c.screenX, c.screenY, c.screenX + c.screenW, c.screenY + c.screenH}
	}

	if c.bind != 0 {
		if ok, _, _ := pIsWindow.Call(c.bind); ok != 0 {
			if r, valid := clientScreenRect(c.bind); valid {
				return r
			}
		}
	}

	if hwnd := findGameWindow(); hwnd != 0 {
		if r, valid := clientScreenRect(hwnd); valid {
			return r
		}
	}

	return rect{c.screenX, c.screenY, c.screenX + c.screenW, c.screenY + c.screenH}
}

func findGameWindow() uintptr {
	if cls, err := windows.UTF16PtrFromString("SDL_app"); err == nil {
		if hwnd, _, _ := pFindWindowW.Call(uintptr(unsafe.Pointer(cls)), 0); hwnd != 0 {
			return hwnd
		}
	}
	if title, err := windows.UTF16PtrFromString("Counter-Strike 2"); err == nil {
		if hwnd, _, _ := pFindWindowW.Call(0, uintptr(unsafe.Pointer(title))); hwnd != 0 {
			return hwnd
		}
	}
	return 0
}

func runUI(ctx context.Context, o options) (result *Relay, runErr error) {
	defer func() {
		if runErr != nil {
			title, _ := windows.UTF16PtrFromString("CS2 Remote Viewer")
			message, _ := windows.UTF16PtrFromString(runErr.Error())
			user32.NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(message)), uintptr(unsafe.Pointer(title)), 0x10)
		}
	}()
	if pSetProcessDpiAwarenessContext.Find() == nil {
		pSetProcessDpiAwarenessContext.Call(^uintptr(3)) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2
	} else {
		pSetProcessDPIAware.Call()
	}
	cw, err := newConnectWindow(o)
	if err != nil {
		return nil, err
	}
	defer cw.Close()
	controls, err := newControlsWindow()
	if err != nil {
		return nil, err
	}
	defer controls.Close()
	desktop := newVirtualDesktopManager()
	defer desktop.Close()
	cw.desktop, controls.desktop = desktop, desktop
	controlsHint = "Ctrl+P = controls    F8 = next viewpoint    F9 = launcher    END = quit"
	var relay *Relay
	var cancel context.CancelFunc
	var overlay *overlayCanvas
	var sc *scene
	started := false
	defer func() {
		if cancel != nil {
			cancel()
		}
		if overlay != nil {
			overlay.Close()
		}
	}()

	startHost := func(server, room, key string) {
		o.server = resolveServerURL(server)
		o.room, o.key = strings.ToUpper(strings.TrimSpace(room)), strings.TrimSpace(key)
		saveConfig(viewerConfig{Server: o.server, Room: o.room, Key: o.key, Me: o.me})
		if cancel != nil {
			cancel()
		}
		started = false
		sc = nil
		if overlay != nil {
			overlay.Hide()
		}
		controls.SelectView("")
		controls.RefreshPlayers(nil)
		relay, cancel = startRelay(ctx, o)
		cw.playerSig = ""
		cw.playerIndices = nil
		pSendMessageW.Call(cw.playerList, lbResetContent, 0, 0)
		cw.RefreshWindows()
		cw.SetStatus("正在连接中继并验证房间。")
	}
	startMapping := func(index int8, hwnd uintptr) error {
		inset, err := strconv.ParseFloat(readWindowText(cw.topInsetEdit), 64)
		if err != nil || inset < 0 || math.IsNaN(inset) || math.IsInf(inset, 0) {
			return fmt.Errorf("顶部裁剪需要填写大于等于 0 的像素数")
		}
		if overlay == nil {
			overlay, err = newOverlayCanvas(o)
			if err != nil {
				return err
			}
		}
		aspect, _, _ := pSendMessageW.Call(cw.aspectCombo, cbGetCurSel, 0, 0)
		overlay.Bind(hwnd)
		overlay.followGeForce = cw.FollowGeForce()
		overlay.aspectMode, overlay.topInset = int(aspect), inset
		overlay.relay, overlay.desktop = relay, desktop
		overlay.lastWindowCheck = time.Time{}
		o.meIndex = int(index)
		selectedPlayerIndex = index
		sc = newScene(relay, o)
		sc.useHostMatrix = overlay.followGeForce
		frame := relay.Snapshot().Frame
		controls.RefreshPlayers(frame)
		viewKey := ""
		if frame != nil {
			if player := frame.PlayerByIndex(int(index)); player != nil {
				viewKey = playerViewKey(player)
			}
		}
		controls.SelectView(viewKey)
		sc.viewKey = controls.viewKey
		started = true
		cw.Hide()
		return nil
	}
	if o.room != "" && o.key != "" {
		startHost(o.server, o.room, o.key)
	}
	if cw.hotkeyWarning != "" {
		setNativeText(controls.status, cw.hotkeyWarning)
	}
	start, lastLog, lastStatus := time.Now(), time.Now(), time.Time{}
	lastRoster := time.Time{}
	for ctx.Err() == nil && !overlayQuit.Load() {
		if o.seconds > 0 && time.Since(start) > time.Duration(o.seconds)*time.Second {
			break
		}
		cw.Pump()
		controls.FlushAppearance(false)
		if cw.TakeConnect() {
			server, room, key := cw.Values()
			if room == "" || key == "" {
				cw.SetStatus("请填写房间代码和 Viewer key。")
			} else {
				startHost(server, room, key)
			}
		}
		if relay != nil {
			if !started {
				cw.RefreshPlayers(relay)
			}
			if time.Since(lastRoster) >= 250*time.Millisecond {
				controls.RefreshPlayers(relay.Snapshot().Frame)
				lastRoster = time.Now()
			}
		}
		if o.autostart && !started && relay != nil {
			snap := relay.Snapshot()
			if snap.Frame != nil && len(snap.Frame.Players) > 0 && snap.Status.Connected {
				target := uintptr(0)
				if !cw.FollowGeForce() {
					target = findGameWindow()
				}
				if err := startMapping(snap.Frame.Players[0].Index, target); err != nil {
					cw.SetStatus(err.Error())
				}
				o.autostart = false
			}
		}
		if relay != nil && !started && time.Since(lastStatus) > 500*time.Millisecond {
			lastStatus = time.Now()
			cw.SetStatus(StatusText(relay.Snapshot(), len(cw.playerIndices)))
		}
		if o.pick >= 0 && relay != nil && !started && len(cw.playerIndices) > 0 {
			cw.PickPlayer(o.pick)
			o.pick = -1
			cw.startRequested.Store(true)
		}
		if cw.TakeStart() {
			index, hwnd, hasWindow := cw.Selection()
			switch {
			case relay == nil:
				cw.SetStatus("请先连接。")
			case relay.Snapshot().Frame == nil || !relay.Snapshot().Status.Connected || len(cw.playerIndices) == 0:
				cw.SetStatus("等待连接成功和最新玩家数据。")
			case !cw.FollowGeForce() && !hasWindow:
				cw.SetStatus("请选择覆盖窗口。")
			default:
				if err := startMapping(index, hwnd); err != nil {
					cw.SetStatus(err.Error())
				}
			}
		}
		if overlayShowConnect.Swap(false) {
			started = false
			controls.Hide()
			if overlay != nil {
				overlay.Hide()
			}
			cw.RefreshWindows()
			cw.Show()
		}
		if overlayShowControls.Swap(false) {
			controls.Toggle()
		}
		if started && overlay != nil && !overlay.followGeForce && overlay.BoundWindow() != 0 {
			if valid, _, _ := pIsWindow.Call(overlay.BoundWindow()); valid == 0 {
				started = false
				overlay.Hide()
				controls.Hide()
				cw.RefreshWindows()
				cw.Show()
				cw.SetStatus("覆盖窗口已关闭。")
			}
		}
		if started && overlay != nil && sc != nil {
			if consumeNextPlayer() {
				if frame := relay.Snapshot().Frame; frame != nil {
					_, current, _ := sc.cameraForFrame(frame, 16.0/9)
					if next := frame.PlayerByIndex(int(nextPlayerIndex(frame.Players, current))); next != nil {
						controls.SelectView(playerViewKey(next))
					}
				}
			}
			sc.viewKey = controls.viewKey
			sc.display = controls.Display()
			sc.palette = controls.appearance.palette()
			overlay.opacity = uint8(controls.appearance.OverlayOpacity * 255 / 100)
			if controls.overlayHidden {
				overlay.ready = false
				overlay.Hide()
			} else {
				sc.draw(overlay)
				overlay.Present()
			}
			if time.Since(lastLog) > 2*time.Second {
				lastLog = time.Now()
				snap := relay.Snapshot()
				fmt.Printf("connected=%v host=%v frames=%d age=%s target=%v rect=%dx%d\n",
					snap.Status.Connected, snap.Status.HostOnline, snap.Frames, ageOf(snap.FrameAt), overlay.ready, overlay.width, overlay.height)
			}
		} else {
			consumeNextPlayer()
		}
		time.Sleep(16 * time.Millisecond)
	}
	return relay, nil
}

func parseRect(s string) (rect, error) {
	parts := strings.Split(s, ",")
	if len(parts) != 4 {
		return rect{}, fmt.Errorf("expected x,y,w,h")
	}

	values := make([]int32, 4)
	for i, p := range parts {
		v, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			return rect{}, err
		}
		values[i] = int32(v)
	}

	return rect{values[0], values[1], values[0] + values[2], values[1] + values[3]}, nil
}

func blendPixel(px []byte, col color.RGBA) {
	a := uint32(col.A)
	switch a {
	case 255:
		px[0] = col.B
		px[1] = col.G
		px[2] = col.R
		px[3] = 255
		return
	case 0:
		return
	}

	inv := 255 - a
	px[0] = uint8((uint32(col.B)*a + uint32(px[0])*inv) / 255)
	px[1] = uint8((uint32(col.G)*a + uint32(px[1])*inv) / 255)
	px[2] = uint8((uint32(col.R)*a + uint32(px[2])*inv) / 255)
	px[3] = uint8(a + uint32(px[3])*inv/255)
}

func colorRef(c color.RGBA) uint32 {
	return uint32(c.R) | uint32(c.G)<<8 | uint32(c.B)<<16
}

func abs32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

func min32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}

func max32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}
