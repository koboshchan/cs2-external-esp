package main

import (
	"encoding/binary"
	"math"
	"reflect"
	"sync"
	"testing"
	"time"
)

func timelineTestFrame(seq uint32, ms uint64) *Frame {
	x := float32(ms - 1000)
	return &Frame{Seq: seq, HostTimeMS: ms, MapName: "test", Camera: Camera{HostFov: 90, Fov: 90},
		Players: []Player{{Index: 1, SteamID: 20, Name: "B", Team: 2, Flags: playerAlive,
			Health: 100, Fov: 90, Pos: Vec3{x, 0, 0}, EyePos: Vec3{x, 0, 64},
			EyeAngles: Vec2{x / 10, x / 5}, BoneMask: 1 << boneHead, Bones: []Vec3{{x + 100, 0, 64}}}}}
}

func TestTimelineSamplesCameraAndBonesTogether(t *testing.T) {
	start := time.Unix(10, 0)
	var timeline frameTimeline
	a, b := timelineTestFrame(1, 1000), timelineTestFrame(2, 1040)
	timeline.append(a, start)
	timeline.append(b, start.Add(40*time.Millisecond))
	f := timeline.sample(start.Add(40*time.Millisecond), 20)
	p := f.Players[0]
	if f.HostTimeMS != 1020 || p.EyePos.X != 20 || p.Bones[0].X != 120 || p.EyeAngles != (Vec2{2, 4}) {
		t.Fatalf("camera and bones used different sample times: %+v", f)
	}
	if timeline.sample(start, 250) != a || timeline.sample(start.Add(time.Second), 35) != b {
		t.Fatal("sampling outside history must hold an endpoint")
	}
	if timeline.sample(start, 0) != b || timeline.sample(start, -10) != b {
		t.Fatal("zero delay must preserve immediate latest-frame display")
	}
	f.Players[0].Bones[0].X = -1
	if a.Players[0].Bones[0].X != 100 || b.Players[0].Bones[0].X != 140 {
		t.Fatal("sampling mutated a received frame")
	}
}

func TestTimelineUsesHostClockDespiteArrivalJitter(t *testing.T) {
	start := time.Unix(10, 0)
	var timeline frameTimeline
	for i, arrivalMS := range []int{0, 50, 85} {
		timeline.append(timelineTestFrame(uint32(i+1), uint64(1000+i*40)), start.Add(time.Duration(arrivalMS)*time.Millisecond))
	}
	if got := timeline.sample(start.Add(90*time.Millisecond), 35).HostTimeMS; got != 1055 {
		t.Fatalf("arrival jitter moved the clock: got %d, want 1055", got)
	}
	// A later packet taking a faster path improves the observed clock offset.
	timeline.append(timelineTestFrame(4, 1120), start.Add(118*time.Millisecond))
	if got := timeline.sample(start.Add(120*time.Millisecond), 35).HostTimeMS; got != 1087 {
		t.Fatalf("earlier offset was ignored: %d", got)
	}
}

func TestTimelineIdentityAndYawWrap(t *testing.T) {
	a, b := timelineTestFrame(1, 1000), timelineTestFrame(2, 1040)
	a.Players[0].EyeAngles = Vec2{2, 179}
	b.Players[0].EyeAngles = Vec2{4, -179}
	b.Players[0].Index = 8
	b.Players = append([]Player{{Index: 1, SteamID: 99}}, b.Players...)
	f := interpolateFrame(a, b, 0.5)
	if f.Players[0].EyeAngles != (Vec2{3, 180}) || f.Players[0].EyePos.X != 20 {
		t.Fatalf("player order, slot change or angle wrap broke interpolation: %+v", f.Players[0])
	}
}

func TestTimelineDiscontinuities(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*Frame)
	}{
		{"death", func(f *Frame) { f.Players[0].Flags = 0 }},
		{"scope", func(f *Frame) { f.Players[0].Flags |= playerScoped; f.Players[0].Fov = 40 }},
		{"teleport", func(f *Frame) { f.Players[0].EyePos.X = 500 }},
		{"fast turn", func(f *Frame) { f.Players[0].EyeAngles.Y = 90 }},
		{"different player", func(f *Frame) { f.Players[0].SteamID++ }},
		{"changed bones", func(f *Frame) { f.Players[0].BoneMask = 0; f.Players[0].Bones = nil }},
		{"missing player", func(f *Frame) { f.Players = nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			a, b := timelineTestFrame(1, 1000), timelineTestFrame(2, 1040)
			test.change(b)
			if got := interpolateFrame(a, b, 0.5); !reflect.DeepEqual(got.Players[0], a.Players[0]) {
				t.Fatalf("discontinuous player was blended: %+v", got.Players[0])
			}
			if interpolateFrame(a, b, 1) != b {
				t.Fatal("discrete state did not take effect at its timestamp")
			}
		})
	}
	for _, change := range []func(*Frame){
		func(f *Frame) { f.Camera.HostIndex++ },
		func(f *Frame) { f.Camera.ObserverMode++ },
		func(f *Frame) { f.Camera.HostFov = 40 },
		func(f *Frame) { f.Camera.HostEyePos.X = 500 },
		func(f *Frame) { f.Flags ^= frameHostAlive },
	} {
		a, b := timelineTestFrame(1, 1000), timelineTestFrame(2, 1040)
		change(b)
		if interpolateFrame(a, b, 0.5) != a {
			t.Fatal("camera cut was blended")
		}
	}
}

func TestTimelineResetAndBounds(t *testing.T) {
	start := time.Unix(10, 0)
	for _, test := range []struct {
		name   string
		change func(*Frame)
	}{
		{"map", func(f *Frame) { f.MapName = "new map" }},
		{"clock", func(f *Frame) { f.HostTimeMS = 900 }},
		{"long gap", func(f *Frame) { f.HostTimeMS = 1400 }},
		{"sequence restart", func(f *Frame) { f.Seq = 0 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			var timeline frameTimeline
			timeline.append(timelineTestFrame(1, 1000), start)
			b := timelineTestFrame(2, 1040)
			test.change(b)
			timeline.append(b, start.Add(40*time.Millisecond))
			if len(timeline.frames) != 1 || timeline.sample(start, 35) != b {
				t.Fatal("old session data survived reset")
			}
		})
	}
	var timeline frameTimeline
	for i := 0; i < 3000; i++ {
		timeline.append(timelineTestFrame(uint32(i), uint64(1000+i)), start.Add(time.Duration(i)*time.Millisecond))
	}
	if len(timeline.frames) != maxBufferedFrames || timeline.frames[len(timeline.frames)-1].Seq != 2999 {
		t.Fatalf("history not bounded: %d", len(timeline.frames))
	}
	if got := timeline.sample(start.Add(2999*time.Millisecond), 10000).HostTimeMS; got != 3749 {
		t.Fatalf("delay limit failed: %d", got)
	}
}

func TestTimelineSequenceWrapAndShortLoss(t *testing.T) {
	start := time.Unix(10, 0)
	var timeline frameTimeline
	timeline.append(timelineTestFrame(math.MaxUint32, 1000), start)
	timeline.append(timelineTestFrame(0, 1040), start.Add(40*time.Millisecond))
	timeline.append(timelineTestFrame(2, 1120), start.Add(120*time.Millisecond))
	if len(timeline.frames) != 3 || timeline.sample(start.Add(120*time.Millisecond), 40).HostTimeMS != 1080 {
		t.Fatal("wrap or short sequence gap discarded useful history")
	}
}

func TestTimelineRecoilProjectionReplay(t *testing.T) {
	// Synthetic 30 Hz metadata and 60 Hz video with a known 50 ms display
	// delay. This validates time alignment, not CS2's actual camera recoil.
	frameAt := func(ms float64) *Frame {
		return &Frame{HostTimeMS: 1000 + uint64(ms), MapName: "test", Camera: Camera{Fov: 90, HostFov: 90},
			Players: []Player{
				{Index: 1, SteamID: 20, Flags: playerAlive, Fov: 90, EyePos: Vec3{0, 0, 64},
					EyeAngles: Vec2{float32(2 * math.Sin(ms*0.04)), float32(math.Sin(ms * 0.01))}},
				{Index: 2, SteamID: 30, Flags: playerAlive, Fov: 90, Pos: Vec3{1000, 100, 0},
					BoneMask: 1 << boneHead, Bones: []Vec3{{1000, 100, 80 + float32(3*math.Sin(ms*0.006))}}},
			}}
	}
	start := time.Unix(10, 0)
	var timeline frameTimeline
	scene := newScene(&Relay{}, options{})
	scene.viewKey = "steam:20"
	project := func(f *Frame) Vec2 {
		matrix, _, _ := scene.cameraForFrame(f, 16.0/9)
		point, visible := Project(matrix, f.Players[1].Bones[0], 1920, 1080)
		if !visible {
			t.Fatal("synthetic target left the viewport")
		}
		return point
	}
	var directSquared, bufferedSquared float64
	frames, samples := 0, 0
	for nowMS := 0; nowMS < 2000; nowMS += 16 {
		for {
			hostMS := frames * 33
			arrivalMS := hostMS + 15 + []int{0, 5, 12, 2, 8}[frames%5]
			if arrivalMS > nowMS {
				break
			}
			frame := frameAt(float64(hostMS))
			frame.Seq = uint32(frames + 1)
			timeline.append(frame, start.Add(time.Duration(arrivalMS)*time.Millisecond))
			frames++
		}
		if nowMS < 100 {
			continue
		}
		now := start.Add(time.Duration(nowMS) * time.Millisecond)
		truth := project(frameAt(float64(nowMS - 50)))
		direct, buffered := project(timeline.sample(now, 0)), project(timeline.sample(now, 35))
		distanceSquared := func(p Vec2) float64 {
			dx, dy := float64(p.X-truth.X), float64(p.Y-truth.Y)
			return dx*dx + dy*dy
		}
		directSquared += distanceSquared(direct)
		bufferedSquared += distanceSquared(buffered)
		samples++
	}
	directRMS := math.Sqrt(directSquared / float64(samples))
	bufferedRMS := math.Sqrt(bufferedSquared / float64(samples))
	t.Logf("synthetic 50 ms video delay, 30 Hz snapshots: immediate %.2f px RMS, buffered %.2f px RMS", directRMS, bufferedRMS)
	if bufferedRMS >= directRMS*0.3 {
		t.Fatalf("buffered replay failed to reduce synthetic alignment error: %.2f vs %.2f", bufferedRMS, directRMS)
	}
}

func TestRelayDisplayResetAndConcurrentSampling(t *testing.T) {
	_, data := loadGolden(t)
	relay := &Relay{Room: "test"}
	relay.consume(data)
	if relay.DisplaySnapshot(0).Frame != relay.Snapshot().Frame {
		t.Fatal("zero delay changed frame identity")
	}
	var work sync.WaitGroup
	work.Add(2)
	go func() {
		defer work.Done()
		for i := 1; i <= 200; i++ {
			binary.LittleEndian.PutUint32(data[8:12], uint32(i))
			binary.LittleEndian.PutUint64(data[12:20], uint64(1000+i*33))
			relay.consume(data)
		}
	}()
	go func() {
		defer work.Done()
		for i := 0; i < 200; i++ {
			relay.DisplaySnapshot(35)
			relay.Snapshot()
		}
	}()
	work.Wait()
	if err := relay.consumeControl([]byte(`{"t":"host_online"}`)); err != nil {
		t.Fatal(err)
	}
	if relay.DisplaySnapshot(250).Frame != nil || len(relay.timeline.frames) != 0 {
		t.Fatal("reconnect rendered a previous host frame")
	}
	relay.consume(data)
	if err := relay.consumeControl([]byte(`{"t":"welcome","role":"viewer","room":"test"}`)); err != nil {
		t.Fatal(err)
	}
	if relay.DisplaySnapshot(250).Frame != nil {
		t.Fatal("welcome retained buffered frames")
	}
}

func TestDisplaySyncSettingsCompatibility(t *testing.T) {
	for _, test := range []struct {
		data string
		want int
	}{
		{"", 35}, {"{}", 35}, {"null", 35}, {"invalid", 35},
		{`{"delay_ms":null}`, 35}, {`{"delay_ms":"50"}`, 35},
		{`{"delay_ms":0}`, 0}, {`{"delay_ms":100}`, 100},
		{`{"delay_ms":-20}`, 0}, {`{"delay_ms":500}`, 250},
	} {
		if got := decodeDisplaySyncSettings([]byte(test.data)).DelayMS; got != test.want {
			t.Errorf("%q gave %d, want %d", test.data, got, test.want)
		}
	}
}
