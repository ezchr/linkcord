package gui

import (
	"log/slog"
	"time"

	"gioui.org/app"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// popoutState is the call popped out into a window of its own, as Discord's
// "Pop Out" does. Both windows draw from the same state, so each takes
// u.frameMu while it handles an event: the state is only ever used by one
// of them at a time. The call is drawn in one window only (the main window
// shows where it went), and the menus and pickers the call's buttons open
// show in the window they were opened from.
type popoutState struct {
	win *app.Window
	// drawing is set while the pop-out lays itself out.
	drawing bool
	// overlay reports whether the open menu or stream picker belongs to
	// the pop-out.
	overlay bool
	// closing is set once the pop-out was asked to close.
	closing bool
}

// popped reports whether the call is in its own window.
func (u *ui) popped() bool { return u.popout.win != nil }

// callWindow is the window the call is drawn in.
func (u *ui) callWindow() *app.Window {
	if u.popout.drawing && u.popout.win != nil {
		return u.popout.win
	}
	return u.win
}

// togglePopout pops the call out into its own window, or brings it back.
func (u *ui) togglePopout() {
	if w := u.popout.win; w != nil {
		u.popout.closing = true
		w.Perform(system.ActionClose)
		return
	}
	w := new(app.Window)
	w.Option(
		app.Title(appName+" - Call"),
		app.Size(unit.Dp(960), unit.Dp(600)),
		app.MinSize(unit.Dp(480), unit.Dp(320)),
	)
	u.popout = popoutState{win: w}
	slog.Info("call popped out")
	go u.popoutLoop(w)
	u.win.Invalidate()
}

// popoutLoop runs the pop-out window until it is closed.
func (u *ui) popoutLoop(w *app.Window) {
	// Speaking rings and the streams watched are drawn as they change;
	// the pop-out is redrawn on a timer, faster while a stream is shown.
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(33 * time.Millisecond)
		defer t.Stop()
		n := 0
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				n++
				if u.watchingAny.Load() || n%8 == 0 {
					w.Invalidate()
				}
			}
		}
	}()
	defer close(stop)
	var ops op.Ops
	for {
		e := w.Event()
		u.frameMu.Lock()
		switch e := e.(type) {
		case app.DestroyEvent:
			if u.popout.win == w {
				u.popout = popoutState{}
			}
			slog.Info("call back in the main window")
			u.frameMu.Unlock()
			u.win.Invalidate()
			return
		case app.FrameEvent:
			// Work posted for the main window runs here too: hidden in the
			// notification area, it draws no frames to run it.
			u.runPending()
			gtx := app.NewContext(&ops, e)
			u.applyZoom(&gtx)
			u.layoutPopout(gtx)
			e.Frame(gtx.Ops)
		}
		u.frameMu.Unlock()
	}
}

func (u *ui) layoutPopout(gtx layout.Context) {
	fillMax(gtx, colorChat)
	c := u.chat
	if c == nil || u.screen != screenChat || !c.call.open {
		// The call was closed: so is its window.
		if !u.popout.closing {
			u.popout.closing = true
			u.popout.win.Perform(system.ActionClose)
		}
		return
	}
	u.popout.drawing = true
	defer func() { u.popout.drawing = false }()
	c.trackPointer(gtx)
	c.layoutCall(gtx)
	if u.popout.overlay {
		if u.voice.picker.open {
			c.layoutStreamPicker(gtx)
		}
		c.layoutMenu(gtx)
	}
	c.watchPointer(gtx)
}

// layoutPoppedCall is what the main window shows of a call popped out.
func (c *chatView) layoutPoppedCall(gtx layout.Context) layout.Dimensions {
	th := c.u.th
	if c.call.popBack.Clicked(gtx) {
		c.u.togglePopout()
	}
	return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(label(th, 16, colorMuted, "The call is in its own window").Layout),
			layout.Rigid(layout.Spacer{Height: unit.Dp(12)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return button(gtx, th, &c.call.popBack, colorAccent, "Bring It Back")
			}),
		)
	})
}
