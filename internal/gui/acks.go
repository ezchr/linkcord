package gui

import (
	"log/slog"
	"time"

	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/utils/httputil"
	"github.com/ayn2op/ningen/v3"
)

// How channels are marked read, as Discord's web app marks them: a channel
// seen is marked 3 seconds later, once for all that arrived meanwhile, and
// several channels marked read together go in one request.

// ackDelay is how long a channel seen waits to be marked read, as in the
// web app.
const ackDelay = 3 * time.Second

// ackSoon marks the channel read after ackDelay, unless a mark is already
// on its way, which then covers what came since.
func (c *chatView) ackSoon(chID discord.ChannelID) {
	if c.demo {
		return
	}
	if c.ackTimers == nil {
		c.ackTimers = make(map[discord.ChannelID]*time.Timer)
	}
	if _, ok := c.ackTimers[chID]; ok {
		return
	}
	c.ackTimers[chID] = time.AfterFunc(ackDelay, func() {
		c.u.post(func() {
			delete(c.ackTimers, chID)
			if !c.closed {
				c.markRead(chID)
			}
		})
	})
}

// stopAcks drops the marks on their way, as when the session closes.
func (c *chatView) stopAcks() {
	for id, t := range c.ackTimers {
		t.Stop()
		delete(c.ackTimers, id)
	}
}

// bulkAck is a channel in a request that marks several read.
type bulkAck struct {
	ChannelID     discord.ChannelID `json:"channel_id"`
	MessageID     discord.MessageID `json:"message_id"`
	ReadStateType int               `json:"read_state_type"`
}

// bulkAcks are the channels of chs with unread messages, to be marked read
// at their last message, in requests of at most 100, as the web app sends
// them.
func bulkAcks(chs []discord.ChannelID, last func(discord.ChannelID) discord.MessageID, unread func(discord.ChannelID) bool) [][]bulkAck {
	var batches [][]bulkAck
	var cur []bulkAck
	for _, id := range chs {
		m := last(id)
		if !m.IsValid() || !unread(id) {
			continue
		}
		cur = append(cur, bulkAck{ChannelID: id, MessageID: m})
		if len(cur) == 100 {
			batches = append(batches, cur)
			cur = nil
		}
	}
	if len(cur) > 0 {
		batches = append(batches, cur)
	}
	return batches
}

// markManyRead marks the channels read in one request, or a few, as the web
// app does for a category or a server.
func (c *chatView) markManyRead(chs []discord.ChannelID) {
	if c.demo {
		return
	}
	batches := bulkAcks(chs, c.st.LastMessage, func(id discord.ChannelID) bool {
		return c.channelUnread(id) != ningen.ChannelRead
	})
	if len(batches) == 0 {
		return
	}
	go func() {
		for _, b := range batches {
			err := c.st.Client.FastRequest("POST", api.Endpoint+"read-states/ack-bulk",
				httputil.WithJSONBody(map[string]any{"read_states": b}))
			if err != nil {
				slog.Error("failed to mark channels read", "err", err, "channels", len(b))
			}
		}
	}()
}
