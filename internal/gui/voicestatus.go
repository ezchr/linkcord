package gui

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/gateway"
	"github.com/ayn2op/arikawa/v3/utils/ws"
	"golang.org/x/exp/shiny/materialdesign/icons"
)

// The status of voice channels, such as what is played in them, which
// Discord does not send with the channels: it is asked for when a server is
// opened, and sent again when it changes. Arikawa does not know these.
//
// https://docs.discord.food/topics/gateway-events#request-channel-info

var iconLock = mustIcon(icons.ActionLock)

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
		ID             discord.ChannelID `json:"id"`
		Status         string            `json:"status"`
		VoiceStartTime json.RawMessage   `json:"voice_start_time"`
	} `json:"channels"`
}

// voiceStartTimeEvent is sent when people come into an empty voice
// channel, or the last one leaves it.
type voiceStartTimeEvent struct {
	ID             discord.ChannelID `json:"id"`
	GuildID        discord.GuildID   `json:"guild_id"`
	VoiceStartTime json.RawMessage   `json:"voice_start_time"`
}

func (*voiceStartTimeEvent) Op() ws.OpCode           { return 0 }
func (*voiceStartTimeEvent) EventType() ws.EventType { return "VOICE_CHANNEL_START_TIME_UPDATE" }

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
		func() ws.Event { return new(voiceStartTimeEvent) },
	)
}

// requestVoiceStatus asks for the status of the voice channels of guildID.
func (c *chatView) requestVoiceStatus(guildID discord.GuildID) {
	if c.demo {
		c.demoVoiceStatus(guildID)
		return
	}
	if !guildID.IsValid() {
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

// setVoiceStart keeps when people came into the voice channel id, as
// Discord sends it, in seconds or milliseconds since 1970, or null once it
// is empty.
func (c *chatView) setVoiceStart(id discord.ChannelID, raw json.RawMessage) {
	if c.voiceStarts == nil {
		c.voiceStarts = make(map[discord.ChannelID]time.Time)
	}
	n, err := strconv.ParseInt(strings.Trim(string(raw), `"`), 10, 64)
	switch {
	case err != nil || n <= 0:
		delete(c.voiceStarts, id)
	case n > 1e12:
		c.voiceStarts[id] = time.UnixMilli(n)
	default:
		c.voiceStarts[id] = time.Unix(n, 0)
	}
}

// layoutVoiceTime is how long people have been in the voice channel id, in
// green, ticking every second.
func (c *chatView) layoutVoiceTime(gtx layout.Context, id discord.ChannelID) layout.Dimensions {
	start, ok := c.voiceStarts[id]
	if !ok {
		return layout.Dimensions{}
	}
	d := max(gtx.Now.Sub(start), 0)
	h, m, sec := int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60
	txt := fmt.Sprintf("%d:%02d", m, sec)
	if h > 0 {
		txt = fmt.Sprintf("%d:%02d:%02d", h, m, sec)
	}
	gtx.Execute(op.InvalidateCmd{At: gtx.Now.Truncate(time.Second).Add(time.Second)})
	return layout.Inset{Left: unit.Dp(4)}.Layout(gtx, label(c.u.th, 12, colorGreen, txt).Layout)
}

// privateChannel reports whether ch is hidden from everyone in its server
// but some, which Discord marks with a lock.
func privateChannel(ch discord.Channel) bool {
	if !ch.GuildID.IsValid() {
		return false
	}
	for _, o := range ch.Overwrites {
		if o.Type == discord.OverwriteRole && discord.Snowflake(o.ID) == discord.Snowflake(ch.GuildID) && o.Deny.Has(discord.PermissionViewChannel) {
			return true
		}
	}
	return false
}

// locked lays out the icon of a channel with a small lock at its top right
// if it is private.
func locked(gtx layout.Context, private bool, w layout.Widget) layout.Dimensions {
	dims := w(gtx)
	if !private {
		return dims
	}
	s := gtx.Dp(10)
	t := op.Offset(image.Pt(dims.Size.X-s+gtx.Dp(1), -gtx.Dp(1))).Push(gtx.Ops)
	icon(gtx, iconLock, 10, colorChannel)
	t.Pop()
	return dims
}

// watchVoiceStatus keeps the status of voice channels as Discord sends it.
func (c *chatView) watchVoiceStatus() func() {
	stopInfo := c.st.AddHandler(func(ev *channelInfoEvent) {
		c.u.post(func() {
			for _, ch := range ev.Channels {
				c.setVoiceStatus(ch.ID, ch.Status)
				c.setVoiceStart(ch.ID, ch.VoiceStartTime)
			}
		})
	})
	stopUpdate := c.st.AddHandler(func(ev *voiceChannelStatusEvent) {
		c.u.post(func() { c.setVoiceStatus(ev.ID, ev.Status) })
	})
	stopStart := c.st.AddHandler(func(ev *voiceStartTimeEvent) {
		c.u.post(func() { c.setVoiceStart(ev.ID, ev.VoiceStartTime) })
	})
	return func() {
		stopInfo()
		stopUpdate()
		stopStart()
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

// demoVoiceStatus gives the voice channels of the demo with people in them
// a status and an hour of talk.
func (c *chatView) demoVoiceStatus(guildID discord.GuildID) {
	states, _ := c.st.Cabinet.VoiceStates(guildID)
	for _, vs := range states {
		if _, ok := c.voiceStarts[vs.ChannelID]; !ok {
			c.setVoiceStatus(vs.ChannelID, "movie night 🍿")
			c.setVoiceStart(vs.ChannelID, json.RawMessage(strconv.FormatInt(time.Now().Add(-time.Hour-12*time.Minute).Unix(), 10)))
		}
	}
}
