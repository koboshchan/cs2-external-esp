package main

import "testing"

func TestGeForceExecutable(t *testing.T) {
	for _, path := range []string{`C:\Program Files\NVIDIA Corporation\GeForceNOW\CEF\GeForceNOW.exe`, "GEFORCENOW.EXE", `D:\GeForceNOWContainer.exe`} {
		if !isGeForceExecutable(path) {
			t.Fatalf("expected native client match for %q", path)
		}
	}
	for _, path := range []string{"chrome.exe", "GeForceNOW Helper.exe", "NotGeForceNOW.exe", "", "GeForceNOW.exe.backup"} {
		if isGeForceExecutable(path) {
			t.Fatalf("unexpected native client match for %q", path)
		}
	}
}

func TestChooseStreamWindow(t *testing.T) {
	cases := []struct {
		name    string
		windows []streamWindow
		want    uintptr
	}{
		{"absent", nil, 0},
		{"small helper", []streamWindow{{Handle: 1, Width: 100, Height: 100, Visible: true}}, 0},
		{"other desktop", []streamWindow{{Handle: 2, Width: 1920, Height: 1080}}, 2},
		{"visible priority", []streamWindow{{Handle: 2, Width: 1920, Height: 1080}, {Handle: 3, Width: 800, Height: 450, Visible: true}}, 3},
		{"largest visible", []streamWindow{{Handle: 2, Width: 800, Height: 450, Visible: true}, {Handle: 3, Width: 1920, Height: 1080, Visible: true}}, 3},
		{"foreground priority", []streamWindow{{Handle: 2, Width: 1920, Height: 1080, Visible: true}, {Handle: 3, Width: 800, Height: 450, Visible: true, Active: true}}, 3},
		{"hidden foreground", []streamWindow{{Handle: 2, Width: 1920, Height: 1080, Active: true}, {Handle: 3, Width: 800, Height: 450, Visible: true}}, 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := chooseStreamWindow(c.windows); got != c.want {
				t.Fatalf("got %d, want %d", got, c.want)
			}
		})
	}
}

func TestDecodeViewerAppearance(t *testing.T) {
	a := decodeViewerAppearance([]byte(`{"overlay_opacity":0,"panel_opacity":5,"colors":["12abef","invalid"]}`))
	if a.OverlayOpacity != 0 || a.PanelOpacity != 40 || a.Colors[0] != "#12ABEF" || len(a.Colors) != 5 {
		t.Fatalf("unexpected decoded appearance %+v", a)
	}
	if a.Colors[1] != defaultViewerAppearance().Colors[1] {
		t.Fatal("invalid color failed to restore default")
	}
	for _, data := range []string{"invalid", "{}", "null"} {
		loaded := decodeViewerAppearance([]byte(data))
		if loaded.OverlayOpacity != 100 || loaded.PanelOpacity != 100 || len(loaded.Colors) != 5 {
			t.Fatalf("missing or invalid settings should retain defaults: %+v", loaded)
		}
	}
	if a.palette().T.R != 0x12 || a.palette().T.G != 0xab || a.palette().T.B != 0xef {
		t.Fatal("RGB values failed to reach palette")
	}
}
