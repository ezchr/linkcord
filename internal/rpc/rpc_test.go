package rpc

import (
	"encoding/json"
	"net"
	"testing"
	"time"
)

// TestRichPresence talks to the server as a game does: a handshake, an
// activity set, and the game closing.
func TestRichPresence(t *testing.T) {
	server, game := net.Pipe()
	got := make(chan json.RawMessage, 4)
	go serve(server, Handler{
		User: func() any { return map[string]string{"id": "1", "username": "me"} },
		Activity: func(conn, clientID string, a json.RawMessage) {
			if clientID != "123" {
				t.Errorf("client %q", clientID)
			}
			got <- a
		},
	})
	game.SetDeadline(time.Now().Add(5 * time.Second))

	writeFrame(game, opHandshake, map[string]any{"v": 1, "client_id": "123"})
	op, data, err := readFrame(game)
	if err != nil || op != opFrame {
		t.Fatal(op, err)
	}
	var ready struct{ Evt string }
	json.Unmarshal(data, &ready)
	if ready.Evt != "READY" {
		t.Fatalf("answered %s", data)
	}

	writeFrame(game, opFrame, map[string]any{
		"cmd": "SET_ACTIVITY", "nonce": "n1",
		"args": map[string]any{"pid": 42, "activity": map[string]any{"details": "In a match"}},
	})
	if a := <-got; a == nil {
		t.Fatal("no activity")
	}
	if _, data, err = readFrame(game); err != nil {
		t.Fatal(err)
	}
	var reply struct{ Cmd, Nonce string }
	json.Unmarshal(data, &reply)
	if reply.Cmd != "SET_ACTIVITY" || reply.Nonce != "n1" {
		t.Fatalf("answered %s", data)
	}

	game.Close()
	select {
	case a := <-got:
		if a != nil {
			t.Fatal("the activity stayed once the game closed")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the activity was not cleared")
	}
}
