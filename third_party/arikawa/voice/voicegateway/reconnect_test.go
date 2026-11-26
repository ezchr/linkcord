package voicegateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ayn2op/arikawa/v3/utils/ws"
	"github.com/gorilla/websocket"
)

// fakeVoiceServer answers each connection with HELLO, then READY to an
// identify or RESUMED to a resume, and closes it with the next code of
// closes, if any: what a voice server that drops the connection does.
type fakeVoiceServer struct {
	ops    chan int // the first op of each connection: 0 identify, 7 resume
	closes []int
}

func (f *fakeVoiceServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer c.Close()
	c.WriteMessage(websocket.TextMessage, []byte(`{"op":8,"d":{"heartbeat_interval":60000}}`))
	for {
		_, b, err := c.ReadMessage()
		if err != nil {
			return
		}
		var op struct {
			Op int `json:"op"`
		}
		json.Unmarshal(b, &op)
		switch op.Op {
		case 0:
			c.WriteMessage(websocket.TextMessage, []byte(`{"op":2,"d":{"ssrc":1,"ip":"127.0.0.1","port":1,"modes":["aead_aes256_gcm_rtpsize"]}}`))
		case 7:
			c.WriteMessage(websocket.TextMessage, []byte(`{"op":9,"d":null}`))
		default:
			continue
		}
		f.ops <- op.Op
		if len(f.closes) > 0 {
			code := f.closes[0]
			f.closes = f.closes[1:]
			c.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(code, "test close"))
			// Wait for the client's answer to the close.
			c.SetReadDeadline(time.Now().Add(2 * time.Second))
			c.ReadMessage()
			return
		}
	}
}

func testGateway(t *testing.T, f *fakeVoiceServer) (*Gateway, <-chan ws.Op, context.CancelFunc) {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	g := New(State{UserID: 1, GuildID: 2, ChannelID: 3, SessionID: "s", Token: "t", Endpoint: "x"}, nil)
	g.gateway.SetAddr("ws" + strings.TrimPrefix(srv.URL, "http") + "/?v=" + Version)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return g, g.Connect(ctx), cancel
}

func nextOp(t *testing.T, f *fakeVoiceServer) int {
	t.Helper()
	select {
	case op := <-f.ops:
		return op
	case <-time.After(20 * time.Second):
		t.Fatal("the gateway did not connect again")
		return -1
	}
}

// TestResumeAfterClose checks that a close that can be come back from,
// such as 4015 (the voice server crashed) or 1006 (the connection dropped),
// has the gateway connect again and resume, rather than sit on a dead
// connection.
func TestResumeAfterClose(t *testing.T) {
	ws.WSError = func(error) {}
	f := &fakeVoiceServer{ops: make(chan int, 4), closes: []int{4015}}
	_, ch, _ := testGateway(t, f)
	go func() {
		for range ch {
		}
	}()

	if op := nextOp(t, f); op != 0 {
		t.Fatalf("first connection sent op %d, want identify", op)
	}
	if op := nextOp(t, f); op != 7 {
		t.Fatalf("connection after the close sent op %d, want resume", op)
	}
}

// TestFatalCloseStops checks that a close the session cannot come back
// from, such as 4014 (disconnected), ends the gateway with the close code
// as its last error, for the session to tell its user.
func TestFatalCloseStops(t *testing.T) {
	ws.WSError = func(error) {}
	f := &fakeVoiceServer{ops: make(chan int, 4), closes: []int{4014}}
	g, ch, _ := testGateway(t, f)

	nextOp(t, f)
	deadline := time.After(10 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if ok {
				continue
			}
			if g.Running() {
				t.Fatal("gateway still running after its channel closed")
			}
			var ce *ws.CloseEvent
			if err := g.LastError(); !errors.As(err, &ce) || ce.Code != 4014 {
				t.Fatalf("last error %v, want close code 4014", err)
			}
			return
		case <-deadline:
			t.Fatal("gateway did not stop on 4014")
		}
	}
}
