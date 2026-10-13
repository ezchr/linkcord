package gui

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"time"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/gateway"
)

func (c *chatView) runPendingRefresh() {
	if c.refreshPending && !c.loading && !c.loadingOlder {
		c.refreshPending = false
		c.refreshCurrentChat()
	}
}

func (c *chatView) refreshCurrentChat() {
	if c.closed || c.demo || c.st == nil || c.selChannel == nil {
		return
	}
	if c.loading || c.loadingOlder {
		c.refreshPending = true
		return
	}
	elapsed := time.Now().UnixMilli() - c.lastRefresh.UnixMilli()
	if c.refreshing || (elapsed >= 0 && elapsed < (5*time.Second).Milliseconds()) {
		return
	}
	id, epoch := c.selChannel.ID, c.historyEpoch
	var anchor discord.MessageID
	if len(c.messages) > 0 {
		anchor = c.messages[len(c.messages)-1].ID
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	c.refreshCancel = cancel
	c.refreshing = true
	c.refreshLive = make(map[discord.MessageID]*discord.Message)
	c.lastRefresh = time.Now()
	// Bypass State.Messages: its cached history can remain stale after sleep.
	client := c.st.Client.WithContext(ctx)
	go func() {
		defer cancel()
		messages, complete, err := fetchChatCatchup(
			func(before discord.MessageID) ([]discord.Message, error) {
				if before == 0 {
					return client.Messages(id, 100)
				}
				return client.MessagesBefore(id, before, 100)
			}, anchor)
		c.u.post(func() {
			if c.closed || c.u.chat != c || c.historyEpoch != epoch || c.selChannel == nil || c.selChannel.ID != id {
				return
			}
			c.refreshing, c.refreshCancel = false, nil
			if err != nil {
				c.refreshLive = nil
				slog.Warn("could not refresh chat after resume", "channel", id, "err", err)
				c.refreshFailures++
				if c.refreshFailures <= 3 {
					time.AfterFunc(5*time.Second, func() {
						c.u.post(func() {
							if !c.closed && c.u.chat == c && c.historyEpoch == epoch {
								c.refreshCurrentChat()
							}
						})
					})
				}
				return
			}
			c.refreshFailures = 0
			var visible discord.MessageID
			pos := c.messageList.Position
			if pos.BeforeEnd && pos.First > 0 && pos.First <= len(c.messages) {
				visible = c.messages[pos.First-1].ID
			}
			c.messages = reconcileChatHistory(c.messages, messages, c.refreshLive, complete)
			c.refreshLive = nil
			c.loadErr = ""
			if complete {
				c.noOlder = true
			}
			clear(c.selectables)
			clear(c.richStates)
			if visible != 0 {
				for i, message := range c.messages {
					if message.ID == visible {
						c.messageList.Position.First = i + 1
						break
					}
				}
			}
			c.requestAuthors(messages)
			if c.u.focused && len(c.messages) > 0 {
				last := c.messages[len(c.messages)-1].ID
				go c.st.ReadState.MarkRead(id, last)
			}
		})
	}()
}

// Fetch every page between the newest messages and our previous newest message.
// A long suspension must not leave a hole when more than 100 messages arrived.
func fetchChatCatchup(fetch func(discord.MessageID) ([]discord.Message, error), anchor discord.MessageID) ([]discord.Message, bool, error) {
	var result []discord.Message
	var before discord.MessageID
	for {
		page, err := fetch(before)
		if err != nil {
			return nil, false, err
		}
		page = sortedMessages(page)
		result = append(result, page...)
		if len(page) < 100 {
			return sortedMessages(result), true, nil
		}
		oldest := page[0].ID
		if anchor == 0 || oldest <= anchor {
			return sortedMessages(result), false, nil
		}
		if before != 0 && oldest >= before {
			return nil, false, errors.New("message pagination did not advance")
		}
		before = oldest
	}
}

// REST owns the fetched range; gateway changes received during the request win.
func reconcileChatHistory(current, fetched []discord.Message, live map[discord.MessageID]*discord.Message, complete bool) []discord.Message {
	messages := make(map[discord.MessageID]discord.Message)
	if !complete && len(fetched) > 0 {
		oldest := fetched[0].ID
		for _, message := range current {
			if message.ID < oldest {
				messages[message.ID] = message
			}
		}
	}
	for _, message := range fetched {
		messages[message.ID] = message
	}
	for id, message := range live {
		if message == nil {
			delete(messages, id)
		} else {
			messages[id] = *message
		}
	}
	result := make([]discord.Message, 0, len(messages))
	for _, message := range messages {
		result = append(result, message)
	}
	return sortedMessages(result)
}

func (c *chatView) recordRefreshEvent(event gateway.Event) {
	if !c.refreshing || c.selChannel == nil {
		return
	}
	var channel discord.ChannelID
	var id discord.MessageID
	switch event := event.(type) {
	case *gateway.MessageCreateEvent:
		channel, id = event.ChannelID, event.ID
	case *gateway.MessageUpdateEvent:
		channel, id = event.ChannelID, event.ID
	case *gateway.MessageDeleteEvent:
		channel, id = event.ChannelID, event.ID
	case *gateway.MessageReactionAddEvent:
		channel, id = event.ChannelID, event.MessageID
	case *gateway.MessageReactionRemoveEvent:
		channel, id = event.ChannelID, event.MessageID
	case *gateway.MessageReactionRemoveAllEvent:
		channel, id = event.ChannelID, event.MessageID
	case *gateway.MessageReactionRemoveEmojiEvent:
		channel, id = event.ChannelID, event.MessageID
	default:
		return
	}
	if channel != c.selChannel.ID {
		return
	}
	c.refreshLive[id] = nil
	if index := slices.IndexFunc(c.messages, func(message discord.Message) bool { return message.ID == id }); index >= 0 {
		message := c.messages[index]
		c.refreshLive[id] = &message
	}
}
