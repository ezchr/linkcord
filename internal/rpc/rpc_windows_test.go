package rpc

import (
	"encoding/json"
	"os"
	"testing"
)

// TestPipe connects to the pipe served as a game does, and is greeted.
func TestPipe(t *testing.T) {
	name, err := Start(Handler{
		User:     func() any { return map[string]string{"id": "1"} },
		Activity: func(string, string, json.RawMessage) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Log("serving", name)
	game, err := os.OpenFile(name, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer game.Close()
	if err := writeFrame(game, opHandshake, map[string]any{"v": 1, "client_id": "123"}); err != nil {
		t.Fatal(err)
	}
	op, data, err := readFrame(game)
	if err != nil || op != opFrame {
		t.Fatal(op, err)
	}
	var ready struct{ Evt string }
	json.Unmarshal(data, &ready)
	if ready.Evt != "READY" {
		t.Fatalf("answered %s", data)
	}
}
