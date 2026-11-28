package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestRelayWaitsForWelcome(t *testing.T) {
	helloReceived := make(chan struct{})
	allowWelcome := make(chan struct{})
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		conn, err := upgrader.Upgrade(w, req, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		close(helloReceived)
		<-allowWelcome
		conn.WriteJSON(map[string]any{"t": "welcome", "role": "viewer", "room": "ABC234", "host_online": true})
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	relay := &Relay{URL: "ws" + strings.TrimPrefix(server.URL, "http"), Room: "ABC234", Key: "test"}
	done := make(chan struct{})
	go func() { relay.Run(ctx); close(done) }()
	select {
	case <-helloReceived:
	case <-time.After(3 * time.Second):
		t.Fatal("hello timed out")
	}
	if relay.Snapshot().Status.Connected {
		t.Error("connected before welcome")
	}
	close(allowWelcome)
	deadline := time.Now().Add(3 * time.Second)
	for !relay.Snapshot().Status.Connected && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !relay.Snapshot().Status.Connected {
		t.Error("welcome was not accepted")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancellation did not stop the reader")
	}
	if relay.Snapshot().Status.Connected {
		t.Error("connection still marked active after cancellation")
	}
}

func TestRelayRejectsInvalidKeyWithoutRetry(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		conn, err := upgrader.Upgrade(w, req, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		conn.ReadMessage()
		conn.WriteJSON(map[string]string{"t": "error", "code": "bad_token", "message": "invalid viewer key"})
	}))
	defer server.Close()
	relay := &Relay{URL: "ws" + strings.TrimPrefix(server.URL, "http"), Room: "ABC234", Key: "wrong"}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { relay.Run(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("invalid key was retried")
	}
	snap := relay.Snapshot()
	if snap.Status.Connected || !strings.Contains(snap.Status.Message, "bad_token") {
		t.Fatalf("unexpected rejection status %+v", snap.Status)
	}
}

func TestRelayHostRestartResetsSequence(t *testing.T) {
	_, data := loadGolden(t)
	relay := &Relay{Room: "ABC234"}
	welcome := []byte(`{"t":"welcome","role":"viewer","room":"ABC234","host_online":true}`)
	if err := relay.consumeControl(welcome); err != nil {
		t.Fatal(err)
	}
	relay.consume(data)
	if err := relay.consumeControl([]byte(`{"t":"host_online"}`)); err != nil {
		t.Fatal(err)
	}
	if relay.Snapshot().Frame != nil {
		t.Fatal("old host frame retained after restart")
	}
	binary.LittleEndian.PutUint32(data[8:12], 1)
	relay.consume(data)
	if relay.Snapshot().Gaps != 0 {
		t.Fatal("host restart counted as a sequence gap")
	}
	relay.setError("old connection error")
	if err := relay.consumeControl(welcome); err != nil {
		t.Fatal(err)
	}
	if relay.Snapshot().LastErr != "" {
		t.Fatal("old error retained after welcome")
	}
}

func TestRelayRejectsUnexpectedWelcome(t *testing.T) {
	relay := &Relay{Room: "ABC234"}
	msg, _ := json.Marshal(map[string]string{"t": "welcome", "role": "host", "room": "ABC234"})
	if err := relay.consumeControl(msg); err == nil {
		t.Fatal("accepted incorrect role")
	}
	if relay.Snapshot().Status.Connected {
		t.Fatal("incorrect role marked connected")
	}
}

func TestNormalizeServerWhitespace(t *testing.T) {
	for raw, want := range map[string]string{"  http://127.0.0.1:8080/\n": "ws://127.0.0.1:8080/ws", "  ": defaultServer, " example.org ": "wss://example.org/ws"} {
		if got := normalizeWSURL(raw); got != want {
			t.Errorf("normalizeWSURL(%q)=%q, want %q", raw, got, want)
		}
	}
}
