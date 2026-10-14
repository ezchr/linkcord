// Package rpc is the local Discord RPC that games and programs talk to, to
// show what the user does in them as Rich Presence: they connect to the
// pipe \\.\pipe\discord-ipc-N, as they would to Discord's own app, and set
// their activity, which Linkcord then shows on the user's profile.
//
// Only what Rich Presence needs is answered: the handshake, SET_ACTIVITY,
// and pings; other commands get an error.
//
// https://github.com/discord/discord-rpc/blob/master/documentation/hard-mode.md
package rpc

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync/atomic"
)

// The opcodes of the frames of the pipe.
const (
	opHandshake = 0
	opFrame     = 1
	opClose     = 2
	opPing      = 3
	opPong      = 4
)

// maxFrame is more than a frame can sensibly weigh.
const maxFrame = 64 << 10

// Handler is what the server does with what programs ask.
type Handler struct {
	// User is the user, as Discord's API gives it, for the handshake's
	// answer.
	User func() any
	// Activity is called when the program connection conn, of the
	// application clientID, sets its activity, nil once it clears it or
	// disconnects. It is called from the connection's goroutine.
	Activity func(conn, clientID string, activity json.RawMessage)
}

var conns atomic.Int64

// serve talks to a program connected over rw until it disconnects.
func serve(rw io.ReadWriteCloser, h Handler) {
	defer rw.Close()
	id := fmt.Sprint(conns.Add(1))
	clientID := ""
	set := false
	defer func() {
		if set {
			h.Activity(id, clientID, nil)
		}
	}()
	for {
		op, data, err := readFrame(rw)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				slog.Debug("rpc connection ended", "err", err)
			}
			return
		}
		switch op {
		case opHandshake:
			var hs struct {
				V        int    `json:"v"`
				ClientID string `json:"client_id"`
			}
			if json.Unmarshal(data, &hs) != nil || hs.ClientID == "" {
				writeFrame(rw, opClose, map[string]any{"code": 4000, "message": "Invalid Client ID"})
				return
			}
			clientID = hs.ClientID
			slog.Info("rpc program connected", "client_id", clientID)
			writeFrame(rw, opFrame, map[string]any{
				"cmd": "DISPATCH", "evt": "READY", "nonce": nil,
				"data": map[string]any{
					"v": 1,
					"config": map[string]any{
						"cdn_host": "cdn.discordapp.com", "api_endpoint": "//discord.com/api", "environment": "production",
					},
					"user": h.User(),
				},
			})
		case opFrame:
			var cmd struct {
				Cmd   string `json:"cmd"`
				Nonce any    `json:"nonce"`
				Args  struct {
					PID      int             `json:"pid"`
					Activity json.RawMessage `json:"activity"`
				} `json:"args"`
			}
			if err := json.Unmarshal(data, &cmd); err != nil {
				return
			}
			if cmd.Cmd != "SET_ACTIVITY" || clientID == "" {
				writeFrame(rw, opFrame, map[string]any{
					"cmd": cmd.Cmd, "evt": "ERROR", "nonce": cmd.Nonce,
					"data": map[string]any{"code": 4000, "message": "Linkcord only shows Rich Presence"},
				})
				continue
			}
			act := cmd.Args.Activity
			if len(act) == 0 || string(act) == "null" {
				act = nil
			}
			set = act != nil
			h.Activity(id, clientID, act)
			reply := json.RawMessage("null")
			if act != nil {
				reply = act
			}
			writeFrame(rw, opFrame, map[string]any{"cmd": "SET_ACTIVITY", "evt": nil, "nonce": cmd.Nonce, "data": reply})
		case opPing:
			writeRaw(rw, opPong, data)
		case opClose:
			return
		}
	}
}

func readFrame(r io.Reader) (op uint32, data []byte, err error) {
	var head [8]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return 0, nil, err
	}
	op, n := binary.LittleEndian.Uint32(head[:]), binary.LittleEndian.Uint32(head[4:])
	if n > maxFrame {
		return 0, nil, fmt.Errorf("rpc: frame of %d bytes", n)
	}
	data = make([]byte, n)
	_, err = io.ReadFull(r, data)
	return op, data, err
}

func writeFrame(w io.Writer, op uint32, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return writeRaw(w, op, data)
}

func writeRaw(w io.Writer, op uint32, data []byte) error {
	frame := make([]byte, 8+len(data))
	binary.LittleEndian.PutUint32(frame, op)
	binary.LittleEndian.PutUint32(frame[4:], uint32(len(data)))
	copy(frame[8:], data)
	_, err := w.Write(frame)
	return err
}
