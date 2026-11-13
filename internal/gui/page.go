package gui

import (
	"sync/atomic"

	"github.com/ayn2op/arikawa/v3/discord"
)

// The page of Discord's web app that shows what Linkcord shows, which a
// browser tab sends as the Referer of its requests.

// shownPage is the address of that page, read by requests on any goroutine.
var shownPage atomic.Value // string

// currentPage is the page shown, "" before anything is.
func currentPage() string {
	p, _ := shownPage.Load().(string)
	return p
}

// pageURL is the web app's address of a channel of a server, or of a
// direct message when guildID is not valid; with no channel, of the server
// or of the direct messages.
func pageURL(guildID discord.GuildID, chID discord.ChannelID) string {
	g := "@me"
	if guildID.IsValid() {
		g = guildID.String()
	}
	u := "https://discord.com/channels/" + g
	if chID.IsValid() {
		u += "/" + chID.String()
	}
	return u
}

// notePage keeps the page of what the chat shows.
func (c *chatView) notePage() {
	if c.demo {
		return
	}
	var ch discord.ChannelID
	if c.selChannel != nil {
		ch = c.selChannel.ID
	}
	shownPage.Store(pageURL(c.selGuild, ch))
}
