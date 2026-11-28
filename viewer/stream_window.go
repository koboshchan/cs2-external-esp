package main

import "strings"

type streamWindow struct {
	Handle        uintptr
	Width, Height int32
	Visible       bool
	Active        bool
}

func isGeForceExecutable(path string) bool {
	path = strings.ReplaceAll(path, "\\", "/")
	name := path[strings.LastIndex(path, "/")+1:]
	return strings.EqualFold(name, "GeForceNOW.exe") || strings.EqualFold(name, "GeForceNOWContainer.exe")
}

func chooseStreamWindow(windows []streamWindow) uintptr {
	var best streamWindow
	for _, window := range windows {
		if window.Handle == 0 || window.Width < 320 || window.Height < 180 {
			continue
		}
		better := best.Handle == 0
		if window.Visible != best.Visible {
			better = window.Visible
		} else if window.Visible && window.Active != best.Active {
			better = window.Active
		} else {
			better = better || int64(window.Width)*int64(window.Height) > int64(best.Width)*int64(best.Height)
		}
		if better {
			best = window
		}
	}
	return best.Handle
}
