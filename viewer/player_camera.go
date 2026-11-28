package main

import (
	"fmt"
	"strings"
)

var playerLabelReplacer = strings.NewReplacer("\x00", " ", "\n", " ", "\r", " ", "\t", " ")

func playerViewKey(p *Player) string {
	if p.SteamID != 0 {
		return fmt.Sprintf("steam:%d", p.SteamID)
	}
	return fmt.Sprintf("slot:%d:%s", p.Index, playerLabelReplacer.Replace(p.Name))
}

func playerViewList(players []Player) string {
	var list strings.Builder
	for i := range players {
		p := &players[i]
		name := playerLabelReplacer.Replace(p.Name)
		fmt.Fprintf(&list, "%s\t#%d  %s  (team %d)\n", playerViewKey(p), p.Index, name, p.Team)
	}
	return strings.TrimSuffix(list.String(), "\n")
}

func (s *scene) cameraForFrame(f *Frame, aspect float32) ([16]float32, *Player, bool) {
	camera := f.Camera
	player := f.PlayerByIndex(int(camera.HostIndex))
	eye, angles, fov := camera.HostEyePos, camera.HostEyeAngles, camera.HostFov
	if s.viewKey != "" {
		player = nil
		for i := range f.Players {
			if playerViewKey(&f.Players[i]) == s.viewKey {
				player = &f.Players[i]
				break
			}
		}
		if player == nil {
			return [16]float32{}, nil, false
		}
		eye, angles, fov = player.EyePos, player.EyeAngles, player.Fov
	}
	if fov <= 1 || fov >= 180 {
		fov = 90
	}
	if s.viewKey == "" && s.useHostMatrix && MatrixXScale(camera.ViewMatrix) > 0 && MatrixYScale(camera.ViewMatrix) > 0 {
		return camera.ViewMatrix, player, true
	}
	return BuildViewMatrix(eye, angles, fov, aspect, camera.FovMode), player, true
}
