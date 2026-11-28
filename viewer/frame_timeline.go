package main

import (
	"math"
	"time"
)

const (
	defaultDisplayDelayMS = 35
	maxDisplayDelayMS     = 250
	maxInterpolationGap   = 200 * time.Millisecond
	maxBufferedFrames     = 256
)

// frameTimeline is guarded by Relay.mu. Its clock follows host timestamps,
// retaining the earliest observed arrival offset so network jitter does not
// move the playback clock backwards on each packet.
type frameTimeline struct {
	frames      []*Frame
	clockAt     time.Time
	clockHostMS uint64
}

func (t *frameTimeline) append(f *Frame, at time.Time) {
	if len(t.frames) > 0 {
		previous := t.frames[len(t.frames)-1]
		if int32(f.Seq-previous.Seq) <= 0 || f.HostTimeMS <= previous.HostTimeMS ||
			f.HostTimeMS-previous.HostTimeMS > uint64(maxInterpolationGap/time.Millisecond) ||
			f.MapName != previous.MapName {
			*t = frameTimeline{}
		}
	}
	if len(t.frames) == 0 {
		t.clockAt = at
	} else {
		expected := t.clockAt.Add(time.Duration(f.HostTimeMS-t.clockHostMS) * time.Millisecond)
		if at.Before(expected) {
			t.clockAt = at
		} else {
			t.clockAt = expected
		}
	}
	t.clockHostMS = f.HostTimeMS
	t.frames = append(t.frames, f)
	// Keep enough history for the full delay range, including network stalls.
	first := 0
	for first < len(t.frames)-1 && (len(t.frames)-first > maxBufferedFrames ||
		f.HostTimeMS-t.frames[first].HostTimeMS > 1000) {
		first++
	}
	if first > 0 {
		copy(t.frames, t.frames[first:])
		clear(t.frames[len(t.frames)-first:])
		t.frames = t.frames[:len(t.frames)-first]
	}
}

func (t *frameTimeline) sample(now time.Time, delayMS int) *Frame {
	if len(t.frames) == 0 {
		return nil
	}
	latest := t.frames[len(t.frames)-1]
	if delayMS <= 0 {
		return latest
	}
	delayMS = min(delayMS, maxDisplayDelayMS)
	// Work relative to the latest timestamp to avoid converting a uint64
	// uptime to floating point and losing sub-frame precision.
	target := now.Sub(t.clockAt).Seconds()*1000 - float64(delayMS)
	if target >= 0 {
		return latest // Never predict future camera angles or bone positions.
	}
	for i := len(t.frames) - 2; i >= 0; i-- {
		older, newer := t.frames[i], t.frames[i+1]
		olderOffset := -float64(latest.HostTimeMS - older.HostTimeMS)
		if target >= olderOffset {
			span := float64(newer.HostTimeMS - older.HostTimeMS)
			return interpolateFrame(older, newer, float32((target-olderOffset)/span))
		}
	}
	return t.frames[0]
}

func interpolateFloat(a, b, alpha float32) float32 { return a + (b-a)*alpha }

func interpolateVec3(a, b Vec3, alpha float32) Vec3 {
	return Vec3{interpolateFloat(a.X, b.X, alpha), interpolateFloat(a.Y, b.Y, alpha), interpolateFloat(a.Z, b.Z, alpha)}
}

func angleDifference(a, b float32) float32 {
	return float32(math.Remainder(float64(b)-float64(a), 360))
}

func interpolateAngles(a, b Vec2, alpha float32) Vec2 {
	return Vec2{a.X + angleDifference(a.X, b.X)*alpha, a.Y + angleDifference(a.Y, b.Y)*alpha}
}

func continuousView(posA, posB Vec3, anglesA, anglesB Vec2, fovA, fovB float32) bool {
	dx, dy, dz := posB.X-posA.X, posB.Y-posA.Y, posB.Z-posA.Z
	return dx*dx+dy*dy+dz*dz <= 128*128 &&
		math.Abs(float64(angleDifference(anglesA.X, anglesB.X))) <= 45 &&
		math.Abs(float64(angleDifference(anglesA.Y, anglesB.Y))) <= 45 &&
		math.Abs(float64(fovB-fovA)) < 0.01
}

func interpolateFrame(a, b *Frame, alpha float32) *Frame {
	if alpha <= 0 {
		return a
	}
	if alpha >= 1 {
		return b
	}
	ca, cb := a.Camera, b.Camera
	if a.Flags != b.Flags || ca.Index != cb.Index || ca.HostIndex != cb.HostIndex ||
		ca.ObserverMode != cb.ObserverMode || ca.Team != cb.Team || ca.FovMode != cb.FovMode ||
		!continuousView(ca.EyePos, cb.EyePos, ca.EyeAngles, cb.EyeAngles, ca.Fov, cb.Fov) ||
		!continuousView(ca.HostEyePos, cb.HostEyePos, ca.HostEyeAngles, cb.HostEyeAngles, ca.HostFov, cb.HostFov) {
		return a // Camera cuts and scope changes remain discrete frame events.
	}
	out := *a
	out.HostTimeMS = a.HostTimeMS + uint64(math.Round(float64(b.HostTimeMS-a.HostTimeMS)*float64(alpha)))
	out.Camera.EyePos = interpolateVec3(ca.EyePos, cb.EyePos, alpha)
	out.Camera.EyeAngles = interpolateAngles(ca.EyeAngles, cb.EyeAngles, alpha)
	out.Camera.HostEyePos = interpolateVec3(ca.HostEyePos, cb.HostEyePos, alpha)
	out.Camera.HostEyeAngles = interpolateAngles(ca.HostEyeAngles, cb.HostEyeAngles, alpha)
	for i := range out.Camera.ViewMatrix {
		out.Camera.ViewMatrix[i] = interpolateFloat(ca.ViewMatrix[i], cb.ViewMatrix[i], alpha)
	}
	byKey := make(map[string]*Player, len(b.Players))
	for i := range b.Players {
		byKey[playerViewKey(&b.Players[i])] = &b.Players[i]
	}
	out.Players = append([]Player(nil), a.Players...)
	for i := range out.Players {
		p := &out.Players[i]
		next := byKey[playerViewKey(p)]
		if next == nil || !p.Alive() || !next.Alive() || p.Team != next.Team ||
			p.Scoped() != next.Scoped() || p.BoneMask != next.BoneMask || len(p.Bones) != len(next.Bones) ||
			!continuousView(p.Pos, next.Pos, p.EyeAngles, next.EyeAngles, p.Fov, next.Fov) ||
			!continuousView(p.EyePos, next.EyePos, p.EyeAngles, next.EyeAngles, p.Fov, next.Fov) {
			continue
		}
		p.Pos = interpolateVec3(p.Pos, next.Pos, alpha)
		p.Vel = interpolateVec3(p.Vel, next.Vel, alpha)
		p.EyePos = interpolateVec3(p.EyePos, next.EyePos, alpha)
		p.EyeAngles = interpolateAngles(p.EyeAngles, next.EyeAngles, alpha)
		p.Bones = append([]Vec3(nil), p.Bones...)
		for j := range p.Bones {
			p.Bones[j] = interpolateVec3(p.Bones[j], next.Bones[j], alpha)
		}
	}
	return &out
}
