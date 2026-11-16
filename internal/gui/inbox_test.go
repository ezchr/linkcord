package gui

import (
	"path/filepath"
	"testing"

	"github.com/ayn2op/arikawa/v3/discord"
)

func TestClassifyInbox(t *testing.T) {
	const me, other = discord.UserID(1), discord.UserID(2)
	const role = discord.RoleID(50)
	guild := discord.GuildID(10)
	mine := discord.Message{ID: 90, Author: discord.User{ID: me}}
	theirs := discord.Message{ID: 91, Author: discord.User{ID: other}}
	base := func(f func(m *discord.Message)) discord.Message {
		m := discord.Message{ID: 100, GuildID: guild, ChannelID: 20, Author: discord.User{ID: other}, Content: "hi"}
		f(&m)
		return m
	}
	rules := inboxRules{me: me, roles: []discord.RoleID{role}}
	tests := []struct {
		name string
		m    discord.Message
		r    inboxRules
		want inboxKind
	}{
		{"plain", base(func(m *discord.Message) {}), rules, inboxNone},
		{"mention", base(func(m *discord.Message) { m.Mentions = []discord.GuildUser{{User: discord.User{ID: me}}} }), rules, inboxMention},
		{"mention in text", base(func(m *discord.Message) { m.Content = "<@!1> look" }), rules, inboxMention},
		{"own message", base(func(m *discord.Message) {
			m.Author.ID = me
			m.Content = "<@1>"
		}), rules, inboxNone},
		{"direct message", base(func(m *discord.Message) {
			m.GuildID = 0
			m.Content = "<@1>"
		}), rules, inboxNone},
		{"blocked", base(func(m *discord.Message) { m.Content = "<@1>" }), inboxRules{me: me, blocked: true}, inboxNone},
		{"everyone", base(func(m *discord.Message) { m.MentionEveryone = true }), rules, inboxMention},
		{"everyone suppressed", base(func(m *discord.Message) { m.MentionEveryone = true }), inboxRules{me: me, suppressEveryone: true}, inboxNone},
		{"everyone in silent channel", base(func(m *discord.Message) { m.MentionEveryone = true }), inboxRules{me: me, silent: true}, inboxNone},
		{"direct mention in silent channel", base(func(m *discord.Message) { m.Content = "<@1>" }), inboxRules{me: me, silent: true}, inboxMention},
		{"my role", base(func(m *discord.Message) { m.MentionRoleIDs = []discord.RoleID{role} }), rules, inboxMention},
		{"other role", base(func(m *discord.Message) { m.MentionRoleIDs = []discord.RoleID{51} }), rules, inboxNone},
		{"other role, found by Discord", base(func(m *discord.Message) { m.MentionRoleIDs = []discord.RoleID{51} }), inboxRules{me: me, anyRole: true}, inboxMention},
		{"roles suppressed", base(func(m *discord.Message) { m.MentionRoleIDs = []discord.RoleID{role} }), inboxRules{me: me, roles: rules.roles, suppressRoles: true}, inboxNone},
		{"reply without ping", base(func(m *discord.Message) {
			m.Type, m.ReferencedMessage = discord.InlinedReplyMessage, &mine
			m.Reference = &discord.MessageReference{MessageID: mine.ID}
		}), rules, inboxReply},
		{"reply with ping", base(func(m *discord.Message) {
			m.Type, m.ReferencedMessage = discord.InlinedReplyMessage, &mine
			m.Mentions = []discord.GuildUser{{User: discord.User{ID: me}}}
		}), rules, inboxReply},
		{"reply to someone else", base(func(m *discord.Message) {
			m.Type, m.ReferencedMessage = discord.InlinedReplyMessage, &theirs
		}), rules, inboxNone},
		{"forward of mine", base(func(m *discord.Message) {
			m.ReferencedMessage = &mine
			m.Reference = &discord.MessageReference{Type: discord.MessageReferenceTypeForward}
		}), rules, inboxNone},
		{"thread starter of mine", base(func(m *discord.Message) {
			m.Type, m.ReferencedMessage = discord.ThreadStarterMessage, &mine
		}), rules, inboxNone},
	}
	for _, tt := range tests {
		if got := classifyInbox(tt.m, tt.r); got != tt.want {
			t.Errorf("%s: got %d, want %d", tt.name, got, tt.want)
		}
	}
}

func inboxIDs(l inboxList) []discord.MessageID {
	var ids []discord.MessageID
	for _, e := range l {
		ids = append(ids, e.Message.ID)
	}
	return ids
}

func TestInboxListOrderAndDedup(t *testing.T) {
	var l inboxList
	for _, id := range []discord.MessageID{5, 9, 1, 7} {
		l.add(inboxEntry{Message: discord.Message{ID: id, Content: "a"}, Kind: inboxMention})
	}
	if got := inboxIDs(l); len(got) != 4 || got[0] != 9 || got[1] != 7 || got[2] != 5 || got[3] != 1 {
		t.Fatalf("order = %v, want 9 7 5 1", got)
	}
	// The same message again replaces it and keeps it read.
	l[1].Read = true
	if !l.add(inboxEntry{Message: discord.Message{ID: 7, Content: "edited"}, Kind: inboxMention}) {
		t.Fatal("an update did not change the list")
	}
	if len(l) != 4 || l[1].Message.Content != "edited" || !l[1].Read {
		t.Fatalf("after update: %+v", l[1])
	}
	if l.add(inboxEntry{Message: discord.Message{ID: 3}, Kind: inboxNone}) || len(l) != 4 {
		t.Fatal("a message for no one was added")
	}
	if !l.remove(5) || l.remove(5) || len(l) != 3 {
		t.Fatalf("remove: %v", inboxIDs(l))
	}
}

func TestInboxListCap(t *testing.T) {
	var l inboxList
	for i := 1; i <= inboxMax+20; i++ {
		l.add(inboxEntry{Message: discord.Message{ID: discord.MessageID(i)}, Kind: inboxReply})
	}
	if len(l) != inboxMax {
		t.Fatalf("len = %d, want %d", len(l), inboxMax)
	}
	if l[0].Message.ID != inboxMax+20 || l[inboxMax-1].Message.ID != 21 {
		t.Fatalf("kept %d..%d, want the newest", l[0].Message.ID, l[inboxMax-1].Message.ID)
	}
	// An older one than all kept stays out.
	if l.add(inboxEntry{Message: discord.Message{ID: 2}, Kind: inboxReply}) || len(l) != inboxMax {
		t.Fatal("an old message pushed in")
	}
}

func TestInboxListDropsNestedReference(t *testing.T) {
	var l inboxList
	deep := discord.Message{ID: 1}
	ref := discord.Message{ID: 2, ReferencedMessage: &deep}
	l.add(inboxEntry{Message: discord.Message{ID: 3, ReferencedMessage: &ref}, Kind: inboxReply})
	if r := l[0].Message.ReferencedMessage; r == nil || r.ID != 2 || r.ReferencedMessage != nil {
		t.Fatalf("reference kept as %+v", r)
	}
	if ref.ReferencedMessage == nil {
		t.Fatal("the caller's message was changed")
	}
}

func TestInboxPersistRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inbox.json")
	const me = discord.UserID(1)
	mine := discord.Message{ID: 40, Author: discord.User{ID: me}, Content: "mine"}
	var l inboxList
	l.add(inboxEntry{Message: discord.Message{ID: 50, GuildID: 10, ChannelID: 20, Author: discord.User{ID: 2, Username: "ana"}, Content: "<@1> hi"}, Kind: inboxMention})
	l.add(inboxEntry{Message: discord.Message{ID: 60, GuildID: 10, ChannelID: 21, Author: discord.User{ID: 3}, Type: discord.InlinedReplyMessage, ReferencedMessage: &mine}, Kind: inboxReply, Read: true})
	if err := writeInbox(path, me, l); err != nil {
		t.Fatal(err)
	}
	got := readInbox(path, me)
	if len(got) != 2 || got[0].Message.ID != 60 || got[1].Message.ID != 50 {
		t.Fatalf("read back %v", inboxIDs(got))
	}
	if got[0].Kind != inboxReply || !got[0].Read || got[0].Message.ReferencedMessage == nil || got[0].Message.ReferencedMessage.Content != "mine" {
		t.Fatalf("reply read back as %+v", got[0])
	}
	if got[1].Kind != inboxMention || got[1].Read || got[1].Message.Author.Username != "ana" || got[1].Message.GuildID != 10 {
		t.Fatalf("mention read back as %+v", got[1])
	}
	// Another account does not see them.
	if other := readInbox(path, 2); len(other) != 0 {
		t.Fatalf("another account read %v", inboxIDs(other))
	}
	if missing := readInbox(filepath.Join(t.TempDir(), "none.json"), me); missing != nil {
		t.Fatal("a missing file gave entries")
	}
}
