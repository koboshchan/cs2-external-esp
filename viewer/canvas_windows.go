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

	pRegisterClassExW        = user32.NewProc("RegisterClassExW")
	pCreateWindowExW         = user32.NewProc("CreateWindowExW")
	pDefWindowProcW          = user32.NewProc("DefWindowProcW")
	pDestroyWindow           = user32.NewProc("DestroyWindow")
	pPeekMessageW            = user32.NewProc("PeekMessageW")
	pTranslateMessage        = user32.NewProc("TranslateMessage")
	pDispatchMessageW        = user32.NewProc("DispatchMessageW")
	pUpdateLayeredWindow     = user32.NewProc("UpdateLayeredWindow")
	pGetDC                   = user32.NewProc("GetDC")
	pReleaseDC               = user32.NewProc("ReleaseDC")
	pGetClientRect           = user32.NewProc("GetClientRect")
	pClientToScreen          = user32.NewProc("ClientToScreen")
	pFindWindowW             = user32.NewProc("FindWindowW")
	pSetWindowPos            = user32.NewProc("SetWindowPos")
	pShowWindow              = user32.NewProc("ShowWindow")
	pGetSystemMetrics        = user32.NewProc("GetSystemMetrics")
	pGetDesktopWindow        = user32.NewProc("GetDesktopWindow")
	pRegisterHotKey          = user32.NewProc("RegisterHotKey")
	pUnregisterHotKey        = user32.NewProc("UnregisterHotKey")
	pSetProcessDPIAware      = user32.NewProc("SetProcessDPIAware")
	pGetModuleHandleW        = kernel32.NewProc("GetModuleHandleW")

	pCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	pCreateDIBSection   = gdi32.NewProc("CreateDIBSection")
	pSelectObject       = gdi32.NewProc("SelectObject")
	pDeleteObject       = gdi32.NewProc("DeleteObject")
	pDeleteDC           = gdi32.NewProc("DeleteDC")
	pCreateFontW        = gdi32.NewProc("CreateFontW")
	pSetTextColor       = gdi32.NewProc("SetTextColor")
	pSetBkMode          = gdi32.NewProc("SetBkMode")
	pTextOutW           = gdi32.NewProc("TextOutW")
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
	swpShowWindow = 0x0040

	smCxScreen = 0
	smCyScreen = 1
	smXVirtualScreen = 76
	smYVirtualScreen = 77
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
	hwnd   uintptr
	hdcMem uintptr
	hbm    uintptr
	oldObj uintptr
	font   uintptr

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

	target  rect
	lastSet rect
	forced  *rect
	bind    uintptr
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
	pSetProcessDPIAware.Call()

	c := &overlayCanvas{}

	// Virtual screen: covers every monitor, so "full screen" and windows on a
	// secondary display both fit in the bitmap.
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

	pShowWindow.Call(hwnd, swShowNoActivate)

	screenDC, _, _ := pGetDC.Call(0)
	c.hdcMem, _, _ = pCreateCompatibleDC.Call(screenDC)
	pReleaseDC.Call(0, screenDC)
	if c.hdcMem == 0 {
		return nil, fmt.Errorf("CreateCompatibleDC failed")
	}

	var bi bitmapInfo
	bi.bmiHeader.biSize = uint32(unsafe.Sizeof(bitmapInfoHeader{}))
	bi.bmiHeader.biWidth = c.screenW
	bi.bmiHeader.biHeight = -c.screenH // top-down
	bi.bmiHeader.biPlanes = 1
	bi.bmiHeader.biBitCount = 32
	bi.bmiHeader.biCompression = biRGB

	var bits unsafe.Pointer
	hbm, _, callErr := pCreateDIBSection.Call(
		c.hdcMem,
		uintptr(unsafe.Pointer(&bi)),
		dibRGBColors,
		uintptr(unsafe.Pointer(&bits)),
		0, 0,
	)
	if hbm == 0 || bits == nil {
		return nil, fmt.Errorf("CreateDIBSection: %v", callErr)
	}

	c.hbm = hbm
	c.oldObj, _, _ = pSelectObject.Call(c.hdcMem, hbm)
	c.bits = unsafe.Slice((*byte)(bits), int(c.screenW*c.screenH*4))

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
		pSelectObject.Call(c.hdcMem, font)
	}
	pSetBkMode.Call(c.hdcMem, transparentBk)

	if o.rect != "" {
		if r, err := parseRect(o.rect); err == nil {
			c.forced = &r
		}
	}

	c.target = c.gameRect()
	c.lastSet = rect{-1, -1, -1, -1}

	return c, nil
}

func (c *overlayCanvas) Opaque() bool { return false }

func (c *overlayCanvas) Show() { pShowWindow.Call(c.hwnd, swShowNoActivate) }

func (c *overlayCanvas) Hide() { pShowWindow.Call(c.hwnd, swHide) }

func (c *overlayCanvas) Close() {
	if c.hwnd != 0 {
		pDestroyWindow.Call(c.hwnd)
		c.hwnd = 0
	}
	if c.font != 0 {
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

func (c *overlayCanvas) Begin() (float32, float32) {
	c.target = c.gameRect()

	w := c.target.right - c.target.left
	h := c.target.bottom - c.target.top
	if w <= 0 || h <= 0 {
		w, h = c.screenW, c.screenH
		c.target = rect{0, 0, w, h}
	}
	if w > c.screenW {
		w = c.screenW
	}
	if h > c.screenH {
		h = c.screenH
	}

	c.width, c.height = w, h
	c.stride = w * 4

	if n := int(w * h * 4); n > 0 && n <= len(c.bits) {
		clear(c.bits[:n])
	}

	c.dirtyMinX, c.dirtyMinY = 1<<30, 1<<30
	c.dirtyMaxX, c.dirtyMaxY = -1, -1

	return float32(w), float32(h)
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
	if s == "" {
		return
	}

	utf16, err := windows.UTF16FromString(s)
	if err != nil {
		return
	}

	pSetTextColor.Call(c.hdcMem, uintptr(colorRef(col)))
	pTextOutW.Call(c.hdcMem, uintptr(int32(x)), uintptr(int32(y)), uintptr(unsafe.Pointer(&utf16[0])), uintptr(len(utf16)-1))

	w := int32(len(utf16)-1) * 8
	c.markDirty(int32(x), int32(y), int32(x)+w, int32(y)+16)
}

func (c *overlayCanvas) Present() {
	c.fixAlpha()

	if c.target != c.lastSet {
		pSetWindowPos.Call(
			c.hwnd,
			uintptr(^uintptr(0)), // HWND_TOPMOST
			uintptr(c.target.left), uintptr(c.target.top),
			uintptr(c.width), uintptr(c.height),
			swpNoActivate|swpShowWindow,
		)
		c.lastSet = c.target
	}

	dst := point{c.target.left, c.target.top}
	src := point{0, 0}
	sz := size{c.width, c.height}
	blend := blendFunction{blendOp: acSrcOver, sourceConstantAlpha: 255, alphaFormat: acSrcAlpha}

	pUpdateLayeredWindow.Call(
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

	c.pump()
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

func (c *overlayCanvas) pump() {
	var m msg
	for {
		r, _, _ := pPeekMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0, pmRemove)
		if r == 0 {
			return
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

func (c *overlayCanvas) Bind(hwnd uintptr) {
	c.bind = hwnd
}

func (c *overlayCanvas) BoundWindow() uintptr { return c.bind }

func clientScreenRect(hwnd uintptr) (rect, bool) {
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

func runUI(ctx context.Context, o options) (*Relay, error) {
	cw, err := newConnectWindow(o)
	if err != nil {
		return nil, err
	}

	var (
		relay   *Relay
		cancel  context.CancelFunc
		canvas  *overlayCanvas
		sc      *scene
		started bool
	)

	defer func() {
		if cancel != nil {
			cancel()
		}
		if canvas != nil {
			canvas.Close()
		}
		cw.Close()
	}()

	startHost := func(server, room, key string) error {
		o.server = resolveServerURL(server)
		o.room = room
		o.key = key
		saveConfig(viewerConfig{Server: o.server, Room: o.room, Key: o.key, Me: o.me})

		if cancel != nil {
			cancel()
		}
		relay, cancel = startRelay(ctx, o)

		cw.RefreshWindows()
		cw.SetStatus("Connected. Pick your player and the window to overlay.")
		return nil
	}

	// Saved credentials connect right away; the launcher still lets you pick
	// the player and the window.
	if o.room != "" && o.key != "" {
		_ = startHost(o.server, o.room, o.key)
	}

	start := time.Now()
	lastLog := time.Now()
	lastStatus := time.Now()
	lastStatusText := ""

	for {
		if ctx.Err() != nil || overlayQuit.Load() {
			break
		}
		if o.seconds > 0 && time.Since(start) > time.Duration(o.seconds)*time.Second {
			break
		}

		cw.Pump()

		if cw.TakeConnect() {
			server, room, key := cw.Values()
			if room == "" || key == "" {
				cw.SetStatus("Room code and viewer key are required.")
			} else if err := startHost(server, room, key); err != nil {
				cw.SetStatus("Connect failed: " + err.Error())
			}
		}

		if relay != nil && !started {
			cw.RefreshPlayers(relay)
		}

		if o.autostart && !started && relay != nil {
			snap := relay.Snapshot()
			if snap.Frame != nil && len(snap.Frame.Players) > 0 {
				hwnd := findGameWindow()
				if hwnd == 0 {
					cw.RefreshWindows()
					if len(cw.windowHandles) > 0 {
						hwnd = cw.windowHandles[0]
					}
				}
				if hwnd == 0 {
					// Nothing enumerable (e.g. a service session): use the desktop.
					hwnd, _, _ = pGetDesktopWindow.Call()
				}
				if hwnd != 0 {
					index := snap.Frame.Players[0].Index
					o.meIndex = int(index)

					if canvas == nil {
						if c, err := newOverlayCanvas(o); err == nil {
							canvas = c
						}
					}
					if canvas != nil {
						canvas.Bind(hwnd)
						sc = newScene(relay, o)
						sc.selected = index
						started = true
						cw.Hide()
						canvas.Show()
					}
				}
			}
		}

		if relay != nil && !started && time.Since(lastStatus) > 500*time.Millisecond {
			lastStatus = time.Now()
			text := StatusText(relay.Snapshot(), len(cw.playerIndices))
			if text != lastStatusText {
				lastStatusText = text
				fmt.Printf("status: %s\n", text)
			}
			cw.SetStatus(text)
		}

		if o.pick >= 0 && relay != nil && !started && len(cw.playerIndices) > 0 {
			cw.PickPlayer(o.pick)
			o.pick = -1
			cw.startRequested.Store(true)
		}

		if cw.TakeStart() {
			index, hwnd, hasWindow := cw.Selection()
			if !hasWindow {
				cw.SetStatus("Pick the window the overlay should attach to.")
			} else if relay == nil {
				cw.SetStatus("Connect first.")
			} else {
				o.meIndex = int(index)

				if canvas == nil {
					c, err := newOverlayCanvas(o)
					if err != nil {
						fmt.Printf("overlay error: %v\n", err)
						cw.SetStatus("Could not create the overlay: " + err.Error())
						return relay, nil
					}
					canvas = c
				}

				canvas.Bind(hwnd)
				selectedPlayerIndex = index
				sc = newScene(relay, o)
				sc.selected = index

				started = true
				cw.Hide()
				canvas.Show()
			}
		}

		if overlayShowConnect.Swap(false) {
			started = false
			if canvas != nil {
				canvas.Hide()
			}
			cw.RefreshWindows()
			cw.Show()
		}

		// The bound window can close while we are attached.
		if started && canvas != nil && canvas.BoundWindow() != 0 {
			if ok, _, _ := pIsWindow.Call(canvas.BoundWindow()); ok == 0 {
				started = false
				canvas.Hide()
				cw.RefreshWindows()
				cw.Show()
				cw.SetStatus("The overlaid window closed.")
			}
		}

		if started && canvas != nil && !cw.Visible() {
			sc.update()
			sc.draw(canvas)
			canvas.Present()

			if time.Since(lastLog) > 2*time.Second {
				lastLog = time.Now()
				snap := relay.Snapshot()
				fmt.Printf("connected=%v host=%v frames=%d bytes=%d gaps=%d age=%s rect=%dx%d\n",
					snap.Status.Connected, snap.Status.HostOnline, snap.Frames, snap.Bytes, snap.Gaps,
					ageOf(snap.FrameAt), canvas.width, canvas.height)
			}
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
