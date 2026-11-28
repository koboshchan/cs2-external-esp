package main

import "encoding/json"

type displaySyncSettings struct {
	DelayMS int `json:"delay_ms"`
}

func decodeDisplaySyncSettings(data []byte) displaySyncSettings {
	settings := displaySyncSettings{DelayMS: defaultDisplayDelayMS}
	var saved struct {
		DelayMS *int `json:"delay_ms"`
	}
	if json.Unmarshal(data, &saved) == nil && saved.DelayMS != nil {
		settings.DelayMS = min(maxDisplayDelayMS, max(0, *saved.DelayMS))
	}
	return settings
}
