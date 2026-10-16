package gui

import (
	"time"
	_ "time/tzdata" // Windows has no time zone database Go reads.

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget"
	"github.com/ayn2op/arikawa/v3/discord"
)

// The user can tell Linkcord where someone lives, as Equicord's Timezones
// does, for their profile to say what time it is for them. It is kept on
// this computer only.

// timeZones are the zones offered, by where they are.
var timeZones = []struct{ name, zone string }{
	{"México (Ciudad de México)", "America/Mexico_City"},
	{"Centroamérica (Guatemala)", "America/Guatemala"},
	{"Cuba", "America/Havana"},
	{"República Dominicana / Puerto Rico", "America/Santo_Domingo"},
	{"Colombia / Perú / Ecuador", "America/Bogota"},
	{"Venezuela", "America/Caracas"},
	{"Bolivia", "America/La_Paz"},
	{"Chile", "America/Santiago"},
	{"Paraguay", "America/Asuncion"},
	{"Argentina", "America/Argentina/Buenos_Aires"},
	{"Uruguay", "America/Montevideo"},
	{"Brasil (São Paulo)", "America/Sao_Paulo"},
	{"USA Eastern (New York)", "America/New_York"},
	{"USA Central (Chicago)", "America/Chicago"},
	{"USA Mountain (Denver)", "America/Denver"},
	{"USA Pacific (Los Angeles)", "America/Los_Angeles"},
	{"España", "Europe/Madrid"},
	{"Canarias / Portugal / UK", "Europe/London"},
	{"Europa Central (París, Berlín)", "Europe/Paris"},
	{"Rusia (Moscú)", "Europe/Moscow"},
	{"India", "Asia/Kolkata"},
	{"China", "Asia/Shanghai"},
	{"Japón / Corea", "Asia/Tokyo"},
	{"Australia (Sídney)", "Australia/Sydney"},
	{"UTC", "UTC"},
}

// zoneName is what the zone is called in the list, or the zone itself.
func zoneName(zone string) string {
	for _, z := range timeZones {
		if z.zone == zone {
			return z.name
		}
	}
	return zone
}

// openTimeZoneMenu lets the user choose where id lives, or forget it.
func (c *chatView) openTimeZoneMenu(id discord.UserID) {
	c.openMenu(c.pointerAt, 280, func() []menuItem {
		var items []menuItem
		set := c.u.app.TimeZones[id.String()]
		for _, z := range timeZones {
			items = append(items, menuItem{key: "tz" + z.zone, text: z.name, check: checked(set == z.zone), act: func() {
				if c.u.app.TimeZones == nil {
					c.u.app.TimeZones = make(map[string]string)
				}
				c.u.app.TimeZones[id.String()] = z.zone
				c.u.saveAppSettings()
			}})
		}
		if set != "" {
			items = append(items, menuItem{sep: true}, menuItem{text: "Forget Their Time Zone", col: colorRed, act: func() {
				delete(c.u.app.TimeZones, id.String())
				c.u.saveAppSettings()
			}})
		}
		return items
	})
}

// layoutLocalTime is what time it is for id, where the user said they live,
// or a link to say it.
func (c *chatView) layoutLocalTime(gtx layout.Context, id discord.UserID) layout.Dimensions {
	th := c.u.th
	if c.tzClicks == nil {
		c.tzClicks = make(map[discord.UserID]*widget.Clickable)
	}
	cl := clickOf(c.tzClicks, id)
	if cl.Clicked(gtx) {
		c.openTimeZoneMenu(id)
	}
	zone := c.u.app.TimeZones[id.String()]
	loc, err := time.LoadLocation(zone)
	if zone == "" || err != nil {
		return cl.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			pointer.CursorPointer.Add(gtx.Ops)
			return label(th, 13, colorLink, "Set their time zone").Layout(gtx)
		})
	}
	now := gtx.Now.In(loc)
	// Redrawn when the minute changes.
	gtx.Execute(op.InvalidateCmd{At: gtx.Now.Truncate(time.Minute).Add(time.Minute)})
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(bold(label(th, 12, colorText, "Local time")).Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return cl.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				pointer.CursorPointer.Add(gtx.Ops)
				return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
					layout.Rigid(label(th, 13, colorText, now.Format("15:04")+" · "+zoneName(zone)).Layout),
					layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
					layout.Rigid(label(th, 12, colorLink, "change").Layout),
				)
			})
		}),
	)
}
