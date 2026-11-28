//go:build windows

package main

import "testing"

func TestOverlayToggleCommand(t *testing.T) {
	previous := gControls
	defer func() { gControls = previous }()
	controls := &controlsWindow{viewKey: "steam:20", appearance: defaultViewerAppearance()}
	gControls = controls
	for i := 0; i < 8; i++ {
		controlsWndProc(0, wmCommand, idToggleOverlay, 0)
		if controls.overlayHidden != (i%2 == 0) {
			t.Fatalf("toggle %d produced hidden=%v", i+1, controls.overlayHidden)
		}
		if controls.viewKey != "steam:20" || controls.appearance.OverlayOpacity != 100 {
			t.Fatal("overlay toggle changed viewpoint or appearance")
		}
	}
}

func TestOverlayMasterHotkeyMessage(t *testing.T) {
	previous := gControls
	defer func() { gControls = previous }()
	controls := &controlsWindow{hotkeyID: masterHotkeyFirst, hotkeyField: ^uintptr(0), viewKey: "steam:20"}
	gControls = controls
	for i := 0; i < 8; i++ {
		controlsWndProc(0, wmHotkey, masterHotkeyFirst, 0)
		if controls.overlayHidden != (i%2 == 0) || controls.viewKey != "steam:20" {
			t.Fatalf("hotkey toggle %d produced hidden=%v, view=%q", i+1, controls.overlayHidden, controls.viewKey)
		}
	}
	controlsWndProc(0, wmHotkey, masterHotkeySecond, 0)
	if controls.overlayHidden {
		t.Fatal("retired registration toggled the overlay")
	}
}

func TestOverlayHotkeyValidation(t *testing.T) {
	for _, key := range []uint32{0x75, 'K', 0x25, 0x87} {
		for modifiers := uint32(0); modifiers < 8; modifiers++ {
			if !(overlayHotkeySetting{Key: key, Modifiers: modifiers}).valid() {
				t.Fatalf("valid binding rejected: key=%d modifiers=%d", key, modifiers)
			}
		}
	}
	for _, binding := range []overlayHotkeySetting{
		{}, {Key: 0x10}, {Key: 0xff}, {Key: 'K', Modifiers: 8},
		{Key: vkF8}, {Key: vkF9}, {Key: vkEnd}, {Key: 'P', Modifiers: modControl},
	} {
		if binding.valid() {
			t.Fatalf("invalid or reserved binding accepted: %+v", binding)
		}
	}
}
