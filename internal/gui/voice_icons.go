package gui

import (
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"github.com/ayn2op/arikawa/v3/discord"
)

// voiceStateIcons are the mute and deafen icons of a voice state, as on
// Discord: gray when the person did it themselves, red when the server did.
// A self-deafened person shows only the headset, which mutes them too.
func voiceStateIcons(gtx layout.Context, vs discord.VoiceState, size unit.Dp) layout.Dimensions {
	type mark struct {
		ic  *widget.Icon
		col colorT
	}
	var marks []mark
	switch {
	case vs.Mute:
		marks = append(marks, mark{iconMicOff, colorRed})
	case vs.SelfMute && !vs.SelfDeaf && !vs.Deaf:
		marks = append(marks, mark{iconMicOff, colorMuted})
	}
	switch {
	case vs.Deaf:
		marks = append(marks, mark{iconDeafened, colorRed})
	case vs.SelfDeaf:
		marks = append(marks, mark{iconDeafened, colorMuted})
	}
	var children []layout.FlexChild
	for i, m := range marks {
		if i > 0 {
			children = append(children, layout.Rigid(layout.Spacer{Width: unit.Dp(2)}.Layout))
		}
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return icon(gtx, m.ic, size, m.col)
		}))
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
}
