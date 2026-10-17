package gui

import "github.com/ayn2op/arikawa/v3/discord"

// devIDs are the Discord accounts of Linkcord's developers. They get the
// debug tools, such as the voice key-commit button that reproduces the
// broken-channel bug, which would let anyone else break a voice channel.
var devIDs = map[discord.UserID]bool{
	1159947404620075008: true, // ezchr
	749345855290474647:  true, // linkmail666
}

// isDev reports whether the signed-in user is a Linkcord developer.
func (u *ui) isDev() bool {
	return u.chat != nil && devIDs[u.chat.me.ID]
}
