package gui

import (
	"fmt"
	"time"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/gateway"
)

// demoEnvVar shows made-up servers, people and messages without connecting
// to Discord, to check the interface without an account.
const demoEnvVar = "REDISCORD_DEMO"

// startDemo fills a state that never connects with made-up data and shows it.
func (u *ui) startDemo() {
	st := newState("demo")
	cab := st.Cabinet
	id := func(n int) discord.Snowflake {
		return discord.NewSnowflake(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(n) * time.Minute))
	}

	me := discord.User{ID: discord.UserID(id(1)), Discriminator: "0", Username: "usuario_demo", DisplayName: "Tú (demo)"}
	cab.MyselfSet(me, false)
	users := []discord.User{
		{ID: discord.UserID(id(2)), Discriminator: "0", Username: "ana", DisplayName: "Ana"},
		{ID: discord.UserID(id(3)), Discriminator: "0", Username: "beto", DisplayName: "Beto"},
		{ID: discord.UserID(id(4)), Discriminator: "0", Username: "carla", DisplayName: "Carla"},
		{ID: discord.UserID(id(5)), Discriminator: "0", Username: "dani", DisplayName: "Dani"},
		{ID: discord.UserID(id(6)), Discriminator: "0", Username: "bot_demo", DisplayName: "Bot de bienvenida", Bot: true},
		{ID: discord.UserID(id(7)), Discriminator: "0", Username: "elena", DisplayName: "𝓔𝓵𝓮𝓷𝓪 | ᴿᵉʸ"},
		{ID: discord.UserID(id(8)), Discriminator: "0", Username: "fer", DisplayName: "Fer"},
	}
	presences := []discord.Presence{
		{Status: discord.OnlineStatus, Activities: []discord.Activity{{Type: discord.ListeningActivity, Name: "YouTube Music", Details: "Canción de prueba", State: "Artista de prueba", Timestamps: &discord.ActivityTimestamps{Start: discord.UnixMsTimestamp(time.Now().Add(-56 * time.Minute).UnixMilli())}}}},
		{Status: discord.OnlineStatus, Activities: []discord.Activity{{Type: discord.GameActivity, Name: "Counter-Strike"}}},
		{Status: discord.IdleStatus},
		{Status: discord.OnlineStatus, Activities: []discord.Activity{{Type: discord.CustomActivity, Name: "Custom Status", State: "Estudiando 📚"}}},
		{Status: discord.OnlineStatus},
		{Status: discord.DoNotDisturbStatus, Activities: []discord.Activity{{Type: discord.CustomActivity, Name: "Custom Status", State: "busy"}}},
		{Status: discord.OfflineStatus},
	}
	cab.PresenceSet(discord.NullGuildID, &discord.Presence{User: me, Status: discord.OnlineStatus, Activities: []discord.Activity{
		{Type: discord.CustomActivity, Name: "Custom Status", State: "Probando reDiscord"},
		{Type: discord.ListeningActivity, Name: "YouTube Music", Details: "Canción de prueba", State: "Artista de prueba", Timestamps: &discord.ActivityTimestamps{Start: discord.UnixMsTimestamp(time.Now().Add(-3 * time.Minute).UnixMilli())}},
	}}, false)

	// A server with the decorated names that many servers use.
	guild := discord.Guild{ID: discord.GuildID(id(100)), Name: "Servidor Demo", OwnerID: me.ID}
	cab.GuildSet(&guild, false)
	roles := []discord.Role{
		{ID: discord.RoleID(id(101)), Name: "Moderadores", Color: 0x2ecc71, Position: 3, Hoist: true},
		{ID: discord.RoleID(id(102)), Name: "Miembros", Color: 0x7a4a1e, Position: 2, Hoist: true},
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

	cat := discord.Channel{ID: discord.ChannelID(id(110)), GuildID: guild.ID, Type: discord.GuildCategory, Name: "【 💬 CHARLA 💬 】", Position: 1}
	voiceCat := discord.Channel{ID: discord.ChannelID(id(111)), GuildID: guild.ID, Type: discord.GuildCategory, Name: "🔊 VOZ", Position: 2}
	channels := []discord.Channel{
		{ID: discord.ChannelID(id(120)), GuildID: guild.ID, Type: discord.GuildText, Name: "╭・━━-👋-bienvenida", Position: 0, Topic: "Bienvenidos al servidor de prueba ✨"},
		{ID: discord.ChannelID(id(121)), GuildID: guild.ID, Type: discord.GuildText, Name: "├・━━-💀-despedida", Position: 1},
		{ID: discord.ChannelID(id(122)), GuildID: guild.ID, Type: discord.GuildText, Name: "╰・━━-🔴-streams", Position: 2},
		{ID: discord.ChannelID(id(123)), GuildID: guild.ID, Type: discord.GuildText, Name: "🔒privado🔒", Position: 3, ParentID: cat.ID},
		{ID: discord.ChannelID(id(124)), GuildID: guild.ID, Type: discord.GuildText, Name: "chat-general", Position: 4, ParentID: cat.ID},
		{ID: discord.ChannelID(id(125)), GuildID: guild.ID, Type: discord.GuildVoice, Name: "🏝 Sala de juegos", Position: 5, ParentID: voiceCat.ID},
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
	group := discord.Channel{ID: discord.ChannelID(id(201)), Type: discord.GroupDM, Name: "Grupo de amigos", DMRecipients: []discord.User{users[0], users[1], users[3]}}
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
		add(channels[0].ID, guild.ID, users[4], time.Duration(i)*time.Hour, fmt.Sprintf("👋 <@%s> **¡Bienvenido al servidor!** Lee las reglas y diviértete.", users[i%4].ID), func(m *discord.Message) {
			m.Attachments = []discord.Attachment{{Filename: "bienvenida.jpg", URL: discord.URL(avatar), Size: 392 * 1024, ContentType: "image/png", Width: 128, Height: 128}}
		})
	}
	add(channels[0].ID, guild.ID, users[1], 5*time.Hour, "hola a todos 🔥🔥", nil)
	add(channels[0].ID, guild.ID, users[1], 5*time.Hour-time.Minute, "alguien para jugar *cs 1.6*? ~~ahora no~~ más tarde", nil)
	add(channels[0].ID, guild.ID, users[2], 4*time.Hour, "Miren esto: `go build ./...` y luego\n```\nrediscord.exe --demo\n```\nfunciona en Windows 7 😎", nil)
	add(channels[0].ID, guild.ID, me, 3*time.Hour, "> esto es una cita\nY esto un ||spoiler|| con https://github.com/ayn2op/discordo", func(m *discord.Message) {
		m.Embeds = []discord.Embed{{Title: "GitHub - ayn2op/discordo: A lightweight, secure, and feature-rich Discord terminal (TUI) client.", Description: "A lightweight, secure, and feature-rich Discord terminal (TUI) client. - ayn2op/discordo", URL: "https://github.com/ayn2op/discordo", Color: 0x1f2328}}
	})
	add(channels[0].ID, guild.ID, users[5], 2*time.Hour, "<@"+me.ID.String()+"> respóndeme cuando puedas 🙏", nil)
	add(channels[0].ID, guild.ID, me, time.Hour, "ya voy", func(m *discord.Message) {
		ref := discord.Message{Author: users[5], Content: "respóndeme cuando puedas 🙏"}
		m.ReferencedMessage = &ref
		m.Type = discord.InlinedReplyMessage
	})
	add(channels[0].ID, guild.ID, users[6], 30*time.Minute, "", func(m *discord.Message) { m.Type = discord.GuildMemberJoinMessage })

	for i := 60; i > 0; i-- {
		author := users[0]
		if i%3 == 0 {
			author = me
		}
		add(dm.ID, 0, author, time.Duration(i)*time.Hour, fmt.Sprintf("mensaje de prueba %d 😀", i), nil)
	}
	for i := 60; i > 0; i-- {
		add(group.ID, 0, users[i%3], time.Duration(i)*time.Minute, fmt.Sprintf("hola grupo %d", i), nil)
	}

	ready := &gateway.ReadyEvent{User: me}
	ready.Guilds = []gateway.GuildCreateEvent{{Guild: guild}}
	for i := range 6 {
		g := discord.Guild{ID: discord.GuildID(id(300 + i)), Name: []string{"Juegos", "Música", "Programación", "Anime", "Discordo Club", "Tareas"}[i], OwnerID: me.ID}
		cab.GuildSet(&g, false)
		ready.Guilds = append(ready.Guilds, gateway.GuildCreateEvent{Guild: g})
	}

	u.chat = newChatView(u, st)
	u.chat.demo = true
	u.chat.load(ready)
	u.chat.selectGuild(guild.ID)
	u.screen = screenChat
}
