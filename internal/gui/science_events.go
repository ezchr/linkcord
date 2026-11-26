package gui

import (
	"context"
	"strconv"
	"time"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/utils/ws"
)

// The analytics events Linkcord sends, where the web client sends them,
// with the properties it gives them (client study, docs 05, 1.5d and 1.7).

// guildProps are trackWithMetadata's guild properties.
func (c *chatView) guildProps(guildID discord.GuildID, p map[string]any) {
	if !guildID.IsValid() {
		return
	}
	p["guild_id"] = guildID.String()
	g, gerr := c.st.Cabinet.Guild(guildID)
	if gerr == nil {
		p["guild_size_total"] = g.ApproximateMembers
		p["guild_num_roles"] = len(g.Roles)
		vip := false
		for _, f := range g.Features {
			if f == discord.VIPRegions {
				vip = true
			}
		}
		p["guild_is_vip"] = vip
	}
	if chs, err := c.st.Cabinet.Channels(guildID); err == nil {
		text, voice := 0, 0
		for _, ch := range chs {
			switch ch.Type {
			case discord.GuildText, discord.GuildAnnouncement, discord.GuildForum:
				text++
			case discord.GuildVoice, discord.GuildStageVoice:
				voice++
			}
		}
		p["guild_num_channels"] = len(chs)
		p["guild_num_text_channels"] = text
		p["guild_num_voice_channels"] = voice
	}
	m, merr := c.st.Cabinet.Member(guildID, c.me.ID)
	p["is_member"] = merr == nil
	if merr == nil {
		p["guild_member_num_roles"] = len(m.RoleIDs)
		if gerr == nil {
			if perms := c.guildPerms(guildID); perms != 0 {
				p["guild_member_perms"] = strconv.FormatUint(uint64(perms), 10)
			}
		}
	}
}

// channelProps are trackWithMetadata's channel properties.
func (c *chatView) channelProps(ch discord.Channel, p map[string]any) {
	p["channel_id"] = ch.ID.String()
	p["channel_type"] = int(ch.Type)
	if perms, ok := c.cachedPerms(ch.ID); ok {
		p["channel_member_perms"] = strconv.FormatUint(uint64(perms), 10)
		p["channel_hidden"] = !perms.Has(discord.PermissionViewChannel)
	}
	if !ch.GuildID.IsValid() {
		p["channel_size_total"] = len(ch.DMRecipients) + 1
	}
	c.guildProps(ch.GuildID, p)
}

func (c *chatView) trackChannelOpened(ch discord.Channel) {
	if c.sci == nil {
		return
	}
	p := map[string]any{}
	c.channelProps(ch, p)
	c.sci.track("channel_opened", p, "channel_opened:"+ch.ID.String(), 15*time.Minute)
	if ch.GuildID.IsValid() {
		g := map[string]any{"is_pending": false, "preview_enabled": false}
		c.guildProps(ch.GuildID, g)
		c.sci.track("guild_viewed", g, "guild_viewed:"+ch.GuildID.String(), 15*time.Minute)
	}
}

func (c *chatView) trackAck(chID discord.ChannelID) {
	if c.sci == nil {
		return
	}
	p := map[string]any{"channel_id": chID.String(), "location": "Channel"}
	if ch, err := c.st.Cabinet.Channel(chID); err == nil && ch.GuildID.IsValid() {
		p["guild_id"] = ch.GuildID.String()
	}
	c.sci.track("ack_messages", p, "ack_messages:"+chID.String(), 15*time.Minute)
}

func (c *chatView) trackMemberList() {
	if c.sci == nil || c.selChannel == nil {
		return
	}
	ch := c.selChannel
	p := map[string]any{"channel_id": ch.ID.String(), "channel_type": int(ch.Type)}
	if ch.GuildID.IsValid() {
		p["guild_id"] = ch.GuildID.String()
	}
	c.sci.track("member_list_viewed", p, "member_list_viewed:"+ch.ID.String(), 15*time.Minute)
}

func (c *chatView) trackSettingsPane(dest string) {
	if c.sci != nil {
		c.sci.track("settings_pane_viewed", map[string]any{"settings_type": "user", "destination_pane": dest}, "", 0)
	}
}

func (c *chatView) trackJoinVoice(ch discord.Channel) {
	if c.sci == nil {
		return
	}
	p := map[string]any{"channel_bitrate": ch.VoiceBitrate, "was_moved": false}
	c.channelProps(ch, p)
	c.sci.track("join_voice_channel", p, "", 0)
}

func (c *chatView) trackVoiceDisconnect(chID discord.ChannelID) {
	if c.sci == nil || !chID.IsValid() {
		return
	}
	c.sci.track("voice_disconnect", map[string]any{"channel_id": chID.String()}, "", 0)
}

func (c *chatView) trackReady(guilds, channels int) {
	if c.sci != nil {
		c.sci.track("ready_payload_received", map[string]any{"num_guilds": guilds, "num_guild_channels": channels}, "", 0)
	}
}

// qosCommand is op 40, QOS_HEARTBEAT, which the web client sends with each
// session heartbeat.
type qosCommand struct {
	Seq int `json:"seq"`
	QoS struct {
		Active  bool     `json:"active"`
		Ver     int      `json:"ver"`
		Reasons []string `json:"reasons"`
	} `json:"qos"`
}

func (*qosCommand) Op() ws.OpCode           { return 40 }
func (*qosCommand) EventType() ws.EventType { return "" }

// watchHeartbeat sends client_heartbeat and QOS_HEARTBEAT every 15 minutes
// while the client is active, foregrounded or in a call, as the web
// client's session heartbeat scheduler does.
func (c *chatView) watchHeartbeat() func() {
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(15 * time.Minute)
		defer t.Stop()
		seq := 0
		for {
			select {
			case <-stop:
				return
			case <-t.C:
			}
			c.u.post(func() {
				if c.closed || c.sci == nil {
					return
				}
				var reasons []string
				if c.u.focused {
					reasons = append(reasons, "foregrounded")
				}
				if c.u.voiceChannel().IsValid() {
					reasons = append(reasons, "rtc_connected")
				}
				if len(reasons) == 0 {
					return
				}
				c.sci.track("client_heartbeat", map[string]any{
					"client_heartbeat_version": 31,
					"is_idle":                  c.idle,
					"idle_duration_ms":         systemIdle().Milliseconds(),
					"is_afk":                   c.idle,
					"is_system_suspended":      false,
					"is_system_locked":         false,
					"uses_client_mods":         false,
				}, "", 0)
				if c.u.app.NoTelemetry {
					return
				}
				seq++
				q := &qosCommand{Seq: seq}
				q.QoS.Active, q.QoS.Ver, q.QoS.Reasons = true, 31, reasons
				st := c.st
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					st.SendGateway(ctx, q)
				}()
			})
		}
	}()
	return func() { close(stop) }
}
