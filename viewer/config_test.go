package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveJSONFileReplacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "appearance.json")
	if err := saveJSONFile(path, defaultViewerAppearance()); err != nil {
		t.Fatal(err)
	}
	a := defaultViewerAppearance()
	a.OverlayOpacity = 43
	if err := saveJSONFile(path, a); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if decodeViewerAppearance(data).OverlayOpacity != 43 {
		t.Fatal("existing settings were not replaced")
	}
	if err := saveJSONFile(path, make(chan int)); err == nil {
		t.Fatal("invalid settings should fail")
	}
	previous, err := os.ReadFile(path)
	if err != nil || string(previous) != string(data) {
		t.Fatal("failed save changed existing settings")
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 1 {
		t.Fatal("temporary settings file was left behind")
	}
}
