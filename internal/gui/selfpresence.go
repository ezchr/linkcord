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
	p := discord.Presence{User: c.me, Status: status, Activities: latestActivities(activities)}
	if old, _ := c.st.PresenceStore.Presence(0, c.me.ID); old != nil {
		if activities == nil {
			p.Activities = old.Activities
		}
		if status == "" {
			p.Status = old.Status
		}
	}
	c.st.PresenceStore.PresenceSet(0, &p, true)
}

// latestActivities are activities with one of each kind and program, the
// last given: sessions and programs that reconnected repeat what they
// showed, and an old song stayed above the one playing.
func latestActivities(acts []discord.Activity) []discord.Activity {
	if len(acts) < 2 {
		return acts
	}
	type key struct {
		typ  discord.ActivityType
		name string
		app  discord.AppID
	}
	last := make(map[key]int, len(acts))
	for i, a := range acts {
		last[key{a.Type, a.Name, a.AppID}] = i
	}
	out := make([]discord.Activity, 0, len(last))
	for i, a := range acts {
		if last[key{a.Type, a.Name, a.AppID}] == i {
			out = append(out, a)
		}
	}
	return out
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
			// The status is the one the user chose here, which the sessions
			// may not say yet, or say as another device has it: idle or do
			// not disturb went back to online.
			if old, _ := c.st.PresenceStore.Presence(0, c.me.ID); old != nil && old.Status != "" {
				status = ""
			}
			c.setMyPresence(status, activities)
		})
	})
}
