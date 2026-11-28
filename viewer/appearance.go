package main

import (
	"encoding/json"
	"fmt"
	"image/color"
	"strconv"
	"strings"
)

type viewerAppearance struct {
	OverlayOpacity int      `json:"overlay_opacity"`
	PanelOpacity   int      `json:"panel_opacity"`
	Colors         []string `json:"colors"`
}

func defaultViewerAppearance() viewerAppearance {
	p := defaultPlayerPalette()
	colors := []color.RGBA{p.T, p.CT, p.Health, p.HealthLow, p.Name}
	a := viewerAppearance{OverlayOpacity: 100, PanelOpacity: 100}
	for _, c := range colors {
		a.Colors = append(a.Colors, fmt.Sprintf("#%02X%02X%02X", c.R, c.G, c.B))
	}
	return a
}

func parseRGB(text string) (color.RGBA, error) {
	text = strings.TrimPrefix(strings.TrimSpace(text), "#")
	if len(text) != 6 {
		return color.RGBA{}, fmt.Errorf("颜色需要 6 位十六进制数")
	}
	value, err := strconv.ParseUint(text, 16, 24)
	if err != nil {
		return color.RGBA{}, fmt.Errorf("颜色需要 6 位十六进制数")
	}
	return color.RGBA{R: uint8(value >> 16), G: uint8(value >> 8), B: uint8(value), A: 255}, nil
}

func decodeViewerAppearance(data []byte) viewerAppearance {
	defaults := defaultViewerAppearance()
	a := defaultViewerAppearance()
	if json.Unmarshal(data, &a) != nil {
		return defaults
	}
	a.OverlayOpacity = min(100, max(0, a.OverlayOpacity))
	a.PanelOpacity = min(100, max(40, a.PanelOpacity))
	colors := append([]string(nil), defaults.Colors...)
	for i := range colors {
		if i < len(a.Colors) {
			if c, err := parseRGB(a.Colors[i]); err == nil {
				colors[i] = fmt.Sprintf("#%02X%02X%02X", c.R, c.G, c.B)
			}
		}
	}
	a.Colors = colors
	return a
}

func (a viewerAppearance) palette() playerPalette {
	colors := [5]color.RGBA{}
	for i := range colors {
		colors[i], _ = parseRGB(a.Colors[i])
	}
	return playerPalette{T: colors[0], CT: colors[1], Health: colors[2], HealthLow: colors[3], Name: colors[4]}
}
