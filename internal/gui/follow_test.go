package gui

import (
	"testing"
	"time"

	"gioui.org/app"
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/gateway"
)

// TestFollowIntoPrivateChannel follows someone, in the demo's way of
// joining, into a channel that hides from @everyone, and tries again a
// join that was missed.
func TestFollowIntoPrivateChannel(t *testing.T) {
	const guild = discord.GuildID(1)
	st := newState("fixture")
	c := &chatView{u: &ui{win: new(app.Window)}, st: st, demo: true, me: discord.User{ID: 100}, done: make(chan struct{})}
	defer close(c.done)
	public := discord.Channel{ID: 10, GuildID: guild, Type: discord.GuildVoice, Name: "general"}
	private := discord.Channel{ID: 11, GuildID: guild, Type: discord.GuildVoice, Name: "private",
		Overwrites: []discord.Overwrite{{ID: discord.Snowflake(guild), Type: discord.OverwriteRole, Deny: discord.PermissionViewChannel}}}
	st.Cabinet.GuildSet(&discord.Guild{ID: guild, OwnerID: 100}, false)
	st.Cabinet.ChannelSet(&public, false)
	st.Cabinet.ChannelSet(&private, false)
	friend := discord.UserID(200)
	move := func(ch discord.ChannelID) {
		vs := discord.VoiceState{GuildID: guild, ChannelID: ch, UserID: friend}
		st.Cabinet.VoiceStateSet(guild, &vs, false)
		c.voiceExtras(&gateway.VoiceStateUpdateEvent{VoiceState: vs})
	}

	move(public.ID)
	c.toggleFollow(guild, friend)
	if c.u.voice.demoChannel != public.ID {
		t.Fatalf("did not join them where they were: in %v", c.u.voice.demoChannel)
	}
	move(private.ID)
	if c.u.voice.demoChannel != private.ID {
		t.Fatalf("did not follow into the private channel: in %v", c.u.voice.demoChannel)
	}

	// A join that did not happen is tried again, but not at once.
	c.u.voice.demoChannel = public.ID
	c.followTo(private.ID)
	if c.u.voice.demoChannel != public.ID {
		t.Fatal("tried again at once")
	}
	c.followAt = time.Now().Add(-time.Minute)
	c.followTo(private.ID)
	if c.u.voice.demoChannel != private.ID {
		t.Fatal("a missed join was not tried again")
	}

	c.toggleFollow(guild, friend)
	move(public.ID)
	if c.u.voice.demoChannel != private.ID {
		t.Fatal("followed after stopping")
	}
}
