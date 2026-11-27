package main

import (
	"encoding/hex"
	"encoding/json"
	"math/rand"
	"os"
	"testing"
)

type goldenExpect struct {
	Seq        uint32  `json:"seq"`
	HostTimeMS uint64  `json:"host_time_ms"`
	MapName    string  `json:"map_name"`
	Flags      uint16  `json:"flags"`
	Camera     struct {
		Index        int8     `json:"index"`
		ObserverMode uint8    `json:"observer_mode"`
		Team         uint8    `json:"team"`
		FovMode      uint8    `json:"fov_mode"`
		EyePos       []float32 `json:"eye_pos"`
		EyeAngles    []float32 `json:"eye_angles"`
		Fov          float32  `json:"fov"`
	} `json:"camera"`
	PlayerCount int `json:"player_count"`
	Players     []struct {
		Index    int8     `json:"index"`
		Team     uint8    `json:"team"`
		Health   uint16   `json:"health"`
		Armor    uint16   `json:"armor"`
		Money    int32    `json:"money"`
		Flags    uint32   `json:"flags"`
		SteamID  uint64   `json:"steam_id"`
		Name     string   `json:"name"`
		WeaponID uint16   `json:"weapon_id"`
		Ammo     uint16   `json:"ammo"`
		BoneMask uint32   `json:"bone_mask"`
		EyePos   []float32 `json:"eye_pos"`
		Fov      float32  `json:"fov"`
	} `json:"players"`
	Bomb struct {
		Pos       []float32 `json:"pos"`
		TimeLeft  float32   `json:"time_left"`
		IsPlanted bool      `json:"is_planted"`
		Site      int8      `json:"site"`
	} `json:"bomb"`
}

type goldenFile struct {
	Proto  int          `json:"proto"`
	Size   int          `json:"size"`
	Hex    string       `json:"hex"`
	Expect goldenExpect `json:"expect"`
}

func loadGolden(t *testing.T) (goldenFile, []byte) {
	t.Helper()

	raw, err := os.ReadFile("../tests/golden/frame_v2.json")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}

	var g goldenFile
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatalf("parse golden: %v", err)
	}

	data, err := hex.DecodeString(g.Hex)
	if err != nil {
		t.Fatalf("decode hex: %v", err)
	}

	if len(data) != g.Size {
		t.Fatalf("golden size mismatch: %d != %d", len(data), g.Size)
	}

	return g, data
}

func TestGoldenFrame(t *testing.T) {
	g, data := loadGolden(t)

	f, err := ParseFrame(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if g.Proto != protoVersion {
		t.Fatalf("golden proto %d, viewer proto %d", g.Proto, protoVersion)
	}
	if f.Seq != g.Expect.Seq || f.HostTimeMS != g.Expect.HostTimeMS || f.Flags != g.Expect.Flags {
		t.Fatalf("header mismatch: %+v", f)
	}
	if f.MapName != g.Expect.MapName {
		t.Fatalf("map %q != %q", f.MapName, g.Expect.MapName)
	}

	c := f.Camera
	e := g.Expect.Camera
	if c.Index != e.Index || c.ObserverMode != e.ObserverMode || c.Team != e.Team || c.FovMode != e.FovMode {
		t.Fatalf("camera header mismatch: %+v", c)
	}
	if c.EyePos.X != e.EyePos[0] || c.EyePos.Y != e.EyePos[1] || c.EyePos.Z != e.EyePos[2] {
		t.Fatalf("camera eye mismatch: %+v", c.EyePos)
	}
	if c.EyeAngles.X != e.EyeAngles[0] || c.EyeAngles.Y != e.EyeAngles[1] {
		t.Fatalf("camera angles mismatch: %+v", c.EyeAngles)
	}
	if c.Fov != e.Fov {
		t.Fatalf("camera fov mismatch: %v != %v", c.Fov, e.Fov)
	}

	if len(f.Players) != g.Expect.PlayerCount {
		t.Fatalf("player count %d != %d", len(f.Players), g.Expect.PlayerCount)
	}

	for i, want := range g.Expect.Players {
		got := f.Players[i]
		if got.Index != want.Index || got.Team != want.Team || got.Health != want.Health ||
			got.Armor != want.Armor || got.Money != want.Money || got.Flags != want.Flags {
			t.Fatalf("player %d mismatch: %+v", i, got)
		}
		if got.SteamID != want.SteamID || got.Name != want.Name {
			t.Fatalf("player %d identity mismatch: %+v", i, got)
		}
		if got.WeaponID != want.WeaponID || got.Ammo != want.Ammo || got.BoneMask != want.BoneMask {
			t.Fatalf("player %d inventory mismatch: %+v", i, got)
		}
		if got.EyePos.X != want.EyePos[0] || got.EyePos.Y != want.EyePos[1] || got.EyePos.Z != want.EyePos[2] {
			t.Fatalf("player %d eye mismatch: %+v", i, got.EyePos)
		}
		if got.Fov != want.Fov {
			t.Fatalf("player %d fov mismatch", i)
		}
		if len(got.Bones) != popcount(want.BoneMask) {
			t.Fatalf("player %d bone count %d", i, len(got.Bones))
		}
	}

	if f.Bomb.IsPlanted != g.Expect.Bomb.IsPlanted || f.Bomb.Site != g.Expect.Bomb.Site {
		t.Fatalf("bomb mismatch: %+v", f.Bomb)
	}
	if f.Bomb.TimeLeft != g.Expect.Bomb.TimeLeft {
		t.Fatalf("bomb timer mismatch: %v", f.Bomb.TimeLeft)
	}
}

func TestGoldenBonesResolve(t *testing.T) {
	_, data := loadGolden(t)

	f, err := ParseFrame(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// The golden frame sets bones 1, 6, 7 and 23.
	for _, id := range []int{1, 6, 7, 23} {
		b, ok := f.Players[0].Bone(id)
		if !ok {
			t.Fatalf("bone %d missing", id)
		}
		if b.X != float32(id) {
			t.Fatalf("bone %d x=%v", id, b.X)
		}
	}
	if _, ok := f.Players[0].Bone(2); ok {
		t.Fatal("bone 2 should be absent")
	}
}

func TestRejectsTruncated(t *testing.T) {
	_, data := loadGolden(t)

	for cut := 0; cut < len(data); cut++ {
		if _, err := ParseFrame(data[:cut]); err == nil {
			t.Fatalf("accepted frame truncated at %d", cut)
		}
	}
}

func TestRejectsTrailingBytes(t *testing.T) {
	_, data := loadGolden(t)

	extended := append(append([]byte{}, data...), 0)
	if _, err := ParseFrame(extended); err == nil {
		t.Fatal("accepted trailing bytes")
	}
}

func TestMutationFuzz(t *testing.T) {
	_, data := loadGolden(t)

	rng := rand.New(rand.NewSource(1234))
	accepted := 0

	for iter := 0; iter < 20000; iter++ {
		mutated := append([]byte{}, data...)
		edits := 1 + rng.Intn(4)
		for e := 0; e < edits; e++ {
			mutated[rng.Intn(len(mutated))] = byte(rng.Intn(256))
		}

		f, err := ParseFrame(mutated)
		if err != nil {
			continue
		}
		accepted++

		// Anything accepted must be internally consistent.
		if len(f.Players) > maxPlayers {
			t.Fatalf("accepted %d players", len(f.Players))
		}
		for _, p := range f.Players {
			if len(p.Bones) != popcount(p.BoneMask) {
				t.Fatalf("accepted inconsistent bones")
			}
		}
	}

	t.Logf("accepted %d/20000 mutants", accepted)
}
