//go:build windows

package main

// Launcher window for the overlay.
//
// Step 1: server, room code, viewer key (native edit boxes with Paste).
// Step 2: connect, then pick which player you are from the live list.
// Step 3: pick the window the overlay should bind to.
// Step 4: start the overlay, which then follows that window.
//
// The overlay itself is click-through and never takes focus, which is why this
// window exists.

import (
	"fmt"
	"strings"
	"sync/atomic"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	wsChild       = 0x40000000
	wsVisible     = 0x10000000
	wsBorder      = 0x00800000
	wsTabStop     = 0x00010000
	wsCaption     = 0x00C00000
	wsSysMenu     = 0x00080000
	wsMinimizeBox = 0x00020000
	wsVScroll     = 0x00200000

	esAutoHScroll = 0x0080

	lbsNotify = 0x0001

	lbAddString    = 0x0180
	lbResetContent = 0x0184
	lbSetCurSel    = 0x0186
	lbGetCurSel    = 0x0188

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

	idPlayerList = 110
	idWindowList = 111
	idRefresh    = 112
	idStart      = 113

	hotkeyShowConnect = 3

	vkF9 = 0x78
)

var (
	pGetWindowTextW      = user32.NewProc("GetWindowTextW")
	pGetWindowTextLengthW = user32.NewProc("GetWindowTextLengthW")
	pSetWindowTextW      = user32.NewProc("SetWindowTextW")
	pSetFocus            = user32.NewProc("SetFocus")
	pGetStockObject      = gdi32.NewProc("GetStockObject")
	pSendMessageW        = user32.NewProc("SendMessageW")

	pOpenClipboard    = user32.NewProc("OpenClipboard")
	pCloseClipboard   = user32.NewProc("CloseClipboard")
	pGetClipboardData = user32.NewProc("GetClipboardData")
	pGlobalLock       = kernel32.NewProc("GlobalLock")
	pGlobalUnlock     = kernel32.NewProc("GlobalUnlock")

	pEnumWindows       = user32.NewProc("EnumWindows")
	pIsWindowVisible   = user32.NewProc("IsWindowVisible")
	pIsWindow          = user32.NewProc("IsWindow")
	pIsIconic          = user32.NewProc("IsIconic")
	pGetClassNameW     = user32.NewProc("GetClassNameW")

	overlayShowConnect atomic.Bool
)

const (
	defaultGUIFont = 17
	wmSetFont      = 0x0030
)

type windowEntry struct {
	hwnd  uintptr
	title string
}

type connectWindow struct {
	hwnd   uintptr
	server uintptr
	room   uintptr
	key    uintptr
	status uintptr

	playerList uintptr
	windowList uintptr

	connectRequested atomic.Bool
	startRequested   atomic.Bool
	visible          bool

	serverValue string
	roomValue   string
	keyValue    string

	playerSig     string
	playerIndices []int8
	windowHandles []uintptr
}

var (
	gConnect    *connectWindow
	enumWindows []windowEntry
)

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
		case idRefresh:
			gConnect.RefreshWindows()
		case idStart:
			gConnect.startRequested.Store(true)
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
	pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))

	title, _ := windows.UTF16PtrFromString("cs2 remote viewer")

	hwnd, _, callErr := pCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(title)),
		wsCaption|wsSysMenu|wsMinimizeBox|wsVisible,
		120, 80, 580, 620,
		0, 0, uintptr(instance), 0,
	)
	if hwnd == 0 {
		return nil, fmt.Errorf("CreateWindowEx: %v", callErr)
	}

	cw := &connectWindow{hwnd: hwnd}
	gConnect = cw

	cw.label("Server", 16, 20)
	cw.server = cw.edit(idServer, 110, 16, 300, o.server)
	cw.button("Paste", idPasteServer, 416, 16, 56, 26)

	cw.label("Room code", 16, 58)
	cw.room = cw.edit(idRoom, 110, 54, 160, o.room)
	cw.button("Paste", idPasteRoom, 276, 54, 56, 26)

	cw.label("Viewer key", 16, 96)
	cw.key = cw.edit(idKey, 110, 92, 300, o.key)
	cw.button("Paste", idPasteKey, 416, 92, 56, 26)

	cw.button("Connect", idGo, 110, 130, 120, 30)
	cw.status = cw.text(idStatus, 244, 136, 300, "Paste the room code and viewer key from the host.")

	cw.label("You are", 16, 180)
	cw.playerList = cw.listBox(idPlayerList, 16, 200, 520, 140)

	cw.label("Overlay window", 16, 352)
	cw.windowList = cw.listBox(idWindowList, 16, 372, 440, 140)
	cw.button("Refresh", idRefresh, 466, 372, 70, 26)

	cw.button("Start overlay", idStart, 16, 524, 160, 32)

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

func (c *connectWindow) label(text string, x, y int32) {
	cls, _ := windows.UTF16PtrFromString("STATIC")
	val, _ := windows.UTF16FromString(text)
	pCreateWindowExW.Call(0,
		uintptr(unsafe.Pointer(cls)),
		uintptr(unsafe.Pointer(&val[0])),
		wsChild|wsVisible,
		uintptr(x), uintptr(y), 120, 22,
		c.hwnd, 0, 0, 0)
}

func (c *connectWindow) text(id int, x, y, w int32, text string) uintptr {
	cls, _ := windows.UTF16PtrFromString("STATIC")
	val, _ := windows.UTF16FromString(text)
	h, _, _ := pCreateWindowExW.Call(0,
		uintptr(unsafe.Pointer(cls)),
		uintptr(unsafe.Pointer(&val[0])),
		wsChild|wsVisible,
		uintptr(x), uintptr(y), uintptr(w), 40,
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

func (c *connectWindow) listBox(id int, x, y, w, h int32) uintptr {
	cls, _ := windows.UTF16PtrFromString("LISTBOX")
	handle, _, _ := pCreateWindowExW.Call(0,
		uintptr(unsafe.Pointer(cls)),
		0,
		wsChild|wsVisible|wsBorder|wsVScroll|wsTabStop|lbsNotify,
		uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		c.hwnd, uintptr(id), 0, 0)

	if handle != 0 {
		font, _, _ := pGetStockObject.Call(defaultGUIFont)
		pSendMessageW.Call(handle, wmSetFont, font, 1)
	}
	return handle
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

// RefreshPlayers fills the player list from the latest snapshot.
func (c *connectWindow) RefreshPlayers(relay *Relay) {
	if relay == nil || c.playerList == 0 {
		return
	}

	snap := relay.Snapshot()
	if snap.Frame == nil {
		return
	}

	players := snap.Frame.Players

	var sig strings.Builder
	for _, p := range players {
		fmt.Fprintf(&sig, "%d:%s;", p.Index, p.Name)
	}
	if sig.String() == c.playerSig {
		return
	}
	c.playerSig = sig.String()

	selected, _, _ := pSendMessageW.Call(c.playerList, lbGetCurSel, 0, 0)

	pSendMessageW.Call(c.playerList, lbResetContent, 0, 0)
	c.playerIndices = c.playerIndices[:0]

	for _, p := range players {
		team := "T"
		if p.Team == 3 {
			team = "CT"
		}
		text := fmt.Sprintf("%d   %-2s   %-20s   %d hp", p.Index, team, p.Name, p.Health)
		ptr, err := windows.UTF16PtrFromString(text)
		if err != nil {
			continue
		}
		pSendMessageW.Call(c.playerList, lbAddString, 0, uintptr(unsafe.Pointer(ptr)))
		c.playerIndices = append(c.playerIndices, p.Index)
	}

	if int(selected) >= 0 && int(selected) < len(c.playerIndices) {
		pSendMessageW.Call(c.playerList, lbSetCurSel, selected, 0)
	} else if len(c.playerIndices) > 0 {
		pSendMessageW.Call(c.playerList, lbSetCurSel, 0, 0)
	}
}

var enumWindowsCallback = syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
	if gConnect == nil || hwnd == gConnect.hwnd {
		return 1
	}
	if visible, _, _ := pIsWindowVisible.Call(hwnd); visible == 0 {
		return 1
	}
	if minimized, _, _ := pIsIconic.Call(hwnd); minimized != 0 {
		return 1
	}
	if length, _, _ := pGetWindowTextLengthW.Call(hwnd); length == 0 {
		return 1
	}

	title := readWindowText(hwnd)
	if title == "" {
		return 1
	}

	enumWindows = append(enumWindows, windowEntry{hwnd: hwnd, title: title})
	return 1
})

// RefreshWindows lists the top-level windows the overlay can bind to.
func (c *connectWindow) RefreshWindows() {
	if c.windowList == 0 {
		return
	}

	enumWindows = enumWindows[:0]
	pEnumWindows.Call(enumWindowsCallback, 0)

	selected, _, _ := pSendMessageW.Call(c.windowList, lbGetCurSel, 0, 0)

	pSendMessageW.Call(c.windowList, lbResetContent, 0, 0)
	c.windowHandles = c.windowHandles[:0]

	for _, entry := range enumWindows {
		ptr, err := windows.UTF16PtrFromString(entry.title)
		if err != nil {
			continue
		}
		pSendMessageW.Call(c.windowList, lbAddString, 0, uintptr(unsafe.Pointer(ptr)))
		c.windowHandles = append(c.windowHandles, entry.hwnd)
	}

	if int(selected) >= 0 && int(selected) < len(c.windowHandles) {
		pSendMessageW.Call(c.windowList, lbSetCurSel, selected, 0)
	}
}

// Selection returns the chosen player index (-1 when none) and window handle.
func (c *connectWindow) Selection() (int8, uintptr) {
	player, _, _ := pSendMessageW.Call(c.playerList, lbGetCurSel, 0, 0)
	window, _, _ := pSendMessageW.Call(c.windowList, lbGetCurSel, 0, 0)

	index := int8(-1)
	if int(player) >= 0 && int(player) < len(c.playerIndices) {
		index = c.playerIndices[player]
	}

	var hwnd uintptr
	if int(window) >= 0 && int(window) < len(c.windowHandles) {
		hwnd = c.windowHandles[window]
	}

	return index, hwnd
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

func (c *connectWindow) PickPlayer(position int) {
	pSendMessageW.Call(c.playerList, lbSetCurSel, uintptr(position), 0)
}

func (c *connectWindow) TakeConnect() bool { return c.connectRequested.Swap(false) }

func (c *connectWindow) TakeStart() bool { return c.startRequested.Swap(false) }

func (c *connectWindow) Values() (server, room, key string) {
	return c.serverValue, c.roomValue, c.keyValue
}

func (c *connectWindow) SetStatus(text string) {
	ptr, err := windows.UTF16FromString(text)
	if err != nil {
		return
	}
	pSetWindowTextW.Call(c.status, uintptr(unsafe.Pointer(&ptr[0])))
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
