package gui

import (
	"encoding/json"
	"testing"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/gateway"
	"github.com/ayn2op/arikawa/v3/state/store/defaultstore"
)

func TestThreadMembershipFromEvents(t *testing.T) {
	me := discord.UserID(1)
	other := discord.UserID(2)
	var s threadState

	// The threads of a server in the ready event are those joined.
	s.noteGuild(gateway.GuildCreateEvent{Threads: []discord.Channel{{ID: 10, Type: discord.GuildPublicThread}}})
	if !s.joined[10] {
		t.Fatal("thread from the ready event not joined")
	}

	steps := []struct {
		name string
		ev   gateway.Event
		id   discord.ChannelID
		want bool
	}{
		{"created by someone else", &gateway.ThreadCreateEvent{Channel: discord.Channel{ID: 11, OwnerID: other}}, 11, false},
		{"created by me", &gateway.ThreadCreateEvent{Channel: discord.Channel{ID: 12, OwnerID: me}}, 12, true},
		{"added with member", &gateway.ThreadCreateEvent{Channel: discord.Channel{ID: 13, OwnerID: other, ThreadMember: &discord.ThreadMember{}}}, 13, true},
		{"someone else added", &gateway.ThreadMembersUpdateEvent{ID: 11, AddedMembers: []discord.ThreadMember{{UserID: other}}}, 11, false},
		{"me added", &gateway.ThreadMembersUpdateEvent{ID: 11, AddedMembers: []discord.ThreadMember{{ID: 11, UserID: me}}}, 11, true},
		{"me removed", &gateway.ThreadMembersUpdateEvent{ID: 11, RemovedMemberIDs: []discord.UserID{me}}, 11, false},
		{"my member updated", &gateway.ThreadMemberUpdateEvent{ThreadMember: discord.ThreadMember{ID: 14, UserID: me}}, 14, true},
		{"sync with my member", &gateway.ThreadListSyncEvent{Members: []discord.ThreadMember{{ID: 15, UserID: me}}}, 15, true},
		{"update keeps joined", &gateway.ThreadUpdateEvent{Channel: discord.Channel{ID: 10}}, 10, true},
		{"deleted", &gateway.ThreadDeleteEvent{ID: 10}, 10, false},
	}
	for _, st := range steps {
		if !s.note(st.ev, me) {
			t.Errorf("%s: not seen as a thread event", st.name)
		}
		if got := s.joined[st.id]; got != st.want {
			t.Errorf("%s: joined = %v, want %v", st.name, got, st.want)
		}
	}
	if s.note(&gateway.MessageCreateEvent{}, me) {
		t.Error("a message counted as a thread event")
	}
}

func TestListedThreads(t *testing.T) {
	parent := discord.ChannelID(100)
	archived := &discord.ThreadMetadata{Archived: true}
	chs := []discord.Channel{
		{ID: 5, Type: discord.GuildPublicThread, ParentID: parent},
		{ID: 3, Type: discord.GuildPrivateThread, ParentID: parent},
		{ID: 4, Type: discord.GuildPublicThread, ParentID: parent},                           // not joined
		{ID: 6, Type: discord.GuildPublicThread, ParentID: parent, ThreadMetadata: archived}, // joined, archived
		{ID: 7, Type: discord.GuildPublicThread, ParentID: 200},                              // another channel
		{ID: 8, Type: discord.GuildText, ParentID: parent},                                   // not a thread
	}
	joined := map[discord.ChannelID]bool{3: true, 5: true, 6: true, 7: true}
	got := ids(listedThreads(chs, parent, joined, 0))
	if want := []discord.ChannelID{3, 5}; !equalIDs(got, want) {
		t.Errorf("listed %v, want %v", got, want)
	}
	// The one open is listed, joined or not, archived or not.
	got = ids(listedThreads(chs, parent, joined, 4))
	if want := []discord.ChannelID{3, 4, 5}; !equalIDs(got, want) {
		t.Errorf("listed with 4 open %v, want %v", got, want)
	}
	got = ids(listedThreads(chs, parent, joined, 6))
	if want := []discord.ChannelID{3, 5, 6}; !equalIDs(got, want) {
		t.Errorf("listed with 6 open %v, want %v", got, want)
	}
	got = ids(activeThreads(chs, parent))
	if len(got) != 3 {
		t.Errorf("active %v, want 3 threads", got)
	}
}

func TestThreadPerms(t *testing.T) {
	base := discord.PermissionViewChannel | discord.PermissionSendMessages
	if p := threadPerms(base, false); p.Has(discord.PermissionSendMessages) {
		t.Error("Send Messages alone lets the user send in threads")
	}
	inThreads := base | discord.PermissionSendMessagesInThreads
	if p := threadPerms(inThreads, false); !p.Has(discord.PermissionSendMessages) || !p.Has(discord.PermissionViewChannel) {
		t.Error("Send Messages in Threads does not let the user send")
	}
	if p := threadPerms(inThreads, true); p.Has(discord.PermissionSendMessages) {
		t.Error("the user sends in a locked thread")
	}
	if p := threadPerms(inThreads|discord.PermissionManageThreads, true); !p.Has(discord.PermissionSendMessages) {
		t.Error("a moderator cannot send in a locked thread")
	}
	// Only Send Messages in Threads counts in a thread.
	if p := threadPerms(discord.PermissionSendMessagesInThreads, false); !p.Has(discord.PermissionSendMessages) {
		t.Error("Send Messages in Threads without Send Messages does not let the user send")
	}
}

func TestThreadMemberJSON(t *testing.T) {
	// Discord sends the user's member of a thread as "member".
	var ch discord.Channel
	if err := json.Unmarshal([]byte(`{"id":"10","type":11,"member":{"join_timestamp":"2024-01-01T00:00:00+00:00","flags":1}}`), &ch); err != nil {
		t.Fatal(err)
	}
	if ch.ThreadMember == nil || ch.ThreadMember.Flags != 1 {
		t.Errorf("member not read: %+v", ch.ThreadMember)
	}
}

func TestThreadStarterAndName(t *testing.T) {
	if _, ok := threadStarter(discord.Message{Type: discord.ThreadStarterMessage}); ok {
		t.Error("a starter without its message shows one")
	}
	th := discord.Channel{ID: 9}
	ref := discord.Message{ID: 9, Content: "hello", Thread: &th}
	got, ok := threadStarter(discord.Message{Type: discord.ThreadStarterMessage, ReferencedMessage: &ref})
	if !ok || got.Content != "hello" || got.Thread != nil {
		t.Errorf("starter = %+v, %v", got, ok)
	}
	if ref.Thread == nil {
		t.Error("the starter changed the message it quotes")
	}
	long := discord.Message{Content: "  this   is a rather long message that goes on and on and on  "}
	if n := defaultThreadName(&long); n != "this is a rather long message that goes " {
		t.Errorf("name %q", n)
	}
	if n := defaultThreadName(&discord.Message{}); n != "New Thread" {
		t.Errorf("name of an empty message %q", n)
	}
	if messageCount(1) != "1 Message" || messageCount(12) != "12 Messages" {
		t.Error("message counts")
	}
}

func ids(chs []discord.Channel) []discord.ChannelID {
	out := make([]discord.ChannelID, len(chs))
	for i, ch := range chs {
		out[i] = ch.ID
	}
	return out
}

func equalIDs(a, b []discord.ChannelID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestMessageUpdateKeepsThread(t *testing.T) {
	// Discord tells of a thread started from a message by updating it.
	old := discord.Message{ID: 1, Content: "hi"}
	th := discord.Channel{ID: 1, Name: "talk"}
	defaultstore.DiffMessage(&discord.Message{ID: 1, Thread: &th}, &old)
	if old.Thread == nil || old.Thread.Name != "talk" || old.Flags&discord.MessageHasThread == 0 || old.Content != "hi" {
		t.Errorf("updated message %+v", old)
	}
}
