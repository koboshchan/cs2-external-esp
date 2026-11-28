package main

// Relay client: connects, authenticates as a viewer, and keeps the latest
// frame. Reconnects with backoff; never blocks the render loop.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type RelayStatus struct {
	Connected  bool
	HostOnline bool
	Room       string
	Message    string
}

type Relay struct {
	URL  string
	Room string
	Key  string

	mu       sync.RWMutex
	status   RelayStatus
	latest   *Frame
	latestAt time.Time
	timeline frameTimeline
	frames   uint64
	bytes    uint64
	gaps     uint64
	lastSeq  uint32
	haveSeq  bool
	lastErr  string
}

func (r *Relay) Run(ctx context.Context) {
	backoff := time.Second
	defer r.setStatus(func(s *RelayStatus) {
		s.Connected = false
		s.HostOnline = false
	})

	for {
		if ctx.Err() != nil {
			return
		}

		err := r.session(ctx)
		if ctx.Err() != nil {
			return
		}
		if r.Snapshot().Status.Connected {
			backoff = time.Second
		}

		r.setStatus(func(s *RelayStatus) {
			s.Connected = false
			s.HostOnline = false
			if err != nil {
				s.Message = err.Error()
			}
		})
		var rejected *relayRejection
		if errors.As(err, &rejected) {
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}

		backoff = min(backoff*2, 10*time.Second)
	}
}

func (r *Relay) session(ctx context.Context) error {
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	conn, _, err := dialer.DialContext(ctx, r.URL, nil)
	if err != nil {
		r.setError(err.Error())
		return err
	}
	defer conn.Close()
	conn.SetReadLimit(maxFrameBytes)
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	pingHandler := conn.PingHandler()
	conn.SetPingHandler(func(data string) error {
		conn.SetReadDeadline(time.Now().Add(15 * time.Second))
		return pingHandler(data)
	})

	hello := map[string]any{
		"t":     "hello",
		"role":  "viewer",
		"room":  r.Room,
		"token": r.Key,
		"proto": protoVersion,
	}
	conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if err := conn.WriteJSON(hello); err != nil {
		r.setError(err.Error())
		return err
	}

	r.setStatus(func(s *RelayStatus) {
		s.Connected = false
		s.Room = r.Room
		s.Message = "authenticating..."
	})

	// Closing the socket on ctx cancellation unblocks ReadMessage.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-done:
		}
	}()

	for {
		kind, data, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		conn.SetReadDeadline(time.Now().Add(15 * time.Second))

		if kind == websocket.BinaryMessage {
			if r.Snapshot().Status.Connected {
				r.consume(data)
			}
			continue
		}

		if err := r.consumeControl(data); err != nil {
			return err
		}
	}
}

func (r *Relay) consume(data []byte) {
	frame, err := ParseFrame(data)
	if err != nil {
		r.setError("bad frame: " + err.Error())
		return
	}

	r.mu.Lock()
	if r.haveSeq && frame.Seq != r.lastSeq+1 {
		r.gaps++
	}
	r.lastSeq = frame.Seq
	r.haveSeq = true
	r.frames++
	r.bytes += uint64(len(data))
	r.latest = frame
	r.latestAt = time.Now()
	r.timeline.append(frame, r.latestAt)
	r.lastErr = ""
	r.status.Message = ""
	r.mu.Unlock()
}

type relayRejection struct {
	message string
}

func (e *relayRejection) Error() string { return e.message }

func (r *Relay) consumeControl(data []byte) error {
	var msg struct {
		T          string `json:"t"`
		Role       string `json:"role"`
		Room       string `json:"room"`
		HostOnline bool   `json:"host_online"`
		Code       string `json:"code"`
		Message    string `json:"message"`
	}
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil
	}

	switch msg.T {
	case "welcome":
		if msg.Role != "viewer" || msg.Room != r.Room {
			return &relayRejection{message: "relay returned an unexpected room or role"}
		}
		r.mu.Lock()
		r.status = RelayStatus{Connected: true, Room: msg.Room, HostOnline: msg.HostOnline}
		r.lastErr = ""
		r.haveSeq = false
		r.latest = nil
		r.latestAt = time.Time{}
		r.timeline = frameTimeline{}
		r.mu.Unlock()
	case "host_online":
		r.mu.Lock()
		r.status.HostOnline = true
		r.haveSeq = false
		r.latest = nil
		r.latestAt = time.Time{}
		r.timeline = frameTimeline{}
		r.mu.Unlock()
	case "host_offline":
		r.setStatus(func(s *RelayStatus) { s.HostOnline = false })
	case "error":
		text := msg.Code
		if msg.Message != "" {
			text += ": " + msg.Message
		}
		r.setError(text)
		switch msg.Code {
		case "bad_token", "room_not_found", "proto_mismatch", "room_full":
			return &relayRejection{message: text}
		}
	}
	return nil
}

func (r *Relay) setStatus(fn func(*RelayStatus)) {
	r.mu.Lock()
	fn(&r.status)
	r.mu.Unlock()
}

func (r *Relay) setError(text string) {
	r.mu.Lock()
	r.lastErr = text
	r.status.Message = text
	r.mu.Unlock()
}

// StatusText describes the connection in plain words, for the launcher.
func StatusText(s RelaySnapshot, players int) string {
	if s.Status.Message != "" {
		return "relay: " + s.Status.Message
	}
	if !s.Status.Connected {
		return "connecting to relay..."
	}
	if !s.Status.HostOnline {
		return "relay ok - host offline (press Host in the ESP)"
	}
	if s.Frame == nil {
		return "host online - waiting for the first frame..."
	}
	if time.Since(s.FrameAt) > 3*time.Second {
		return "host online - data stalled, waiting for fresh frames..."
	}
	return fmt.Sprintf("host online - %d frames - %d players", s.Frames, players)
}

type RelaySnapshot struct {
	Status  RelayStatus
	Frame   *Frame
	FrameAt time.Time
	Frames  uint64
	Bytes   uint64
	Gaps    uint64
	LastErr string
}

func (r *Relay) Snapshot() RelaySnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.snapshotLocked()
}

// DisplaySnapshot samples all camera and player transforms at one timestamp.
// FrameAt and connection counters still describe the latest received packet.
func (r *Relay) DisplaySnapshot(delayMS int) RelaySnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	snapshot := r.snapshotLocked()
	if frame := r.timeline.sample(time.Now(), delayMS); frame != nil {
		snapshot.Frame = frame
	}
	return snapshot
}

func (r *Relay) snapshotLocked() RelaySnapshot {
	return RelaySnapshot{
		Status:  r.status,
		Frame:   r.latest,
		FrameAt: r.latestAt,
		Frames:  r.frames,
		Bytes:   r.bytes,
		Gaps:    r.gaps,
		LastErr: r.lastErr,
	}
}

var errNoRoom = errors.New("room is required")
