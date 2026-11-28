package main

// Persisted viewer settings, so the connect screen is filled in next launch.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type viewerConfig struct {
	Server string `json:"server,omitempty"`
	Room   string `json:"room,omitempty"`
	Key    string `json:"key,omitempty"`
	Me     string `json:"me,omitempty"`
}

func configPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "cs2-remote-viewer", "config.json")
}

func loadConfig() viewerConfig {
	var c viewerConfig

	path := configPath()
	if path == "" {
		return c
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return c
	}

	// Notepad and PowerShell like to prepend a BOM.
	raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})

	_ = json.Unmarshal(raw, &c)
	return c
}

func saveConfig(c viewerConfig) {
	_ = saveJSONFile(configPath(), c)
}

func saveJSONFile(path string, value any) error {
	if path == "" {
		return fmt.Errorf("无法确定设置保存位置")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}

	file, err := os.CreateTemp(filepath.Dir(path), ".config-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err := file.Write(raw); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
