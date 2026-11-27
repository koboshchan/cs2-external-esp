package main

// Wire format for the remote ESP, mirroring src/net/protocol.hpp (v2).
// Every field is little-endian and packed by hand, so the bytes are identical
// on Windows and macOS. Parsing treats the payload as hostile input: bounds,
// counts and floats are all validated before use.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

const (
	protoVersion  = 2
	magic         = 0x52325343 // 'CS2R'
	maxFrameBytes = 64 * 1024
	minFrameBytes = 12

	maxPlayers = 64
	maxBones   = 32
	maxNameLen = 32
	maxMapLen  = 32
)

const (
	frameHostAlive      = 1 << 0
	frameHostSpectating = 1 << 1
)

const (
	playerAlive     = 1 << 0
	playerScoped    = 1 << 1
	playerFlashed   = 1 << 2
	playerDefusing  = 1 << 3
	playerSpotted   = 1 << 4
	playerHasC4     = 1 << 5
	playerLocal     = 1 << 6
	playerBot       = 1 << 7
	playerReloading = 1 << 8
)

const (
	fovSourceHorPlus = 0
	fovHorizontal    = 1
)

// Bone indices used by the renderer, matching BoneIds in the C++ side.
const (
	bonePelvis      = 1
	boneSpine1      = 3
	boneSpine2      = 4
	boneChest       = 23
	boneNeck        = 6
	boneHead        = 7
	boneShoulderL   = 9
	boneElbowL      = 10
	boneHandL       = 11
	boneShoulderR   = 13
	boneElbowR      = 14
	boneHandR       = 15
	boneHipL        = 17
	boneKneeL       = 18
	boneFootHeelL   = 19
	boneHipR        = 20
	boneKneeR       = 21
	boneFootHeelR   = 22
)

var skeletonConnections = [][2]int{
	{bonePelvis, boneSpine1},
	{boneSpine1, boneSpine2},
	{boneSpine2, boneChest},
	{boneChest, boneNeck},
	{boneNeck, boneHead},
	{boneNeck, boneShoulderL},
	{boneShoulderL, boneElbowL},
	{boneElbowL, boneHandL},
	{boneNeck, boneShoulderR},
	{boneShoulderR, boneElbowR},
	{boneElbowR, boneHandR},
	{bonePelvis, boneHipL},
	{boneHipL, boneKneeL},
	{boneKneeL, boneFootHeelL},
	{bonePelvis, boneHipR},
	{boneHipR, boneKneeR},
	{boneKneeR, boneFootHeelR},
}

type Vec2 struct {
	X, Y float32
}

type Vec3 struct {
	X, Y, Z float32
}

type Camera struct {
	Index        int8
	ObserverMode uint8
	Team         uint8
	FovMode      uint8

	EyePos    Vec3
	EyeAngles Vec2
	Fov       float32

	ViewMatrix [16]float32

	HostIndex    int8
	HostEyePos   Vec3
	HostEyeAngles Vec2
	HostFov      float32
}

type Player struct {
	Index   int8
	Team    uint8
	Health  uint16
	Armor   uint16
	Money   int32
	Flags   uint32
	SteamID uint64
	Name    string

	WeaponID uint16
	Ammo     uint16

	Pos       Vec3
	Vel       Vec3
	EyePos    Vec3
	EyeAngles Vec2
	Fov       float32

	BoneMask uint32
	Bones    []Vec3
}

func (p *Player) Alive() bool     { return p.Flags&playerAlive != 0 }
func (p *Player) Scoped() bool    { return p.Flags&playerScoped != 0 }
func (p *Player) Flashed() bool   { return p.Flags&playerFlashed != 0 }
func (p *Player) Defusing() bool  { return p.Flags&playerDefusing != 0 }
func (p *Player) Spotted() bool   { return p.Flags&playerSpotted != 0 }
func (p *Player) HasC4() bool     { return p.Flags&playerHasC4 != 0 }
func (p *Player) Local() bool     { return p.Flags&playerLocal != 0 }
func (p *Player) Bot() bool       { return p.Flags&playerBot != 0 }
func (p *Player) Reloading() bool { return p.Flags&playerReloading != 0 }

// Bone returns the world position of a bone index and whether it is present.
func (p *Player) Bone(index int) (Vec3, bool) {
	if index < 0 || index >= 32 || p.BoneMask&(1<<uint(index)) == 0 {
		return Vec3{}, false
	}
	// Bones are serialized in ascending bit order.
	slot := 0
	for i := 0; i < index; i++ {
		if p.BoneMask&(1<<uint(i)) != 0 {
			slot++
		}
	}
	if slot >= len(p.Bones) {
		return Vec3{}, false
	}
	return p.Bones[slot], true
}

type Bomb struct {
	Pos       Vec3
	TimeLeft  float32
	IsPlanted bool
	Site      int8
}

type Frame struct {
	Flags      uint16
	Seq        uint32
	HostTimeMS uint64
	MatchTime  float32
	MapName    string

	Camera  Camera
	Players []Player
	Bomb    Bomb
}

func (f *Frame) PlayerBySteamID(id uint64) *Player {
	for i := range f.Players {
		if f.Players[i].SteamID == id {
			return &f.Players[i]
		}
	}
	return nil
}

func (f *Frame) PlayerByName(name string) *Player {
	for i := range f.Players {
		if f.Players[i].Name == name {
			return &f.Players[i]
		}
	}
	return nil
}

func (f *Frame) PlayerByIndex(index int) *Player {
	for i := range f.Players {
		if int(f.Players[i].Index) == index {
			return &f.Players[i]
		}
	}
	return nil
}

var errTruncated = errors.New("truncated frame")

type reader struct {
	b   []byte
	off int
	err error
}

func (r *reader) need(n int) bool {
	if r.err != nil {
		return false
	}
	if n < 0 || r.off+n > len(r.b) {
		r.err = errTruncated
		return false
	}
	return true
}

func (r *reader) u8() uint8 {
	if !r.need(1) {
		return 0
	}
	v := r.b[r.off]
	r.off++
	return v
}

func (r *reader) u16() uint16 {
	if !r.need(2) {
		return 0
	}
	v := binary.LittleEndian.Uint16(r.b[r.off:])
	r.off += 2
	return v
}

func (r *reader) u32() uint32 {
	if !r.need(4) {
		return 0
	}
	v := binary.LittleEndian.Uint32(r.b[r.off:])
	r.off += 4
	return v
}

func (r *reader) u64() uint64 {
	if !r.need(8) {
		return 0
	}
	v := binary.LittleEndian.Uint64(r.b[r.off:])
	r.off += 8
	return v
}

func (r *reader) i8() int8 { return int8(r.u8()) }

func (r *reader) i32() int32 { return int32(r.u32()) }

func (r *reader) f32() float32 { return math.Float32frombits(r.u32()) }

func (r *reader) vec2() Vec2 { return Vec2{r.f32(), r.f32()} }

func (r *reader) vec3() Vec3 { return Vec3{r.f32(), r.f32(), r.f32()} }

func (r *reader) str8() string {
	n := int(r.u8())
	if !r.need(n) {
		return ""
	}
	s := string(r.b[r.off : r.off+n])
	r.off += n
	return s
}

func finite(v float32) bool { return !math.IsNaN(float64(v)) && !math.IsInf(float64(v), 0) }

func finite3(v Vec3) bool { return finite(v.X) && finite(v.Y) && finite(v.Z) }

func finite2(v Vec2) bool { return finite(v.X) && finite(v.Y) }

func popcount(v uint32) int {
	n := 0
	for v != 0 {
		v &= v - 1
		n++
	}
	return n
}

// ParseFrame decodes one snapshot. Anything malformed is rejected as a whole.
func ParseFrame(data []byte) (*Frame, error) {
	if len(data) < minFrameBytes {
		return nil, fmt.Errorf("frame too small (%d)", len(data))
	}
	if len(data) > maxFrameBytes {
		return nil, fmt.Errorf("frame too large (%d)", len(data))
	}

	r := &reader{b: data}

	if r.u32() != magic {
		return nil, errors.New("bad magic")
	}
	if v := r.u16(); v != protoVersion {
		return nil, fmt.Errorf("unsupported protocol %d", v)
	}

	f := &Frame{}
	f.Flags = r.u16()
	f.Seq = r.u32()
	f.HostTimeMS = r.u64()
	f.MatchTime = r.f32()
	f.MapName = r.str8()
	playerCount := int(r.u8())

	if r.err != nil {
		return nil, r.err
	}
	if len(f.MapName) > maxMapLen {
		return nil, errors.New("map name too long")
	}
	if playerCount > maxPlayers {
		return nil, fmt.Errorf("too many players (%d)", playerCount)
	}

	c := &f.Camera
	c.Index = r.i8()
	c.ObserverMode = r.u8()
	c.Team = r.u8()
	c.FovMode = r.u8()
	c.EyePos = r.vec3()
	c.EyeAngles = r.vec2()
	c.Fov = r.f32()
	for i := 0; i < 16; i++ {
		c.ViewMatrix[i] = r.f32()
	}
	c.HostIndex = r.i8()
	c.HostEyePos = r.vec3()
	c.HostEyeAngles = r.vec2()
	c.HostFov = r.f32()

	if r.err != nil {
		return nil, r.err
	}
	if !finite3(c.EyePos) || !finite2(c.EyeAngles) || !finite(c.Fov) || c.Fov <= 0 || c.Fov > 180 {
		return nil, errors.New("invalid camera")
	}

	f.Players = make([]Player, 0, playerCount)
	for i := 0; i < playerCount; i++ {
		var p Player
		p.Index = r.i8()
		p.Team = r.u8()
		p.Health = r.u16()
		p.Armor = r.u16()
		p.Money = r.i32()
		p.Flags = r.u32()
		p.SteamID = r.u64()
		p.Name = r.str8()
		p.WeaponID = r.u16()
		p.Ammo = r.u16()
		p.Pos = r.vec3()
		p.Vel = r.vec3()
		p.EyeAngles = r.vec2()
		p.EyePos = r.vec3()
		p.Fov = r.f32()
		p.BoneMask = r.u32()

		if r.err != nil {
			return nil, r.err
		}
		if len(p.Name) > maxNameLen {
			return nil, errors.New("player name too long")
		}
		if !finite3(p.Pos) || !finite3(p.Vel) || !finite3(p.EyePos) || !finite2(p.EyeAngles) {
			return nil, errors.New("player position not finite")
		}
		if !finite(p.Fov) || p.Fov <= 0 || p.Fov > 180 {
			return nil, errors.New("player fov out of range")
		}

		bones := popcount(p.BoneMask)
		if bones > maxBones {
			return nil, errors.New("too many bones")
		}
		p.Bones = make([]Vec3, bones)
		for b := 0; b < bones; b++ {
			p.Bones[b] = r.vec3()
			if !finite3(p.Bones[b]) {
				return nil, errors.New("bone not finite")
			}
		}
		if r.err != nil {
			return nil, r.err
		}

		f.Players = append(f.Players, p)
	}

	f.Bomb.Pos = r.vec3()
	f.Bomb.TimeLeft = r.f32()
	f.Bomb.IsPlanted = r.u8() != 0
	f.Bomb.Site = r.i8()

	if r.err != nil {
		return nil, r.err
	}
	if r.off != len(data) {
		return nil, fmt.Errorf("trailing bytes (%d of %d)", r.off, len(data))
	}
	if !finite3(f.Bomb.Pos) || !finite(f.Bomb.TimeLeft) {
		return nil, errors.New("bomb not finite")
	}

	return f, nil
}
