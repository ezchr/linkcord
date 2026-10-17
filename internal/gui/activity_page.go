package gui

import (
	"cmp"
	"image"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
)

// The Activity Status page, as Discord's Registered Games: whether games
// show, which of those running or played before stay off the status, and
// programs the user adds as games, which Discord's list lacks.

// customGame is a program the user added as a game: its file name, which
// finds it running, and the name shown.
type customGame struct {
	Exe  string `json:"exe"`
	Name string `json:"name"`
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

// noteSeenGames keeps the games detected, for the user to hide them even
// when they do not run.
func (c *chatView) noteSeenGames() {
	app := &c.u.app
	changed := false
	for _, p := range c.games {
		if strings.HasPrefix(p.game.ID, customPrefix) {
			continue
		}
		if app.SeenGames[p.game.ID] != p.game.Name {
			if app.SeenGames == nil {
				app.SeenGames = make(map[string]string)
			}
			app.SeenGames[p.game.ID] = p.game.Name
			changed = true
		}
	}
	if changed {
		c.u.saveAppSettings()
	}
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
	addBtn  widget.Clickable
	names   map[string]*widget.Editor
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
	if a.addBtn.Clicked(gtx) {
		c.openAddGame()
	}
	app := &c.u.app
	for i := 0; i < len(app.CustomGames); i++ {
		g := app.CustomGames[i]
		if cl := a.removes[g.Exe]; cl != nil && cl.Clicked(gtx) {
			app.CustomGames = slices.Delete(app.CustomGames, i, i+1)
			delete(a.names, g.Exe)
			c.u.saveAppSettings()
			c.applyCustomGames()
			i--
			continue
		}
		if ed := a.names[g.Exe]; ed != nil {
			for {
				ev, ok := ed.Update(gtx)
				if !ok {
					break
				}
				if _, ok := ev.(widget.ChangeEvent); ok {
					app.CustomGames[i].Name = strings.TrimSpace(ed.Text())
					c.u.saveAppSettings()
					c.applyCustomGames()
				}
			}
		}
	}
}

// activityRows are the Activity Status page.
func (c *chatView) activityRows() []layout.Widget {
	th := c.u.th
	g := &c.settings.general
	a := &c.settings.activity
	if a.names == nil {
		a.names = make(map[string]*widget.Editor)
		a.removes = make(map[string]*widget.Clickable)
	}
	if g.activityBtns == nil {
		g.activityBtns = make(map[string]*widget.Clickable)
	}
	toggle := func(act shownActivity) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return switchRow(gtx, th, clickOf(g.activityBtns, act.id), act.name, "", !c.activityHidden(act.id))
			})
		}
	}
	rows := []layout.Widget{
		func(gtx layout.Context) layout.Dimensions {
			return switchRow(gtx, th, &g.gamesBtn, "Show Games You Play",
				"Games show on your status while they run, as Discord's app shows them, from Discord's own list of games, updated daily, and the programs you add below.", !c.u.app.NoGameDetection)
		},
		layout.Spacer{Height: unit.Dp(20)}.Layout,
		settingsHeading(th, "PLAYING NOW"),
		settingsText(th, "Turn one off for it never to show on your status."),
		layout.Spacer{Height: unit.Dp(8)}.Layout,
	}
	// Those running now, and those of before, apart.
	now := make(map[string]bool)
	var current, before []shownActivity
	for _, act := range c.knownActivities() {
		running := slices.ContainsFunc(c.games, func(p playing) bool { return p.game.ID == act.id }) ||
			slices.ContainsFunc(mapValues(c.rpcActivities), func(r rpcActivity) bool { return r.clientID == act.id })
		if running {
			current = append(current, act)
			now[act.id] = true
		}
	}
	for id, name := range c.u.app.SeenGames {
		if !now[id] {
			before = append(before, shownActivity{id, name})
		}
	}
	for id, name := range c.u.app.HiddenActivities {
		if !now[id] && c.u.app.SeenGames[id] == "" && !strings.HasPrefix(id, customPrefix) {
			before = append(before, shownActivity{id, name})
		}
	}
	slices.SortFunc(before, func(x, y shownActivity) int { return strings.Compare(strings.ToLower(x.name), strings.ToLower(y.name)) })
	if len(current) == 0 {
		rows = append(rows, label(th, 14, colorMuted, "No game is running now.").Layout)
	}
	for _, act := range current {
		rows = append(rows, toggle(act))
	}

	rows = append(rows,
		layout.Spacer{Height: unit.Dp(20)}.Layout,
		settingsHeading(th, "ADDED BY YOU"),
		settingsText(th, "Programs Discord does not know as games: while they run, they show on your status with the name you give them."),
		layout.Spacer{Height: unit.Dp(8)}.Layout,
	)
	for _, cg := range c.u.app.CustomGames {
		ed := a.names[cg.Exe]
		if ed == nil {
			ed = &widget.Editor{SingleLine: true}
			ed.SetText(cg.Name)
			a.names[cg.Exe] = ed
		}
		remove := clickOf(a.removes, cg.Exe)
		id := customPrefix + cg.Exe
		rows = append(rows, settingsCard(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return rounded(gtx, colorInput, 6, func(gtx layout.Context) layout.Dimensions {
								gtx.Constraints.Min.X = gtx.Constraints.Max.X
								return layout.UniformInset(unit.Dp(8)).Layout(gtx, materialEditor(th, ed, tr("Name shown")).Layout)
							})
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							sub := cg.Exe
							if c.u.app.HiddenActivities[id] != "" {
								sub += " · " + tr("hidden")
							}
							return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, label(th, 12, colorMuted, sub).Layout)
						}),
					)
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return iconButton(gtx, remove, iconDelete, colorMuted, colorRed)
				}),
			)
		}))
	}
	rows = append(rows, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{}.Layout(gtx, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return button(gtx, th, &a.addBtn, colorSecondaryButton, tr("Add a Program Running"))
		}))
	})

	if len(before) > 0 {
		rows = append(rows,
			layout.Spacer{Height: unit.Dp(20)}.Layout,
			settingsHeading(th, "PLAYED BEFORE"),
			settingsText(th, "Games detected before: those turned off do not show when they run again."),
			layout.Spacer{Height: unit.Dp(8)}.Layout,
		)
		for _, act := range before {
			rows = append(rows, toggle(act))
		}
	}
	return rows
}
