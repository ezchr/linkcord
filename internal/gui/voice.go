package gui

import (
	"gioui.org/layout"
	"github.com/ayn2op/arikawa/v3/discord"
	"golang.org/x/exp/shiny/materialdesign/icons"
)

var iconMicOff = mustIcon(icons.AVMicOff)

// voiceState is the voice call of the window. Voice comes in a later stage.
type voiceState struct{}

func (u *ui) voiceChannel() discord.ChannelID                         { return 0 }
func (u *ui) voiceSpeaking(discord.UserID) bool                       { return false }
func (u *ui) layoutVoicePanel(gtx layout.Context) layout.Dimensions   { return layout.Dimensions{} }
func (u *ui) layoutVoiceButtons(gtx layout.Context) layout.Dimensions { return layout.Dimensions{} }
func (c *chatView) joinVoice(ch discord.Channel)                      {}
