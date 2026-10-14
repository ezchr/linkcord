package gui

import (
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/gateway"
)

// Discord does not tell the user their own presence as it tells others':
// it sends the sessions of the account instead, with "all" for what others
// see. So the user's own status and activities are kept from those, and set
// at once when changed here, rather than staying as they were at sign-in.

// setMyPresence keeps status and activities as the user's own presence.
func (c *chatView) setMyPresence(status discord.Status, activities []discord.Activity) {
	p := discord.Presence{User: c.me, Status: status, Activities: activities}
	if old, _ := c.st.PresenceStore.Presence(0, c.me.ID); old != nil && activities == nil {
		p.Activities = old.Activities
	}
	c.st.PresenceStore.PresenceSet(0, &p, true)
}

// watchMyPresence keeps the user's own presence as their sessions say.
func (c *chatView) watchMyPresence() func() {
	return c.st.AddHandler(func(ev *gateway.SessionsReplaceEvent) {
		var mine *gateway.UserSession
		for i, s := range *ev {
			if s.SessionID == "all" {
				mine = &(*ev)[i]
				break
			}
			if mine == nil && s.Active {
				mine = &(*ev)[i]
			}
		}
		if mine == nil || mine.Status == "" {
			return
		}
		status, activities := mine.Status, mine.Activities
		c.u.post(func() {
			c.setMyPresence(status, activities)
		})
	})
}
