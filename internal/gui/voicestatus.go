package gui

import (
	"context"
	"log/slog"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/gateway"
	"github.com/ayn2op/arikawa/v3/utils/ws"
)

// The status of voice channels, such as what is played in them, which
// Discord does not send with the channels: it is asked for when a server is
// opened, and sent again when it changes. Arikawa does not know these.
//
// https://docs.discord.food/topics/gateway-events#request-channel-info

// requestChannelInfoCommand asks for the status of the voice channels of a
// server, which a channelInfoEvent answers.
type requestChannelInfoCommand struct {
	GuildID discord.GuildID `json:"guild_id"`
	Fields  []string        `json:"fields"`
}

func (*requestChannelInfoCommand) Op() ws.OpCode           { return 43 }
func (*requestChannelInfoCommand) EventType() ws.EventType { return "" }

// channelInfoEvent is the status of the voice channels of a server.
type channelInfoEvent struct {
	GuildID  discord.GuildID `json:"guild_id"`
	Channels []struct {
		ID     discord.ChannelID `json:"id"`
		Status string            `json:"status"`
	} `json:"channels"`
}

// voiceChannelStatusEvent is sent when the status of a voice channel
// changes.
type voiceChannelStatusEvent struct {
	ID      discord.ChannelID `json:"id"`
	GuildID discord.GuildID   `json:"guild_id"`
	Status  string            `json:"status"`
}

func (*channelInfoEvent) Op() ws.OpCode                  { return 0 }
func (*channelInfoEvent) EventType() ws.EventType        { return "CHANNEL_INFO" }
func (*voiceChannelStatusEvent) Op() ws.OpCode           { return 0 }
func (*voiceChannelStatusEvent) EventType() ws.EventType { return "VOICE_CHANNEL_STATUS_UPDATE" }

func init() {
	// Before any gateway is made, as each copies the registry.
	gateway.OpUnmarshalers.Add(
		func() ws.Event { return new(channelInfoEvent) },
		func() ws.Event { return new(voiceChannelStatusEvent) },
	)
}

// requestVoiceStatus asks for the status of the voice channels of guildID.
func (c *chatView) requestVoiceStatus(guildID discord.GuildID) {
	if c.demo || !guildID.IsValid() {
		return
	}
	go func() {
		err := c.st.SendGateway(context.Background(), &requestChannelInfoCommand{
			GuildID: guildID,
			Fields:  []string{"status", "voice_start_time"},
		})
		if err != nil {
			slog.Warn("could not ask for the status of voice channels", "err", err)
		}
	}()
}

// setVoiceStatus keeps the status of the voice channel id, shown under its
// name.
func (c *chatView) setVoiceStatus(id discord.ChannelID, status string) {
	if c.voiceStatuses == nil {
		c.voiceStatuses = make(map[discord.ChannelID]string)
	}
	if status == "" {
		delete(c.voiceStatuses, id)
	} else {
		c.voiceStatuses[id] = status
	}
}

// watchVoiceStatus keeps the status of voice channels as Discord sends it.
func (c *chatView) watchVoiceStatus() func() {
	stopInfo := c.st.AddHandler(func(ev *channelInfoEvent) {
		c.u.post(func() {
			for _, ch := range ev.Channels {
				c.setVoiceStatus(ch.ID, ch.Status)
			}
		})
	})
	stopUpdate := c.st.AddHandler(func(ev *voiceChannelStatusEvent) {
		c.u.post(func() { c.setVoiceStatus(ev.ID, ev.Status) })
	})
	return func() {
		stopInfo()
		stopUpdate()
	}
}

// openVoiceChannelMenu opens the menu of the voice channel id at the
// pointer: its chat, joining or leaving it, and its link.
func (c *chatView) openVoiceChannelMenu(id discord.ChannelID) {
	ch, err := c.st.Cabinet.Channel(id)
	if err != nil {
		return
	}
	channel := *ch
	c.openMenu(c.pointerAt, 220, func() []menuItem {
		items := []menuItem{{text: "Open Chat", act: func() { c.selectChannel(channel) }}}
		if c.u.voiceChannel() == channel.ID {
			items = append(items, menuItem{text: "Disconnect", col: colorRed, act: c.u.leaveVoice})
		} else {
			items = append(items, menuItem{text: "Join Voice", act: func() {
				c.joinVoice(channel)
				c.openCall(channel)
			}})
		}
		link := "https://discord.com/channels/" + channel.GuildID.String() + "/" + channel.ID.String()
		return append(items,
			menuItem{sep: true},
			menuItem{text: "Copy Link", act: func() { c.copyText = link }},
			menuItem{text: "Copy Channel ID", act: func() { c.copyText = channel.ID.String() }},
		)
	})
}
