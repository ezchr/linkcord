package gui

import (
	"context"
	"log/slog"
	"time"

	"github.com/ayn2op/arikawa/v3/utils/ws"
	lchttp "github.com/ezchr/linkcord/internal/http"
)

// timeSpentSessionCommand is op 41, UPDATE_TIME_SPENT_SESSION_ID, which the
// web client sends when the gateway session is established (CONNECTION_OPEN)
// and whenever its client_heartbeat_session_id is replaced, so the gateway
// knows which heartbeat session the time spent belongs to.
type timeSpentSessionCommand struct {
	InitializationTimestamp int64  `json:"initialization_timestamp"`
	SessionID               string `json:"session_id"`
	ClientLaunchID          string `json:"client_launch_id"`
}

func (*timeSpentSessionCommand) Op() ws.OpCode           { return 41 }
func (*timeSpentSessionCommand) EventType() ws.EventType { return "" }

// sendTimeSpentSession sends UPDATE_TIME_SPENT_SESSION_ID with the current
// heartbeat session.
func (c *chatView) sendTimeSpentSession() {
	// Sent whatever the analytics setting, as the web client always sends
	// it, with a heartbeat after it.
	if c.st == nil || c.demo {
		return
	}
	id, created := lchttp.HeartbeatSession()
	cmd := &timeSpentSessionCommand{
		InitializationTimestamp: created,
		SessionID:               id,
		ClientLaunchID:          lchttp.LaunchID(),
	}
	st := c.st
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := st.SendGateway(ctx, cmd); err != nil {
			slog.Warn("failed to send time spent session", "err", err)
			return
		}
		st.Gateway().Beat(ctx)
	}()
}

// loadExperiments loads the experiments again once logged in, as the web
// client does, which keeps the installation id Discord gives. Nothing is
// shown from them, so no experiment events are sent.
func (c *chatView) loadExperiments() {
	if c.st == nil {
		return
	}
	client := c.st.Client
	go func() {
		if _, err := lchttp.FetchExperiments(client); err != nil {
			slog.Warn("failed to load experiments", "err", err)
		}
	}()
}
