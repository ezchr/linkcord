package gui

import (
	"image"
	"log/slog"
	"time"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"github.com/ayn2op/arikawa/v3/discord"
)

// Who reacted shows in each reaction, their pictures beside the count, as
// Vencord's WhoReacted does. Discord says it only when asked, message by
// message, so it is asked for the reactions seen, one at a time.

// shownReactors is how many of those who reacted show in a reaction.
const shownReactors = 3

type reactorsKey struct {
	msg   discord.MessageID
	emoji string
}

// reactorsEntry is who reacted, as last asked, for count reactions.
type reactorsEntry struct {
	count   int
	users   []discord.User
	pending bool
}

type reactorsJob struct {
	key reactorsKey
	ch  discord.ChannelID
	e   discord.Emoji
}

// reactorsOf are some of those who reacted with r to m, asked for when not
// known or when the count changed.
func (c *chatView) reactorsOf(m discord.Message, r discord.Reaction) []discord.User {
	if c.u.app.NoWhoReacted {
		return nil
	}
	if c.demo {
		return c.demoReactors(r)
	}
	if c.reactors == nil {
		c.reactors = make(map[reactorsKey]*reactorsEntry)
	}
	k := reactorsKey{m.ID, emojiKey(r.Emoji)}
	e := c.reactors[k]
	if e == nil {
		e = new(reactorsEntry)
		c.reactors[k] = e
	}
	if e.count != r.Count && !e.pending {
		e.pending = true
		c.queueReactors(reactorsJob{k, m.ChannelID, r.Emoji})
	}
	return e.users
}

// queueReactors asks Discord who reacted, a reaction at a time, so that the
// reactions of a whole chat do not hit its limits.
func (c *chatView) queueReactors(job reactorsJob) {
	if c.reactorJobs == nil {
		c.reactorJobs = make(chan reactorsJob, 256)
		go func() {
			for {
				select {
				case <-c.done:
					return
				case j := <-c.reactorJobs:
					users, err := c.st.Reactions(j.ch, j.key.msg, j.e.APIString(), shownReactors)
					if err != nil {
						slog.Debug("could not get who reacted", "err", err)
					}
					c.u.post(func() {
						e := c.reactors[j.key]
						if e == nil {
							return
						}
						e.pending = false
						if err == nil {
							e.users = users
							// The count it is for, read when it was asked.
							if i := c.messageIndex(j.key.msg); i >= 0 {
								for _, r := range c.messages[i].Reactions {
									if emojiKey(r.Emoji) == j.key.emoji {
										e.count = r.Count
									}
								}
							}
						} else {
							// Not asked again for these.
							e.count = -1
						}
					})
					time.Sleep(300 * time.Millisecond)
				}
			}
		}()
	}
	select {
	case c.reactorJobs <- job:
	default:
		// Too many waiting: asked again when seen later.
		if e := c.reactors[job.key]; e != nil {
			e.pending = false
		}
	}
}

// demoReactors are made-up people for the demo's reactions.
func (c *chatView) demoReactors(r discord.Reaction) []discord.User {
	var out []discord.User
	if r.Me {
		out = append(out, c.me)
	}
	if members, err := c.st.Cabinet.Members(c.selGuild); err == nil {
		for _, m := range members {
			if len(out) >= min(r.Count, shownReactors) {
				break
			}
			if m.User.ID != c.me.ID && !m.User.Bot {
				out = append(out, m.User)
			}
		}
	}
	return out
}

// layoutReactors are the pictures of users, overlapping, in a reaction.
func (c *chatView) layoutReactors(gtx layout.Context, users []discord.User) layout.Dimensions {
	n := min(len(users), shownReactors)
	if n == 0 {
		return layout.Dimensions{}
	}
	guildID := discord.NullGuildID
	if c.selChannel != nil {
		guildID = c.selChannel.GuildID
	}
	s, step := gtx.Dp(16), gtx.Dp(11)
	pad := gtx.Dp(6)
	for i := n - 1; i >= 0; i-- {
		u := users[i]
		t := op.Offset(image.Pt(pad+i*step, 0)).Push(gtx.Ops)
		c.u.picture(gtx, c.authorAvatarURL(guildID, u), unit.Dp(16), colorFor(uint64(u.ID)), u.DisplayOrUsername(), -1)
		t.Pop()
	}
	return layout.Dimensions{Size: image.Pt(pad+(n-1)*step+s, s)}
}
