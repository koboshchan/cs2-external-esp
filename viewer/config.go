package main

// Persisted viewer settings, so the connect screen is filled in next launch.

import (
	"encoding/json"
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

	_ = json.Unmarshal(raw, &c)
	return c
}

func saveConfig(c viewerConfig) {
	path := configPath()
	if path == "" {
		return
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}

	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return
	}

	_ = os.WriteFile(path, raw, 0o600)
}
