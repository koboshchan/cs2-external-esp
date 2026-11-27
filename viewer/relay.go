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

	mu          sync.RWMutex
	status      RelayStatus
	latest      *Frame
	latestAt    time.Time
	frames      uint64
	bytes       uint64
	gaps        uint64
	lastSeq     uint32
	haveSeq     bool
	lastErr     string
}

func (r *Relay) Run(ctx context.Context) {
	backoff := time.Second

	for {
		if ctx.Err() != nil {
			return
		}

		err := r.session(ctx)
		if ctx.Err() != nil {
			return
		}

		r.setStatus(func(s *RelayStatus) {
			s.Connected = false
			s.HostOnline = false
			if err != nil {
				s.Message = err.Error()
			}
		})

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}

		if backoff < 10*time.Second {
			backoff *= 2
		}
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

	hello := map[string]any{
		"t":     "hello",
		"role":  "viewer",
		"room":  r.Room,
		"token": r.Key,
		"proto": protoVersion,
	}
	if err := conn.WriteJSON(hello); err != nil {
		r.setError(err.Error())
		return err
	}

	r.setStatus(func(s *RelayStatus) {
		s.Connected = true
		s.Room = r.Room
		s.Message = ""
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
			r.setError(err.Error())
			return err
		}

		if kind == websocket.BinaryMessage {
			r.consume(data)
			continue
		}

		r.consumeControl(data)
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
	r.mu.Unlock()
}

func (r *Relay) consumeControl(data []byte) {
	var msg struct {
		T          string `json:"t"`
		Role       string `json:"role"`
		Room       string `json:"room"`
		HostOnline bool   `json:"host_online"`
		Code       string `json:"code"`
		Message    string `json:"message"`
	}
	if err := json.Unmarshal(data, &msg); err != nil {
		return
	}

	switch msg.T {
	case "welcome":
		r.setStatus(func(s *RelayStatus) {
			s.Connected = true
			s.Room = msg.Room
			s.HostOnline = msg.HostOnline
			s.Message = ""
		})
	case "host_online":
		r.setStatus(func(s *RelayStatus) { s.HostOnline = true })
	case "host_offline":
		r.setStatus(func(s *RelayStatus) { s.HostOnline = false })
	case "error":
		text := msg.Code
		if msg.Message != "" {
			text += ": " + msg.Message
		}
		r.setError(text)
	}
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
	if s.Status.Message != "" && !s.Status.Connected {
		return "relay: " + s.Status.Message
	}
	if !s.Status.Connected {
		return "connecting to relay..."
	}
	if !s.Status.HostOnline {
		return "relay ok - host offline (press Host in the ESP)"
	}
	if s.Frames == 0 {
		return "host online - waiting for the first frame..."
	}
	return fmt.Sprintf("host online - %d frames - %d players", s.Frames, players)
}

type RelaySnapshot struct {
	Status   RelayStatus
	Frame    *Frame
	FrameAt  time.Time
	Frames   uint64
	Bytes    uint64
	Gaps     uint64
	LastErr  string
}

func (r *Relay) Snapshot() RelaySnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
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
