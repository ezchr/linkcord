package gui

import (
	"errors"
	"regexp"
	"slices"
	"testing"

	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/utils/httputil"
	"github.com/ayn2op/arikawa/v3/utils/json"
)

// The request that forwards a message is a reference of type 1 to it, with
// a nonce and no text, as the web app sends it.
func TestForwardData(t *testing.T) {
	src := discord.Message{ID: 300, ChannelID: 200, Content: "not sent again"}
	body := func(data api.SendMessageData) string {
		t.Helper()
		j, err := json.Marshal(data)
		if err != nil {
			t.Fatal(err)
		}
		return regexp.MustCompile(`"nonce":"\d+"`).ReplaceAllString(string(j), `"nonce":"N"`)
	}
	want := `{"nonce":"N","tts":false,"message_reference":{"type":1,"message_id":"300","channel_id":"200","guild_id":"100"},"flags":0}`
	if got := body(forwardData(src, 100)); got != want {
		t.Errorf("in a server:\n got %s\nwant %s", got, want)
	}
	// A direct message has no server.
	want = `{"nonce":"N","tts":false,"message_reference":{"type":1,"message_id":"300","channel_id":"200"},"flags":0}`
	if got := body(forwardData(src, 0)); got != want {
		t.Errorf("in a direct message:\n got %s\nwant %s", got, want)
	}
	// The server of the message itself counts before the channel's.
	src.GuildID = 7
	if ref := forwardData(src, 100).Reference; ref.GuildID != 7 {
		t.Errorf("guild %d, want the message's", ref.GuildID)
	}
	if a, b := forwardData(src, 0).Nonce, forwardData(src, 0).Nonce; a == b || a == "" {
		t.Errorf("nonces %q and %q, want two different ones", a, b)
	}
}

func TestForwardable(t *testing.T) {
	plain := discord.Message{ID: 1, Content: "hi"}
	with := func(f func(*discord.Message)) discord.Message {
		m := plain
		f(&m)
		return m
	}
	tests := []struct {
		name          string
		m             discord.Message
		kept, canRead bool
		want          bool
	}{
		{"plain", plain, false, true, true},
		{"reply", with(func(m *discord.Message) { m.Type = discord.InlinedReplyMessage }), false, true, true},
		{"join message", with(func(m *discord.Message) { m.Type = discord.GuildMemberJoinMessage }), false, true, false},
		{"thread created", with(func(m *discord.Message) { m.Type = discord.ThreadCreatedMessage }), false, true, false},
		{"only the user sees it", with(func(m *discord.Message) { m.Flags = discord.EphemeralMessage }), false, true, false},
		{"poll", with(func(m *discord.Message) { m.Poll = &discord.Poll{} }), false, true, false},
		{"deleted, kept here", plain, true, true, false},
		{"no history", plain, false, false, false},
		{"not sent yet", with(func(m *discord.Message) { m.ID = 0 }), false, true, false},
	}
	for _, tt := range tests {
		if got := forwardable(tt.m, tt.kept, tt.canRead); got != tt.want {
			t.Errorf("%s: %v, want %v", tt.name, got, tt.want)
		}
	}
}

func destNames(ds []forwardDest) []string {
	var out []string
	for _, d := range ds {
		out = append(out, d.name)
	}
	return out
}

func TestDMDests(t *testing.T) {
	chs := []discord.Channel{
		{ID: 1, Type: discord.DirectMessage, Name: "old", LastMessageID: 10},
		{ID: 2, Type: discord.GroupDM, Name: "group", LastMessageID: 30},
		{ID: 3, Type: discord.DirectMessage, Name: "request", LastMessageID: 40},
		{ID: 4, Type: discord.DirectMessage, Name: "new", LastMessageID: 20},
		// One never written in counts from when it was made.
		{ID: 50, Type: discord.DirectMessage, Name: "empty"},
	}
	got := destNames(dmDests(chs, map[discord.ChannelID]bool{3: true}, func(ch discord.Channel) string { return ch.Name }))
	if want := []string{"empty", "group", "new", "old"}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestGuildDests(t *testing.T) {
	g := discord.Guild{ID: 1, Name: "Server"}
	archived := &discord.ThreadMetadata{Archived: true}
	chs := []discord.Channel{
		{ID: 10, Type: discord.GuildCategory, Name: "second", Position: 2},
		{ID: 11, Type: discord.GuildCategory, Name: "first", Position: 1},
		{ID: 20, Type: discord.GuildText, Name: "in-second", ParentID: 10, Position: 0},
		{ID: 21, Type: discord.GuildText, Name: "b", ParentID: 11, Position: 5},
		{ID: 22, Type: discord.GuildText, Name: "a", ParentID: 11, Position: 4},
		{ID: 23, Type: discord.GuildAnnouncement, Name: "top", Position: 9},
		{ID: 24, Type: discord.GuildText, Name: "read-only", ParentID: 11, Position: 1},
		{ID: 25, Type: discord.GuildVoice, Name: "voice", ParentID: 11, Position: 0},
		{ID: 26, Type: discord.GuildForum, Name: "forum", ParentID: 11, Position: 0},
		{ID: 30, Type: discord.GuildPublicThread, Name: "joined", ParentID: 22},
		{ID: 31, Type: discord.GuildPublicThread, Name: "not joined", ParentID: 22},
		{ID: 32, Type: discord.GuildPublicThread, Name: "archived", ParentID: 22, ThreadMetadata: archived},
		{ID: 33, Type: discord.GuildPublicThread, Name: "under read-only", ParentID: 24},
	}
	joined := map[discord.ChannelID]bool{30: true, 32: true, 33: true}
	canSend := func(ch discord.Channel) bool { return ch.ID != 24 }
	ds := guildDests(g, chs, joined, canSend)
	// A thread can be written in though its channel cannot.
	want := []string{"# top", "under read-only", "# a", "joined", "# b", "# in-second"}
	if got := destNames(ds); !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if ds[2].place != "Server" || ds[3].place != "Server › # a" {
		t.Errorf("places %q and %q", ds[2].place, ds[3].place)
	}
}

func TestFilterDests(t *testing.T) {
	all := []forwardDest{
		{name: "Ana"},
		{name: "Mariana"},
		{name: "# general", place: "Anagrams"},
		{name: "# announcements", place: "Games"},
		{name: "ideas", place: "Games › # general"},
	}
	tests := []struct {
		query string
		want  []string
	}{
		{"", []string{"Ana", "Mariana", "# general", "# announcements", "ideas"}},
		{"  ", []string{"Ana", "Mariana", "# general", "# announcements", "ideas"}},
		// By the start of the name, then inside it, then by the server.
		{"an", []string{"Ana", "# announcements", "Mariana", "# general"}},
		{"ANA", []string{"Ana", "Mariana", "# general"}},
		{"#gen", []string{"# general", "ideas"}},
		{"games", []string{"# announcements", "ideas"}},
		{"nothing", nil},
	}
	for _, tt := range tests {
		if got := destNames(filterDests(all, tt.query)); !slices.Equal(got, tt.want) {
			t.Errorf("%q: got %v, want %v", tt.query, got, tt.want)
		}
	}
}

func TestForwardPlan(t *testing.T) {
	// Each message to each place, the oldest first, then the text.
	got := forwardPlan(2, 2, true, nil, nil)
	want := []forwardStep{
		{msg: 0, dest: 0}, {msg: 0, dest: 1},
		{msg: 1, dest: 0}, {msg: 1, dest: 1},
		{msg: 1, dest: 0, note: true}, {msg: 1, dest: 1, note: true},
	}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if got := forwardPlan(1, 1, false, nil, nil); !slices.Equal(got, []forwardStep{{}}) {
		t.Errorf("one message to one place: %v", got)
	}
	// Sent again, a place gets only what it lacks.
	got = forwardPlan(3, 2, true, []int{3, 1}, []bool{true, false})
	want = []forwardStep{{msg: 1, dest: 1}, {msg: 2, dest: 1}, {msg: 2, dest: 1, note: true}}
	if !slices.Equal(got, want) {
		t.Errorf("again: got %v, want %v", got, want)
	}
	if got := forwardPlan(2, 1, false, []int{2}, nil); len(got) != 0 {
		t.Errorf("nothing left: %v", got)
	}
}

func TestSendErrorText(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{&httputil.HTTPError{Status: 429, Code: 20016, Message: "slowmode"}, "Slow mode: wait before sending here again"},
		{&httputil.HTTPError{Status: 429}, "Discord asked to slow down; try again later"},
		{&httputil.HTTPError{Status: 403, Code: 50013}, "You do not have permission to send messages here"},
		{&httputil.HTTPError{Status: 403, Code: 50007}, "This person does not accept your messages"},
		{&httputil.HTTPError{Status: 404}, "The channel or the message is gone"},
		{&httputil.HTTPError{Status: 400, Message: "Invalid Form Body"}, "Invalid Form Body"},
		{errors.New("no network"), "no network"},
	}
	for _, tt := range tests {
		if got := sendErrorText(tt.err); got != tt.want {
			t.Errorf("%v: got %q, want %q", tt.err, got, tt.want)
		}
	}
}
