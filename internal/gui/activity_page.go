package gui

import (
	"cmp"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"golang.org/x/exp/shiny/materialdesign/icons"
)

// The Activity Status page, as Discord's Registered Games: the game played
// now, and the games played before and the programs the user added, each
// shown on the status or not, with when it was last played.

var iconVerified = mustIcon(icons.ActionCheckCircle)

// customGame is a program the user added as a game: its file name, which
// finds it running, and the name shown.
type customGame struct {
	Exe  string `json:"exe"`
	Name string `json:"name"`
}

// seenGame is a game played, and when last.
type seenGame struct {
	Name string    `json:"name"`
	Last time.Time `json:"last"`
}

// customPrefix starts the IDs of the games the user added, which have no
// application of Discord's.
const customPrefix = "custom:"

// customList finds the programs the user added as games.
func customList(games []customGame) gameList {
	var known []knownGame
	for _, g := range games {
		if g.Exe != "" {
			known = append(known, knownGame{ID: customPrefix + g.Exe, Name: cmp.Or(g.Name, g.Exe), Paths: []string{g.Exe}})
		}
	}
	return indexGames(known)
}

// applyCustomGames gives the detection of games those the user added.
func (c *chatView) applyCustomGames() {
	list := customList(c.u.app.CustomGames)
	c.customGames.Store(&list)
}

// noteSeenGames keeps the games played, and when last: those running now
// and those that stopped, as the games running went from old to now.
func (c *chatView) noteSeenGames(old, now []playing) {
	app := &c.u.app
	if app.SeenGames == nil {
		app.SeenGames = make(map[string]seenGame)
	}
	t := time.Now()
	for _, p := range now {
		app.SeenGames[p.game.ID] = seenGame{Name: p.game.Name, Last: t}
	}
	for _, p := range old {
		if !slices.ContainsFunc(now, func(n playing) bool { return n.game.ID == p.game.ID }) {
			app.SeenGames[p.game.ID] = seenGame{Name: p.game.Name, Last: t}
		}
	}
	c.u.saveAppSettings()
}

// runningPrograms are the programs with a window that could be added as
// games: not Windows' own, Linkcord or those added, by file name.
func (c *chatView) runningPrograms() []string {
	windir := strings.ToLower(os.Getenv("WINDIR"))
	self, _ := os.Executable()
	seen := make(map[string]bool)
	for _, g := range c.customGameExes() {
		seen[g] = true
	}
	var out []string
	for _, p := range windowExecutables() {
		lower := strings.ToLower(p)
		base := filepath.Base(lower)
		if !strings.HasSuffix(base, ".exe") || seen[base] || base == "linkcord.exe" || (windir != "" && strings.HasPrefix(lower, windir)) || strings.EqualFold(p, self) {
			continue
		}
		seen[base] = true
		out = append(out, base)
	}
	slices.Sort(out)
	return out
}

// customGameExes are the file names of the programs added, read from the
// detection's copy, as runningPrograms runs away from drawing.
func (c *chatView) customGameExes() []string {
	var out []string
	if p := c.customGames.Load(); p != nil {
		for exe := range *p {
			out = append(out, exe)
		}
	}
	return out
}

// activityView is the state of the Activity Status page.
type activityView struct {
	addLink widget.Clickable
	// editing is the program added whose name is being changed.
	editing string
	names   map[string]*widget.Editor
	edits   map[string]*widget.Clickable
	removes map[string]*widget.Clickable
}

// openAddGame lists the programs running, to add one as a game; they are
// looked for away from drawing, which a program could hold up.
func (c *chatView) openAddGame() {
	at := c.pointerAt
	go func() {
		progs := c.runningPrograms()
		c.u.post(func() { c.showAddGame(at, progs) })
	}()
}

// showAddGame is the menu of the programs progs running, at at.
func (c *chatView) showAddGame(at image.Point, progs []string) {
	c.openMenu(at, 300, func() []menuItem {
		items := []menuItem{{title: true, text: "PROGRAMS RUNNING"}}
		if len(progs) == 0 {
			items = append(items, menuItem{title: true, text: "No other programs are running"})
		}
		for _, exe := range progs {
			items = append(items, menuItem{key: "add" + exe, text: exe, act: func() {
				name := strings.TrimSuffix(exe, ".exe")
				if name != "" {
					name = strings.ToUpper(name[:1]) + name[1:]
				}
				c.u.app.CustomGames = append(c.u.app.CustomGames, customGame{Exe: exe, Name: name})
				c.u.saveAppSettings()
				c.applyCustomGames()
			}})
		}
		return items
	})
}

// updateActivityPage handles the Activity Status page.
func (c *chatView) updateActivityPage(gtx layout.Context) {
	a := &c.settings.activity
	if a.addLink.Clicked(gtx) {
		c.openAddGame()
	}
	app := &c.u.app
	for i := 0; i < len(app.CustomGames); i++ {
		g := app.CustomGames[i]
		if cl := a.removes[g.Exe]; cl != nil && cl.Clicked(gtx) {
			app.CustomGames = slices.Delete(app.CustomGames, i, i+1)
			delete(a.names, g.Exe)
			delete(app.SeenGames, customPrefix+g.Exe)
			c.u.saveAppSettings()
			c.applyCustomGames()
			i--
			continue
		}
		if cl := a.edits[g.Exe]; cl != nil && cl.Clicked(gtx) {
			if a.editing == g.Exe {
				a.editing = ""
			} else {
				a.editing = g.Exe
				if ed := a.names[g.Exe]; ed != nil {
					gtx.Execute(key.FocusCmd{Tag: ed})
				}
			}
		}
		if ed := a.names[g.Exe]; ed != nil {
			for {
				ev, ok := ed.Update(gtx)
				if !ok {
					break
				}
				switch ev.(type) {
				case widget.ChangeEvent:
					app.CustomGames[i].Name = strings.TrimSpace(ed.Text())
					c.u.saveAppSettings()
					c.applyCustomGames()
				case widget.SubmitEvent:
					a.editing = ""
				}
			}
		}
	}
}

// playedAgo is when t was, as Discord says it: "28 minutes ago".
func playedAgo(t time.Time) string {
	d := time.Since(t)
	es := uiLang == "es"
	unit := func(n int, one, many, uno, muchos string) string {
		if es {
			if n == 1 {
				return "hace " + uno
			}
			return fmt.Sprintf("hace %d %s", n, muchos)
		}
		if n == 1 {
			return one + " ago"
		}
		return fmt.Sprintf("%d %s ago", n, many)
	}
	switch {
	case d < time.Minute:
		if es {
			return "hace un momento"
		}
		return "a moment ago"
	case d < time.Hour:
		return unit(int(d.Minutes()), "a minute", "minutes", "un minuto", "minutos")
	case d < 24*time.Hour:
		return unit(int(d.Hours()), "an hour", "hours", "una hora", "horas")
	case d < 30*24*time.Hour:
		return unit(int(d.Hours()/24), "a day", "days", "un día", "días")
	case d < 365*24*time.Hour:
		return unit(int(d.Hours()/24/30), "a month", "months", "un mes", "meses")
	}
	return unit(int(d.Hours()/24/365), "a year", "years", "un año", "años")
}

// gameRow is a game of the list: its ID, name, when last played, and
// whether the user added it.
type gameRow struct {
	id, name string
	last     time.Time
	custom   string
}

// activityRows are the Activity Status page.
func (c *chatView) activityRows() []layout.Widget {
	th := c.u.th
	g := &c.settings.general
	a := &c.settings.activity
	if a.names == nil {
		a.names = make(map[string]*widget.Editor)
		a.edits = make(map[string]*widget.Clickable)
		a.removes = make(map[string]*widget.Clickable)
	}
	if g.activityBtns == nil {
		g.activityBtns = make(map[string]*widget.Clickable)
	}
	app := &c.u.app
	heading := func(txt string) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(4)}.Layout(gtx, label(th, 22, colorHeader, txt).Layout)
		}
	}

	// The games and programs running now.
	var current []shownActivity
	for _, act := range c.knownActivities() {
		if slices.ContainsFunc(c.games, func(p playing) bool { return p.game.ID == act.id }) ||
			slices.ContainsFunc(mapValues(c.rpcActivities), func(r rpcActivity) bool { return r.clientID == act.id }) {
			current = append(current, act)
		}
	}
	// Games register themselves the first time they run; this turns the
	// detection and the register off whole.
	rows := []layout.Widget{
		func(gtx layout.Context) layout.Dimensions {
			return switchRow(gtx, th, &g.gamesBtn, "Register Games",
				"Games are added to the list below the first time you open them, and show on your status while they run; turn one off below for it not to show. Off, no game is detected nor registered.", !c.u.app.NoGameDetection)
		},
		layout.Spacer{Height: unit.Dp(24)}.Layout,
		heading(tr("Current Game")), layout.Spacer{Height: unit.Dp(12)}.Layout,
	}
	if len(current) == 0 {
		rows = append(rows, settingsCard(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(bold(label(th, 15, colorHeader, "No game detected")).Layout),
				layout.Rigid(layout.Spacer{Height: unit.Dp(4)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
						layout.Rigid(label(th, 13, colorMuted, tr("Not seeing your game?")+" ").Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return a.addLink.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								pointer.CursorPointer.Add(gtx.Ops)
								return label(th, 13, colorLink, "Add it!").Layout(gtx)
							})
						}),
					)
				}),
			)
		}))
	}
	for _, act := range current {
		rows = append(rows, settingsCard(func(gtx layout.Context) layout.Dimensions {
			return switchRow(gtx, th, clickOf(g.activityBtns, act.id), act.name, tr("Now playing!"), !c.activityHidden(act.id))
		}))
	}
	if len(current) > 0 {
		rows = append(rows, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
				layout.Rigid(label(th, 13, colorMuted, tr("Not seeing your game?")+" ").Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return a.addLink.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						pointer.CursorPointer.Add(gtx.Ops)
						return label(th, 13, colorLink, "Add it!").Layout(gtx)
					})
				}),
			)
		})
	}

	// Those played before and those added, the latest first.
	var games []gameRow
	for id, s := range app.SeenGames {
		if !strings.HasPrefix(id, customPrefix) {
			games = append(games, gameRow{id: id, name: s.Name, last: s.Last})
		}
	}
	for _, cg := range app.CustomGames {
		id := customPrefix + cg.Exe
		games = append(games, gameRow{id: id, name: cmp.Or(cg.Name, cg.Exe), last: app.SeenGames[id].Last, custom: cg.Exe})
	}
	slices.SortFunc(games, func(x, y gameRow) int {
		return cmp.Or(y.last.Compare(x.last), strings.Compare(strings.ToLower(x.name), strings.ToLower(y.name)))
	})
	rows = append(rows,
		layout.Spacer{Height: unit.Dp(24)}.Layout,
		func(gtx layout.Context) layout.Dimensions {
			w := gtx.Constraints.Max.X
			paint.FillShape(gtx.Ops, colorSelected, clip.Rect{Max: image.Pt(w, 1)}.Op())
			return layout.Dimensions{Size: image.Pt(w, 1)}
		},
		layout.Spacer{Height: unit.Dp(24)}.Layout,
		heading(tr("Added Games")),
		settingsText(th, "Turn one off for it never to show on your status. Those with a check are games Discord knows; the others, programs you added."),
		layout.Spacer{Height: unit.Dp(12)}.Layout,
	)
	if len(games) == 0 {
		rows = append(rows, label(th, 14, colorMuted, "No games yet: they show here once played.").Layout)
	}
	for _, gr := range games {
		rows = append(rows, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(14)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return c.layoutGameRow(gtx, gr)
			})
		})
	}
	return rows
}

// layoutGameRow is a game of the list: its name, with a check if Discord
// knows it, or buttons to rename or remove it if the user added it, when
// it was last played, and whether it shows on the status.
func (c *chatView) layoutGameRow(gtx layout.Context, gr gameRow) layout.Dimensions {
	th := c.u.th
	g := &c.settings.general
	a := &c.settings.activity
	last := tr("never")
	if !gr.last.IsZero() {
		last = playedAgo(gr.last)
	}
	click := clickOf(g.activityBtns, gr.id)
	if click.Clicked(gtx) {
		c.toggleActivityHidden(gr.id, gr.name)
	}
	name := func(gtx layout.Context) layout.Dimensions {
		if gr.custom != "" && a.editing == gr.custom {
			ed := a.names[gr.custom]
			if ed == nil {
				ed = &widget.Editor{SingleLine: true, Submit: true}
				ed.SetText(gr.name)
				a.names[gr.custom] = ed
			}
			gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(320))
			return rounded(gtx, colorInput, 6, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return layout.UniformInset(unit.Dp(6)).Layout(gtx, materialEditor(th, ed, tr("Name shown")).Layout)
			})
		}
		children := []layout.FlexChild{layout.Rigid(bold(label(th, 16, colorHeader, gr.name)).Layout)}
		if gr.custom == "" {
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return icon(gtx, iconVerified, 16, colorAccent)
				})
			}))
		} else {
			for _, b := range []struct {
				cl *widget.Clickable
				ic *widget.Icon
				hv colorT
			}{{clickOf(a.edits, gr.custom), iconEdit, colorHeader}, {clickOf(a.removes, gr.custom), iconDelete, colorRed}} {
				children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return iconButton(gtx, b.cl, b.ic, colorMuted, b.hv)
					})
				}))
			}
		}
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(name),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
						layout.Rigid(label(th, 13, colorMuted, tr("Last played:")+" ").Layout),
						layout.Rigid(bold(label(th, 13, colorMuted, last)).Layout),
					)
				}),
			)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				pointer.CursorPointer.Add(gtx.Ops)
				return switchToggle(gtx, !c.activityHidden(gr.id))
			})
		}),
	)
}
