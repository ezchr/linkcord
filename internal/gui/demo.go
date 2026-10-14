package gui

import (
	"fmt"
	"os"
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
		{ID: discord.UserID(id(2)), Discriminator: "0", Username: "ana", DisplayName: "Ana", PrimaryGuild: &discord.PrimaryGuild{GuildID: discord.GuildID(id(100)), Enabled: true, Tag: "DEMO"}},
		{ID: discord.UserID(id(3)), Discriminator: "0", Username: "beto", DisplayName: "Beto"},
		{ID: discord.UserID(id(4)), Discriminator: "0", Username: "carla", DisplayName: "Carla"},
		{ID: discord.UserID(id(5)), Discriminator: "0", Username: "dani", DisplayName: "Dani ダニ"},
		{ID: discord.UserID(id(6)), Discriminator: "0", Username: "demo_bot", DisplayName: "Welcome Bot", Bot: true},
		{ID: discord.UserID(id(7)), Discriminator: "0", Username: "elena", DisplayName: "𝓔𝓵𝓮𝓷𝓪 | ᴷⁱⁿᵍ"},
		{ID: discord.UserID(id(8)), Discriminator: "0", Username: "fer", DisplayName: "Fer"},
	}
	// Beto speaks in the demo's voice channel, for its rings to show.
	u.voice.demoSpeaker = users[1].ID
	presences := []discord.Presence{
		{Status: discord.OnlineStatus, Activities: []discord.Activity{{Type: discord.ListeningActivity, Name: "YouTube Music", Details: "Sample song", State: "Sample artist", Buttons: []string{"Listen Along"}, Metadata: &discord.ActivityMetadata{ButtonURLs: []string{"https://music.youtube.com"}}, Timestamps: &discord.ActivityTimestamps{Start: discord.UnixMsTimestamp(time.Now().Add(-56 * time.Minute).UnixMilli())}}}},
		{Status: discord.OnlineStatus, Activities: []discord.Activity{{Type: discord.GameActivity, Name: "Counter-Strike"}}},
		{Status: discord.IdleStatus},
		{Status: discord.OnlineStatus, Activities: []discord.Activity{{Type: discord.CustomActivity, Name: "Custom Status", State: "Studying 📚"}}},
		{Status: discord.OnlineStatus},
		{Status: discord.DoNotDisturbStatus, Activities: []discord.Activity{{Type: discord.CustomActivity, Name: "Custom Status", State: "busy"}}},
		{Status: discord.OfflineStatus},
	}
	cab.PresenceSet(discord.NullGuildID, &discord.Presence{User: me, Status: discord.OnlineStatus, Activities: []discord.Activity{
		{Type: discord.CustomActivity, Name: "Custom Status", State: "Testing Linkcord"},
		{Type: discord.ListeningActivity, Name: "YouTube Music", Details: "Sample song", State: "Sample artist", Timestamps: &discord.ActivityTimestamps{Start: discord.UnixMsTimestamp(time.Now().Add(-3 * time.Minute).UnixMilli()), End: discord.UnixMsTimestamp(time.Now().Add(70 * time.Second).UnixMilli())}},
	}}, false)

	// A server with the decorated names that many servers use.
	guild := discord.Guild{ID: discord.GuildID(id(100)), Name: "Demo Server", OwnerID: me.ID, NitroBoost: discord.NitroLevel2, NitroBoosters: 9}
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
		m := discord.Member{User: user, RoleIDs: memberRoles[i]}
		if i == 1 {
			// Beto boosts the server.
			m.BoostedSince = discord.NewTimestamp(time.Now().Add(-30 * 24 * time.Hour))
		}
		cab.MemberSet(guild.ID, &m, false)
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
		{ID: discord.ChannelID(id(123)), GuildID: guild.ID, Type: discord.GuildText, Name: "🔒private🔒", Position: 3, ParentID: cat.ID,
			// Hidden from everyone but some, which shows a lock.
			Overwrites: []discord.Overwrite{{ID: discord.Snowflake(guild.ID), Type: discord.OverwriteRole, Deny: discord.PermissionViewChannel}}},
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
		cab.VoiceStateSet(guild.ID, &discord.VoiceState{GuildID: guild.ID, ChannelID: channels[5].ID, UserID: users[i].ID, SelfMute: i == 6, SelfStream: i == 0}, false)
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
	var deleted discord.Message
	add(channels[0].ID, guild.ID, users[1], 5*time.Hour-time.Minute, "anyone up for *cs 1.6*? ~~not now~~ maybe later", func(m *discord.Message) {
		deleted = *m
	})
	add(channels[0].ID, guild.ID, users[0], 5*time.Hour-2*time.Minute, "count me in <:StareNeutral:612368399732965376> but I'm winning", nil)
	add(channels[0].ID, guild.ID, users[0], 5*time.Hour-3*time.Minute, "<a:StareNodGIF:614322540332056577><:StareNeutral:612368399732965376>", nil)
	add(channels[0].ID, guild.ID, users[2], 4*time.Hour, "Check this out: `go build ./...`, then\n```\nlinkcord.exe --demo\n```\nworks on Windows 7 😎", nil)
	add(channels[0].ID, guild.ID, me, 3*time.Hour, "> this is a quote\nAnd this is a ||spoiler|| with https://github.com/ezchr/linkcord", func(m *discord.Message) {
		m.Embeds = []discord.Embed{{Title: "GitHub - ezchr/linkcord", Description: "Lightweight Discord client for Windows 7, 8, 10, and 11. - ezchr/linkcord", URL: "https://github.com/ezchr/linkcord", Color: 0x1f2328}}
	})
	var asked discord.Message
	add(channels[0].ID, guild.ID, users[5], 2*time.Hour, "<@"+me.ID.String()+"> reply when you can 🙏", func(m *discord.Message) {
		asked = *m
	})
	add(channels[0].ID, guild.ID, users[3], 100*time.Minute, "", func(m *discord.Message) {
		m.Reference = &discord.MessageReference{Type: discord.MessageReferenceTypeForward, ChannelID: channels[1].ID}
		m.MessageSnapshots = []discord.MessageSnapshot{{Message: discord.MessageSnapshotMessage{
			Content: "**patch notes** are out: new maps and a *faster* lobby", Timestamp: discord.NewTimestamp(time.Now().Add(-26 * time.Hour)),
		}}}
	})
	add(channels[1].ID, guild.ID, users[1], 97*time.Minute, "", func(m *discord.Message) {
		m.Poll = &discord.Poll{
			Question: discord.PollMedia{Text: "Which map tonight?"},
			Answers: []discord.PollAnswer{
				{AnswerID: 1, PollMedia: discord.PollMedia{Text: "Dust", Emoji: &discord.Emoji{Name: "🏜️"}}},
				{AnswerID: 2, PollMedia: discord.PollMedia{Text: "Inferno", Emoji: &discord.Emoji{Name: "🔥"}}},
				{AnswerID: 3, PollMedia: discord.PollMedia{Text: "Mirage", Emoji: &discord.Emoji{Name: "🌴"}}},
			},
			Expiry: discord.NewTimestamp(time.Now().Add(20 * time.Hour)),
			Results: &discord.PollResults{AnswerCounts: []discord.PollAnswerCount{
				{ID: 1, Count: 4}, {ID: 2, Count: 7, MeVoted: true}, {ID: 3, Count: 2},
			}},
		}
	})
	add(channels[0].ID, guild.ID, users[0], 95*time.Minute, "the logs from yesterday", func(m *discord.Message) {
		m.Attachments = []discord.Attachment{{Filename: "server-log.txt", URL: discord.URL(avatar), Size: 18 * 1024, ContentType: "text/plain"}}
	})
	// A voice message to test with, from LINKCORD_DEMO_VOICE.
	if voice := os.Getenv("LINKCORD_DEMO_VOICE"); voice != "" {
		add(channels[0].ID, guild.ID, users[1], 92*time.Minute, "", func(m *discord.Message) {
			wave := make([]byte, 64)
			for i := range wave {
				wave[i] = byte(60 + (i*37)%190)
			}
			m.Flags = discord.IsVoiceMessage
			m.Attachments = []discord.Attachment{{Filename: "voice-message.ogg", URL: discord.URL(voice), Size: 40 * 1024, ContentType: "audio/ogg", DurationSecs: 6, Waveform: wave}}
		})
	}
	// A video to test playback with, from LINKCORD_DEMO_VIDEO.
	if video := os.Getenv("LINKCORD_DEMO_VIDEO"); video != "" {
		// LINKCORD_DEMO_VIDEO_SIZE, such as 360x640, gives it another shape.
		// It is then the newest message, to be seen at once.
		vw, vh, ago := 1920, 1080, 90*time.Minute
		if n, _ := fmt.Sscanf(os.Getenv("LINKCORD_DEMO_VIDEO_SIZE"), "%dx%d", &vw, &vh); n == 2 {
			ago = 0
		}
		add(channels[0].ID, guild.ID, users[2], ago, "look at this clip", func(m *discord.Message) {
			m.Attachments = []discord.Attachment{{Filename: "clip.mp4", URL: discord.URL(video), Size: 2 << 20, ContentType: "video/mp4", Width: uint(vw), Height: uint(vh)}}
		})
	}
	add(channels[0].ID, guild.ID, users[1], 2*time.Minute, "https://example.com/oauth/authorize?code=true&client_id=9d1c250a-e61b-44d9-88ed-5944d1962f5e&response_type=code&redirect_uri=http://localhost:51514/callback&scope=org:create_api_key+user:profile+user:inference&state=u2VtMFWSXJ6b6YfOLxMm5_yHOMVahUZsoZM5gk5_Isw", nil)
	add(channels[0].ID, guild.ID, me, time.Hour, "on my way", func(m *discord.Message) {
		ref := asked
		m.ReferencedMessage = &ref
		m.Reference = &discord.MessageReference{MessageID: asked.ID, ChannelID: asked.ChannelID}
		m.Type = discord.InlinedReplyMessage
	})
	add(channels[0].ID, guild.ID, users[6], 30*time.Minute, "", func(m *discord.Message) { m.Type = discord.GuildMemberJoinMessage })

	// A music bot with formatting in its embed, buttons of every color, a
	// menu, and a message laid out with components.
	add(channels[0].ID, guild.ID, users[4], 25*time.Minute, "", func(m *discord.Message) {
		m.Embeds = []discord.Embed{{
			Description: "> Now playing\n**[Demo Song (original)](https://example.com/song)**\n`[▬▬▬▬🔘──────────]` 1:24 / 3:05",
			Color:       0xf0b232,
		}}
		m.Components = discord.TopLevelComponents{
			&discord.ActionRowComponent{
				&discord.ButtonComponent{Style: discord.SecondaryButtonStyle(), Emoji: &discord.ComponentEmoji{Name: "⏮"}, CustomID: "prev"},
				&discord.ButtonComponent{Style: discord.PrimaryButtonStyle(), Label: "Pause", Emoji: &discord.ComponentEmoji{Name: "⏸"}, CustomID: "pause"},
				&discord.ButtonComponent{Style: discord.SecondaryButtonStyle(), Emoji: &discord.ComponentEmoji{Name: "⏭"}, CustomID: "next"},
				&discord.ButtonComponent{Style: discord.SuccessButtonStyle(), Label: "Add song", CustomID: demoFormID},
				&discord.ButtonComponent{Style: discord.DangerButtonStyle(), Label: "Stop", CustomID: "stop"},
			},
			&discord.ActionRowComponent{
				&discord.StringSelectComponent{CustomID: "loop", Placeholder: "Loop mode", Options: []discord.SelectOption{
					{Label: "Off", Value: "off", Description: "Play the queue once", Emoji: &discord.ComponentEmoji{Name: "➡️"}},
					{Label: "Song", Value: "song", Description: "Repeat the current song", Emoji: &discord.ComponentEmoji{Name: "🔂"}},
					{Label: "Queue", Value: "queue", Description: "Repeat the whole queue", Emoji: &discord.ComponentEmoji{Name: "🔁"}},
				}},
			},
			&discord.ActionRowComponent{
				&discord.ButtonComponent{Style: discord.LinkButtonStyle("https://example.com/dashboard"), Label: "Dashboard"},
				&discord.ButtonComponent{Style: discord.SecondaryButtonStyle(), Label: "Disabled", CustomID: "off", Disabled: true},
			},
		}
	})
	add(channels[0].ID, guild.ID, users[4], 24*time.Minute, "", func(m *discord.Message) {
		m.Embeds = []discord.Embed{{
			Author:      &discord.EmbedAuthor{Name: "Server Stats"},
			Title:       "Weekly report",
			URL:         "https://example.com/stats",
			Description: "Here is how the server did this week. **Great job, everyone!** 🎉",
			Color:       0x5865f2,
			Fields: []discord.EmbedField{
				{Name: "Messages", Value: "12,480", Inline: true},
				{Name: "New members", Value: "37", Inline: true},
				{Name: "Voice hours", Value: "210", Inline: true},
				{Name: "Top channel", Value: "<#" + channels[0].ID.String() + "> with *4,210* messages"},
			},
			Thumbnail: &discord.EmbedThumbnail{URL: discord.URL(avatar), Width: 128, Height: 128},
			Footer:    &discord.EmbedFooter{Text: "Stats bot"},
			Timestamp: discord.NewTimestamp(time.Now().Add(-24 * time.Minute)),
		}}
	})
	add(channels[0].ID, guild.ID, users[4], 22*time.Minute, "Nothing is playing right now.", func(m *discord.Message) {
		m.Flags = discord.EphemeralMessage
	})
	add(channels[0].ID, guild.ID, users[4], 23*time.Minute, "", func(m *discord.Message) {
		m.Flags = discord.IsComponentsV2
		m.Components = discord.TopLevelComponents{
			&discord.ContainerComponent{AccentColor: 0x23a55a, Components: []discord.Component{
				&discord.SectionComponent{
					Components: []discord.Component{
						&discord.TextDisplayComponent{Content: "## Event tonight\nMovie night in **🏝 Game Room** at 9 PM.\n-# Organized by the moderators"},
					},
					Accessory: &discord.ThumbnailComponent{Media: discord.UnfurledMediaitem{URL: avatar, Width: 128, Height: 128}},
				},
				&discord.SeparatorComponent{Divider: true, Spacing: discord.SeparatorComponentSpacingSmallPadding},
				&discord.ActionRowComponent{
					&discord.ButtonComponent{Style: discord.SuccessButtonStyle(), Label: "I'm going", Emoji: &discord.ComponentEmoji{Name: "✅"}, CustomID: "going"},
					&discord.ButtonComponent{Style: discord.SecondaryButtonStyle(), Label: "Maybe", CustomID: "maybe"},
				},
			}},
		}
	})

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
	// A message someone deleted, shown with its sign; the demo keeps nothing.
	u.chat.deleted = newDeletedLog("")
	u.chat.deleted.record(deleted)

	// Friends in each state, and a message request from a stranger.
	rel := func(user discord.User, t discord.RelationshipType) discord.Relationship {
		return discord.Relationship{UserID: user.ID, User: user, Type: t}
	}
	gabi := discord.User{ID: discord.UserID(id(9)), Discriminator: "0", Username: "gabi", DisplayName: "Gabi"}
	hugo := discord.User{ID: discord.UserID(id(10)), Discriminator: "0", Username: "hugo_77", DisplayName: "Hugo"}
	spam := discord.User{ID: discord.UserID(id(11)), Discriminator: "0", Username: "free_nitro", DisplayName: "Free Nitro"}
	stranger := discord.User{ID: discord.UserID(id(12)), Discriminator: "0", Username: "ivan.dev", DisplayName: "Iván"}
	u.chat.friends.demoRels = []discord.Relationship{
		rel(users[0], discord.FriendRelationship), rel(users[1], discord.FriendRelationship),
		rel(users[2], discord.FriendRelationship), rel(users[3], discord.FriendRelationship),
		rel(users[6], discord.FriendRelationship),
		rel(gabi, discord.IncomingFriendRequest), rel(hugo, discord.SentFriendRequest),
		rel(spam, discord.BlockedRelationship),
	}
	request := discord.Channel{ID: discord.ChannelID(id(202)), Type: discord.DirectMessage, DMRecipients: []discord.User{stranger}, LastMessageID: discord.MessageID(id(9100))}
	cab.ChannelSet(&request, false)
	for i := 60; i > 0; i-- {
		add(request.ID, 0, stranger, time.Duration(i)*time.Minute, fmt.Sprintf("hi! I saw you in the Games server (%d)", i), nil)
	}
	u.chat.friends.setRequests([]messageRequest{{
		ID: request.ID, Type: request.Type, IsMessageRequest: true, LastMessageID: request.LastMessageID,
		Since: discord.NewTimestamp(time.Now().Add(-time.Hour)), Recipients: request.DMRecipients,
	}})

	u.chat.guild.events[guild.ID] = upcomingEvents([]discord.GuildScheduledEvent{
		{Name: "Movie night", Description: "We watch a movie together. Bring snacks!", Status: discord.ScheduledEvent,
			StartTime: discord.NewTimestamp(time.Now().Add(5 * time.Hour)), ChannelID: channels[5].ID, UserCount: 12},
		{Name: "CS 1.6 tournament", Status: discord.ActiveEvent, StartTime: discord.NewTimestamp(time.Now().Add(-time.Hour)),
			EntityMetadata: &discord.EntityMetadata{Location: "Server 192.168.0.10"}, UserCount: 8},
	})

	u.chat.load(ready)
	u.chat.selectGuild(guild.ID)
	u.screen = screenChat
	// Ana calls, to see the incoming call, with LINKCORD_DEMO_CALL.
	if os.Getenv("LINKCORD_DEMO_CALL") != "" {
		time.AfterFunc(2*time.Second, func() {
			u.post(func() {
				cl := call{ChannelID: dm.ID, Ringing: []discord.UserID{me.ID}, VoiceStates: []discord.VoiceState{{ChannelID: dm.ID, UserID: users[0].ID}}}
				u.chat.noteCall(cl)
				u.chat.callRings(cl)
			})
		})
	}
}

// demoFormID is the button of the demo whose bot answers with a form.
const demoFormID = "demo_form"

// demoForm is the form of the demo, with the kinds of fields bots use.
func demoForm(m discord.Message) modalCreateEvent {
	ev := modalCreateEvent{
		ID: "demo", ChannelID: m.ChannelID, Title: "Add a song", CustomID: "add_song",
		Components: discord.TopLevelComponents{
			&discord.LabelComponent{Label: "Song", Description: "A name or a link", Component: &discord.TextInputComponent{
				CustomID: "song", Style: discord.TextInputShortStyle, Required: true, Placeholder: "Never Gonna Give You Up",
			}},
			&discord.LabelComponent{Label: "Where in the queue", Component: &discord.StringSelectComponent{
				CustomID: "where", Options: []discord.SelectOption{{Label: "Next", Value: "next"}, {Label: "Last", Value: "last", Default: true}},
			}},
			&discord.LabelComponent{Label: "Note for the DJ", Component: &discord.TextInputComponent{
				CustomID: "note", Style: discord.TextInputParagraphStyle,
			}},
		},
	}
	ev.Application.Name = m.Author.DisplayOrUsername()
	return ev
}
