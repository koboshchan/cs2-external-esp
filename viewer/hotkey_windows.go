//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	hkmSetHotkey       = 0x0401
	hkmGetHotkey       = 0x0402
	masterHotkeyFirst  = 5
	masterHotkeySecond = 6
)

var pGetFocus = user32.NewProc("GetFocus")

// The hotkey control stores Shift, Control and Alt in bits 8, 9 and 10.
// Its extended-key flag is used for display and has no RegisterHotKey modifier.
type overlayHotkeySetting struct {
	Key       uint32 `json:"key"`
	Modifiers uint32 `json:"modifiers"`
}

func (h overlayHotkeySetting) valid() bool {
	if h.Key < 0x20 || h.Key > 0xfe || h.Modifiers & ^uint32(7) != 0 {
		return false
	}
	if h.Modifiers == 0 && (h.Key == vkF8 || h.Key == vkF9 || h.Key == vkEnd) {
		return false
	}
	return h.Key != 'P' || h.Modifiers != modControl
}

func hotkeySettingsPath() string {
	path := configPath()
	if path == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(path), "hotkeys.json")
}

func (c *controlsWindow) loadOverlayHotkey() {
	hotkey := overlayHotkeySetting{Key: 0x75} // F6
	data, _ := os.ReadFile(hotkeySettingsPath())
	var saved overlayHotkeySetting
	if json.Unmarshal(data, &saved) == nil && saved.valid() {
		hotkey = saved
	}
	c.hotkey = hotkey
	flags := (hotkey.Modifiers&4)>>2 | (hotkey.Modifiers & 2) | (hotkey.Modifiers&1)<<2
	// Arrow/navigation keys need HOTKEYF_EXT for the native control's label.
	if hotkey.Key >= 0x21 && hotkey.Key <= 0x2e {
		flags |= 8
	}
	pSendMessageW.Call(c.hotkeyField, hkmSetHotkey, uintptr(hotkey.Key|flags<<8), 0)
	if err := c.registerOverlayHotkey(hotkey); err != nil {
		setNativeText(c.status, "总开关键注册失败，请重新绑定。"+err.Error())
	}
}

func (c *controlsWindow) registerOverlayHotkey(hotkey overlayHotkeySetting) error {
	if c.hotkeyID != 0 && c.hotkey == hotkey {
		return nil
	}
	nextID := uintptr(masterHotkeyFirst)
	if c.hotkeyID == nextID {
		nextID = masterHotkeySecond
	}
	ok, _, err := pRegisterHotKey.Call(c.hwnd, nextID, uintptr(hotkey.Modifiers)|modNoRepeat, uintptr(hotkey.Key))
	if ok == 0 {
		return fmt.Errorf("%v", err)
	}
	if c.hotkeyID != 0 {
		pUnregisterHotKey.Call(c.hwnd, c.hotkeyID)
	}
	c.hotkeyID, c.hotkey = nextID, hotkey
	return nil
}

func (c *controlsWindow) applyOverlayHotkey() {
	value, _, _ := pSendMessageW.Call(c.hotkeyField, hkmGetHotkey, 0, 0)
	flags := uint32(value >> 8)
	hotkey := overlayHotkeySetting{Key: uint32(value & 0xff),
		Modifiers: (flags&1)<<2 | (flags & 2) | (flags&4)>>2}
	if !hotkey.valid() {
		setNativeText(c.status, "请选择其他快捷键。F8、F9、End 和 Ctrl+P 已用于查看端。")
		return
	}
	if err := c.registerOverlayHotkey(hotkey); err != nil {
		setNativeText(c.status, "该快捷键被占用或不可用，请重新绑定。")
		return
	}
	if err := saveJSONFile(hotkeySettingsPath(), hotkey); err != nil {
		setNativeText(c.status, "按键已生效，保存失败。"+err.Error())
		return
	}
	setNativeText(c.status, "快捷键已保存，按一下切换全部映射。")
	pSetFocus.Call(c.viewList)
}
