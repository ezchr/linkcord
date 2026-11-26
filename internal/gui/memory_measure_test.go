package gui

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/state/store"
	"github.com/ayn2op/arikawa/v3/state/store/defaultstore"
)

// heapInUse returns the live heap after a full collection.
func heapInUse() uint64 {
	runtime.GC()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.HeapAlloc
}

// Sizes of a heavy account: many big servers, a long session.
const (
	measureGuilds        = 100
	measureChannels      = 60  // per guild
	measureRoles         = 40  // per guild
	measureEmoji         = 50  // per guild
	measureVisitedGuilds = 30  // guilds whose member list was opened
	measureListMembers   = 300 // member list rows kept per visited guild
	measureAuthors       = 400 // distinct message authors per visited guild
)

func fakeUser(id int) discord.User {
	return discord.User{
		ID:          discord.UserID(1e17 + id),
		Username:    fmt.Sprintf("user_name_%d", id),
		DisplayName: fmt.Sprintf("Display %d", id),
		Avatar:      "a_0123456789abcdef0123456789abcdef",
	}
}

func fakeMember(id int) discord.Member {
	return discord.Member{
		User:    fakeUser(id),
		Nick:    "nickname",
		RoleIDs: []discord.RoleID{1, 2, 3, 4},
		Joined:  discord.NewTimestamp(time.Now()),
	}
}

func fakePresence(g discord.GuildID, id int) discord.Presence {
	return discord.Presence{
		User:    discord.User{ID: discord.UserID(1e17 + id)},
		GuildID: g,
		Status:  discord.OnlineStatus,
		Activities: []discord.Activity{{
			Name:    "Some Game",
			Type:    discord.GameActivity,
			Details: "In a match",
			State:   "Ranked",
		}},
		ClientStatus: discord.ClientStatus{Desktop: discord.OnlineStatus},
	}
}

func fakeMessage(ch discord.ChannelID, g discord.GuildID, i int) discord.Message {
	m := discord.Message{
		ID:        discord.MessageID(2e17 + int64(ch)*1000 + int64(i)),
		ChannelID: ch,
		GuildID:   g,
		Author:    fakeUser(i % 50),
		Content:   strings.Repeat("some message text ", 7),
		Timestamp: discord.NewTimestamp(time.Now()),
	}
	if i%5 == 0 {
		m.Embeds = []discord.Embed{{
			Title:       "A link title",
			Description: strings.Repeat("embed description ", 12),
			URL:         "https://example.com/some/long/path?with=query",
			Thumbnail:   &discord.EmbedThumbnail{URL: "https://example.com/img.png", Proxy: "https://media.discordapp.net/external/abc/img.png", Width: 400, Height: 300},
		}}
	}
	if i%10 == 0 {
		m.Attachments = []discord.Attachment{{
			ID: 1, Filename: "image.png", URL: "https://cdn.discordapp.com/attachments/1/2/image.png?ex=1&is=2&hm=0123456789abcdef",
			Proxy: "https://media.discordapp.net/attachments/1/2/image.png?ex=1&is=2&hm=0123456789abcdef", Size: 123456, Width: 1920, Height: 1080,
		}}
	}
	if i%8 == 0 {
		m.Reactions = []discord.Reaction{{Count: 3, Emoji: discord.Emoji{Name: "👍"}}}
	}
	return m
}

// TestMeasureStoreMemory reports how much of the heap each part of the state
// takes for a heavy account. Run with LINKCORD_MEMREPORT=1 and -v.
func TestMeasureStoreMemory(t *testing.T) {
	if os.Getenv("LINKCORD_MEMREPORT") == "" {
		t.Skip("set LINKCORD_MEMREPORT=1 to measure")
	}
	report := func(name string, before uint64) uint64 {
		now := heapInUse()
		t.Logf("%-40s %7.1f MB", name, float64(now-before)/(1<<20))
		return now
	}

	cab := defaultstore.New()
	base := heapInUse()

	at := base
	for g := 1; g <= measureGuilds; g++ {
		gid := discord.GuildID(g)
		_ = cab.GuildSet(&discord.Guild{ID: gid, Name: fmt.Sprintf("Guild %d", g), Icon: "0123456789abcdef"}, false)
	}
	at = report("guilds", at)
	for g := 1; g <= measureGuilds; g++ {
		gid := discord.GuildID(g)
		for c := 0; c < measureChannels; c++ {
			_ = cab.ChannelSet(&discord.Channel{
				ID: discord.ChannelID(g*1000 + c), GuildID: gid, Type: discord.GuildText,
				Name: "general-chat", Topic: strings.Repeat("topic ", 10),
				Overwrites: []discord.Overwrite{{ID: 1}, {ID: 2}, {ID: 3}},
			}, false)
		}
	}
	at = report(fmt.Sprintf("channels (%d)", measureGuilds*measureChannels), at)
	for g := 1; g <= measureGuilds; g++ {
		for r := 0; r < measureRoles; r++ {
			_ = cab.RoleSet(discord.GuildID(g), &discord.Role{ID: discord.RoleID(g*1000 + r), Name: "Some Role"}, false)
		}
	}
	at = report(fmt.Sprintf("roles (%d)", measureGuilds*measureRoles), at)
	for g := 1; g <= measureGuilds; g++ {
		emojis := make([]discord.Emoji, measureEmoji)
		for e := range emojis {
			emojis[e] = discord.Emoji{ID: discord.EmojiID(g*1000 + e), Name: "emoji_name"}
		}
		_ = cab.EmojiSet(discord.GuildID(g), emojis, false)
	}
	at = report(fmt.Sprintf("emoji (%d)", measureGuilds*measureEmoji), at)

	fillMembers(cab, measureVisitedGuilds)
	at = report(fmt.Sprintf("members+presences (%d guilds)", measureVisitedGuilds), at)

	for g := 1; g <= measureVisitedGuilds-3; g++ {
		dropGuildMembers(cab, discord.GuildID(g), 0)
	}
	at = heapInUse()
	t.Logf("%-40s %7.1f MB", "members after pruning all but 3 guilds", float64(at-base)/(1<<20))

	t.Logf("sizeof(discord.Message) = %d bytes", unsafe.Sizeof(discord.Message{}))
	def := defaultstore.NewMessage(storeMessagesPerChannel)
	fillMessages(def, storeChannels, storeMessagesPerChannel)
	at = report(fmt.Sprintf("messages full (%dx%d)", storeChannels, storeMessagesPerChannel), at)
	_ = def.Reset()
	at = heapInUse()

	rs := newRecentStore(storeMessagesPerChannel, storeChannels)
	for c := 0; c < storeOpenedChannels; c++ {
		rs.MarkOpened(discord.ChannelID(1e6 + 2000 - storeOpenedChannels + c))
	}
	fillMessages(rs, 2000, storeMessagesPerChannel)
	at = report("messages recentStore after 2000 channels", at)
	_ = rs.Reset()
	at = heapInUse()

	t.Logf("%-40s %7.1f MB", "total live", float64(at-base)/(1<<20))
	runtime.KeepAlive(cab)
	runtime.KeepAlive(rs)
}

func fillMembers(cab *store.Cabinet, guilds int) {
	for g := 1; g <= guilds; g++ {
		gid := discord.GuildID(g)
		for i := 0; i < measureListMembers+measureAuthors; i++ {
			id := g*10000 + i
			m := fakeMember(id)
			_ = cab.MemberSet(gid, &m, false)
			if i < measureListMembers {
				p := fakePresence(gid, id)
				_ = cab.PresenceSet(gid, &p, false)
			}
		}
	}
}

func fillMessages(s store.MessageStore, channels, per int) {
	for c := 0; c < channels; c++ {
		ch := discord.ChannelID(1e6 + c)
		for i := 0; i < per; i++ {
			m := fakeMessage(ch, 1, i)
			_ = s.MessageSet(&m, false)
		}
	}
}
