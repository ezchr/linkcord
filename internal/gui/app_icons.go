package gui

import (
	"sync"
	"sync/atomic"

	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/discord"
)

// appIcons keeps the icons of applications, asked for once each.
var appIcons sync.Map // discord.AppID → *appIcon

type appIcon struct {
	once sync.Once
	// hash is set before done.
	hash string
	done atomic.Bool
}

// appIconURL is the icon of the application id, which Discord shows for an
// activity without a picture of its own, as games it detects; empty until
// it arrives, or if it has none.
func (c *chatView) appIconURL(id discord.AppID) string {
	if c.demo || !id.IsValid() {
		return ""
	}
	v, _ := appIcons.LoadOrStore(id, new(appIcon))
	icon := v.(*appIcon)
	icon.once.Do(func() {
		go func() {
			info, err := c.st.Client.RequestJSON[struct {
				Icon string `json:"icon"`
			}]("GET", api.Endpoint+"applications/"+id.String()+"/rpc")
			if err == nil {
				icon.hash = info.Icon
			}
			icon.done.Store(true)
			c.u.win.Invalidate()
		}()
	})
	if !icon.done.Load() || icon.hash == "" {
		return ""
	}
	return "https://cdn.discordapp.com/app-icons/" + id.String() + "/" + icon.hash + ".png?size=128"
}
