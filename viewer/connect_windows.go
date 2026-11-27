//go:build windows

package main

// Native connect window for the overlay.
//
// The overlay is click-through and never takes focus, so it cannot host text
// inputs. This small normal window collects the room code and viewer key (and
// remembers them), then hides itself and lets the overlay take over. F9 brings
// it back.

import (
	"fmt"
	"strings"
	"sync/atomic"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	wsChild        = 0x40000000
	wsVisible      = 0x10000000
	wsBorder       = 0x00800000
	wsTabStop      = 0x00010000
	wsCaption      = 0x00C00000
	wsSysMenu      = 0x00080000
	wsMinimizeBox  = 0x00020000
	wsOverlapped   = 0x00000000

	esAutoHScroll = 0x0080

	swHide = 0
	swShow = 5

	wmCommand = 0x0111
	wmClose   = 0x0010

	bnClicked = 0

	idServer = 101
	idRoom   = 102
	idKey    = 103
	idGo     = 104
	idStatus = 105

	idPasteServer = 106
	idPasteRoom   = 107
	idPasteKey    = 108

	hotkeyShowConnect = 3

	vkF9 = 0x78
)

var (
	pGetWindowTextW = user32.NewProc("GetWindowTextW")
	pSetWindowTextW = user32.NewProc("SetWindowTextW")
	pSetFocus       = user32.NewProc("SetFocus")
	pGetStockObject = gdi32.NewProc("GetStockObject")
	pSendMessageW   = user32.NewProc("SendMessageW")

	pOpenClipboard    = user32.NewProc("OpenClipboard")
	pCloseClipboard   = user32.NewProc("CloseClipboard")
	pGetClipboardData = user32.NewProc("GetClipboardData")
	pGlobalLock       = kernel32.NewProc("GlobalLock")
	pGlobalUnlock     = kernel32.NewProc("GlobalUnlock")

	overlayShowConnect atomic.Bool
)

const (
	defaultGUIFont = 17
	wmSetFont      = 0x0030
)

type connectWindow struct {
	hwnd    uintptr
	server  uintptr
	room    uintptr
	key     uintptr
	status  uintptr

	connectRequested atomic.Bool
	visible          bool

	serverValue string
	roomValue   string
	keyValue    string
}

var gConnect *connectWindow

func connectWndProc(hwnd, message, wParam, lParam uintptr) uintptr {
	switch message {
	case wmCommand:
		id := int(wParam & 0xffff)
		code := int((wParam >> 16) & 0xffff)
		if gConnect == nil {
			return 0
		}
		if code != bnClicked {
			return 0
		}
		switch id {
		case idGo:
			gConnect.readValues()
			gConnect.connectRequested.Store(true)
		case idPasteServer:
			gConnect.setField(gConnect.server, clipboardText())
		case idPasteRoom:
			gConnect.setField(gConnect.room, clipboardText())
		case idPasteKey:
			gConnect.setField(gConnect.key, clipboardText())
		}
		return 0

	case wmHotkey:
		switch wParam {
		case hotkeyNextPlayer:
			requestNextPlayer()
		case hotkeyShowConnect:
			overlayShowConnect.Store(true)
		case hotkeyQuit:
			overlayQuit.Store(true)
		}
		return 0

	case wmClose:
		pShowWindow.Call(hwnd, swHide)
		if gConnect != nil {
			gConnect.visible = false
		}
		return 0
	}

	r, _, _ := pDefWindowProcW.Call(hwnd, message, wParam, lParam)
	return r
}

func newConnectWindow(o options) (*connectWindow, error) {
	instanceRaw, _, _ := pGetModuleHandleW.Call(0)
	instance := windows.Handle(instanceRaw)

	className, err := windows.UTF16PtrFromString("Cs2RemoteConnect")
	if err != nil {
		return nil, err
	}

	wc := wndClassEx{
		cbSize:        uint32(unsafe.Sizeof(wndClassEx{})),
		lpfnWndProc:   syscall.NewCallback(connectWndProc),
		hInstance:     instance,
		lpszClassName: className,
	}

	if r, _, callErr := pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		// Class may already be registered; that is fine.
		_ = callErr
	}

	title, _ := windows.UTF16PtrFromString("cs2 remote viewer - connect")

	hwnd, _, callErr := pCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(title)),
		wsCaption|wsSysMenu|wsMinimizeBox|wsVisible,
		120, 120, 500, 300,
		0, 0, uintptr(instance), 0,
	)
	if hwnd == 0 {
		return nil, fmt.Errorf("CreateWindowEx: %v", callErr)
	}

	cw := &connectWindow{hwnd: hwnd}
	gConnect = cw

	cw.label("Server", 16, 20)
	cw.server = cw.edit(idServer, 110, 16, 290, o.server)
	cw.button("Paste", idPasteServer, 406, 16, 62, 26)

	cw.label("Room code", 16, 58)
	cw.room = cw.edit(idRoom, 110, 54, 160, o.room)
	cw.button("Paste", idPasteRoom, 276, 54, 62, 26)

	cw.label("Viewer key", 16, 96)
	cw.key = cw.edit(idKey, 110, 92, 290, o.key)
	cw.button("Paste", idPasteKey, 406, 92, 62, 26)

	cw.status = cw.text(idStatus, 110, 168, 360, "Enter the room code and viewer key from the host.")

	cw.button("Connect", idGo, 110, 128, 130, 30)

	pRegisterHotKey.Call(hwnd, hotkeyNextPlayer, 0, vkF8)
	pRegisterHotKey.Call(hwnd, hotkeyShowConnect, 0, vkF9)
	pRegisterHotKey.Call(hwnd, hotkeyQuit, 0, vkEnd)

	cw.visible = true
	pSetFocus.Call(cw.room)

	return cw, nil
}

func (c *connectWindow) button(text string, id int, x, y, w, h int32) uintptr {
	cls, _ := windows.UTF16PtrFromString("BUTTON")
	val, _ := windows.UTF16FromString(text)

	handle, _, _ := pCreateWindowExW.Call(0,
		uintptr(unsafe.Pointer(cls)),
		uintptr(unsafe.Pointer(&val[0])),
		wsChild|wsVisible|wsTabStop,
		uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		c.hwnd, uintptr(id), 0, 0)

	if handle != 0 {
		font, _, _ := pGetStockObject.Call(defaultGUIFont)
		pSendMessageW.Call(handle, wmSetFont, font, 1)
	}
	return handle
}

func (c *connectWindow) setField(hwnd uintptr, text string) {
	if hwnd == 0 || text == "" {
		return
	}
	ptr, err := windows.UTF16PtrFromString(text)
	if err != nil {
		return
	}
	pSetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(ptr)))
}

func (c *connectWindow) label(text string, x, y int32) {
	cls, _ := windows.UTF16PtrFromString("STATIC")
	val, _ := windows.UTF16FromString(text)
	pCreateWindowExW.Call(0,
		uintptr(unsafe.Pointer(cls)),
		uintptr(unsafe.Pointer(&val[0])),
		wsChild|wsVisible,
		uintptr(x), uintptr(y), 90, 22,
		c.hwnd, 0, 0, 0)
}

func (c *connectWindow) text(id int, x, y, w int32, text string) uintptr {
	cls, _ := windows.UTF16PtrFromString("STATIC")
	val, _ := windows.UTF16FromString(text)
	h, _, _ := pCreateWindowExW.Call(0,
		uintptr(unsafe.Pointer(cls)),
		uintptr(unsafe.Pointer(&val[0])),
		wsChild|wsVisible,
		uintptr(x), uintptr(y), uintptr(w), 60,
		c.hwnd, uintptr(id), 0, 0)
	return h
}

func (c *connectWindow) edit(id int, x, y, w int32, text string) uintptr {
	cls, _ := windows.UTF16PtrFromString("EDIT")
	val, _ := windows.UTF16FromString(text)
	h, _, _ := pCreateWindowExW.Call(0,
		uintptr(unsafe.Pointer(cls)),
		uintptr(unsafe.Pointer(&val[0])),
		wsChild|wsVisible|wsBorder|wsTabStop|esAutoHScroll,
		uintptr(x), uintptr(y), uintptr(w), 26,
		c.hwnd, uintptr(id), 0, 0)
	return h
}

func (c *connectWindow) readValues() {
	c.serverValue = readWindowText(c.server)
	c.roomValue = readWindowText(c.room)
	c.keyValue = readWindowText(c.key)
}

func readWindowText(hwnd uintptr) string {
	if hwnd == 0 {
		return ""
	}

	buf := make([]uint16, 512)
	n, _, _ := pGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return ""
	}
	return strings.TrimSpace(windows.UTF16ToString(buf[:n]))
}

func (c *connectWindow) Pump() {
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

func (c *connectWindow) TakeConnect() bool {
	return c.connectRequested.Swap(false)
}

func (c *connectWindow) Values() (server, room, key string) {
	return c.serverValue, c.roomValue, c.keyValue
}

func (c *connectWindow) SetStatus(text string) {
	ptr, err := windows.UTF16PtrFromString(text)
	if err != nil {
		return
	}
	pSetWindowTextW.Call(c.status, uintptr(unsafe.Pointer(ptr)))
}

func (c *connectWindow) Show() {
	pShowWindow.Call(c.hwnd, swShow)
	pSetFocus.Call(c.room)
	c.visible = true
}

func (c *connectWindow) Hide() {
	pShowWindow.Call(c.hwnd, swHide)
	c.visible = false
}

func (c *connectWindow) Visible() bool { return c.visible }

func clipboardText() string {
	ok, _, _ := pOpenClipboard.Call(0)
	if ok == 0 {
		return ""
	}
	defer pCloseClipboard.Call()

	const cfUnicodeText = 13

	handle, _, _ := pGetClipboardData.Call(cfUnicodeText)
	if handle == 0 {
		return ""
	}

	ptr, _, _ := pGlobalLock.Call(handle)
	if ptr == 0 {
		return ""
	}
	defer pGlobalUnlock.Call(handle)

	// The handle is locked for the duration, so the pointer stays valid.
	// UTF-16 until the terminator, capped so a huge clipboard cannot stall us.
	const maxChars = 4096
	n := 0
	for n < maxChars {
		if *(*uint16)(unsafe.Pointer(ptr + uintptr(n*2))) == 0 {
			break
		}
		n++
	}
	if n == 0 {
		return ""
	}

	runes := unsafe.Slice((*uint16)(unsafe.Pointer(ptr)), n)
	return strings.TrimSpace(windows.UTF16ToString(runes))
}

func (c *connectWindow) Close() {
	if c.hwnd == 0 {
		return
	}
	pUnregisterHotKey.Call(c.hwnd, hotkeyNextPlayer)
	pUnregisterHotKey.Call(c.hwnd, hotkeyShowConnect)
	pUnregisterHotKey.Call(c.hwnd, hotkeyQuit)
	pDestroyWindow.Call(c.hwnd)
	c.hwnd = 0
	gConnect = nil
}
