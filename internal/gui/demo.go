package gui

import (
	"fmt"
	"time"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/gateway"
)

// demoEnvVar shows made-up servers, people and messages without connecting
// to Discord, to check the interface without an account.
const demoEnvVar = "LINKCORD_DEMO"

// startDemo fills a state that never connects with made-up data and shows it.
func (u *ui) startDemo() {
	st := newState("demo")
	cab := st.Cabinet
	id := func(n int) discord.Snowflake {
		return discord.NewSnowflake(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(n) * time.Minute))
	}

	me := discord.User{ID: discord.UserID(id(1)), Discriminator: "0", Username: "demo_user", DisplayName: "You (demo)"}
	cab.MyselfSet(me, false)
	users := []discord.User{
		{ID: discord.UserID(id(2)), Discriminator: "0", Username: "ana", DisplayName: "Ana"},
		{ID: discord.UserID(id(3)), Discriminator: "0", Username: "beto", DisplayName: "Beto"},
		{ID: discord.UserID(id(4)), Discriminator: "0", Username: "carla", DisplayName: "Carla"},
		{ID: discord.UserID(id(5)), Discriminator: "0", Username: "dani", DisplayName: "Dani"},
		{ID: discord.UserID(id(6)), Discriminator: "0", Username: "demo_bot", DisplayName: "Welcome Bot", Bot: true},
		{ID: discord.UserID(id(7)), Discriminator: "0", Username: "elena", DisplayName: "𝓔𝓵𝓮𝓷𝓪 | ᴷⁱⁿᵍ"},
		{ID: discord.UserID(id(8)), Discriminator: "0", Username: "fer", DisplayName: "Fer"},
	}
	presences := []discord.Presence{
		{Status: discord.OnlineStatus, Activities: []discord.Activity{{Type: discord.ListeningActivity, Name: "YouTube Music", Details: "Sample song", State: "Sample artist", Timestamps: &discord.ActivityTimestamps{Start: discord.UnixMsTimestamp(time.Now().Add(-56 * time.Minute).UnixMilli())}}}},
		{Status: discord.OnlineStatus, Activities: []discord.Activity{{Type: discord.GameActivity, Name: "Counter-Strike"}}},
		{Status: discord.IdleStatus},
		{Status: discord.OnlineStatus, Activities: []discord.Activity{{Type: discord.CustomActivity, Name: "Custom Status", State: "Studying 📚"}}},
		{Status: discord.OnlineStatus},
		{Status: discord.DoNotDisturbStatus, Activities: []discord.Activity{{Type: discord.CustomActivity, Name: "Custom Status", State: "busy"}}},
		{Status: discord.OfflineStatus},
	}
	cab.PresenceSet(discord.NullGuildID, &discord.Presence{User: me, Status: discord.OnlineStatus, Activities: []discord.Activity{
		{Type: discord.CustomActivity, Name: "Custom Status", State: "Testing Linkcord"},
		{Type: discord.ListeningActivity, Name: "YouTube Music", Details: "Sample song", State: "Sample artist", Timestamps: &discord.ActivityTimestamps{Start: discord.UnixMsTimestamp(time.Now().Add(-3 * time.Minute).UnixMilli())}},
	}}, false)

	// A server with the decorated names that many servers use.
	guild := discord.Guild{ID: discord.GuildID(id(100)), Name: "Demo Server", OwnerID: me.ID}
	cab.GuildSet(&guild, false)
	roles := []discord.Role{
		{ID: discord.RoleID(id(101)), Name: "Moderators", Color: 0x2ecc71, Position: 3, Hoist: true},
		{ID: discord.RoleID(id(102)), Name: "Members", Color: 0x7a4a1e, Position: 2, Hoist: true},
		{ID: discord.RoleID(id(103)), Name: "Bots", Color: 0xf1c40f, Position: 1, Hoist: true},
	}
	for i := range roles {
		cab.RoleSet(guild.ID, &roles[i], false)
	}
	memberRoles := [][]discord.RoleID{{roles[0].ID}, {roles[0].ID}, {roles[1].ID}, {roles[1].ID}, {roles[2].ID}, {roles[1].ID}, {roles[1].ID}}
	cab.MemberSet(guild.ID, &discord.Member{User: me, RoleIDs: []discord.RoleID{roles[0].ID}}, false)
	for i, user := range users {
		cab.MemberSet(guild.ID, &discord.Member{User: user, RoleIDs: memberRoles[i]}, false)
		p := presences[i]
		p.User, p.GuildID = user, guild.ID
		cab.PresenceSet(guild.ID, &p, false)
		p.GuildID = 0
		cab.PresenceSet(discord.NullGuildID, &p, false)
	}

	cat := discord.Channel{ID: discord.ChannelID(id(110)), GuildID: guild.ID, Type: discord.GuildCategory, Name: "【 💬 CHAT 💬 】", Position: 1}
	voiceCat := discord.Channel{ID: discord.ChannelID(id(111)), GuildID: guild.ID, Type: discord.GuildCategory, Name: "🔊 VOICE", Position: 2}
	channels := []discord.Channel{
		{ID: discord.ChannelID(id(120)), GuildID: guild.ID, Type: discord.GuildText, Name: "╭・━━-👋-welcome", Position: 0, Topic: "Welcome to the demo server ✨"},
		{ID: discord.ChannelID(id(121)), GuildID: guild.ID, Type: discord.GuildText, Name: "├・━━-💀-goodbye", Position: 1},
		{ID: discord.ChannelID(id(122)), GuildID: guild.ID, Type: discord.GuildText, Name: "╰・━━-🔴-streams", Position: 2},
		{ID: discord.ChannelID(id(123)), GuildID: guild.ID, Type: discord.GuildText, Name: "🔒private🔒", Position: 3, ParentID: cat.ID},
		{ID: discord.ChannelID(id(124)), GuildID: guild.ID, Type: discord.GuildText, Name: "general-chat", Position: 4, ParentID: cat.ID},
		{ID: discord.ChannelID(id(125)), GuildID: guild.ID, Type: discord.GuildVoice, Name: "🏝 Game Room", Position: 5, ParentID: voiceCat.ID},
		{ID: discord.ChannelID(id(126)), GuildID: guild.ID, Type: discord.GuildVoice, Name: "General", Position: 6, ParentID: voiceCat.ID},
	}
	cab.ChannelSet(&cat, false)
	cab.ChannelSet(&voiceCat, false)
	for i := range channels {
		cab.ChannelSet(&channels[i], false)
	}
	for _, i := range []int{3, 0, 6, 1} {
		cab.VoiceStateSet(guild.ID, &discord.VoiceState{GuildID: guild.ID, ChannelID: channels[5].ID, UserID: users[i].ID, SelfMute: i == 6}, false)
	}

	// Direct messages: one with a person and a group.
	dm := discord.Channel{ID: discord.ChannelID(id(200)), Type: discord.DirectMessage, DMRecipients: []discord.User{users[0]}, LastMessageID: discord.MessageID(id(9000))}
	group := discord.Channel{ID: discord.ChannelID(id(201)), Type: discord.GroupDM, Name: "Friends Group", DMRecipients: []discord.User{users[0], users[1], users[3]}}
	cab.ChannelSet(&dm, false)
	cab.ChannelSet(&group, false)

	avatar := cdnSize(discord.User{ID: discord.UserID(id(9)), Discriminator: "0"}.AvatarURLWithType(discord.PNGImage))
	n := 1000
	add := func(ch discord.ChannelID, guildID discord.GuildID, author discord.User, ago time.Duration, content string, extra func(*discord.Message)) {
		n++
		m := discord.Message{
			ID: discord.MessageID(discord.NewSnowflake(time.Now().Add(-ago))), ChannelID: ch, GuildID: guildID,
			Author: author, Content: content, Timestamp: discord.NewTimestamp(time.Now().Add(-ago)),
		}
		if extra != nil {
			extra(&m)
		}
		cab.MessageSet(&m, false)
	}
	for i := 60; i > 6; i-- {
		add(channels[0].ID, guild.ID, users[4], time.Duration(i)*time.Hour, fmt.Sprintf("👋 <@%s> **Welcome to the server!** Read the rules and have fun.", users[i%4].ID), func(m *discord.Message) {
			m.Attachments = []discord.Attachment{{Filename: "welcome.jpg", URL: discord.URL(avatar), Size: 392 * 1024, ContentType: "image/png", Width: 128, Height: 128}}
		})
	}
	add(channels[0].ID, guild.ID, users[1], 5*time.Hour, "hello everyone 🔥🔥", func(m *discord.Message) {
		m.Reactions = []discord.Reaction{{Count: 3, Emoji: discord.Emoji{Name: "🔥"}}, {Count: 1, Me: true, Emoji: discord.Emoji{Name: "😂"}}}
	})
	add(channels[0].ID, guild.ID, users[1], 5*time.Hour-time.Minute, "anyone up for *cs 1.6*? ~~not now~~ maybe later", nil)
	add(channels[0].ID, guild.ID, users[0], 5*time.Hour-2*time.Minute, "count me in <:StareNeutral:612368399732965376> but I'm winning", nil)
	add(channels[0].ID, guild.ID, users[0], 5*time.Hour-3*time.Minute, "<a:StareNodGIF:614322540332056577><:StareNeutral:612368399732965376>", nil)
	add(channels[0].ID, guild.ID, users[2], 4*time.Hour, "Check this out: `go build ./...`, then\n```\nlinkcord.exe --demo\n```\nworks on Windows 7 😎", nil)
	add(channels[0].ID, guild.ID, me, 3*time.Hour, "> this is a quote\nAnd this is a ||spoiler|| with https://github.com/ezchr/linkcord", func(m *discord.Message) {
		m.Embeds = []discord.Embed{{Title: "GitHub - ezchr/linkcord", Description: "Lightweight Discord client for Windows 7, 8, 10, and 11. - ezchr/linkcord", URL: "https://github.com/ezchr/linkcord", Color: 0x1f2328}}
	})
	add(channels[0].ID, guild.ID, users[5], 2*time.Hour, "<@"+me.ID.String()+"> reply when you can 🙏", nil)
	add(channels[0].ID, guild.ID, me, time.Hour, "on my way", func(m *discord.Message) {
		ref := discord.Message{Author: users[5], Content: "reply when you can 🙏"}
		m.ReferencedMessage = &ref
		m.Type = discord.InlinedReplyMessage
	})
	add(channels[0].ID, guild.ID, users[6], 30*time.Minute, "", func(m *discord.Message) { m.Type = discord.GuildMemberJoinMessage })

	for i := 60; i > 0; i-- {
		author := users[0]
		if i%3 == 0 {
			author = me
		}
		add(dm.ID, 0, author, time.Duration(i)*time.Hour, fmt.Sprintf("test message %d 😀", i), nil)
	}
	for i := 60; i > 0; i-- {
		add(group.ID, 0, users[i%3], time.Duration(i)*time.Minute, fmt.Sprintf("hello group %d", i), nil)
	}

	ready := &gateway.ReadyEvent{User: me}
	ready.Guilds = []gateway.GuildCreateEvent{{Guild: guild}}
	for i := range 6 {
		g := discord.Guild{ID: discord.GuildID(id(300 + i)), Name: []string{"Games", "Music", "Programming", "Anime", "Linkcord Club", "Homework"}[i], OwnerID: me.ID}
		cab.GuildSet(&g, false)
		ready.Guilds = append(ready.Guilds, gateway.GuildCreateEvent{Guild: g})
	}

	u.chat = newChatView(u, st)
	u.chat.demo = true
	u.chat.load(ready)
	u.chat.selectGuild(guild.ID)
	u.screen = screenChat
}
