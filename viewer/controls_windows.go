//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	idDisplayFirst  = 201
	idViewList      = 210
	idOverlayAlpha  = 211
	idPanelAlpha    = 212
	idColorFirst    = 220
	idApplyColors   = 230
	idResetColors   = 231
	idToggleOverlay = 232
	idApplyHotkey   = 233
	wmHScroll       = 0x0114
	tbmGetPos       = 0x0400
	tbmSetPos       = 0x0405
	tbmSetRange     = 0x0406
	bsAutoCheckBox  = 0x0003
	lbnSelChange    = 1
)

var (
	pInitCommonControlsEx       = windows.NewLazySystemDLL("comctl32.dll").NewProc("InitCommonControlsEx")
	pSetLayeredWindowAttributes = user32.NewProc("SetLayeredWindowAttributes")
	gControls                   *controlsWindow
)

type controlsWindow struct {
	hwnd, viewList, viewStatus uintptr
	displayChecks              [5]uintptr
	opacitySliders             [2]uintptr
	opacityLabels              [2]uintptr
	colorFields                [5]uintptr
	status                     uintptr
	visible                    bool
	returnWindow               uintptr
	appearance                 viewerAppearance
	viewKey                    string
	viewKeys, viewTitles       []string
	playerSignature            string
	appearanceDirty            bool
	appearanceChanged          time.Time
	desktop                    *virtualDesktopManager
	overlayToggleButton        uintptr
	overlayHidden              bool
	hotkeyField                uintptr
	hotkey                     overlayHotkeySetting
	hotkeyID                   uintptr
}

func appearancePath() string {
	path := configPath()
	if path == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(path), "appearance.json")
}

func controlsWndProc(hwnd, message, wParam, lParam uintptr) uintptr {
	c := gControls
	if c == nil {
		r, _, _ := pDefWindowProcW.Call(hwnd, message, wParam, lParam)
		return r
	}
	switch message {
	case wmHotkey:
		if wParam == c.hotkeyID && c.hotkeyID != 0 {
			focus, _, _ := pGetFocus.Call()
			if focus != c.hotkeyField {
				c.ToggleOverlay()
			}
		}
		return 0
	case wmClose:
		c.Hide()
		return 0
	case wmHScroll:
		c.updateOpacity()
		return 0
	case wmCommand:
		id, code := int(wParam&0xffff), int(wParam>>16&0xffff)
		if id == idViewList && code == lbnSelChange {
			row, _, _ := pSendMessageW.Call(c.viewList, lbGetCurSel, 0, 0)
			if row < uintptr(len(c.viewKeys)) {
				c.viewKey = c.viewKeys[row]
				setNativeText(c.viewStatus, "当前视角  "+c.viewTitles[row])
			}
			return 0
		}
		if code == bnClicked {
			switch id {
			case idApplyColors:
				c.applyColors()
			case idResetColors:
				c.appearance = defaultViewerAppearance()
				c.populateAppearance()
				c.persistAppearance()
			case idToggleOverlay:
				c.ToggleOverlay()
			case idApplyHotkey:
				c.applyOverlayHotkey()
			}
		}
		return 0
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, message, wParam, lParam)
	return r
}

func newControlsWindow() (*controlsWindow, error) {
	common := struct{ Size, Classes uint32 }{8, 0x00000004 | 0x00000040}
	if ok, _, _ := pInitCommonControlsEx.Call(uintptr(unsafe.Pointer(&common))); ok == 0 {
		return nil, fmt.Errorf("InitCommonControlsEx failed")
	}
	instance, _, _ := pGetModuleHandleW.Call(0)
	className, _ := windows.UTF16PtrFromString("Cs2RemoteDisplayControls")
	wc := wndClassEx{cbSize: uint32(unsafe.Sizeof(wndClassEx{})),
		lpfnWndProc: syscall.NewCallback(controlsWndProc), hInstance: windows.Handle(instance),
		hIcon: applicationIcon(), hIconSm: applicationIcon(),
		hbrBackground: windows.Handle(16), lpszClassName: className}
	if ok, _, err := pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); ok == 0 {
		return nil, fmt.Errorf("register controls class: %v", err)
	}
	title, _ := windows.UTF16PtrFromString("显示控制台 · Ctrl+P")
	hwnd, _, err := pCreateWindowExW.Call(wsExTopmost|wsExToolWindow|wsExLayered,
		uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(title)),
		wsCaption|wsSysMenu, 180, 80, 500, 814, 0, 0, instance, 0)
	if hwnd == 0 {
		return nil, fmt.Errorf("create controls: %v", err)
	}
	data, _ := os.ReadFile(appearancePath())
	c := &controlsWindow{hwnd: hwnd, appearance: decodeViewerAppearance(data)}
	gControls = c
	for i, title := range []string{"人物方框", "血条", "骨骼", "人物名称与状态", "队友"} {
		handle := createNativeControl(hwnd, "BUTTON", title, idDisplayFirst+i, bsAutoCheckBox|wsTabStop,
			20, int32(16+i*30), 440, 26)
		pSendMessageW.Call(handle, bmSetCheck, 1, 0)
		c.displayChecks[i] = handle
	}
	createNativeControl(hwnd, "STATIC", "骨骼世界视角", 0, 0, 20, 178, 440, 24)
	c.viewList = createNativeControl(hwnd, "LISTBOX", "", idViewList, wsBorder|wsVScroll|wsTabStop|lbsNotify, 20, 206, 440, 110)
	c.viewStatus = createNativeControl(hwnd, "STATIC", "当前视角  主机当前视角", 0, 0, 20, 324, 440, 28)
	for i, title := range []string{"映射不透明度", "面板不透明度"} {
		y := int32(365 + i*46)
		createNativeControl(hwnd, "STATIC", title, 0, 0, 20, y, 120, 24)
		slider := createNativeControl(hwnd, "msctls_trackbar32", "", idOverlayAlpha+i, wsTabStop, 144, y-4, 240, 32)
		minimum := uintptr(0)
		if i == 1 {
			minimum = 40
		}
		pSendMessageW.Call(slider, tbmSetRange, 1, minimum|100<<16)
		c.opacitySliders[i] = slider
		c.opacityLabels[i] = createNativeControl(hwnd, "STATIC", "100%", 0, 0, 395, y, 65, 24)
	}
	createNativeControl(hwnd, "STATIC", "颜色（#RRGGBB）", 0, 0, 20, 462, 440, 24)
	for i, title := range []string{"T 方框与骨骼", "CT 方框与骨骼", "正常血条", "低血量血条", "人物名称"} {
		x, y := int32(20+(i%2)*230), int32(498+(i/2)*42)
		createNativeControl(hwnd, "STATIC", title, 0, 0, x, y+4, 128, 24)
		c.colorFields[i] = createNativeControl(hwnd, "EDIT", "", idColorFirst+i, wsBorder|wsTabStop|esAutoHScroll, x+130, y, 88, 26)
	}
	createNativeControl(hwnd, "BUTTON", "应用颜色", idApplyColors, wsTabStop, 20, 634, 150, 30)
	createNativeControl(hwnd, "BUTTON", "恢复默认外观", idResetColors, wsTabStop, 185, 634, 170, 30)
	c.overlayToggleButton = createNativeControl(hwnd, "BUTTON", "隐藏映射", idToggleOverlay, wsTabStop, 370, 634, 100, 30)
	createNativeControl(hwnd, "STATIC", "映射总开关键", 0, 0, 20, 682, 128, 24)
	c.hotkeyField = createNativeControl(hwnd, "msctls_hotkey32", "", 0, wsBorder|wsTabStop, 150, 678, 190, 30)
	createNativeControl(hwnd, "BUTTON", "保存按键", idApplyHotkey, wsTabStop, 350, 678, 120, 30)
	c.status = createNativeControl(hwnd, "STATIC", "点击按键框输入快捷键，然后保存。", 0, 0, 20, 726, 440, 40)
	c.loadOverlayHotkey()
	c.populateAppearance()
	c.RefreshPlayers(nil)
	return c, nil
}

func setNativeText(hwnd uintptr, text string) {
	value, err := windows.UTF16PtrFromString(text)
	if err == nil && hwnd != 0 {
		pSetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(value)))
	}
}

func (c *controlsWindow) populateAppearance() {
	pSendMessageW.Call(c.opacitySliders[0], tbmSetPos, 1, uintptr(c.appearance.OverlayOpacity))
	pSendMessageW.Call(c.opacitySliders[1], tbmSetPos, 1, uintptr(c.appearance.PanelOpacity))
	for i, field := range c.colorFields {
		setNativeText(field, c.appearance.Colors[i])
	}
	c.applyOpacity()
}

func (c *controlsWindow) applyOpacity() {
	pSetLayeredWindowAttributes.Call(c.hwnd, 0, uintptr(c.appearance.PanelOpacity*255/100), 2)
	setNativeText(c.opacityLabels[0], strconv.Itoa(c.appearance.OverlayOpacity)+"%")
	setNativeText(c.opacityLabels[1], strconv.Itoa(c.appearance.PanelOpacity)+"%")
}

func (c *controlsWindow) persistAppearance() {
	c.appearanceDirty = true
	c.appearanceChanged = time.Now()
	setNativeText(c.status, "外观已应用，正在保存。")
}

func (c *controlsWindow) FlushAppearance(force bool) {
	if !c.appearanceDirty || (!force && time.Since(c.appearanceChanged) < 300*time.Millisecond) {
		return
	}
	c.appearanceDirty = false
	if err := saveJSONFile(appearancePath(), c.appearance); err != nil {
		setNativeText(c.status, "外观已应用，保存失败。"+err.Error())
		return
	}
	setNativeText(c.status, "外观已应用并保存。")
}

func (c *controlsWindow) updateOpacity() {
	overlay, _, _ := pSendMessageW.Call(c.opacitySliders[0], tbmGetPos, 0, 0)
	panel, _, _ := pSendMessageW.Call(c.opacitySliders[1], tbmGetPos, 0, 0)
	c.appearance.OverlayOpacity = min(100, max(0, int(overlay)))
	c.appearance.PanelOpacity = min(100, max(40, int(panel)))
	c.applyOpacity()
	c.persistAppearance()
}

func (c *controlsWindow) applyColors() {
	colors := make([]string, len(c.colorFields))
	for i, field := range c.colorFields {
		color, err := parseRGB(readWindowText(field))
		if err != nil {
			setNativeText(c.status, "颜色格式无效，请输入 #RRGGBB。")
			return
		}
		colors[i] = fmt.Sprintf("#%02X%02X%02X", color.R, color.G, color.B)
	}
	c.appearance.Colors = colors
	c.populateAppearance()
	c.persistAppearance()
}

func (c *controlsWindow) Display() displaySettings {
	var enabled [5]bool
	for i, handle := range c.displayChecks {
		value, _, _ := pSendMessageW.Call(handle, bmGetCheck, 0, 0)
		enabled[i] = value == 1
	}
	return displaySettings{Boxes: enabled[0], Health: enabled[1], Skeleton: enabled[2], Names: enabled[3], Teammates: enabled[4]}
}

func (c *controlsWindow) RefreshPlayers(frame *Frame) {
	var players []Player
	if frame != nil {
		players = frame.Players
	}
	signature := playerViewList(players)
	if signature == c.playerSignature && len(c.viewKeys) > 0 {
		return
	}
	c.playerSignature = signature
	oldTitle := "所选玩家"
	for i, key := range c.viewKeys {
		if key == c.viewKey {
			oldTitle = strings.TrimSuffix(c.viewTitles[i], " · 等待玩家返回")
		}
	}
	c.viewKeys, c.viewTitles = []string{""}, []string{"主机当前视角"}
	found := c.viewKey == ""
	for i := range players {
		p := &players[i]
		key := playerViewKey(p)
		c.viewKeys = append(c.viewKeys, key)
		c.viewTitles = append(c.viewTitles, fmt.Sprintf("#%d  %s  (team %d)", p.Index, playerLabelReplacer.Replace(p.Name), p.Team))
		found = found || key == c.viewKey
	}
	if !found {
		c.viewKeys = append(c.viewKeys, c.viewKey)
		c.viewTitles = append(c.viewTitles, oldTitle+" · 等待玩家返回")
	}
	pSendMessageW.Call(c.viewList, 0x000B, 0, 0)
	pSendMessageW.Call(c.viewList, lbResetContent, 0, 0)
	for _, title := range c.viewTitles {
		ptr, _ := windows.UTF16PtrFromString(title)
		pSendMessageW.Call(c.viewList, lbAddString, 0, uintptr(unsafe.Pointer(ptr)))
	}
	c.SelectView(c.viewKey)
	pSendMessageW.Call(c.viewList, 0x000B, 1, 0)
	pInvalidateRect.Call(c.viewList, 0, 1)
}

func (c *controlsWindow) SelectView(key string) {
	for i, entry := range c.viewKeys {
		if key == entry {
			c.viewKey = key
			pSendMessageW.Call(c.viewList, lbSetCurSel, uintptr(i), 0)
			setNativeText(c.viewStatus, "当前视角  "+c.viewTitles[i])
			return
		}
	}
}

func (c *controlsWindow) Toggle() {
	if c.visible {
		c.Hide()
		return
	}
	c.returnWindow, _, _ = pGetForegroundWindow.Call()
	c.desktop.Follow(c.returnWindow, c.hwnd)
	placeNativeWindow(c.hwnd, c.returnWindow)
	pShowWindow.Call(c.hwnd, swShow)
	pSetForegroundWindow.Call(c.hwnd)
	pSetFocus.Call(c.viewList)
	c.visible = true
}

func (c *controlsWindow) Hide() {
	pShowWindow.Call(c.hwnd, swHide)
	c.visible = false
	if c.returnWindow != 0 {
		pSetForegroundWindow.Call(c.returnWindow)
		c.returnWindow = 0
	}
}

func (c *controlsWindow) Close() {
	c.FlushAppearance(true)
	if c.hotkeyID != 0 {
		pUnregisterHotKey.Call(c.hwnd, c.hotkeyID)
	}
	pDestroyWindow.Call(c.hwnd)
	c.hwnd = 0
	gControls = nil
}

func (c *controlsWindow) ToggleOverlay() {
	c.overlayHidden = !c.overlayHidden
	title := "隐藏映射"
	if c.overlayHidden {
		title = "显示映射"
	}
	setNativeText(c.overlayToggleButton, title)
}
